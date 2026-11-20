package main

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"sort"
	"sync"
	"time"
)

type neighbor struct {
	IP        string `json:"ip"`
	LLAddr    string `json:"lladdr"`
	IfIndex   int    `json:"ifindex"`
	IfName    string `json:"ifname"`
	State     string `json:"state"`
	IsRouter  int    `json:"is_router"`
	Source    string `json:"source"` // label (or hostname) of the router that reported this entry
	UpdatedAt string `json:"updated_at"`
}

// collectFunc polls one target and returns its neighbors, already restricted
// to the target's interface filter. It must return when ctx is done.
type collectFunc func(ctx context.Context, t Target) ([]neighbor, error)

// targetState is what the collector remembers about one target between polls.
type targetState struct {
	neighbors   []neighbor // result of the last successful poll; nil once stale
	lastSuccess time.Time
	failures    int // consecutive failed polls
}

type pollResult struct {
	neighbors []neighbor
	err       error
}

// collector keeps, per target, the neighbors of its last successful poll and
// serves the merge of all targets. A failed poll leaves a target's neighbors
// in place; after stalePolls consecutive failures they are dropped.
type collector struct {
	ctx           context.Context // cancelled at shutdown; bounds every poll
	collect       collectFunc
	stalePolls    int
	targetTimeout time.Duration

	wg sync.WaitGroup // running refreshes

	mu       sync.RWMutex
	targets  []Target
	states   map[string]*targetState // by Target.key()
	cached   []neighbor              // merged view served to clients
	ready    bool                    // the first refresh has finished
	inflight chan struct{}           // closed when the running refresh ends; nil when idle
	closed   bool
}

func newCollector(ctx context.Context, targets []Target, collect collectFunc, stalePolls int, targetTimeout time.Duration) *collector {
	return &collector{
		ctx:           ctx,
		collect:       collect,
		stalePolls:    stalePolls,
		targetTimeout: targetTimeout,
		targets:       append([]Target(nil), targets...),
		states:        map[string]*targetState{},
		cached:        []neighbor{},
	}
}

// run refreshes once immediately and then on every tick until the
// collector's context is cancelled.
func (c *collector) run(interval time.Duration) {
	c.refresh(c.ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.refresh(c.ctx)
		}
	}
}

// stop waits for a running refresh to end. Cancel the collector's context
// first, otherwise this waits for the poll to complete.
func (c *collector) stop() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.wg.Wait()
}

// refresh polls every target and returns when the poll has finished.
// Refreshes never overlap: a call that arrives while one is running waits
// for that one and returns without starting another. ctx bounds only the
// wait; the poll itself runs under the collector's context.
func (c *collector) refresh(ctx context.Context) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	done := c.inflight
	if done == nil {
		done = make(chan struct{})
		c.inflight = done
		targets := append([]Target(nil), c.targets...)
		c.wg.Add(1)
		go c.runRefresh(targets, done)
	}
	c.mu.Unlock()

	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (c *collector) runRefresh(targets []Target, done chan struct{}) {
	defer c.wg.Done()
	results := c.pollAll(targets)

	c.mu.Lock()
	defer c.mu.Unlock()
	defer close(done)
	c.inflight = nil
	if c.ctx.Err() != nil {
		return // shutting down: the errors are ours, not the routers'
	}
	failed := c.applyLocked(results, time.Now())
	c.ready = true
	log.Printf("snmp: refreshed %d neighbor(s) from %d target(s), %d failed", len(c.cached), len(targets), failed)
}

// pollAll polls the targets in parallel, each under its own deadline.
func (c *collector) pollAll(targets []Target) map[string]pollResult {
	results := make(map[string]pollResult, len(targets))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()
			ns, err := c.pollTarget(t)
			if err != nil && c.ctx.Err() == nil {
				log.Printf("snmp %s: %v", t.Host, err)
			}
			mu.Lock()
			results[t.key()] = pollResult{neighbors: ns, err: err}
			mu.Unlock()
		}(t)
	}
	wg.Wait()
	return results
}

