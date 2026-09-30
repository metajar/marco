package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Person is someone being monitored (e.g. a kid) who owns devices.
type Person struct {
	ID          int64
	Name        string
	Color       string
	Notes       string
	PausedUntil time.Time
	CreatedAt   time.Time
	Schedules   []*Schedule

	// Texting the kid directly when their device drops off the Wi-Fi.
	Phone       string
	TextKid     bool
	KidTemplate string // empty uses Config.KidAlertTemplate
}

// CanText reports whether this person should get their own offline text.
func (p *Person) CanText() bool { return p.TextKid && strings.TrimSpace(p.Phone) != "" }

// Schedule is a recurring window during which a person's devices are expected
// to be on the network. Days is a bitmask where bit N is time.Weekday(N).
// A window whose end is before its start runs overnight into the next day.
type Schedule struct {
	ID       int64
	PersonID int64
	Name     string
	Days     int
	StartMin int
	EndMin   int
	Enabled  bool
}

func (p *Person) Paused(now time.Time) bool {
	return !p.PausedUntil.IsZero() && now.Before(p.PausedUntil)
}

func (p *Person) Initials() string {
	fields := strings.Fields(p.Name)
	switch len(fields) {
	case 0:
		return "?"
	case 1:
		r := []rune(fields[0])
		if len(r) > 1 {
			return strings.ToUpper(string(r[:2]))
		}
		return strings.ToUpper(string(r))
	default:
		return strings.ToUpper(string([]rune(fields[0])[:1]) + string([]rune(fields[len(fields)-1])[:1]))
	}
}

// ScheduleActive reports whether any enabled schedule covers t.
func (p *Person) ScheduleActive(t time.Time) bool {
	for _, s := range p.Schedules {
		if s.Active(t) {
			return true
		}
	}
	return false
}

// ActiveUntil returns the latest end among the schedules active at t.
func (p *Person) ActiveUntil(t time.Time) time.Time {
	var end time.Time
	for _, s := range p.Schedules {
		if s.Active(t) {
			if e := s.EndAfter(t); e.After(end) {
				end = e
			}
		}
	}
	return end
}

// NextStart returns the soonest upcoming schedule start after t.
func (p *Person) NextStart(t time.Time) time.Time {
	var next time.Time
	for _, s := range p.Schedules {
		if n := s.NextStart(t); !n.IsZero() && (next.IsZero() || n.Before(next)) {
			next = n
		}
	}
	return next
}

func (s *Schedule) HasDay(d time.Weekday) bool { return s.Days&(1<<uint(d)) != 0 }

func (s *Schedule) Overnight() bool { return s.EndMin <= s.StartMin }

func (s *Schedule) Active(t time.Time) bool {
	if !s.Enabled || s.Days == 0 {
		return false
	}
	m := t.Hour()*60 + t.Minute()
	today := t.Weekday()
	yesterday := (today + 6) % 7
	if s.StartMin == s.EndMin { // all day
		return s.HasDay(today)
	}
	if !s.Overnight() {
		return s.HasDay(today) && m >= s.StartMin && m < s.EndMin
	}
	return (s.HasDay(today) && m >= s.StartMin) || (s.HasDay(yesterday) && m < s.EndMin)
}

// EndAfter returns when the window containing t closes.
func (s *Schedule) EndAfter(t time.Time) time.Time {
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	m := t.Hour()*60 + t.Minute()
	switch {
	case s.StartMin == s.EndMin:
		return day.AddDate(0, 0, 1)
	case !s.Overnight() || m < s.EndMin:
		return day.Add(time.Duration(s.EndMin) * time.Minute)
	default:
		return day.AddDate(0, 0, 1).Add(time.Duration(s.EndMin) * time.Minute)
	}
}

func (s *Schedule) NextStart(t time.Time) time.Time {
	if !s.Enabled || s.Days == 0 {
		return time.Time{}
	}
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	for i := 0; i <= 7; i++ {
		d := day.AddDate(0, 0, i)
		if !s.HasDay(d.Weekday()) {
			continue
		}
		start := d.Add(time.Duration(s.StartMin) * time.Minute)
		if start.After(t) {
			return start
		}
	}
	return time.Time{}
}

