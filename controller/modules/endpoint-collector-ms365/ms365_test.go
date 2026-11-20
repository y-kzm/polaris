package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCanonicalPrefix(t *testing.T) {
	tests := []struct {
		in         string
		wantValue  string
		wantFamily string
		wantOK     bool
	}{
		{"13.107.6.152/31", "13.107.6.152/31", "ipv4", true},
		{"13.107.6.153/31", "13.107.6.152/31", "ipv4", true}, // masked
		{"192.0.2.7", "192.0.2.7/32", "ipv4", true},          // bare address
		{"2603:1027::/48", "2603:1027::/48", "ipv6", true},
		{"2603:1027:0:1::5/48", "2603:1027::/48", "ipv6", true}, // masked
		{"2603:1027:0000:0000::/48", "2603:1027::/48", "ipv6", true},
		{"2603:1027::ABCD", "2603:1027::abcd/128", "ipv6", true},
		{" 2a01:111::/32 ", "2a01:111::/32", "ipv6", true},
		{"::/0", "::/0", "ipv6", true},
		{"0.0.0.0/0", "0.0.0.0/0", "ipv4", true},
		{"", "", "", false},
		{"not-an-ip", "", "", false},
		{"teams.microsoft.com", "", "", false},
		{"2603:1027::/129", "", "", false},
		{"13.107.6.152/33", "", "", false},
		{"13.107.6/24", "", "", false},
		{"2603:1027::/", "", "", false},
		{"fe80::1%eth0", "", "", false},
		{"fe80::1%eth0/64", "", "", false},
		{"::ffff:192.0.2.1", "", "", false}, // IPv4-mapped: neither list
		{"::ffff:192.0.2.0/120", "", "", false},
	}
	for _, tt := range tests {
		value, family, ok := canonicalPrefix(tt.in)
		if value != tt.wantValue || family != tt.wantFamily || ok != tt.wantOK {
			t.Errorf("canonicalPrefix(%q) = %q, %q, %v; want %q, %q, %v", tt.in, value, family, ok, tt.wantValue, tt.wantFamily, tt.wantOK)
		}
	}
}

func TestSanitizeEndpointSets(t *testing.T) {
	in := []MSEndpointSet{
		{ID: 1, ServiceArea: "Exchange", ServiceAreaDisplayName: "Exchange Online",
			URLs: []string{"outlook.office.com", " ", ""},
			IPs:  []string{"2603:1026:0:5::9/36", "garbage", "13.107.6.153/31", ""}},
		{ID: 2, ServiceArea: "Skype", URLs: []string{"*.teams.microsoft.com"}},
		{ID: 3, ServiceArea: "Common", IPs: []string{"x"}},
	}
	clean, records, dropped := sanitizeEndpointSets(in)
	if records != 4 || dropped != 3 {
		t.Fatalf("records=%d dropped=%d, want 4 and 3", records, dropped)
	}
	if got := strings.Join(clean[0].IPs, " "); got != "2603:1026::/36 13.107.6.152/31" {
		t.Errorf("set 1 IPs = %q", got)
	}
	if got := strings.Join(clean[0].URLs, " "); got != "outlook.office.com" {
		t.Errorf("set 1 URLs = %q", got)
	}
	if clean[1].ServiceAreaDisplayName != "Skype" {
		t.Errorf("set 2 display name = %q, want the service area", clean[1].ServiceAreaDisplayName)
	}
	if len(clean) != 3 || len(clean[2].IPs) != 0 {
		t.Errorf("set 3 = %+v, want kept without IPs", clean[2])
	}
	if in[0].IPs[0] != "2603:1026:0:5::9/36" || len(in[0].URLs) != 3 {
		t.Error("sanitizeEndpointSets modified its input")
	}
}

// fakeMS imitates endpoints.office.com.
type fakeMS struct {
	mu            sync.Mutex
	versionStatus int
	versionBody   string
	endpStatus    int
	endpBody      string
	endpCalls     int
	paths         []string
}

