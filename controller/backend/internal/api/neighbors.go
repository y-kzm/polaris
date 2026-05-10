package api

import "net/http"

func (h *Handler) listNeighbors(w http.ResponseWriter, r *http.Request) {
	neighbors, err := h.neighbors.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, neighbors)
}

func (h *Handler) refreshNeighbors(w http.ResponseWriter, r *http.Request) {
	if h.watcher == nil {
		writeError(w, http.StatusServiceUnavailable, "neighbor watcher not configured")
		return
	}
	if err := h.watcher.Poll(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	neighbors, err := h.neighbors.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, neighbors)
}

func (h *Handler) listNeighborSources(w http.ResponseWriter, _ *http.Request) {
	sources := h.neighborSources
	if sources == nil {
		sources = []string{}
	}
	writeJSON(w, http.StatusOK, map[string][]string{"sources": sources})
}
