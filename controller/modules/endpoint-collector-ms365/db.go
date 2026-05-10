package main

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var db *sql.DB

func initDB(dbPath string) error {
	var err error
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	return createSchema()
}

func createSchema() error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS versions (
			instance TEXT PRIMARY KEY,
			latest TEXT,
			last_checked DATETIME
		);

		CREATE TABLE IF NOT EXISTS records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			instance TEXT NOT NULL,
			endpoint_set_id INTEGER NOT NULL,
			type TEXT NOT NULL,
			value TEXT NOT NULL,
			source_version TEXT NOT NULL,
			first_seen DATETIME NOT NULL,
			UNIQUE(instance, source_version, endpoint_set_id, type, value)
		);

		CREATE INDEX IF NOT EXISTS idx_records_lookup ON records(instance, source_version, type);

		CREATE TABLE IF NOT EXISTS endpoint_set_meta (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			instance TEXT NOT NULL,
			source_version TEXT NOT NULL,
			endpoint_set_id INTEGER NOT NULL,
			service_area TEXT,
			service_area_display_name TEXT,
			tcp_ports TEXT,
			udp_ports TEXT,
			express_route INTEGER,
			category TEXT,
			required_flag INTEGER,
			UNIQUE(instance, source_version, endpoint_set_id)
		);

		CREATE INDEX IF NOT EXISTS idx_meta_lookup ON endpoint_set_meta(instance, source_version);
	`)
	return err
}

func getLatestVersion(instance string) (string, error) {
	var v sql.NullString
	err := db.QueryRow(`SELECT latest FROM versions WHERE instance = ?`, instance).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v.String, err
}

func upsertVersion(instance, version string) error {
	_, err := db.Exec(`
		INSERT INTO versions (instance, latest, last_checked)
		VALUES (?, ?, ?)
		ON CONFLICT(instance) DO UPDATE SET latest = excluded.latest, last_checked = excluded.last_checked
	`, instance, version, time.Now().UTC())
	return err
}

type InsertStats struct {
	Inserted     int
	EndpointSets int
}

func insertEndpointData(instance, version string, sets []MSEndpointSet) (InsertStats, error) {
	tx, err := db.Begin()
	if err != nil {
		return InsertStats{}, err
	}
	defer tx.Rollback()

	var stats InsertStats
	now := time.Now().UTC()

	for _, set := range sets {
		_, err := tx.Exec(`
			INSERT INTO endpoint_set_meta
				(instance, source_version, endpoint_set_id, service_area, service_area_display_name,
				 tcp_ports, udp_ports, express_route, category, required_flag)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(instance, source_version, endpoint_set_id) DO NOTHING
		`, instance, version, set.ID, set.ServiceArea, set.ServiceAreaDisplayName,
			set.TCPPorts, set.UDPPorts, boolToInt(set.ExpressRoute), set.Category, boolToInt(set.Required))
		if err != nil {
			return InsertStats{}, fmt.Errorf("insert meta id=%d: %w", set.ID, err)
		}
		stats.EndpointSets++

		for _, u := range set.URLs {
			res, err := tx.Exec(`
				INSERT INTO records (instance, endpoint_set_id, type, value, source_version, first_seen)
				VALUES (?, ?, 'fqdn', ?, ?, ?)
				ON CONFLICT(instance, source_version, endpoint_set_id, type, value) DO NOTHING
			`, instance, set.ID, u, version, now)
			if err != nil {
				return InsertStats{}, fmt.Errorf("insert fqdn: %w", err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				stats.Inserted++
			}
		}

		for _, ip := range set.IPs {
			res, err := tx.Exec(`
				INSERT INTO records (instance, endpoint_set_id, type, value, source_version, first_seen)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(instance, source_version, endpoint_set_id, type, value) DO NOTHING
			`, instance, set.ID, classifyIP(ip), ip, version, now)
			if err != nil {
				return InsertStats{}, fmt.Errorf("insert ip: %w", err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				stats.Inserted++
			}
		}
	}

	return stats, tx.Commit()
}

func queryEndpoints(instance, version, recordType string, limit, offset int) ([]string, error) {
	query := `SELECT DISTINCT value FROM records WHERE instance = ? AND source_version = ?`
	args := []any{instance, version}
	if recordType != "" {
		query += ` AND type = ?`
		args = append(args, recordType)
	}
	query += ` ORDER BY value LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

type ServiceInfo struct {
	ServiceArea            string
	ServiceAreaDisplayName string
}

