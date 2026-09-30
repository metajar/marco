package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"marco/internal/notify"
	"marco/internal/store"
)

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.st.Config()
	if err != nil {
		s.fail(w, err)
		return
	}
	recipients, _ := s.st.AlertRecipients()
	s.render(w, r, "settings", page{Title: "Settings", Pretitle: "Configuration", Active: "settings", D: map[string]any{
		"Config":     cfg,
		"Recipients": recipients,
		"ServerTZ":   time.Local.String(),
		"Preview":    previewMessages(cfg),
	}})
}

func previewMessages(cfg store.Config) []string {
	loc := cfg.Location()
	since := time.Now().Add(-18 * time.Minute)
	msg := notify.Message{Person: "Emma", Device: "iPhone", IP: "192.168.1.42", Schedule: "School nights", Since: since}
	return []string{
		notify.Render(cfg.AlertTemplate, loc, msg, 18*time.Minute),
		notify.Render(cfg.RecoverTemplate, loc, msg, 18*time.Minute),
		notify.Render(cfg.KidAlertTemplate, loc, msg, 18*time.Minute),
	}
}

func (s *Server) settingsSave(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.st.Config()
	cfg.CheckInterval = formInt(r, "check_interval", cfg.CheckInterval)
	cfg.OfflineAfter = formInt(r, "offline_after", cfg.OfflineAfter)
	cfg.ProbeTimeout = formInt(r, "probe_timeout", cfg.ProbeTimeout)
	cfg.Ports = strings.TrimSpace(r.FormValue("ports"))
	cfg.UseARP = formBool(r, "use_arp")
	cfg.TrackAlways = formBool(r, "track_always")
	cfg.RealertMinutes = formInt(r, "realert_minutes", cfg.RealertMinutes)
	cfg.NotifyRecovered = formBool(r, "notify_recovered")
	cfg.TextbeltKey = strings.TrimSpace(r.FormValue("textbelt_key"))
	cfg.TextbeltURL = strings.TrimSpace(r.FormValue("textbelt_url"))
	cfg.WebhookURL = strings.TrimSpace(r.FormValue("webhook_url"))
	cfg.AlertTemplate = strings.TrimSpace(r.FormValue("alert_template"))
	cfg.RecoverTemplate = strings.TrimSpace(r.FormValue("recover_template"))
	cfg.KidAlertTemplate = strings.TrimSpace(r.FormValue("kid_alert_template"))
	cfg.Timezone = strings.TrimSpace(r.FormValue("timezone"))
	cfg.RetentionDays = formInt(r, "retention_days", cfg.RetentionDays)

	if cfg.Timezone != "" {
		if _, err := time.LoadLocation(cfg.Timezone); err != nil {
			s.redirect(w, r, "/settings", "danger", fmt.Sprintf("Unknown timezone %q — use a name like America/Chicago.", cfg.Timezone))
			return
		}
	}
	for _, u := range []string{cfg.WebhookURL, cfg.TextbeltURL} {
		if u == "" {
			continue
		}
		if pu, err := url.Parse(u); err != nil || (pu.Scheme != "http" && pu.Scheme != "https") {
			s.redirect(w, r, "/settings", "danger", fmt.Sprintf("%q isn't a valid http(s) URL.", u))
			return
		}
	}
	if err := s.st.SaveConfig(cfg); err != nil {
		s.fail(w, err)
		return
	}
	s.refreshLocation()
	s.audit(r, 0, 0, "updated settings")
	s.mon.CheckNow()
	s.redirect(w, r, "/settings", "success", "Settings saved.")
}

