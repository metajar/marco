package web

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"marco/internal/notify"
	"marco/internal/store"
)

func (s *Server) peopleList(w http.ResponseWriter, r *http.Request) {
	cards, _, _, err := s.family()
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, r, "people", page{Title: "Family", Pretitle: "People", Active: "people", D: cards})
}

func (s *Server) personNew(w http.ResponseWriter, r *http.Request) {
	people, _ := s.st.ListPeople()
	p := &store.Person{Color: personColors[len(people)%len(personColors)]}
	s.render(w, r, "person_form", page{Title: "Add a family member", Pretitle: "People", Active: "people", D: p})
}

func readPerson(r *http.Request, p *store.Person) string {
	p.Name = strings.TrimSpace(r.FormValue("name"))
	p.Notes = strings.TrimSpace(r.FormValue("notes"))
	p.Phone = strings.TrimSpace(r.FormValue("phone"))
	p.TextKid = formBool(r, "text_kid")
	p.KidTemplate = strings.TrimSpace(r.FormValue("kid_template"))
	if c := r.FormValue("color"); slices.Contains(personColors, c) {
		p.Color = c
	}
	if p.Name == "" {
		return "Please enter a name."
	}
	if p.TextKid && p.Phone == "" {
		return "Add " + p.Name + "'s mobile number to text them, or turn that option off."
	}
	return ""
}

func (s *Server) personCreate(w http.ResponseWriter, r *http.Request) {
	p := &store.Person{Color: "blue"}
	if msg := readPerson(r, p); msg != "" {
		s.render(w, r, "person_form", page{Title: "Add a family member", Active: "people", Flash: &flash{"danger", msg}, D: p})
		return
	}
	if err := s.st.SavePerson(p); err != nil {
		s.fail(w, err)
		return
	}
	// Starter schedule so the person is useful right away; easy to edit.
	s.st.SaveSchedule(&store.Schedule{PersonID: p.ID, Name: "School nights", Days: 0x1f, StartMin: 21 * 60, EndMin: 7 * 60, Enabled: true})
	s.audit(r, 0, p.ID, "added %s", p.Name)
	s.redirect(w, r, fmt.Sprintf("/people/%d", p.ID), "success",
		p.Name+" added with a starter \"School nights\" schedule. Adjust it below, then add their phone.")
}

