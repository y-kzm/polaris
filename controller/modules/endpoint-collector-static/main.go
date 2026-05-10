package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
)

type fqdnService struct {
	Service     string `json:"service"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type serviceEndpoints struct {
	FQDNs        []string `json:"fqdns,omitempty"`
	IPv6Prefixes []string `json:"ipv6_prefixes,omitempty"`
	IPv4Prefixes []string `json:"ipv4_prefixes,omitempty"`
}

var staticServices []fqdnService
var staticEndpoints map[string]serviceEndpoints

func init() {
	data := os.Getenv("STATIC_DATA")
	if data == "" || data == "[]" || data == "null" {
		staticServices = defaultServices
		staticEndpoints = defaultEndpoints
		return
	}

	type configEntry struct {
		Service      string   `json:"service"`
		Name         string   `json:"name"`
		Description  string   `json:"description"`
		FQDNs        []string `json:"fqdns"`
		IPv6Prefixes []string `json:"ipv6_prefixes"`
		IPv4Prefixes []string `json:"ipv4_prefixes"`
	}
	var entries []configEntry
	if err := json.Unmarshal([]byte(data), &entries); err != nil {
		log.Fatalf("endpoint-collector-static: parse STATIC_DATA: %v", err)
	}

	staticEndpoints = make(map[string]serviceEndpoints, len(entries))
	for _, e := range entries {
		key := e.Service
		if key == "" {
			key = e.Name
		}
		desc := e.Description
		if desc == "" {
			desc = e.Name
		}
		staticServices = append(staticServices, fqdnService{
			Service:     key,
			Name:        e.Name,
			Description: desc,
		})
		staticEndpoints[key] = serviceEndpoints{
			FQDNs:        e.FQDNs,
			IPv6Prefixes: e.IPv6Prefixes,
			IPv4Prefixes: e.IPv4Prefixes,
		}
	}
}

var defaultServices = []fqdnService{}

var defaultEndpoints = map[string]serviceEndpoints{}

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

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8082"
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/fqdn/services", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, staticServices)
	})

	mux.HandleFunc("GET /api/fqdn/services/{name}/endpoints", func(w http.ResponseWriter, r *http.Request) {
		ep, ok := staticEndpoints[r.PathValue("name")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, ep)
	})

	log.Printf("endpoint-collector-static listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, corsMiddleware(mux)))
}
