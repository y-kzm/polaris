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

func (s *RuleStore) Delete(id int) error {
	res, err := s.db.Exec(`DELETE FROM rules WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("rule not found: %d", id)
	}
	return nil
}
