package store

import (
	"strings"
	"time"
)

// Event kinds shown in the activity log.
const (
	EventOnline      = "online"
	EventOffline     = "offline"
	EventAlert       = "alert"
	EventRecovered   = "recovered"
	EventKidText     = "kid_text"
	EventNotifyError = "notify_error"
	EventIPChanged   = "ip_changed"
	EventLogin       = "login"
	EventConfig      = "config"
	EventSystem      = "system"
)

var EventKinds = []struct{ Value, Label string }{
	{EventOffline, "Went offline"},
	{EventOnline, "Came online"},
	{EventAlert, "Alert sent"},
	{EventRecovered, "Recovered"},
	{EventKidText, "Texted kid"},
	{EventNotifyError, "Notification failed"},
	{EventIPChanged, "IP changed"},
	{EventLogin, "Sign-in"},
	{EventConfig, "Configuration"},
	{EventSystem, "System"},
}

type Event struct {
	ID         int64
	At         time.Time
	DeviceID   int64
	PersonID   int64
	Kind       string
	Message    string
	DeviceName string
	DeviceKind string
	PersonName string
}

type EventFilter struct {
	DeviceID int64
	PersonID int64
	Kind     string
	From, To time.Time
	Limit    int
	Offset   int
}

func (s *Store) AddEvent(kind string, deviceID, personID int64, message string) error {
	_, err := s.db.Exec(`INSERT INTO events (at, device_id, person_id, kind, message) VALUES (?, ?, ?, ?, ?)`,
		time.Now().Unix(), nullID(deviceID), nullID(personID), kind, message)
	return err
}

func (f EventFilter) where() (string, []any) {
	var conds []string
	var args []any
	if f.DeviceID != 0 {
		conds = append(conds, "e.device_id = ?")
		args = append(args, f.DeviceID)
	}
	if f.PersonID != 0 {
		conds = append(conds, "e.person_id = ?")
		args = append(args, f.PersonID)
	}
	if f.Kind != "" {
		conds = append(conds, "e.kind = ?")
		args = append(args, f.Kind)
	}
	if !f.From.IsZero() {
		conds = append(conds, "e.at >= ?")
		args = append(args, f.From.Unix())
	}
	if !f.To.IsZero() {
		conds = append(conds, "e.at < ?")
		args = append(args, f.To.Unix())
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func (s *Store) ListEvents(f EventFilter) ([]*Event, error) {
	where, args := f.where()
	if f.Limit <= 0 {
		f.Limit = 50
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.Query(`SELECT e.id, e.at, COALESCE(e.device_id, 0), COALESCE(e.person_id, 0), e.kind, e.message,
			COALESCE(d.name, ''), COALESCE(d.kind, ''), COALESCE(p.name, '')
		FROM events e
		LEFT JOIN devices d ON d.id = e.device_id
		LEFT JOIN people p ON p.id = e.person_id`+where+`
		ORDER BY e.at DESC, e.id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		var e Event
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.DeviceID, &e.PersonID, &e.Kind, &e.Message, &e.DeviceName, &e.DeviceKind, &e.PersonName); err != nil {
			return nil, err
		}
		e.At = fromTS(at)
		out = append(out, &e)
	}
	return out, rows.Err()
}

func (s *Store) CountEvents(f EventFilter) (int, error) {
	where, args := f.where()
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM events e`+where, args...).Scan(&n)
	return n, err
}

// Check is the raw result of a single probe.
type Check struct {
	At        time.Time
	Online    bool
	Method    string
	LatencyMs int
}

func (s *Store) AddCheck(deviceID int64, c Check) error {
	_, err := s.db.Exec(`INSERT INTO checks (device_id, at, online, method, latency_ms) VALUES (?, ?, ?, ?, ?)`,
		deviceID, c.At.Unix(), b2i(c.Online), c.Method, c.LatencyMs)
	return err
}

func (s *Store) RecentChecks(deviceID int64, limit int) ([]Check, error) {
	rows, err := s.db.Query(`SELECT at, online, method, latency_ms FROM checks WHERE device_id = ? ORDER BY at DESC, id DESC LIMIT ?`, deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Check
	for rows.Next() {
		var c Check
		var at int64
		if err := rows.Scan(&at, &c.Online, &c.Method, &c.LatencyMs); err != nil {
			return nil, err
		}
		c.At = fromTS(at)
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddTransition records a device's presence state changing at a point in time.
func (s *Store) AddTransition(deviceID int64, at time.Time, state string) error {
	_, err := s.db.Exec(`INSERT INTO transitions (device_id, at, state) VALUES (?, ?, ?)`, deviceID, at.Unix(), state)
	return err
}

// Segment is a contiguous span of time a device spent in one state.
type Segment struct {
	State      string
	Start, End time.Time
}

// Segments returns the device's presence history between from and to.
func (s *Store) Segments(deviceID int64, from, to time.Time) ([]Segment, error) {
	state := StatusUnknown
	var prev string
	err := s.db.QueryRow(`SELECT state FROM transitions WHERE device_id = ? AND at <= ? ORDER BY at DESC, id DESC LIMIT 1`,
		deviceID, from.Unix()).Scan(&prev)
	if err == nil {
		state = prev
	}
	rows, err := s.db.Query(`SELECT at, state FROM transitions WHERE device_id = ? AND at > ? AND at < ? ORDER BY at, id`,
		deviceID, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Segment
	cur := from
	add := func(st string, a, b time.Time) {
		if !b.After(a) {
			return
		}
		if n := len(out); n > 0 && out[n-1].State == st {
			out[n-1].End = b
			return
		}
		out = append(out, Segment{State: st, Start: a, End: b})
	}
	for rows.Next() {
		var at int64
		var st string
		if err := rows.Scan(&at, &st); err != nil {
			return nil, err
		}
		t := fromTS(at)
		add(state, cur, t)
		cur, state = t, st
	}
	add(state, cur, to)
	return out, rows.Err()
}

// Prune deletes history older than the retention period.
func (s *Store) Prune(retention time.Duration) error {
	cutoff := time.Now().Add(-retention).Unix()
	for _, q := range []string{
		`DELETE FROM checks WHERE at < ?`,
		`DELETE FROM events WHERE at < ?`,
		`DELETE FROM transitions WHERE at < ?`,
	} {
		if _, err := s.db.Exec(q, cutoff); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
	return err
}
