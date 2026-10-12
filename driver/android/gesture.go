//go:build android

package android

import (
	"math"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// gesture is a touch as it goes. pinched says a second finger came
// down: the touch is a pinch from then on, and the finger left after it
// only lifts. pinching says the two fingers are down now. spread is how
// far apart they were last, and mid the point between them, in logical
// pixels.
type gesture struct {
	pinched  bool
	pinching bool
	spread   float32
	mid      geom.Point
}

// touch turns the first finger into the pointer: it presses, moves and
// lets go as a mouse's primary button does, marked as touch, and leaves
// the window as it lifts, since a finger has no hover. The engine works
// out from what the finger does and what it pressed whether it taps,
// drags, scrolls or long presses. x and y are in device pixels.
//
// In a window that takes fingers the first finger is a finger of its
// own as well: as it comes down, its [input.Finger] goes ahead of the
// press, so the node that takes it holds it as the press arrives, and
// touch reports true, for the view to send the rest of it through
// finger. id is the finger's.
//
// The events go to the window's queue with the driver's lock held, so a
// touch and a pinch reach the engine in the order they happened.
func (d *Driver) touch(action, id int, x, y float32, now time.Time) (own bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// The windows are drawn slid up by the pan, so the point touched
	// is that much further down them.
	at := geom.Pt(x/d.density, (y+float32(math.Round(d.pan)))/d.density)
	g := &d.gesture
	w := d.touched
	if action == touchDown {
		// A finger of its own still down from a touch before had its
		// lift lost; it lifts now, so no node waits on it.
		d.liftFingersLocked(now)
		w = d.hitLocked(at)
		d.touched = w
		own = w != nil && w.takesFingers
		d.takeFingersLocked(own)
		d.keyed, d.refit = false, false
		*g = gesture{}
		if now.Sub(d.lastAt) < doubleTapTime && abs(at.X-d.lastTap.X) < doubleTapSpace && abs(at.Y-d.lastTap.Y) < doubleTapSpace {
			d.clicks++
		} else {
			d.clicks = 1
		}
		d.lastTap, d.lastAt = at, now
	}
	if action == touchUp || action == touchCancel {
		d.touched = nil
	}
	if w == nil {
		return false
	}
	pos := at.Sub(w.pos)
	if g.pinched && action != touchDown {
		// After a pinch the finger only lifts.
		if action == touchUp || action == touchCancel {
			d.endPinchLocked(w, now)
			w.in.Push(input.PointerLeave{Time: now})
		}
		return false
	}
	switch action {
	case touchDown:
		if own {
			if d.fingers == nil {
				d.fingers = map[int]*Window{}
			}
			d.fingers[id] = w
			w.in.Push(input.Finger{ID: id, Pos: pos, Phase: input.FingerDown, Time: now})
		}
		w.in.Push(input.PointerMove{Pos: pos, Touch: true, Time: now})
		w.in.Push(input.PointerDown{Pos: pos, Button: input.ButtonPrimary, Clicks: d.clicks, Touch: true, Time: now})
	case touchMove:
		w.in.Push(input.PointerMove{Pos: pos, Touch: true, Time: now})
	case touchUp:
		w.in.Push(input.PointerUp{Pos: pos, Button: input.ButtonPrimary, Touch: true, Time: now})
		w.in.Push(input.PointerLeave{Time: now})
	case touchCancel:
		w.in.Push(input.PointerUp{Pos: input.Away, Button: input.ButtonPrimary, Touch: true, Time: now})
		w.in.Push(input.PointerLeave{Time: now})
	}
	return own
}

// finger turns a finger of its own, in a touch whose window takes
// fingers, into [input.Finger] for the window it came down in, wherever
// it goes after. A finger beyond the first comes down here; the first
// comes down through touch, and moves and lifts here. action is a
// touch's; a cancel lifts every finger at [input.Away]. x and y are in
// device pixels.
func (d *Driver) finger(action, id int, x, y float32, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if action == touchCancel {
		d.liftFingersLocked(now)
		return
	}
	at := geom.Pt(x/d.density, (y+float32(math.Round(d.pan)))/d.density)
	w := d.fingers[id]
	phase := input.FingerMove
	switch action {
	case touchDown:
		// The id is free again once its finger lifted, so a finger still
		// held under it had its lift lost: it lifts first.
		if w != nil {
			w.in.Push(input.Finger{ID: id, Pos: input.Away, Phase: input.FingerUp, Time: now})
			delete(d.fingers, id)
		}
		if w = d.hitLocked(at); w == nil {
			return
		}
		if d.fingers == nil {
			d.fingers = map[int]*Window{}
		}
		d.fingers[id] = w
		phase = input.FingerDown
	case touchUp:
		delete(d.fingers, id)
		phase = input.FingerUp
	}
	if w == nil {
		return
	}
	w.in.Push(input.Finger{ID: id, Pos: at.Sub(w.pos), Phase: phase, Time: now})
}

// liftFingersLocked lifts every finger of its own still down at
// [input.Away].
func (d *Driver) liftFingersLocked(now time.Time) {
	for id, w := range d.fingers {
		w.in.Push(input.Finger{ID: id, Pos: input.Away, Phase: input.FingerUp, Time: now})
	}
	clear(d.fingers)
}

// takeFingersLocked tells the view whether a touch's fingers are
// fingers of their own, when that changes.
func (d *Driver) takeFingersLocked(on bool) {
	if on == d.fingersOn {
		return
	}
	d.fingersOn = on
	takeFingers(on)
}

// Pinch actions, as GunimView sends them.
const (
	pinchStart = iota
	pinchMove
	pinchEnd
)

// pinch turns two fingers into a pinch. As the second finger comes
// down, the first one's press is let go at [input.Away], so it neither
// clicks nor scrolls nor opens a menu. The window under the fingers
// hears [input.Pinch] as they come down, as they spread, close or move,
// and as one of them lifts, at the point between them: the engine gives
// it to a node that zooms with a pinch, and otherwise makes it Ctrl with
// the wheel. x0, y0, x1 and y1 are the fingers, in device pixels.
func (d *Driver) pinch(action int, x0, y0, x1, y1 float32, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	g := &d.gesture
	w := d.touched
	if w == nil {
		return
	}
	pan := float32(math.Round(d.pan))
	a := geom.Pt(x0/d.density, (y0+pan)/d.density)
	b := geom.Pt(x1/d.density, (y1+pan)/d.density)
	spread := float32(math.Hypot(float64(a.X-b.X), float64(a.Y-b.Y)))
	mid := a.Add(b).Mul(0.5)
	switch action {
	case pinchStart:
		if !g.pinched {
			// The first finger's press, whatever it became, is called
			// off.
			w.in.Push(input.PointerUp{Pos: input.Away, Button: input.ButtonPrimary, Touch: true, Time: now})
		}
		d.endPinchLocked(w, now)
		g.pinched, g.pinching, g.spread, g.mid = true, true, spread, mid
		w.in.Push(input.Pinch{Pos: mid.Sub(w.pos), Scale: 1, Phase: input.PinchStart, Time: now})
	case pinchMove:
		if !g.pinching {
			return
		}
		scale := float32(1)
		if g.spread > 0 && spread > 0 {
			scale = spread / g.spread
		}
		delta := mid.Sub(g.mid)
		g.spread, g.mid = spread, mid
		w.in.Push(input.Pinch{Pos: mid.Sub(w.pos), Delta: delta, Scale: scale, Phase: input.PinchMove, Time: now})
	case pinchEnd:
		d.endPinchLocked(w, now)
	}
}

// endPinchLocked ends the pinch going on in w, if there is one, where
// the fingers last were.
func (d *Driver) endPinchLocked(w *Window, now time.Time) {
	g := &d.gesture
	if !g.pinching {
		return
	}
	g.pinching = false
	w.in.Push(input.Pinch{Pos: g.mid.Sub(w.pos), Scale: 1, Phase: input.PinchEnd, Time: now})
}
