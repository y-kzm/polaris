package engine

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"controller/backend/internal/store"
)

var (
	routerA = store.Router{ID: "a", Name: "Router(a)", Address: "2001:db8::a", Interface: "eth0",
		RAIntervalMs: 3000, CurrentHopLimit: 64, RouterLifetimeS: 1800, PolicySourceAddress: "fe80::face:a"}
	routerB = store.Router{ID: "b", Name: "Router(b)", Address: "2001:db8::b", Interface: "eth0",
		RAIntervalMs: 3000, CurrentHopLimit: 64, RouterLifetimeS: 1800, PolicySourceAddress: "fe80::face:b"}
	testRouters = []store.Router{routerA, routerB}
)

func rule(id int, nexthop string, prefixes ...string) store.Rule {
	r := store.Rule{ID: id, Nexthop: nexthop}
	for _, p := range prefixes {
		r.Entries = append(r.Entries, store.Entry{Type: "ipv6", Value: p})
	}
	return r
}

func group(name string, rules []int, members ...string) store.Group {
	return store.Group{Name: name, Rules: rules, Members: members}
}

// The example of Figure 4 / Table 1 in the paper.
func TestCompilePaperExample(t *testing.T) {
	rules := []store.Rule{
		rule(1, routerA.Address, "2001:db8:1::/48", "2001:db8:2::/48"),
		rule(2, routerB.Address, "::/0"),
	}
	groups := []store.Group{
		group("G1", []int{1}, "fe80::1"),
		group("G2", []int{2}, "fe80::2"),
	}

	got, warnings := Compile(rules, groups, testRouters, nil)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	want := map[string][]Message{
		"a": {{Routes: []string{"2001:db8:1::/48", "2001:db8:2::/48"}, High: false, Clients: []string{"fe80::1"}}},
		"b": {{Routes: []string{}, High: true, Clients: []string{"fe80::2"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// A host receives at most one policy RA per router, whatever the number of
// groups and rules that apply to it.
func TestCompileMergesRulesPerRouterAndHost(t *testing.T) {
	rules := []store.Rule{
		rule(1, routerA.Address, "2001:db8:1::/48"),
		rule(2, routerA.ID, "::/0", "2001:db8:3::/48"),
	}
	groups := []store.Group{
		group("G1", []int{1}, "fe80::1", "fe80::2", "fe80::3"),
		group("G2", []int{2}, "fe80::2", "FE80::3"),
	}

	got, warnings := Compile(rules, groups, testRouters, nil)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	want := []Message{
		{Routes: []string{"2001:db8:1::/48", "2001:db8:3::/48"}, High: true, Clients: []string{"fe80::2", "fe80::3"}},
		{Routes: []string{"2001:db8:1::/48"}, High: false, Clients: []string{"fe80::1"}},
	}
	if !reflect.DeepEqual(got["a"], want) {
		t.Fatalf("got %+v\nwant %+v", got["a"], want)
	}
	seen := map[string]int{}
	for _, m := range got["a"] {
		for _, c := range m.Clients {
			seen[c]++
		}
	}
	for h, n := range seen {
		if n != 1 {
			t.Errorf("host %s appears in %d messages of one router", h, n)
		}
	}
}

func TestCompileWarnings(t *testing.T) {
	rules := []store.Rule{
		rule(1, routerA.Address, "2001:db8:1::/48", "not-a-prefix"),
		rule(2, routerB.Address, "2001:db8:1::/48"),
		rule(3, "2001:db8::dead", "::/0"),
	}
	groups := []store.Group{
		group("G1", []int{1, 2, 3, 99}, "fe80::1", "2001:db8::1"),
	}

	got, warnings := Compile(rules, groups, testRouters, nil)

	for _, want := range []string{
		`"not-a-prefix" is not an IPv6 prefix`,
		`2001:db8:1::/48 is assigned to both Router(a) and Router(b)`,
		`next hop "2001:db8::dead" is not a registered router`,
		`rule 99, which does not exist`,
		`"2001:db8::1" is not a link-local IPv6 address`,
	} {
		found := false
		for _, w := range warnings {
			if strings.Contains(w, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("missing warning containing %q in %v", want, warnings)
		}
	}
	// Compilation still succeeds for the valid parts.
	if len(got["a"]) != 1 || len(got["b"]) != 1 {
		t.Fatalf("got %+v", got)
	}
}

// A service entry is replaced by the prefixes it resolves to at compile time.
func TestCompileResolvesServices(t *testing.T) {
	rules := []store.Rule{{ID: 1, Nexthop: routerA.Address, Entries: []store.Entry{
		{Type: EntryService, Value: "Teams"},
		{Type: "ipv6", Value: "2001:db8:9::/48"},
		{Type: EntryService, Value: "Gone"},
	}}}
	groups := []store.Group{group("G", []int{1}, "fe80::1")}
	resolve := func(name string) ([]string, string, error) {
		if name == "Teams" {
			return []string{"2603:1027::/48", "2001:DB8:9::/48", "not-a-prefix"}, "cached", nil
		}
		return nil, "", errors.New("unknown service")
	}

	got, warnings := Compile(rules, groups, testRouters, resolve)

	want := []string{"2001:db8:9::/48", "2603:1027::/48"}
	if msgs := got["a"]; len(msgs) != 1 || !reflect.DeepEqual(msgs[0].Routes, want) {
		t.Fatalf("messages = %+v; want routes %v", got["a"], want)
	}
	joined := strings.Join(warnings, "\n")
	for _, part := range []string{`service "Teams": cached`, `"not-a-prefix"`, `service "Gone" contributes no prefix`} {
		if !strings.Contains(joined, part) {
			t.Errorf("warnings lack %q:\n%s", part, joined)
		}
	}

	if _, warnings := Compile(rules, groups, testRouters, nil); !strings.Contains(strings.Join(warnings, "\n"), "no endpoint source") {
		t.Errorf("without a resolver: %v", warnings)
	}
}
