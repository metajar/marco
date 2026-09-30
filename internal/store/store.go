// Package store persists marco's configuration and activity in SQLite.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer at a time; a single connection avoids SQLITE_BUSY.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := s.addColumns(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// columnAdditions are columns added after the first release. CREATE TABLE IF
// NOT EXISTS won't add them to an existing database, so they're added here.
var columnAdditions = []struct{ table, column, def string }{
	{"people", "phone", "TEXT NOT NULL DEFAULT ''"},
	{"people", "text_kid", "INTEGER NOT NULL DEFAULT 0"},
	{"people", "kid_template", "TEXT NOT NULL DEFAULT ''"},
}

func (s *Store) addColumns() error {
	for _, c := range columnAdditions {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, c.table, c.column).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := s.db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, c.table, c.column, c.def)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS admins (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	username TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash TEXT NOT NULL,
	phone TEXT NOT NULL DEFAULT '',
	notify INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	token TEXT PRIMARY KEY,
	admin_id INTEGER NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
	expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS people (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	color TEXT NOT NULL DEFAULT 'blue',
	notes TEXT NOT NULL DEFAULT '',
	paused_until INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS schedules (
	id INTEGER PRIMARY KEY,
	person_id INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
	name TEXT NOT NULL DEFAULT '',
	days INTEGER NOT NULL,
	start_min INTEGER NOT NULL,
	end_min INTEGER NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS devices (
	id INTEGER PRIMARY KEY,
	person_id INTEGER REFERENCES people(id) ON DELETE SET NULL,
	name TEXT NOT NULL,
	kind TEXT NOT NULL DEFAULT 'phone',
	ip TEXT NOT NULL,
	mac TEXT NOT NULL DEFAULT '',
	ports TEXT NOT NULL DEFAULT '',
	monitored INTEGER NOT NULL DEFAULT 1,
	alerts INTEGER NOT NULL DEFAULT 1,
	status TEXT NOT NULL DEFAULT 'unknown',
	last_seen INTEGER NOT NULL DEFAULT 0,
	last_check INTEGER NOT NULL DEFAULT 0,
	last_method TEXT NOT NULL DEFAULT '',
	misses INTEGER NOT NULL DEFAULT 0,
	first_miss_at INTEGER NOT NULL DEFAULT 0,
	alert_active INTEGER NOT NULL DEFAULT 0,
	alerted_at INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS checks (
	id INTEGER PRIMARY KEY,
	device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	at INTEGER NOT NULL,
	online INTEGER NOT NULL,
	method TEXT NOT NULL DEFAULT '',
	latency_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS checks_device_at ON checks(device_id, at);
CREATE TABLE IF NOT EXISTS transitions (
	id INTEGER PRIMARY KEY,
	device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	at INTEGER NOT NULL,
	state TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS transitions_device_at ON transitions(device_id, at);
CREATE TABLE IF NOT EXISTS events (
	id INTEGER PRIMARY KEY,
	at INTEGER NOT NULL,
	device_id INTEGER REFERENCES devices(id) ON DELETE SET NULL,
	person_id INTEGER REFERENCES people(id) ON DELETE SET NULL,
	kind TEXT NOT NULL,
	message TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS events_at ON events(at);
CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

// ts converts a time to a unix timestamp, storing the zero time as 0.
func ts(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func fromTS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(v, 0)
}

// nullID stores 0 as NULL for optional foreign keys.
func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
