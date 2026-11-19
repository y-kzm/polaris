package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"controller/backend/internal/engine"
	"controller/backend/internal/gora"
	"controller/backend/internal/store"
)

// applyTimeout bounds one apply. It covers the gRPC timeouts of two phases.
const applyTimeout = 60 * time.Second

// applyMu serialises applies: two concurrent ones would plan against the
// same running state and interleave their phases.
var applyMu sync.Mutex

func (h *Handler) applyPolicy(w http.ResponseWriter, r *http.Request) {
	applyMu.Lock()
	defer applyMu.Unlock()

	rules, err := h.rules.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load rules: "+err.Error())
		return
	}
	groups, err := h.groups.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load groups: "+err.Error())
		return
	}
	routers, err := h.routers.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load routers: "+err.Error())
		return
	}

	// ?dry_run=1 reports what would change without touching the routers.
	dryRun := r.URL.Query().Get("dry_run") != ""
	// An apply must not stop between its phases because the client went
	// away, so it does not inherit the cancellation of the request.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), applyTimeout)
	defer cancel()
	result := engine.Apply(ctx, dialAgent, rules, groups, routers, dryRun)

	status := http.StatusOK
	if !result.Success {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

func dialAgent(router *store.Router) (engine.Agent, error) {
	return gora.New(router.Address, gora.DefaultPort)
}
