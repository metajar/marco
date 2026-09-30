// Package monitor runs the periodic presence checks and raises alerts.
package monitor

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"marco/internal/notify"
	"marco/internal/probe"
	"marco/internal/store"
)

type Monitor struct {
	st *store.Store
	n  *notify.Notifier

	// probe is swappable so tests can simulate devices.
	probe func(ctx context.Context, ip string, opt probe.Options) probe.Result
	// findIP locates a MAC's current address.
	findIP func(mac string) string

	kick chan struct{}

	mu        sync.Mutex
	lastRound time.Time
	nextRound time.Time
	running   bool
	lastPrune time.Time
}

// Status describes the monitor loop for display in the UI.
type Status struct {
	LastRound time.Time
	NextRound time.Time
	Running   bool
}

func New(st *store.Store, n *notify.Notifier) *Monitor {
	return &Monitor{st: st, n: n, kick: make(chan struct{}, 1), probe: probe.Probe, findIP: probe.FindIPByMAC}
}

func (m *Monitor) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Status{LastRound: m.lastRound, NextRound: m.nextRound, Running: m.running}
}

// CheckNow asks the loop to start a round immediately.
func (m *Monitor) CheckNow() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

func (m *Monitor) Run(ctx context.Context) {
	m.startup()
	for {
		cfg, err := m.st.Config()
		if err != nil {
			log.Printf("monitor: load config: %v", err)
		}
		m.round(ctx, cfg)
		interval := time.Duration(cfg.CheckInterval) * time.Second
		m.mu.Lock()
		m.nextRound = time.Now().Add(interval)
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-m.kick:
		case <-time.After(interval):
		}
	}
}

// startup marks every device unknown: whatever happened while marco was down
// wasn't observed, and the charts should say so rather than guess.
func (m *Monitor) startup() {
	devices, err := m.st.ListDevices()
	if err != nil {
		log.Printf("monitor: %v", err)
		return
	}
	now := time.Now()
	for _, d := range devices {
		if d.Status != store.StatusUnknown {
			m.setState(d, store.StatusUnknown, now)
		}
		d.Misses, d.FirstMissAt = 0, time.Time{}
		m.st.SaveDeviceState(d)
	}
	m.st.AddEvent(store.EventSystem, 0, 0, "Marco started monitoring")
}

func (m *Monitor) round(ctx context.Context, cfg store.Config) {
	m.mu.Lock()
	m.running = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.running = false
		m.lastRound = time.Now()
		m.mu.Unlock()
	}()

	devices, err := m.st.ListDevices()
	if err != nil {
		log.Printf("monitor: list devices: %v", err)
		return
	}
	people, err := m.st.ListPeople()
	if err != nil {
		log.Printf("monitor: list people: %v", err)
		return
	}
	byID := map[int64]*store.Person{}
	for _, p := range people {
		byID[p.ID] = p
	}

	now := time.Now().In(cfg.Location())
	sem := make(chan struct{}, 32)
	var wg sync.WaitGroup
	for _, d := range devices {
		p := byID[d.PersonID]
		active := p != nil && p.ScheduleActive(now)
		if !d.Monitored || (!cfg.TrackAlways && !active) {
			if d.Status != store.StatusUnknown {
				m.setState(d, store.StatusUnknown, now)
				d.Misses, d.FirstMissAt, d.AlertActive = 0, time.Time{}, false
				m.st.SaveDeviceState(d)
			}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(d *store.Device) {
			defer wg.Done()
			defer func() { <-sem }()
			m.check(ctx, cfg, d, p, active)
		}(d)
	}
	wg.Wait()

	if time.Since(m.lastPrune) > time.Hour {
		m.lastPrune = time.Now()
		if err := m.st.Prune(time.Duration(cfg.RetentionDays) * 24 * time.Hour); err != nil {
			log.Printf("monitor: prune: %v", err)
		}
	}
}

func (m *Monitor) setState(d *store.Device, state string, at time.Time) {
	d.Status = state
	if err := m.st.AddTransition(d.ID, at, state); err != nil {
		log.Printf("monitor: transition: %v", err)
	}
}

