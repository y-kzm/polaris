package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
)

const msBaseURL = "https://endpoints.office.com"

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

func fetchLatestVersion(instance, clientRequestID string) (string, error) {
	url := fmt.Sprintf("%s/version/%s?ClientRequestId=%s", msBaseURL, instance, clientRequestID)
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("fetch version: %w", err)
	}
	defer resp.Body.Close()

	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", fmt.Errorf("decode version: %w", err)
	}

	// MS API returns an array for /version/{instance}
	var arr []MSVersionResponse
	if err := json.Unmarshal(raw, &arr); err == nil && len(arr) > 0 {
		return arr[0].Latest, nil
	}
	var obj MSVersionResponse
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", fmt.Errorf("parse version response: %w", err)
	}
	return obj.Latest, nil
}

func fetchEndpoints(instance, clientRequestID string) ([]MSEndpointSet, error) {
	url := fmt.Sprintf("%s/endpoints/%s?ClientRequestId=%s", msBaseURL, instance, clientRequestID)
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("fetch endpoints: %w", err)
	}
	defer resp.Body.Close()

	var sets []MSEndpointSet
	if err := json.NewDecoder(resp.Body).Decode(&sets); err != nil {
		return nil, fmt.Errorf("decode endpoints: %w", err)
	}
	return sets, nil
}

// classifyIP determines whether an IP prefix is IPv4 or IPv6.
func classifyIP(ip string) string {
	host, _, _ := strings.Cut(ip, "/")
	parsed := net.ParseIP(host)
	if parsed != nil && parsed.To4() == nil {
		return "ipv6"
	}
	return "ipv4"
}
