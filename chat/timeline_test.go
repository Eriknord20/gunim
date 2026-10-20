package chat

import (
	"strconv"
	"testing"
	"time"
)

func TestASteadyStreamStillShowsHeadings(t *testing.T) {
	start := time.Date(2026, 9, 29, 10, 0, 0, 0, time.Local)
	// One person, a message every 30 seconds for 20 minutes.
	msgs := make([]Message, 0, 40)
	for i := range 40 {
		msgs = append(msgs, Message{ID: "m" + strconv.Itoa(i), Author: "Johan Ek", At: start.Add(time.Duration(i) * 30 * time.Second)})
	}
	var headed []time.Time
	for _, it := range Timeline(msgs, start, "") {
		if it.Day == "" && !it.Continued {
			headed = append(headed, it.At)
		}
	}
	if len(headed) < 4 {
		t.Fatalf("%d headings in 20 minutes of one person's messages, want one at least every %v", len(headed), groupFor)
	}
	for i := 1; i < len(headed); i++ {
		if gap := headed[i].Sub(headed[i-1]); gap > groupFor {
			t.Fatalf("headings %v apart, want at most %v", gap, groupFor)
		}
	}
}

func TestTheTimelineHeadsEachDayAndMarksWhatIsNew(t *testing.T) {
	yesterday := now.Add(-24 * time.Hour)
	msgs := []Message{
		{ID: "a", Author: "Anna Berg", At: yesterday},
		{ID: "b", Author: "Anna Berg", At: now.Add(-3 * time.Minute)},
		{ID: "c", Author: "Anna Berg", At: now.Add(-2 * time.Minute)},
		{ID: "d", Author: "Anna Berg", At: now.Add(-time.Minute), Reply: Quote{ID: "a"}},
		{ID: "e", Author: "Anna Berg", At: now, Private: true},
	}
	got := Timeline(msgs, now, "c")
	want := []struct {
		key, day  string
		new, cont bool
	}{
		{"day:a", "Yesterday", false, false}, {"a", "", false, false},
		{"day:b", "Today", false, false}, {"b", "", false, false},
		{"new:c", "", true, false}, {"c", "", false, false},
		// A reply, and a private answer, head themselves.
		{"d", "", false, false}, {"e", "", false, false},
	}
	if len(got) != len(want) {
		t.Fatalf("%d rows, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if g := got[i]; g.Key != w.key || g.Day != w.day || g.New != w.new || g.Continued != w.cont {
			t.Fatalf("row %d is %+v, want %+v", i, g, w)
		}
	}
}

func TestDayNameSaysTodayYesterdayOrTheDate(t *testing.T) {
	for _, c := range []struct {
		t    time.Time
		want string
	}{
		{now.Add(-time.Hour), "Today"},
		{now.Add(-24 * time.Hour), "Yesterday"},
		{time.Date(2026, 10, 14, 9, 0, 0, 0, time.Local), "Wednesday 14 October"},
	} {
		if got := DayName(c.t, now); got != c.want {
			t.Fatalf("DayName(%v) = %q, want %q", c.t, got, c.want)
		}
	}
}
