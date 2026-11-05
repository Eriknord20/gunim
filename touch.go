package gunim

import (
	"math"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// A TouchDragger is a node a finger drags, as a slider's thumb or a
// plot that pans does. A finger that presses one and moves goes on
// moving the pointer, as a mouse does, and a finger holding one still
// goes on holding it. A finger that presses anything else and moves
// scrolls what it came down on.
type TouchDragger interface {
	Node
	DragsTouch() bool
}

// Touch slop, long press and fling, in logical pixels and seconds. A
// finger that moves further than touchSlop from where it went down has
// chosen between scrolling and dragging, and one held within it for
// longPress is a long press. A fling slows by e every flingDecay
// seconds, and stops below flingStop pixels a second.
const (
	touchSlop  = 8
	longPress  = 400 * time.Millisecond
	flingDecay = 0.33
	flingStop  = 20
	// speedSpan is the shortest time a finger's speed is read over. Moves
	// closer than this count together: a pixel of wobble as a finger lifts,
	// over a tenth of a millisecond, would read as a fling the other way.
	speedSpan = 4 * time.Millisecond
)

// touchPress follows a finger's press in a window: where it went down,
// whether its moves have chosen yet, whether it became a long press,
// and, for a scroll, where it was last, how fast it moves, in logical
// pixels a second, and where and when that speed was last read. stopLong
// stops the wait for a long press.
type touchPress struct {
	root        *state
	start, last geom.Point
	lastAt      time.Time
	chosen      bool
	scrolling   bool
	held        bool
	vel         geom.Point
	readAt      geom.Point
	readTime    time.Time
	stopLong    func()
}

// touchFling is a scroll coasting on after the finger lifted.
type touchFling struct {
	root *state
	at   geom.Point
	vel  geom.Point
}

// touchEvent takes the events of a finger pressing with the primary
// button, and reports whether it dealt with ev itself; what it makes of
// them goes to handleRaw.
//
// Moves within the slop are held: the finger has not shown yet whether
// it drags or scrolls. Past it, a press on a [TouchDragger] goes on as
// a mouse's does. Any other press is let go at [input.Away], so a button
// under the finger does nothing, and each move after scrolls what the
// finger came down on by as far as the finger went, as a wheel would. A
// finger that lifts while scrolling flings: the scroll coasts on,
// slowing, over the frames that follow.
//
// A finger's press moves focus only as it lifts from a tap, and then only
// to a node that takes text, as on a phone: a finger that scrolls, or
// taps a button, leaves the keyboard in the text being written.
//
// A finger held within the slop for longPress, on anything but a
// TouchDragger dragging, is a long press: its press is let go at Away
// and the secondary button goes down where it is, as for a right click,
// which opens a context menu; the device buzzes. The finger's moves go
// on as moves with the button held, as a text field takes to choose
// more words, and the button comes up as the finger lifts.
func (u *UI) touchEvent(root *state, ev any) bool {
	switch e := ev.(type) {
	case input.PointerDown:
		u.fling = nil
		u.endTouch()
		if e.Touch && e.Button == input.ButtonPrimary {
			t := &touchPress{root: root, start: e.Pos, last: e.Pos, lastAt: e.Time}
			t.stopLong = u.After(longPress, func(u *UI) { u.longPress(t) })
			u.touch = t
		}
	case input.PointerMove:
		t := u.touch
		if !e.Touch || t == nil || t.root != root || t.held {
			return false
		}
		if !t.chosen {
			d := e.Pos.Sub(t.start)
			if math.Hypot(float64(d.X), float64(d.Y)) <= touchSlop {
				return true
			}
			t.chosen = true
			t.stopLong()
			if u.dragsTouch() {
				return false
			}
			t.scrolling = true
			u.handleRaw(root, input.PointerUp{Pos: input.Away, Button: input.ButtonPrimary, Mods: e.Mods, Touch: true, Time: e.Time})
			// The scroll starts from here, with no jump for the slop.
			t.last, t.lastAt = e.Pos, e.Time
			t.readAt, t.readTime = e.Pos, e.Time
			return true
		}
		if !t.scrolling {
			return false
		}
		delta := e.Pos.Sub(t.last)
		if dt := e.Time.Sub(t.readTime); dt >= speedSpan {
			t.vel = t.vel.Mul(0.6).Add(e.Pos.Sub(t.readAt).Mul(float32(0.4 / dt.Seconds())))
			t.readAt, t.readTime = e.Pos, e.Time
		}
		t.last, t.lastAt = e.Pos, e.Time
		u.handleRaw(root, input.Scroll{Pos: t.start, Delta: delta, Mods: e.Mods, Touch: true, Time: e.Time})
		return true
	case input.PointerUp:
		t := u.touch
		if t == nil || t.root != root || e.Button != input.ButtonPrimary {
			return false
		}
		u.endTouch()
		switch {
		case !e.Touch:
			return false
		case !t.scrolling && !t.held && e.Pos != input.Away:
			// A tap: focus moves now, where the finger came down, to text
			// it pressed.
			if root == u.root {
				u.focusAt(t.start, true)
			}
			return false
		case t.held:
			u.handleRaw(root, input.PointerUp{Pos: e.Pos, Button: input.ButtonSecondary, Mods: e.Mods, Touch: true, Time: e.Time})
			return true
		case !t.scrolling:
			return false
		}
		speed := math.Hypot(float64(t.vel.X), float64(t.vel.Y))
		if e.Pos != input.Away && e.Time.Sub(t.lastAt) < 100*time.Millisecond && speed > flingStop {
			u.fling = &touchFling{root: root, at: t.start, vel: t.vel}
			u.invalid = true
		}
		return true
	}
	return false
}

// endTouch forgets the finger pressing, and stops its wait for a long
// press.
func (u *UI) endTouch() {
	if t := u.touch; t != nil {
		t.stopLong()
		u.touch = nil
	}
}

// dragsTouch reports whether the node the press went to drags by touch
// now.
func (u *UI) dragsTouch() bool {
	if c := u.capture; c != nil {
		if d, ok := c.node.(TouchDragger); ok && d.DragsTouch() {
			return true
		}
	}
	return false
}

// longPress turns t into a long press, if the finger still presses
// within the slop and what it pressed is not dragging.
func (u *UI) longPress(t *touchPress) {
	if u.touch != t || t.chosen || u.dragsTouch() {
		return
	}
	t.chosen, t.held = true, true
	now := u.timerClock()
	u.handleRaw(t.root, input.PointerUp{Pos: input.Away, Button: input.ButtonPrimary, Touch: true, Time: now})
	u.handleRaw(t.root, input.PointerDown{Pos: t.start, Button: input.ButtonSecondary, Clicks: 1, Touch: true, Time: now})
	if b, ok := u.windowOf(t.root).(driver.Buzzer); ok {
		b.Buzz()
	}
}

// stepFling scrolls a fling on by dt, slowing it, and reports whether it
// coasts on.
func (u *UI) stepFling(dt time.Duration) bool {
	f := u.fling
	if f == nil || dt <= 0 {
		return f != nil
	}
	s := dt.Seconds()
	f.vel = f.vel.Mul(float32(math.Exp(-s / flingDecay)))
	if math.Hypot(float64(f.vel.X), float64(f.vel.Y)) < flingStop {
		u.fling = nil
		return false
	}
	u.handleRaw(f.root, input.Scroll{Pos: f.at, Delta: f.vel.Mul(float32(s)), Touch: true, Time: u.now})
	return true
}
