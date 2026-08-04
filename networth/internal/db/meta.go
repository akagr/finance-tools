package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/akagr/finance-tools/networth/internal/money"
)

// Settings are the display and reporting preferences stored inside the database,
// so that a file carries its own presentation rules wherever it goes.
type Settings struct {
	BaseCurrency string
	Grouping     money.Grouping
	FYStartMonth int
	Decimals     int
}

// Settings reads the settings table, falling back to sane defaults for any key
// a hand-edited or older database is missing.
func (d *DB) Settings() (Settings, error) {
	s := Settings{BaseCurrency: "INR", Grouping: money.GroupIndian, FYStartMonth: 4}
	rows, err := d.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return s, err
		}
		switch k {
		case "base_currency":
			s.BaseCurrency = v
		case "grouping":
			s.Grouping = money.Grouping(v)
		case "fy_start_month":
			if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 12 {
				s.FYStartMonth = n
			}
		case "decimals":
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= money.Places {
				s.Decimals = n
			}
		}
	}
	return s, rows.Err()
}

// SetSetting writes one preference.
func (d *DB) SetSetting(key, value string) error {
	_, err := d.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// UIState reads a piece of application state. Storing the interface's own state
// in the database is what makes handing over the file hand over the whole
// application, not just its numbers.
func (d *DB) UIState(key, def string) string {
	var v string
	if err := d.QueryRow(`SELECT value FROM ui_state WHERE key = ?`, key).Scan(&v); err != nil {
		return def
	}
	return v
}

// SetUIState stores a piece of application state.
func (d *DB) SetUIState(key, value string) error {
	_, err := d.Exec(`INSERT INTO ui_state (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// Meta reads a value from app_meta.
func (d *DB) Meta(key string) string {
	var v string
	if err := d.QueryRow(`SELECT value FROM app_meta WHERE key = ?`, key).Scan(&v); err != nil {
		return ""
	}
	return v
}

// Backup writes a consistent copy of the database to dst using VACUUM INTO,
// which is atomic and safe to run while the database is open. With no plain-text
// source to fall back on, routine backups carry more weight than they would in a
// file-per-fact design, so every destructive command takes one first.
func (d *DB) Backup(dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	_, err := d.Exec(`VACUUM INTO ?`, dst)
	return err
}

// AutoBackup writes a timestamped copy next to the database and prunes all but
// the newest keep copies.
func (d *DB) AutoBackup(keep int) (string, error) {
	dir := filepath.Join(filepath.Dir(d.Path), "backups")
	name := fmt.Sprintf("%s.%s.db", filepath.Base(d.Path), time.Now().UTC().Format("20060102T150405Z"))
	dst := filepath.Join(dir, name)
	if err := d.Backup(dst); err != nil {
		return "", err
	}
	pruneBackups(dir, filepath.Base(d.Path), keep)
	return dst, nil
}

func pruneBackups(dir, prefix string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > len(prefix) && e.Name()[:len(prefix)] == prefix {
			names = append(names, e.Name())
		}
	}
	if len(names) <= keep {
		return
	}
	// Names embed a sortable UTC timestamp, so lexical order is chronological.
	for i := 0; i < len(names)-keep; i++ {
		os.Remove(filepath.Join(dir, names[i]))
	}
}

// Issue is one problem found by the quality checks.
type Issue struct {
	Check    string
	Severity string
	Detail   string
}

// Check runs every invariant in v_quality. It reports rather than fails: the
// database is meant to be edited by hand and by other tools, so a violation must
// be visible and fixable rather than fatal.
func (d *DB) Check() ([]Issue, error) {
	rows, err := d.Query(`SELECT check_name, severity, detail FROM v_quality ORDER BY severity, check_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Issue
	for rows.Next() {
		var i Issue
		if err := rows.Scan(&i.Check, &i.Severity, &i.Detail); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Integrity runs SQLite's own structural check on the file.
func (d *DB) Integrity() (string, error) {
	var s string
	err := d.QueryRow(`PRAGMA integrity_check`).Scan(&s)
	return s, err
}

// SavedQueries lists the named formulas stored in the database.
func (d *DB) SavedQueries() ([]SavedQuery, error) {
	rows, err := d.Query(`SELECT name, description, sql, builtin FROM saved_queries ORDER BY builtin DESC, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SavedQuery
	for rows.Next() {
		var q SavedQuery
		var b int
		if err := rows.Scan(&q.Name, &q.Description, &q.SQL, &b); err != nil {
			return nil, err
		}
		q.Builtin = b == 1
		out = append(out, q)
	}
	return out, rows.Err()
}

// Result is a generic query result, used by the query browser and by `networth
// query` so that arbitrary SQL - the whole point of using a real database - is a
// first-class feature rather than an escape hatch.
type Result struct {
	Columns []string
	Rows    [][]any
}

// RunQuery executes arbitrary SQL and returns the rows. Callers are expected to
// pass user-authored SQL; the file is the user's own data and any SQL client
// could do the same, so this adds no restriction beyond a read-only intent.
func (d *DB) RunQuery(q string, limit int) (*Result, error) {
	rows, err := d.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res := &Result{Columns: cols}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		res.Rows = append(res.Rows, vals)
		if limit > 0 && len(res.Rows) >= limit {
			break
		}
	}
	return res, rows.Err()
}

// NullString is a small helper for the many optional TEXT columns.
func NullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
