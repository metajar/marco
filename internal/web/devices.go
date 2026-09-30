package web

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"marco/internal/probe"
	"marco/internal/store"
)

func (s *Server) devicesList(w http.ResponseWriter, r *http.Request) {
	devices, err := s.st.ListDevices()
	if err != nil {
		s.fail(w, err)
		return
	}
	people, err := s.st.ListPeople()
	if err != nil {
		s.fail(w, err)
		return
	}
	names := map[int64]*store.Person{}
	for _, p := range people {
		names[p.ID] = p
	}
	s.render(w, r, "devices", page{Title: "Devices", Pretitle: "Network", Active: "devices", D: map[string]any{
		"Devices": devices, "People": names,
	}})
}

type deviceForm struct {
	Device *store.Device
	People []*store.Person
	IsNew  bool
}

func (s *Server) deviceNew(w http.ResponseWriter, r *http.Request) {
	people, _ := s.st.ListPeople()
	d := &store.Device{
		Kind: "phone", Monitored: true, Alerts: true,
		IP:   r.URL.Query().Get("ip"),
		MAC:  probe.NormalizeMAC(r.URL.Query().Get("mac")),
		Name: r.URL.Query().Get("name"),
	}
	fmt.Sscan(r.URL.Query().Get("person"), &d.PersonID)
	s.render(w, r, "device_form", page{Title: "Add a device", Pretitle: "Devices", Active: "devices",
		D: deviceForm{Device: d, People: people, IsNew: true}})
}

func readDevice(r *http.Request, d *store.Device) string {
	d.Name = strings.TrimSpace(r.FormValue("name"))
	d.Kind = r.FormValue("kind")
	d.IP = strings.TrimSpace(r.FormValue("ip"))
	d.Ports = strings.TrimSpace(r.FormValue("ports"))
	d.Monitored = formBool(r, "monitored")
	d.Alerts = formBool(r, "alerts")
	d.PersonID = int64(formInt(r, "person_id", 0))
	mac := strings.TrimSpace(r.FormValue("mac"))
	d.MAC = probe.NormalizeMAC(mac)
	switch {
	case d.Name == "":
		return "Please give the device a name, like \"Emma's iPhone\"."
	case net.ParseIP(d.IP) == nil || net.ParseIP(d.IP).To4() == nil:
		return "Please enter a valid IPv4 address, like 192.168.1.42."
	case mac != "" && d.MAC == "":
		return "That MAC address doesn't look right. It should look like aa:bb:cc:dd:ee:ff."
	}
	return ""
}

func (s *Server) deviceCreate(w http.ResponseWriter, r *http.Request) {
	d := &store.Device{}
	if msg := readDevice(r, d); msg != "" {
		people, _ := s.st.ListPeople()
		s.render(w, r, "device_form", page{Title: "Add a device", Active: "devices", Flash: &flash{"danger", msg},
			D: deviceForm{Device: d, People: people, IsNew: true}})
		return
	}
	if err := s.st.SaveDevice(d); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, d.ID, d.PersonID, "added device %s (%s)", d.Name, d.IP)
	s.mon.CheckNow()
	s.redirect(w, r, fmt.Sprintf("/devices/%d", d.ID), "success", d.Name+" added. Checking it now…")
}

