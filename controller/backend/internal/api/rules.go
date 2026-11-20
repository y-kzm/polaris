package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"controller/backend/internal/store"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) listRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.Rules.List()
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
	if err := h.validateRule(&rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := h.Rules.Create(&rule)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	created, _ := h.Rules.Get(id)
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) updateRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if existing, err := h.Rules.Get(id); err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "rule not found")
		return
	}
	var rule store.Rule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rule.ID = id
	if err := h.validateRule(&rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.Rules.Update(&rule); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, _ := h.Rules.Get(id)
	writeJSON(w, http.StatusOK, updated)
}

// deleteRule also removes the rule from every group that uses it.
func (h *Handler) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Rules.Delete(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
