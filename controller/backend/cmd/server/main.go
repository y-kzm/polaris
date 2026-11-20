// Command server is the RA controller backend: the REST API, the policy
// engine and, if STATIC_DIR is set, the web UI.
//
// Configuration comes from environment variables only (see loadConfig), so
// that the process can be started alone, by server.sh or by a container.
package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"controller/backend/internal/api"
	"controller/backend/internal/fqdn"
	"controller/backend/internal/neighbor"
	"controller/backend/internal/store"
)

func main() {
	cfg := loadConfig()

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	deps := api.Deps{
		Routers:     store.NewRouterStore(db),
		Rules:       store.NewRuleStore(db),
		Groups:      store.NewGroupStore(db),
		Neighbors:   store.NewNeighborStore(db),
		Policy:      store.NewPolicyStore(db),
		StaticDir:   cfg.StaticDir,
		CORSOrigins: cfg.CORSOrigins,
	}
	if len(cfg.NeighborSources) > 0 {
		deps.Watcher = neighbor.NewWatcher(cfg.NeighborSources, cfg.StaleAfter, deps.Neighbors)
	}
	if len(cfg.FQDNAPIBases) > 0 {
		deps.FQDN = fqdn.NewClient(cfg.FQDNAPIBases)
	}
	h := api.New(deps)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if deps.Watcher != nil {
		go deps.Watcher.Run(ctx, cfg.FetchInterval)
	}
	if cfg.ReconcileInterval > 0 {
		go h.RunReconciler(ctx, cfg.ReconcileInterval)
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           h.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      150 * time.Second, // an apply, or a neighbor refresh that waits for SNMP walks
		IdleTimeout:       120 * time.Second,
	}
	// On shutdown, requests in flight are given time to finish: an apply
	// should complete both of its phases.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		<-ctx.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 70*time.Second)
		defer shutCancel()
		srv.Shutdown(shutCtx)
	}()

	ui := "API only"
	if cfg.StaticDir != "" {
		ui = "API and UI from " + cfg.StaticDir
	}
	log.Printf("RA controller listening on %s (%s)", cfg.ListenAddr, ui)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
	<-drained
	log.Println("server stopped")
}

type config struct {
	ListenAddr        string
	DBPath            string
	StaticDir         string
	CORSOrigins       []string
	NeighborSources   []neighbor.Source
	FetchInterval     time.Duration
	StaleAfter        time.Duration
	FQDNAPIBases      []string
	ReconcileInterval time.Duration
}

// loadConfig reads the environment:
//
//	LISTEN_ADDR            address to listen on (default ":" + PORT)
//	PORT                   port, if LISTEN_ADDR is not set (default 8080)
//	DB_PATH                SQLite database file (default controller.db)
//	STATIC_DIR             directory of the built web UI; empty = API only
//	CORS_ORIGINS           comma-separated origins allowed to call the API; empty = same origin only
//	NEIGHBOR_API_URLS      comma-separated URLs of neighbor sources (their GET /api/neighbors)
//	NEIGHBOR_SOURCE_NAMES  names for those sources, same order (default: the URL's host)
//	FETCH_INTERVAL         seconds between neighbor fetches (default 10)
//	NEIGHBOR_STALE_AFTER   seconds a failing source keeps its last hosts (default 5 x FETCH_INTERVAL)
//	FQDN_API_BASES         comma-separated URLs of endpoint sources (their GET /api/fqdn/services)
//	RECONCILE_INTERVAL     seconds between reconciles of the applied policy (default 60; 0 = off)
func loadConfig() config {
	listen := envStr("LISTEN_ADDR", "")
	if listen == "" {
		listen = net.JoinHostPort("", strconv.Itoa(envInt("PORT", 8080)))
	}
	fetch := time.Duration(envInt("FETCH_INTERVAL", 10)) * time.Second
	if fetch <= 0 {
		fetch = 10 * time.Second
	}
	cfg := config{
		ListenAddr:        listen,
		DBPath:            envStr("DB_PATH", "controller.db"),
		StaticDir:         envStr("STATIC_DIR", ""),
		CORSOrigins:       envStrSlice("CORS_ORIGINS"),
		FetchInterval:     fetch,
		StaleAfter:        time.Duration(envInt("NEIGHBOR_STALE_AFTER", int(5*fetch/time.Second))) * time.Second,
		FQDNAPIBases:      envStrSlice("FQDN_API_BASES"),
		ReconcileInterval: time.Duration(envInt("RECONCILE_INTERVAL", 60)) * time.Second,
	}
	names := envStrSlice("NEIGHBOR_SOURCE_NAMES")
	for i, u := range envStrSlice("NEIGHBOR_API_URLS") {
		name := u
		if i < len(names) {
			name = names[i]
		}
		cfg.NeighborSources = append(cfg.NeighborSources, neighbor.Source{Name: name, URL: u})
	}
	if os.Getenv("NEIGHBOR_IFNAME") != "" {
		log.Printf("NEIGHBOR_IFNAME is no longer used: restrict the interfaces in the neighbor source itself " +
			"(for the SNMP source, \"ifname\" of each target)")
	}
	if cfg.StaticDir != "" {
		if _, err := os.Stat(cfg.StaticDir + "/index.html"); err != nil {
			log.Fatalf("STATIC_DIR %s has no index.html: build the frontend first (%v)", cfg.StaticDir, err)
		}
	}
	return cfg
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Printf("%s=%q is not a number; using %d", key, v, def)
		return def
	}
	return n
}

// envStrSlice splits a comma-separated env var into a slice, ignoring empty entries.
func envStrSlice(key string) []string {
	var result []string
	for _, s := range strings.Split(os.Getenv(key), ",") {
		if s = strings.TrimSpace(s); s != "" {
			result = append(result, s)
		}
	}
	return result
}
