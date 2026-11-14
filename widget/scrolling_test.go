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

// wheelView is a view staged to say whether the wheel is its own: axis is the way it scrolls, across what it answers
// for the other way while it has room, and to puts it a share of the way from its start to its end.
type wheelView struct {
	ws     gunim.WheelScroller
	axis   geom.Point
	across bool
	to     func(share float32)
	run    func(int)
}

// scrollingView stages s, a view that scrolls up and down, in the window run steps.
func scrollingView(ws gunim.WheelScroller, s *scrolling, run func(int)) wheelView {
	return wheelView{ws: ws, axis: geom.Pt(0, 1), to: func(share float32) { s.JumpTo(share * s.end()) }, run: run}
}

// menuView stages a menu of n rows.
func menuView(t *testing.T, n int) wheelView {
	items := make([]MenuItem, 0, n)
	for i := range n {
		items = append(items, MenuItem{Label: strconv.Itoa(i)})
	}
	m := NewMenu(items)
	_, run := stage(t, m)
	return scrollingView(m, &m.scroll, run)
}

// tableView stages a table of 300 rows, with columns wider than it where wide is set.
func tableView(t *testing.T, wide bool) wheelView {
	_, tbl, _, run := newTableStage(t, 300)
	if wide {
		tbl.Columns = []TableColumn{{Title: "Name"}, {Title: "A", Width: 250}, {Title: "B", Width: 250},
			{Title: "C", Width: 250}}
	}
	return wheelView{ws: tbl, axis: geom.Pt(1, 0), run: run,
		to: func(share float32) { tbl.left = share * max(0, tbl.wide-tbl.width) }}
}

// crumbView stages an address bar width wide, showing cs.
func crumbView(t *testing.T, width float32, cs []Crumb) wheelView {
	a, _, run, _ := addressStage(t, width, cs)
	c := a.crumbs
	return wheelView{ws: c, axis: geom.Pt(1, 0), across: true, run: run,
		to: func(share float32) { c.shift.Jump(share * c.most) }}
}

// Each view that scrolls with the wheel says the wheel is its own while its content is larger than it along the
// wheel's axis, at its start, in the middle and at its end, either way; and says so of neither way while it fits.
func TestEachViewSaysWhetherTheWheelIsItsOwn(t *testing.T) {
	for _, c := range []struct {
		name string
		room bool
		mk   func(t *testing.T) wheelView
	}{
		{"Scroll with room", true, func(t *testing.T) wheelView {
			s := NewScroll(&block{h: 1000})
			_, run := stage(t, &frame{child: s, size: geom.Sz(300, 300)})
			return scrollingView(s, &s.scrolling, run)
		}},
		{"Scroll that fits", false, func(t *testing.T) wheelView {
			s := NewScroll(&block{h: 100})
			_, run := stage(t, &frame{child: s, size: geom.Sz(300, 300)})
			return scrollingView(s, &s.scrolling, run)
		}},
		{"VirtualList with room", true, func(t *testing.T) wheelView {
			_, l, run := newVirtual(t, keys(200), 40)
			return scrollingView(l, &l.scrolling, run)
		}},
		{"VirtualList that fits", false, func(t *testing.T) wheelView {
			_, l, run := newVirtual(t, keys(3), 40)
			return scrollingView(l, &l.scrolling, run)
		}},
		{"TileGrid with room", true, func(t *testing.T) wheelView {
			g, _, run := tileStage(t, 100)
			return scrollingView(g, &g.scrolling, run)
		}},
		{"TileGrid that fits", false, func(t *testing.T) wheelView {
			g, _, run := tileStage(t, 4)
			return scrollingView(g, &g.scrolling, run)
		}},
		{"Menu with room", true, func(t *testing.T) wheelView { return menuView(t, 80) }},
		{"Menu that fits", false, func(t *testing.T) wheelView { return menuView(t, 2) }},
		{"Table wider than it", true, func(t *testing.T) wheelView { return tableView(t, true) }},
		{"Table that fits", false, func(t *testing.T) wheelView { return tableView(t, false) }},
		{"places that overflow", true, func(t *testing.T) wheelView { return crumbView(t, 200, longCrumbs()) }},
		{"places that fit", false, func(t *testing.T) wheelView { return crumbView(t, 400, []Crumb{{"x", "/x"}}) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			v := c.mk(t)
			v.run(2)
			across := geom.Pt(v.axis.Y, v.axis.X)
			for _, share := range []float32{0, 0.5, 1} {
				v.to(share)
				v.run(2)
				for _, sign := range []float32{1, -1} {
					if got := v.ws.ScrollsAlong(v.axis.Mul(40 * sign)); got != c.room {
						t.Errorf("%v of the way along, the wheel turned by %v is the view's %v, want %v",
							share, v.axis.Mul(40*sign), got, c.room)
					}
					if got, want := v.ws.ScrollsAlong(across.Mul(40*sign)), v.across && c.room; got != want {
						t.Errorf("%v of the way along, the wheel turned by %v is the view's %v, want %v",
							share, across.Mul(40*sign), got, want)
					}
				}
			}
			if v.ws.ScrollsAlong(geom.Point{}) {
				t.Error("a wheel turned by nothing is the view's")
			}
		})
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
