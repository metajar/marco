package web

import (
	"time"

	"marco/internal/store"
)

// Chart data is rendered straight into templates; html/template JSON-encodes
// these structs inside <script> blocks.

type rangePoint struct {
	X    string   `json:"x"`
	Y    [2]int64 `json:"y"` // unix millis
	Link string   `json:"link,omitempty"`
}

type rangeSeries struct {
	Name string       `json:"name"`
	Data []rangePoint `json:"data"`
}

type barSeries struct {
	Name string    `json:"name"`
	Data []float64 `json:"data"`
}

type barChart struct {
	Categories []string    `json:"categories"`
	Series     []barSeries `json:"series"`
}

var stateSeriesNames = map[string]string{
	store.StatusOnline:  "On Wi-Fi",
	store.StatusOffline: "Off Wi-Fi",
	store.StatusUnknown: "Not tracked",
}

// ranges offered on chart pages.
var chartRanges = []struct {
	Key   string
	Label string
	Dur   time.Duration
}{
	{"24h", "24 hours", 24 * time.Hour},
	{"7d", "7 days", 7 * 24 * time.Hour},
	{"30d", "30 days", 30 * 24 * time.Hour},
}

func parseRange(key string) (string, time.Duration) {
	for _, r := range chartRanges {
		if r.Key == key {
			return r.Key, r.Dur
		}
	}
	return chartRanges[0].Key, chartRanges[0].Dur
}

type timelineRow struct {
	Label    string
	DeviceID int64
	Segments []store.Segment
}

// timeline turns per-device segments into an ApexCharts rangeBar dataset.
func timeline(rows []timelineRow) []rangeSeries {
	order := []string{store.StatusOnline, store.StatusOffline, store.StatusUnknown}
	byState := map[string]*rangeSeries{}
	var out []rangeSeries
	for _, st := range order {
		out = append(out, rangeSeries{Name: stateSeriesNames[st], Data: []rangePoint{}})
	}
	for i, st := range order {
		byState[st] = &out[i]
	}
	for _, row := range rows {
		for _, seg := range row.Segments {
			rs := byState[seg.State]
			if rs == nil {
				continue
			}
			rs.Data = append(rs.Data, rangePoint{X: row.Label, Y: [2]int64{seg.Start.UnixMilli(), seg.End.UnixMilli()}})
		}
	}
	return out
}

// presenceStats summarizes a device's presence over a period.
type presenceStats struct {
	OnlinePct          float64 // share of tracked time on the network, -1 if nothing tracked
	ScheduledOnlinePct float64 // same, restricted to the person's schedule windows
	OfflineScheduled   time.Duration
	Drops              int // transitions into offline
}

// minuteWalk steps through segments minute by minute, calling fn with the
// state and whether the person's schedule was active at that minute.
func minuteWalk(segs []store.Segment, p *store.Person, loc *time.Location, fn func(t time.Time, state string, scheduled bool)) {
	for _, seg := range segs {
		for t := seg.Start; t.Before(seg.End); t = t.Add(time.Minute) {
			lt := t.In(loc)
			fn(lt, seg.State, p != nil && p.ScheduleActive(lt))
		}
	}
}

func computeStats(segs []store.Segment, p *store.Person, loc *time.Location) presenceStats {
	var online, known, schedOnline, schedKnown, schedOffline int
	minuteWalk(segs, p, loc, func(_ time.Time, state string, scheduled bool) {
		if state == store.StatusUnknown {
			return
		}
		known++
		if state == store.StatusOnline {
			online++
		}
		if scheduled {
			schedKnown++
			if state == store.StatusOnline {
				schedOnline++
			} else {
				schedOffline++
			}
		}
	})
	st := presenceStats{OnlinePct: -1, ScheduledOnlinePct: -1, OfflineScheduled: time.Duration(schedOffline) * time.Minute}
	if known > 0 {
		st.OnlinePct = 100 * float64(online) / float64(known)
	}
	if schedKnown > 0 {
		st.ScheduledOnlinePct = 100 * float64(schedOnline) / float64(schedKnown)
	}
	for i, seg := range segs {
		if seg.State == store.StatusOffline && i > 0 { // skip a span carried in from before the range
			st.Drops++
		}
	}
	return st
}

func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// dailyBreakdown returns hours per day spent on/off the network, splitting
// offline time by whether it fell inside a schedule window.
func dailyBreakdown(segs []store.Segment, p *store.Person, start time.Time, days int, loc *time.Location) barChart {
	online := make([]float64, days)
	offSched := make([]float64, days)
	offOther := make([]float64, days)
	cats := make([]string, days)
	for i := range cats {
		cats[i] = start.AddDate(0, 0, i).Format("Mon Jan 2")
	}
	minuteWalk(segs, p, loc, func(t time.Time, state string, scheduled bool) {
		i := int(dayStart(t).Sub(start).Hours()/24 + 0.5)
		if i < 0 || i >= days {
			return
		}
		switch {
		case state == store.StatusOnline:
			online[i] += 1.0 / 60
		case state == store.StatusOffline && scheduled:
			offSched[i] += 1.0 / 60
		case state == store.StatusOffline:
			offOther[i] += 1.0 / 60
		}
	})
	round := func(v []float64) []float64 {
		for i := range v {
			v[i] = float64(int(v[i]*10+0.5)) / 10
		}
		return v
	}
	return barChart{Categories: cats, Series: []barSeries{
		{Name: "On Wi-Fi", Data: round(online)},
		{Name: "Off Wi-Fi during schedule", Data: round(offSched)},
		{Name: "Off Wi-Fi (unscheduled)", Data: round(offOther)},
	}}
}

// scheduleBands returns the person's schedule windows within [from, to] for
// shading chart backgrounds.
func scheduleBands(p *store.Person, from, to time.Time, loc *time.Location) [][2]int64 {
	if p == nil {
		return nil
	}
	var out [][2]int64
	var cur *[2]int64
	for t := from.In(loc).Truncate(time.Minute); t.Before(to); t = t.Add(time.Minute) {
		if p.ScheduleActive(t) {
			if cur == nil {
				out = append(out, [2]int64{t.UnixMilli(), t.UnixMilli()})
				cur = &out[len(out)-1]
			}
			cur[1] = t.Add(time.Minute).UnixMilli()
		} else {
			cur = nil
		}
	}
	return out
}
