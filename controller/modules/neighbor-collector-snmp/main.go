package main

import (
	"context"
	"encoding/json"
	"errors"
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

// maxBodyBytes limits the request bodies this module reads.
const maxBodyBytes = 64 << 10

// envPositiveInt reads a positive integer from the environment. An unset
// variable yields the fallback silently, an invalid one with a log line.
func envPositiveInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		log.Printf("%s=%q is not a positive integer, using %d", key, v, fallback)
		return fallback
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// newMux builds the HTTP API. allowRegistration enables PUT /api/targets/{host}.
func newMux(c *collector, allowRegistration bool) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// GET /api/neighbors — answered from memory. The ifname query parameter
	// is kept for compatibility; the supported filter is the per-target
	// "ifname" of SNMP_TARGETS.
	mux.HandleFunc("GET /api/neighbors", func(w http.ResponseWriter, r *http.Request) {
		ns, ready := c.neighbors(r.URL.Query().Get("ifname"))
		if !ready {
			// Not "no hosts": the first poll has not finished yet.
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "first SNMP poll has not finished"})
			return
		}
		writeJSON(w, http.StatusOK, ns)
	})

	// POST /api/neighbors/refresh — triggers an immediate SNMP poll and blocks until done.
	// If a poll is already running it waits for that one instead.
	mux.HandleFunc("POST /api/neighbors/refresh", func(w http.ResponseWriter, r *http.Request) {
		c.refresh(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})

	// PUT /api/targets/{host} — registers or updates the site-facing interface name for a
	// router target at runtime. Used by the Docker lab, where each router calls it on
	// startup after it detects its own interface. Elsewhere the same fields are set in
	// SNMP_TARGETS, and this endpoint answers 403 unless ALLOW_TARGET_REGISTRATION=1.
	mux.HandleFunc("PUT /api/targets/", func(w http.ResponseWriter, r *http.Request) {
		if !allowRegistration {
			http.Error(w, "target registration is disabled (set ALLOW_TARGET_REGISTRATION=1)", http.StatusForbidden)
			return
		}
		host := r.URL.Path[len("/api/targets/"):]
		if host == "" {
			http.Error(w, "missing host", http.StatusBadRequest)
			return
		}
		var body struct {
			IfName string `json:"ifname"`
			SelfLL string `json:"self_ll"`
			Label  string `json:"label"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IfName == "" {
			http.Error(w, "ifname required", http.StatusBadRequest)
			return
		}
		c.setTargetIface(host, body.IfName, body.SelfLL, body.Label)
		log.Printf("target %s: ifname=%q self_ll=%q label=%q", host, body.IfName, body.SelfLL, body.Label)
		w.WriteHeader(http.StatusNoContent)
	})

	return mux
}

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8084"
	}
	pollSecs := envPositiveInt("POLL_INTERVAL", 30)
	stalePolls := envPositiveInt("STALE_POLLS", 3)
	targetTimeout := time.Duration(envPositiveInt("TARGET_TIMEOUT", 20)) * time.Second
	allowRegistration := os.Getenv("ALLOW_TARGET_REGISTRATION") == "1"

	targets, err := parseTargets(os.Getenv("SNMP_TARGETS"))
	if err != nil {
		log.Fatalf("neighbor-collector-snmp: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := newCollector(ctx, targets, collectFrom, stalePolls, targetTimeout)

	server := &http.Server{
		Handler:           newMux(c, allowRegistration),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// POST /api/neighbors/refresh blocks for one poll, which is bounded by the per-target deadline.
		WriteTimeout: targetTimeout + 10*time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Listen before the first poll so that start-up does not wait for SNMP.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("neighbor-collector-snmp: listen %s: %v", addr, err)
	}
	log.Printf("neighbor-collector-snmp listening on %s (targets=%v poll=%ds target_timeout=%s stale_polls=%d registration=%t)",
		addr, c.targetHosts(), pollSecs, targetTimeout, stalePolls, allowRegistration)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.run(time.Duration(pollSecs) * time.Second)
	}()

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(ln) }()

	select {
	case err := <-serveErr:
		log.Fatalf("neighbor-collector-snmp: %v", err)
	case <-ctx.Done():
	}
	stop() // a second signal terminates the process at once

	log.Printf("neighbor-collector-snmp: shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("neighbor-collector-snmp: shutdown: %v", err)
	}
	wg.Wait()
	c.stop()
}
