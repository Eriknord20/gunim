package widget

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
)

// newScrolledReorderList mounts twenty 40-tall rows, 46 apart, which
// the pointer can reorder, in a scroll view 200 tall.
func newScrolledReorderList(t *testing.T) (*gunim.Window, *Scroll, *List, func(int)) {
	t.Helper()
	l := NewList()
	l.OnReorder = func(keys []Key, u *gunim.UI) gunim.Intent { return reordered{keys} }
	s := NewScroll(l)
	w := gunimtest.New(t, geom.Sz(300, 200), nil)
	gunim.RegisterView(w, "l", func(shownItems) gunim.Node { return s },
		func(_ gunim.Node, st shownItems, u *gunim.UI) {
			Sync(l, u, st.Items, func(k Key) Key { return k },
				func(k Key) *block { return &block{key: k, h: 40} }, nil)
		})
	if err := w.Client().Mount(gunim.Root, "l", "l", shownItems{keys(20)}); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(60)
	return w, s, l, run
}

func TestARowHeldAtTheBottomScrollsTheList(t *testing.T) {
	w, s, l, run := newScrolledReorderList(t)
	// Pick up row 1, 46 down, by its middle, and carry it down in small
	// steps to 10 short of the bottom edge.
	w.Input(input.PointerDown{Pos: geom.Pt(100, 66), Clicks: 1})
	for y := float32(76); y <= 190; y += 16 {
		w.Input(input.PointerMove{Pos: geom.Pt(100, min(y, 190))})
		run(1)
	}
	w.Input(input.PointerMove{Pos: geom.Pt(100, 190)})
	run(1)

	// Held there, the view scrolls down every frame, never back, and the
	// row stays under the pointer: its top 20 above it.
	last := s.Offset()
	for i := range 240 {
		run(1)
		at := s.Offset()
		if at < last {
			t.Fatalf("frame %d: the view scrolled back, from %v to %v", i, last, at)
		}
		last = at
		// The list is placed on whole device pixels, as onPixels says:
		// the row may be off the pointer by up to half of one.
		if onScreen := l.rows["1"].y.Value() - at; math.Abs(float64(onScreen-170)) > halfPixel(w) {
			t.Fatalf("frame %d: the carried row's top is at %v on screen, want 170, under the pointer", i, onScreen)
		}
	}
	end := l.Height() - 200
	if last != end {
		t.Fatalf("after four seconds held at the edge the view is at %v, want the end, %v", last, end)
	}

	// Let go: the row lands last, where the pointer is.
	w.Input(input.PointerUp{Pos: geom.Pt(100, 190)})
	run(60)
	order := l.Keys()
	if i := slices.Index(order, "1"); i != 19 {
		t.Fatalf("the row landed at %d in %v, want 19, last", i, order)
	}
}

func TestAPressHeldAtTheEdgeScrollsNothing(t *testing.T) {
	w, s, _, run := newScrolledReorderList(t)
	// A press near the bottom, moved too little to pick the row up.
	w.Input(input.PointerDown{Pos: geom.Pt(100, 190), Clicks: 1})
	w.Input(input.PointerMove{Pos: geom.Pt(100, 192)})
	run(60)
	if at := s.Offset(); at != 0 {
		t.Fatalf("a press at the edge scrolled the view to %v", at)
	}
	w.Input(input.PointerUp{Pos: geom.Pt(100, 192)})
}

// takenBy is what a drop on the i-th of a column of targets sends.
type takenBy struct{ Index int }

func TestADragHeldAtTheBottomOfAScrollingListOfTargetsScrollsIt(t *testing.T) {
	// A draggable 40 tall on top, and under it a view 160 tall of
	// twenty targets 40 tall.
	d := NewDraggable(&block{h: 40}, "apple")
	targets := make([]gunim.Node, 20)
	glows := make([]*DropTarget, 20)
	for i := range targets {
		target := NewDropTarget(&block{h: 40})
		target.OnDrop = func(input.Drop, *gunim.UI) gunim.Intent { return takenBy{i} }
		targets[i], glows[i] = target, target
	}
	list := Column(targets...)
	list.Cross = CrossStretch
	s := NewScroll(list)
	col := Column(d, s).Grow(s, 1)
	col.Cross = CrossStretch
	w, run := stage(t, &frame{child: col, size: geom.Sz(300, 200)})
	run(10)

	w.Input(input.PointerDown{Pos: geom.Pt(100, 20), Clicks: 1, Time: time.Now()})
	for y := float32(36); y <= 190; y += 16 {
		w.Input(input.PointerMove{Pos: geom.Pt(100, min(y, 190)), Time: time.Now()})
		run(1)
	}
	w.Input(input.PointerMove{Pos: geom.Pt(100, 190), Time: time.Now()})
	last := s.Offset()
	for i := range 240 {
		run(1)
		at := s.Offset()
		if at < last {
			t.Fatalf("frame %d: the view scrolled back, from %v to %v", i, last, at)
		}
		last = at
	}
	if last == 0 {
		t.Fatal("a drag held at the bottom edge left the view where it was")
	}
	// One target lights up, one of the last, brought under the pointer,
	// and it takes the drop.
	run(20)
	under := -1
	for i, g := range glows {
		if g.glow.Value() > 0.5 {
			if under >= 0 {
				t.Fatalf("targets %d and %d both glow", under, i)
			}
			under = i
		}
	}
	if under < 15 {
		t.Fatalf("target %d glows, want one of the last, scrolled under the pointer", under)
	}
	w.Input(input.PointerUp{Pos: geom.Pt(100, 190), Time: time.Now()})
	run(30)
	got := sent(w)
	if len(got) != 1 || got[0] != (takenBy{under}) {
		t.Fatalf("intents %v, want a drop on target %d", got, under)
	}
}

