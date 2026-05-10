package store

import (
	"database/sql"
	"fmt"
)

type RouterStore struct{ db *sql.DB }

func NewRouterStore(db *sql.DB) *RouterStore { return &RouterStore{db} }

func (s *RouterStore) List() ([]Router, error) {
	rows, err := s.db.Query(`SELECT id,name,address,interface,status,ra_interval_ms,current_hop_limit,managed,other,router_lifetime_s,reachable_time_ms,retransmit_time_ms,created_at,updated_at FROM routers ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var routers []Router
	for rows.Next() {
		var r Router
		var managed, other int
		if err := rows.Scan(&r.ID, &r.Name, &r.Address, &r.Interface, &r.Status,
			&r.RAIntervalMs, &r.CurrentHopLimit, &managed, &other,
			&r.RouterLifetimeS, &r.ReachableTimeMs, &r.RetransmitTimeMs,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Managed = managed == 1
		r.Other = other == 1
		routers = append(routers, r)
	}
	if routers == nil {
		routers = []Router{}
	}
	return routers, rows.Err()
}

func (s *RouterStore) Get(id string) (*Router, error) {
	var r Router
	var managed, other int
	err := s.db.QueryRow(`SELECT id,name,address,interface,status,ra_interval_ms,current_hop_limit,managed,other,router_lifetime_s,reachable_time_ms,retransmit_time_ms,created_at,updated_at FROM routers WHERE id=?`, id).
		Scan(&r.ID, &r.Name, &r.Address, &r.Interface, &r.Status,
			&r.RAIntervalMs, &r.CurrentHopLimit, &managed, &other,
			&r.RouterLifetimeS, &r.ReachableTimeMs, &r.RetransmitTimeMs,
			&r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Managed = managed == 1
	r.Other = other == 1
	return &r, nil
}

func (s *RouterStore) Create(r *Router) error {
	_, err := s.db.Exec(`INSERT INTO routers(id,name,address,interface,status,ra_interval_ms,current_hop_limit,managed,other,router_lifetime_s,reachable_time_ms,retransmit_time_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.Name, r.Address, r.Interface, r.Status,
		r.RAIntervalMs, r.CurrentHopLimit, boolInt(r.Managed), boolInt(r.Other),
		r.RouterLifetimeS, r.ReachableTimeMs, r.RetransmitTimeMs)
	return err
}

func (s *RouterStore) Update(r *Router) error {
	res, err := s.db.Exec(`UPDATE routers SET name=?,address=?,interface=?,status=?,ra_interval_ms=?,current_hop_limit=?,managed=?,other=?,router_lifetime_s=?,reachable_time_ms=?,retransmit_time_ms=?,updated_at=datetime('now') WHERE id=?`,
		r.Name, r.Address, r.Interface, r.Status,
		r.RAIntervalMs, r.CurrentHopLimit, boolInt(r.Managed), boolInt(r.Other),
		r.RouterLifetimeS, r.ReachableTimeMs, r.RetransmitTimeMs, r.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("router not found: %s", r.ID)
	}
	return nil
}

func (s *RouterStore) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM routers WHERE id=?`, id)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
