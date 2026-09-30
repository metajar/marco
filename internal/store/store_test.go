package store

import (
	"path/filepath"
	"testing"
	"time"
)

func at(day time.Weekday, hh, mm int) time.Time {
	// 2026-09-27 is a Sunday.
	return time.Date(2026, 9, 27+int(day), hh, mm, 0, 0, time.UTC)
}

func TestScheduleActive(t *testing.T) {
	schoolNights := &Schedule{Days: 0x1f, StartMin: 21 * 60, EndMin: 7 * 60, Enabled: true} // Sun–Thu 9pm–7am
	homework := &Schedule{Days: 0x3e, StartMin: 16 * 60, EndMin: 18 * 60, Enabled: true}    // Mon–Fri 4–6pm
	always := &Schedule{Days: 0x7f, StartMin: 0, EndMin: 0, Enabled: true}

	cases := []struct {
		name string
		s    *Schedule
		t    time.Time
		want bool
	}{
		{"sunday 9pm starts", schoolNights, at(time.Sunday, 21, 0), true},
		{"sunday 8:59pm not yet", schoolNights, at(time.Sunday, 20, 59), false},
		{"monday 6:59am carries over from sunday", schoolNights, at(time.Monday, 6, 59), true},
		{"monday 7am ends", schoolNights, at(time.Monday, 7, 0), false},
		{"friday 11pm not a school night", schoolNights, at(time.Friday, 23, 0), false},
		{"friday 3am carries over from thursday", schoolNights, at(time.Friday, 3, 0), true},
		{"saturday 3am no carry from friday", schoolNights, at(time.Saturday, 3, 0), false},
		{"sunday 3am no carry from saturday", schoolNights, at(time.Sunday, 3, 0), false},
		{"homework wed 5pm", homework, at(time.Wednesday, 17, 0), true},
		{"homework wed 6pm ends", homework, at(time.Wednesday, 18, 0), false},
		{"homework saturday", homework, at(time.Saturday, 17, 0), false},
		{"always", always, at(time.Saturday, 12, 34), true},
	}
	for _, c := range cases {
		if got := c.s.Active(c.t); got != c.want {
			t.Errorf("%s: Active(%s) = %v, want %v", c.name, c.t.Format("Mon 15:04"), got, c.want)
		}
	}

	off := *schoolNights
	off.Enabled = false
	if off.Active(at(time.Sunday, 22, 0)) {
		t.Error("disabled schedule should never be active")
	}
}

func TestScheduleEndAndNext(t *testing.T) {
	s := &Schedule{Days: 0x1f, StartMin: 21 * 60, EndMin: 7 * 60, Enabled: true}
	if got, want := s.EndAfter(at(time.Sunday, 22, 0)), at(time.Monday, 7, 0); !got.Equal(want) {
		t.Errorf("EndAfter evening = %v, want %v", got, want)
	}
	if got, want := s.EndAfter(at(time.Monday, 2, 0)), at(time.Monday, 7, 0); !got.Equal(want) {
		t.Errorf("EndAfter early morning = %v, want %v", got, want)
	}
	// Friday afternoon: next school night starts Sunday 9pm.
	if got, want := s.NextStart(at(time.Friday, 15, 0)), at(time.Sunday, 21, 0).AddDate(0, 0, 7); !got.Equal(want) {
		t.Errorf("NextStart = %v, want %v", got, want)
	}
	if got, want := s.NextStart(at(time.Monday, 8, 0)), at(time.Monday, 21, 0); !got.Equal(want) {
		t.Errorf("NextStart = %v, want %v", got, want)
	}
}

func TestSegments(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d := &Device{Name: "phone", Kind: "phone", IP: "10.0.0.2", Monitored: true}
	if err := st.SaveDevice(d); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	st.AddTransition(d.ID, base.Add(-time.Hour), StatusOnline) // before the range
	st.AddTransition(d.ID, base.Add(10*time.Minute), StatusOffline)
	st.AddTransition(d.ID, base.Add(30*time.Minute), StatusOnline)
	st.AddTransition(d.ID, base.Add(40*time.Minute), StatusOnline) // duplicate state merges

	segs, err := st.Segments(d.ID, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []Segment{
		{StatusOnline, base, base.Add(10 * time.Minute)},
		{StatusOffline, base.Add(10 * time.Minute), base.Add(30 * time.Minute)},
		{StatusOnline, base.Add(30 * time.Minute), base.Add(time.Hour)},
	}
	if len(segs) != len(want) {
		t.Fatalf("got %d segments %+v, want %d", len(segs), segs, len(want))
	}
	for i := range want {
		if segs[i].State != want[i].State || !segs[i].Start.Equal(want[i].Start) || !segs[i].End.Equal(want[i].End) {
			t.Errorf("segment %d = %+v, want %+v", i, segs[i], want[i])
		}
	}

	// With no history the whole range is unknown.
	segs, _ = st.Segments(d.ID, base.Add(-48*time.Hour), base.Add(-47*time.Hour))
	if len(segs) != 1 || segs[0].State != StatusUnknown {
		t.Errorf("expected a single unknown segment, got %+v", segs)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c, _ := st.Config()
	if c.CheckInterval != 60 || !c.UseARP {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	c.TextbeltKey = "abc"
	c.UseARP = false
	c.CheckInterval = 5 // clamped to 15
	if err := st.SaveConfig(c); err != nil {
		t.Fatal(err)
	}
	c2, _ := st.Config()
	if c2.TextbeltKey != "abc" || c2.UseARP || c2.CheckInterval != 15 {
		t.Errorf("round trip mismatch: %+v", c2)
	}
}

func TestMigratesOldPeopleTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a database from before kid texting existed.
	for _, c := range []string{"phone", "text_kid", "kid_template"} {
		if _, err := st.db.Exec(`ALTER TABLE people DROP COLUMN ` + c); err != nil {
			t.Fatal(err)
		}
	}
	st.db.Exec(`INSERT INTO people (name, created_at) VALUES ('Old', 1)`)
	st.Close()

	st, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st.Close()
	people, err := st.ListPeople()
	if err != nil || len(people) != 1 || people[0].TextKid {
		t.Fatalf("people=%v err=%v", people, err)
	}
}