func TestARowHeldAtTheTopScrollsBackUp(t *testing.T) {
	w, s, l, run := newScrolledReorderList(t)
	s.ScrollTo(300, Quick.Default())
	run(60)
	// Row 8 has its top at 368, 68 down on screen. Pick it up and carry
	// it up to 5 below the top edge.
	w.Input(input.PointerDown{Pos: geom.Pt(100, 88), Clicks: 1})
	for y := float32(72); y >= 5; y -= 16 {
		w.Input(input.PointerMove{Pos: geom.Pt(100, y)})
		run(1)
	}
	w.Input(input.PointerMove{Pos: geom.Pt(100, 5)})
	run(1)
	last := s.Offset()
	for i := range 120 {
		run(1)
		at := s.Offset()
		if at > last {
			t.Fatalf("frame %d: the view scrolled back down, from %v to %v", i, last, at)
		}
		last = at
		if onScreen := l.rows["8"].y.Value() - at; math.Abs(float64(onScreen+15)) > halfPixel(w) {
			t.Fatalf("frame %d: the carried row's top is at %v on screen, want -15, under the pointer", i, onScreen)
		}
	}
	if last != 0 {
		t.Fatalf("after two seconds held at the top the view is at %v, want 0", last)
	}
	w.Input(input.PointerUp{Pos: geom.Pt(100, 5)})
	run(60)
	if order := l.Keys(); order[0] != "8" {
		t.Fatalf("order %v, want row 8 first", order)
	}
}

func TestARowHeldAZoneFromTheBottomScrollsTheList(t *testing.T) {
	w, s, _, run := newScrolledReorderList(t)
	// Carry row 1 down to 44 above the bottom edge of a view 200 tall,
	// inside the zone of 48 but no nearer.
	w.Input(input.PointerDown{Pos: geom.Pt(100, 66), Clicks: 1})
	for y := float32(76); y <= 156; y += 16 {
		w.Input(input.PointerMove{Pos: geom.Pt(100, min(y, 156))})
		run(1)
	}
	w.Input(input.PointerMove{Pos: geom.Pt(100, 156)})
	run(60)
	if at := s.Offset(); at < 100 {
		t.Fatalf("a second held 44 above the bottom scrolled the view to %v, want at least 100", at)
	}
	w.Input(input.PointerUp{Pos: geom.Pt(100, 156)})
}

func TestADragHeldPastTheBottomOfAListOfTargetsScrollsIt(t *testing.T) {
	// A draggable 40 tall on top, under it a view of twenty targets 40
	// tall, and under that a bar 40 tall that takes no drop.
	d := NewDraggable(&block{h: 40}, "apple")
	targets := make([]gunim.Node, 20)
	for i := range targets {
		target := NewDropTarget(&block{h: 40})
		target.OnDrop = func(input.Drop, *gunim.UI) gunim.Intent { return takenBy{i} }
		targets[i] = target
	}
	list := Column(targets...)
	list.Cross = CrossStretch
	s := NewScroll(list)
	col := Column(d, s, &block{h: 40}).Grow(s, 1)
	col.Cross = CrossStretch
	w, run := stage(t, &frame{child: col, size: geom.Sz(300, 240)})
	run(10)

	// Carry the drag down past the view's bottom, at 200, onto the bar.
	w.Input(input.PointerDown{Pos: geom.Pt(100, 20), Clicks: 1, Time: time.Now()})
	for y := float32(36); y <= 220; y += 16 {
		w.Input(input.PointerMove{Pos: geom.Pt(100, min(y, 220)), Time: time.Now()})
		run(1)
	}
	w.Input(input.PointerMove{Pos: geom.Pt(100, 220), Time: time.Now()})
	run(60)
	if at := s.Offset(); at < 300 {
		t.Fatalf("a second held 20 past the bottom edge scrolled the view to %v, want at least 300", at)
	}
	w.Input(input.PointerUp{Pos: geom.Pt(100, 220), Time: time.Now()})
	run(30)
}

// halfPixel is half a device pixel of w, in logical pixels, and a little
// over: how far what scrolls may be from where the pointer holds it.
func halfPixel(w *gunim.Window) float64 { return 0.5/float64(w.Offscreen().Scale()) + 0.01 }
