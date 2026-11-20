package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// AppliedPolicy is the policy as of the last successful apply. The periodic
// reconcile works from it, never from the rules and groups being edited.
type AppliedPolicy struct {
	Rules     []Rule  `json:"rules"`
	Groups    []Group `json:"groups"`
	AppliedAt string  `json:"applied_at"`
}

type PolicyStore struct{ db *sql.DB }

func NewPolicyStore(db *sql.DB) *PolicyStore { return &PolicyStore{db} }

// SaveApplied records the rules and groups that were just applied.
func (s *PolicyStore) SaveApplied(rules []Rule, groups []Group) (string, error) {
	appliedAt := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`INSERT INTO applied_policy(id, rules, groups, applied_at) VALUES(1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET rules=excluded.rules, groups=excluded.groups, applied_at=excluded.applied_at`,
		marshalJSON(rules), marshalJSON(groups), appliedAt)
	return appliedAt, err
}

// RenameNexthop points the rules of the applied policy at a router's new
// address, so that the periodic reconcile keeps enforcing them.
func (s *PolicyStore) RenameNexthop(oldAddr, newAddr string) error {
	applied, err := s.LoadApplied()
	if err != nil || applied == nil {
		return err
	}
	for i := range applied.Rules {
		if applied.Rules[i].Nexthop == oldAddr {
			applied.Rules[i].Nexthop = newAddr
		}
	}
	_, err = s.db.Exec(`UPDATE applied_policy SET rules=? WHERE id=1`, marshalJSON(applied.Rules))
	return err
}

// LoadApplied returns the applied policy, or nil if nothing was applied yet.
func (s *PolicyStore) LoadApplied() (*AppliedPolicy, error) {
	var rulesJSON, groupsJSON string
	var p AppliedPolicy
	err := s.db.QueryRow(`SELECT rules, groups, applied_at FROM applied_policy WHERE id=1`).
		Scan(&rulesJSON, &groupsJSON, &p.AppliedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(rulesJSON), &p.Rules); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(groupsJSON), &p.Groups); err != nil {
		return nil, err
	}
	return &p, nil
}

// PutServicePrefixes remembers the prefixes a service resolved to.
func (s *PolicyStore) PutServicePrefixes(name string, prefixes []string) error {
	_, err := s.db.Exec(`INSERT INTO service_cache(name, prefixes, resolved_at) VALUES(?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET prefixes=excluded.prefixes, resolved_at=excluded.resolved_at`,
		name, marshalJSON(prefixes), time.Now().UTC().Format(time.RFC3339))
	return err
}

// GetServicePrefixes returns the prefixes of the last successful resolution
// of a service and when it happened; ok is false if there was none.
func (s *PolicyStore) GetServicePrefixes(name string) (prefixes []string, resolvedAt string, ok bool, err error) {
	var prefixesJSON string
	err = s.db.QueryRow(`SELECT prefixes, resolved_at FROM service_cache WHERE name=?`, name).
		Scan(&prefixesJSON, &resolvedAt)
	if err == sql.ErrNoRows {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	if err := json.Unmarshal([]byte(prefixesJSON), &prefixes); err != nil {
		return nil, "", false, err
	}
	return prefixes, resolvedAt, true, nil
}
