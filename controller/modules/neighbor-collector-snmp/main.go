package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8084"
	}

	pollSecs := 30
	if v := os.Getenv("POLL_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			pollSecs = n
		}
	}

	targets := parseTargets(os.Getenv("SNMP_TARGETS"))
	c := newCollector(targets)
	c.refresh()

	go c.run(time.Duration(pollSecs) * time.Second)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/neighbors", func(w http.ResponseWriter, r *http.Request) {
		ifname := r.URL.Query().Get("ifname")
		ns := c.neighbors(ifname)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ns)
	})

	// POST /api/neighbors/refresh — triggers an immediate SNMP poll and blocks until done.
	mux.HandleFunc("POST /api/neighbors/refresh", func(w http.ResponseWriter, r *http.Request) {
		c.refresh()
		w.WriteHeader(http.StatusNoContent)
	})

	// PUT /api/targets/{host} — registers or updates the internal interface name for a
	// router target.  Called by each router on startup after it detects its own
	// internal-segment interface, so only site-internal neighbors are returned.
	mux.HandleFunc("PUT /api/targets/", func(w http.ResponseWriter, r *http.Request) {
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
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IfName == "" {
			http.Error(w, "ifname required", http.StatusBadRequest)
			return
		}
		c.setTargetIface(host, body.IfName, body.SelfLL, body.Label)
		log.Printf("target %s: ifname=%q self_ll=%q label=%q", host, body.IfName, body.SelfLL, body.Label)
		w.WriteHeader(http.StatusNoContent)
	})

	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.Host
	}
	log.Printf("neighbor-collector-snmp listening on %s (targets=%v poll=%ds)", addr, names, pollSecs)
	log.Fatal(http.ListenAndServe(addr, corsMiddleware(mux)))
}
