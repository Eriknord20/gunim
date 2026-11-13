package touch

import (
	"time"

	"github.com/marrasen/gunim/geom"
)

// A Contact is one finger in a touch screen's report: Down as it lands,
// Move as it moves, Up as it lifts. ID is the finger's while it is down.
// Pos is in logical pixels.
type Contact struct {
	ID    int
	Pos   geom.Point
	Phase Action
}

// Contacts turns a touch screen's reports of every finger into calls on
// [Gestures], with the same choices Android's view makes from a
// MotionEvent: the first finger down drives the pointer; in a window that
// takes fingers each later one is a finger of its own; otherwise the
// second starts a pinch with the first, and later ones are left out.
type Contacts struct {
	G *Gestures

	// pointer is the finger that drives the pointer, or -1, and second
	// the one pinching with it while pinching.
	pointer, second int
	pinching        bool
	// down is where each finger down is, and own where each finger of its
	// own was sent last, so one that stays put sends no move.
	down map[int]geom.Point
	own  map[int]geom.Point
}

// NewContacts returns Contacts that call g.
func NewContacts(g *Gestures) *Contacts {
	return &Contacts{G: g, pointer: -1, second: -1, down: map[int]geom.Point{}, own: map[int]geom.Point{}}
}

// Down reports whether finger id is down.
func (c *Contacts) Down(id int) bool {
	_, ok := c.down[id]
	return ok
}

// Count returns how many fingers are down.
func (c *Contacts) Count() int { return len(c.down) }

// Report takes one report: the fingers that lifted, landed or moved since
// the last. Lifts go first, then landings, then the moves, as one move of
// every finger, as Android's view hands a move.
func (c *Contacts) Report(cs []Contact, now time.Time) {
	for _, k := range cs {
		if k.Phase == Up {
			c.up(k, now)
		}
	}
	for _, k := range cs {
		if k.Phase == Down {
			c.land(k, now)
		}
	}
	moved := false
	for _, k := range cs {
		if k.Phase == Move {
			if _, ok := c.down[k.ID]; ok {
				c.down[k.ID] = k.Pos
				moved = true
			}
		}
	}
	if moved {
		c.move(now)
	}
}

// land takes a finger landing.
func (c *Contacts) land(k Contact, now time.Time) {
	if _, ok := c.down[k.ID]; ok {
		// Its lift was lost: it lifts first.
		c.up(k, now)
	}
	first := len(c.down) == 0
	c.down[k.ID] = k.Pos
	if first {
		// The touch starts.
		c.pointer, c.second, c.pinching = k.ID, -1, false
		clear(c.own)
		if c.G.Touch(Down, k.ID, k.Pos, now) {
			c.own[k.ID] = k.Pos
		}
		return
	}
	if w := c.G.Touched(); w != nil && w.TakesFingers() && !c.pinching {
		// A finger of its own, while the first goes on as the pointer.
		c.own[k.ID] = k.Pos
		c.G.Finger(Down, k.ID, k.Pos, now)
		return
	}
	if c.pointer >= 0 && !c.pinching {
		c.second, c.pinching = k.ID, true
		c.G.Pinch(PinchStart, c.down[c.pointer], k.Pos, now)
	}
}

// move takes every finger's move.
func (c *Contacts) move(now time.Time) {
	for id, was := range c.own {
		if at := c.down[id]; at != was {
			c.own[id] = at
			c.G.Finger(Move, id, at, now)
		}
	}
	if c.pinching {
		c.G.Pinch(PinchMove, c.down[c.pointer], c.down[c.second], now)
		return
	}
	if at, ok := c.down[c.pointer]; ok {
		c.G.Touch(Move, c.pointer, at, now)
	}
}

// up takes a finger lifting.
func (c *Contacts) up(k Contact, now time.Time) {
	if _, ok := c.down[k.ID]; !ok {
		return
	}
	_, own := c.own[k.ID]
	if len(c.down) == 1 {
		// The last finger lifts: the touch ends, whatever it became, and
		// then a finger of its own lifts as itself.
		c.G.Touch(Up, k.ID, k.Pos, now)
		if own {
			c.G.Finger(Up, k.ID, k.Pos, now)
		}
		clear(c.down)
		clear(c.own)
		c.pointer, c.second, c.pinching = -1, -1, false
		return
	}
	switch {
	case c.pinching && (k.ID == c.pointer || k.ID == c.second):
		c.G.Pinch(PinchEnd, c.down[c.pointer], c.down[c.second], now)
		c.pinching = false
	case !c.pinching && k.ID == c.pointer:
		c.G.Touch(Up, k.ID, k.Pos, now)
		c.pointer = -1
	}
	// The pointer lets go before its finger lifts, so the node holding the
	// finger still holds it as the release passes.
	if own {
		delete(c.own, k.ID)
		c.G.Finger(Up, k.ID, k.Pos, now)
	}
	delete(c.down, k.ID)
}

// Cancel calls the touch off, as when the screen goes dark: the press lets
// go at [input.Away], and every finger of its own lifts there.
func (c *Contacts) Cancel(now time.Time) {
	if len(c.down) > 0 {
		c.G.Touch(Cancel, -1, geom.Point{}, now)
		c.G.Finger(Cancel, -1, geom.Point{}, now)
	}
	clear(c.down)
	clear(c.own)
	c.pointer, c.second, c.pinching = -1, -1, false
}
