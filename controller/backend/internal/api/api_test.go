package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	gorav1 "controller/backend/api/gora/v1"
	"controller/backend/internal/engine"
	"controller/backend/internal/fqdn"
	"controller/backend/internal/store"
)

// fakeAgent is an in-memory go-ra agent with a baseline configuration.
type fakeAgent struct {
	configs map[int32]*gorav1.InterfaceConfig
}

func newFakeAgent() *fakeAgent {
	return &fakeAgent{configs: map[int32]*gorav1.InterfaceConfig{
		0: {Id: 0, Name: "eth0", Preference: "medium", RouterLifetimeSeconds: 1800, SourceAddress: "fe80::b:a5e"},
	}}
}

func (a *fakeAgent) ListInterfaces(context.Context) ([]*gorav1.InterfaceConfig, error) {
	out := make([]*gorav1.InterfaceConfig, 0, len(a.configs))
	for _, c := range a.configs {
		out = append(out, c)
	}
	return out, nil
}
func (a *fakeAgent) AddInterface(_ context.Context, c *gorav1.InterfaceConfig) error {
	a.configs[c.Id] = c
	return nil
}
func (a *fakeAgent) UpdateInterface(_ context.Context, c *gorav1.InterfaceConfig) error {
	a.configs[c.Id] = c
	return nil
}
func (a *fakeAgent) DeleteInterface(_ context.Context, id int32, _ bool) error {
	delete(a.configs, id)
	return nil
}
func (a *fakeAgent) SendRA(context.Context, *gorav1.InterfaceConfig, []string) error { return nil }
func (a *fakeAgent) Close()                                                          {}

func (a *fakeAgent) managed() int {
	n := 0
	for _, c := range a.configs {
		if c.Description == engine.ManagedDescription {
			n++
		}
	}
	return n
}

// site is a controller with fake routers behind it.
type site struct {
	t      *testing.T
	h      *Handler
	srv    *httptest.Server
	mu     sync.Mutex
	agents map[string]*fakeAgent // by router address
	down   map[string]bool
}

func newSite(t *testing.T, fqdnClient *fqdn.Client) *site {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := &site{t: t, agents: map[string]*fakeAgent{}, down: map[string]bool{}}
	s.h = New(Deps{
		Routers: store.NewRouterStore(db), Rules: store.NewRuleStore(db), Groups: store.NewGroupStore(db),
		Neighbors: store.NewNeighborStore(db), Policy: store.NewPolicyStore(db), FQDN: fqdnClient,
		Dial: func(r *store.Router) (engine.Agent, error) {
			s.mu.Lock() // the engine dials the routers in parallel
			defer s.mu.Unlock()
			if s.down[r.Address] {
				return nil, errors.New("unreachable")
			}
			if s.agents[r.Address] == nil {
				s.agents[r.Address] = newFakeAgent()
			}
			return s.agents[r.Address], nil
		},
	})
	s.srv = httptest.NewServer(s.h.Router())
	t.Cleanup(func() { s.srv.Close(); db.Close() })
	return s
}

