// Package api implements the HTTP REST API of the RA controller (contract C4
// of docs/design.md) and serves the web UI.
package api

import (
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"controller/backend/internal/engine"
	"controller/backend/internal/fqdn"
	"controller/backend/internal/gora"
	"controller/backend/internal/neighbor"
	"controller/backend/internal/store"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

// Deps are the things a Handler works with.
type Deps struct {
	Routers   *store.RouterStore
	Rules     *store.RuleStore
	Groups    *store.GroupStore
	Neighbors *store.NeighborStore
	Policy    *store.PolicyStore
	// Watcher and FQDN are nil if no source of that kind is configured.
	Watcher *neighbor.Watcher
	FQDN    *fqdn.Client
	// Dial connects to a router's agent. Nil means gRPC on the default port.
	Dial engine.Dialer
	// StaticDir holds the built web UI; empty serves the API only.
	StaticDir string
	// CORSOrigins are the origins allowed to call the API from a browser.
	// Empty allows only the UI served by this backend (same origin).
	CORSOrigins []string
}

type Handler struct {
	Deps
	// applyMu serialises everything that changes the routers: two applies
	// would plan against the same running state and interleave their phases.
	applyMu sync.Mutex
}

func New(deps Deps) *Handler {
	if deps.Dial == nil {
		deps.Dial = func(router *store.Router) (engine.Agent, error) {
			return gora.New(router.Address, gora.DefaultPort)
		}
	}
	return &Handler{Deps: deps}
}

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	if len(h.CORSOrigins) > 0 {
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins: h.CORSOrigins,
			AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowedHeaders: []string{"Content-Type"},
		}))
	}

	r.Route("/api", func(r chi.Router) {
		r.Use(h.sameSite)
		r.Use(limitBody)
		r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})

		r.Get("/neighbors", h.listNeighbors)
		r.Post("/neighbors/refresh", h.refreshNeighbors)
		r.Get("/neighbor-sources", h.listNeighborSources)
		r.Get("/sources", h.listSources)

		r.Get("/routers", h.listRouters)
		r.Post("/routers", h.createRouter)
		r.Get("/routers/status", h.allRouterStatuses)
		r.Put("/routers/{id}", h.updateRouter)
		r.Delete("/routers/{id}", h.deleteRouter)
		r.Get("/routers/{id}/status", h.routerStatus)
		r.Get("/routers/{id}/interfaces", h.listRouterInterfaces)

		r.Get("/rules", h.listRules)
		r.Post("/rules", h.createRule)
		r.Put("/rules/{id}", h.updateRule)
		r.Delete("/rules/{id}", h.deleteRule)

		r.Get("/groups", h.listGroups)
		r.Post("/groups", h.createGroup)
		r.Put("/groups/{id}", h.updateGroup)
		r.Put("/groups/{id}/rules", h.updateGroupRules)
		r.Delete("/groups/{id}", h.deleteGroup)

		r.Get("/fqdn/services", h.listFqdnServices)
		r.Get("/fqdn/services/{name}/endpoints", h.getFqdnEndpoints)

		r.Get("/policy/plan", h.planPolicy)
		r.Post("/policy/apply", h.applyPolicy)

		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "no such API endpoint")
		})
	})

	if h.StaticDir != "" {
		r.NotFound(h.serveUI)
	}
	return r
}

// serveUI serves the built web UI. Paths that are not files get index.html,
// so that the single-page application can be reloaded on any of its routes.
func (h *Handler) serveUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	// filepath.Join cleans the path, so it cannot leave StaticDir.
	path := filepath.Join(h.StaticDir, filepath.FromSlash("/"+r.URL.Path))
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		path = filepath.Join(h.StaticDir, "index.html")
	}
	if strings.HasSuffix(path, "index.html") {
		// index.html names the hashed bundles; it must not be cached.
		w.Header().Set("Cache-Control", "no-store")
	}
	http.ServeFile(w, r, path)
}

// sameSite refuses requests that change something and come from another web
// site. CORS alone only keeps a foreign page from reading the answer; without
// this check any page the administrator visits could still trigger an apply.
// A request that changes something must also carry JSON, which a plain HTML
// form cannot send.
func (h *Handler) sameSite(next http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(h.CORSOrigins))
	for _, origin := range h.CORSOrigins {
		allowed[origin] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		// Browsers send Origin with every such request; other clients do not.
		if origin := r.Header.Get("Origin"); origin != "" {
			_, listed := allowed[origin]
			_, any := allowed["*"]
			if u, err := url.Parse(origin); !listed && !any && (err != nil || u.Host != r.Host) {
				writeError(w, http.StatusForbidden, "cross-site request refused: "+origin+" is not an allowed origin (cors_origins)")
				return
			}
		}
		if r.ContentLength != 0 {
			if mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mediaType != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "the request body must be application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// limitBody bounds request bodies: the largest legitimate one is a group
// with a few thousand members.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