func (f *fakeMS) set(version, endpoints string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.versionStatus, f.endpStatus = 200, 200
	f.versionBody = version
	f.endpBody = endpoints
}

func (f *fakeMS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.URL.EscapedPath()+"?"+r.URL.RawQuery)
	switch {
	case strings.HasPrefix(r.URL.Path, "/version/"):
		w.WriteHeader(f.versionStatus)
		io.WriteString(w, f.versionBody)
	case strings.HasPrefix(r.URL.Path, "/endpoints/"):
		f.endpCalls++
		w.WriteHeader(f.endpStatus)
		io.WriteString(w, f.endpBody)
	default:
		http.NotFound(w, r)
	}
}

const goodEndpoints = `[
 {"id":1,"serviceArea":"Exchange","serviceAreaDisplayName":"Exchange Online",
  "urls":["outlook.office.com"],"ips":["13.107.6.153/31","2603:1026:0:5::9/36","bogus","2603:1036::1"],
  "tcpPorts":"80,443","category":"Optimize","required":true},
 {"id":2,"serviceArea":"Skype","serviceAreaDisplayName":"Microsoft Teams",
  "urls":["*.teams.microsoft.com"],"ips":["2603:1027::/48"],"udpPorts":"3478"},
 {"id":3,"serviceArea":"Common","serviceAreaDisplayName":"Microsoft 365 Common and Office Online",
  "urls":["*.office.com"]}
]`

// setup gives the test its own database, a fake Microsoft and the module's HTTP API.
func setup(t *testing.T) (*fakeMS, *httptest.Server) {
	t.Helper()
	ms := &fakeMS{}
	msSrv := httptest.NewServer(ms)

	oldBase, oldDB, oldInstance, oldID := msBaseURL, db, defaultInstance, clientRequestID
	msBaseURL = msSrv.URL
	defaultInstance = "Worldwide"
	clientRequestID = "test id&x"
	refreshVerified = map[string]bool{}
	if err := initDB(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(newMux(false))
	t.Cleanup(func() {
		api.Close()
		msSrv.Close()
		db.Close()
		msBaseURL, db, defaultInstance, clientRequestID = oldBase, oldDB, oldInstance, oldID
	})
	return ms, api
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b))
}

func serviceNames(t *testing.T, api *httptest.Server) string {
	t.Helper()
	code, body := get(t, api.URL+"/api/fqdn/services")
	if code != 200 {
		t.Fatalf("GET /api/fqdn/services: %d %s", code, body)
	}
	var services []fqdnService
	if err := json.Unmarshal([]byte(body), &services); err != nil {
		t.Fatalf("services body %s: %v", body, err)
	}
	names := make([]string, len(services))
	for i, s := range services {
		names[i] = s.Name
	}
	return strings.Join(names, "|")
}

func TestNoDataAnswers503(t *testing.T) {
	_, api := setup(t)
	for _, path := range []string{
		"/api/fqdn/services",
		"/api/fqdn/services/Microsoft%20Teams/endpoints",
		"/api/endpoints",
		"/api/edl/ipv6",
		"/api/endpoints/map?fqdn=*",
	} {
		if code, body := get(t, api.URL+path); code != http.StatusServiceUnavailable {
			t.Errorf("GET %s without data: %d %s, want 503", path, code, body)
		}
	}
}

