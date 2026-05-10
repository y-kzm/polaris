// Package fqdn aggregates FQDN/IP endpoint data from one or more
// endpoint-collector services and exposes a unified view to the API layer.
package fqdn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type Service struct {
	Service     string `json:"service,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type Endpoints struct {
	FQDNs        []string `json:"fqdns,omitempty"`
	IPv4Prefixes []string `json:"ipv4_prefixes,omitempty"`
	IPv6Prefixes []string `json:"ipv6_prefixes,omitempty"`
}

type Client struct {
	baseURLs []string
	http     *http.Client
}

func NewClient(baseURLs []string) *Client {
	return &Client{baseURLs: baseURLs, http: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) listServicesFrom(ctx context.Context, baseURL string) ([]Service, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fqdn list %s: %w", baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fqdn list %s: HTTP %d", baseURL, resp.StatusCode)
	}
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	var services []Service
	if err := json.Unmarshal(raw, &services); err != nil {
		var wrapper struct {
			Services []Service `json:"services"`
		}
		if err2 := json.Unmarshal(raw, &wrapper); err2 != nil {
			return nil, err
		}
		services = wrapper.Services
	}
	return services, nil
}

// ListServices fetches services from all configured sources and returns a deduplicated
// union. Deduplication key is Name; first occurrence wins.
func (c *Client) ListServices(ctx context.Context) ([]Service, error) {
	seen := map[string]struct{}{}
	var result []Service
	for _, base := range c.baseURLs {
		services, err := c.listServicesFrom(ctx, base)
		if err != nil {
			continue
		}
		for _, s := range services {
			if _, exists := seen[s.Name]; !exists {
				seen[s.Name] = struct{}{}
				result = append(result, s)
			}
		}
	}
	if result == nil {
		result = []Service{}
	}
	return result, nil
}

func (c *Client) getEndpointsFrom(ctx context.Context, baseURL, serviceName string) (Endpoints, error) {
	u := fmt.Sprintf("%s/%s/endpoints", baseURL, url.PathEscape(serviceName))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Endpoints{}, fmt.Errorf("fqdn endpoints %s: %w", baseURL, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Endpoints{}, fmt.Errorf("fqdn endpoints %s: %w", baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Endpoints{}, fmt.Errorf("fqdn endpoints %s: HTTP %d", baseURL, resp.StatusCode)
	}
	var eps Endpoints
	if err := json.NewDecoder(resp.Body).Decode(&eps); err != nil {
		return Endpoints{}, err
	}
	return eps, nil
}

// GetEndpoints fetches endpoints for serviceName from all configured sources and returns
// a deduplicated union per field (fqdns, ipv4_prefixes, ipv6_prefixes).
func (c *Client) GetEndpoints(ctx context.Context, serviceName string) (Endpoints, error) {
	seenFQDN := map[string]struct{}{}
	seenIPv4 := map[string]struct{}{}
	seenIPv6 := map[string]struct{}{}
	var result Endpoints
	for _, base := range c.baseURLs {
		eps, err := c.getEndpointsFrom(ctx, base, serviceName)
		if err != nil {
			continue
		}
		for _, v := range eps.FQDNs {
			if _, ok := seenFQDN[v]; !ok {
				seenFQDN[v] = struct{}{}
				result.FQDNs = append(result.FQDNs, v)
			}
		}
		for _, v := range eps.IPv4Prefixes {
			if _, ok := seenIPv4[v]; !ok {
				seenIPv4[v] = struct{}{}
				result.IPv4Prefixes = append(result.IPv4Prefixes, v)
			}
		}
		for _, v := range eps.IPv6Prefixes {
			if _, ok := seenIPv6[v]; !ok {
				seenIPv6[v] = struct{}{}
				result.IPv6Prefixes = append(result.IPv6Prefixes, v)
			}
		}
	}
	return result, nil
}
