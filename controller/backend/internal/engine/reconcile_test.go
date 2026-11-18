package engine

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	gorav1 "controller/backend/api/gora/v1"
	"controller/backend/internal/store"
)

var baseline = &gorav1.InterfaceConfig{Id: 0, Name: "eth0", Preference: "medium", RouterLifetimeSeconds: 1800}

func msg(high bool, clients []string, routes ...string) Message {
	if routes == nil {
		routes = []string{}
	}
	return Message{Routes: routes, High: high, Clients: clients}
}

// running returns the configurations a router runs after plan was applied on
// top of the given ones.
// plan reconciles and fails the test on an error.
func mustReconcile(t *testing.T, router *store.Router, state []*gorav1.InterfaceConfig, desired []Message) *Plan {
	t.Helper()
	plan, err := Reconcile(router, state, desired)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return plan
}

func running(before []*gorav1.InterfaceConfig, plan *Plan) []*gorav1.InterfaceConfig {
	deleted := map[int32]bool{}
	for _, id := range plan.Delete {
		deleted[id] = true
	}
	updated := map[int32]*gorav1.InterfaceConfig{}
	for _, cfg := range plan.Update {
		updated[cfg.Id] = cfg
	}
	for _, cfg := range plan.Shrink {
		updated[cfg.Id] = cfg
	}
	var out []*gorav1.InterfaceConfig
	for _, cfg := range before {
		switch {
		case deleted[cfg.Id]:
		case updated[cfg.Id] != nil:
			out = append(out, updated[cfg.Id])
		default:
			out = append(out, cfg)
		}
	}
	return append(out, plan.Add...)
}

func routePrefixes(cfg *gorav1.InterfaceConfig) []string {
	out := []string{}
	for _, r := range cfg.Routes {
		out = append(out, r.Prefix)
	}
	return out
}

func TestReconcileFreshRouter(t *testing.T) {
	handMade := &gorav1.InterfaceConfig{Id: 1, Name: "eth0", Clients: []string{"fe80::99"}}
	desired := []Message{
		msg(true, []string{"fe80::2"}),
		msg(false, []string{"fe80::1"}, "2001:db8:1::/48"),
	}

	plan := mustReconcile(t, &routerA, []*gorav1.InterfaceConfig{baseline, handMade}, desired)

	if len(plan.Add) != 2 || len(plan.Update)+len(plan.Delete)+len(plan.Withdraw) != 0 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	// IDs 0 and 1 are taken by configurations the controller does not own.
	if plan.Add[0].Id != 2 || plan.Add[1].Id != 3 {
		t.Errorf("IDs = %d, %d; want 2, 3", plan.Add[0].Id, plan.Add[1].Id)
	}

	high, rio := plan.Add[0], plan.Add[1]
	for _, cfg := range plan.Add {
		if cfg.SourceAddress != "fe80::face:a" || !cfg.DisableRsReply || cfg.Description != ManagedDescription {
			t.Errorf("config #%d: source=%q disableRsReply=%t description=%q",
				cfg.Id, cfg.SourceAddress, cfg.DisableRsReply, cfg.Description)
		}
	}
	if high.Preference != "high" || high.RouterLifetimeSeconds != 1800 {
		t.Errorf("default-router RA: preference=%s lifetime=%d", high.Preference, high.RouterLifetimeSeconds)
	}
	// A route-only policy RA must not make the policy address a default
	// router.
	if rio.Preference != "medium" || rio.RouterLifetimeSeconds != 0 {
		t.Errorf("route-only RA: preference=%s lifetime=%d; want medium, 0", rio.Preference, rio.RouterLifetimeSeconds)
	}
}

