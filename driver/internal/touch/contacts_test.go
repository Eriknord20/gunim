package touch

import (
	"math"
	"testing"
	"time"

	"github.com/marrasen/gunim/geom"
)

func c(id int, x, y float32, a Action) Contact { return Contact{ID: id, Pos: geom.Pt(x, y), Phase: a} }

func TestOneFingerIsThePointer(t *testing.T) {
	g, page, _ := screen()
	cs := NewContacts(g)
	cs.Report([]Contact{c(4, 10, 20, Down)}, at(0))
	cs.Report([]Contact{c(4, 15, 20, Move)}, at(17))
	cs.Report([]Contact{c(4, 15, 20, Up)}, at(34))
	want(t, page, "page: move 10,20", "page: down 10,20 clicks 1", "page: move 15,20", "page: up 15,20", "page: leave")
}

func TestTwoFingersPinchAndTheThirdIsLeftOut(t *testing.T) {
	g, page, _ := screen()
	cs := NewContacts(g)
	cs.Report([]Contact{c(0, 10, 10, Down)}, at(0))
	cs.Report([]Contact{c(1, 30, 10, Down)}, at(17))
	cs.Report([]Contact{c(2, 300, 300, Down)}, at(20))
	cs.Report([]Contact{c(0, 0, 10, Move), c(1, 40, 10, Move), c(2, 310, 300, Move)}, at(34))
	cs.Report([]Contact{c(1, 40, 10, Up)}, at(51))
	// The pointer's finger alone now: it only lifts.
	cs.Report([]Contact{c(0, 5, 10, Move)}, at(60))
	cs.Report([]Contact{c(2, 310, 300, Up)}, at(65))
	cs.Report([]Contact{c(0, 5, 10, Up)}, at(68))
	want(t, page, "page: move 10,10", "page: down 10,10 clicks 1",
		"page: up away", "page: pinch start 20,10 scale 1.00 delta 0,0",
		"page: pinch move 20,10 scale 2.00 delta 0,0",
		"page: pinch end 20,10 scale 1.00 delta 0,0",
		"page: leave")
}

func TestFingersOfTheirOwnInAWindowThatTakesThem(t *testing.T) {
	g, page, _ := screen()
	page.fingers = true
	cs := NewContacts(g)
	cs.Report([]Contact{c(0, 10, 10, Down)}, at(0))
	cs.Report([]Contact{c(1, 30, 10, Down)}, at(17))
	// Finger 1 stays put: it sends no move.
	cs.Report([]Contact{c(0, 12, 10, Move), c(1, 30, 10, Move)}, at(34))
	// The pointer's finger lifts first: the press lets go, then the finger.
	cs.Report([]Contact{c(0, 12, 10, Up)}, at(51))
	cs.Report([]Contact{c(1, 30, 12, Move)}, at(60))
	cs.Report([]Contact{c(1, 30, 12, Up)}, at(68))
	want(t, page, "page: finger 0 down 10,10", "page: move 10,10", "page: down 10,10 clicks 1",
		"page: finger 1 down 30,10",
		"page: finger 0 move 12,10", "page: move 12,10",
		"page: up 12,10", "page: leave", "page: finger 0 up 12,10",
		"page: finger 1 move 30,12",
		"page: finger 1 up 30,12")
}

func TestLiftsGoBeforeLandingsInOneReport(t *testing.T) {
	g, page, _ := screen()
	cs := NewContacts(g)
	cs.Report([]Contact{c(0, 10, 10, Down)}, at(0))
	// One finger lifts as another lands, in the same report: a new touch.
	cs.Report([]Contact{c(1, 50, 50, Down), c(0, 10, 10, Up)}, at(17))
	want(t, page, "page: move 10,10", "page: down 10,10 clicks 1", "page: up 10,10", "page: leave",
		"page: move 50,50", "page: down 50,50 clicks 1")
}

func TestCancelCallsTheTouchOff(t *testing.T) {
	g, page, _ := screen()
	page.fingers = true
	cs := NewContacts(g)
	cs.Report([]Contact{c(0, 10, 10, Down)}, at(0))
	page.take()
	cs.Cancel(at(5))
	want(t, page, "page: up away", "page: leave", "page: finger 0 up away")
	if cs.Down(0) {
		t.Error("finger 0 is still down after Cancel")
	}
}

