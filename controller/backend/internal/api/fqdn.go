package api

import (
	"net/http"

	"controller/backend/internal/fqdn"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) listFqdnServices(w http.ResponseWriter, r *http.Request) {
	if h.fqdn == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	services, err := h.fqdn.ListServices(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, services)
}

func (h *Handler) getFqdnEndpoints(w http.ResponseWriter, r *http.Request) {
	if h.fqdn == nil {
		writeJSON(w, http.StatusOK, fqdn.Endpoints{})
		return
	}
	name := chi.URLParam(r, "name")
	endpoints, err := h.fqdn.GetEndpoints(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, endpoints)
}
