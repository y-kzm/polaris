package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errPoll = errors.New("poll failed")

// scriptedCollect returns a collectFunc that answers from a per-host
// function of the poll number (1-based).
func scriptedCollect(script map[string]func(poll int) ([]neighbor, error)) collectFunc {
	var mu sync.Mutex
	polls := map[string]int{}
	return func(ctx context.Context, t Target) ([]neighbor, error) {
		mu.Lock()
		polls[t.Host]++
		n := polls[t.Host]
		mu.Unlock()
		return script[t.Host](n)
	}
}

func nb(ip string) neighbor {
	return neighbor{IP: ip, LLAddr: "02:00:00:00:00:01", IfName: "eth1", State: "REACHABLE", UpdatedAt: "2026-11-20T00:00:00Z"}
}

func servedIPs(c *collector) string {
	ns, _ := c.neighbors("")
	ips := make([]string, len(ns))
	for i, n := range ns {
		ips[i] = n.IP
	}
	return strings.Join(ips, " ")
}

func TestCollectorLastGoodAndStale(t *testing.T) {
	a := Target{Host: "a", Port: 161}
	b := Target{Host: "b", Port: 161}
	okB := func(int) ([]neighbor, error) { return []neighbor{nb("fe80::b")}, nil }

	tests := []struct {
		name       string
		stalePolls int
		// a's outcome per poll: true = success with fe80::a.
		aOutcomes []bool
		// served IPs after each poll.
		want []string
	}{
		{
			name:       "one failure keeps the last good data",
			stalePolls: 3,
			aOutcomes:  []bool{true, false},
			want:       []string{"fe80::a fe80::b", "fe80::a fe80::b"},
		},
		{
			name:       "dropped at the third consecutive failure",
			stalePolls: 3,
			aOutcomes:  []bool{true, false, false, false, false},
			want:       []string{"fe80::a fe80::b", "fe80::a fe80::b", "fe80::a fe80::b", "fe80::b", "fe80::b"},
		},
		{
			name:       "a success resets the count",
			stalePolls: 3,
			aOutcomes:  []bool{true, false, false, true, false, false, false},
			want:       []string{"fe80::a fe80::b", "fe80::a fe80::b", "fe80::a fe80::b", "fe80::a fe80::b", "fe80::a fe80::b", "fe80::a fe80::b", "fe80::b"},
		},
		{
			name:       "comes back after being dropped",
			stalePolls: 2,
			aOutcomes:  []bool{true, false, false, true},
			want:       []string{"fe80::a fe80::b", "fe80::a fe80::b", "fe80::b", "fe80::a fe80::b"},
		},
		{
			name:       "stale_polls 1 drops at once",
			stalePolls: 1,
			aOutcomes:  []bool{true, false},
			want:       []string{"fe80::a fe80::b", "fe80::b"},
		},
		{
			name:       "never reachable",
			stalePolls: 3,
			aOutcomes:  []bool{false, false, false, false},
			want:       []string{"fe80::b", "fe80::b", "fe80::b", "fe80::b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collect := scriptedCollect(map[string]func(int) ([]neighbor, error){
				"a": func(poll int) ([]neighbor, error) {
					if tt.aOutcomes[poll-1] {
						return []neighbor{nb("fe80::a")}, nil
					}
					return nil, errPoll
				},
				"b": okB,
			})
			c := newCollector(context.Background(), []Target{a, b}, collect, tt.stalePolls, time.Second)
			for i, want := range tt.want {
				c.refresh(context.Background())
				if got := servedIPs(c); got != want {
					t.Fatalf("after poll %d: served %q, want %q", i+1, got, want)
				}
			}
		})
	}
}

func TestCollectorOnlyTargetFailing(t *testing.T) {
	collect := scriptedCollect(map[string]func(int) ([]neighbor, error){
		"a": func(poll int) ([]neighbor, error) {
			if poll == 1 {
				return []neighbor{nb("fe80::1"), nb("fe80::2")}, nil
			}
			return nil, errPoll
		},
	})
	c := newCollector(context.Background(), []Target{{Host: "a", Port: 161}}, collect, 3, time.Second)
	c.refresh(context.Background())
	c.refresh(context.Background())
	if got, want := servedIPs(c), "fe80::1 fe80::2"; got != want {
		t.Fatalf("after a failed poll of the only target: served %q, want %q", got, want)
	}
	st := c.states["a:161"]
	if st.failures != 1 || st.lastSuccess.IsZero() {
		t.Fatalf("state: failures=%d lastSuccess=%v, want 1 and a time", st.failures, st.lastSuccess)
	}
}