func (s *Server) deviceEdit(w http.ResponseWriter, r *http.Request) {
	d, err := s.st.GetDevice(pathID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	people, _ := s.st.ListPeople()
	s.render(w, r, "device_form", page{Title: "Edit " + d.Name, Pretitle: "Devices", Active: "devices",
		D: deviceForm{Device: d, People: people}})
}

func (s *Server) deviceUpdate(w http.ResponseWriter, r *http.Request) {
	d, err := s.st.GetDevice(pathID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	if msg := readDevice(r, d); msg != "" {
		people, _ := s.st.ListPeople()
		s.render(w, r, "device_form", page{Title: "Edit " + d.Name, Active: "devices", Flash: &flash{"danger", msg},
			D: deviceForm{Device: d, People: people}})
		return
	}
	if err := s.st.SaveDevice(d); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, d.ID, d.PersonID, "updated device %s", d.Name)
	s.mon.CheckNow()
	s.redirect(w, r, fmt.Sprintf("/devices/%d", d.ID), "success", "Saved.")
}

func (s *Server) deviceDelete(w http.ResponseWriter, r *http.Request) {
	d, err := s.st.GetDevice(pathID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.st.DeleteDevice(d.ID); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, 0, d.PersonID, "removed device %s (%s)", d.Name, d.IP)
	s.redirect(w, r, "/devices", "success", d.Name+" removed.")
}

func (s *Server) deviceShow(w http.ResponseWriter, r *http.Request) {
	d, err := s.st.GetDevice(pathID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	var p *store.Person
	if d.PersonID != 0 {
		p, _ = s.st.GetPerson(d.PersonID)
	}
	rangeKey, dur := parseRange(r.URL.Query().Get("range"))
	loc := s.location()
	now := s.now()
	from := now.Add(-dur)
	segs, err := s.st.Segments(d.ID, from, now)
	if err != nil {
		s.fail(w, err)
		return
	}

	const days = 14
	dayFrom := dayStart(now).AddDate(0, 0, -(days - 1))
	daySegs, err := s.st.Segments(d.ID, dayFrom, now)
	if err != nil {
		s.fail(w, err)
		return
	}
	checks, _ := s.st.RecentChecks(d.ID, 25)
	events, _ := s.st.ListEvents(store.EventFilter{DeviceID: d.ID, Limit: 15})
	alerts, _ := s.st.CountEvents(store.EventFilter{DeviceID: d.ID, Kind: store.EventAlert, From: from})

	label := d.Name
	s.render(w, r, "device", page{Title: d.Name, Pretitle: "Device", Active: "devices", D: map[string]any{
		"Device":   d,
		"Person":   p,
		"Range":    rangeKey,
		"Ranges":   chartRanges,
		"Stats":    computeStats(segs, p, loc),
		"Alerts":   alerts,
		"Timeline": timeline([]timelineRow{{Label: label, Segments: segs}}),
		"Bands":    scheduleBands(p, from, now, loc),
		"From":     from.UnixMilli(),
		"To":       now.UnixMilli(),
		"Daily":    dailyBreakdown(daySegs, p, dayFrom, days, loc),
		"Checks":   checks,
		"Events":   events,
		"Private":  probe.IsPrivateMAC(d.MAC),
		"Since":    time.Since(d.LastSeen),
	}})
}

func (s *Server) discoverPage(w http.ResponseWriter, r *http.Request) {
	var subnets []string
	for _, n := range probe.LocalSubnets() {
		subnets = append(subnets, n.String())
	}
	s.render(w, r, "discover", page{Title: "Find devices", Pretitle: "Network", Active: "discover", D: map[string]any{
		"Subnets": subnets,
	}})
}

type foundRow struct {
	probe.Found
	Known *store.Device
}

func (s *Server) discoverScan(w http.ResponseWriter, r *http.Request) {
	found := probe.Scan(r.Context())
	devices, _ := s.st.ListDevices()
	var rows []foundRow
	for _, f := range found {
		row := foundRow{Found: f}
		for _, d := range devices {
			if (d.MAC != "" && d.MAC == f.MAC) || d.IP == f.IP {
				row.Known = d
				break
			}
		}
		rows = append(rows, row)
	}
	var subnets []string
	for _, n := range probe.LocalSubnets() {
		subnets = append(subnets, n.String())
	}
	s.render(w, r, "discover", page{Title: "Find devices", Pretitle: "Network", Active: "discover", D: map[string]any{
		"Subnets": subnets,
		"Scanned": true,
		"Rows":    rows,
	}})
}
