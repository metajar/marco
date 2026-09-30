package store

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	StatusUnknown = "unknown"
	StatusOnline  = "online"
	StatusOffline = "offline"
)

// Device is a network device (usually a phone) belonging to a person.
type Device struct {
	ID        int64
	PersonID  int64
	Name      string
	Kind      string
	IP        string
	MAC       string
	Ports     string // optional per-device TCP ports; empty uses the global list
	Monitored bool
	Alerts    bool
	CreatedAt time.Time

	// Monitoring state, maintained by the monitor.
	Status      string
	LastSeen    time.Time
	LastCheck   time.Time
	LastMethod  string
	Misses      int
	FirstMissAt time.Time
	AlertActive bool
	AlertedAt   time.Time
}

// DeviceKinds lists the icon choices offered in the UI.
var DeviceKinds = []struct{ Value, Label, Icon string }{
	{"phone", "Phone", "ti-device-mobile"},
	{"tablet", "Tablet", "ti-device-tablet"},
	{"laptop", "Laptop", "ti-device-laptop"},
	{"desktop", "Desktop", "ti-device-desktop"},
	{"watch", "Watch", "ti-device-watch"},
	{"console", "Game console", "ti-device-gamepad-2"},
	{"tv", "TV / streamer", "ti-device-tv"},
	{"speaker", "Speaker", "ti-device-speaker"},
	{"other", "Other", "ti-devices"},
}

func KindIcon(kind string) string {
	for _, k := range DeviceKinds {
		if k.Value == kind {
			return k.Icon
		}
	}
	return "ti-devices"
}

// PortList parses the device's port override.
func ParsePorts(s string) []int {
	var out []int
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if p, err := strconv.Atoi(f); err == nil && p > 0 && p < 65536 {
			out = append(out, p)
		}
	}
	return out
}

const deviceCols = `id, COALESCE(person_id, 0), name, kind, ip, mac, ports, monitored, alerts, created_at,
	status, last_seen, last_check, last_method, misses, first_miss_at, alert_active, alerted_at`

func scanDevice(sc interface{ Scan(...any) error }) (*Device, error) {
	var d Device
	var created, lastSeen, lastCheck, firstMiss, alerted int64
	err := sc.Scan(&d.ID, &d.PersonID, &d.Name, &d.Kind, &d.IP, &d.MAC, &d.Ports, &d.Monitored, &d.Alerts, &created,
		&d.Status, &lastSeen, &lastCheck, &d.LastMethod, &d.Misses, &firstMiss, &d.AlertActive, &alerted)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	d.CreatedAt, d.LastSeen, d.LastCheck = fromTS(created), fromTS(lastSeen), fromTS(lastCheck)
	d.FirstMissAt, d.AlertedAt = fromTS(firstMiss), fromTS(alerted)
	return &d, nil
}

func (s *Store) ListDevices() ([]*Device, error) {
	rows, err := s.db.Query(`SELECT ` + deviceCols + ` FROM devices ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetDevice(id int64) (*Device, error) {
	return scanDevice(s.db.QueryRow(`SELECT `+deviceCols+` FROM devices WHERE id = ?`, id))
}

// SaveDevice writes the user-editable fields of a device.
func (s *Store) SaveDevice(d *Device) error {
	if d.ID == 0 {
		d.CreatedAt = time.Now()
		if d.Status == "" {
			d.Status = StatusUnknown
		}
		res, err := s.db.Exec(`INSERT INTO devices (person_id, name, kind, ip, mac, ports, monitored, alerts, status, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			nullID(d.PersonID), d.Name, d.Kind, d.IP, d.MAC, d.Ports, b2i(d.Monitored), b2i(d.Alerts), d.Status, ts(d.CreatedAt))
		if err != nil {
			return err
		}
		d.ID, err = res.LastInsertId()
		return err
	}
	_, err := s.db.Exec(`UPDATE devices SET person_id = ?, name = ?, kind = ?, ip = ?, mac = ?, ports = ?, monitored = ?, alerts = ? WHERE id = ?`,
		nullID(d.PersonID), d.Name, d.Kind, d.IP, d.MAC, d.Ports, b2i(d.Monitored), b2i(d.Alerts), d.ID)
	return err
}

// SaveDeviceState writes the monitor-maintained fields of a device.
func (s *Store) SaveDeviceState(d *Device) error {
	_, err := s.db.Exec(`UPDATE devices SET status = ?, last_seen = ?, last_check = ?, last_method = ?, misses = ?,
		first_miss_at = ?, alert_active = ?, alerted_at = ? WHERE id = ?`,
		d.Status, ts(d.LastSeen), ts(d.LastCheck), d.LastMethod, d.Misses, ts(d.FirstMissAt), b2i(d.AlertActive), ts(d.AlertedAt), d.ID)
	return err
}

func (s *Store) SetDeviceAddress(id int64, ip, mac string) error {
	_, err := s.db.Exec(`UPDATE devices SET ip = ?, mac = ? WHERE id = ?`, ip, mac, id)
	return err
}

func (s *Store) DeleteDevice(id int64) error {
	_, err := s.db.Exec(`DELETE FROM devices WHERE id = ?`, id)
	return err
}
