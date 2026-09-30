package web

import (
	"html/template"
	"strconv"
	"time"

	"marco/internal/notify"
	"marco/internal/probe"
	"marco/internal/store"
)

var personColors = []string{"blue", "azure", "indigo", "purple", "pink", "red", "orange", "yellow", "lime", "green", "teal", "cyan"}

var weekdays = []struct {
	Bit   int
	Short string
}{{0, "Sun"}, {1, "Mon"}, {2, "Tue"}, {3, "Wed"}, {4, "Thu"}, {5, "Fri"}, {6, "Sat"}}

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"kindIcon": store.KindIcon,
		"kinds":    func() any { return store.DeviceKinds },
		"colors":   func() []string { return personColors },
		"weekdays": func() any { return weekdays },
		"hasBit":   func(mask, bit int) bool { return mask&(1<<uint(bit)) != 0 },
		"statusColor": func(status string) string {
			switch status {
			case store.StatusOnline:
				return "green"
			case store.StatusOffline:
				return "red"
			}
			return "secondary"
		},
		"statusLabel": func(status string) string {
			switch status {
			case store.StatusOnline:
				return "On Wi-Fi"
			case store.StatusOffline:
				return "Off Wi-Fi"
			}
			return "Unknown"
		},
		"eventIcon": func(kind string) string {
			switch kind {
			case store.EventOnline:
				return "ti-wifi"
			case store.EventOffline:
				return "ti-wifi-off"
			case store.EventAlert:
				return "ti-message-exclamation"
			case store.EventRecovered:
				return "ti-message-check"
			case store.EventKidText:
				return "ti-message-user"
			case store.EventNotifyError:
				return "ti-alert-triangle"
			case store.EventIPChanged:
				return "ti-arrows-exchange"
			case store.EventLogin:
				return "ti-login"
			case store.EventConfig:
				return "ti-settings"
			}
			return "ti-info-circle"
		},
		"eventColor": func(kind string) string {
			switch kind {
			case store.EventOnline, store.EventRecovered:
				return "green"
			case store.EventOffline:
				return "red"
			case store.EventAlert:
				return "orange"
			case store.EventKidText:
				return "purple"
			case store.EventNotifyError:
				return "yellow"
			case store.EventIPChanged:
				return "azure"
			}
			return "secondary"
		},
		"ago": func(t time.Time) string {
			if t.IsZero() {
				return "never"
			}
			d := time.Since(t)
			if d < 10*time.Second {
				return "just now"
			}
			if d < 0 {
				return "in " + notify.HumanDuration(-d)
			}
			return notify.HumanDuration(d) + " ago"
		},
		"until": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return notify.HumanDuration(time.Until(t))
		},
		"dur": notify.HumanDuration,
		"clock": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.In(s.location()).Format("3:04 PM")
		},
		"when": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			t = t.In(s.location())
			now := s.now()
			switch {
			case sameDay(t, now):
				return "Today " + t.Format("3:04 PM")
			case sameDay(t, now.AddDate(0, 0, -1)):
				return "Yesterday " + t.Format("3:04 PM")
			case sameDay(t, now.AddDate(0, 0, 1)):
				return "Tomorrow " + t.Format("3:04 PM")
			case now.Sub(t) < 6*24*time.Hour && t.Before(now):
				return t.Format("Mon 3:04 PM")
			}
			return t.Format("Jan 2, 3:04 PM")
		},
		"stamp": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.In(s.location()).Format("Mon Jan 2 2006, 3:04:05 PM MST")
		},
		"hhmm": func(m int) string {
			return time.Date(2000, 1, 1, m/60, m%60, 0, 0, time.UTC).Format("15:04")
		},
		"privateMAC": probe.IsPrivateMAC,
		"kidDefault": func() string {
			cfg, _ := s.st.Config()
			return cfg.KidAlertTemplate
		},
		"pct": formatPct,
		"add": func(a, b int) int { return a + b },
		"mulSeconds": func(sec, n int) time.Duration {
			return time.Duration(sec*n) * time.Second
		},
		"dict": func(kv ...any) map[string]any {
			m := make(map[string]any, len(kv)/2)
			for i := 0; i+1 < len(kv); i += 2 {
				k, _ := kv[i].(string)
				m[k] = kv[i+1]
			}
			return m
		},
		"navItem": func(active, key, href, icon, label string) map[string]any {
			return map[string]any{"On": active == key, "Href": href, "Icon": icon, "Label": label}
		},
	}
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func formatPct(v float64) string {
	if v < 0 {
		return "—"
	}
	if v >= 99.95 {
		return "100%"
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + "%"
}