func (s *Schedule) DaysLabel() string {
	switch s.Days {
	case 0x7f:
		return "Every day"
	case 0x3e:
		return "Weekdays"
	case 0x41:
		return "Weekends"
	case 0x1f:
		return "Sun–Thu"
	}
	names := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	var parts []string
	for i, n := range names {
		if s.Days&(1<<uint(i)) != 0 {
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, ", ")
}

func MinutesLabel(m int) string {
	h, mm := m/60, m%60
	suffix := "AM"
	if h >= 12 {
		suffix = "PM"
	}
	h12 := h % 12
	if h12 == 0 {
		h12 = 12
	}
	return fmt.Sprintf("%d:%02d %s", h12, mm, suffix)
}

func (s *Schedule) TimeLabel() string {
	if s.StartMin == s.EndMin {
		return "All day"
	}
	return MinutesLabel(s.StartMin) + " – " + MinutesLabel(s.EndMin)
}

func scanPerson(sc interface{ Scan(...any) error }) (*Person, error) {
	var p Person
	var paused, created int64
	if err := sc.Scan(&p.ID, &p.Name, &p.Color, &p.Notes, &paused, &created, &p.Phone, &p.TextKid, &p.KidTemplate); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.PausedUntil, p.CreatedAt = fromTS(paused), fromTS(created)
	return &p, nil
}

const personCols = `id, name, color, notes, paused_until, created_at, phone, text_kid, kid_template`

// ListPeople returns everyone with their schedules loaded.
func (s *Store) ListPeople() ([]*Person, error) {
	rows, err := s.db.Query(`SELECT ` + personCols + ` FROM people ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var out []*Person
	byID := map[int64]*Person{}
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
		byID[p.ID] = p
	}
	rows.Close()
	scheds, err := s.listSchedules(`SELECT id, person_id, name, days, start_min, end_min, enabled FROM schedules ORDER BY start_min`)
	if err != nil {
		return nil, err
	}
	for _, sc := range scheds {
		if p := byID[sc.PersonID]; p != nil {
			p.Schedules = append(p.Schedules, sc)
		}
	}
	return out, nil
}

func (s *Store) GetPerson(id int64) (*Person, error) {
	p, err := scanPerson(s.db.QueryRow(`SELECT `+personCols+` FROM people WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	p.Schedules, err = s.listSchedules(`SELECT id, person_id, name, days, start_min, end_min, enabled FROM schedules WHERE person_id = ? ORDER BY start_min`, id)
	return p, err
}

func (s *Store) SavePerson(p *Person) error {
	if p.ID == 0 {
		p.CreatedAt = time.Now()
		res, err := s.db.Exec(`INSERT INTO people (name, color, notes, paused_until, created_at, phone, text_kid, kid_template) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			p.Name, p.Color, p.Notes, ts(p.PausedUntil), ts(p.CreatedAt), p.Phone, b2i(p.TextKid), p.KidTemplate)
		if err != nil {
			return err
		}
		p.ID, err = res.LastInsertId()
		return err
	}
	_, err := s.db.Exec(`UPDATE people SET name = ?, color = ?, notes = ?, paused_until = ?, phone = ?, text_kid = ?, kid_template = ? WHERE id = ?`,
		p.Name, p.Color, p.Notes, ts(p.PausedUntil), p.Phone, b2i(p.TextKid), p.KidTemplate, p.ID)
	return err
}

func (s *Store) DeletePerson(id int64) error {
	_, err := s.db.Exec(`DELETE FROM people WHERE id = ?`, id)
	return err
}

func (s *Store) listSchedules(q string, args ...any) ([]*Schedule, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Schedule
	for rows.Next() {
		var sc Schedule
		if err := rows.Scan(&sc.ID, &sc.PersonID, &sc.Name, &sc.Days, &sc.StartMin, &sc.EndMin, &sc.Enabled); err != nil {
			return nil, err
		}
		out = append(out, &sc)
	}
	return out, rows.Err()
}

func (s *Store) GetSchedule(id int64) (*Schedule, error) {
	scheds, err := s.listSchedules(`SELECT id, person_id, name, days, start_min, end_min, enabled FROM schedules WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(scheds) == 0 {
		return nil, ErrNotFound
	}
	return scheds[0], nil
}

func (s *Store) SaveSchedule(sc *Schedule) error {
	if sc.ID == 0 {
		res, err := s.db.Exec(`INSERT INTO schedules (person_id, name, days, start_min, end_min, enabled) VALUES (?, ?, ?, ?, ?, ?)`,
			sc.PersonID, sc.Name, sc.Days, sc.StartMin, sc.EndMin, b2i(sc.Enabled))
		if err != nil {
			return err
		}
		sc.ID, err = res.LastInsertId()
		return err
	}
	_, err := s.db.Exec(`UPDATE schedules SET name = ?, days = ?, start_min = ?, end_min = ?, enabled = ? WHERE id = ?`,
		sc.Name, sc.Days, sc.StartMin, sc.EndMin, b2i(sc.Enabled), sc.ID)
	return err
}

func (s *Store) DeleteSchedule(id int64) error {
	_, err := s.db.Exec(`DELETE FROM schedules WHERE id = ?`, id)
	return err
}
