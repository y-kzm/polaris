package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// retryInterval is how soon a failed periodic refresh is tried again.
const retryInterval = time.Minute

// upstreamError marks a refresh that failed because of Microsoft's answer
// (or the lack of one), as opposed to a local database error.
type upstreamError struct{ err error }

func (e *upstreamError) Error() string { return e.err.Error() }
func (e *upstreamError) Unwrap() error { return e.err }

var (
	// refreshMu serialises refreshes: the periodic one and POST /refresh.
	refreshMu sync.Mutex
	// refreshVerified records the instances whose current version was stored
	// by this process, i.e. went through sanitizeEndpointSets. Until then a
	// refresh downloads the data even if the version is unchanged, so data
	// left by an earlier build is replaced once. Guarded by refreshMu.
	refreshVerified = map[string]bool{}
)

// refreshInstance checks Microsoft's current version for the instance and,
// if it is new, downloads, validates and stores the endpoints and switches
// the current version, all in one transaction. On any error the current
// version and its data stay as they are.
func refreshInstance(ctx context.Context, instance string) (refreshResp, error) {
	refreshMu.Lock()
	defer refreshMu.Unlock()

	resp := refreshResp{Instance: instance}

	prevVersion, err := getLatestVersion(instance)
	if err != nil {
		return resp, err
	}
	resp.PreviousVersion = prevVersion

	latestRemote, err := fetchLatestVersion(ctx, instance, clientRequestID)
	if err != nil {
		return resp, &upstreamError{err}
	}
	if latestRemote == "" {
		return resp, &upstreamError{errors.New("version response has no version")}
	}
	resp.LatestRemote = latestRemote

	if latestRemote == prevVersion && refreshVerified[instance] {
		return resp, nil
	}

	sets, err := fetchEndpoints(ctx, instance, clientRequestID)
	if err != nil {
		return resp, &upstreamError{err}
	}
	if len(sets) == 0 {
		return resp, &upstreamError{errors.New("endpoint list is empty")}
	}
	clean, records, dropped := sanitizeEndpointSets(sets)
	if records == 0 {
		return resp, &upstreamError{fmt.Errorf("endpoint list has %d set(s) but no usable FQDN or address range", len(sets))}
	}
	if dropped > 0 {
		log.Printf("refresh %s %s: dropped %d invalid address range(s)", instance, latestRemote, dropped)
	}

	stats, err := storeEndpointData(instance, latestRemote, clean)
	if err != nil {
		return resp, err
	}
	refreshVerified[instance] = true

	resp.Updated = true
	resp.Counts = refreshCounts{Inserted: stats.Inserted, EndpointSets: stats.EndpointSets}
	return resp, nil
}

// runRefresher refreshes the instance once immediately and then every
// interval until ctx is cancelled. A failed refresh is retried after
// retryInterval (or interval, if shorter).
func runRefresher(ctx context.Context, instance string, interval time.Duration) {
	retry := min(interval, retryInterval)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		next := interval
		resp, err := refreshInstance(ctx, instance)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			next = retry
			log.Printf("refresh %s: %v (keeping version %q, retry in %s)", instance, err, resp.PreviousVersion, next)
		case resp.Updated:
			log.Printf("refresh %s: version %s loaded (%d endpoint set(s), %d record(s))",
				instance, resp.LatestRemote, resp.Counts.EndpointSets, resp.Counts.Inserted)
		default:
			log.Printf("refresh %s: version %s is current", instance, resp.LatestRemote)
		}
		timer.Reset(next)
	}
}