func queryServices(instance, version string) ([]ServiceInfo, error) {
	rows, err := db.Query(`
		SELECT DISTINCT service_area, COALESCE(service_area_display_name, service_area)
		FROM endpoint_set_meta
		WHERE instance = ? AND source_version = ? AND service_area IS NOT NULL
		ORDER BY service_area_display_name
	`, instance, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var services []ServiceInfo
	for rows.Next() {
		var si ServiceInfo
		if err := rows.Scan(&si.ServiceArea, &si.ServiceAreaDisplayName); err != nil {
			return nil, err
		}
		services = append(services, si)
	}
	return services, rows.Err()
}

type ServiceEndpoints struct {
	EndpointSetIDs []int
	FQDNs          []string
	IPv4Prefixes   []string
	IPv6Prefixes   []string
}

func queryServiceEndpoints(instance, version, displayName string) (ServiceEndpoints, error) {
	rows, err := db.Query(`
		SELECT endpoint_set_id FROM endpoint_set_meta
		WHERE instance = ? AND source_version = ?
		  AND COALESCE(service_area_display_name, service_area) = ?
	`, instance, version, displayName)
	if err != nil {
		return ServiceEndpoints{}, err
	}

	var setIDs []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return ServiceEndpoints{}, err
		}
		setIDs = append(setIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ServiceEndpoints{}, err
	}
	rows.Close()

	se := ServiceEndpoints{EndpointSetIDs: setIDs}
	if len(setIDs) == 0 {
		return se, nil
	}

	args := make([]any, 0, 2+len(setIDs))
	args = append(args, instance, version)
	ph := make([]string, len(setIDs))
	for i, id := range setIDs {
		ph[i] = "?"
		args = append(args, id)
	}

	rows2, err := db.Query(fmt.Sprintf(`
		SELECT DISTINCT type, value FROM records
		WHERE instance = ? AND source_version = ? AND endpoint_set_id IN (%s)
		ORDER BY value
	`, strings.Join(ph, ",")), args...)
	if err != nil {
		return ServiceEndpoints{}, err
	}
	defer rows2.Close()

	for rows2.Next() {
		var t, v string
		if err := rows2.Scan(&t, &v); err != nil {
			return ServiceEndpoints{}, err
		}
		switch t {
		case "fqdn":
			se.FQDNs = append(se.FQDNs, v)
		case "ipv4":
			se.IPv4Prefixes = append(se.IPv4Prefixes, v)
		case "ipv6":
			se.IPv6Prefixes = append(se.IPv6Prefixes, v)
		}
	}
	return se, rows2.Err()
}

func queryIPsForFQDNs(instance, version string, fqdns []string) (ipv4, ipv6 []string, err error) {
	if len(fqdns) == 0 {
		return nil, nil, nil
	}

	// Find endpoint set IDs that contain any of the matched FQDNs
	ph := make([]string, len(fqdns))
	args := make([]any, 0, 2+len(fqdns))
	args = append(args, instance, version)
	for i, f := range fqdns {
		ph[i] = "?"
		args = append(args, f)
	}

	rows, err := db.Query(fmt.Sprintf(`
		SELECT DISTINCT endpoint_set_id FROM records
		WHERE instance = ? AND source_version = ? AND type = 'fqdn' AND value IN (%s)
	`, strings.Join(ph, ",")), args...)
	if err != nil {
		return nil, nil, err
	}

	var setIDs []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		setIDs = append(setIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()

	if len(setIDs) == 0 {
		return nil, nil, nil
	}

	args2 := make([]any, 0, 2+len(setIDs))
	args2 = append(args2, instance, version)
	ph2 := make([]string, len(setIDs))
	for i, id := range setIDs {
		ph2[i] = "?"
		args2 = append(args2, id)
	}

	rows2, err := db.Query(fmt.Sprintf(`
		SELECT DISTINCT type, value FROM records
		WHERE instance = ? AND source_version = ? AND type IN ('ipv4', 'ipv6') AND endpoint_set_id IN (%s)
		ORDER BY value
	`, strings.Join(ph2, ",")), args2...)
	if err != nil {
		return nil, nil, err
	}
	defer rows2.Close()

	for rows2.Next() {
		var t, v string
		if err := rows2.Scan(&t, &v); err != nil {
			return nil, nil, err
		}
		if t == "ipv4" {
			ipv4 = append(ipv4, v)
		} else {
			ipv6 = append(ipv6, v)
		}
	}
	return ipv4, ipv6, rows2.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
