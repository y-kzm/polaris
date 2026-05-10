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
	// Idempotent column add — ignore "duplicate column" error on existing DBs
	// that pre-date the source field.
	if _, err := db.Exec(`ALTER TABLE neighbors ADD COLUMN source TEXT NOT NULL DEFAULT ''`); err != nil {
		// SQLite reports "duplicate column name: source" when the column already exists.
		if !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("migrate neighbors.source: %w", err)
		}
	}
	return nil
}
