package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"controller/backend/internal/engine"
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
	routers, err := h.Routers.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, routers)
}

// allRouterStatuses queries every router in parallel and returns
// Record<routerID, RouterStatus> matching the frontend's type.
func (h *Handler) allRouterStatuses(w http.ResponseWriter, r *http.Request) {
	routers, err := h.Routers.List()
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

// validateRouter normalises a router in place and checks it against the
// registry. id is the router being updated, "" for a new one.
func (h *Handler) validateRouter(router *store.Router, id string) error {
	router.Name, router.Address = strings.TrimSpace(router.Name), strings.TrimSpace(router.Address)
	if router.Name == "" {
		return errRequired("name")
	}
	if router.Address == "" {
		return errRequired("address")
	}
	switch router.Status {
	case "":
		router.Status = store.StatusActive
	case store.StatusActive, store.StatusInactive:
	default:
		return fmt.Errorf("status must be %q or %q", store.StatusActive, store.StatusInactive)
	}
	if err := normalizePolicySourceAddress(router); err != nil {
		return err
	}
	existing, err := h.Routers.List()
	if err != nil {
		return err
	}
	for _, other := range existing {
		if other.ID == id {
			continue
		}
		if other.Name == router.Name {
			return fmt.Errorf("a router named %q already exists", router.Name)
		}
		// One agent is one router: two records for it would each treat the
		// agent's policy configurations as their own and undo each other.
		if sameAddress(other.Address, router.Address) {
			return fmt.Errorf("%s is already registered with the address %s", other.Name, other.Address)
		}
	}
	applyRouterDefaults(router)
	return nil
}

func (h *Handler) createRouter(w http.ResponseWriter, r *http.Request) {
	var router store.Router
	if err := json.NewDecoder(r.Body).Decode(&router); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.validateRouter(&router, ""); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	router.ID = uuid.New().String() // never taken from the client
	if err := h.Routers.Create(&router); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	created, _ := h.Routers.Get(router.ID)
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) updateRouter(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := h.Routers.Get(id)
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
	if update.Status == "" {
		update.Status = existing.Status
	}
	if err := h.validateRouter(&update, id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The whole change is one step for applies and the reconciler.
	h.applyMu.Lock()
	defer h.applyMu.Unlock()
	if err := h.Routers.Update(&update); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if update.Address != existing.Address {
		// Rules name their router by address. They follow the router to its
		// new address — in the draft and in the applied policy — or the next
		// reconcile would drop them as "not a registered router".
		if err := errors.Join(
			h.Rules.RenameNexthop(existing.Address, update.Address),
			h.Policy.RenameNexthop(existing.Address, update.Address),
		); err != nil {
			writeError(w, http.StatusInternalServerError, "the address was changed, but updating the rules failed: "+err.Error())
			return
		}
	}
	updated, _ := h.Routers.Get(id)
	writeJSON(w, http.StatusOK, updated)
}

// applyRouterDefaults fills in the RA parameters the agent requires. A zero
// Router Lifetime in particular cannot be combined with a high preference.
func applyRouterDefaults(router *store.Router) {
	if router.RAIntervalMs == 0 {
		router.RAIntervalMs = 3000
	}
	if router.CurrentHopLimit == 0 {
		router.CurrentHopLimit = 64
	}
	if router.RouterLifetimeS == 0 {
		router.RouterLifetimeS = 1800
	}
}

// normalizePolicySourceAddress validates the optional policy source address
// and stores it in canonical form.
func normalizePolicySourceAddress(router *store.Router) error {
	s := strings.TrimSpace(router.PolicySourceAddress)
	if s == "" {
		router.PolicySourceAddress = ""
		return nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil || !addr.Is6() || !addr.IsLinkLocalUnicast() {
		return fmt.Errorf("policy_source_address must be a link-local IPv6 address (fe80::/10)")
	}
	router.PolicySourceAddress = addr.WithZone("").String()
	return nil
}

// decommissionTimeout bounds the withdrawal of a router's policy state.
const decommissionTimeout = 20 * time.Second

// deleteRouter decommissions a router: its policy state is withdrawn from the
// hosts before it leaves the registry, because afterwards no apply would
// ever reach it again.
func (h *Handler) deleteRouter(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	router, err := h.Routers.Get(id)
	if err != nil || router == nil {
		writeError(w, http.StatusNotFound, "router not found")
		return
	}
	h.applyMu.Lock()
	defer h.applyMu.Unlock()

	uses := func(rules []store.Rule) []string {
		var ids []string
		for _, rule := range rules {
			if rule.Nexthop == router.Address || rule.Nexthop == router.ID {
				ids = append(ids, fmt.Sprintf("#%d", rule.ID))
			}
		}
		return ids
	}
	rules, err := h.Rules.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if using := uses(rules); len(using) > 0 {
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"%s is the next hop of rule(s) %s; delete or change them first", router.Name, strings.Join(using, ", ")))
		return
	}
	// The hosts follow the applied policy, not the draft. If it still sends
	// them through this router, withdrawing now would leave them without the
	// routes before any other router advertises them.
	applied, err := h.Policy.LoadApplied()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if applied != nil {
		if using := uses(applied.Rules); len(using) > 0 {
			writeError(w, http.StatusConflict, fmt.Sprintf(
				"the applied policy still sends hosts through %s (rule(s) %s). Apply the current policy first, then delete the router",
				router.Name, strings.Join(using, ", ")))
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), decommissionTimeout)
	defer cancel()
	withdrawn, derr := engine.Decommission(ctx, h.Dial, router)
	force := flag(r, "force")
	if derr != nil && !force {
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"the policy state of %s could not be withdrawn (%v). Fix the connection and retry, or delete with force=1 "+
				"to remove it anyway; hosts may then keep its policy routes until their lifetimes expire",
			router.Name, derr))
		return
	}
	if err := h.Routers.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	response := map[string]any{"withdrawn_hosts": withdrawn}
	if derr != nil {
		response["warning"] = fmt.Sprintf("removed without withdrawing its policy state: %v", derr)
	}
	writeJSON(w, http.StatusOK, response)
}

// listRouterInterfaces returns the currently running InterfaceConfig list from
// the router's go-ra agent via the ListInterfaces gRPC call.
func (h *Handler) listRouterInterfaces(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	router, err := h.Routers.Get(id)
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
	router, err := h.Routers.Get(id)
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

// sameAddress reports whether two router addresses name the same host:
// IP addresses are compared by value, anything else as text.
func sameAddress(a, b string) bool {
	ipA, errA := netip.ParseAddr(strings.Trim(a, "[]"))
	ipB, errB := netip.ParseAddr(strings.Trim(b, "[]"))
	if errA == nil && errB == nil {
		return ipA.WithZone("") == ipB.WithZone("")
	}
	return strings.EqualFold(a, b)
}

func errRequired(field string) error { return fmt.Errorf("%s is required", field) }
