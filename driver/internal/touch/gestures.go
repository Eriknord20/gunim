// Package touch turns a touch screen's fingers into the engine's input,
// for the drivers with no window system that read fingers themselves:
// driver/android and driver/kms. Both hand it the same calls, so taps,
// scrolls, flings, long presses and pinches behave the same on both.
//
// [Gestures] is the shared core: the first finger drives the pointer as a
// touch, a second starts a pinch, and in a window that takes fingers each
// finger is an [input.Finger] too. Android's view decides which finger is
// which and calls it directly. [Contacts] makes the same decisions from a
// touch screen's reports of every finger, as driver/kms reads them, and
// [Resampler] smooths a finger's moves to one at each frame.
//
// Nothing here locks. The driver calls it with its own lock held, so a
// touch and a pinch reach a window's queue in the order they happened.
package touch

import (
	"math"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// A Window is a driver's window as the gestures see it.
type Window interface {
	// Push queues an input event for the window.
	Push(ev any)
	// Origin is where the window's top left lies, in the logical pixels
	// the gestures are handed.
	Origin() geom.Point
	// TakesFingers is what the window last said through
	// [driver.FingerTaker].
	TakesFingers() bool
}

// Action is what a finger did.
type Action int

// The actions, in the order Android's view numbers them.
const (
	Down Action = iota
	Move
	Up
	Cancel
)

// PinchAction is what a pinch did.
type PinchAction int

// The pinch actions, in the order Android's view numbers them.
const (
	PinchStart PinchAction = iota
	PinchMove
	PinchEnd
)

// The double tap's reach: two presses this close in time and space are a
// double tap.
const (
	DoubleTapTime  = 300 * time.Millisecond
	DoubleTapSpace = 24 // logical pixels
)

// Gestures is a touch screen's touch as it goes.
type Gestures struct {
	// Hit returns the topmost window at p that takes the pointer, or nil.
	Hit func(p geom.Point) Window
	// Started, if set, hears the window a touch starts in, nil for none,
	// once the touch holds it: Android tells its view whether that window
	// takes fingers.
	Started func(w Window)

	// touched is the window the touch in progress started in, which
	// keeps the touch until it lifts.
	touched Window
	// fingers is the window each finger of its own came down in, by id.
	fingers map[int]Window
	// pinched says a second finger came down: the touch is a pinch from
	// then on, and the finger left after it only lifts. pinching says the
	// two fingers are down now. spread is how far apart they were last,
	// and mid the point between them.
	pinched, pinching bool
	spread            float32
	mid               geom.Point
	// lastTap and lastAt are the last press, for a double tap.
	lastTap geom.Point
	lastAt  time.Time
	clicks  int
}

// Touched returns the window the touch in progress started in, or nil.
func (g *Gestures) Touched() Window { return g.touched }

// Pinching reports whether two fingers pinch now.
func (g *Gestures) Pinching() bool { return g.pinching }

// Forget lets go of a window that closed.
func (g *Gestures) Forget(w Window) {
	if g.touched == w {
		g.touched = nil
	}
	for id, x := range g.fingers {
		if x == w {
			delete(g.fingers, id)
		}
	}
}

// Touch turns the first finger into the pointer: it presses, moves and
// lets go as a mouse's primary button does, marked as touch, and leaves the
// window as it lifts, since a finger has no hover. The engine works out
// from what the finger does and what it pressed whether it taps, drags,
// scrolls or long presses. at is in logical pixels.
//
// In a window that takes fingers the first finger is a finger of its own
// as well: as it comes down, its [input.Finger] goes ahead of the press, so
// the node that takes it holds it as the press arrives, and Touch reports
// true, for the caller to send the rest of it through [Gestures.Finger].
// id is the finger's.
func (g *Gestures) Touch(a Action, id int, at geom.Point, now time.Time) (own bool) {
	w := g.touched
	if a == Down {
		// A finger of its own still down from a touch before had its lift
		// lost; it lifts now, so no node waits on it.
		g.LiftFingers(now)
		w = g.Hit(at)
		g.touched = w
		own = w != nil && w.TakesFingers()
		if g.Started != nil {
			g.Started(w)
		}
		g.pinched, g.pinching, g.spread, g.mid = false, false, 0, geom.Point{}
		if now.Sub(g.lastAt) < DoubleTapTime && abs(at.X-g.lastTap.X) < DoubleTapSpace && abs(at.Y-g.lastTap.Y) < DoubleTapSpace {
			g.clicks++
		} else {
			g.clicks = 1
		}
		g.lastTap, g.lastAt = at, now
	}
	if a == Up || a == Cancel {
		g.touched = nil
	}
	if w == nil {
		return false
	}
	pos := at.Sub(w.Origin())
	if g.pinched && a != Down {
		// After a pinch the finger only lifts.
		if a == Up || a == Cancel {
			g.endPinch(w, now)
			w.Push(input.PointerLeave{Time: now})
		}
		return false
	}
	switch a {
	case Down:
		if own {
			if g.fingers == nil {
				g.fingers = map[int]Window{}
			}
			g.fingers[id] = w
			w.Push(input.Finger{ID: id, Pos: pos, Phase: input.FingerDown, Time: now})
		}
		w.Push(input.PointerMove{Pos: pos, Touch: true, Time: now})
		w.Push(input.PointerDown{Pos: pos, Button: input.ButtonPrimary, Clicks: g.clicks, Touch: true, Time: now})
	case Move:
		w.Push(input.PointerMove{Pos: pos, Touch: true, Time: now})
	case Up:
		w.Push(input.PointerUp{Pos: pos, Button: input.ButtonPrimary, Touch: true, Time: now})
		w.Push(input.PointerLeave{Time: now})
	case Cancel:
		w.Push(input.PointerUp{Pos: input.Away, Button: input.ButtonPrimary, Touch: true, Time: now})
		w.Push(input.PointerLeave{Time: now})
	}
	return own
}

// Finger turns a finger of its own, in a touch whose window takes
// fingers, into [input.Finger] for the window it came down in, wherever it
// goes after. A finger beyond the first comes down here; the first comes
// down through [Gestures.Touch], and moves and lifts here. A Cancel lifts
// every finger at [input.Away].
func (g *Gestures) Finger(a Action, id int, at geom.Point, now time.Time) {
	if a == Cancel {
		g.LiftFingers(now)
		return
	}
	w := g.fingers[id]
	phase := input.FingerMove
	switch a {
	case Down:
		// The id is free again once its finger lifted, so a finger still
		// held under it had its lift lost: it lifts first.
		if w != nil {
			w.Push(input.Finger{ID: id, Pos: input.Away, Phase: input.FingerUp, Time: now})
			delete(g.fingers, id)
		}
		if w = g.Hit(at); w == nil {
			return
		}
		if g.fingers == nil {
			g.fingers = map[int]Window{}
		}
		g.fingers[id] = w
		phase = input.FingerDown
	case Up:
		delete(g.fingers, id)
		phase = input.FingerUp
	case Move, Cancel:
	}
	if w == nil {
		return
	}
	w.Push(input.Finger{ID: id, Pos: at.Sub(w.Origin()), Phase: phase, Time: now})
}

// LiftFingers lifts every finger of its own still down, at [input.Away].
func (g *Gestures) LiftFingers(now time.Time) {
	for id, w := range g.fingers {
		w.Push(input.Finger{ID: id, Pos: input.Away, Phase: input.FingerUp, Time: now})
	}
	clear(g.fingers)
}

// Pinch turns two fingers into a pinch. As the second finger comes down,
// the first one's press is let go at [input.Away], so it neither clicks
// nor scrolls nor opens a menu. The window the touch started in hears
// [input.Pinch] as they come down, as they spread, close or move, and as
// one of them lifts, at the point between them: the engine gives it to a
// node that zooms with a pinch, and otherwise makes it Ctrl with the wheel.
// a and b are the two fingers, in logical pixels.
func (g *Gestures) Pinch(act PinchAction, a, b geom.Point, now time.Time) {
	w := g.touched
	if w == nil {
		return
	}
	spread := float32(math.Hypot(float64(a.X-b.X), float64(a.Y-b.Y)))
	mid := a.Add(b).Mul(0.5)
	origin := w.Origin()
	switch act {
	case PinchStart:
		if !g.pinched {
			// The first finger's press, whatever it became, is called off.
			w.Push(input.PointerUp{Pos: input.Away, Button: input.ButtonPrimary, Touch: true, Time: now})
		}
		g.endPinch(w, now)
		g.pinched, g.pinching, g.spread, g.mid = true, true, spread, mid
		w.Push(input.Pinch{Pos: mid.Sub(origin), Scale: 1, Phase: input.PinchStart, Time: now})
	case PinchMove:
		if !g.pinching {
			return
		}
		scale := float32(1)
		if g.spread > 0 && spread > 0 {
			scale = spread / g.spread
		}
		delta := mid.Sub(g.mid)
		g.spread, g.mid = spread, mid
		w.Push(input.Pinch{Pos: mid.Sub(origin), Delta: delta, Scale: scale, Phase: input.PinchMove, Time: now})
	case PinchEnd:
		g.endPinch(w, now)
	}
}

// endPinch ends the pinch going on in w, if there is one, where the
// fingers last were.
func (g *Gestures) endPinch(w Window, now time.Time) {
	if !g.pinching {
		return
	}
	g.pinching = false
	w.Push(input.Pinch{Pos: g.mid.Sub(w.Origin()), Scale: 1, Phase: input.PinchEnd, Time: now})
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
