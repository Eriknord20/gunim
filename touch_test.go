package gunim

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// dragsTouch is a recorder a finger drags.
type dragsTouch struct{ recorder }

func (*dragsTouch) DragsTouch() bool { return true }

// touchStage opens a window holding n, and returns a finger on it.
func touchStage(t *testing.T, n Node) (w *Window, finger func(ev any)) {
	t.Helper()
	w = newWindow(driver.Offscreen(geom.Sz(400, 400)), nil)
	w.ui.Insert(w.ui.Root(), n)
	w.Frame(time.Second / 60)
	return w, func(ev any) { w.Input(ev) }
}

// swipe presses a finger at from, moves it in steps every 16 ms to to,
// starting at at, and lifts it, the last move held still for rest.
func swipe(finger func(any), from, to geom.Point, steps int, at time.Time, rest time.Duration) {
	finger(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1, Touch: true, Time: at})
	for i := 1; i <= steps; i++ {
		at = at.Add(16 * time.Millisecond)
		p := from.Add(to.Sub(from).Mul(float32(i) / float32(steps)))
		finger(input.PointerMove{Pos: p, Touch: true, Time: at})
	}
	at = at.Add(rest)
	finger(input.PointerUp{Pos: to, Button: input.ButtonPrimary, Touch: true, Time: at})
}

// scrolled sums the scroll events r heard, and counts them.
func scrolled(r *recorder) (sum geom.Point, n int) {
	for _, e := range r.events {
		if s, ok := e.(input.Scroll); ok {
			sum, n = sum.Add(s.Delta), n+1
		}
	}
	return sum, n
}

func TestATapStaysAClick(t *testing.T) {
	r := &recorder{}
	_, finger := touchStage(t, r)
	swipe(finger, geom.Pt(100, 100), geom.Pt(104, 103), 2, time.Now(), 0)
	var up *input.PointerUp
	for _, e := range r.events {
		switch e := e.(type) {
		case input.PointerUp:
			up = &e
		case input.Scroll:
			t.Fatal("a finger that stayed within the slop scrolled")
		}
	}
	if up == nil || up.Pos.X < 0 {
		t.Fatalf("the press was let go at %v, want where the finger lifted", up)
	}
}

func TestAFingerMovedScrollsWhatItCameDownOn(t *testing.T) {
	r := &recorder{}
	_, finger := touchStage(t, r)
	swipe(finger, geom.Pt(100, 300), geom.Pt(100, 100), 10, time.Now(), 200*time.Millisecond)
	moves, ups := 0, 0
	for _, e := range r.events {
		switch e := e.(type) {
		case input.PointerMove:
			moves++
		case input.PointerUp:
			ups++
			if e.Pos != input.Away {
				t.Fatalf("the press was let go at %v, want far away, so nothing under it acts", e.Pos)
			}
		case input.Scroll:
			if e.Pos != geom.Pt(100, 300) {
				t.Fatalf("a scroll went to %v, want where the finger came down", e.Pos)
			}
		}
	}
	sum, n := scrolled(r)
	if n == 0 || sum.Y > -150 || sum.Y < -200 {
		t.Fatalf("the finger scrolled %v in %d steps, want up by the 200 it moved less the slop", sum, n)
	}
	if ups != 1 || moves > 1 {
		t.Fatalf("the node heard %d moves and %d releases, want the press let go once and no drag", moves, ups)
	}
}

func TestAFingersReleaseSaysAFingerLifted(t *testing.T) {
	for _, to := range []geom.Point{geom.Pt(102, 300), geom.Pt(100, 100)} {
		r := &recorder{}
		_, finger := touchStage(t, r)
		swipe(finger, geom.Pt(100, 300), to, 10, time.Now(), 0)
		ups := 0
		for _, e := range r.events {
			if e, ok := e.(input.PointerUp); ok {
				ups++
				if !e.Touch {
					t.Fatalf("a finger moved to %v let go at %v with Touch false", to, e.Pos)
				}
			}
		}
		if ups != 1 {
			t.Fatalf("a finger moved to %v let go %d times", to, ups)
		}
	}
}

func TestAFingerDragsWhatDragsByTouch(t *testing.T) {
	d := &dragsTouch{}
	_, finger := touchStage(t, d)
	swipe(finger, geom.Pt(100, 300), geom.Pt(100, 100), 10, time.Now(), 0)
	moves := 0
	for _, e := range d.events {
		switch e := e.(type) {
		case input.PointerMove:
			moves++
		case input.Scroll:
			t.Fatal("a finger on a touch dragger scrolled")
		case input.PointerUp:
			if e.Pos != geom.Pt(100, 100) {
				t.Fatalf("the drag ended at %v, want where the finger lifted", e.Pos)
			}
		}
	}
	if moves < 5 {
		t.Fatalf("the dragger heard %d moves, want the finger's moves past the slop", moves)
	}
}

