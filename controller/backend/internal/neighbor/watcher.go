// Package neighbor polls the neighbor sources and keeps the local neighbor
// table in sync.
//
// A neighbor source is any HTTP server that implements contract C2 of
// docs/design.md. The watcher knows nothing about how a source finds its
// hosts, and passes it no filter: which interfaces count is the source's
// business.
package neighbor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"controller/backend/internal/store"
)

// pollTimeout caps how long Poll waits for the sources to refresh their data.
const pollTimeout = 90 * time.Second

// RemoteNeighbor is one entry of a source's GET /api/neighbors answer.
type RemoteNeighbor struct {
	IP        string `json:"ip"`
	LLAddr    string `json:"lladdr"`
	IfIndex   int    `json:"ifindex"`
	IfName    string `json:"ifname"`
	State     string `json:"state"`
	IsRouter  int    `json:"is_router"`
	Source    string `json:"source"`
	UpdatedAt string `json:"updated_at"`
}

// Source is one configured neighbor source.
type Source struct {
	Name string
	URL  string // of its GET /api/neighbors
}

// SourceStatus reports the health of a source.
type SourceStatus struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	OK          bool   `json:"ok"`
	Error       string `json:"error"`
	LastSuccess string `json:"last_success"`
	// Count is the number of hosts currently taken from this source,
	// including the ones kept from its last successful fetch.
	Count int `json:"count"`
}

type sourceState struct {
	neighbors   []store.Neighbor
	lastSuccess time.Time
	err         string
}

type Watcher struct {
	sources    []Source
	staleAfter time.Duration
	store      *store.NeighborStore
	client     *http.Client

	mu    sync.Mutex
	state map[string]*sourceState // by URL
}

// NewWatcher creates a watcher. A source that keeps failing keeps the hosts
// of its last successful fetch for staleAfter; after that they are dropped.
func NewWatcher(sources []Source, staleAfter time.Duration, ns *store.NeighborStore) *Watcher {
	state := make(map[string]*sourceState, len(sources))
	for _, s := range sources {
		state[s.URL] = &sourceState{}
	}
	return &Watcher{
		sources:    sources,
		staleAfter: staleAfter,
		store:      ns,
		client:     &http.Client{Timeout: 5 * time.Second},
		state:      state,
	}
}

func (w *Watcher) fetchOne(ctx context.Context, apiURL string) ([]store.Neighbor, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var remotes []RemoteNeighbor
	if err := json.NewDecoder(resp.Body).Decode(&remotes); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	neighbors := make([]store.Neighbor, 0, len(remotes))
	for _, r := range remotes {
		// Only link-local unicast addresses: the controller sends unicast
		// RAs to them, and a global address of the same host would be a
		// second entry for it.
		ip := net.ParseIP(r.IP)
		if ip == nil || !ip.IsLinkLocalUnicast() {
			continue
		}
		updatedAt := r.UpdatedAt
		if updatedAt == "" {
			updatedAt = time.Now().UTC().Format(time.RFC3339)
		}
		neighbors = append(neighbors, store.Neighbor{
			ID:        ip.String(),
			LLAddr:    r.LLAddr,
			IfIndex:   r.IfIndex,
			IfName:    r.IfName,
			State:     r.State,
			IsRouter:  r.IsRouter,
			Source:    r.Source,
			UpdatedAt: updatedAt,
		})
	}
	return neighbors, nil
}

// Fetch pulls the neighbors of every source and stores their union. A source
// that fails contributes the hosts of its last successful fetch until they
// are stale, so one failed fetch does not empty the table.
func (w *Watcher) Fetch(ctx context.Context) error {
	type outcome struct {
		neighbors []store.Neighbor
		err       error
	}
	outcomes := make([]outcome, len(w.sources))
	var wg sync.WaitGroup
	for i, src := range w.sources {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			outcomes[i].neighbors, outcomes[i].err = w.fetchOne(ctx, url)
		}(i, src.URL)
	}
	wg.Wait()

	now := time.Now()
	w.mu.Lock()
	seen := map[string]store.Neighbor{}
	for i, src := range w.sources {
		st := w.state[src.URL]
		if err := outcomes[i].err; err != nil {
			if st.err != err.Error() {
				log.Printf("neighbor source %s: %v", src.Name, err)
			}
			st.err = err.Error()
			if len(st.neighbors) > 0 && now.Sub(st.lastSuccess) > w.staleAfter {
				log.Printf("neighbor source %s: no answer for %s; dropping its %d host(s)",
					src.Name, w.staleAfter, len(st.neighbors))
				st.neighbors = nil
			}
		} else {
			st.neighbors, st.lastSuccess, st.err = outcomes[i].neighbors, now, ""
		}
		for _, n := range st.neighbors {
			if prev, ok := seen[n.ID]; !ok || n.UpdatedAt > prev.UpdatedAt {
				seen[n.ID] = n
			}
		}
	}
	w.mu.Unlock()

	merged := make([]store.Neighbor, 0, len(seen))
	for _, n := range seen {
		merged = append(merged, n)
	}
	return w.store.Replace(merged)
}

// Statuses reports the health of every source.
func (w *Watcher) Statuses() []SourceStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]SourceStatus, 0, len(w.sources))
	for _, src := range w.sources {
		st := w.state[src.URL]
		status := SourceStatus{Name: src.Name, URL: src.URL, OK: st.err == "" && !st.lastSuccess.IsZero(),
			Error: st.err, Count: len(st.neighbors)}
		if !st.lastSuccess.IsZero() {
			status.LastSuccess = st.lastSuccess.UTC().Format(time.RFC3339)
		}
		out = append(out, status)
	}
	return out
}

// Poll asks every source to refresh its data now (POST <url>/refresh), then
// fetches the results. Sources without that endpoint are skipped.
func (w *Watcher) Poll(ctx context.Context) error {
	pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()

	// A refresh may involve SNMP walks; the 5 s fetch client is too short.
	pollClient := &http.Client{Timeout: pollTimeout}

	var wg sync.WaitGroup
	for _, src := range w.sources {
		wg.Add(1)
		go func(src Source) {
			defer wg.Done()
			req, err := http.NewRequestWithContext(pollCtx, http.MethodPost, src.URL+"/refresh", nil)
			if err != nil {
				log.Printf("neighbor source %s: build refresh request: %v", src.Name, err)
				return
			}
			resp, err := pollClient.Do(req)
			if err != nil {
				log.Printf("neighbor source %s: refresh: %v", src.Name, err)
				return
			}
			resp.Body.Close()
		}(src)
	}
	wg.Wait()

	return w.Fetch(ctx)
}

func (w *Watcher) Run(ctx context.Context, interval time.Duration) {
	log.Printf("neighbor watcher: %d source(s), every %s, stale after %s", len(w.sources), interval, w.staleAfter)
	if err := w.Fetch(ctx); err != nil {
		log.Printf("neighbor watcher: %v", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.Fetch(ctx); err != nil {
				log.Printf("neighbor watcher: %v", err)
			}
		}
	}
}
