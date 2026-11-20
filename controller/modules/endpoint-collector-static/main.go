// endpoint-collector-static serves a fixed service list given in the
// STATIC_DATA environment variable. It is a fixture for tests and small
// deployments: the prefixes of a service are only as current as the
// configuration.
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

type configEntry struct {
	Service      string   `json:"service"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	FQDNs        []string `json:"fqdns"`
	IPv6Prefixes []string `json:"ipv6_prefixes"`
	IPv4Prefixes []string `json:"ipv4_prefixes"`
}

// parsePrefix accepts a prefix or a bare address (taken as a host prefix)
// and returns it masked.
func parsePrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("%q is not an IP prefix or address", s)
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// canonicalPrefixes validates a prefix list of one address family and
// returns the prefixes in canonical masked form.
func canonicalPrefixes(in []string, wantV6 bool) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, s := range in {
		p, err := parsePrefix(s)
		if err != nil {
			return nil, err
		}
		isV6 := p.Addr().Is6() && !p.Addr().Is4In6()
		if isV6 != wantV6 {
			return nil, fmt.Errorf("%q is in the list of the other address family", s)
		}
		out = append(out, p.String())
	}
	return out, nil
}

// parseStaticData validates STATIC_DATA: a JSON array of objects with
// "name" (or "service"), and optional "description", "fqdns",
// "ipv6_prefixes" and "ipv4_prefixes". Empty, "null" and "[]" mean no
// services. A service is found under its "service" key and its "name".
func parseStaticData(data string) ([]fqdnService, map[string]serviceEndpoints, error) {
	services := []fqdnService{}
	endpoints := map[string]serviceEndpoints{}

	data = strings.TrimSpace(data)
	if data == "" || data == "null" {
		return services, endpoints, nil
	}
	var entries []configEntry
	if err := json.Unmarshal([]byte(data), &entries); err != nil {
		return nil, nil, fmt.Errorf("STATIC_DATA must be a JSON array of service objects "+
			`(e.g. [{"name":"Example","ipv6_prefixes":["2001:db8::/48"]}]): %w`, err)
	}

	for i, e := range entries {
		key := e.Service
		if key == "" {
			key = e.Name
		}
		if key == "" {
			return nil, nil, fmt.Errorf(`STATIC_DATA entry %d: "name" is required`, i)
		}
		name := e.Name
		if name == "" {
			name = key
		}
		desc := e.Description
		if desc == "" {
			desc = name
		}
		v6, err := canonicalPrefixes(e.IPv6Prefixes, true)
		if err != nil {
			return nil, nil, fmt.Errorf(`STATIC_DATA entry %d (%s): "ipv6_prefixes": %w`, i, key, err)
		}
		v4, err := canonicalPrefixes(e.IPv4Prefixes, false)
		if err != nil {
			return nil, nil, fmt.Errorf(`STATIC_DATA entry %d (%s): "ipv4_prefixes": %w`, i, key, err)
		}
		ep := serviceEndpoints{FQDNs: e.FQDNs, IPv6Prefixes: v6, IPv4Prefixes: v4}
		keys := []string{key}
		if name != key {
			keys = append(keys, name)
		}
		for _, k := range keys {
			if _, dup := endpoints[k]; dup {
				return nil, nil, fmt.Errorf("STATIC_DATA entry %d: service %q is defined twice", i, k)
			}
			endpoints[k] = ep
		}
		services = append(services, fqdnService{Service: key, Name: name, Description: desc})
	}
	return services, endpoints, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func newMux(services []fqdnService, endpoints map[string]serviceEndpoints) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /api/fqdn/services", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, services)
	})

	mux.HandleFunc("GET /api/fqdn/services/{name}/endpoints", func(w http.ResponseWriter, r *http.Request) {
		ep, ok := endpoints[r.PathValue("name")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, ep)
	})

	return mux
}

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8082"
	}

	services, endpoints, err := parseStaticData(os.Getenv("STATIC_DATA"))
	if err != nil {
		log.Fatalf("endpoint-collector-static: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := &http.Server{
		Handler:           newMux(services, endpoints),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second, // every handler answers from memory
		IdleTimeout:       60 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("endpoint-collector-static: listen %s: %v", addr, err)
	}
	log.Printf("endpoint-collector-static listening on %s (%d service(s))", addr, len(services))

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(ln) }()

	select {
	case err := <-serveErr:
		log.Fatalf("endpoint-collector-static: %v", err)
	case <-ctx.Done():
	}
	stop() // a second signal terminates the process at once

	log.Printf("endpoint-collector-static: shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("endpoint-collector-static: shutdown: %v", err)
	}
}
