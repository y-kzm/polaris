package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

type GroupStore struct{ db *sql.DB }

func NewGroupStore(db *sql.DB) *GroupStore { return &GroupStore{db} }

func (s *GroupStore) List() ([]Group, error) {
	rows, err := s.db.Query(`SELECT id,name,rules,members,created_at,updated_at FROM groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []Group
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, *g)
	}
	if groups == nil {
		groups = []Group{}
	}
	return groups, rows.Err()
}

func (s *GroupStore) Get(id int) (*Group, error) {
	row := s.db.QueryRow(`SELECT id,name,rules,members,created_at,updated_at FROM groups WHERE id=?`, id)
	var g Group
	var rulesJSON, membersJSON string
	err := row.Scan(&g.ID, &g.Name, &rulesJSON, &membersJSON, &g.CreatedAt, &g.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(rulesJSON), &g.Rules); err != nil {
		g.Rules = []int{}
	}
	if err := json.Unmarshal([]byte(membersJSON), &g.Members); err != nil {
		g.Members = []string{}
	}
	return &g, nil
}

func (s *GroupStore) Create(g *Group) (int, error) {
	rulesJSON := marshalJSON(g.Rules)
	membersJSON := marshalJSON(g.Members)
	res, err := s.db.Exec(`INSERT INTO groups(name,rules,members) VALUES(?,?,?)`,
		g.Name, rulesJSON, membersJSON)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return int(id), nil
}

func (s *GroupStore) UpdateRules(id int, rules []int) error {
	rulesJSON := marshalJSON(rules)
	res, err := s.db.Exec(`UPDATE groups SET rules=?, updated_at=datetime('now') WHERE id=?`, rulesJSON, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("group not found: %d", id)
	}
	return nil
}

func (s *GroupStore) Delete(id int) error {
	res, err := s.db.Exec(`DELETE FROM groups WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("group not found: %d", id)
	}
	return nil
}

func scanGroup(rows *sql.Rows) (*Group, error) {
	var g Group
	var rulesJSON, membersJSON string
	if err := rows.Scan(&g.ID, &g.Name, &rulesJSON, &membersJSON, &g.CreatedAt, &g.UpdatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(rulesJSON), &g.Rules); err != nil {
		g.Rules = []int{}
	}
	if err := json.Unmarshal([]byte(membersJSON), &g.Members); err != nil {
		g.Members = []string{}
	}
	return &g, nil
}
