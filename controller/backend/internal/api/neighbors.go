package api

import (
	"net/http"

	"controller/backend/internal/fqdn"
	"controller/backend/internal/neighbor"
)

func (h *Handler) listNeighbors(w http.ResponseWriter, r *http.Request) {
	neighbors, err := h.Neighbors.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, neighbors)
}

func (h *Handler) refreshNeighbors(w http.ResponseWriter, r *http.Request) {
	if h.Watcher == nil {
		writeError(w, http.StatusServiceUnavailable, "no neighbor source is configured")
		return
	}
	if err := h.Watcher.Poll(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.listNeighbors(w, r)
}

// listNeighborSources returns the names of the neighbor sources.
// Kept for clients of the first API version; GET /sources says more.
func (h *Handler) listNeighborSources(w http.ResponseWriter, _ *http.Request) {
	names := []string{}
	if h.Watcher != nil {
		for _, s := range h.Watcher.Statuses() {
			names = append(names, s.Name)
		}
	}
	writeJSON(w, http.StatusOK, map[string][]string{"sources": names})
}

// listSources reports the health of every neighbor and endpoint source.
func (h *Handler) listSources(w http.ResponseWriter, _ *http.Request) {
	neighbors := []neighbor.SourceStatus{}
	if h.Watcher != nil {
		neighbors = h.Watcher.Statuses()
	}
	endpoints := []fqdn.SourceStatus{}
	if h.FQDN != nil {
		endpoints = h.FQDN.Statuses()
	}
	writeJSON(w, http.StatusOK, map[string]any{"neighbors": neighbors, "endpoints": endpoints})
}