func TestAFlickCoastsAndStops(t *testing.T) {
	r := &recorder{}
	w, finger := touchStage(t, r)
	swipe(finger, geom.Pt(100, 300), geom.Pt(100, 200), 5, time.Now(), 0)
	_, during := scrolled(r)
	for range 10 {
		w.Frame(time.Second / 60)
	}
	_, after := scrolled(r)
	if after <= during {
		t.Fatal("the scroll stopped as the finger lifted, want it to coast on")
	}
	for range 300 {
		w.Frame(time.Second / 60)
	}
	_, settled := scrolled(r)
	for range 30 {
		w.Frame(time.Second / 60)
	}
	if _, later := scrolled(r); later != settled {
		t.Fatal("the fling never stopped")
	}
	// A finger pressing again stops a fling at once.
	swipe(finger, geom.Pt(100, 300), geom.Pt(100, 200), 5, time.Now(), 0)
	finger(input.PointerDown{Pos: geom.Pt(50, 50), Button: input.ButtonPrimary, Clicks: 1, Touch: true, Time: time.Now()})
	_, pressed := scrolled(r)
	w.Frame(time.Second / 60)
	if _, next := scrolled(r); next != pressed {
		t.Fatal("the fling went on under a finger pressing again")
	}
}

func TestAFingerHeldStillIsALongPress(t *testing.T) {
	r := &recorder{}
	w, finger := touchStage(t, r)
	at := time.Now()
	finger(input.PointerDown{Pos: geom.Pt(100, 100), Button: input.ButtonPrimary, Clicks: 1, Touch: true, Time: at})
	finger(input.PointerMove{Pos: geom.Pt(103, 102), Touch: true, Time: at.Add(100 * time.Millisecond)})
	for range 40 {
		w.Frame(time.Second / 60)
	}
	var downs []input.PointerDown
	for _, e := range r.events {
		if d, ok := e.(input.PointerDown); ok {
			downs = append(downs, d)
		}
	}
	if len(downs) != 2 || downs[1].Button != input.ButtonSecondary || downs[1].Pos != geom.Pt(100, 100) {
		t.Fatalf("a finger held still pressed %+v, want the secondary button after the primary", downs)
	}
	// Moves go on with the button held, and the lift lets it go.
	finger(input.PointerMove{Pos: geom.Pt(160, 100), Touch: true, Time: time.Now()})
	finger(input.PointerUp{Pos: geom.Pt(160, 100), Button: input.ButtonPrimary, Touch: true, Time: time.Now()})
	var moved, up bool
	for _, e := range r.events {
		switch e := e.(type) {
		case input.PointerMove:
			moved = moved || e.Pos == geom.Pt(160, 100)
		case input.PointerUp:
			up = up || e.Button == input.ButtonSecondary && e.Pos == geom.Pt(160, 100)
		case input.Scroll:
			t.Fatal("a long press scrolled")
		}
	}
	if !moved || !up {
		t.Fatalf("after the long press the node heard the move %v and the release %v, want both", moved, up)
	}
}

func TestAFingerHoldingADragStillStaysADrag(t *testing.T) {
	// A handle or a thumb held still is a drag paused, and stays one.
	d := &dragsTouch{}
	w, finger := touchStage(t, d)
	finger(input.PointerDown{Pos: geom.Pt(100, 100), Button: input.ButtonPrimary, Clicks: 1, Touch: true, Time: time.Now()})
	for range 40 {
		w.Frame(time.Second / 60)
	}
	for _, e := range d.events {
		if p, ok := e.(input.PointerDown); ok && p.Button == input.ButtonSecondary {
			t.Fatal("a finger holding a drag still became a long press")
		}
	}
}

// button is a focusable node that takes no text, as a button is.
type button struct {
	recorder
	size geom.Size
}

func (b *button) Layout(Constraints, Frame, Children) geom.Size { return b.size }
func (*button) Focusable() bool                                 { return true }

// stack lays its children out top to bottom.
type stack struct{ kids []Node }

