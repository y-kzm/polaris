package engine

import (
	"fmt"
	"sort"
	"strings"

	gorav1 "controller/backend/api/gora/v1"
	"controller/backend/internal/store"

	"google.golang.org/protobuf/proto"
)

// ManagedDescription marks the RA configurations that the controller owns on
// a router. Configurations without it (the baseline RA, anything defined by
// hand) are never modified or removed.
const ManagedDescription = "polaris-policy"

// defaultRouteLifetime is the lifetime of the advertised RIO routes in
// seconds. It is not yet exposed as a per-rule option.
const defaultRouteLifetime = 1800

// Withdrawal is a one-shot RA that removes state a previous policy installed
// on a set of hosts. RAs are soft state: without it a host keeps a route or
// a default router until the lifetime expires.
type Withdrawal struct {
	// Config is the RA to send. Its header and routes carry the state the
	// hosts keep under the new policy, so that the withdrawal does not
	// disturb it.
	Config *gorav1.InterfaceConfig
	// Routes are advertised with a zero lifetime.
	Routes []string
	// DefaultRouter is true if the hosts lose this router as their
	// preferred default router.
	DefaultRouter bool
}

// Plan is the set of operations that moves one router from its running
// policy configurations to the desired messages.
//
// Add and Update belong to the make phase and never take anything away from
// a host. Shrink, Delete and Withdraw belong to the break phase.
type Plan struct {
	Add []*gorav1.InterfaceConfig
	// Update holds configurations that change in place. If the change
	// removes clients, the configuration here still lists them; the final
	// client list is in Shrink.
	Update []*gorav1.InterfaceConfig
	// Shrink holds the final form of the configurations that lose clients.
	Shrink    []*gorav1.InterfaceConfig
	Unchanged int
	Delete    []int32
	Withdraw  []Withdrawal
}

// Empty reports whether the router already runs the desired policy.
func (p *Plan) Empty() bool {
	return len(p.Add)+len(p.Update)+len(p.Shrink)+len(p.Delete)+len(p.Withdraw) == 0
}

// Changed is the number of existing configurations the plan modifies.
func (p *Plan) Changed() int {
	ids := map[int32]struct{}{}
	for _, cfg := range p.Update {
		ids[cfg.Id] = struct{}{}
	}
	for _, cfg := range p.Shrink {
		ids[cfg.Id] = struct{}{}
	}
	return len(ids)
}

// WithdrawnHosts is the number of hosts that receive a withdrawal RA.
func (p *Plan) WithdrawnHosts() int {
	n := 0
	for _, w := range p.Withdraw {
		n += len(w.Config.Clients)
	}
	return n
}