// call sends a request and decodes the JSON answer into out (if not nil).
func (s *site) call(method, path string, body, out any) int {
	s.t.Helper()
	var reader *bytes.Reader = bytes.NewReader(nil)
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, s.srv.URL+"/api"+path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		// An empty body (204) leaves out untouched.
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
			s.t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

func (s *site) mustCall(want int, method, path string, body, out any) {
	s.t.Helper()
	var raw json.RawMessage
	if got := s.call(method, path, body, &raw); got != want {
		s.t.Fatalf("%s %s: HTTP %d, want %d: %s", method, path, got, want, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			s.t.Fatal(err)
		}
	}
}

func (s *site) addRouter(name, address string) store.Router {
	s.t.Helper()
	var r store.Router
	s.mustCall(http.StatusCreated, "POST", "/routers", map[string]any{
		"name": name, "address": address, "interface": "eth0", "policy_source_address": "fe80::face:" + name,
	}, &r)
	return r
}

type planResponse struct {
	Pending  bool                  `json:"pending"`
	Warnings []string              `json:"warnings"`
	Routers  []engine.RouterResult `json:"routers"`
}

// policy creates one rule (default route via the router) and one group.
func (s *site) policy(router store.Router) (store.Rule, store.Group) {
	s.t.Helper()
	var rule store.Rule
	s.mustCall(http.StatusCreated, "POST", "/rules", map[string]any{
		"nexthop": router.Address, "entries": []map[string]string{{"type": "ipv6", "value": "::/0"}},
	}, &rule)
	var group store.Group
	s.mustCall(http.StatusCreated, "POST", "/groups", map[string]any{
		"name": "G", "members": []string{"FE80::0001", "fe80::1", "fe80::2"}, "rules": []int{rule.ID},
	}, &group)
	return rule, group
}

func TestValidation(t *testing.T) {
	s := newSite(t, nil)
	a := s.addRouter("a", "2001:db8::a")
	rule, group := s.policy(a)

	if got := group.Members; len(got) != 2 || got[0] != "fe80::1" {
		t.Errorf("members were not canonicalised and deduplicated: %v", got)
	}
	for name, tc := range map[string]struct {
		method, path string
		body         any
	}{
		"duplicate router address": {"POST", "/routers", map[string]any{"name": "a2", "address": "2001:DB8:0::a"}},
		"duplicate router name":    {"POST", "/routers", map[string]any{"name": "a", "address": "2001:db8::9"}},
		"bad policy address":       {"POST", "/routers", map[string]any{"name": "z", "address": "x", "policy_source_address": "2001:db8::1"}},
		"bad status":               {"POST", "/routers", map[string]any{"name": "z", "address": "x", "status": "paused"}},
		"unknown next hop":         {"POST", "/rules", map[string]any{"nexthop": "2001:db8::ff"}},
		"bad prefix":               {"POST", "/rules", map[string]any{"nexthop": a.Address, "entries": []map[string]string{{"type": "ipv6", "value": "nope"}}}},
		"fqdn entry":               {"POST", "/rules", map[string]any{"nexthop": a.Address, "entries": []map[string]string{{"type": "fqdn", "value": "example.com"}}}},
		"global member":            {"POST", "/groups", map[string]any{"name": "H", "members": []string{"2001:db8::1"}}},
		"missing rule":             {"PUT", "/groups/" + itoa(group.ID) + "/rules", map[string]any{"rules": []int{rule.ID, 999}}},
	} {
		var e struct{ Error string }
		if got := s.call(tc.method, tc.path, tc.body, &e); got != http.StatusBadRequest || e.Error == "" {
			t.Errorf("%s: HTTP %d, error %q; want 400 with a message", name, got, e.Error)
		}
	}
}

func TestDeleteRuleRemovesItFromGroups(t *testing.T) {
	s := newSite(t, nil)
	rule, group := s.policy(s.addRouter("a", "2001:db8::a"))

	s.mustCall(http.StatusNoContent, "DELETE", "/rules/"+itoa(rule.ID), nil, nil)

	var groups []store.Group
	s.mustCall(http.StatusOK, "GET", "/groups", nil, &groups)
	if len(groups) != 1 || groups[0].ID != group.ID || len(groups[0].Rules) != 0 {
		t.Fatalf("groups after the rule was deleted: %+v", groups)
	}
}

func TestPlanApplyAndReconcile(t *testing.T) {
	s := newSite(t, nil)
	a := s.addRouter("a", "2001:db8::a")
	s.policy(a)

	var plan planResponse
	s.mustCall(http.StatusOK, "GET", "/policy/plan", nil, &plan)
	if !plan.Pending || len(plan.Routers) != 1 || plan.Routers[0].Added != 1 || len(plan.Routers[0].Messages) != 1 {
		t.Fatalf("plan before the apply: %+v", plan)
	}
	if msg := plan.Routers[0].Messages[0]; msg.Preference != "high" || msg.SourceAddress != "fe80::face:a" || len(msg.Clients) != 2 {
		t.Errorf("planned message: %+v", msg)
	}
	if s.agents[a.Address].managed() != 0 {
		t.Fatal("the plan changed the router")
	}

	var applied struct {
		Success   bool   `json:"success"`
		AppliedAt string `json:"applied_at"`
	}
	s.mustCall(http.StatusOK, "POST", "/policy/apply", nil, &applied)
	if !applied.Success || applied.AppliedAt == "" || s.agents[a.Address].managed() != 1 {
		t.Fatalf("apply: %+v, managed=%d", applied, s.agents[a.Address].managed())
	}
	s.mustCall(http.StatusOK, "GET", "/policy/plan", nil, &plan)
	if plan.Pending {
		t.Errorf("plan right after the apply is still pending: %+v", plan)
	}

	// The agent restarts and loses the policy; the reconciler restores it.
	s.agents[a.Address] = newFakeAgent()
	// A draft edit that was not applied must not be pushed by the reconciler.
	var groups []store.Group
	s.mustCall(http.StatusOK, "GET", "/groups", nil, &groups)
	s.mustCall(http.StatusOK, "PUT", "/groups/"+itoa(groups[0].ID), map[string]any{
		"name": "G", "members": []string{"fe80::1", "fe80::2", "fe80::3"}, "rules": groups[0].Rules,
	}, nil)

	if problem := s.h.reconcileOnce(context.Background()); problem != "" {
		t.Fatalf("reconcile: %s", problem)
	}
	agent := s.agents[a.Address]
	if agent.managed() != 1 {
		t.Fatalf("the reconciler did not restore the policy: %d managed config(s)", agent.managed())
	}
	for _, c := range agent.configs {
		if c.Description == engine.ManagedDescription && len(c.Clients) != 2 {
			t.Errorf("the reconciler pushed the unapplied edit: clients %v", c.Clients)
		}
	}
	s.mustCall(http.StatusOK, "GET", "/policy/plan", nil, &plan)
	if !plan.Pending {
		t.Error("an unapplied edit must show as pending")
	}
}

func TestDeleteRouterDecommissions(t *testing.T) {
	s := newSite(t, nil)
	a := s.addRouter("a", "2001:db8::a")
	rule, _ := s.policy(a)
	s.mustCall(http.StatusOK, "POST", "/policy/apply", nil, nil)

	var e struct{ Error string }
	if got := s.call("DELETE", "/routers/"+a.ID, nil, &e); got != http.StatusConflict || !strings.Contains(e.Error, "next hop of rule") {
		t.Fatalf("delete while a rule points to the router: HTTP %d %q", got, e.Error)
	}
	s.mustCall(http.StatusNoContent, "DELETE", "/rules/"+itoa(rule.ID), nil, nil)
	// The applied policy still uses the router until the change is applied.
	if got := s.call("DELETE", "/routers/"+a.ID, nil, &e); got != http.StatusConflict || !strings.Contains(e.Error, "applied policy") {
		t.Fatalf("delete before the change was applied: HTTP %d %q", got, e.Error)
	}
	// Policy state the controller does not know it left behind (here: the
	// apply is skipped and the snapshot emptied by hand) is still withdrawn.
	if _, err := s.h.Policy.SaveApplied(nil, nil); err != nil {
		t.Fatal(err)
	}

	s.down[a.Address] = true
	if got := s.call("DELETE", "/routers/"+a.ID, nil, &e); got != http.StatusConflict || !strings.Contains(e.Error, "force=1") {
		t.Fatalf("delete of an unreachable router: HTTP %d %q", got, e.Error)
	}
	s.down[a.Address] = false

	var done struct {
		WithdrawnHosts int `json:"withdrawn_hosts"`
	}
	s.mustCall(http.StatusOK, "DELETE", "/routers/"+a.ID, nil, &done)
	if done.WithdrawnHosts != 2 || s.agents[a.Address].managed() != 0 {
		t.Fatalf("withdrawn=%d, managed=%d; the policy state was not withdrawn", done.WithdrawnHosts, s.agents[a.Address].managed())
	}

	b := s.addRouter("b", "2001:db8::b")
	s.down[b.Address] = true
	var forced struct{ Warning string }
	s.mustCall(http.StatusOK, "DELETE", "/routers/"+b.ID+"?force=1", nil, &forced)
	if forced.Warning == "" {
		t.Error("a forced delete of an unreachable router must carry a warning")
	}
}

// A service entry follows its source at every compile, and falls back to the
// last successful answer when the source is down.
func TestServiceEntries(t *testing.T) {
	prefixes := `["2603:1027::/48"]`
	down := false
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case down:
			http.Error(w, "loading", http.StatusServiceUnavailable)
		case r.URL.Path == "/Teams/endpoints":
			w.Write([]byte(`{"ipv6_prefixes":` + prefixes + `}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer source.Close()

	s := newSite(t, fqdn.NewClient([]string{source.URL}))
	a := s.addRouter("a", "2001:db8::a")
	var rule store.Rule
	s.mustCall(http.StatusCreated, "POST", "/rules", map[string]any{
		"nexthop": a.Address, "entries": []map[string]string{{"type": "service", "value": "Teams"}},
	}, &rule)
	s.mustCall(http.StatusCreated, "POST", "/groups", map[string]any{"name": "G", "members": []string{"fe80::1"}, "rules": []int{rule.ID}}, nil)

	routes := func() ([]string, []string) {
		t.Helper()
		var plan planResponse
		s.mustCall(http.StatusOK, "GET", "/policy/plan", nil, &plan)
		if len(plan.Routers[0].Messages) == 0 {
			return nil, plan.Warnings
		}
		return plan.Routers[0].Messages[0].Routes, plan.Warnings
	}

	if got, _ := routes(); len(got) != 1 || got[0] != "2603:1027::/48" {
		t.Fatalf("routes = %v", got)
	}
	prefixes = `["2603:1027::/48","2603:1037::/48"]` // the service grew
	if got, _ := routes(); len(got) != 2 {
		t.Fatalf("routes after the source changed = %v; want 2", got)
	}
	down = true
	got, warnings := routes()
	if len(got) != 2 || !strings.Contains(strings.Join(warnings, "\n"), "using the prefixes resolved at") {
		t.Fatalf("routes with the source down = %v, warnings %v; want the cached 2 and a warning", got, warnings)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// Rules follow a router to its new address, in the draft and in the applied
// policy; otherwise the reconciler would drop them.
func TestRouterAddressChange(t *testing.T) {
	s := newSite(t, nil)
	a := s.addRouter("a", "2001:db8::a")
	s.policy(a)
	s.mustCall(http.StatusOK, "POST", "/policy/apply", nil, nil)

	a.Address = "2001:db8::a2"
	s.mustCall(http.StatusOK, "PUT", "/routers/"+a.ID, a, nil)

	var rules []store.Rule
	s.mustCall(http.StatusOK, "GET", "/rules", nil, &rules)
	if rules[0].Nexthop != "2001:db8::a2" {
		t.Fatalf("the rule still names %s", rules[0].Nexthop)
	}
	if problem := s.h.reconcileOnce(context.Background()); problem != "" {
		t.Fatalf("reconcile: %s", problem)
	}
	if got := s.agents["2001:db8::a2"].managed(); got != 1 {
		t.Fatalf("the reconciler did not install the policy at the new address: %d config(s)", got)
	}
}

// The applied policy decides whether a router is still in use.
func TestDeleteRouterChecksAppliedPolicy(t *testing.T) {
	s := newSite(t, nil)
	a, b := s.addRouter("a", "2001:db8::a"), s.addRouter("b", "2001:db8::b")
	rule, _ := s.policy(a)
	s.mustCall(http.StatusOK, "POST", "/policy/apply", nil, nil)

	// The draft now sends the hosts through b, but that was not applied.
	s.mustCall(http.StatusOK, "PUT", "/rules/"+itoa(rule.ID), map[string]any{
		"nexthop": b.Address, "entries": []map[string]string{{"type": "ipv6", "value": "::/0"}},
	}, nil)
	var e struct{ Error string }
	if got := s.call("DELETE", "/routers/"+a.ID, nil, &e); got != http.StatusConflict || !strings.Contains(e.Error, "applied policy") {
		t.Fatalf("HTTP %d %q; want 409 naming the applied policy", got, e.Error)
	}
	if s.agents[a.Address].managed() != 1 {
		t.Fatal("the refused delete withdrew the policy")
	}
	s.mustCall(http.StatusOK, "POST", "/policy/apply", nil, nil)
	s.mustCall(http.StatusOK, "DELETE", "/routers/"+a.ID, nil, nil)
}

func TestCrossSiteRequestsAreRefused(t *testing.T) {
	s := newSite(t, nil)
	do := func(origin, contentType, body string) int {
		req, _ := http.NewRequest("POST", s.srv.URL+"/api/policy/apply", strings.NewReader(body))
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := do("https://evil.example", "", ""); got != http.StatusForbidden {
		t.Errorf("a request from another site: HTTP %d; want 403", got)
	}
	if got := do(s.srv.URL, "", ""); got != http.StatusOK {
		t.Errorf("a request from the UI's own origin: HTTP %d; want 200", got)
	}
	if got := do("", "", ""); got != http.StatusOK {
		t.Errorf("a request from a non-browser client: HTTP %d; want 200", got)
	}
	if got := do("", "text/plain", "{}"); got != http.StatusUnsupportedMediaType {
		t.Errorf("a body that is not JSON: HTTP %d; want 415", got)
	}
}