// pollTarget runs the collect function under the per-target deadline. It
// returns at the deadline even if the collect function does not.
func (c *collector) pollTarget(t Target) ([]neighbor, error) {
	ctx, cancel := context.WithTimeout(c.ctx, c.targetTimeout)
	defer cancel()

	ch := make(chan pollResult, 1)
	go func() {
		ns, err := c.collect(ctx, t)
		ch <- pollResult{neighbors: ns, err: err}
	}()
	select {
	case r := <-ch:
		return r.neighbors, r.err
	case <-ctx.Done():
		if c.ctx.Err() == nil {
			return nil, fmt.Errorf("no result within the target timeout of %s", c.targetTimeout)
		}
		return nil, ctx.Err()
	}
}

// applyLocked records the outcome of one poll per target and rebuilds the
// merged view. It returns the number of failed targets. c.mu must be held.
func (c *collector) applyLocked(results map[string]pollResult, now time.Time) int {
	failed := 0
	for key, r := range results {
		st := c.states[key]
		if st == nil {
			st = &targetState{}
			c.states[key] = st
		}
		if r.err == nil {
			st.neighbors = r.neighbors
			if st.neighbors == nil {
				st.neighbors = []neighbor{}
			}
			st.lastSuccess = now
			st.failures = 0
			continue
		}
		failed++
		st.failures++
		if st.failures >= c.stalePolls && st.neighbors != nil {
			log.Printf("snmp %s: %d consecutive failed polls, dropping its %d neighbor(s) (last success: %s)",
				key, st.failures, len(st.neighbors), formatTime(st.lastSuccess))
			st.neighbors = nil
		}
	}
	c.cached = mergeNeighbors(c.targets, c.states)
	return failed
}

// mergeNeighbors builds the served list from every target's current data.
// It writes the source field, marks the routers' own link-locals as
// is_router=1 and keeps one entry per IP (the most recently updated; on a
// tie the earlier target wins). The result is sorted by IP.
func mergeNeighbors(targets []Target, states map[string]*targetState) []neighbor {
	// Set of all routers' own link-locals so neighbors matching them are
	// promoted to is_router=1 regardless of routing table.
	routerSelfLLs := make(map[string]struct{}, len(targets))
	for _, t := range targets {
		if t.SelfLL == "" {
			continue
		}
		ll := t.SelfLL
		if a, err := netip.ParseAddr(ll); err == nil {
			ll = a.WithZone("").String()
		}
		routerSelfLLs[ll] = struct{}{}
	}

	seen := map[string]neighbor{}
	for _, t := range targets {
		st := states[t.key()]
		if st == nil {
			continue
		}
		for _, n := range st.neighbors {
			n.Source = t.source()
			if _, isRouter := routerSelfLLs[n.IP]; isRouter {
				n.IsRouter = 1
			}
			if prev, ok := seen[n.IP]; !ok || n.UpdatedAt > prev.UpdatedAt {
				seen[n.IP] = n
			}
		}
	}
	merged := make([]neighbor, 0, len(seen))
	for _, n := range seen {
		merged = append(merged, n)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].IP < merged[j].IP })
	return merged
}

// setTargetIface updates (or adds) the site-facing interface info for a
// target. The values persist across refreshes; the interface filter takes
// effect at the target's next poll.
func (c *collector) setTargetIface(host, ifname, selfLL, label string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	found := false
	for i, t := range c.targets {
		if t.Host == host {
			c.targets[i].IfName = ifname
			c.targets[i].SelfLL = selfLL
			c.targets[i].Label = label
			found = true
		}
	}
	if !found {
		c.targets = append(c.targets, Target{Host: host, Port: 161, Community: "public", IfName: ifname, SelfLL: selfLL, Label: label})
	}
	c.cached = mergeNeighbors(c.targets, c.states)
}

// neighbors returns the merged view, optionally restricted to one interface
// name, and whether the first refresh has finished.
func (c *collector) neighbors(ifname string) ([]neighbor, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]neighbor, 0, len(c.cached))
	for _, n := range c.cached {
		if ifname == "" || n.IfName == ifname {
			out = append(out, n)
		}
	}
	return out, c.ready
}

func (c *collector) targetHosts() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := make([]string, len(c.targets))
	for i, t := range c.targets {
		names[i] = t.Host
	}
	return names
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.UTC().Format(time.RFC3339)
}