func (s *Server) personShow(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetPerson(pathID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	devices, err := s.st.ListDevices()
	if err != nil {
		s.fail(w, err)
		return
	}
	var mine []*store.Device
	for _, d := range devices {
		if d.PersonID == p.ID {
			mine = append(mine, d)
		}
	}
	now := s.now()
	from := now.Add(-7 * 24 * time.Hour)
	var rows []timelineRow
	for _, d := range mine {
		segs, _ := s.st.Segments(d.ID, from, now)
		rows = append(rows, timelineRow{Label: d.Name, DeviceID: d.ID, Segments: segs})
	}
	events, _ := s.st.ListEvents(store.EventFilter{PersonID: p.ID, Limit: 10})
	cfg, _ := s.st.Config()
	s.render(w, r, "person", page{Title: p.Name, Pretitle: "Family member", Active: "people", D: map[string]any{
		"Person":      p,
		"Devices":     mine,
		"Active":      p.ScheduleActive(now),
		"ActiveUntil": p.ActiveUntil(now),
		"NextStart":   p.NextStart(now),
		"Paused":      p.Paused(now),
		"Timeline":    timeline(rows),
		"Bands":       scheduleBands(p, from, now, s.location()),
		"From":        from.UnixMilli(),
		"To":          now.UnixMilli(),
		"Events":      events,
		"KidPreview":  kidPreview(cfg, p),
	}})
}

func (s *Server) personUpdate(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetPerson(pathID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	if msg := readPerson(r, p); msg != "" {
		s.redirect(w, r, fmt.Sprintf("/people/%d", p.ID), "danger", msg)
		return
	}
	if err := s.st.SavePerson(p); err != nil {
		s.fail(w, err)
		return
	}
	s.redirect(w, r, fmt.Sprintf("/people/%d", p.ID), "success", "Saved.")
}

func (s *Server) personDelete(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetPerson(pathID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.st.DeletePerson(p.ID); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, 0, 0, "removed %s (their devices were kept but unassigned)", p.Name)
	s.redirect(w, r, "/people", "success", p.Name+" removed.")
}

func (s *Server) personPause(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetPerson(pathID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	minutes := formInt(r, "minutes", 0)
	now := s.now()
	var msg string
	switch {
	case minutes <= 0:
		p.PausedUntil = time.Time{}
		msg = "Alerts resumed for " + p.Name + "."
		s.audit(r, 0, p.ID, "resumed alerts for %s", p.Name)
	case minutes == 1: // until the end of the current (or next) schedule window
		until := p.ActiveUntil(now)
		if until.IsZero() {
			if next := p.NextStart(now); !next.IsZero() {
				until = p.ActiveUntil(next)
			}
		}
		if until.IsZero() {
			until = now.Add(12 * time.Hour)
		}
		p.PausedUntil = until
		msg = fmt.Sprintf("Alerts for %s paused until %s.", p.Name, until.Format("Mon 3:04 PM"))
		s.audit(r, 0, p.ID, "paused alerts for %s until %s", p.Name, until.Format("Mon 3:04 PM"))
	default:
		p.PausedUntil = now.Add(time.Duration(minutes) * time.Minute)
		msg = fmt.Sprintf("Alerts for %s paused until %s.", p.Name, p.PausedUntil.Format("Mon 3:04 PM"))
		s.audit(r, 0, p.ID, "paused alerts for %s until %s", p.Name, p.PausedUntil.Format("Mon 3:04 PM"))
	}
	if err := s.st.SavePerson(p); err != nil {
		s.fail(w, err)
		return
	}
	back := r.FormValue("back")
	if back == "" || back[0] != '/' {
		back = fmt.Sprintf("/people/%d", p.ID)
	}
	s.redirect(w, r, back, "success", msg)
}

// personTestText sends the kid's offline message to their phone so parents
// can see exactly what it looks like.
func (s *Server) personTestText(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetPerson(pathID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	back := fmt.Sprintf("/people/%d#texting", p.ID)
	cfg, _ := s.st.Config()
	switch {
	case p.Phone == "":
		s.redirect(w, r, back, "warning", "Add "+p.Name+"'s mobile number first.")
		return
	case cfg.TextbeltKey == "":
		s.redirect(w, r, "/settings#alerts", "warning", "Add your Textbelt key first.")
		return
	}
	text := "(Test) " + kidPreview(cfg, p)
	if err := s.n.SMS(r.Context(), cfg, p.Phone, text); err != nil {
		s.st.AddEvent(store.EventNotifyError, 0, p.ID, fmt.Sprintf("Test text to %s failed: %v", p.Name, err))
		s.redirect(w, r, back, "danger", "The text didn't go through: "+err.Error())
		return
	}
	s.st.AddEvent(store.EventKidText, 0, p.ID, fmt.Sprintf("%s sent %s a test text: “%s”", currentAdmin(r).Name, p.Name, text))
	s.redirect(w, r, back, "success", "Test text sent to "+p.Name+".")
}

// kidPreview renders the person's offline message with sample values.
func kidPreview(cfg store.Config, p *store.Person) string {
	tmpl := p.KidTemplate
	if tmpl == "" {
		tmpl = cfg.KidAlertTemplate
	}
	since := time.Now().Add(-5 * time.Minute)
	msg := notify.Message{Person: p.Name, Device: "phone", IP: "192.168.1.42", Schedule: "School nights", Since: since}
	return notify.Render(tmpl, cfg.Location(), msg, 5*time.Minute)
}

func parseClock(v string) (int, bool) {
	t, err := time.Parse("15:04", strings.TrimSpace(v))
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

func (s *Server) scheduleCreate(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetPerson(pathID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	r.ParseForm()
	sc := &store.Schedule{PersonID: p.ID, Name: strings.TrimSpace(r.FormValue("name")), Enabled: true}
	for _, v := range r.Form["days"] {
		var bit int
		if _, err := fmt.Sscan(v, &bit); err == nil && bit >= 0 && bit < 7 {
			sc.Days |= 1 << uint(bit)
		}
	}
	back := fmt.Sprintf("/people/%d#schedules", p.ID)
	start, ok1 := parseClock(r.FormValue("start"))
	end, ok2 := parseClock(r.FormValue("end"))
	switch {
	case sc.Days == 0:
		s.redirect(w, r, back, "danger", "Pick at least one day for the schedule.")
		return
	case !ok1 || !ok2:
		s.redirect(w, r, back, "danger", "Please enter a start and end time.")
		return
	}
	sc.StartMin, sc.EndMin = start, end
	if sc.Name == "" {
		sc.Name = "Schedule"
	}
	if err := s.st.SaveSchedule(sc); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, 0, p.ID, "added schedule %q for %s (%s, %s)", sc.Name, p.Name, sc.DaysLabel(), sc.TimeLabel())
	s.redirect(w, r, back, "success", "Schedule added.")
}

func (s *Server) scheduleToggle(w http.ResponseWriter, r *http.Request) {
	sc, err := s.st.GetSchedule(pathID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	sc.Enabled = !sc.Enabled
	if err := s.st.SaveSchedule(sc); err != nil {
		s.fail(w, err)
		return
	}
	state := "disabled"
	if sc.Enabled {
		state = "enabled"
	}
	s.audit(r, 0, sc.PersonID, "%s schedule %q", state, sc.Name)
	s.redirect(w, r, fmt.Sprintf("/people/%d#schedules", sc.PersonID), "success", "Schedule "+state+".")
}

func (s *Server) scheduleDelete(w http.ResponseWriter, r *http.Request) {
	sc, err := s.st.GetSchedule(pathID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.st.DeleteSchedule(sc.ID); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, 0, sc.PersonID, "deleted schedule %q", sc.Name)
	s.redirect(w, r, fmt.Sprintf("/people/%d#schedules", sc.PersonID), "success", "Schedule deleted.")
}