func (m *Monitor) check(ctx context.Context, cfg store.Config, d *store.Device, p *store.Person, active bool) {
	ports := store.ParsePorts(d.Ports)
	if len(ports) == 0 {
		ports = store.ParsePorts(cfg.Ports)
	}
	opt := probe.Options{Ports: ports, Timeout: time.Duration(cfg.ProbeTimeout) * time.Second, UseARP: cfg.UseARP}
	res := m.probe(ctx, d.IP, opt)
	if ctx.Err() != nil {
		return
	}

	// Follow the device if its DHCP lease moved it to another address.
	if !res.Online && d.MAC != "" && cfg.UseARP {
		if ip := m.findIP(d.MAC); ip != "" && ip != d.IP {
			if r := m.probe(ctx, ip, opt); r.Online {
				m.st.AddEvent(store.EventIPChanged, d.ID, d.PersonID, fmt.Sprintf("%s moved from %s to %s", d.Name, d.IP, ip))
				d.IP, res = ip, r
				m.st.SetDeviceAddress(d.ID, d.IP, d.MAC)
			}
		}
	}
	if res.MAC != "" && d.MAC == "" {
		d.MAC = res.MAC
		m.st.SetDeviceAddress(d.ID, d.IP, d.MAC)
	}

	now := time.Now().In(cfg.Location())
	m.st.AddCheck(d.ID, store.Check{At: now, Online: res.Online, Method: res.Method, LatencyMs: int(res.Latency.Milliseconds())})
	d.LastCheck = now
	personName := ""
	if p != nil {
		personName = p.Name
	}

	if res.Online {
		offlineSince := d.LastSeen
		if offlineSince.IsZero() { // never seen before this drop
			offlineSince = d.FirstMissAt
		}
		if d.Status != store.StatusOnline {
			if d.Status == store.StatusOffline {
				msg := d.Name + " is back on the network"
				if !offlineSince.IsZero() {
					msg += " after " + notify.HumanDuration(now.Sub(offlineSince))
				}
				m.st.AddEvent(store.EventOnline, d.ID, d.PersonID, msg)
			} else {
				m.st.AddEvent(store.EventOnline, d.ID, d.PersonID, d.Name+" detected on the network")
			}
			m.setState(d, store.StatusOnline, now)
		}
		if d.AlertActive {
			d.AlertActive = false
			dur := now.Sub(offlineSince)
			msg := notify.Message{Event: "recovered", Person: personName, Device: d.Name, IP: d.IP, Since: offlineSince}
			msg.Text = notify.Render(cfg.RecoverTemplate, cfg.Location(), msg, dur)
			summary := "not sent (recovery texts are off)"
			if cfg.NotifyRecovered {
				out := m.n.Send(ctx, cfg, msg)
				summary = out.Summary()
				m.logFailures(d, out)
			}
			m.st.AddEvent(store.EventRecovered, d.ID, d.PersonID, fmt.Sprintf("%s returned after %s — %s", d.Name, notify.HumanDuration(dur), summary))
		}
		d.Misses, d.FirstMissAt = 0, time.Time{}
		d.LastSeen, d.LastMethod = now, res.Method
	} else {
		d.Misses++
		if d.FirstMissAt.IsZero() {
			d.FirstMissAt = now
		}
		if d.Status != store.StatusOffline && d.Misses >= cfg.OfflineAfter {
			m.setState(d, store.StatusOffline, d.FirstMissAt)
			m.st.AddEvent(store.EventOffline, d.ID, d.PersonID,
				fmt.Sprintf("%s dropped off the network (no answer since %s)", d.Name, d.FirstMissAt.In(cfg.Location()).Format("3:04 PM")))
		}
		if d.Status == store.StatusOffline {
			m.maybeAlert(ctx, cfg, d, p, active, now)
		}
	}

	// Once the schedule window closes, re-arm so the next window alerts again.
	if d.AlertActive && !active {
		d.AlertActive = false
	}
	if err := m.st.SaveDeviceState(d); err != nil {
		log.Printf("monitor: save %s: %v", d.Name, err)
	}
}

func (m *Monitor) maybeAlert(ctx context.Context, cfg store.Config, d *store.Device, p *store.Person, active bool, now time.Time) {
	if !d.Alerts || p == nil || !active || p.Paused(now) {
		return
	}
	realert := cfg.RealertMinutes > 0 && now.Sub(d.AlertedAt) >= time.Duration(cfg.RealertMinutes)*time.Minute
	if d.AlertActive && !realert {
		return
	}
	since := d.LastSeen
	if since.IsZero() {
		since = d.FirstMissAt
	}
	schedName := "schedule"
	for _, s := range p.Schedules {
		if s.Active(now) && s.Name != "" {
			schedName = s.Name
			break
		}
	}
	msg := notify.Message{Event: "alert", Person: p.Name, Device: d.Name, IP: d.IP, Schedule: schedName, Since: since}
	firstAlert := !d.AlertActive
	msg.Text = notify.Render(cfg.AlertTemplate, cfg.Location(), msg, now.Sub(since))
	if !firstAlert {
		msg.Text = "Reminder: " + msg.Text
	}
	out := m.n.Send(ctx, cfg, msg)
	m.logFailures(d, out)
	d.AlertActive, d.AlertedAt = true, now
	m.st.AddEvent(store.EventAlert, d.ID, d.PersonID, fmt.Sprintf("Alert: “%s” — %s", msg.Text, out.Summary()))

	// The kid gets one text per drop; the reminders are for parents.
	if firstAlert {
		m.textKid(ctx, cfg, d, p, msg, now.Sub(since))
	}
}

// textKid sends the person their own "you went offline" message.
func (m *Monitor) textKid(ctx context.Context, cfg store.Config, d *store.Device, p *store.Person, msg notify.Message, offFor time.Duration) {
	if !p.CanText() {
		return
	}
	if cfg.TextbeltKey == "" {
		m.st.AddEvent(store.EventNotifyError, d.ID, p.ID, "Couldn't text "+p.Name+": no Textbelt key is set")
		return
	}
	tmpl := p.KidTemplate
	if tmpl == "" {
		tmpl = cfg.KidAlertTemplate
	}
	text := notify.Render(tmpl, cfg.Location(), msg, offFor)
	if err := m.n.SMS(ctx, cfg, p.Phone, text); err != nil {
		m.st.AddEvent(store.EventNotifyError, d.ID, p.ID, fmt.Sprintf("Text to %s failed: %v", p.Name, err))
		return
	}
	m.st.AddEvent(store.EventKidText, d.ID, p.ID, fmt.Sprintf("Texted %s: “%s”", p.Name, text))
}

func (m *Monitor) logFailures(d *store.Device, out notify.Outcome) {
	for _, err := range out.Errors {
		m.st.AddEvent(store.EventNotifyError, d.ID, d.PersonID, err.Error())
	}
}
