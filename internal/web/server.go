// Package web serves the Tabler-based administration UI.
package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"marco/internal/monitor"
	"marco/internal/notify"
	"marco/internal/store"
)

//go:embed templates static
var assets embed.FS

const sessionCookie = "marco_session"

type Server struct {
	st  *store.Store
	mon *monitor.Monitor
	n   *notify.Notifier

	pages map[string]*template.Template

	mu  sync.RWMutex
	loc *time.Location
}

func New(st *store.Store, mon *monitor.Monitor, n *notify.Notifier) (*Server, error) {
	s := &Server{st: st, mon: mon, n: n}
	s.refreshLocation()
	if err := s.parseTemplates(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) refreshLocation() {
	cfg, _ := s.st.Config()
	s.mu.Lock()
	s.loc = cfg.Location()
	s.mu.Unlock()
}

func (s *Server) location() *time.Location {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loc
}

func (s *Server) now() time.Time { return time.Now().In(s.location()) }

func (s *Server) parseTemplates() error {
	s.pages = map[string]*template.Template{}
	files, err := fs.Glob(assets, "templates/pages/*.html")
	if err != nil {
		return err
	}
	for _, f := range files {
		name := strings.TrimSuffix(f[strings.LastIndex(f, "/")+1:], ".html")
		t, err := template.New(name).Funcs(s.funcs()).ParseFS(assets, "templates/layout.html", "templates/partials.html", f)
		if err != nil {
			return fmt.Errorf("template %s: %w", name, err)
		}
		s.pages[name] = t
	}
	return nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))

	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("GET /setup", s.setupPage)
	mux.HandleFunc("POST /setup", s.setup)

	auth := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireAuth(h)) }
	auth("POST /logout", s.logout)
	auth("GET /{$}", s.dashboard)
	auth("POST /check-now", s.checkNow)

	auth("GET /people", s.peopleList)
	auth("GET /people/new", s.personNew)
	auth("POST /people", s.personCreate)
	auth("GET /people/{id}", s.personShow)
	auth("POST /people/{id}", s.personUpdate)
	auth("POST /people/{id}/delete", s.personDelete)
	auth("POST /people/{id}/pause", s.personPause)
	auth("POST /people/{id}/test-text", s.personTestText)
	auth("POST /people/{id}/schedules", s.scheduleCreate)
	auth("POST /schedules/{id}/toggle", s.scheduleToggle)
	auth("POST /schedules/{id}/delete", s.scheduleDelete)

	auth("GET /devices", s.devicesList)
	auth("GET /devices/new", s.deviceNew)
	auth("POST /devices", s.deviceCreate)
	auth("GET /devices/{id}", s.deviceShow)
	auth("GET /devices/{id}/edit", s.deviceEdit)
	auth("POST /devices/{id}", s.deviceUpdate)
	auth("POST /devices/{id}/delete", s.deviceDelete)
	auth("GET /discover", s.discoverPage)
	auth("POST /discover", s.discoverScan)

	auth("GET /activity", s.activity)

	auth("GET /settings", s.settingsPage)
	auth("POST /settings", s.settingsSave)
	auth("POST /settings/test", s.settingsTest)

	auth("GET /parents", s.adminsList)
	auth("POST /parents", s.adminCreate)
	auth("GET /parents/{id}", s.adminEdit)
	auth("POST /parents/{id}", s.adminUpdate)
	auth("POST /parents/{id}/delete", s.adminDelete)

	return s.sameOrigin(mux)
}

// sameOrigin rejects cross-site form posts. Session cookies are also
// SameSite=Lax, so this is defense in depth.
func (s *Server) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					http.Error(w, "cross-origin request rejected", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

type ctxKey struct{}

func (s *Server) requireAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n, err := s.st.CountAdmins(); err == nil && n == 0 {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		admin, err := s.st.SessionAdmin(c.Value)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, admin)))
	})
}

func currentAdmin(r *http.Request) *store.Admin {
	a, _ := r.Context().Value(ctxKey{}).(*store.Admin)
	return a
}

// page is the data passed to every template.
type page struct {
	Title    string
	Pretitle string
	Active   string
	User     *store.Admin
	Flash    *flash
	Now      time.Time
	Monitor  monitor.Status
	D        any
}

type flash struct {
	Kind string // success, danger, warning, info
	Text string
}

func (s *Server) setFlash(w http.ResponseWriter, kind, text string) {
	http.SetCookie(w, &http.Cookie{Name: "marco_flash", Value: url.QueryEscape(kind + "|" + text), Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (s *Server) takeFlash(w http.ResponseWriter, r *http.Request) *flash {
	c, err := r.Cookie("marco_flash")
	if err != nil {
		return nil
	}
	http.SetCookie(w, &http.Cookie{Name: "marco_flash", Path: "/", MaxAge: -1})
	v, _ := url.QueryUnescape(c.Value)
	kind, text, ok := strings.Cut(v, "|")
	if !ok {
		return nil
	}
	return &flash{Kind: kind, Text: text}
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, p page) {
	t, ok := s.pages[name]
	if !ok {
		http.Error(w, "unknown page "+name, http.StatusInternalServerError)
		return
	}
	p.User = currentAdmin(r)
	p.Now = s.now()
	p.Monitor = s.mon.Status()
	if p.Flash == nil {
		p.Flash = s.takeFlash(w, r)
	}
	root := "layout"
	if name == "login" || name == "setup" {
		root = "auth"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, root, p); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func (s *Server) redirect(w http.ResponseWriter, r *http.Request, to, kind, msg string) {
	if msg != "" {
		s.setFlash(w, kind, msg)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	log.Printf("error: %v", err)
	http.Error(w, "something went wrong: "+err.Error(), http.StatusInternalServerError)
}

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

func formInt(r *http.Request, key string, def int) int {
	v, err := strconv.Atoi(strings.TrimSpace(r.FormValue(key)))
	if err != nil {
		return def
	}
	return v
}

func formBool(r *http.Request, key string) bool {
	v := r.FormValue(key)
	return v == "1" || v == "on" || v == "true"
}

// audit records an admin action in the activity log.
func (s *Server) audit(r *http.Request, deviceID, personID int64, format string, args ...any) {
	who := "Someone"
	if a := currentAdmin(r); a != nil {
		who = a.Name
	}
	s.st.AddEvent(store.EventConfig, deviceID, personID, who+" "+fmt.Sprintf(format, args...))
}
