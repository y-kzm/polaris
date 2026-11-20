package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"

	"controller/backend/internal/engine"
	"controller/backend/internal/fqdn"
	"controller/backend/internal/store"
)

// applyTimeout bounds one apply. It covers the gRPC timeouts of two phases.
const applyTimeout = 60 * time.Second

// draft is the policy as it is being edited.
type draft struct {
	rules   []store.Rule
	groups  []store.Group
	routers []store.Router
}

func (h *Handler) loadDraft() (*draft, error) {
	rules, err := h.Rules.List()
	if err != nil {
		return nil, fmt.Errorf("load rules: %w", err)
	}
	groups, err := h.Groups.List()
	if err != nil {
		return nil, fmt.Errorf("load groups: %w", err)
	}
	routers, err := h.Routers.List()
	if err != nil {
		return nil, fmt.Errorf("load routers: %w", err)
	}
	return &draft{rules, groups, routers}, nil
}

// resolver returns the function that turns a service entry into prefixes.
// A source that does not answer is replaced by the result of the last
// successful lookup, so an outage does not remove a service's routes.
func (h *Handler) resolver(ctx context.Context) engine.Resolver {
	type answer struct {
		prefixes []string
		note     string
		err      error
	}
	memo := map[string]answer{} // one lookup per service and compile
	// After the sources failed once, the rest of this compile uses the
	// cache: a source that does not answer must not use up the time the
	// apply needs for the routers.
	var sourcesDown error
	return func(name string) ([]string, string, error) {
		if a, ok := memo[name]; ok {
			return a.prefixes, a.note, a.err
		}
		a := answer{}
		a.prefixes, a.note, a.err = h.resolveService(ctx, name, &sourcesDown)
		memo[name] = a
		return a.prefixes, a.note, a.err
	}
}

// lookupTimeout bounds the lookup of one service at the endpoint sources.
const lookupTimeout = 8 * time.Second

func (h *Handler) resolveService(ctx context.Context, name string, sourcesDown *error) ([]string, string, error) {
	lookupErr := errors.New("no endpoint source is configured")
	if *sourcesDown != nil {
		lookupErr = *sourcesDown
	} else if h.FQDN != nil {
		lookupCtx, cancel := context.WithTimeout(ctx, lookupTimeout)
		endpoints, err := h.FQDN.GetEndpoints(lookupCtx, name)
		cancel()
		switch {
		case err == nil:
			prefixes := canonicalPrefixes(endpoints.IPv6Prefixes)
			if !endpoints.Incomplete {
				if err := h.Policy.PutServicePrefixes(name, prefixes); err != nil {
					log.Printf("policy: cache prefixes of service %q: %v", name, err)
				}
				if len(prefixes) == 0 {
					return prefixes, "the service has no IPv6 prefix", nil
				}
				return prefixes, "", nil
			}
			// Some source did not answer. Dropping its prefixes now would
			// withdraw routes because of an outage, and advertise them again
			// when it returns; keep what was known.
			cached, _, _, cerr := h.Policy.GetServicePrefixes(name)
			if cerr != nil {
				return nil, "", cerr
			}
			return canonicalPrefixes(append(prefixes, cached...)),
				"an endpoint source did not answer; its last known prefixes are kept", nil
		case errors.Is(err, fqdn.ErrUnknownService):
			return nil, "", err
		default:
			lookupErr, *sourcesDown = err, err
		}
	}
	cached, resolvedAt, ok, err := h.Policy.GetServicePrefixes(name)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", fmt.Errorf("%w, and the service was never resolved before", lookupErr)
	}
	return cached, fmt.Sprintf("using the prefixes resolved at %s (%v)", resolvedAt, lookupErr), nil
}

// canonicalPrefixes returns the valid IPv6 prefixes in canonical form,
// sorted and without duplicates.
func canonicalPrefixes(prefixes []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		canonical, err := engine.ParsePrefix(p)
		if err != nil {
			continue
		}
		if _, dup := seen[canonical]; !dup {
			seen[canonical] = struct{}{}
			out = append(out, canonical)
		}
	}
	sort.Strings(out)
	return out
}

// flag reports whether a query parameter is switched on ("1", "true", …).
func flag(r *http.Request, name string) bool {
	on, _ := strconv.ParseBool(r.URL.Query().Get(name))
	return on
}

// run executes one engine.Apply. The caller holds applyMu.
func (h *Handler) run(parent context.Context, rules []store.Rule, groups []store.Group, routers []store.Router, opts engine.Options) engine.Result {
	// An apply must not stop between its phases because the client went
	// away, so it does not inherit the cancellation of the request.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), applyTimeout)
	defer cancel()
	opts.Resolve = h.resolver(ctx)
	return engine.Apply(ctx, h.Dial, rules, groups, routers, opts)
}