func (s *stack) Children() []Node { return s.kids }
func (s *stack) Layout(c Constraints, _ Frame, kids Children) geom.Size {
	var y float32
	for k := range kids.All {
		sz := k.Layout(Loose(c.Max))
		k.Place(geom.Pt(0, y))
		y += sz.H
	}
	return c.Max
}
func (s *stack) Paint(p *paint.Painter, _ Frame, _ geom.Size, kids Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

func TestAFingerMovesFocusOnlyToTextItTaps(t *testing.T) {
	// field at the top, a button under it, and another field under that.
	field := &box{size: geom.Sz(400, 100)}
	btn := &button{size: geom.Sz(400, 100)}
	other := &box{size: geom.Sz(400, 100)}
	w, finger := touchStage(t, &stack{kids: []Node{field, btn, other}})
	w.ui.Focus(field)
	tap := func(p geom.Point) {
		finger(input.PointerDown{Pos: p, Button: input.ButtonPrimary, Clicks: 1, Touch: true, Time: time.Now()})
		finger(input.PointerUp{Pos: p, Button: input.ButtonPrimary, Touch: true, Time: time.Now()})
	}
	tap(geom.Pt(50, 150))
	if w.ui.Focused() != field {
		t.Fatalf("a tap on a button took the focus to %v, want it left in the field", w.ui.Focused())
	}
	swipe(finger, geom.Pt(50, 250), geom.Pt(50, 120), 5, time.Now(), 0)
	if w.ui.Focused() != field {
		t.Fatal("a finger scrolling moved the focus")
	}
	finger(input.PointerDown{Pos: geom.Pt(50, 250), Button: input.ButtonPrimary, Clicks: 1, Touch: true, Time: time.Now()})
	if w.ui.Focused() != field {
		t.Fatal("the focus moved as the finger came down, before it showed a tap")
	}
	finger(input.PointerUp{Pos: geom.Pt(50, 250), Button: input.ButtonPrimary, Touch: true, Time: time.Now()})
	if w.ui.Focused() != other {
		t.Fatalf("a tap on the other field left the focus at %v, want the other field", w.ui.Focused())
	}
	// A mouse moves focus as ever.
	finger(input.PointerDown{Pos: geom.Pt(50, 150), Button: input.ButtonPrimary, Clicks: 1})
	if w.ui.Focused() != btn {
		t.Fatalf("a click on the button left the focus at %v, want the button", w.ui.Focused())
	}
}

func TestAFingerLightsNothingAsItPasses(t *testing.T) {
	r := &recorder{}
	w, finger := touchStage(t, r)
	finger(input.PointerMove{Pos: geom.Pt(100, 100), Touch: true, Time: time.Now()})
	swipe(finger, geom.Pt(100, 300), geom.Pt(100, 100), 5, time.Now(), 0)
	for range 5 {
		w.Frame(time.Second / 60)
	}
	for _, e := range r.events {
		if _, ok := e.(input.PointerEnter); ok {
			t.Fatal("a finger lit the node it passed over, as a mouse's hover does")
		}
	}
	// A mouse lights it as ever.
	finger(input.PointerMove{Pos: geom.Pt(100, 100), Time: time.Now()})
	if !r.got(input.PointerEnter{}) {
		t.Fatal("the mouse moving over the node did not light it")
	}
}

func TestAFingersScrollSaysAFingerScrolled(t *testing.T) {
	// A slow drag, and a quick one that flings on after the finger lifts.
	for _, rest := range []time.Duration{200 * time.Millisecond, 0} {
		r := &recorder{}
		ui, finger := touchStage(t, r)
		swipe(finger, geom.Pt(100, 300), geom.Pt(100, 100), 10, time.Now(), rest)
		for range 30 {
			ui.Frame(16 * time.Millisecond)
		}
		_, n := scrolled(r)
		if n == 0 {
			t.Fatal("the finger scrolled nothing")
		}
		for _, e := range r.events {
			if s, ok := e.(input.Scroll); ok && !s.Touch {
				t.Fatalf("a finger's scroll (rest %v) came with Touch false: %+v", rest, s)
			}
		}
	}
}

// As a finger lifts, a busy browser hands over its last moves together, the last a
// pixel or two back the way it came. That is wobble, and the fling goes on the way
// the finger went.
func TestAWobbleAsAFingerLiftsTurnsNoFlingBack(t *testing.T) {
	r := &recorder{}
	w, finger := touchStage(t, r)
	at := time.Now()
	finger(input.PointerDown{Pos: geom.Pt(100, 300), Button: input.ButtonPrimary, Clicks: 1, Touch: true, Time: at})
	y := float32(300)
	for range 10 {
		at = at.Add(16 * time.Millisecond)
		y -= 20
		finger(input.PointerMove{Pos: geom.Pt(100, y), Touch: true, Time: at})
	}
	for _, step := range []struct {
		back float32
		gap  time.Duration
	}{{3, 100 * time.Microsecond}, {2, 100 * time.Microsecond}} {
		at = at.Add(step.gap)
		y += step.back
		finger(input.PointerMove{Pos: geom.Pt(100, y), Touch: true, Time: at})
	}
	finger(input.PointerUp{Pos: geom.Pt(100, y), Button: input.ButtonPrimary, Touch: true, Time: at})
	r.events = nil
	for f := range 30 {
		w.Frame(time.Second / 60)
		for _, e := range r.events {
			if s, ok := e.(input.Scroll); ok && s.Delta.Y > 0 {
				t.Fatalf("frame %d: a fling up went back down by %v", f, s.Delta.Y)
			}
		}
		r.events = nil
	}
}