func TestRefreshLoadsValidatedData(t *testing.T) {
	ms, api := setup(t)
	ms.set(`{"instance":"Worldwide","latest":"2026110100"}`, goodEndpoints)

	resp, err := refreshInstance(context.Background(), "Worldwide")
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Updated || resp.LatestRemote != "2026110100" || resp.Counts.EndpointSets != 3 || resp.Counts.Inserted != 7 {
		t.Fatalf("refresh response: %+v", resp)
	}

	if got, want := serviceNames(t, api), "Exchange Online|Microsoft 365 Common and Office Online|Microsoft Teams"; got != want {
		t.Fatalf("services = %q, want %q", got, want)
	}

	code, body := get(t, api.URL+"/api/fqdn/services/Exchange%20Online/endpoints")
	want := `{"fqdns":["outlook.office.com"],"ipv4_prefixes":["13.107.6.152/31"],"ipv6_prefixes":["2603:1026::/36","2603:1036::1/128"]}`
	if code != 200 || body != want {
		t.Fatalf("Exchange endpoints: %d %s\nwant %s", code, body, want)
	}
	// A known service without address ranges is 200, not 404.
	if code, body := get(t, api.URL+"/api/fqdn/services/Microsoft%20365%20Common%20and%20Office%20Online/endpoints"); code != 200 || body != `{"fqdns":["*.office.com"]}` {
		t.Fatalf("Common endpoints: %d %s", code, body)
	}
	if code, body := get(t, api.URL+"/api/fqdn/services/Nope/endpoints"); code != http.StatusNotFound {
		t.Fatalf("unknown service: %d %s, want 404", code, body)
	}
	if code, body := get(t, api.URL+"/api/fqdn/services?version=1999"); code != http.StatusNotFound {
		t.Fatalf("unknown explicit version: %d %s, want 404", code, body)
	}
	// Nothing unparseable was stored, under any type.
	if code, body := get(t, api.URL+"/api/edl/ipv4"); code != 200 || body != "13.107.6.152/31" {
		t.Fatalf("ipv4 list: %d %q", code, body)
	}

	// Same version again: no second download.
	resp, err = refreshInstance(context.Background(), "Worldwide")
	if err != nil || resp.Updated {
		t.Fatalf("second refresh: %+v, %v; want not updated", resp, err)
	}
	if ms.endpCalls != 1 {
		t.Fatalf("endpoints downloaded %d times, want 1", ms.endpCalls)
	}

	// The instance is path-escaped and the request ID query-escaped.
	for _, p := range ms.paths {
		if !strings.HasSuffix(p, "/Worldwide?ClientRequestId=test+id%26x") {
			t.Errorf("request to Microsoft: %s", p)
		}
	}
}

