package web

import (
	"net/http"
	"net/url"
	"strconv"

	"marco/internal/store"
)

const eventsPerPage = 50

func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.EventFilter{Kind: q.Get("kind"), Limit: eventsPerPage}
	f.DeviceID, _ = strconv.ParseInt(q.Get("device"), 10, 64)
	f.PersonID, _ = strconv.ParseInt(q.Get("person"), 10, 64)
	pageNum, _ := strconv.Atoi(q.Get("page"))
	if pageNum < 1 {
		pageNum = 1
	}
	f.Offset = (pageNum - 1) * eventsPerPage

	rangeKey, dur := parseRange(q.Get("range"))
	now := s.now()
	from := now.Add(-dur)

	events, err := s.st.ListEvents(f)
	if err != nil {
		s.fail(w, err)
		return
	}
	total, _ := s.st.CountEvents(f)

	cards, unassigned, devices, err := s.family()
	if err != nil {
		s.fail(w, err)
		return
	}

	// Presence charts follow the same person/device filter as the log.
	loc := s.location()
	days := int(dur.Hours() / 24)
	if days < 1 {
		days = 1
	}
	dayFrom := dayStart(now).AddDate(0, 0, -(days - 1))
	var rows []timelineRow
	offline := barChart{}
	for i := 0; i < days; i++ {
		offline.Categories = append(offline.Categories, dayFrom.AddDate(0, 0, i).Format("Mon Jan 2"))
	}
	include := func(p *store.Person, d *store.Device) {
		if !d.Monitored || (f.DeviceID != 0 && d.ID != f.DeviceID) || (f.PersonID != 0 && (p == nil || p.ID != f.PersonID)) {
			return
		}
		label := d.Name
		if p != nil {
			label = p.Name + " · " + d.Name
		}
		segs, _ := s.st.Segments(d.ID, from, now)
		rows = append(rows, timelineRow{Label: label, DeviceID: d.ID, Segments: segs})
		daySegs, _ := s.st.Segments(d.ID, dayFrom, now)
		daily := dailyBreakdown(daySegs, p, dayFrom, days, loc)
		series := barSeries{Name: label, Data: make([]float64, days)}
		for i := range series.Data {
			series.Data[i] = daily.Series[1].Data[i] + daily.Series[2].Data[i]
		}
		offline.Series = append(offline.Series, series)
	}
	for _, c := range cards {
		for _, d := range c.Devices {
			include(c.Person, d)
		}
	}
	for _, d := range unassigned {
		include(nil, d)
	}

	var people []*store.Person
	for _, c := range cards {
		people = append(people, c.Person)
	}

	pageURL := func(n int) string {
		v := url.Values{}
		for k, vs := range q {
			v[k] = vs
		}
		v.Set("page", strconv.Itoa(n))
		return "/activity?" + v.Encode()
	}
	rangeURL := func(key string) string {
		v := url.Values{}
		for k, vs := range q {
			v[k] = vs
		}
		v.Set("range", key)
		v.Del("page")
		return "/activity?" + v.Encode()
	}
	var ranges []map[string]any
	for _, rg := range chartRanges {
		ranges = append(ranges, map[string]any{"Key": rg.Key, "Label": rg.Label, "URL": rangeURL(rg.Key)})
	}

	pages := (total + eventsPerPage - 1) / eventsPerPage
	d := map[string]any{
		"Events":   events,
		"Total":    total,
		"Page":     pageNum,
		"Pages":    pages,
		"Filter":   f,
		"People":   people,
		"Devices":  devices,
		"Kinds":    store.EventKinds,
		"Range":    rangeKey,
		"Ranges":   ranges,
		"Timeline": timeline(rows),
		"RowCount": len(rows),
		"Offline":  offline,
		"From":     from.UnixMilli(),
		"To":       now.UnixMilli(),
		"Filtered": f.DeviceID != 0 || f.PersonID != 0 || f.Kind != "",
	}
	if pageNum > 1 {
		d["PrevURL"] = pageURL(pageNum - 1)
	}
	if pageNum < pages {
		d["NextURL"] = pageURL(pageNum + 1)
	}
	s.render(w, r, "activity", page{Title: "Activity", Pretitle: "History", Active: "activity", D: d})
}