// A target that never answers must not delay the others beyond its deadline.
func TestCollectorSlowTargetDoesNotBlockOthers(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	collect := func(ctx context.Context, tg Target) ([]neighbor, error) {
		if tg.Host == "dead" {
			<-release // ignores ctx on purpose: the collector must enforce the deadline itself
			return nil, errPoll
		}
		return []neighbor{nb("fe80::b")}, nil
	}
	c := newCollector(context.Background(), []Target{{Host: "dead", Port: 161}, {Host: "b", Port: 161}}, collect, 3, 100*time.Millisecond)

	start := time.Now()
	c.refresh(context.Background())
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("refresh took %s with a 100ms target deadline", elapsed)
	}
	if got, want := servedIPs(c), "fe80::b"; got != want {
		t.Fatalf("served %q, want %q", got, want)
	}
	if st := c.states["dead:161"]; st == nil || st.failures != 1 {
		t.Fatalf("dead target state: %+v, want 1 failure", st)
	}
}

func TestCollectorPollsTargetsInParallel(t *testing.T) {
	const n = 4
	var started sync.WaitGroup
	started.Add(n)
	allStarted := make(chan struct{})
	go func() { started.Wait(); close(allStarted) }()

	// Every collect waits until all of them have started: this only
	// terminates in time if the targets are polled concurrently.
	collect := func(ctx context.Context, tg Target) ([]neighbor, error) {
		started.Done()
		select {
		case <-allStarted:
			return []neighbor{nb("fe80::" + tg.Host)}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	targets := []Target{{Host: "1", Port: 161}, {Host: "2", Port: 161}, {Host: "3", Port: 161}, {Host: "4", Port: 161}}
	c := newCollector(context.Background(), targets, collect, 3, 5*time.Second)
	c.refresh(context.Background())
	if got, want := servedIPs(c), "fe80::1 fe80::2 fe80::3 fe80::4"; got != want {
		t.Fatalf("served %q, want %q", got, want)
	}
}

// A refresh that arrives while one is running waits for it and does not
// start another poll.
func TestCollectorRefreshJoinsRunningRefresh(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	collect := func(ctx context.Context, tg Target) ([]neighbor, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		return []neighbor{nb("fe80::a")}, nil
	}
	c := newCollector(context.Background(), []Target{{Host: "a", Port: 161}}, collect, 3, 10*time.Second)

	const callers = 5
	var wg sync.WaitGroup
	returned := make(chan struct{}, callers)
	wg.Add(1)
	go func() { defer wg.Done(); c.refresh(context.Background()); returned <- struct{}{} }()
	<-entered // the first refresh is now polling

	for range callers - 1 {
		wg.Add(1)
		go func() { defer wg.Done(); c.refresh(context.Background()); returned <- struct{}{} }()
	}
	select {
	case <-returned:
		t.Fatal("a refresh returned while the poll was still running")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("collect ran %d times for %d overlapping refreshes, want 1", got, callers)
	}

	// The next refresh, with none running, polls again.
	c.refresh(context.Background())
	if got := calls.Load(); got != 2 {
		t.Fatalf("collect ran %d times after a later refresh, want 2", got)
	}
}

func TestCollectorRefreshWaitHonoursCallerContext(t *testing.T) {
	release := make(chan struct{})
	collect := func(ctx context.Context, tg Target) ([]neighbor, error) {
		<-release
		return nil, nil
	}
	c := newCollector(context.Background(), []Target{{Host: "a", Port: 161}}, collect, 3, 10*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	c.refresh(ctx) // returns when the caller gives up; the poll goes on
	if _, ready := c.neighbors(""); ready {
		t.Fatal("collector is ready although the first poll has not finished")
	}
	close(release)
	c.stop()
	if _, ready := c.neighbors(""); !ready {
		t.Fatal("collector is not ready after the poll finished")
	}
}

func TestMergeNeighbors(t *testing.T) {
	targets := []Target{
		{Host: "r1", Port: 161, Label: "router-a", SelfLL: "FE80::A"},
		{Host: "r2", Port: 161, SelfLL: "fe80::b"},
		{Host: "r3", Port: 161},
	}
	older := nb("fe80::1")
	newer := nb("fe80::1")
	newer.UpdatedAt = "2026-11-20T00:00:30Z"
	newer.LLAddr = "02:00:00:00:00:99"
	states := map[string]*targetState{
		"r1:161": {neighbors: []neighbor{older, nb("fe80::b"), nb("fe80::2")}},
		"r2:161": {neighbors: []neighbor{newer, nb("fe80::a"), nb("fe80::2")}},
		// r3 has no state yet.
		"gone:161": {neighbors: []neighbor{nb("fe80::dead")}}, // not a target any more
	}
	got := mergeNeighbors(targets, states)

	type row struct {
		ip, source, lladdr string
		isRouter           int
	}
	want := []row{
		{"fe80::1", "r2", "02:00:00:00:00:99", 0},       // newer entry wins
		{"fe80::2", "router-a", "02:00:00:00:00:01", 0}, // tie: earlier target wins
		{"fe80::a", "r2", "02:00:00:00:00:01", 1},       // r1's own link-local, case-insensitive
		{"fe80::b", "router-a", "02:00:00:00:00:01", 1}, // r2's own link-local
	}
	if len(got) != len(want) {
		t.Fatalf("got %d neighbors %+v, want %d", len(got), got, len(want))
	}
	for i, w := range want {
		g := row{got[i].IP, got[i].Source, got[i].LLAddr, got[i].IsRouter}
		if g != w {
			t.Errorf("row %d: got %+v, want %+v", i, g, w)
		}
	}
	if states["r1:161"].neighbors[0].Source != "" {
		t.Error("mergeNeighbors modified the cached per-target data")
	}
}

func TestSetTargetIfaceKeptAcrossRefreshes(t *testing.T) {
	var seen []Target
	var mu sync.Mutex
	collect := func(ctx context.Context, tg Target) ([]neighbor, error) {
		mu.Lock()
		seen = append(seen, tg)
		mu.Unlock()
		return []neighbor{nb("fe80::1")}, nil
	}
	c := newCollector(context.Background(), []Target{{Host: "r1", Port: 161, Community: "c"}}, collect, 3, time.Second)
	c.refresh(context.Background())
	c.setTargetIface("r1", "eth1", "fe80::1", "router-a")
	if ns, _ := c.neighbors(""); ns[0].Source != "router-a" || ns[0].IsRouter != 1 {
		t.Fatalf("label and self_ll not applied to the served data: %+v", ns[0])
	}
	c.refresh(context.Background())
	c.refresh(context.Background())
	want := Target{Host: "r1", Port: 161, Community: "c", IfName: "eth1", SelfLL: "fe80::1", Label: "router-a"}
	if seen[1] != want || seen[2] != want {
		t.Fatalf("targets polled after registration: %+v, want %+v", seen[1:], want)
	}
	c.setTargetIface("r9", "eth2", "", "")
	if hosts := c.targetHosts(); len(hosts) != 2 || hosts[1] != "r9" {
		t.Fatalf("unknown host was not added: %v", hosts)
	}
}

func TestHTTP(t *testing.T) {
	release := make(chan struct{})
	collect := func(ctx context.Context, tg Target) ([]neighbor, error) {
		<-release
		n := nb("fe80::1")
		return []neighbor{n}, nil
	}
	newServer := func(allow bool) (*collector, *httptest.Server) {
		c := newCollector(context.Background(), []Target{{Host: "r1", Port: 161}}, collect, 3, 5*time.Second)
		srv := httptest.NewServer(newMux(c, allow))
		t.Cleanup(srv.Close)
		return c, srv
	}
	do := func(method, url, body string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(method, url, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return resp.StatusCode, strings.TrimSpace(sb.String())
	}

	_, srv := newServer(false)

	if code, body := do("GET", srv.URL+"/health", ""); code != 200 || body != `{"status":"ok"}` {
		t.Fatalf("GET /health: %d %s", code, body)
	}
	if code, _ := do("GET", srv.URL+"/api/neighbors", ""); code != http.StatusServiceUnavailable {
		t.Fatalf("GET /api/neighbors before the first poll: %d, want 503", code)
	}
	if code, _ := do("PUT", srv.URL+"/api/targets/r1", `{"ifname":"eth1"}`); code != http.StatusForbidden {
		t.Fatalf("PUT /api/targets without ALLOW_TARGET_REGISTRATION: %d, want 403", code)
	}

	close(release)
	if code, _ := do("POST", srv.URL+"/api/neighbors/refresh", ""); code != http.StatusNoContent {
		t.Fatalf("POST /api/neighbors/refresh: %d, want 204", code)
	}
	code, body := do("GET", srv.URL+"/api/neighbors", "")
	if code != 200 {
		t.Fatalf("GET /api/neighbors: %d %s", code, body)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil || len(got) != 1 {
		t.Fatalf("GET /api/neighbors body: %s (%v)", body, err)
	}
	for _, k := range []string{"ip", "lladdr", "ifindex", "ifname", "state", "is_router", "source", "updated_at"} {
		if _, ok := got[0][k]; !ok {
			t.Errorf("field %q missing in %s", k, body)
		}
	}
	if code, body := do("GET", srv.URL+"/api/neighbors?ifname=other", ""); code != 200 || body != "[]" {
		t.Fatalf("GET /api/neighbors?ifname=other: %d %s, want 200 []", code, body)
	}

	c2, srv2 := newServer(true)
	if code, _ := do("PUT", srv2.URL+"/api/targets/r1", `{"ifname":"eth1","self_ll":"fe80::a","label":"router-a"}`); code != http.StatusNoContent {
		t.Fatalf("PUT /api/targets with registration allowed: %d, want 204", code)
	}
	if got := c2.targets[0]; got.IfName != "eth1" || got.SelfLL != "fe80::a" || got.Label != "router-a" {
		t.Fatalf("target after PUT: %+v", got)
	}
	if code, _ := do("PUT", srv2.URL+"/api/targets/r1", `{"label":"x"}`); code != http.StatusBadRequest {
		t.Fatalf("PUT without ifname: %d, want 400", code)
	}
	big := `{"ifname":"` + strings.Repeat("x", maxBodyBytes) + `"}`
	if code, _ := do("PUT", srv2.URL+"/api/targets/r1", big); code != http.StatusBadRequest {
		t.Fatalf("PUT with a body over the limit: %d, want 400", code)
	}
}
