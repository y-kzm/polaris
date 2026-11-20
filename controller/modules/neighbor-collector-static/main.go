// neighbor-collector-static serves a fixed neighbor list given in the
// STATIC_DATA environment variable. It is a fixture for tests and small
// deployments: it never observes the network, so the list is only as
// current as the configuration.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type neighbor struct {
	IP        string `json:"ip"`
	LLAddr    string `json:"lladdr"`
	IfIndex   int    `json:"ifindex"`
	IfName    string `json:"ifname"`
	State     string `json:"state"`
	IsRouter  int    `json:"is_router"`
	Source    string `json:"source,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

// parseStaticData validates STATIC_DATA: a JSON array of neighbor objects,
// each with a valid "ip". Empty, "null" and "[]" mean no neighbors.
func parseStaticData(data string) ([]neighbor, error) {
	data = strings.TrimSpace(data)
	if data == "" || data == "null" {
		return []neighbor{}, nil
	}
	neighbors := []neighbor{}
	if err := json.Unmarshal([]byte(data), &neighbors); err != nil {
		return nil, fmt.Errorf("STATIC_DATA must be a JSON array of neighbor objects "+
			`(e.g. [{"ip":"fe80::1","lladdr":"02:00:00:00:00:01","ifname":"eth1","state":"REACHABLE"}]): %w`, err)
	}
	if neighbors == nil { // "null" inside whitespace etc.
		return []neighbor{}, nil
	}
	for i, n := range neighbors {
		if n.IP == "" {
			return nil, fmt.Errorf(`STATIC_DATA entry %d: "ip" is required`, i)
		}
		if _, err := netip.ParseAddr(n.IP); err != nil {
			return nil, fmt.Errorf(`STATIC_DATA entry %d: "ip" %q is not an IP address`, i, n.IP)
		}
	}
	return neighbors, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func newMux(neighbors []neighbor) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /api/neighbors", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, neighbors)
	})

	// POST /api/neighbors/refresh — no-op for static data, but exists for API consistency.
	mux.HandleFunc("POST /api/neighbors/refresh", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	return mux
}

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8083"
	}

	neighbors, err := parseStaticData(os.Getenv("STATIC_DATA"))
	if err != nil {
		log.Fatalf("neighbor-collector-static: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := &http.Server{
		Handler:           newMux(neighbors),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second, // every handler answers from memory
		IdleTimeout:       60 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("neighbor-collector-static: listen %s: %v", addr, err)
	}
	log.Printf("neighbor-collector-static listening on %s (%d neighbor(s))", addr, len(neighbors))

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(ln) }()

	select {
	case err := <-serveErr:
		log.Fatalf("neighbor-collector-static: %v", err)
	case <-ctx.Done():
	}
	stop() // a second signal terminates the process at once

	log.Printf("neighbor-collector-static: shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("neighbor-collector-static: shutdown: %v", err)
	}
}
