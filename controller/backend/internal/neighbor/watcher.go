// Package neighbor polls remote neighbor-collector APIs and keeps the local
// neighbor table in sync. Each collector exposes a simple JSON endpoint;
// Watcher merges results from all configured sources on every tick.
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

// pollTimeout caps how long Poll waits for collectors to finish their SNMP walk.
// SNMP with retries can be slow, so this is set well above the per-target timeout.
const pollTimeout = 90 * time.Second

// RemoteNeighbor is the shape returned by ipv6-neigh-watcher.
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

type Watcher struct {
	apiURLs []string
	ifname  string
	store   *store.NeighborStore
	client  *http.Client
}

func NewWatcher(apiURLs []string, ifname string, ns *store.NeighborStore) *Watcher {
	return &Watcher{
		apiURLs: apiURLs,
		ifname:  ifname,
		store:   ns,
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

func (w *Watcher) fetchOne(ctx context.Context, apiURL string) ([]store.Neighbor, error) {
	url := fmt.Sprintf("%s?ifname=%s", apiURL, w.ifname)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", apiURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", apiURL, resp.StatusCode)
	}
	var remotes []RemoteNeighbor
	if err := json.NewDecoder(resp.Body).Decode(&remotes); err != nil {
		return nil, fmt.Errorf("decode %s: %w", apiURL, err)
	}
	neighbors := make([]store.Neighbor, 0, len(remotes))
	for _, r := range remotes {
		if r.IP == "" {
			continue
		}
		// Only keep link-local unicast addresses (fe80::/10).
		// The controller sends unicast RAs to these addresses, and GUA entries
		// from the same host would create duplicates with wrong IDs.
		if ip := net.ParseIP(r.IP); ip == nil || !ip.IsLinkLocalUnicast() {
			continue
		}
		updatedAt := r.UpdatedAt
		if updatedAt == "" {
			updatedAt = time.Now().UTC().Format(time.RFC3339)
		}
		neighbors = append(neighbors, store.Neighbor{
			ID:        r.IP,
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

// Fetch pulls neighbors from all configured sources and stores the deduplicated union.
// Deduplication key is IP address; when the same IP appears in multiple sources the
// entry with the newer UpdatedAt is kept.
func (w *Watcher) Fetch(ctx context.Context) error {
	seen := map[string]store.Neighbor{}
	for _, url := range w.apiURLs {
		neighbors, err := w.fetchOne(ctx, url)
		if err != nil {
			log.Printf("neighbor watcher: %v", err)
			continue
		}
		for _, n := range neighbors {
			if prev, ok := seen[n.ID]; !ok || n.UpdatedAt > prev.UpdatedAt {
				seen[n.ID] = n
			}
		}
	}
	merged := make([]store.Neighbor, 0, len(seen))
	for _, n := range seen {
		merged = append(merged, n)
	}
	return w.store.Replace(merged)
}

// Poll asks every collector to run an immediate SNMP poll (POST /api/neighbors/refresh),
// then fetches the updated results into the local store.
// Collectors that do not support the endpoint are skipped gracefully.
func (w *Watcher) Poll(ctx context.Context) error {
	pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()

	// Use a dedicated client with a long timeout; the regular fetch client (5s) is too short for SNMP.
	pollClient := &http.Client{Timeout: pollTimeout}

	var wg sync.WaitGroup
	for _, apiURL := range w.apiURLs {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			req, err := http.NewRequestWithContext(pollCtx, http.MethodPost, url+"/refresh", nil)
			if err != nil {
				log.Printf("neighbor watcher: build poll request %s: %v", url, err)
				return
			}
			resp, err := pollClient.Do(req)
			if err != nil {
				log.Printf("neighbor watcher: poll %s: %v", url, err)
				return
			}
			resp.Body.Close()
		}(apiURL)
	}
	wg.Wait()

	return w.Fetch(ctx)
}

func (w *Watcher) Run(ctx context.Context, interval time.Duration) {
	log.Printf("neighbor watcher: starting (urls=%v ifname=%s interval=%s)", w.apiURLs, w.ifname, interval)
	w.Fetch(ctx) // immediate first fetch
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
