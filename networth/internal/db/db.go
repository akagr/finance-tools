// Package db opens and maintains the SQLite file that is the single source of
// truth: financial data, user settings and application state all live in it, so
// the binary can be pointed at any file - including someone else's - and work
// with no configuration on the side.
//
// Two rules follow from that and are enforced here:
//
//   - Schema changes are forward-only numbered migrations recorded in the file
//     itself, and a database written by a newer version than this binary
//     understands is refused rather than damaged.
//   - The view layer is code, not data, so it is dropped and recreated from
//     embedded SQL on every open. A database therefore always carries the
//     current formula layer, visible to sqlite3 and any other client.
package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/akagr/finance-tools/networth/internal/money"
	_ "modernc.org/sqlite"
)

//go:embed sql/*.sql
var sqlFS embed.FS

// AppVersion is the version of this binary's schema understanding. It is written
// into every database it touches.
const AppVersion = "0.1.0"

// SchemaVersion is the migration this binary expects after opening.
const SchemaVersion = 1

// DB wraps a handle to the net worth database.
type DB struct {
	*sql.DB
	Path string
}

// ErrNotOurs is returned when the file exists but was not written by networth,
// so that pointing at the wrong path never corrupts an unrelated database.
var ErrNotOurs = errors.New("this file is not a networth database")

// Open opens the database at path, creating and initialising it if it does not
// exist, then applies any pending migrations and reinstalls the view layer. The
// returned flag reports whether the file was created by this call, which lets
// the interface offer a first-run walkthrough instead of an empty screen.
//
// Open-or-create is deliberate: with the database as the only source of truth
// there is nothing else to set up, so pointing the binary at a path should just
// work - whether that path is a fresh file or one handed over by someone else.
func Open(path string) (*DB, bool, error) {
	created := false
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return nil, false, err
		}
		created = true
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, false, err
			}
		}
	}
	d, err := open(path, created)
	if err != nil {
		return nil, false, err
	}
	return d, created, nil
}

func open(path string, create bool) (*DB, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// modernc's driver serialises access per connection; a single connection
	// keeps write ordering obvious and is ample for a single-user file.
	sqlDB.SetMaxOpenConns(1)
	d := &DB{DB: sqlDB, Path: path}
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, err
	}
	if !create {
		if err := d.checkIsOurs(); err != nil {
			d.Close()
			return nil, err
		}
	}
	if err := d.migrate(); err != nil {
		d.Close()
		return nil, err
	}
	if err := d.installViews(); err != nil {
		d.Close()
		return nil, fmt.Errorf("installing views: %w", err)
	}
	return d, nil
}

// checkIsOurs refuses files this binary should not write to: databases belonging
// to some other application, and databases written by a newer networth than
// this one. Both matter because the file is meant to travel between machines.
func (d *DB) checkIsOurs() error {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='app_meta'`).Scan(&n)
	if err != nil {
		return fmt.Errorf("reading %s: %w", d.Path, err)
	}
	if n == 0 {
		// An empty file is a database waiting to be initialised; a populated one
		// belongs to something else and must be left alone.
		if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master`).Scan(&n); err != nil {
			return fmt.Errorf("reading %s: %w", d.Path, err)
		}
		if n > 0 {
			return fmt.Errorf("%w (%s)", ErrNotOurs, d.Path)
		}
		return nil
	}
	var min string
	err = d.QueryRow(`SELECT value FROM app_meta WHERE key='min_app_version'`).Scan(&min)
	if err == nil && min != "" && compareVersions(min, AppVersion) > 0 {
		return fmt.Errorf("%s was written by a newer networth: it needs %s or later, this is %s",
			d.Path, min, AppVersion)
	}
	return nil
}

// migration is one numbered, forward-only schema step.
type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := sqlFS.ReadDir("sql")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, e := range entries {
		name := e.Name()
		if name == "views.sql" || name == "queries.sql" || !strings.HasSuffix(name, ".sql") {
			continue
		}
		numStr, rest, ok := strings.Cut(strings.TrimSuffix(name, ".sql"), "_")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(numStr)
		if err != nil {
			continue
		}
		body, err := sqlFS.ReadFile("sql/" + name)
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{version: n, name: rest, sql: string(body)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	return ms, nil
}

func (d *DB) migrate() error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	applied := map[int]bool{}
	rows, err := d.Query(`SELECT version FROM schema_migrations`)
	if err == nil {
		for rows.Next() {
			var v int
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return err
			}
			applied[v] = true
		}
		rows.Close()
	}
	for _, m := range ms {
		if applied[m.version] {
			continue
		}
		if err := d.applyMigration(m); err != nil {
			return fmt.Errorf("migration %04d_%s: %w", m.version, m.name, err)
		}
	}
	return d.writeMeta()
}

func (d *DB) applyMigration(m migration) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(m.sql); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.version, m.name, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
		return err
	}
	return tx.Commit()
}

// writeMeta records everything needed to interpret the file without this code:
// the money scale, the schema version, and the oldest binary that can safely
// open it.
func (d *DB) writeMeta() error {
	meta := map[string]string{
		"scale":           strconv.Itoa(money.Scale),
		"scale_places":    strconv.Itoa(money.Places),
		"app_version":     AppVersion,
		"min_app_version": "0.1.0",
		"schema_version":  strconv.Itoa(SchemaVersion),
		"format":          "networth",
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range meta {
		if _, err := tx.Exec(`INSERT INTO app_meta (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// installViews recreates the whole derived layer from the embedded SQL. It runs
// on every open so that improvements to a formula reach existing databases, and
// so that a file received from someone else is immediately queryable with the
// current definitions in any SQL client.
func (d *DB) installViews() error {
	body, err := sqlFS.ReadFile("sql/views.sql")
	if err != nil {
		return err
	}
	if _, err := d.Exec(string(body)); err != nil {
		return err
	}
	return d.installSavedQueries()
}

// installSavedQueries refreshes the built-in named queries, leaving any the user
// wrote themselves untouched.
func (d *DB) installSavedQueries() error {
	body, err := sqlFS.ReadFile("sql/queries.sql")
	if err != nil {
		return nil // optional
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM saved_queries WHERE builtin = 1`); err != nil {
		return err
	}
	for _, q := range parseQueries(string(body)) {
		if _, err := tx.Exec(`INSERT INTO saved_queries (name, sql, description, builtin)
			VALUES (?, ?, ?, 1)
			ON CONFLICT(name) DO UPDATE SET sql = excluded.sql, description = excluded.description`,
			q.Name, q.SQL, q.Description); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SavedQuery is a named formula stored in the database.
type SavedQuery struct {
	Name        string
	Description string
	SQL         string
	Builtin     bool
}

// parseQueries reads the "-- name: x" / "-- desc: y" block format of queries.sql.
func parseQueries(body string) []SavedQuery {
	var out []SavedQuery
	var cur *SavedQuery
	var buf []string
	flush := func() {
		if cur != nil {
			cur.SQL = strings.TrimSpace(strings.Join(buf, "\n"))
			if cur.SQL != "" {
				out = append(out, *cur)
			}
		}
		cur, buf = nil, nil
	}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(trimmed, "-- name:"); ok {
			flush()
			cur = &SavedQuery{Name: strings.TrimSpace(rest), Builtin: true}
			continue
		}
		if rest, ok := strings.CutPrefix(trimmed, "-- desc:"); ok && cur != nil {
			cur.Description = strings.TrimSpace(rest)
			continue
		}
		if cur != nil {
			buf = append(buf, line)
		}
	}
	flush()
	return out
}

func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}
