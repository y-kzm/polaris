package main

import (
	"context"
	"fmt"
	"log"
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
	log.Printf("starting go-ra client backend server on :%d", cfg.Port)

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	routerStore := store.NewRouterStore(db)
	ruleStore := store.NewRuleStore(db)
	groupStore := store.NewGroupStore(db)
	neighborStore := store.NewNeighborStore(db)

	var watcher *neighbor.Watcher
	if len(cfg.NeighborAPIURLs) > 0 {
		watcher = neighbor.NewWatcher(cfg.NeighborAPIURLs, cfg.NeighborIfName, neighborStore)
	}

	var fqdnClient *fqdn.Client
	if len(cfg.FQDNAPIBases) > 0 {
		fqdnClient = fqdn.NewClient(cfg.FQDNAPIBases)
	}

	h := api.New(routerStore, ruleStore, groupStore, neighborStore, watcher, fqdnClient, cfg.NeighborSourceNames)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if watcher != nil {
		go watcher.Run(ctx, time.Duration(cfg.FetchIntervalSec)*time.Second)
	}

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      h.Router(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 120 * time.Second, // covers slow SNMP polls triggered by /neighbors/refresh
	}

	go func() {
		<-ctx.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		srv.Shutdown(shutCtx)
	}()

	log.Printf("listening on :%d", cfg.Port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
	log.Println("server stopped")
}

type config struct {
	Port                 int
	DBPath               string
	NeighborAPIURLs      []string
	NeighborSourceNames  []string
	NeighborIfName       string
	FetchIntervalSec     int
	FQDNAPIBases         []string
}

func loadConfig() config {
	return config{
		Port:                envInt("PORT", 8080),
		DBPath:              envStr("DB_PATH", "controller.db"),
		NeighborAPIURLs:     envStrSlice("NEIGHBOR_API_URLS"),
		NeighborSourceNames: envStrSlice("NEIGHBOR_SOURCE_NAMES"),
		NeighborIfName:      envStr("NEIGHBOR_IFNAME", ""),
		FetchIntervalSec:    envInt("FETCH_INTERVAL", 10),
		FQDNAPIBases:        envStrSlice("FQDN_API_BASES"),
	}
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
		return def
	}
	return n
}

// envStrSlice splits a comma-separated env var into a slice, ignoring empty entries.
func envStrSlice(key string) []string {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	var result []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			result = append(result, s)
		}
	}
	return result
}
