package web

import (
	"net/http"
	"time"

	"marco/internal/store"
)

type personCard struct {
	Person      *store.Person
	Devices     []*store.Device
	Active      bool
	ActiveUntil time.Time
	NextStart   time.Time
	Paused      bool
}

// family groups devices under their owners.
func (s *Server) family() ([]*personCard, []*store.Device, []*store.Device, error) {
	people, err := s.st.ListPeople()
	if err != nil {
		return nil, nil, nil, err
	}
	devices, err := s.st.ListDevices()
	if err != nil {
		return nil, nil, nil, err
	}
	now := s.now()
	byID := map[int64]*personCard{}
	var cards []*personCard
	for _, p := range people {
		c := &personCard{
			Person:      p,
			Active:      p.ScheduleActive(now),
			ActiveUntil: p.ActiveUntil(now),
			NextStart:   p.NextStart(now),
			Paused:      p.Paused(now),
		}
		cards = append(cards, c)
		byID[p.ID] = c
	}
	var unassigned []*store.Device
	for _, d := range devices {
		if c := byID[d.PersonID]; c != nil {
			c.Devices = append(c.Devices, d)
		} else {
			unassigned = append(unassigned, d)
		}
	}
	return cards, unassigned, devices, nil
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	cards, unassigned, devices, err := s.family()
	if err != nil {
		s.fail(w, err)
		return
	}
	var stats struct{ Online, Offline, Unknown, Alerts, Watching int }
	for _, d := range devices {
		switch d.Status {
		case store.StatusOnline:
			stats.Online++
		case store.StatusOffline:
			stats.Offline++
		default:
			stats.Unknown++
		}
		if d.AlertActive {
			stats.Alerts++
		}
	}
	for _, c := range cards {
		if c.Active && !c.Paused {
			stats.Watching++
		}
	}

	now := s.now()
	from := now.Add(-24 * time.Hour)
	var rows []timelineRow
	for _, c := range cards {
		for _, d := range c.Devices {
			if !d.Monitored {
				continue
			}
			segs, err := s.st.Segments(d.ID, from, now)
			if err != nil {
				s.fail(w, err)
				return
			}
			rows = append(rows, timelineRow{Label: c.Person.Name + " · " + d.Name, DeviceID: d.ID, Segments: segs})
		}
	}
	events, err := s.st.ListEvents(store.EventFilter{Limit: 8})
	if err != nil {
		s.fail(w, err)
		return
	}
	cfg, _ := s.st.Config()

	s.render(w, r, "dashboard", page{Title: "Who's home?", Pretitle: "Dashboard", Active: "dashboard", D: map[string]any{
		"Cards":      cards,
		"Unassigned": unassigned,
		"Stats":      stats,
		"Events":     events,
		"Timeline":   timeline(rows),
		"RowCount":   len(rows),
		"From":       from.UnixMilli(),
		"To":         now.UnixMilli(),
		"NoTexts":    cfg.TextbeltKey == "" && cfg.WebhookURL == "",
		"Interval":   cfg.CheckInterval,
	}})
}

func (s *Server) checkNow(w http.ResponseWriter, r *http.Request) {
	s.mon.CheckNow()
	back := r.FormValue("back")
	if back == "" || back[0] != '/' {
		back = "/"
	}
	s.redirect(w, r, back, "info", "Checking every device now — results appear in a few seconds.")
}