func TestReconcileWithoutPolicySourceAddress(t *testing.T) {
	legacy := routerA
	legacy.PolicySourceAddress = ""

	plan := mustReconcile(t, &legacy, []*gorav1.InterfaceConfig{baseline},
		[]Message{msg(false, []string{"fe80::1"}, "2001:db8:1::/48")})

	// Sharing the baseline address, the RA must repeat the baseline Router
	// Lifetime, or it would remove the router from the default router list.
	if got := plan.Add[0]; got.SourceAddress != "" || got.RouterLifetimeSeconds != 1800 {
		t.Errorf("source=%q lifetime=%d; want \"\", 1800", got.SourceAddress, got.RouterLifetimeSeconds)
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	desired := []Message{
		msg(true, []string{"fe80::2", "fe80::3"}, "2001:db8:3::/48"),
		msg(false, []string{"fe80::1"}, "2001:db8:1::/48"),
	}
	before := []*gorav1.InterfaceConfig{baseline}
	after := running(before, mustReconcile(t, &routerA, before, desired))

	plan := mustReconcile(t, &routerA, after, desired)
	if !plan.Empty() || plan.Unchanged != 2 {
		t.Fatalf("second plan is not empty: %+v", plan)
	}
}

func TestReconcileTransitions(t *testing.T) {
	tests := []struct {
		name          string
		before, after []Message
		wantAdd       int
		wantUpdate    int
		wantDelete    int
		wantWithdraw  []Withdrawal // only Routes, DefaultRouter and Config.Clients are compared
		wantHeader    string       // preference/lifetime/routes of the first withdrawal RA
		wantHeaderRIO []string
	}{
		{
			name:         "host removed from a group",
			before:       []Message{msg(false, []string{"fe80::1", "fe80::2"}, "2001:db8:1::/48")},
			after:        []Message{msg(false, []string{"fe80::1"}, "2001:db8:1::/48")},
			wantUpdate:   1,
			wantWithdraw: []Withdrawal{{Routes: []string{"2001:db8:1::/48"}, Config: &gorav1.InterfaceConfig{Clients: []string{"fe80::2"}}}},
			wantHeader:   "medium/0",
		},
		{
			name:         "policy removed entirely",
			before:       []Message{msg(true, []string{"fe80::1"}, "2001:db8:1::/48")},
			after:        nil,
			wantDelete:   1,
			wantWithdraw: []Withdrawal{{Routes: []string{"2001:db8:1::/48"}, DefaultRouter: true, Config: &gorav1.InterfaceConfig{Clients: []string{"fe80::1"}}}},
			wantHeader:   "medium/0",
		},
		{
			// The host moves to another configuration of the same router.
			// The route it keeps must not be withdrawn.
			name:       "host gains a route",
			before:     []Message{msg(false, []string{"fe80::1", "fe80::2"}, "2001:db8:1::/48")},
			after:      []Message{msg(false, []string{"fe80::1"}, "2001:db8:1::/48"), msg(false, []string{"fe80::2"}, "2001:db8:1::/48", "2001:db8:3::/48")},
			wantAdd:    1,
			wantUpdate: 1,
		},
		{
			name:          "host loses the default router but keeps a route",
			before:        []Message{msg(true, []string{"fe80::1"}, "2001:db8:1::/48")},
			after:         []Message{msg(false, []string{"fe80::1"}, "2001:db8:1::/48")},
			wantAdd:       1,
			wantDelete:    1,
			wantWithdraw:  []Withdrawal{{Routes: nil, DefaultRouter: true, Config: &gorav1.InterfaceConfig{Clients: []string{"fe80::1"}}}},
			wantHeader:    "medium/0",
			wantHeaderRIO: []string{"2001:db8:1::/48"},
		},
		{
			// The withdrawal RA shares the source address with the RA that
			// keeps the host's default router, so it must repeat that state.
			name:         "host loses a route but keeps the default router",
			before:       []Message{msg(true, []string{"fe80::1"}, "2001:db8:1::/48")},
			after:        []Message{msg(true, []string{"fe80::1"})},
			wantAdd:      1,
			wantDelete:   1,
			wantWithdraw: []Withdrawal{{Routes: []string{"2001:db8:1::/48"}, Config: &gorav1.InterfaceConfig{Clients: []string{"fe80::1"}}}},
			wantHeader:   "high/1800",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := []*gorav1.InterfaceConfig{baseline}
			state := running(start, mustReconcile(t, &routerA, start, tt.before))

			plan := mustReconcile(t, &routerA, state, tt.after)

			if len(plan.Add) != tt.wantAdd || plan.Changed() != tt.wantUpdate || len(plan.Delete) != tt.wantDelete {
				t.Errorf("add=%d update=%d delete=%d; want %d, %d, %d",
					len(plan.Add), plan.Changed(), len(plan.Delete), tt.wantAdd, tt.wantUpdate, tt.wantDelete)
			}
			if len(plan.Withdraw) != len(tt.wantWithdraw) {
				t.Fatalf("withdrawals = %+v; want %d", plan.Withdraw, len(tt.wantWithdraw))
			}
			for i, want := range tt.wantWithdraw {
				got := plan.Withdraw[i]
				if !reflect.DeepEqual(got.Routes, want.Routes) || got.DefaultRouter != want.DefaultRouter ||
					!reflect.DeepEqual(got.Config.Clients, want.Config.Clients) {
					t.Errorf("withdrawal %d = routes %v default %t clients %v; want %v %t %v", i,
						got.Routes, got.DefaultRouter, got.Config.Clients,
						want.Routes, want.DefaultRouter, want.Config.Clients)
				}
			}
			if len(plan.Withdraw) > 0 {
				cfg := plan.Withdraw[0].Config
				header := cfg.Preference + "/" + strconv.Itoa(int(cfg.RouterLifetimeSeconds))
				if header != tt.wantHeader {
					t.Errorf("withdrawal RA header = %s; want %s", header, tt.wantHeader)
				}
				if tt.wantHeaderRIO != nil && !reflect.DeepEqual(routePrefixes(cfg), tt.wantHeaderRIO) {
					t.Errorf("withdrawal RA routes = %v; want %v", routePrefixes(cfg), tt.wantHeaderRIO)
				}
				if cfg.SourceAddress != routerA.PolicySourceAddress {
					t.Errorf("withdrawal RA source = %q", cfg.SourceAddress)
				}
			}

			// Applying the plan must reach a fixed point.
			if again := mustReconcile(t, &routerA, running(state, plan), tt.after); !again.Empty() {
				t.Errorf("not a fixed point: %+v", again)
			}
		})
	}
}

