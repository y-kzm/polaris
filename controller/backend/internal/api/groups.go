package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"controller/backend/internal/store"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.Groups.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

// validateGroup normalises a group in place.
func (h *Handler) validateGroup(group *store.Group) error {
	group.Name = strings.TrimSpace(group.Name)
	if group.Name == "" {
		return errRequired("name")
	}
	members, err := normalizeMembers(group.Members)
	if err != nil {
		return err
	}
	rules, err := h.validateRuleIDs(group.Rules)
	if err != nil {
		return err
	}
	group.Members, group.Rules = members, rules
	return nil
}

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	var group store.Group
	if err := json.NewDecoder(r.Body).Decode(&group); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.validateGroup(&group); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := h.Groups.Create(&group)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	created, _ := h.Groups.Get(id)
	writeJSON(w, http.StatusCreated, created)
}

// updateGroup replaces the name, members and rules of a group.
func (h *Handler) updateGroup(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if existing, err := h.Groups.Get(id); err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "group not found")
		return
	}
	var group store.Group
	if err := json.NewDecoder(r.Body).Decode(&group); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	group.ID = id
	if err := h.validateGroup(&group); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.Groups.Update(&group); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, _ := h.Groups.Get(id)
	writeJSON(w, http.StatusOK, updated)
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
	rules, err := h.validateRuleIDs(body.Rules)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.Groups.UpdateRules(id, rules); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	updated, _ := h.Groups.Get(id)
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Groups.Delete(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
