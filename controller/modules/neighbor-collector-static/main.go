package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
)

type neighbor struct {
	IP        string `json:"ip"`
	LLAddr    string `json:"lladdr"`
	IfIndex   int    `json:"ifindex"`
	IfName    string `json:"ifname"`
	State     string `json:"state"`
	IsRouter  int    `json:"is_router"`
	UpdatedAt string `json:"updated_at"`
}

var staticNeighbors []neighbor

func init() {
	data := os.Getenv("STATIC_DATA")
	if data == "" || data == "[]" || data == "null" {
		staticNeighbors = defaultNeighbors
		return
	}
	if err := json.Unmarshal([]byte(data), &staticNeighbors); err != nil {
		log.Fatalf("neighbor-collector-static: parse STATIC_DATA: %v", err)
	}
}

var defaultNeighbors = []neighbor{}

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
		addr = ":8083"
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/neighbors", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(staticNeighbors)
	})

	// POST /api/neighbors/refresh — no-op for static data, but exists for API consistency.
	mux.HandleFunc("POST /api/neighbors/refresh", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	log.Printf("neighbor-collector-static listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, corsMiddleware(mux)))
}
