// Package api implements the HTTP REST API for the RA Controller.
// All routes are mounted under /api and served by the chi router.
package api

import (
	"encoding/json"
	"net/http"

	"controller/backend/internal/fqdn"
	"controller/backend/internal/neighbor"
	"controller/backend/internal/store"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

type Handler struct {
	routers         *store.RouterStore
	rules           *store.RuleStore
	groups          *store.GroupStore
	neighbors       *store.NeighborStore
	watcher         *neighbor.Watcher
	fqdn            *fqdn.Client
	neighborSources []string
}

func New(
	routers *store.RouterStore,
	rules *store.RuleStore,
	groups *store.GroupStore,
	neighbors *store.NeighborStore,
	watcher *neighbor.Watcher,
	fqdnClient *fqdn.Client,
	neighborSources []string,
) *Handler {
	return &Handler{
		routers:         routers,
		rules:           rules,
		groups:          groups,
		neighbors:       neighbors,
		watcher:         watcher,
		fqdn:            fqdnClient,
		neighborSources: neighborSources,
	}
}

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type"},
	}))

	r.Route("/api", func(r chi.Router) {
		r.Get("/neighbors", h.listNeighbors)
		r.Post("/neighbors/refresh", h.refreshNeighbors)
		r.Get("/neighbor-sources", h.listNeighborSources)

		r.Get("/routers", h.listRouters)
		r.Post("/routers", h.createRouter)
		r.Get("/routers/status", h.allRouterStatuses)
		r.Put("/routers/{id}", h.updateRouter)
		r.Delete("/routers/{id}", h.deleteRouter)
		r.Get("/routers/{id}/status", h.routerStatus)
		r.Get("/routers/{id}/interfaces", h.listRouterInterfaces)

		r.Get("/rules", h.listRules)
		r.Post("/rules", h.createRule)
		r.Delete("/rules/{id}", h.deleteRule)

		r.Get("/groups", h.listGroups)
		r.Post("/groups", h.createGroup)
		r.Put("/groups/{id}/rules", h.updateGroupRules)
		r.Delete("/groups/{id}", h.deleteGroup)

		r.Get("/fqdn/services", h.listFqdnServices)
		r.Get("/fqdn/services/{name}/endpoints", h.getFqdnEndpoints)

		r.Post("/policy/apply", h.applyPolicy)
	})

	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
