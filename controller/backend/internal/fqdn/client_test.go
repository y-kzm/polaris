package fqdn

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetEndpoints(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Teams/endpoints" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"ipv6_prefixes":["2603:1027::/48"]}`))
	}))
	defer good.Close()
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "loading", http.StatusServiceUnavailable)
	}))
	defer down.Close()
	ctx := context.Background()

	// One source down, one answering: the answer is used.
	c := NewClient([]string{down.URL, good.URL})
	eps, err := c.GetEndpoints(ctx, "Teams")
	if err != nil || len(eps.IPv6Prefixes) != 1 || !eps.Incomplete {
		t.Fatalf("eps=%+v err=%v", eps, err)
	}
	if st := c.Statuses(); st[0].OK || !st[1].OK {
		t.Errorf("statuses = %+v", st)
	}

	// The source that answers does not know the service.
	if _, err := NewClient([]string{good.URL}).GetEndpoints(ctx, "Nope"); !errors.Is(err, ErrUnknownService) {
		t.Errorf("unknown service: err = %v", err)
	}

	// An outage is an error, not "no prefixes".
	if _, err := NewClient([]string{down.URL}).GetEndpoints(ctx, "Teams"); err == nil || errors.Is(err, ErrUnknownService) {
		t.Errorf("all sources down: err = %v", err)
	}
	if _, err := NewClient([]string{down.URL}).ListServices(ctx); err == nil {
		t.Error("ListServices with all sources down must fail")
	}
}
