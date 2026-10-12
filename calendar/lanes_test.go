package calendar

import (
	"strconv"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
)

func at(h, m int) time.Time { return time.Date(2026, 9, 29, h, m, 0, 0, time.UTC) }

func TestLanesKeepOverlappingEventsReadable(t *testing.T) {
	type ev struct{ from, to [2]int }
	cases := []struct {
		name string
		evs  []ev
		want []placed
	}{
		{"one alone takes the whole column", []ev{{[2]int{9, 0}, [2]int{10, 0}}},
			[]placed{{0, 0, 0, 1, 1}}},
		{"one after another each take it whole", []ev{{[2]int{9, 0}, [2]int{10, 0}}, {[2]int{10, 0}, [2]int{11, 0}}},
			[]placed{{0, 0, 0, 1, 1}, {1, 0, 0, 1, 1}}},
		{"two starting close together sit side by side", []ev{{[2]int{9, 0}, [2]int{11, 0}}, {[2]int{9, 15}, [2]int{12, 0}}},
			[]placed{{0, 0, 0, 1, 2}, {1, 0, 1, 1, 2}}},
		{"a later one sits on top, indented", []ev{{[2]int{9, 0}, [2]int{11, 0}}, {[2]int{10, 0}, [2]int{12, 0}}},
			[]placed{{0, 0, 0, 1, 1}, {1, 1, 0, 1, 1}}},
		{"each later one indents a step further", []ev{
			{[2]int{9, 0}, [2]int{13, 0}}, {[2]int{10, 0}, [2]int{12, 0}}, {[2]int{11, 0}, [2]int{11, 30}}},
			[]placed{{0, 0, 0, 1, 1}, {1, 1, 0, 1, 1}, {2, 2, 0, 1, 1}}},
		{"two close together on top share the indent", []ev{
			{[2]int{9, 0}, [2]int{12, 0}}, {[2]int{10, 0}, [2]int{11, 0}}, {[2]int{10, 15}, [2]int{11, 0}}},
			[]placed{{0, 0, 0, 1, 1}, {1, 1, 0, 1, 2}, {2, 1, 1, 1, 2}}},
	}
	for _, c := range cases {
		var starts, ends []time.Time
		for _, e := range c.evs {
			starts = append(starts, at(e.from[0], e.from[1]))
			ends = append(ends, at(e.to[0], e.to[1]))
		}
		got, order := lanes(starts, ends, time.Minute)
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("%s: event %d at %+v, want %+v", c.name, i, got[i], c.want[i])
			}
		}
		for k := 1; k < len(order); k++ {
			if starts[order[k]].Before(starts[order[k-1]]) {
				t.Errorf("%s: drawn in the order %v, want the earliest first", c.name, order)
			}
		}
	}
}

func TestDaysCountByTheCalendarAcrossAClockChange(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Stockholm")
	if err != nil {
		t.Skip("no time zone data")
	}
	sat := time.Date(2026, 10, 24, 0, 0, 0, 0, loc)
	if got := AddDays(sat, 2); got.Day() != 26 || got.Hour() != 0 {
		t.Fatalf("two days after Saturday is %v, want Monday at midnight", got)
	}
	if got := WeekStart(time.Date(2026, 10, 25, 15, 0, 0, 0, loc), time.Monday); got.Day() != 19 {
		t.Fatalf("the week of Sunday the 25th starts %v, want Monday the 19th", got)
	}
}

func TestParseClockReadsTheWaysPeopleWriteATime(t *testing.T) {
	ok := map[string]time.Duration{
		"9": 9 * time.Hour, "09": 9 * time.Hour, "930": 9*time.Hour + 30*time.Minute, "0930": 9*time.Hour + 30*time.Minute,
		"9:30": 9*time.Hour + 30*time.Minute, "9.30": 9*time.Hour + 30*time.Minute,
		// Spaces either side: what a user types is trimmed on the way in.
		" 23:59 ": 23*time.Hour + 59*time.Minute, //nolint:gocritic // the spaces are the test
		"0:00":    0,
	}
	for s, want := range ok {
		if got, good := ParseClock(s); !good || got != want {
			t.Errorf("%q reads as %v, %v; want %v", s, got, good, want)
		}
	}
	for _, s := range []string{"", "24", "9:3", "9:60", "abc", "12345", ":30"} {
		if got, good := ParseClock(s); good {
			t.Errorf("%q reads as %v, want no time", s, got)
		}
	}
}

// meets reports whether a and b share some area.
func meets(a, b geom.Rect) bool {
	return a.Min.X < b.Max.X && b.Min.X < a.Max.X && a.Min.Y < b.Max.Y && b.Min.Y < a.Max.Y
}

// drawnBoxes returns the boxes of the timed events as drawn this frame, in the grid's space, by event ID.
func drawnBoxes(d *Days) map[string]geom.Rect {
	out := map[string]geom.Rect{}
	for _, k := range d.order {
		if s := d.sprites[k]; s != nil && !s.gone && !s.long {
			out[s.e.ID] = d.onScreen(s, s.rect.Value())
		}
	}
	return out
}

func TestShortEventsInARowSitSideBySide(t *testing.T) {
	d := newWeek()
	tue := monday.Add(24*time.Hour + 9*time.Hour)
	d.events = nil
	// Four five-minute events in a row, and two quarter-hour meetings back to back an hour later.
	for i, id := range []string{"a", "b", "c", "d"} {
		s := tue.Add(time.Duration(i) * 5 * time.Minute)
		d.events = append(d.events, Event{ID: id, Title: id, Start: s, End: s.Add(5 * time.Minute)})
	}
	for i, id := range []string{"m1", "m2"} {
		s := tue.Add(time.Hour + time.Duration(i)*15*time.Minute)
		d.events = append(d.events, Event{ID: id, Title: id, Start: s, End: s.Add(15 * time.Minute)})
	}
	w, run, _ := stage(t, d)
	check := func(when string) {
		t.Helper()
		boxes := drawnBoxes(d)
		if len(boxes) != len(d.events) {
			t.Fatalf("%s: %d events drawn, want %d", when, len(boxes), len(d.events))
		}
		for _, a := range d.events {
			for _, b := range d.events {
				if a.ID < b.ID && meets(boxes[a.ID], boxes[b.ID]) {
					t.Fatalf("%s: %s at %v and %s at %v cover each other", when, a.ID, boxes[a.ID], b.ID, boxes[b.ID])
				}
			}
		}
	}
	for f := range 30 {
		check("frame " + strconv.Itoa(f))
		run(1)
	}
	// Half way through the grid stepping to the next week and back, they keep apart.
	withUI(t, w, func(u *gunim.UI) { d.SetDays(monday.Add(7*24*time.Hour), 7, u) })
	run(3)
	withUI(t, w, func(u *gunim.UI) { d.SetDays(monday, 7, u) })
	for f := range 30 {
		run(1)
		check("back, frame " + strconv.Itoa(f))
	}
}
