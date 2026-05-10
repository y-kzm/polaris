package main

import (
	"encoding/json"
	"net"
	"net/http"
	"path"
	"strconv"
	"time"
)

// Response types — core format matches endpoint-collector-static
type fqdnService struct {
	Service     string `json:"service"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type serviceEndpointsResp struct {
	FQDNs        []string `json:"fqdns,omitempty"`
	IPv4Prefixes []string `json:"ipv4_prefixes,omitempty"`
	IPv6Prefixes []string `json:"ipv6_prefixes,omitempty"`
}

type healthResp struct {
	OK   bool   `json:"ok"`
	Time string `json:"time"`
}

type refreshCounts struct {
	Inserted     int `json:"inserted"`
	EndpointSets int `json:"endpoint_sets"`
}

type refreshResp struct {
	Instance        string        `json:"instance"`
	PreviousVersion string        `json:"previous_version,omitempty"`
	LatestRemote    string        `json:"latest_remote"`
	Updated         bool          `json:"updated"`
	Counts          refreshCounts `json:"counts"`
}

type endpointsListResp struct {
	Instance string   `json:"instance"`
	Version  string   `json:"version"`
	Type     string   `json:"type,omitempty"`
	Items    []string `json:"items"`
}

type endpointsMapResp struct {
	FQDNQuery    string   `json:"fqdn_query"`
	Instance     string   `json:"instance"`
	Version      string   `json:"version"`
	MatchedFQDNs []string `json:"matched_fqdns"`
	IPv4Prefixes []string `json:"ipv4_prefixes"`
	IPv6Prefixes []string `json:"ipv6_prefixes"`
}

type dnsResolveResp struct {
	FQDN string   `json:"fqdn"`
	IPv4 []string `json:"ipv4"`
	IPv6 []string `json:"ipv6"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func getInstance(r *http.Request) string {
	if v := r.URL.Query().Get("instance"); v != "" {
		return v
	}
	return defaultInstance
}

// GET /health
func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResp{OK: true, Time: time.Now().UTC().Format(time.RFC3339)})
}

// POST /refresh
func handleRefresh(w http.ResponseWriter, r *http.Request) {
	instance := getInstance(r)

	prevVersion, err := getLatestVersion(instance)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	latestRemote, err := fetchLatestVersion(instance, clientRequestID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	resp := refreshResp{
		Instance:        instance,
		PreviousVersion: prevVersion,
		LatestRemote:    latestRemote,
	}

	if latestRemote == prevVersion {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	sets, err := fetchEndpoints(instance, clientRequestID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	stats, err := insertEndpointData(instance, latestRemote, sets)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := upsertVersion(instance, latestRemote); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp.Updated = true
	resp.Counts = refreshCounts{Inserted: stats.Inserted, EndpointSets: stats.EndpointSets}
	writeJSON(w, http.StatusOK, resp)
}

// GET /api/fqdn/services
func handleListServices(w http.ResponseWriter, r *http.Request) {
	instance := getInstance(r)
	version := r.URL.Query().Get("version")

	if version == "" {
		var err error
		version, err = getLatestVersion(instance)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	services, err := queryServices(instance, version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	result := make([]fqdnService, 0, len(services))
	for _, s := range services {
		result = append(result, fqdnService{
			Service:     s.ServiceAreaDisplayName,
			Name:        s.ServiceAreaDisplayName,
			Description: s.ServiceAreaDisplayName,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

// GET /api/fqdn/services/{name}/endpoints
func handleServiceEndpoints(w http.ResponseWriter, r *http.Request) {
	serviceArea := r.PathValue("name")
	instance := getInstance(r)
	version := r.URL.Query().Get("version")

	if version == "" {
		var err error
		version, err = getLatestVersion(instance)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	se, err := queryServiceEndpoints(instance, version, serviceArea)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, serviceEndpointsResp{
		FQDNs:        se.FQDNs,
		IPv4Prefixes: se.IPv4Prefixes,
		IPv6Prefixes: se.IPv6Prefixes,
	})
}

// GET /api/endpoints
func handleListEndpoints(w http.ResponseWriter, r *http.Request) {
	instance := getInstance(r)
	version := r.URL.Query().Get("version")
	recordType := r.URL.Query().Get("type")

	limit := 5000
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}

	if version == "" {
		var err error
		version, err = getLatestVersion(instance)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	items, err := queryEndpoints(instance, version, recordType, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []string{}
	}

	writeJSON(w, http.StatusOK, endpointsListResp{
		Instance: instance,
		Version:  version,
		Type:     recordType,
		Items:    items,
	})
}

// GET /api/edl/{type}  (fqdn / ipv4 / ipv6)
func handleEDL(w http.ResponseWriter, r *http.Request) {
	edlType := r.PathValue("type")
	if edlType != "fqdn" && edlType != "ipv4" && edlType != "ipv6" {
		http.NotFound(w, r)
		return
	}

	instance := getInstance(r)
	version := r.URL.Query().Get("version")

	if version == "" {
		var err error
		version, err = getLatestVersion(instance)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	items, err := queryEndpoints(instance, version, edlType, 100000, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, item := range items {
		w.Write([]byte(item + "\n"))
	}
}

// GET /api/endpoints/map
func handleEndpointsMap(w http.ResponseWriter, r *http.Request) {
	fqdnQuery := r.URL.Query().Get("fqdn")
	if fqdnQuery == "" {
		writeError(w, http.StatusBadRequest, "fqdn parameter required")
		return
	}
	instance := getInstance(r)
	version := r.URL.Query().Get("version")

	if version == "" {
		var err error
		version, err = getLatestVersion(instance)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	allFQDNs, err := queryEndpoints(instance, version, "fqdn", 100000, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// fnmatch-style wildcard filtering; path.Match treats '/' as separator,
	// but FQDNs never contain '/', so behaviour is identical to fnmatch.
	var matched []string
	for _, f := range allFQDNs {
		if ok, _ := path.Match(fqdnQuery, f); ok {
			matched = append(matched, f)
		}
	}

	var ipv4, ipv6 []string
	if len(matched) > 0 {
		ipv4, ipv6, err = queryIPsForFQDNs(instance, version, matched)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if matched == nil {
		matched = []string{}
	}
	if ipv4 == nil {
		ipv4 = []string{}
	}
	if ipv6 == nil {
		ipv6 = []string{}
	}

	writeJSON(w, http.StatusOK, endpointsMapResp{
		FQDNQuery:    fqdnQuery,
		Instance:     instance,
		Version:      version,
		MatchedFQDNs: matched,
		IPv4Prefixes: ipv4,
		IPv6Prefixes: ipv6,
	})
}

// GET /api/dns/resolve
func handleDNSResolve(w http.ResponseWriter, r *http.Request) {
	fqdn := r.URL.Query().Get("fqdn")
	if fqdn == "" {
		writeError(w, http.StatusBadRequest, "fqdn parameter required")
		return
	}

	addrs, err := net.LookupHost(fqdn)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	var ipv4, ipv6 []string
	for _, addr := range addrs {
		if ip := net.ParseIP(addr); ip != nil {
			if ip.To4() != nil {
				ipv4 = append(ipv4, addr)
			} else {
				ipv6 = append(ipv6, addr)
			}
		}
	}
	if ipv4 == nil {
		ipv4 = []string{}
	}
	if ipv6 == nil {
		ipv6 = []string{}
	}

	writeJSON(w, http.StatusOK, dnsResolveResp{FQDN: fqdn, IPv4: ipv4, IPv6: ipv6})
}
