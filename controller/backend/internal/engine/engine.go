// Package engine turns the stored rules and groups into Router Advertisement
// configurations and applies them to the site-exit routers.
//
// Compile implements Algorithm 1 of the paper (policy -> control request
// messages). Reconcile compares the messages with what a router is running
// and produces the operations that move it to the new policy. Apply executes
// those operations on all routers in two phases, so that a host always learns
// its new first-hop router before the old one is withdrawn.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	gorav1 "controller/backend/api/gora/v1"
	"controller/backend/internal/store"
)

// Agent is the part of the go-ra gRPC API that the engine uses.
type Agent interface {
	ListInterfaces(ctx context.Context) ([]*gorav1.InterfaceConfig, error)
	AddInterface(ctx context.Context, cfg *gorav1.InterfaceConfig) error
	UpdateInterface(ctx context.Context, cfg *gorav1.InterfaceConfig) error
	DeleteInterface(ctx context.Context, id int32, skipGoodbye bool) error
	SendRA(ctx context.Context, cfg *gorav1.InterfaceConfig, withdrawnRoutes []string) error
	Close()
}

// Dialer connects to the agent of a router.
type Dialer func(router *store.Router) (Agent, error)

// RouterResult is the outcome of applying the policy to one router.
type RouterResult struct {
	Router    string `json:"router"`
	Address   string `json:"address"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
	Added     int    `json:"added"`
	Updated   int    `json:"updated"`
	Deleted   int    `json:"deleted"`
	Unchanged int    `json:"unchanged"`
	// Withdrawn is the number of hosts that were sent a withdrawal RA.
	Withdrawn int `json:"withdrawn"`
}

// Result is the outcome of Apply.
type Result struct {
	Results  []RouterResult `json:"results"`
	Warnings []string       `json:"warnings"`
	Success  bool           `json:"success"`
	DryRun   bool           `json:"dry_run"`
}

type routerJob struct {
	router *store.Router
	agent  Agent
	plan   *Plan
	result *RouterResult
}

// Apply compiles the policy and brings every registered router to it.
//
// Phase 1 (make) installs the new and changed configurations on all routers;
// the agents advertise them immediately. Phase 2 (break) removes the obsolete
// configurations and sends the withdrawal RAs. Because phase 2 starts only
// after phase 1 has succeeded on every reachable router, a host that moves
// from one router to another holds both for a moment, never neither.
//
// Phase 1 never takes anything away from a host. If any router is unreachable
// or fails in phase 1, phase 2 is skipped everywhere: the old policy stays in
// force next to whatever part of the new one could be installed, rather than
// being withdrawn from hosts whose new router is not ready. Applying again
// after the problem is fixed (or after the dead router is removed from the
// registry) completes the transition.
//
// With dryRun the plans are computed and reported but nothing is changed.
func Apply(ctx context.Context, dial Dialer, rules []store.Rule, groups []store.Group, routers []store.Router, dryRun bool) Result {
	desired, warnings := Compile(rules, groups, routers)
	if warnings == nil {
		warnings = []string{}
	}

	jobs := make([]*routerJob, len(routers))
	results := make([]RouterResult, len(routers))
	for i := range routers {
		results[i] = RouterResult{Router: routers[i].Name, Address: routers[i].Address}
		jobs[i] = &routerJob{router: &routers[i], result: &results[i]}
	}
	defer func() {
		for _, j := range jobs {
			if j.agent != nil {
				j.agent.Close()
			}
		}
	}()

	// Phase 0: read the running state and plan.
	forEach(jobs, func(j *routerJob) {
		agent, err := dial(j.router)
		if err != nil {
			j.result.Error = fmt.Sprintf("connect: %v", err)
			return
		}
		j.agent = agent
		running, err := agent.ListInterfaces(ctx)
		if err != nil {
			j.result.Error = fmt.Sprintf("list interfaces: %v", err)
			return
		}
		plan, err := Reconcile(j.router, running, desired[j.router.ID])
		if err != nil {
			j.result.Error = err.Error()
			return
		}
		j.plan = plan
		j.result.Added = len(j.plan.Add)
		j.result.Updated = j.plan.Changed()
		j.result.Deleted = len(j.plan.Delete)
		j.result.Unchanged = j.plan.Unchanged
		j.result.Withdrawn = j.plan.WithdrawnHosts()
	})

	if !dryRun {
		// Phase 1: make.
		forEach(jobs, func(j *routerJob) {
			if j.plan == nil {
				return
			}
			for _, cfg := range j.plan.Add {
				if err := j.agent.AddInterface(ctx, cfg); err != nil {
					j.result.Error = fmt.Sprintf("add config #%d: %v", cfg.Id, err)
					return
				}
			}
			for _, cfg := range j.plan.Update {
				if err := j.agent.UpdateInterface(ctx, cfg); err != nil {
					j.result.Error = fmt.Sprintf("update config #%d: %v", cfg.Id, err)
					return
				}
			}
			// The agent applies a configuration asynchronously. Sending the
			// new RA here, synchronously, guarantees that it is on the wire
			// before phase 2 withdraws anything on another router.
			for _, cfg := range append(append([]*gorav1.InterfaceConfig{}, j.plan.Add...), j.plan.Update...) {
				if err := j.agent.SendRA(ctx, cfg, nil); err != nil {
					j.result.Error = fmt.Sprintf("advertise config #%d: %v", cfg.Id, err)
					return
				}
			}
		})

		// Any failure so far — an unreachable router included — means some
		// host may not have its new first-hop router yet.
		makeFailed := false
		for _, j := range jobs {
			if j.result.Error != "" {
				makeFailed = true
			}
		}

		// Phase 2: break.
		if makeFailed {
			for _, j := range jobs {
				if j.plan == nil {
					continue
				}
				pending := len(j.plan.Shrink)+len(j.plan.Delete)+len(j.plan.Withdraw) > 0
				j.result.Deleted, j.result.Withdrawn = 0, 0
				if j.result.Error == "" && pending {
					j.result.Error = "old policy kept: withdrawal skipped because another router could not take the new policy"
				}
			}
		} else {
			forEach(jobs, func(j *routerJob) {
				var errs []error
				for _, cfg := range j.plan.Shrink {
					if err := j.agent.UpdateInterface(ctx, cfg); err != nil {
						errs = append(errs, fmt.Errorf("update config #%d: %w", cfg.Id, err))
					}
				}
				// The withdrawal RAs below cover everything the deleted
				// configurations advertised, so no goodbye RA is needed.
				for _, id := range j.plan.Delete {
					if err := j.agent.DeleteInterface(ctx, id, true); err != nil {
						errs = append(errs, fmt.Errorf("delete config #%d: %w", id, err))
					}
				}
				// One failed withdrawal must not prevent the others.
				for _, w := range j.plan.Withdraw {
					if err := j.agent.SendRA(ctx, w.Config, w.Routes); err != nil {
						errs = append(errs, fmt.Errorf("withdraw from %d host(s): %w", len(w.Config.Clients), err))
					}
				}
				if err := errors.Join(errs...); err != nil {
					j.result.Error = err.Error()
				}
			})
		}
	}

	success := true
	for i := range results {
		results[i].Success = results[i].Error == ""
		if !results[i].Success {
			success = false
			log.Printf("engine: %s (%s): %s", results[i].Router, results[i].Address, results[i].Error)
			continue
		}
		log.Printf("engine: %s (%s): +%d ~%d -%d =%d, %d host(s) withdrawn (dry-run=%t)",
			results[i].Router, results[i].Address, results[i].Added, results[i].Updated,
			results[i].Deleted, results[i].Unchanged, results[i].Withdrawn, dryRun)
	}
	return Result{Results: results, Warnings: warnings, Success: success, DryRun: dryRun}
}

// forEach runs f for every job concurrently and waits for all of them. An
// unreachable router costs a timeout, so the routers are handled in parallel.
func forEach(jobs []*routerJob, f func(*routerJob)) {
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j *routerJob) {
			defer wg.Done()
			f(j)
		}(j)
	}
	wg.Wait()
}
