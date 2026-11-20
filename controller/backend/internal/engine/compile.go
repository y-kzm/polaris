package engine

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"controller/backend/internal/store"
)

// content is what one site-exit router advertises to one host in its policy
// RA: the destination prefixes carried as Route Information Options and
// whether the router is the host's preferred default router.
type content struct {
	routes map[string]struct{} // canonical IPv6 prefixes, never ::/0
	high   bool                // ::/0 is assigned to the router
}

func newContent() *content { return &content{routes: map[string]struct{}{}} }

func (c *content) empty() bool { return c == nil || (!c.high && len(c.routes) == 0) }

func (c *content) sortedRoutes() []string {
	if c == nil {
		return []string{}
	}
	out := make([]string, 0, len(c.routes))
	for r := range c.routes {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// key identifies the content. Hosts with the same key on the same router
// share one control request message.
func (c *content) key() string {
	prf := "medium"
	if c != nil && c.high {
		prf = "high"
	}
	return prf + "|" + strings.Join(c.sortedRoutes(), ",")
}

// Message is a control request message of the paper's Algorithm 1: the RA
// content that one site-exit router applies to a set of hosts.
type Message struct {
	Routes  []string // RIO prefixes, sorted, without ::/0
	High    bool     // prf: high if ::/0 is assigned to the router
	Clients []string // link-local addresses of the hosts, sorted
}

func (m *Message) content() *content {
	c := newContent()
	c.high = m.High
	for _, r := range m.Routes {
		c.routes[r] = struct{}{}
	}
	return c
}

// hostState maps a host link-local address to the content it receives from
// one router.
type hostState map[string]*content

// Compile implements Algorithm 1. For every pair of a router and a host it
// merges all rules that apply to the host and designate the router, then
// groups the hosts that receive identical content from the same router into
// one message. The result maps a router ID to its messages; every host
// appears in at most one message per router.
//
// Problems that do not prevent compilation (unknown next hop, malformed
// entry, the same prefix assigned to two routers for one host) are returned
// as warnings.
//
// resolve turns the name of a service entry into its prefixes; it may be nil
// if the rules contain no service entries. Rules that designate an inactive
// router are skipped.
func Compile(rules []store.Rule, groups []store.Group, routers []store.Router, resolve Resolver) (map[string][]Message, []string) {
	var warnings []string
	warn := func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}

	// The Nexthop of a rule holds either the router address or its ID.
	routerByRef := make(map[string]*store.Router, len(routers)*2)
	for i := range routers {
		routerByRef[routers[i].Address] = &routers[i]
		routerByRef[routers[i].ID] = &routers[i]
	}
	ruleByID := make(map[int]*store.Rule, len(rules))
	for i := range rules {
		ruleByID[rules[i].ID] = &rules[i]
	}

	// M: (router ID, host) -> content. Lines 1-16 of Algorithm 1.
	states := map[string]hostState{}
	// designated[host][prefix] = router ID, to detect conflicting rules.
	designated := map[string]map[string]string{}

	for _, g := range groups {
		hosts := canonicalHosts(g.Members, func(m string) {
			warn("group %q: %q is not a link-local IPv6 address; skipped", g.Name, m)
		})
		for _, ruleID := range g.Rules {
			rule := ruleByID[ruleID]
			if rule == nil {
				warn("group %q refers to rule %d, which does not exist", g.Name, ruleID)
				continue
			}
			router := routerByRef[rule.Nexthop]
			if router == nil {
				warn("rule %d: next hop %q is not a registered router", rule.ID, rule.Nexthop)
				continue
			}
			if !router.Active() {
				warn("rule %d: %s is inactive; the rule is not enforced", rule.ID, router.Name)
				continue
			}
			prefixes := rulePrefixes(rule, resolve, warn)
			if states[router.ID] == nil {
				states[router.ID] = hostState{}
			}
			for _, h := range hosts {
				c := states[router.ID][h]
				if c == nil {
					c = newContent()
					states[router.ID][h] = c
				}
				if designated[h] == nil {
					designated[h] = map[string]string{}
				}
				for _, p := range prefixes {
					if other, ok := designated[h][p]; ok && other != router.ID {
						warn("host %s: %s is assigned to both %s and %s; the intent is not enforced for this prefix",
							h, p, routerName(routers, other), router.Name)
					}
					designated[h][p] = router.ID
					if p == defaultPrefix {
						c.high = true
					} else {
						c.routes[p] = struct{}{}
					}
				}
			}
		}
	}

	// Lines 17-21 of Algorithm 1.
	out := make(map[string][]Message, len(states))
	for routerID, st := range states {
		out[routerID] = groupByContent(st)
	}
	sort.Strings(warnings)
	return out, dedupe(warnings)
}

const defaultPrefix = "::/0"

// groupByContent groups the hosts with identical content into messages,
// ordered by content key. Hosts with empty content are dropped.
func groupByContent(st hostState) []Message {
	byKey := map[string]*Message{}
	for h, c := range st {
		if c.empty() {
			continue
		}
		k := c.key()
		m := byKey[k]
		if m == nil {
			m = &Message{Routes: c.sortedRoutes(), High: c.high}
			byKey[k] = m
		}
		m.Clients = append(m.Clients, h)
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	msgs := make([]Message, 0, len(keys))
	for _, k := range keys {
		sort.Strings(byKey[k].Clients)
		msgs = append(msgs, *byKey[k])
	}
	return msgs
}

// rulePrefixes returns the canonical destination prefixes of a rule. A bare
// address is treated as a /128.
func rulePrefixes(rule *store.Rule, resolve Resolver, warn func(string, ...any)) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(value, origin string) {
		p, err := ParsePrefix(value)
		if err != nil {
			warn("rule %d: %s %q is not an IPv6 prefix; skipped", rule.ID, origin, value)
			return
		}
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	for _, e := range rule.Entries {
		if e.Type != EntryService {
			add(e.Value, "entry")
			continue
		}
		if resolve == nil {
			warn("rule %d: service %q cannot be resolved: no endpoint source is configured", rule.ID, e.Value)
			continue
		}
		prefixes, note, err := resolve(e.Value)
		if err != nil {
			warn("rule %d: service %q contributes no prefix: %v", rule.ID, e.Value, err)
			continue
		}
		if note != "" {
			warn("service %q: %s", e.Value, note)
		}
		for _, p := range prefixes {
			add(p, fmt.Sprintf("prefix of service %q", e.Value))
		}
	}
	return out
}

// EntryService is the type of a rule entry that names a service; its
// prefixes are looked up at every compile. Every other entry is a literal
// IPv6 prefix.
const EntryService = "service"

// Resolver returns the IPv6 prefixes of a service. A non-empty note reports
// that the answer is not fresh (for example, taken from a cache).
type Resolver func(service string) (prefixes []string, note string, err error)

// ParsePrefix returns the canonical form of an IPv6 prefix. A bare address
// is treated as a host prefix.
func ParsePrefix(s string) (string, error) {
	s = strings.TrimSpace(s)
	p, err := netip.ParsePrefix(s)
	if err != nil {
		addr, aerr := netip.ParseAddr(s)
		if aerr != nil {
			return "", err
		}
		p = netip.PrefixFrom(addr, addr.BitLen())
	}
	if !p.Addr().Is6() || p.Addr().Is4In6() {
		return "", fmt.Errorf("%s is not an IPv6 prefix", s)
	}
	return p.Masked().String(), nil
}

// canonicalHosts returns the valid link-local members in canonical form,
// without duplicates.
func canonicalHosts(members []string, invalid func(string)) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, m := range members {
		h, ok := canonicalHost(m)
		if !ok {
			invalid(m)
			continue
		}
		if _, dup := seen[h]; dup {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	return out
}

// CanonicalHost returns the canonical form of a link-local unicast address.
func CanonicalHost(s string) (string, bool) { return canonicalHost(s) }

func canonicalHost(s string) (string, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil || !addr.Is6() || !addr.IsLinkLocalUnicast() {
		return "", false
	}
	return addr.WithZone("").String(), true
}

func routerName(routers []store.Router, id string) string {
	for _, r := range routers {
		if r.ID == id {
			return r.Name
		}
	}
	return id
}

func dedupe(sorted []string) []string {
	out := sorted[:0]
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}