// planPolicy reports what applying the draft would do, without doing it.
func (h *Handler) planPolicy(w http.ResponseWriter, r *http.Request) {
	h.applyMu.Lock()
	defer h.applyMu.Unlock()

	d, err := h.loadDraft()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	applied, err := h.Policy.LoadApplied()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load applied policy: "+err.Error())
		return
	}
	result := h.run(r.Context(), d.rules, d.groups, d.routers, engine.Options{DryRun: true, Messages: true, Quiet: true})

	appliedAt := ""
	if applied != nil {
		appliedAt = applied.AppliedAt
	}
	writeJSON(w, http.StatusOK, map[string]any{
		// Pending: the routers do not run the draft, or the draft was edited
		// since the last apply (the two can differ while a router is down).
		"pending":    result.Changes() > 0 || !samePolicy(applied, d),
		"applied_at": appliedAt,
		"warnings":   result.Warnings,
		"routers":    result.Results,
	})
}

func (h *Handler) applyPolicy(w http.ResponseWriter, r *http.Request) {
	h.applyMu.Lock()
	defer h.applyMu.Unlock()

	d, err := h.loadDraft()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// ?dry_run=1 reports what would change without touching the routers.
	dryRun := flag(r, "dry_run")
	result := h.run(r.Context(), d.rules, d.groups, d.routers, engine.Options{DryRun: dryRun})

	response := struct {
		engine.Result
		AppliedAt string `json:"applied_at,omitempty"`
	}{Result: result}
	// The draft becomes the applied policy only once every router runs it.
	// Until then the periodic reconcile keeps enforcing the previous one.
	if result.Success && !dryRun {
		appliedAt, err := h.Policy.SaveApplied(d.rules, d.groups)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "the routers were updated, but saving the applied policy failed: "+err.Error())
			return
		}
		response.AppliedAt = appliedAt
	}

	status := http.StatusOK
	if !result.Success {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, response)
}

// samePolicy reports whether the draft equals the applied policy.
func samePolicy(applied *store.AppliedPolicy, d *draft) bool {
	if applied == nil {
		return len(d.rules) == 0 && len(d.groups) == 0
	}
	type rule struct {
		ID      int
		Nexthop string
		Entries []store.Entry
	}
	type group struct {
		ID      int
		Rules   []int
		Members []string
	}
	shape := func(rules []store.Rule, groups []store.Group) string {
		rs := make([]rule, 0, len(rules))
		for _, r := range rules {
			rs = append(rs, rule{r.ID, r.Nexthop, r.Entries})
		}
		gs := make([]group, 0, len(groups))
		for _, g := range groups {
			gs = append(gs, group{g.ID, g.Rules, g.Members})
		}
		b, _ := json.Marshal(struct {
			Rules  []rule
			Groups []group
		}{rs, gs})
		return string(b)
	}
	return shape(applied.Rules, applied.Groups) == shape(d.rules, d.groups)
}

// RunReconciler applies the applied policy again at every interval. It
// restores a router whose agent was restarted, follows the prefixes of
// services, and completes a break phase that had to be skipped. It works from
// the applied policy, so edits that were not applied yet are never pushed by
// the timer. It returns when ctx is cancelled.
func (h *Handler) RunReconciler(ctx context.Context, interval time.Duration) {
	log.Printf("reconciler: every %s", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastProblem := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		problem := h.reconcileOnce(ctx)
		if problem != lastProblem { // log a persisting problem once, not every interval
			if problem != "" {
				log.Printf("reconciler: %s", problem)
			} else {
				log.Printf("reconciler: recovered")
			}
			lastProblem = problem
		}
	}
}

func (h *Handler) reconcileOnce(ctx context.Context) string {
	h.applyMu.Lock()
	defer h.applyMu.Unlock()

	applied, err := h.Policy.LoadApplied()
	if err != nil {
		return "load applied policy: " + err.Error()
	}
	if applied == nil {
		return "" // nothing was ever applied
	}
	routers, err := h.Routers.List()
	if err != nil {
		return "load routers: " + err.Error()
	}
	result := h.run(ctx, applied.Rules, applied.Groups, routers, engine.Options{Quiet: true})
	if result.Success {
		return ""
	}
	problem := ""
	for _, res := range result.Results {
		if res.Error != "" {
			problem += fmt.Sprintf("%s: %s; ", res.Router, res.Error)
		}
	}
	return problem
}
