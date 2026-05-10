// Package engine computes the effective policy from rules and groups, then
// pushes the resulting interface configuration to each affected router via gRPC.
package engine

import (
	"context"
	"fmt"
	"log"

	gorav1 "controller/backend/api/gora/v1"
	"controller/backend/internal/gora"
	"controller/backend/internal/store"
)

// defaultRouteLifetime is 30 minutes, matching go-ra's built-in default.
// Route lifetime is not yet exposed as a per-rule config option.
const defaultRouteLifetime = 1800

// ApplyResult records per-rule deployment outcome.
type ApplyResult struct {
	RuleID  int    `json:"rule_id"`
	Router  string `json:"router"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// Apply computes and pushes the full policy to all affected routers.
func Apply(ctx context.Context, rules []store.Rule, groups []store.Group, routers []store.Router) []ApplyResult {
	// Index by both Address (hostname) and ID (UUID) so the Nexthop field
	// can hold either form.
	routerByRef := make(map[string]*store.Router, len(routers)*2)
	for i := range routers {
		routerByRef[routers[i].Address] = &routers[i]
		routerByRef[routers[i].ID] = &routers[i]
	}

	// Aggregate members per rule across all groups
	memberSet := make(map[int]map[string]struct{})
	for _, g := range groups {
		for _, rID := range g.Rules {
			if memberSet[rID] == nil {
				memberSet[rID] = make(map[string]struct{})
			}
			for _, m := range g.Members {
				memberSet[rID][m] = struct{}{}
			}
		}
	}

	var results []ApplyResult

	for _, rule := range rules {
		router := routerByRef[rule.Nexthop]
		if router == nil {
			results = append(results, ApplyResult{
				RuleID: rule.ID, Router: rule.Nexthop,
				Success: false, Error: "router not found",
			})
			continue
		}

		client, err := gora.New(router.Address, gora.DefaultPort)
		if err != nil {
			results = append(results, ApplyResult{
				RuleID: rule.ID, Router: router.Address,
				Success: false, Error: fmt.Sprintf("connect: %v", err),
			})
			continue
		}

		members := make([]string, 0, len(memberSet[rule.ID]))
		for m := range memberSet[rule.ID] {
			members = append(members, m)
		}

		cfg := buildInterfaceConfig(&rule, router, members)
		if err := client.Upsert(ctx, cfg); err != nil {
			results = append(results, ApplyResult{
				RuleID: rule.ID, Router: router.Address,
				Success: false, Error: err.Error(),
			})
			log.Printf("engine: rule %d → %s: %v", rule.ID, router.Address, err)
		} else {
			results = append(results, ApplyResult{
				RuleID: rule.ID, Router: router.Address, Success: true,
			})
			log.Printf("engine: rule %d → %s: OK (%d clients)", rule.ID, router.Address, len(members))
		}
		client.Close()
	}

	return results
}

func buildInterfaceConfig(rule *store.Rule, router *store.Router, members []string) *gorav1.InterfaceConfig {
	hasClients := len(members) > 0
	cfg := &gorav1.InterfaceConfig{
		Id:                         int32(rule.ID),
		Name:                       router.Interface,
		RaIntervalMilliseconds:     router.RAIntervalMs,
		CurrentHopLimit:            router.CurrentHopLimit,
		Managed:                    router.Managed,
		Other:                      router.Other,
		RouterLifetimeSeconds:      router.RouterLifetimeS,
		ReachableTimeMilliseconds:  int64(router.ReachableTimeMs),
		RetransmitTimeMilliseconds: int64(router.RetransmitTimeMs),
		Clients:                    members,
		Preference:                 "medium",
		// Suppress responses to Router Solicitation when the RA is targeted
		// at specific clients — otherwise unicast policies would leak to
		// unrelated hosts via RS-triggered RAs.
		DisableRsReply: hasClients,
	}

	// Encode the rule's destination set into RA fields:
	//   ::/0     -> raise DRP to "high" so this router becomes the preferred
	//               default gateway for the targeted clients.
	//   anything else -> emit as a Route Information Option (RIO) so only that
	//               specific prefix is routed via this router.
	for _, e := range rule.Entries {
		if e.Value == "::/0" {
			cfg.Preference = "high"
		} else {
			cfg.Routes = append(cfg.Routes, &gorav1.RouteConfig{
				Prefix:          e.Value,
				LifetimeSeconds: defaultRouteLifetime,
				Preference:      "medium",
			})
		}
	}

	return cfg
}