// Reconcile computes the plan for one router. running is the full list of RA
// configurations on the router; only the managed ones are considered.
//
// A running configuration whose content (routes and preference) matches a
// desired message keeps its ID and is updated in place if its clients or
// parameters changed. Everything a host received before but does not receive
// any more becomes a withdrawal.
//
// It fails if the policy source address is already the source of a
// configuration the controller does not own: a route-only policy RA has
// Router Lifetime 0 and would remove that configuration's default router
// from the hosts.
func Reconcile(router *store.Router, running []*gorav1.InterfaceConfig, desired []Message) (*Plan, error) {
	plan := &Plan{}

	// Source address of the baseline RA, if it is configured explicitly.
	baselineSource := ""
	for _, cfg := range running {
		if isManaged(cfg) || cfg.Name != router.Interface {
			continue
		}
		if router.PolicySourceAddress != "" && sameAddress(cfg.SourceAddress, router.PolicySourceAddress) {
			return nil, fmt.Errorf("policy source address %s is also the source of config #%d, which the controller does not manage; use a separate link-local address",
				router.PolicySourceAddress, cfg.Id)
		}
		if router.PolicySourceAddress != "" && cfg.SourceAddress == "" {
			// The agent then picks any link-local address of the interface,
			// possibly the policy address itself.
			return nil, fmt.Errorf("config #%d on %s has no sourceAddress, so it may be sent from the policy source address %s; set its sourceAddress to the baseline link-local address",
				cfg.Id, cfg.Name, router.PolicySourceAddress)
		}
		if baselineSource == "" {
			baselineSource = cfg.SourceAddress
		}
	}

	usedIDs := map[int32]bool{}
	managedByKey := map[string][]*gorav1.InterfaceConfig{}
	oldState := hostState{}
	// State installed from a source address the router no longer uses for
	// its policy RAs (the address was changed). It lives in separate router
	// entries on the hosts and has to be withdrawn from that old address.
	foreign := map[string]hostState{}
	for _, cfg := range running {
		usedIDs[cfg.Id] = true
		if !isManaged(cfg) {
			continue
		}
		c := configContent(cfg)
		if src := canonicalSource(cfg.SourceAddress); src != canonicalSource(router.PolicySourceAddress) {
			if foreign[src] == nil {
				foreign[src] = hostState{}
			}
			mergeState(foreign[src], cfg.Clients, c)
			continue // never reused: it is deleted below
		}
		managedByKey[c.key()] = append(managedByKey[c.key()], cfg)
		mergeState(oldState, cfg.Clients, c)
	}

	newState := hostState{}
	claimed := map[int32]bool{}
	for i := range desired {
		msg := &desired[i]
		c := msg.content()
		mergeState(newState, msg.Clients, c)

		var existing *gorav1.InterfaceConfig
		for _, cfg := range managedByKey[c.key()] {
			if !claimed[cfg.Id] {
				existing = cfg
				break
			}
		}
		if existing == nil {
			id := nextFreeID(usedIDs)
			usedIDs[id] = true
			plan.Add = append(plan.Add, buildConfig(router, id, c, msg.Clients))
			continue
		}
		claimed[existing.Id] = true
		want := buildConfig(router, existing.Id, c, msg.Clients)
		switch union := unionClients(existing.Clients, want.Clients); {
		case sameConfig(existing, want):
			plan.Unchanged++
		case len(union) == len(want.Clients):
			// No client is removed.
			plan.Update = append(plan.Update, want)
		default:
			// Keep the leaving clients until the break phase.
			grown := proto.Clone(want).(*gorav1.InterfaceConfig)
			grown.Clients = union
			if !sameConfig(existing, grown) {
				plan.Update = append(plan.Update, grown)
			}
			plan.Shrink = append(plan.Shrink, want)
		}
	}

	for _, cfg := range running {
		if isManaged(cfg) && !claimed[cfg.Id] {
			plan.Delete = append(plan.Delete, cfg.Id)
		}
	}
	sort.Slice(plan.Delete, func(i, j int) bool { return plan.Delete[i] < plan.Delete[j] })

	plan.Withdraw = withdrawals(router, oldState, newState)

	sources := make([]string, 0, len(foreign))
	for src := range foreign {
		sources = append(sources, src)
	}
	sort.Strings(sources)
	for _, src := range sources {
		// Nothing is advertised from the old address any more.
		old := *router
		old.PolicySourceAddress = src
		ws := withdrawals(&old, foreign[src], hostState{})
		if src == "" {
			// The old policy RAs shared the baseline address. Name it
			// explicitly: with a second link-local address on the
			// interface, the agent's default source is no longer that one.
			for i := range ws {
				ws[i].Config.SourceAddress = baselineSource
			}
		}
		plan.Withdraw = append(plan.Withdraw, ws...)
	}
	return plan, nil
}

