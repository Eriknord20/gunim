package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// pressRow is a row that takes presses and moves, as a row of buttons
// or selectable text does, and counts the presses it got.
type pressRow struct {
	block
	presses *int
}

func (r *pressRow) Handle(e input.Event, _ *gunim.UI) bool {
	switch e.(type) {
	case input.PointerDown:
		*r.presses++
		return true
	case input.PointerMove, input.PointerUp:
		return true
	}
	return false
}

func (r *pressRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	r.block.Paint(p, f, box, kids)
}

// newPressList mounts a 300 by 400 list of 200 rows of 40 that take
// presses and moves, 9194 tall in all, and returns a count of the
// presses they got.
func newPressList(t *testing.T) (w *gunim.Window, list *VirtualList, run func(int), presses *int) {
	t.Helper()
	presses = new(int)
	list = NewVirtualList(func(k Key) gunim.Node {
		return &pressRow{block: block{key: k, h: 40}, presses: presses}
	})
	w = gunimtest.New(t, geom.Sz(300, 400), nil)
	gunim.RegisterView(w, "v", func(shownKeys) gunim.Node { return list },
		func(_ gunim.Node, s shownKeys, u *gunim.UI) { list.SetKeys(s.Keys, u) })
	if err := w.Client().Mount(gunim.Root, "v", "v", shownKeys{keys(200)}); err != nil {
		t.Fatal(err)
	}
	run = func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(2)
	return w, list, run, presses
}

func TestTheBarShowsWhileThePointerIsOverTheList(t *testing.T) {
	w, l, run, _ := newPressList(t)
	if l.bar.Value() != 0 {
		t.Fatalf("the bar shows at %v before anything happened", l.bar.Value())
	}
	w.Input(input.PointerMove{Pos: geom.Pt(100, 100), Time: time.Now()})
	prev := float32(0)
	for range 30 {
		run(1)
		now := l.bar.Value()
		if prev < 1 && now < prev-0.001 {
			t.Fatalf("the bar faded from %v to %v while coming in", prev, now)
		}
		prev = now
	}
	if prev < 0.99 {
		t.Fatalf("the bar is at %v after half a second of hover, want 1", prev)
	}
	// Two seconds on, well past the linger, it is still there, and the
	// list asks for no frames to keep it.
	run(120)
	if v := l.bar.Value(); v < 0.99 {
		t.Fatalf("the bar faded to %v while the pointer rested over the list", v)
	}
	if l.Step(0) {
		t.Fatal("the list keeps drawing frames while the pointer rests over it")
	}
	w.Input(input.PointerLeave{Time: time.Now()})
	run(1)
	// While the bar lingers, the window sleeps, and asks to be woken to
	// fade it.
	if l.Step(0) || l.WakeIn() <= 0 {
		t.Fatalf("lingering, the list keeps drawing %v, and asks to wake in %v", l.Step(0), l.WakeIn())
	}
	run(120)
	if v := l.bar.Value(); v > 0.01 {
		t.Fatalf("the bar is at %v two seconds after the pointer left, want gone", v)
	}
}

func TestDraggingTheThumbScrollsOverRowsThatTakePresses(t *testing.T) {
	w, l, run, presses := newPressList(t)
	const end = 9194 - 400
	// The list is 9194 tall in a view of 400, so the thumb is 24 tall at
	// the top, and each pixel it moves scrolls (9194-400)/(400-24).
	w.Input(input.PointerMove{Pos: geom.Pt(296, 10), Time: time.Now()})
	run(10)
	if !l.onBar || l.wide.Target() != 1 {
		t.Fatalf("the pointer on the bar left it narrow: onBar %v, wide %v", l.onBar, l.wide.Target())
	}
	w.Input(input.PointerDown{Pos: geom.Pt(296, 10), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	if *presses != 0 {
		t.Fatalf("a press on the bar reached a row")
	}
	per := float32(end) / (400 - 24)
	prev := l.Offset()
	for y := float32(26); y <= 202; y += 16 {
		w.Input(input.PointerMove{Pos: geom.Pt(296, y), Time: time.Now()})
		run(1)
		now := l.Offset()
		if now < prev {
			t.Fatalf("at y %v the list went back from %v to %v", y, prev, now)
		}
		if want := (y - 10) * per; now < want-0.5 || now > want+0.5 {
			t.Fatalf("at y %v the list is at %v, want %v", y, now, want)
		}
		prev = now
	}
	// Past the bottom of the view the thumb stops at the end.
	w.Input(input.PointerMove{Pos: geom.Pt(250, 600), Time: time.Now()})
	run(1)
	if at := l.Offset(); at != end {
		t.Fatalf("dragged past the bottom, the list is at %v, want %v", at, end)
	}
	w.Input(input.PointerUp{Pos: geom.Pt(250, 600), Button: input.ButtonPrimary, Time: time.Now()})
	run(30)
	if at := l.Offset(); at != end {
		t.Fatalf("after the release the list moved to %v", at)
	}
	if *presses != 0 {
		t.Fatalf("the thumb drag pressed %d rows", *presses)
	}
}

func TestAPressOnTheTrackPagesTowardIt(t *testing.T) {
	w, l, run, presses := newPressList(t)
	w.Input(input.PointerMove{Pos: geom.Pt(296, 300), Time: time.Now()})
	w.Input(input.PointerDown{Pos: geom.Pt(296, 300), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	w.Input(input.PointerUp{Pos: geom.Pt(296, 300), Button: input.ButtonPrimary, Time: time.Now()})
	prev := l.Offset()
	for range 60 {
		run(1)
		now := l.Offset()
		// The spring may overshoot the page a little and settle back.
		if prev < 360 && now < prev-0.01 {
			t.Fatalf("paging down went back from %v to %v", prev, now)
		}
		prev = now
	}
	if prev < 359 || prev > 361 {
		t.Fatalf("a press below the thumb paged to %v, want 360", prev)
	}
	if *presses != 0 {
		t.Fatalf("a press on the track reached a row")
	}
}

func TestTheBarNarrowsWhenThePointerMovesOntoARow(t *testing.T) {
	w, l, run, _ := newPressList(t)
	w.Input(input.PointerMove{Pos: geom.Pt(296, 100), Time: time.Now()})
	run(30)
	if l.wide.Value() < 0.99 {
		t.Fatalf("the bar is %v wide with the pointer on it, want 1", l.wide.Value())
	}
	// The row takes the move, and the list still hears the pointer went.
	w.Input(input.PointerMove{Pos: geom.Pt(270, 100), Time: time.Now()})
	run(30)
	if l.onBar || l.wide.Value() > 0.01 {
		t.Fatalf("with the pointer on a row the bar stays wide: onBar %v, wide %v", l.onBar, l.wide.Value())
	}
}
