// Package store provides SQLite-backed persistence for routers, rules,
// groups, and neighbors using plain database/sql queries.
package store

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite serializes writes; a single connection avoids "database is locked" errors.
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS routers (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			address TEXT NOT NULL,
			interface TEXT NOT NULL DEFAULT '',
			policy_source_address TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			ra_interval_ms INTEGER NOT NULL DEFAULT 3000,
			current_hop_limit INTEGER NOT NULL DEFAULT 64,
			managed INTEGER NOT NULL DEFAULT 0,
			other INTEGER NOT NULL DEFAULT 0,
			router_lifetime_s INTEGER NOT NULL DEFAULT 1800,
			reachable_time_ms INTEGER NOT NULL DEFAULT 0,
			retransmit_time_ms INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE TABLE IF NOT EXISTS rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			comment TEXT NOT NULL DEFAULT '',
			entries TEXT NOT NULL DEFAULT '[]',
			nexthop TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE TABLE IF NOT EXISTS groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			rules TEXT NOT NULL DEFAULT '[]',
			members TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE TABLE IF NOT EXISTS applied_policy (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			rules TEXT NOT NULL,
			groups TEXT NOT NULL,
			applied_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS service_cache (
			name TEXT PRIMARY KEY,
			prefixes TEXT NOT NULL,
			resolved_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS neighbors (
			id TEXT PRIMARY KEY,
			lladdr TEXT NOT NULL DEFAULT '',
			ifindex INTEGER NOT NULL DEFAULT 0,
			ifname TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL DEFAULT '',
			is_router INTEGER NOT NULL DEFAULT 0,
			source TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
	`)
	if err != nil {
		return err
	}
	// Columns added after the first release. SQLite has no ADD COLUMN IF NOT
	// EXISTS, so the "duplicate column name" error of an up-to-date DB is
	// ignored.
	added := []struct{ table, column string }{
		{"neighbors", "source"},
		{"routers", "policy_source_address"},
	}
	for _, c := range added {
		stmt := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s TEXT NOT NULL DEFAULT ''`, c.table, c.column)
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("migrate %s.%s: %w", c.table, c.column, err)
		}
	}
	return nil
}
