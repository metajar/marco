package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"marco/internal/notify"
	"marco/internal/probe"
	"marco/internal/store"
)

type harness struct {
	t      *testing.T
	st     *store.Store
	m      *Monitor
	cfg    store.Config
	device *store.Device
	person *store.Person

	mu     sync.Mutex
	online bool
	texts  []string
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t}
	st, err := store.Open(filepath.Join(t.TempDir(), "marco.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h.st = st

	textbelt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		h.mu.Lock()
		h.texts = append(h.texts, r.FormValue("phone")+": "+r.FormValue("message"))
		h.mu.Unlock()
		w.Write([]byte(`{"success":true,"quotaRemaining":10}`))
	}))
	t.Cleanup(textbelt.Close)

	st.SaveAdmin(&store.Admin{Name: "Mom", Username: "mom", PasswordHash: "x", Phone: "+15550001111", Notify: true})
	st.SaveAdmin(&store.Admin{Name: "Dad", Username: "dad", PasswordHash: "x", Phone: "", Notify: true}) // no phone: skipped

	h.person = &store.Person{Name: "Emma", Color: "pink"}
	st.SavePerson(h.person)
	st.SaveSchedule(&store.Schedule{PersonID: h.person.ID, Name: "Always", Days: 0x7f, Enabled: true})

	h.device = &store.Device{PersonID: h.person.ID, Name: "iPhone", Kind: "phone", IP: "192.0.2.10", Monitored: true, Alerts: true}
	st.SaveDevice(h.device)

	h.cfg = store.DefaultConfig()
	h.cfg.OfflineAfter = 2
	h.cfg.RealertMinutes = 0
	h.cfg.TextbeltKey = "test"
	h.cfg.TextbeltURL = textbelt.URL

	h.m = New(st, notify.New(st))
	h.m.probe = func(ctx context.Context, ip string, opt probe.Options) probe.Result {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.online {
			return probe.Result{Online: true, Method: "tcp/62078", MAC: "da:a1:19:00:00:01"}
		}
		return probe.Result{}
	}
	h.m.findIP = func(string) string { return "" }
	return h
}

func (h *harness) round(online bool) *store.Device {
	h.mu.Lock()
	h.online = online
	h.mu.Unlock()
	h.m.round(context.Background(), h.cfg)
	d, err := h.st.GetDevice(h.device.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	return d
}

func (h *harness) textCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.texts)
}

func TestAlertLifecycle(t *testing.T) {
	h := newHarness(t)

	d := h.round(true)
	if d.Status != store.StatusOnline || d.MAC != "da:a1:19:00:00:01" {
		t.Fatalf("after first check: status=%s mac=%q", d.Status, d.MAC)
	}

	d = h.round(false)
	if d.Status != store.StatusOnline || d.Misses != 1 {
		t.Fatalf("one miss should not flip status: %s misses=%d", d.Status, d.Misses)
	}
	if h.textCount() != 0 {
		t.Fatal("no text expected after a single miss")
	}

	d = h.round(false)
	if d.Status != store.StatusOffline || !d.AlertActive {
		t.Fatalf("second miss should mark offline and alert: status=%s alert=%v", d.Status, d.AlertActive)
	}
	if h.textCount() != 1 {
		t.Fatalf("expected exactly one text (Dad has no phone), got %v", h.texts)
	}
	if !strings.Contains(h.texts[0], "+15550001111") || !strings.Contains(h.texts[0], "Emma's iPhone") {
		t.Errorf("unexpected alert text: %s", h.texts[0])
	}

	h.round(false)
	if h.textCount() != 1 {
		t.Fatalf("re-alerts are off; expected no new text, got %v", h.texts)
	}

	d = h.round(true)
	if d.Status != store.StatusOnline || d.AlertActive || d.Misses != 0 {
		t.Fatalf("recovery: status=%s alert=%v misses=%d", d.Status, d.AlertActive, d.Misses)
	}
	if h.textCount() != 2 || !strings.Contains(h.texts[1], "Polo!") {
		t.Fatalf("expected a recovery text, got %v", h.texts)
	}

	events, _ := h.st.ListEvents(store.EventFilter{DeviceID: h.device.ID, Limit: 50})
	kinds := map[string]int{}
	for _, e := range events {
		kinds[e.Kind]++
	}
	for _, k := range []string{store.EventOnline, store.EventOffline, store.EventAlert, store.EventRecovered} {
		if kinds[k] == 0 {
			t.Errorf("missing %s event; got %v", k, kinds)
		}
	}

}