// A steady drag on the Touch Display 2 reports every 17 ms, against
// frames every 16.7 ms. Moved by its newest report, a frame steps 0, 1 or 2
// reports' worth; resampled, every frame steps the same, within 10 %.
func TestResamplingEvensASteadyDrag(t *testing.T) {
	const (
		report = 17 * time.Millisecond
		speed  = 1.0 // logical pixels a millisecond
	)
	const frame = 16664 * time.Microsecond // 60.01 Hz
	place := func(t time.Time) float32 { return float32(float64(t.Sub(t0)) / float64(time.Millisecond) * speed) }
	var r Resampler
	r.Down(0, t0, geom.Pt(0, 0))
	next := t0.Add(report)
	var raw, even []float32
	var lastRaw, lastEven float32
	for f := 1; f <= 600; f++ {
		start := t0.Add(time.Duration(f) * frame).Add(3 * time.Millisecond)
		newest := lastRaw
		for !next.After(start) {
			r.Move(0, next, geom.Pt(place(next), 0))
			newest = place(next)
			next = next.Add(report)
		}
		raw = append(raw, newest-lastRaw)
		lastRaw = newest
		if out := r.At(start, nil); len(out) == 1 {
			even = append(even, out[0].Pos.X-lastEven)
			lastEven = out[0].Pos.X
		} else {
			even = append(even, 0)
		}
	}
	spread := func(steps []float32) float64 {
		var sum float64
		for _, s := range steps {
			sum += float64(s)
		}
		mean := sum / float64(len(steps))
		worst := 0.0
		for _, s := range steps {
			worst = math.Max(worst, math.Abs(float64(s)-mean)/mean)
		}
		return worst
	}
	// Past the first frames, where the drag starts from rest.
	if s := spread(raw[10:]); s < 0.99 {
		t.Errorf("raw reports step within %.0f %% of their mean; the test wants them uneven, as on the Pi", s*100)
	}
	if s := spread(even[10:]); s > 0.10 {
		t.Errorf("resampled steps vary by %.0f %% of their mean, want under 10 %%", s*100)
	}
}

func TestResamplingCarriesOnByEightMillisecondsAtMost(t *testing.T) {
	var r Resampler
	r.Down(0, at(0), geom.Pt(0, 0))
	r.Move(0, at(17), geom.Pt(17, 0))
	// Sampled 9 ms before the frame's start, 17 ms less 8: at 26 ms, 9 ms
	// past the newest report, carried on 8.
	out := r.At(at(35), nil)
	if len(out) != 1 || out[0].Pos.X != 25 {
		t.Fatalf("got %v, want the finger carried on to 25", out)
	}
	// Nothing new: no move, and no further.
	if out := r.At(at(45), nil); len(out) != 0 {
		t.Errorf("got %v after the furthest carry, want nothing", out)
	}
	if r.Pending() {
		t.Error("Pending with every report sent")
	}
	// A report between: interpolated, never earlier than the move sent.
	r.Move(0, at(34), geom.Pt(30, 0))
	if !r.Pending() {
		t.Error("not Pending with a report not sent")
	}
	if out := r.At(at(34), nil); len(out) != 0 {
		t.Errorf("got %v for a time before the move sent, want nothing", out)
	}
	out = r.At(at(39), nil)
	if len(out) != 1 || math.Abs(float64(out[0].Pos.X-26.941)) > 0.01 {
		t.Errorf("got %v, want 26.94, at 30 between the reports at 17 and 34", out)
	}
	r.Up(0)
	if out := r.At(at(209), nil); len(out) != 0 {
		t.Errorf("got %v for a lifted finger", out)
	}
}

// A finger that stops reporting has stopped: once two report gaps pass,
// it goes back to where it was last reported, from ahead of it.
func TestAFingerThatStopsShowsWhereItIs(t *testing.T) {
	var r Resampler
	r.Down(0, at(0), geom.Pt(0, 0))
	r.Move(0, at(17), geom.Pt(17, 0))
	r.Move(0, at(34), geom.Pt(34, 0))
	if out := r.At(at(51), nil); len(out) != 1 || out[0].Pos.X != 42 {
		t.Fatalf("got %v, want the finger carried on to 42", out)
	}
	// Two gaps, 34 ms, after the last report: still carried, no further.
	if out := r.At(at(77), nil); len(out) != 0 {
		t.Fatalf("got %v within two gaps, want nothing", out)
	}
	out := r.At(at(78), nil)
	if len(out) != 1 || out[0].Pos.X != 34 {
		t.Fatalf("got %v past two gaps, want the finger back at 34", out)
	}
	if out := r.At(at(120), nil); len(out) != 0 {
		t.Fatalf("got %v once back, want nothing", out)
	}
}

func TestNewestSendsTheNewestReport(t *testing.T) {
	var r Resampler
	r.Down(0, at(0), geom.Pt(0, 0))
	r.Move(0, at(17), geom.Pt(5, 0))
	r.Move(0, at(34), geom.Pt(9, 0))
	out := r.Newest(nil)
	if len(out) != 1 || out[0].Pos.X != 9 {
		t.Fatalf("got %v, want the newest report", out)
	}
	if out := r.Newest(nil); len(out) != 0 {
		t.Errorf("got %v again", out)
	}
}
