package store

import (
	"database/sql"
)

type NeighborStore struct{ db *sql.DB }

func NewNeighborStore(db *sql.DB) *NeighborStore { return &NeighborStore{db} }

func (s *NeighborStore) List() ([]Neighbor, error) {
	rows, err := s.db.Query(`SELECT id,lladdr,ifindex,ifname,state,is_router,source,updated_at FROM neighbors ORDER BY updated_at DESC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var neighbors []Neighbor
	for rows.Next() {
		var n Neighbor
		if err := rows.Scan(&n.ID, &n.LLAddr, &n.IfIndex, &n.IfName, &n.State, &n.IsRouter, &n.Source, &n.UpdatedAt); err != nil {
			return nil, err
		}
		neighbors = append(neighbors, n)
	}
	if neighbors == nil {
		neighbors = []Neighbor{}
	}
	return neighbors, rows.Err()
}

// Replace atomically swaps the entire neighbor table with the provided list.
// This is the primary write path used by the Watcher; it avoids stale entries
// without needing a separate reconciliation pass.
func (s *NeighborStore) Replace(neighbors []Neighbor) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM neighbors`); err != nil {
		tx.Rollback()
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO neighbors(id,lladdr,ifindex,ifname,state,is_router,source,updated_at) VALUES(?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, n := range neighbors {
		if _, err := stmt.Exec(n.ID, n.LLAddr, n.IfIndex, n.IfName, n.State, n.IsRouter, n.Source, n.UpdatedAt); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

