package api

import (
	"fmt"
	"strings"

	"controller/backend/internal/engine"
	"controller/backend/internal/store"
)

// Validation happens here, once: what is stored is canonical, so the rest of
// the system (and the UI) can compare addresses and prefixes as strings.

// normalizeEntries validates the entries of a rule and returns them in
// canonical form without duplicates.
func normalizeEntries(entries []store.Entry) ([]store.Entry, error) {
	out := make([]store.Entry, 0, len(entries))
	seen := map[string]struct{}{}
	for _, e := range entries {
		value := strings.TrimSpace(e.Value)
		switch e.Type {
		case engine.EntryService:
			if value == "" {
				return nil, fmt.Errorf("a service entry needs the name of the service")
			}
			e = store.Entry{Type: engine.EntryService, Value: value}
		case "", "ipv6":
			prefix, err := engine.ParsePrefix(value)
			if err != nil {
				return nil, fmt.Errorf("%q is not an IPv6 prefix", e.Value)
			}
			e = store.Entry{Type: "ipv6", Value: prefix, Service: e.Service}
		default:
			return nil, fmt.Errorf("entry type %q is not supported: use \"ipv6\" (a prefix) or \"service\" (a service name)", e.Type)
		}
		key := e.Type + "\x00" + e.Value
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, e)
	}
	return out, nil
}

// normalizeMembers validates the members of a group: link-local unicast
// addresses, returned in canonical form without duplicates.
func normalizeMembers(members []string) ([]string, error) {
	out := make([]string, 0, len(members))
	seen := map[string]struct{}{}
	for _, m := range members {
		host, ok := engine.CanonicalHost(m)
		if !ok {
			return nil, fmt.Errorf("%q is not a link-local IPv6 address (fe80::/10); hosts are identified by their link-local address", m)
		}
		if _, dup := seen[host]; dup {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}
	return out, nil
}

// routerOf returns the registered router a rule's next hop refers to (by
// address or by ID), or nil.
func routerOf(routers []store.Router, nexthop string) *store.Router {
	for i := range routers {
		if nexthop != "" && (routers[i].Address == nexthop || routers[i].ID == nexthop) {
			return &routers[i]
		}
	}
	return nil
}

// validateRule checks a rule against the router registry and normalises it.
func (h *Handler) validateRule(rule *store.Rule) error {
	rule.Nexthop = strings.TrimSpace(rule.Nexthop)
	if rule.Nexthop == "" {
		return fmt.Errorf("nexthop is required: the address of a registered router")
	}
	routers, err := h.Routers.List()
	if err != nil {
		return err
	}
	if routerOf(routers, rule.Nexthop) == nil {
		return fmt.Errorf("nexthop %q is not a registered router", rule.Nexthop)
	}
	entries, err := normalizeEntries(rule.Entries)
	if err != nil {
		return err
	}
	rule.Entries = entries
	return nil
}

// validateRuleIDs checks that every ID names an existing rule and removes
// duplicates.
func (h *Handler) validateRuleIDs(ids []int) ([]int, error) {
	rules, err := h.Rules.List()
	if err != nil {
		return nil, err
	}
	known := make(map[int]struct{}, len(rules))
	for _, r := range rules {
		known[r.ID] = struct{}{}
	}
	out := make([]int, 0, len(ids))
	seen := map[int]struct{}{}
	for _, id := range ids {
		if _, ok := known[id]; !ok {
			return nil, fmt.Errorf("rule %d does not exist", id)
		}
		if _, dup := seen[id]; !dup {
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out, nil
}
