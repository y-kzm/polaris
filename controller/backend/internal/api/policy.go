package api

import (
	"net/http"

	"controller/backend/internal/engine"
)

func (h *Handler) applyPolicy(w http.ResponseWriter, r *http.Request) {
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

	results := engine.Apply(r.Context(), rules, groups, routers)

	allOK := true
	for _, res := range results {
		if !res.Success {
			allOK = false
			break
		}
	}

	status := http.StatusOK
	if !allOK {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, map[string]any{
		"results": results,
		"success": allOK,
	})
}
