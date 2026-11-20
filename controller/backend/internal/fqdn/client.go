// Package fqdn queries the endpoint sources: HTTP servers that implement
// contract C3 of docs/design.md and answer "which prefixes belong to service
// X". The client merges the answers of all configured sources.
package fqdn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// ErrUnknownService means that every source that answered does not know the
// service.
var ErrUnknownService = errors.New("no endpoint source knows this service")

type Service struct {
	Service     string `json:"service,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type Endpoints struct {
	FQDNs        []string `json:"fqdns,omitempty"`
	IPv4Prefixes []string `json:"ipv4_prefixes,omitempty"`
	IPv6Prefixes []string `json:"ipv6_prefixes,omitempty"`
	// Incomplete is true if a source that may know the service did not
	// answer: the lists then hold only what the other sources returned.
	Incomplete bool `json:"incomplete,omitempty"`
}

// SourceStatus reports the health of an endpoint source.
type SourceStatus struct {
	URL         string `json:"url"`
	OK          bool   `json:"ok"`
	Error       string `json:"error"`
	LastSuccess string `json:"last_success"`
}

type sourceState struct {
	err         string
	lastSuccess time.Time
	asked       bool
}

type Client struct {
	baseURLs []string // each the URL of a source's GET /api/fqdn/services
	http     *http.Client

	mu    sync.Mutex
	state map[string]*sourceState
}

func NewClient(baseURLs []string) *Client {
	state := make(map[string]*sourceState, len(baseURLs))
	for _, u := range baseURLs {
		state[u] = &sourceState{}
	}
	return &Client{baseURLs: baseURLs, http: &http.Client{Timeout: 10 * time.Second}, state: state}
}

func (c *Client) record(baseURL string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.state[baseURL]
	st.asked = true
	if err != nil {
		st.err = err.Error()
		return
	}
	st.err, st.lastSuccess = "", time.Now()
}

// Statuses reports the health of every source, as of the last request to it.
func (c *Client) Statuses() []SourceStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]SourceStatus, 0, len(c.baseURLs))
	for _, u := range c.baseURLs {
		st := c.state[u]
		status := SourceStatus{URL: u, OK: st.asked && st.err == "", Error: st.err}
		if !st.lastSuccess.IsZero() {
			status.LastSuccess = st.lastSuccess.UTC().Format(time.RFC3339)
		}
		out = append(out, status)
	}
	return out
}

// get fetches a URL and decodes its JSON body; notFound reports a 404.
func (c *Client) get(ctx context.Context, u string, v any) (notFound bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return false, json.NewDecoder(resp.Body).Decode(v)
}

func (c *Client) listServicesFrom(ctx context.Context, baseURL string) ([]Service, error) {
	var raw json.RawMessage
	notFound, err := c.get(ctx, baseURL, &raw)
	if err == nil && notFound {
		err = errors.New("HTTP 404")
	}
	if err != nil {
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

// ListServices returns the union of the services of all sources (first
// occurrence of a name wins). It fails only if every source failed: an
// outage must not look like "there are no services".
func (c *Client) ListServices(ctx context.Context) ([]Service, error) {
	seen := map[string]struct{}{}
	result := []Service{}
	var errs []error
	for _, base := range c.baseURLs {
		services, err := c.listServicesFrom(ctx, base)
		c.record(base, err)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", base, err))
			continue
		}
		for _, s := range services {
			if _, exists := seen[s.Name]; !exists {
				seen[s.Name] = struct{}{}
				result = append(result, s)
			}
		}
	}
	if len(errs) == len(c.baseURLs) && len(errs) > 0 {
		return nil, fmt.Errorf("no endpoint source answered: %w", errors.Join(errs...))
	}
	return result, nil
}

// GetEndpoints returns the union of a service's endpoints over all sources.
// It returns ErrUnknownService if the sources that answered do not know the
// service, and another error if no source answered.
func (c *Client) GetEndpoints(ctx context.Context, serviceName string) (Endpoints, error) {
	var result Endpoints
	seen := map[string]struct{}{}
	add := func(dst *[]string, kind string, values []string) {
		for _, v := range values {
			if _, ok := seen[kind+v]; !ok {
				seen[kind+v] = struct{}{}
				*dst = append(*dst, v)
			}
		}
	}
	var errs []error
	known := false
	for _, base := range c.baseURLs {
		var eps Endpoints
		u := fmt.Sprintf("%s/%s/endpoints", base, url.PathEscape(serviceName))
		notFound, err := c.get(ctx, u, &eps)
		c.record(base, err)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", base, err))
			continue
		}
		if notFound {
			continue
		}
		known = true
		add(&result.FQDNs, "f", eps.FQDNs)
		add(&result.IPv4Prefixes, "4", eps.IPv4Prefixes)
		add(&result.IPv6Prefixes, "6", eps.IPv6Prefixes)
	}
	switch {
	case known:
		result.Incomplete = len(errs) > 0
		return result, nil
	case len(errs) > 0:
		return Endpoints{}, fmt.Errorf("no endpoint source answered: %w", errors.Join(errs...))
	default:
		return Endpoints{}, ErrUnknownService
	}
}
