package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

var (
	defaultInstance string
	clientRequestID string
)

// newMux builds the HTTP API. enableDNSResolve adds GET /api/dns/resolve.
func newMux(enableDNSResolve bool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("POST /refresh", handleRefresh)
	mux.HandleFunc("GET /api/fqdn/services", handleListServices)
	mux.HandleFunc("GET /api/fqdn/services/{name}/endpoints", handleServiceEndpoints)
	mux.HandleFunc("GET /api/endpoints", handleListEndpoints)
	mux.HandleFunc("GET /api/endpoints/map", handleEndpointsMap)
	mux.HandleFunc("GET /api/edl/{type}", handleEDL)
	if enableDNSResolve {
		mux.HandleFunc("GET /api/dns/resolve", handleDNSResolve)
	}
	return mux
}

func main() {
	dbPath := getEnv("DB_PATH", "./m365_endpoints.db")
	defaultInstance = getEnv("M365_INSTANCE", "Worldwide")
	clientRequestID = getEnv("CLIENT_REQUEST_ID", mustUUID())
	addr := getEnv("ADDR", ":8000")
	enableDNSResolve := os.Getenv("ENABLE_DNS_RESOLVE") == "1"

	if !knownInstances[defaultInstance] {
		log.Fatalf("M365_INSTANCE=%q is not a Microsoft 365 instance (known: Worldwide, USGovDoD, USGovGCCHigh, China)", defaultInstance)
	}
	refreshSecs := 3600
	if v := os.Getenv("REFRESH_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			refreshSecs = n
		} else {
			log.Printf("REFRESH_INTERVAL=%q is not a positive integer, using %d", v, refreshSecs)
		}
	}

	if err := initDB(dbPath); err != nil {
		log.Fatalf("init db: %v", err)
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := &http.Server{
		Handler:           newMux(enableDNSResolve),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// POST /refresh may wait for a running refresh and then run its own;
		// each makes two calls to Microsoft of at most 30 s.
		WriteTimeout: 150 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Listen before the first refresh so that start-up does not wait for Microsoft.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen %s: %v", addr, err)
	}
	log.Printf("listening on %s (instance=%s refresh=%ds dns_resolve=%t)", addr, defaultInstance, refreshSecs, enableDNSResolve)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runRefresher(ctx, defaultInstance, time.Duration(refreshSecs)*time.Second)
	}()

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(ln) }()

	select {
	case err := <-serveErr:
		log.Fatalf("serve: %v", err)
	case <-ctx.Done():
	}
	stop() // a second signal terminates the process at once

	log.Printf("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("shutdown: %v", err)
	}
	wg.Wait()
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		log.Fatalf("generate uuid: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