func TestPausedPersonGetsNoAlert(t *testing.T) {
	h := newHarness(t)
	h.person.PausedUntil = time.Now().Add(time.Hour)
	h.st.SavePerson(h.person)

	h.round(true)
	h.round(false)
	d := h.round(false)
	if d.Status != store.StatusOffline {
		t.Fatalf("status = %s, want offline", d.Status)
	}
	if d.AlertActive || h.textCount() != 0 {
		t.Fatalf("paused person should not trigger alerts (texts: %v)", h.texts)
	}
}

func TestNoAlertOutsideSchedule(t *testing.T) {
	h := newHarness(t)
	p, _ := h.st.GetPerson(h.person.ID)
	for _, s := range p.Schedules {
		s.Enabled = false
		h.st.SaveSchedule(s)
	}
	h.round(true)
	h.round(false)
	d := h.round(false)
	if d.Status != store.StatusOffline {
		t.Fatalf("history is still tracked outside schedules; status = %s", d.Status)
	}
	if h.textCount() != 0 {
		t.Fatalf("no texts expected outside a schedule, got %v", h.texts)
	}
}

func TestRealert(t *testing.T) {
	h := newHarness(t)
	h.cfg.RealertMinutes = 1
	h.round(true)
	h.round(false)
	h.round(false) // alert
	d, _ := h.st.GetDevice(h.device.ID)
	d.AlertedAt = time.Now().Add(-2 * time.Minute) // pretend the alert was a while ago
	h.st.SaveDeviceState(d)
	h.round(false)
	if h.textCount() != 2 || !strings.Contains(h.texts[1], "Reminder") {
		t.Fatalf("expected a reminder text, got %v", h.texts)
	}
}

func (h *harness) textsTo(phone string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, t := range h.texts {
		if strings.HasPrefix(t, phone+": ") {
			out = append(out, strings.TrimPrefix(t, phone+": "))
		}
	}
	return out
}

func TestKidGetsOneTextPerDrop(t *testing.T) {
	h := newHarness(t)
	h.cfg.RealertMinutes = 1
	h.person.Phone = "+15557770000"
	h.person.TextKid = true
	h.st.SavePerson(h.person)

	h.round(true)
	h.round(false)
	h.round(false) // offline → parents alerted, kid texted
	kid := h.textsTo("+15557770000")
	if len(kid) != 1 || !strings.Contains(kid[0], "Hey Emma") || !strings.Contains(kid[0], "iPhone") {
		t.Fatalf("expected the default kid text, got %v", kid)
	}

	d, _ := h.st.GetDevice(h.device.ID)
	d.AlertedAt = time.Now().Add(-2 * time.Minute)
	h.st.SaveDeviceState(d)
	h.round(false) // parent reminder only
	if kid := h.textsTo("+15557770000"); len(kid) != 1 {
		t.Fatalf("reminders should not re-text the kid, got %v", kid)
	}
	if parent := h.textsTo("+15550001111"); len(parent) != 2 {
		t.Fatalf("parent should have alert + reminder, got %v", parent)
	}

	// Back online, then a second drop texts the kid again — with a custom message.
	h.round(true)
	h.person.KidTemplate = "{person}, phone back on Wi-Fi. Now."
	h.st.SavePerson(h.person)
	h.round(false)
	h.round(false)
	kid = h.textsTo("+15557770000")
	if len(kid) != 2 || kid[1] != "Emma, phone back on Wi-Fi. Now." {
		t.Fatalf("expected custom text on second drop, got %v", kid)
	}
}

func TestKidNotTextedWhenDisabled(t *testing.T) {
	h := newHarness(t)
	h.person.Phone = "+15557770000" // number saved but texting switched off
	h.st.SavePerson(h.person)
	h.round(true)
	h.round(false)
	h.round(false)
	if kid := h.textsTo("+15557770000"); len(kid) != 0 {
		t.Fatalf("texting is off, got %v", kid)
	}
}

func TestRecoveryDurationForNeverSeenDevice(t *testing.T) {
	h := newHarness(t)
	h.round(false)
	h.round(false) // offline + alert without ever having been seen
	h.round(true)
	parent := h.textsTo("+15550001111")
	if len(parent) != 2 || !regexp.MustCompile(`after \d+s\.$`).MatchString(parent[1]) {
		t.Fatalf("recovery text should use the first miss as the start, got %v", parent)
	}
}