func (s *Server) settingsTest(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.st.Config()
	who := currentAdmin(r).Name
	msg := notify.Message{Event: "test", Text: "Marco test from " + who + ": alerts are working. Polo!"}
	var out notify.Outcome
	if phone := strings.TrimSpace(r.FormValue("phone")); phone != "" {
		if cfg.TextbeltKey == "" {
			s.redirect(w, r, "/settings#alerts", "warning", "Add a Textbelt key first.")
			return
		}
		if err := s.n.SMS(r.Context(), cfg, phone, msg.Text); err != nil {
			out.Errors = append(out.Errors, err)
		} else {
			out.Delivered = append(out.Delivered, phone)
		}
	} else {
		out = s.n.Send(r.Context(), cfg, msg)
	}
	if len(out.Errors) > 0 {
		var errs []string
		for _, e := range out.Errors {
			errs = append(errs, e.Error())
		}
		s.st.AddEvent(store.EventNotifyError, 0, 0, "Test notification: "+strings.Join(errs, "; "))
		s.redirect(w, r, "/settings#alerts", "danger", "Test had problems: "+strings.Join(errs, "; "))
		return
	}
	if len(out.Delivered) == 0 {
		s.redirect(w, r, "/settings#alerts", "warning", "Nothing to send to — add a Textbelt key or webhook URL first.")
		return
	}
	s.st.AddEvent(store.EventSystem, 0, 0, who+" sent a test notification ("+out.Summary()+")")
	s.redirect(w, r, "/settings#alerts", "success", "Test "+out.Summary()+".")
}

// Parent (admin) accounts.

func (s *Server) adminsList(w http.ResponseWriter, r *http.Request) {
	admins, err := s.st.ListAdmins()
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, r, "parents", page{Title: "Parents", Pretitle: "Who can sign in and get alerts", Active: "parents", D: admins})
}

func readAdmin(r *http.Request, a *store.Admin) {
	a.Name = strings.TrimSpace(r.FormValue("name"))
	a.Username = strings.TrimSpace(r.FormValue("username"))
	a.Phone = strings.TrimSpace(r.FormValue("phone"))
	a.Notify = formBool(r, "notify")
}

func (s *Server) adminCreate(w http.ResponseWriter, r *http.Request) {
	a := &store.Admin{}
	readAdmin(r, a)
	if msg := validateAdmin(a, r.FormValue("password"), true); msg != "" {
		s.redirect(w, r, "/parents", "danger", msg)
		return
	}
	if existing, _ := s.st.GetAdminByUsername(a.Username); existing != nil {
		s.redirect(w, r, "/parents", "danger", "That username is taken.")
		return
	}
	a.PasswordHash, _ = store.HashPassword(r.FormValue("password"))
	if err := s.st.SaveAdmin(a); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, 0, 0, "added parent account for %s", a.Name)
	s.redirect(w, r, "/parents", "success", a.Name+" can now sign in as \""+a.Username+"\".")
}

func (s *Server) adminEdit(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAdmin(pathID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, r, "parent_form", page{Title: "Edit " + a.Name, Pretitle: "Parents", Active: "parents", D: a})
}

func (s *Server) adminUpdate(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAdmin(pathID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	readAdmin(r, a)
	back := fmt.Sprintf("/parents/%d", a.ID)
	pw := r.FormValue("password")
	if msg := validateAdmin(a, pw, false); msg != "" {
		s.redirect(w, r, back, "danger", msg)
		return
	}
	if existing, _ := s.st.GetAdminByUsername(a.Username); existing != nil && existing.ID != a.ID {
		s.redirect(w, r, back, "danger", "That username is taken.")
		return
	}
	if pw != "" {
		a.PasswordHash, _ = store.HashPassword(pw)
	}
	if err := s.st.SaveAdmin(a); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, 0, 0, "updated parent account for %s", a.Name)
	s.redirect(w, r, "/parents", "success", "Saved.")
}

func (s *Server) adminDelete(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAdmin(pathID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	if a.ID == currentAdmin(r).ID {
		s.redirect(w, r, "/parents", "danger", "You can't remove your own account while signed in.")
		return
	}
	if err := s.st.DeleteAdmin(a.ID); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, 0, 0, "removed parent account for %s", a.Name)
	s.redirect(w, r, "/parents", "success", a.Name+" removed.")
}
