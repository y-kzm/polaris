package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

type RuleStore struct{ db *sql.DB }

func NewRuleStore(db *sql.DB) *RuleStore { return &RuleStore{db} }

func (s *RuleStore) List() ([]Rule, error) {
	rows, err := s.db.Query(`SELECT id,comment,entries,nexthop,created_at,updated_at FROM rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []Rule
	for rows.Next() {
		var r Rule
		var entriesJSON string
		if err := rows.Scan(&r.ID, &r.Comment, &entriesJSON, &r.Nexthop, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(entriesJSON), &r.Entries); err != nil {
			r.Entries = []Entry{}
		}
		rules = append(rules, r)
	}
	if rules == nil {
		rules = []Rule{}
	}
	return rules, rows.Err()
}

func (s *RuleStore) Get(id int) (*Rule, error) {
	var r Rule
	var entriesJSON string
	err := s.db.QueryRow(`SELECT id,comment,entries,nexthop,created_at,updated_at FROM rules WHERE id=?`, id).
		Scan(&r.ID, &r.Comment, &entriesJSON, &r.Nexthop, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(entriesJSON), &r.Entries); err != nil {
		r.Entries = []Entry{}
	}
	return &r, nil
}

func (s *RuleStore) Create(r *Rule) (int, error) {
	entriesJSON := marshalJSON(r.Entries)
	res, err := s.db.Exec(`INSERT INTO rules(comment,entries,nexthop) VALUES(?,?,?)`,
		r.Comment, entriesJSON, r.Nexthop)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return int(id), nil
}

func (s *RuleStore) Update(r *Rule) error {
	res, err := s.db.Exec(`UPDATE rules SET comment=?, entries=?, nexthop=?, updated_at=datetime('now') WHERE id=?`,
		r.Comment, marshalJSON(r.Entries), r.Nexthop, r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("rule not found: %d", r.ID)
	}
	return nil
}

// RenameNexthop points every rule at a router's new address.
func (s *RuleStore) RenameNexthop(oldAddr, newAddr string) error {
	_, err := s.db.Exec(`UPDATE rules SET nexthop=?, updated_at=datetime('now') WHERE nexthop=?`, newAddr, oldAddr)
	return err
}

// Delete removes a rule and, in the same transaction, its ID from every
// group, so that no group is left pointing at a rule that does not exist.
func (s *RuleStore) Delete(id int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`DELETE FROM rules WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("rule not found: %d", id)
	}

	rows, err := tx.Query(`SELECT id, rules FROM groups`)
	if err != nil {
		return err
	}
	updates := map[int]string{}
	for rows.Next() {
		var groupID int
		var rulesJSON string
		if err := rows.Scan(&groupID, &rulesJSON); err != nil {
			rows.Close()
			return err
		}
		var ruleIDs []int
		if err := json.Unmarshal([]byte(rulesJSON), &ruleIDs); err != nil {
			continue
		}
		kept := make([]int, 0, len(ruleIDs))
		for _, ruleID := range ruleIDs {
			if ruleID != id {
				kept = append(kept, ruleID)
			}
		}
		if len(kept) != len(ruleIDs) {
			updates[groupID] = marshalJSON(kept)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for groupID, rulesJSON := range updates {
		if _, err := tx.Exec(`UPDATE groups SET rules=?, updated_at=datetime('now') WHERE id=?`, rulesJSON, groupID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
