package main

import (
	"crypto/rand"
	"fmt"
	"log"
	"net/http"
	"os"
)

var (
	defaultInstance string
	clientRequestID string
)

func main() {
	dbPath := getEnv("DB_PATH", "./m365_endpoints.db")
	defaultInstance = getEnv("M365_INSTANCE", "Worldwide")
	clientRequestID = getEnv("CLIENT_REQUEST_ID", mustUUID())
	addr := getEnv("ADDR", ":8000")

	if err := initDB(dbPath); err != nil {
		log.Fatalf("init db: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("POST /refresh", handleRefresh)
	mux.HandleFunc("GET /api/fqdn/services", handleListServices)
	mux.HandleFunc("GET /api/fqdn/services/{name}/endpoints", handleServiceEndpoints)
	mux.HandleFunc("GET /api/endpoints", handleListEndpoints)
	mux.HandleFunc("GET /api/endpoints/map", handleEndpointsMap)
	mux.HandleFunc("GET /api/edl/{type}", handleEDL)
	mux.HandleFunc("GET /api/dns/resolve", handleDNSResolve)

	log.Printf("listening on %s (instance=%s)", addr, defaultInstance)
	log.Fatal(http.ListenAndServe(addr, corsMiddleware(mux)))
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
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
