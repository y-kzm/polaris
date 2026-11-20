package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// msBaseURL is the Microsoft 365 endpoint web service. It is a variable so
// that tests can point it at a local server.
var msBaseURL = "https://endpoints.office.com"

// msClient is used for every call to Microsoft.
var msClient = &http.Client{Timeout: 30 * time.Second}

// maxResponseBytes bounds what is read from one Microsoft response.
const maxResponseBytes = 32 << 20

// knownInstances are the instances of the Microsoft 365 endpoint web service.
var knownInstances = map[string]bool{
	"Worldwide":    true,
	"USGovDoD":     true,
	"USGovGCCHigh": true,
	"China":        true,
}

type MSVersionResponse struct {
	Latest string `json:"latest"`
}

type MSEndpointSet struct {
	ID                     int      `json:"id"`
	ServiceArea            string   `json:"serviceArea"`
	ServiceAreaDisplayName string   `json:"serviceAreaDisplayName"`
	URLs                   []string `json:"urls"`
	IPs                    []string `json:"ips"`
	TCPPorts               string   `json:"tcpPorts"`
	UDPPorts               string   `json:"udpPorts"`
	ExpressRoute           bool     `json:"expressRoute"`
	Category               string   `json:"category"`
	Required               bool     `json:"required"`
}

// msGet fetches {msBaseURL}/{method}/{instance} and decodes the JSON body
// into v. Any status other than 200 is an error.
func msGet(ctx context.Context, method, instance, clientRequestID string, v any) error {
	u := fmt.Sprintf("%s/%s/%s?ClientRequestId=%s", msBaseURL, method, url.PathEscape(instance), url.QueryEscape(clientRequestID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", method, err)
	}
	resp, err := msClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: HTTP %d", method, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(v); err != nil {
		return fmt.Errorf("decode %s: %w", method, err)
	}
	return nil
}

func fetchLatestVersion(ctx context.Context, instance, clientRequestID string) (string, error) {
	var raw json.RawMessage
	if err := msGet(ctx, "version", instance, clientRequestID, &raw); err != nil {
		return "", err
	}

	// MS API returns an object for /version/{instance} and an array for /version.
	var arr []MSVersionResponse
	if err := json.Unmarshal(raw, &arr); err == nil && len(arr) > 0 {
		return strings.TrimSpace(arr[0].Latest), nil
	}
	var obj MSVersionResponse
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", fmt.Errorf("parse version response: %w", err)
	}
	return strings.TrimSpace(obj.Latest), nil
}

func fetchEndpoints(ctx context.Context, instance, clientRequestID string) ([]MSEndpointSet, error) {
	var sets []MSEndpointSet
	if err := msGet(ctx, "endpoints", instance, clientRequestID, &sets); err != nil {
		return nil, err
	}
	return sets, nil
}

// canonicalPrefix parses an address range as published by Microsoft: a
// prefix, or a bare address taken as a host prefix. It returns the masked
// canonical form and its family ("ipv4" or "ipv6"), classified from the
// parsed value. ok is false for anything else, including zoned addresses
// and IPv4-mapped IPv6 addresses, which belong to neither list.
func canonicalPrefix(s string) (value, family string, ok bool) {
	s = strings.TrimSpace(s)
	p, err := netip.ParsePrefix(s)
	if err != nil {
		a, err := netip.ParseAddr(s)
		if err != nil || a.Zone() != "" {
			return "", "", false
		}
		p = netip.PrefixFrom(a, a.BitLen())
	}
	p = p.Masked()
	switch a := p.Addr(); {
	case a.Is4():
		return p.String(), "ipv4", true
	case a.Is4In6():
		return "", "", false
	default:
		return p.String(), "ipv6", true
	}
}

// sanitizeEndpointSets returns the sets with every address range replaced by
// its canonical form and invalid or empty entries removed. dropped counts
// the removed address ranges; records counts the FQDNs and ranges that
// remain. The input is not modified.
func sanitizeEndpointSets(sets []MSEndpointSet) (clean []MSEndpointSet, records, dropped int) {
	clean = make([]MSEndpointSet, 0, len(sets))
	for _, set := range sets {
		urls := make([]string, 0, len(set.URLs))
		for _, u := range set.URLs {
			if u = strings.TrimSpace(u); u != "" {
				urls = append(urls, u)
			}
		}
		ips := make([]string, 0, len(set.IPs))
		for _, ip := range set.IPs {
			v, _, ok := canonicalPrefix(ip)
			if !ok {
				dropped++
				continue
			}
			ips = append(ips, v)
		}
		set.URLs = urls
		set.IPs = ips
		if set.ServiceAreaDisplayName == "" {
			set.ServiceAreaDisplayName = set.ServiceArea // services are listed and looked up by display name
		}
		records += len(urls) + len(ips)
		clean = append(clean, set)
	}
	return clean, records, dropped
}
