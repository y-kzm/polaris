package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"controller/backend/internal/store"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) listRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.rules.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

func (h *Handler) createRule(w http.ResponseWriter, r *http.Request) {
	var rule store.Rule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if rule.Nexthop == "" {
		writeError(w, http.StatusBadRequest, "nexthop is required")
		return
	}
	if rule.Entries == nil {
		rule.Entries = []store.Entry{}
	}
	id, err := h.rules.Create(&rule)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	created, _ := h.rules.Get(id)
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.rules.Delete(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