// Changing the policy source address of a router must not leave the state
// advertised from the old address on the hosts.
func TestReconcileSourceAddressChange(t *testing.T) {
	desired := []Message{msg(true, []string{"fe80::1"}, "2001:db8:1::/48")}
	start := []*gorav1.InterfaceConfig{baseline}
	state := running(start, mustReconcile(t, &routerA, start, desired))

	moved := routerA
	moved.PolicySourceAddress = "fe80::face:99"
	plan := mustReconcile(t, &moved, state, desired)

	if len(plan.Add) != 1 || len(plan.Delete) != 1 || len(plan.Update) != 0 {
		t.Fatalf("add=%d update=%d delete=%d; want 1, 0, 1", len(plan.Add), len(plan.Update), len(plan.Delete))
	}
	if plan.Add[0].SourceAddress != "fe80::face:99" {
		t.Errorf("new config source = %q", plan.Add[0].SourceAddress)
	}
	if len(plan.Withdraw) != 1 {
		t.Fatalf("withdrawals = %+v; want 1", plan.Withdraw)
	}
	w := plan.Withdraw[0]
	if w.Config.SourceAddress != routerA.PolicySourceAddress || w.Config.RouterLifetimeSeconds != 0 ||
		!w.DefaultRouter || !reflect.DeepEqual(w.Routes, []string{"2001:db8:1::/48"}) ||
		!reflect.DeepEqual(w.Config.Clients, []string{"fe80::1"}) || len(w.Config.Routes) != 0 {
		t.Errorf("withdrawal = source %q lifetime %d default %t routes %v clients %v kept %v",
			w.Config.SourceAddress, w.Config.RouterLifetimeSeconds, w.DefaultRouter, w.Routes,
			w.Config.Clients, routePrefixes(w.Config))
	}
	if again := mustReconcile(t, &moved, running(state, plan), desired); !again.Empty() {
		t.Errorf("not a fixed point: %+v", again)
	}
}

// The make phase must not take a route away from a host: a client that
// leaves a configuration stays in it until the break phase.
func TestReconcileKeepsLeavingClientsUntilBreak(t *testing.T) {
	start := []*gorav1.InterfaceConfig{baseline}
	before := []Message{msg(false, []string{"fe80::1", "fe80::2"}, "2001:db8:1::/48")}
	state := running(start, mustReconcile(t, &routerA, start, before))

	// fe80::2 leaves, fe80::3 joins.
	plan := mustReconcile(t, &routerA, state, []Message{msg(false, []string{"fe80::1", "fe80::3"}, "2001:db8:1::/48")})

	if len(plan.Update) != 1 || !reflect.DeepEqual(plan.Update[0].Clients, []string{"fe80::1", "fe80::2", "fe80::3"}) {
		t.Fatalf("make-phase update = %+v; want clients fe80::1..3", plan.Update)
	}
	if len(plan.Shrink) != 1 || !reflect.DeepEqual(plan.Shrink[0].Clients, []string{"fe80::1", "fe80::3"}) {
		t.Fatalf("break-phase update = %+v; want clients fe80::1, fe80::3", plan.Shrink)
	}
}

// A route-only policy RA has Router Lifetime 0. Sent from the baseline
// address it would remove the baseline default router.
func TestReconcileRejectsBaselineAddressAsPolicySource(t *testing.T) {
	explicit := &gorav1.InterfaceConfig{Id: 0, Name: "eth0", Preference: "medium",
		RouterLifetimeSeconds: 1800, SourceAddress: "FE80::FACE:A"}

	_, err := Reconcile(&routerA, []*gorav1.InterfaceConfig{explicit}, nil)
	if err == nil || !strings.Contains(err.Error(), "use a separate link-local address") {
		t.Fatalf("err = %v", err)
	}
}

// Moving from the shared address to a policy address: the old state is
// withdrawn from the baseline address, named explicitly.
func TestReconcileWithdrawsLegacyStateFromBaselineAddress(t *testing.T) {
	explicit := &gorav1.InterfaceConfig{Id: 0, Name: "eth0", Preference: "medium",
		RouterLifetimeSeconds: 1800, SourceAddress: "fe80::b:a5e"}
	legacy := routerA
	legacy.PolicySourceAddress = ""
	desired := []Message{msg(true, []string{"fe80::1"})}
	start := []*gorav1.InterfaceConfig{explicit}
	state := running(start, mustReconcile(t, &legacy, start, desired))

	plan := mustReconcile(t, &routerA, state, desired)

	if len(plan.Withdraw) != 1 {
		t.Fatalf("withdrawals = %+v", plan.Withdraw)
	}
	// Medium with the baseline lifetime restores the baseline entry.
	w := plan.Withdraw[0].Config
	if w.SourceAddress != "fe80::b:a5e" || w.Preference != "medium" || w.RouterLifetimeSeconds != 1800 {
		t.Errorf("withdrawal = source %q %s/%d", w.SourceAddress, w.Preference, w.RouterLifetimeSeconds)
	}
}
