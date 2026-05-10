package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"controller/backend/internal/gora"
	"controller/backend/internal/store"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// routerIfaceStatus is the per-interface status returned by the go-ra agent.
type routerIfaceStatus struct {
	Name            string `json:"name"`
	ID              int32  `json:"id"`
	State           string `json:"state"`
	TxSolicitedRA   int32  `json:"tx_solicited_ra"`
	TxUnsolicitedRA int32  `json:"tx_unsolicited_ra"`
}

// routerStatusEntry is the per-router entry in the allRouterStatuses response.
type routerStatusEntry struct {
	Name       string              `json:"name"`
	Address    string              `json:"address"`
	Reachable  bool                `json:"reachable"`
	Error      string              `json:"error,omitempty"`
	Interfaces []routerIfaceStatus `json:"interfaces"`
}

func (h *Handler) listRouters(w http.ResponseWriter, r *http.Request) {
	routers, err := h.routers.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, routers)
}

// allRouterStatuses queries every router in parallel and returns
// Record<routerID, RouterStatus> matching the frontend's type.
func (h *Handler) allRouterStatuses(w http.ResponseWriter, r *http.Request) {
	routers, err := h.routers.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	result := make(map[string]routerStatusEntry, len(routers))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, router := range routers {
		wg.Add(1)
		go func(rtr store.Router) {
			defer wg.Done()
			st := routerStatusEntry{
				Name:       rtr.Name,
				Address:    rtr.Address,
				Interfaces: []routerIfaceStatus{},
			}
			client, err := gora.New(rtr.Address, gora.DefaultPort)
			if err != nil {
				st.Error = err.Error()
				mu.Lock()
				result[rtr.ID] = st
				mu.Unlock()
				return
			}
			defer client.Close()
			ifaces, err := client.GetStatus(context.Background())
			if err != nil {
				st.Error = err.Error()
			} else {
				st.Reachable = true
				for _, iface := range ifaces {
					st.Interfaces = append(st.Interfaces, routerIfaceStatus{
						Name:            iface.Name,
						ID:              iface.Id,
						State:           iface.State,
						TxSolicitedRA:   iface.TxSolicitedRa,
						TxUnsolicitedRA: iface.TxUnsolicitedRa,
					})
				}
			}
			mu.Lock()
			result[rtr.ID] = st
			mu.Unlock()
		}(router)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) createRouter(w http.ResponseWriter, r *http.Request) {
	var router store.Router
	if err := json.NewDecoder(r.Body).Decode(&router); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if router.Name == "" || router.Address == "" {
		writeError(w, http.StatusBadRequest, "name and address are required")
		return
	}
	if router.ID == "" {
		router.ID = uuid.New().String()
	}
	if router.Status == "" {
		router.Status = "active"
	}
	if router.RAIntervalMs == 0 {
		router.RAIntervalMs = 3000
	}
	if router.CurrentHopLimit == 0 {
		router.CurrentHopLimit = 64
	}
	if router.RouterLifetimeS == 0 {
		router.RouterLifetimeS = 1800
	}
	if err := h.routers.Create(&router); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	created, _ := h.routers.Get(router.ID)
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) updateRouter(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := h.routers.Get(id)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "router not found")
		return
	}
	var update store.Router
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	update.ID = id
	if err := h.routers.Update(&update); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, _ := h.routers.Get(id)
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) deleteRouter(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.routers.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listRouterInterfaces returns the currently running InterfaceConfig list from
// the router's go-ra agent via the ListInterfaces gRPC call.
func (h *Handler) listRouterInterfaces(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	router, err := h.routers.Get(id)
	if err != nil || router == nil {
		writeError(w, http.StatusNotFound, "router not found")
		return
	}
	client, err := gora.New(router.Address, gora.DefaultPort)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer client.Close()
	ifaces, err := client.ListInterfaces(context.Background())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ifaces)
}

func (h *Handler) routerStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	router, err := h.routers.Get(id)
	if err != nil || router == nil {
		writeError(w, http.StatusNotFound, "router not found")
		return
	}
	client, err := gora.New(router.Address, gora.DefaultPort)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer client.Close()
	statuses, err := client.GetStatus(context.Background())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"connected": false,
			"error":     err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"connected":  true,
		"interfaces": statuses,
	})
}
