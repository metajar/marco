package web

import (
	"net/http"
	"strings"
	"time"

	"marco/internal/store"
)

const sessionTTL = 30 * 24 * time.Hour

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, a *store.Admin) error {
	token, err := s.st.CreateSession(a.ID, sessionTTL)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: int(sessionTTL.Seconds()),
	})
	return nil
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if n, _ := s.st.CountAdmins(); n == 0 {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	s.render(w, r, "login", page{Title: "Sign in"})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAdminByUsername(r.FormValue("username"))
	if err != nil || !a.CheckPassword(r.FormValue("password")) {
		time.Sleep(500 * time.Millisecond)
		s.render(w, r, "login", page{Title: "Sign in", Flash: &flash{"danger", "That username and password didn't match."},
			D: map[string]string{"Username": r.FormValue("username")}})
		return
	}
	if err := s.startSession(w, r, a); err != nil {
		s.fail(w, err)
		return
	}
	s.st.AddEvent(store.EventLogin, 0, 0, a.Name+" signed in from "+clientIP(r))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.st.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) setupPage(w http.ResponseWriter, r *http.Request) {
	if n, _ := s.st.CountAdmins(); n > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, r, "setup", page{Title: "Welcome to Marco", D: map[string]string{}})
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	if n, _ := s.st.CountAdmins(); n > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	a := &store.Admin{
		Name:     strings.TrimSpace(r.FormValue("name")),
		Username: strings.TrimSpace(r.FormValue("username")),
		Phone:    strings.TrimSpace(r.FormValue("phone")),
		Notify:   true,
	}
	form := map[string]string{"Name": a.Name, "Username": a.Username, "Phone": a.Phone}
	if msg := validateAdmin(a, r.FormValue("password"), true); msg != "" {
		s.render(w, r, "setup", page{Title: "Welcome to Marco", Flash: &flash{"danger", msg}, D: form})
		return
	}
	a.PasswordHash, _ = store.HashPassword(r.FormValue("password"))
	if err := s.st.SaveAdmin(a); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.startSession(w, r, a); err != nil {
		s.fail(w, err)
		return
	}
	s.st.AddEvent(store.EventConfig, 0, 0, a.Name+" set up Marco")
	s.redirect(w, r, "/people/new", "success", "You're all set! Start by adding a family member, then their devices.")
}

func validateAdmin(a *store.Admin, password string, requirePassword bool) string {
	switch {
	case a.Name == "":
		return "Please enter a name."
	case a.Username == "" || strings.ContainsAny(a.Username, " \t"):
		return "Please choose a username without spaces."
	case requirePassword && len(password) < 8:
		return "Passwords need at least 8 characters."
	case !requirePassword && password != "" && len(password) < 8:
		return "Passwords need at least 8 characters."
	}
	return ""
}

func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}
