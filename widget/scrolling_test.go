package widget

import (
	"strconv"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// newDragList mounts a 300 by 400 list of 200 rows of 40, 9194 tall in
// all, which the pointer can drag.
func newDragList(t *testing.T) (*gunim.Window, *VirtualList, func(int)) {
	t.Helper()
	w, l, run := newVirtual(t, keys(200), 40)
	l.DragScroll = true
	return w, l, run
}

// drag presses at (100, from), moves down to the given y in steps over
// d, and lets
// go, with timestamps ending now, as a hand would.
func drag(w *gunim.Window, from, to float32, d time.Duration) {
	const steps = 8
	end := time.Now()
	start := end.Add(-d)
	w.Input(input.PointerDown{Pos: geom.Pt(100, from), Clicks: 1, Time: start})
	for i := 1; i <= steps; i++ {
		y := from + (to-from)*float32(i)/steps
		w.Input(input.PointerMove{Pos: geom.Pt(100, y), Time: start.Add(d * time.Duration(i) / steps)})
	}
	w.Input(input.PointerUp{Pos: geom.Pt(100, to), Time: end})
}

func TestAFlingCarriesOnAfterRelease(t *testing.T) {
	w, l, run := newDragList(t)
	drag(w, 300, 100, 80*time.Millisecond) // 200 px in 80 ms: a fast flick
	if at := l.Offset(); at < 199 || at > 201 {
		t.Fatalf("after dragging 200 px the list is at %v", at)
	}
	prev := l.Offset()
	for range 120 {
		run(1)
		now := l.Offset()
		if now < prev-0.01 {
			t.Fatalf("the fling went backwards, from %v to %v", prev, now)
		}
		prev = now
	}
	if prev < 600 {
		t.Fatalf("the fling stopped at %v, want well past the 200 dragged", prev)
	}
}

// A busy page hears a release late, and when it happened still decides the fling.
func TestAFlingHeardLateStillFlings(t *testing.T) {
	w, l, run := newDragList(t)
	const late = 400 * time.Millisecond
	end := time.Now().Add(-late)
	start := end.Add(-100 * time.Millisecond)
	w.Input(input.PointerDown{Pos: geom.Pt(100, 300), Clicks: 1, Time: start})
	for i := 1; i <= 8; i++ {
		w.Input(input.PointerMove{Pos: geom.Pt(100, 300-25*float32(i)), Time: start.Add(100 * time.Millisecond * time.Duration(i) / 8)})
	}
	w.Input(input.PointerUp{Pos: geom.Pt(100, 100), Time: end})
	run(120)
	if l.Offset() < 600 {
		t.Fatalf("the fling stopped at %v, want well past the 200 dragged", l.Offset())
	}
}

func TestADragPastTheTopStretchesAndSpringsBack(t *testing.T) {
	w, l, run := newDragList(t)
	start := time.Now()
	w.Input(input.PointerDown{Pos: geom.Pt(100, 100), Clicks: 1, Time: start})
	w.Input(input.PointerMove{Pos: geom.Pt(100, 300), Time: start.Add(time.Second)})
	at := l.Offset()
	if at >= 0 || at <= -200 {
		t.Fatalf("a drag of 200 past the top shows offset %v, want part of the way", at)
	}
	w.Input(input.PointerUp{Pos: geom.Pt(100, 300), Time: start.Add(2 * time.Second)})
	prev := at
	for range 120 {
		run(1)
		now := l.Offset()
		if now < prev-0.01 || now > 0.5 {
			t.Fatalf("springing back went from %v to %v", prev, now)
		}
		prev = now
	}
	if prev != 0 {
		t.Fatalf("settled at %v, want 0", prev)
	}
}

func TestAFlingPastTheEndBouncesBack(t *testing.T) {
	w, l, run := newDragList(t)
	end := float32(200*46 - 6 - 400)
	l.ScrollTo(end-100, Quick.Default())
	run(120)
	drag(w, 300, 200, 40*time.Millisecond) // fast, toward the end
	most := l.Offset()
	for range 180 {
		run(1)
		most = max(most, l.Offset())
	}
	if most <= end {
		t.Fatalf("the fling stopped at the end, %v, without running past it", most)
	}
	if at := l.Offset(); at < end-0.5 || at > end+0.5 {
		t.Fatalf("settled at %v, want the end, %v", at, end)
	}
}

func TestTheWheelTakesOverAFlingFromWhereItIs(t *testing.T) {
	w, l, run := newDragList(t)
	drag(w, 300, 100, 80*time.Millisecond)
	run(10)
	at := l.Offset()
	w.Input(input.Scroll{Pos: geom.Pt(100, 100), Delta: geom.Pt(0, -40)})
	run(120)
	if got := l.Offset(); got < at+39 || got > at+41 {
		t.Fatalf("the wheel took over at %v and ended at %v, want 40 further", at, got)
	}
}

// A list says the wheel would scroll it only where its content can move that way: down from the top, and up from the
// end.
func TestAListSaysWhereTheWheelWouldScrollIt(t *testing.T) {
	_, l, run := newVirtual(t, keys(200), 40)
	run(2)
	if l.WouldScroll(geom.Pt(0, 40)) || !l.WouldScroll(geom.Pt(0, -40)) || l.WouldScroll(geom.Pt(40, 0)) {
		t.Errorf("at the top, the wheel would scroll it up %v, down %v and sideways %v; want down alone",
			l.WouldScroll(geom.Pt(0, 40)), l.WouldScroll(geom.Pt(0, -40)), l.WouldScroll(geom.Pt(40, 0)))
	}
	l.JumpTo(1e6)
	run(2)
	if !l.WouldScroll(geom.Pt(0, 40)) || l.WouldScroll(geom.Pt(0, -40)) {
		t.Errorf("at the end, the wheel would scroll it up %v and down %v; want up alone",
			l.WouldScroll(geom.Pt(0, 40)), l.WouldScroll(geom.Pt(0, -40)))
	}
}

// Every view in the package that scrolls its content with the wheel says when it would, so a control inside it leaves
// it the wheel.
func TestEveryViewThatScrollsWithTheWheelSaysSo(t *testing.T) {
	for _, c := range []struct {
		name string
		n    any
	}{
		{"Scroll", (*Scroll)(nil)}, {"VirtualList", (*VirtualList)(nil)}, {"TileGrid", (*TileGrid)(nil)},
		{"Menu", (*Menu)(nil)}, {"Table", (*Table)(nil)}, {"the address bar's places", (*crumbBar)(nil)},
	} {
		if _, ok := c.n.(gunim.WheelScroller); !ok {
			t.Errorf("%s scrolls with the wheel and is no WheelScroller", c.name)
		}
	}
}

// A menu taller than its card says the wheel would scroll it down from its top, and not up.
func TestAMenuSaysWhereTheWheelWouldScrollIt(t *testing.T) {
	items := make([]MenuItem, 0, 80)
	for i := range 80 {
		items = append(items, MenuItem{Label: strconv.Itoa(i)})
	}
	m := NewMenu(items)
	_, run := stage(t, m)
	run(2)
	if !m.scroll.scrollable() {
		t.Fatal("the menu fits its card, with nothing to scroll")
	}
	if wm, ok := any(m).(gunim.WheelScroller); !ok || !wm.WouldScroll(geom.Pt(0, -40)) || wm.WouldScroll(geom.Pt(0, 40)) {
		t.Errorf("at its top, the menu is a WheelScroller %v; want one the wheel scrolls down and not up", ok)
	}
}