func TestBadResponseKeepsCurrentVersion(t *testing.T) {
	const goodVersion = `{"latest":"2026110100"}`
	const newVersion = `{"latest":"2026120100"}`

	tests := []struct {
		name string
		// break makes the fake Microsoft answer badly for a newer version.
		breakIt func(ms *fakeMS)
	}{
		{"empty endpoint list", func(ms *fakeMS) { ms.set(newVersion, `[]`) }},
		{"null endpoint list", func(ms *fakeMS) { ms.set(newVersion, `null`) }},
		{"sets without any usable record", func(ms *fakeMS) {
			ms.set(newVersion, `[{"id":1,"serviceArea":"Exchange","serviceAreaDisplayName":"Exchange Online","ips":["garbage","300.1.1.1/8"]}]`)
		}},
		{"endpoints not json", func(ms *fakeMS) { ms.set(newVersion, `<html>Service Unavailable</html>`) }},
		{"endpoints of the wrong shape", func(ms *fakeMS) { ms.set(newVersion, `{"error":"x"}`) }},
		{"endpoints HTTP 500", func(ms *fakeMS) { ms.set(newVersion, goodEndpoints); ms.endpStatus = 500 }},
		{"endpoints HTTP 429 with a json body", func(ms *fakeMS) { ms.set(newVersion, goodEndpoints); ms.endpStatus = 429 }},
		{"empty version string", func(ms *fakeMS) { ms.set(`{"latest":""}`, goodEndpoints) }},
		{"version without the field", func(ms *fakeMS) { ms.set(`{}`, goodEndpoints) }},
		{"blank version string", func(ms *fakeMS) { ms.set(`{"latest":"  "}`, goodEndpoints) }},
		{"version not json", func(ms *fakeMS) { ms.set(`oops`, goodEndpoints) }},
		{"version HTTP 503", func(ms *fakeMS) { ms.set(newVersion, goodEndpoints); ms.versionStatus = 503 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms, api := setup(t)
			ms.set(goodVersion, goodEndpoints)
			if _, err := refreshInstance(context.Background(), "Worldwide"); err != nil {
				t.Fatal(err)
			}
			before := serviceNames(t, api)
			_, epBefore := get(t, api.URL+"/api/fqdn/services/Microsoft%20Teams/endpoints")

			tt.breakIt(ms)
			_, err := refreshInstance(context.Background(), "Worldwide")
			if err == nil {
				t.Fatal("refresh accepted a bad response")
			}
			var up *upstreamError
			if !errors.As(err, &up) {
				t.Errorf("error %v is not an upstreamError", err)
			}
			// POST /refresh reports it as 502.
			resp, err := http.Post(api.URL+"/refresh", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadGateway {
				t.Errorf("POST /refresh: %d, want 502", resp.StatusCode)
			}

			if v, _ := getLatestVersion("Worldwide"); v != "2026110100" {
				t.Fatalf("current version = %q, want the previous 2026110100", v)
			}
			if after := serviceNames(t, api); after != before {
				t.Fatalf("services changed: %q -> %q", before, after)
			}
			if code, ep := get(t, api.URL+"/api/fqdn/services/Microsoft%20Teams/endpoints"); code != 200 || ep != epBefore {
				t.Fatalf("Teams endpoints changed: %d %s (before: %s)", code, ep, epBefore)
			}
		})
	}
}

func TestBadFirstResponseLeavesNoData(t *testing.T) {
	ms, api := setup(t)
	ms.set(`{"latest":"2026110100"}`, `[]`)
	if _, err := refreshInstance(context.Background(), "Worldwide"); err == nil {
		t.Fatal("refresh accepted an empty endpoint list")
	}
	if code, body := get(t, api.URL+"/api/fqdn/services"); code != http.StatusServiceUnavailable {
		t.Fatalf("services after a failed first load: %d %s, want 503", code, body)
	}
	if code, body := get(t, api.URL+"/api/fqdn/services/Microsoft%20Teams/endpoints"); code != http.StatusServiceUnavailable {
		t.Fatalf("endpoints after a failed first load: %d %s, want 503", code, body)
	}
}

func TestNewVersionReplacesOld(t *testing.T) {
	ms, api := setup(t)
	ms.set(`{"latest":"2026110100"}`, goodEndpoints)
	if _, err := refreshInstance(context.Background(), "Worldwide"); err != nil {
		t.Fatal(err)
	}
	ms.set(`{"latest":"2026120100"}`, `[{"id":2,"serviceArea":"Skype","serviceAreaDisplayName":"Microsoft Teams","ips":["2603:1063::/38"]}]`)
	resp, err := refreshInstance(context.Background(), "Worldwide")
	if err != nil || !resp.Updated || resp.PreviousVersion != "2026110100" {
		t.Fatalf("refresh: %+v, %v", resp, err)
	}
	if got := serviceNames(t, api); got != "Microsoft Teams" {
		t.Fatalf("services = %q, want only Microsoft Teams", got)
	}
	if code, body := get(t, api.URL+"/api/fqdn/services/Microsoft%20Teams/endpoints"); code != 200 || body != `{"ipv6_prefixes":["2603:1063::/38"]}` {
		t.Fatalf("Teams endpoints: %d %s", code, body)
	}
	if code, _ := get(t, api.URL+"/api/fqdn/services/Exchange%20Online/endpoints"); code != http.StatusNotFound {
		t.Fatalf("service gone in the new version: %d, want 404", code)
	}
	// The old version stays addressable.
	if code, body := get(t, api.URL+"/api/fqdn/services/Exchange%20Online/endpoints?version=2026110100"); code != 200 {
		t.Fatalf("old version: %d %s", code, body)
	}
}

// Rows of the current version written by an earlier build (unvalidated) are
// replaced by the first refresh of this process even if the version is unchanged.
func TestFirstRefreshReplacesUnverifiedData(t *testing.T) {
	ms, api := setup(t)
	if _, err := db.Exec(`INSERT INTO versions (instance, latest, last_checked) VALUES ('Worldwide', '2026110100', '2026-11-01')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO endpoint_set_meta (instance, source_version, endpoint_set_id, service_area, service_area_display_name)
		VALUES ('Worldwide', '2026110100', 1, 'Exchange', 'Exchange Online')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO records (instance, endpoint_set_id, type, value, source_version, first_seen)
		VALUES ('Worldwide', 1, 'ipv4', 'bogus', '2026110100', '2026-11-01')`); err != nil {
		t.Fatal(err)
	}
	ms.set(`{"latest":"2026110100"}`, goodEndpoints)
	if _, err := refreshInstance(context.Background(), "Worldwide"); err != nil {
		t.Fatal(err)
	}
	if code, body := get(t, api.URL+"/api/edl/ipv4"); code != 200 || body != "13.107.6.152/31" {
		t.Fatalf("ipv4 list after the first refresh: %d %q", code, body)
	}
}

func TestInstanceParameter(t *testing.T) {
	ms, api := setup(t)
	ms.set(`{"latest":"2026110100"}`, goodEndpoints)

	for _, path := range []string{"/api/fqdn/services?instance=Mars", "/api/endpoints?instance=..%2Fversion", "/api/fqdn/services/x/endpoints?instance=worldwide"} {
		if code, body := get(t, api.URL+path); code != http.StatusBadRequest {
			t.Errorf("GET %s: %d %s, want 400", path, code, body)
		}
	}
	resp, err := http.Post(api.URL+"/refresh?instance=Mars", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || len(ms.paths) != 0 {
		t.Fatalf("POST /refresh?instance=Mars: %d, %d call(s) to Microsoft; want 400 and none", resp.StatusCode, len(ms.paths))
	}

	resp, err = http.Post(api.URL+"/refresh?instance=China", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(ms.paths[0], "/version/China?") {
		t.Fatalf("POST /refresh?instance=China: %d, paths %v", resp.StatusCode, ms.paths)
	}
	// Loaded for China only: the default instance still has no data.
	if code, _ := get(t, api.URL+"/api/fqdn/services?instance=China"); code != 200 {
		t.Errorf("services of China: %d, want 200", code)
	}
	if code, _ := get(t, api.URL+"/api/fqdn/services"); code != http.StatusServiceUnavailable {
		t.Errorf("services of the default instance: %d, want 503", code)
	}
}

func TestHealthCORSAndDNSResolve(t *testing.T) {
	_, api := setup(t)

	req, _ := http.NewRequest("GET", api.URL+"/health", nil)
	req.Header.Set("Origin", "http://example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var health map[string]any
	json.NewDecoder(resp.Body).Decode(&health)
	resp.Body.Close()
	if resp.StatusCode != 200 || health["status"] != "ok" || health["ok"] != true {
		t.Fatalf("GET /health: %d %v", resp.StatusCode, health)
	}
	if v := resp.Header.Get("Access-Control-Allow-Origin"); v != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want none", v)
	}

	if code, _ := get(t, api.URL+"/api/dns/resolve?fqdn=localhost"); code != http.StatusNotFound {
		t.Fatalf("GET /api/dns/resolve without ENABLE_DNS_RESOLVE: %d, want 404", code)
	}
	enabled := httptest.NewServer(newMux(true))
	defer enabled.Close()
	if code, body := get(t, enabled.URL+"/api/dns/resolve"); code != http.StatusBadRequest {
		t.Fatalf("GET /api/dns/resolve with ENABLE_DNS_RESOLVE and no fqdn: %d %s, want 400", code, body)
	}
}