// unionClients returns the sorted union of two client lists.
func unionClients(a, b []string) []string {
	set := map[string]struct{}{}
	for _, list := range [][]string{a, b} {
		for _, c := range list {
			if h, ok := canonicalHost(c); ok {
				c = h
			}
			set[c] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// canonicalSource returns a source address in canonical form, so that two
// spellings of one address compare equal. Other strings are returned as is.
func canonicalSource(s string) string {
	if h, ok := canonicalHost(s); ok {
		return h
	}
	return s
}

func sameAddress(a, b string) bool {
	ha, okA := canonicalHost(a)
	hb, okB := canonicalHost(b)
	return okA && okB && ha == hb
}

// withdrawals computes the per-host difference between the old and the new
// state and groups the hosts that need an identical withdrawal RA.
func withdrawals(router *store.Router, oldState, newState hostState) []Withdrawal {
	type group struct {
		after   *content
		gone    []string
		dropDef bool
		clients []string
	}
	groups := map[string]*group{}

	for h, before := range oldState {
		after := newState[h]
		if after == nil {
			after = newContent()
		}
		var gone []string
		for r := range before.routes {
			if _, kept := after.routes[r]; !kept {
				gone = append(gone, r)
			}
		}
		sort.Strings(gone)
		dropDefault := before.high && !after.high
		if len(gone) == 0 && !dropDefault {
			continue
		}

		k := after.key() + "#" + strings.Join(gone, ",")
		if dropDefault {
			k += "#default"
		}
		g := groups[k]
		if g == nil {
			g = &group{after: after, gone: gone, dropDef: dropDefault}
			groups[k] = g
		}
		g.clients = append(g.clients, h)
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]Withdrawal, 0, len(keys))
	for _, k := range keys {
		g := groups[k]
		sort.Strings(g.clients)
		out = append(out, Withdrawal{
			Config:        buildConfig(router, 0, g.after, g.clients),
			Routes:        g.gone,
			DefaultRouter: g.dropDef,
		})
	}
	return out
}

// buildConfig builds the RA configuration that advertises content c from
// router to clients.
//
// With a policy source address, the policy RA is a separate router entry on
// the host. It is a default router (preference high) only if ::/0 is
// assigned to the router; otherwise its Router Lifetime is 0 and it only
// carries routes, leaving the baseline entry of the router untouched.
//
// Without one, the policy RA shares the source address of the baseline RA,
// so it repeats the baseline Router Lifetime to avoid removing the router
// from the host's default router list. In that mode a baseline RA still
// resets a high preference until the next policy RA.
func buildConfig(router *store.Router, id int32, c *content, clients []string) *gorav1.InterfaceConfig {
	cfg := &gorav1.InterfaceConfig{
		Id:                         id,
		Description:                ManagedDescription,
		Name:                       router.Interface,
		RaIntervalMilliseconds:     router.RAIntervalMs,
		CurrentHopLimit:            router.CurrentHopLimit,
		Managed:                    router.Managed,
		Other:                      router.Other,
		ReachableTimeMilliseconds:  int64(router.ReachableTimeMs),
		RetransmitTimeMilliseconds: int64(router.RetransmitTimeMs),
		Clients:                    clients,
		Preference:                 "medium",
		SourceAddress:              router.PolicySourceAddress,
		// A policy RA must reach only its clients, so it never answers a
		// Router Solicitation.
		DisableRsReply: true,
	}

	switch {
	case c.high:
		cfg.Preference = "high"
		cfg.RouterLifetimeSeconds = router.RouterLifetimeS
	case router.PolicySourceAddress == "":
		cfg.RouterLifetimeSeconds = router.RouterLifetimeS
	}

	for _, r := range c.sortedRoutes() {
		cfg.Routes = append(cfg.Routes, &gorav1.RouteConfig{
			Prefix:          r,
			LifetimeSeconds: defaultRouteLifetime,
			Preference:      "medium",
		})
	}
	return cfg
}

func isManaged(cfg *gorav1.InterfaceConfig) bool {
	return cfg.Description == ManagedDescription
}

// configContent recovers the content a running configuration advertises.
func configContent(cfg *gorav1.InterfaceConfig) *content {
	c := newContent()
	c.high = cfg.Preference == "high" && cfg.RouterLifetimeSeconds > 0
	for _, r := range cfg.Routes {
		if r.LifetimeSeconds == 0 {
			continue
		}
		p, err := parsePrefix(r.Prefix)
		if err != nil {
			p = r.Prefix
		}
		c.routes[p] = struct{}{}
	}
	return c
}

// mergeState adds content c to every client. A host normally appears in one
// managed configuration per router; merging keeps the result correct if a
// router was left in an inconsistent state.
func mergeState(st hostState, clients []string, c *content) {
	for _, client := range clients {
		h, ok := canonicalHost(client)
		if !ok {
			h = client
		}
		cur := st[h]
		if cur == nil {
			cur = newContent()
			st[h] = cur
		}
		cur.high = cur.high || c.high
		for r := range c.routes {
			cur.routes[r] = struct{}{}
		}
	}
}

// sameConfig reports whether the running configuration already equals the
// wanted one. The agent fills in defaults (for example send_goodbye), so
// only the fields the controller sets are compared.
func sameConfig(running, want *gorav1.InterfaceConfig) bool {
	r := proto.Clone(running).(*gorav1.InterfaceConfig)
	r.SendGoodbye = want.SendGoodbye
	r.Mtu = want.Mtu
	sort.Strings(r.Clients)
	return proto.Equal(r, want)
}

// nextFreeID returns the smallest unused configuration ID starting from 1.
// ID 0 is conventionally the baseline RA.
func nextFreeID(used map[int32]bool) int32 {
	for id := int32(1); ; id++ {
		if !used[id] {
			return id
		}
	}
}
