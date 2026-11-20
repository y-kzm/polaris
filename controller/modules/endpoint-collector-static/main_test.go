package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseStaticData(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		wantN    int
		wantKeys []string
		wantErr  string
	}{
		{name: "unset", data: ""},
		{name: "null", data: "null"},
		{name: "empty array", data: "[]"},
		{name: "name only", data: `[{"name":"Teams","ipv6_prefixes":["2603:1027::/48"]}]`, wantN: 1, wantKeys: []string{"Teams"}},
		{name: "service and name", data: `[{"service":"teams","name":"Teams"}]`, wantN: 1, wantKeys: []string{"teams", "Teams"}},
		{name: "service only", data: `[{"service":"teams"}]`, wantN: 1, wantKeys: []string{"teams"}},
		{name: "not json", data: `Teams`, wantErr: "JSON array"},
		{name: "object instead of array", data: `{"name":"Teams"}`, wantErr: "JSON array"},
		{name: "prefixes not a list", data: `[{"name":"Teams","ipv6_prefixes":"2603:1027::/48"}]`, wantErr: "JSON array"},
		{name: "no name", data: `[{"ipv6_prefixes":["2603:1027::/48"]}]`, wantErr: `"name" is required`},
		{name: "duplicate", data: `[{"name":"Teams"},{"name":"Teams"}]`, wantErr: "defined twice"},
		{name: "name collides with a service key", data: `[{"service":"a","name":"A"},{"service":"A","name":"B"}]`, wantErr: "defined twice"},
		{name: "bad prefix", data: `[{"name":"Teams","ipv6_prefixes":["2603:1027::/200"]}]`, wantErr: "not an IP prefix"},
		{name: "ipv4 in the ipv6 list", data: `[{"name":"Teams","ipv6_prefixes":["192.0.2.0/24"]}]`, wantErr: "other address family"},
		{name: "ipv6 in the ipv4 list", data: `[{"name":"Teams","ipv4_prefixes":["2603:1027::/48"]}]`, wantErr: "other address family"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			services, endpoints, err := parseStaticData(tt.data)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if services == nil || len(services) != tt.wantN {
				t.Fatalf("got %#v, want %d service(s) in a non-nil slice", services, tt.wantN)
			}
			for _, k := range tt.wantKeys {
				if _, ok := endpoints[k]; !ok {
					t.Errorf("service not found under %q", k)
				}
			}
		})
	}
}

func TestParseStaticDataCanonicalPrefixes(t *testing.T) {
	_, endpoints, err := parseStaticData(`[{"name":"X","ipv6_prefixes":["2001:DB8:0:1::5/48","2001:db8::1"],"ipv4_prefixes":["192.0.2.77/24"]}]`)
	if err != nil {
		t.Fatal(err)
	}
	ep := endpoints["X"]
	if got := strings.Join(ep.IPv6Prefixes, " "); got != "2001:db8::/48 2001:db8::1/128" {
		t.Errorf("ipv6_prefixes = %q", got)
	}
	if got := strings.Join(ep.IPv4Prefixes, " "); got != "192.0.2.0/24" {
		t.Errorf("ipv4_prefixes = %q", got)
	}
}

func TestHTTP(t *testing.T) {
	services, endpoints, err := parseStaticData(`[{"name":"Teams","description":"Microsoft Teams","fqdns":["teams.example"],"ipv6_prefixes":["2603:1027::/48"]},{"name":"Empty Svc"}]`)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newMux(services, endpoints))
	defer srv.Close()

	get := func(base, path string) (int, string, http.Header) {
		t.Helper()
		req, _ := http.NewRequest("GET", base+path, nil)
		req.Header.Set("Origin", "http://example.com")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(b)), resp.Header
	}

	if code, body, _ := get(srv.URL, "/health"); code != 200 || body != `{"status":"ok"}` {
		t.Fatalf("GET /health: %d %s", code, body)
	}
	code, body, hdr := get(srv.URL, "/api/fqdn/services")
	want := `[{"service":"Teams","name":"Teams","description":"Microsoft Teams"},{"service":"Empty Svc","name":"Empty Svc","description":"Empty Svc"}]`
	if code != 200 || body != want {
		t.Fatalf("GET /api/fqdn/services: %d %s, want %s", code, body, want)
	}
	if v := hdr.Get("Access-Control-Allow-Origin"); v != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want none", v)
	}
	if code, body, _ := get(srv.URL, "/api/fqdn/services/Teams/endpoints"); code != 200 || body != `{"fqdns":["teams.example"],"ipv6_prefixes":["2603:1027::/48"]}` {
		t.Fatalf("GET endpoints of Teams: %d %s", code, body)
	}
	if code, body, _ := get(srv.URL, "/api/fqdn/services/Empty%20Svc/endpoints"); code != 200 || body != `{}` {
		t.Fatalf("GET endpoints of a known service without prefixes: %d %s, want 200 {}", code, body)
	}
	if code, _, _ := get(srv.URL, "/api/fqdn/services/Nope/endpoints"); code != http.StatusNotFound {
		t.Fatalf("GET endpoints of an unknown service: %d, want 404", code)
	}

	s0, e0, _ := parseStaticData("")
	srv2 := httptest.NewServer(newMux(s0, e0))
	defer srv2.Close()
	if code, body, _ := get(srv2.URL, "/api/fqdn/services"); code != 200 || body != "[]" {
		t.Fatalf("GET /api/fqdn/services without data: %d %s, want 200 []", code, body)
	}
}
