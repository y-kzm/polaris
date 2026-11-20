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
		name    string
		data    string
		want    int
		wantErr string
	}{
		{name: "unset", data: "", want: 0},
		{name: "null", data: "null", want: 0},
		{name: "empty array", data: "[]", want: 0},
		{name: "one neighbor", data: `[{"ip":"fe80::1","lladdr":"02:00:00:00:00:01","ifindex":3,"ifname":"eth1","state":"REACHABLE","is_router":0,"source":"lab","updated_at":"2026-11-20T01:02:03Z"}]`, want: 1},
		{name: "ip only", data: `[{"ip":"fe80::1"},{"ip":"fe80::2"}]`, want: 2},
		{name: "not json", data: `fe80::1`, wantErr: "JSON array"},
		{name: "object instead of array", data: `{"ip":"fe80::1"}`, wantErr: "JSON array"},
		{name: "array of strings", data: `["fe80::1"]`, wantErr: "JSON array"},
		{name: "wrong field type", data: `[{"ip":"fe80::1","ifindex":"three"}]`, wantErr: "JSON array"},
		{name: "missing ip", data: `[{"ip":"fe80::1"},{"lladdr":"02:00:00:00:00:01"}]`, wantErr: `entry 1: "ip" is required`},
		{name: "bad ip", data: `[{"ip":"fe80::zz"}]`, wantErr: "not an IP address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseStaticData(tt.data)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got == nil || len(got) != tt.want {
				t.Fatalf("got %#v, want %d neighbor(s) in a non-nil slice", got, tt.want)
			}
		})
	}
}

func TestHTTP(t *testing.T) {
	get := func(srv *httptest.Server, method, path string) (int, string, http.Header) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		req.Header.Set("Origin", "http://example.com")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(b)), resp.Header
	}

	neighbors, err := parseStaticData(`[{"ip":"fe80::1","lladdr":"02:00:00:00:00:01","ifname":"eth1","state":"REACHABLE"}]`)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newMux(neighbors))
	defer srv.Close()

	if code, body, _ := get(srv, "GET", "/health"); code != 200 || body != `{"status":"ok"}` {
		t.Fatalf("GET /health: %d %s", code, body)
	}
	code, body, hdr := get(srv, "GET", "/api/neighbors")
	want := `[{"ip":"fe80::1","lladdr":"02:00:00:00:00:01","ifindex":0,"ifname":"eth1","state":"REACHABLE","is_router":0,"updated_at":""}]`
	if code != 200 || body != want {
		t.Fatalf("GET /api/neighbors: %d %s, want %s", code, body, want)
	}
	if v := hdr.Get("Access-Control-Allow-Origin"); v != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want none", v)
	}
	if code, _, _ := get(srv, "POST", "/api/neighbors/refresh"); code != http.StatusNoContent {
		t.Fatalf("POST /api/neighbors/refresh: %d, want 204", code)
	}

	empty, _ := parseStaticData("")
	srv2 := httptest.NewServer(newMux(empty))
	defer srv2.Close()
	if code, body, _ := get(srv2, "GET", "/api/neighbors"); code != 200 || body != "[]" {
		t.Fatalf("GET /api/neighbors without data: %d %s, want 200 []", code, body)
	}
}
