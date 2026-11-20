package api

import (
	"errors"
	"net/http"

	"controller/backend/internal/fqdn"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) listFqdnServices(w http.ResponseWriter, r *http.Request) {
	if h.FQDN == nil {
		writeJSON(w, http.StatusOK, []fqdn.Service{})
		return
	}
	services, err := h.FQDN.ListServices(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, services)
}

func (h *Handler) getFqdnEndpoints(w http.ResponseWriter, r *http.Request) {
	if h.FQDN == nil {
		writeError(w, http.StatusNotFound, "no endpoint source is configured")
		return
	}
	endpoints, err := h.FQDN.GetEndpoints(r.Context(), chi.URLParam(r, "name"))
	switch {
	case errors.Is(err, fqdn.ErrUnknownService):
		writeError(w, http.StatusNotFound, err.Error())
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		writeJSON(w, http.StatusOK, endpoints)
	}
}
