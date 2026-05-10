package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"controller/backend/internal/store"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.groups.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	var group store.Group
	if err := json.NewDecoder(r.Body).Decode(&group); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if group.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	group.Rules = []int{}
	if group.Members == nil {
		group.Members = []string{}
	}
	id, err := h.groups.Create(&group)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	created, _ := h.groups.Get(id)
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) updateGroupRules(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Rules []int `json:"rules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Rules == nil {
		body.Rules = []int{}
	}
	if err := h.groups.UpdateRules(id, body.Rules); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	updated, _ := h.groups.Get(id)
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.groups.Delete(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
