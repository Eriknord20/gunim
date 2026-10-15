package gunim

import (
	"fmt"
	"os"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// A drag held near the edge of a scroll view scrolls it, so a row can
// be dragged to a place in a list longer than the view, or a picture
// dropped into a list at a place out of sight.
//
// Each frame a drag is held, the engine asks the scroll views around
// it to scroll, by how near the pointer is to their edges. The content
// then moves under a pointer that stands still, so the engine replays
// the pointer where it is, with the keys held: the node holding the
// drag hears it move, and a drag from [UI.StartDrag] lands on whatever
// is now under it.

// A DragHolder is a node that drags something while it holds the
// pointer, such as a list whose rows the pointer drags into order.
// While DragHeld reports true, scroll views around it scroll when the
// pointer nears their edges, and it hears the pointer move, where it
// stands, as the content moves under it.
type DragHolder interface {
	Node
	DragHeld() bool
}

// An EdgeScroller is a node that scrolls when a drag nears its edges,
// as a scroll view does. Each frame a drag is held, the engine calls
// EdgeScroll on the EdgeScrollers around it, innermost first, until
// one scrolls. p is the pointer, in the node's space, and dt the time
// the frame covers. It returns how far its content moved, in its own
// space, and zero when it stays put.
type EdgeScroller interface {
	Node
	EdgeScroll(p geom.Point, dt time.Duration, u *UI) geom.Point
}

// EdgeZone is how near an edge of a scroll view a held drag starts to
// scroll it, in logical pixels. A view less than four zones long has a
// quarter of its length at each end.
const EdgeZone = 48

// The speeds of a scroll by a held drag, in logical pixels a second:
// edgeSlow as the drag enters the zone, rising evenly to edgeFast at
// the edge, and on to edgeFastest two zones past it.
const (
	edgeSlow    = 150
	edgeFast    = 800
	edgeFastest = 3200
)

// EdgeSpeed returns how fast a drag held at p scrolls a view whose
// content shows from lo to hi along one axis, in logical pixels a
// second: below zero toward lo, above zero toward hi, and zero away
// from both edges. A drag past an edge, outside the view, scrolls
// faster the further past it is.
func EdgeSpeed(p, lo, hi float32) float32 {
	zone := min(EdgeZone, (hi-lo)/4)
	if zone <= 0 {
		return 0
	}
	switch {
	case p < lo+zone:
		return -edgeRamp((lo + zone - p) / zone)
	case p > hi-zone:
		return edgeRamp((p - hi + zone) / zone)
	}
	return 0
}

// edgeRamp is the speed of a drag t zones on from where the zone at an
// edge starts: t is 1 at the edge, and 3 two zones past it.
func edgeRamp(t float32) float32 {
	if t <= 1 {
		return edgeSlow + (edgeFast-edgeSlow)*t
	}
	return edgeFast + (edgeFastest-edgeFast)*min(t-1, 2)/2
}

// edgeScroll scrolls what a held drag is near the edge of, for a frame
// of dt, and replays the pointer when the content moved. It reports
// whether anything scrolled.
func (u *UI) edgeScroll(dt time.Duration) bool {
	var from *state
	var at geom.Point
	over := false
	switch {
	case u.capture != nil && u.drag == nil && holdsDrag(u.capture):
		from, at = u.capture, u.pointer
	case u.dragOver:
		from, at, over = u.hit(u.root, u.dragOverAt), u.dragOverAt, true
	default:
		u.edgeLast = nil
		return false
	}
	for _, s := range u.edgeScrollers(from, at, over) {
		e, ok := s.node.(EdgeScroller)
		if !ok {
			continue
		}
		var moved geom.Point
		u.on(s, func() { moved = e.EdgeScroll(u.local(s, at), dt, u) })
		edgeLog(u.now, dt, u.local(s, at), moved)
		if moved == (geom.Point{}) {
			continue
		}
		// The nodes still stand where the last frame drew them, so the
		// point under the pointer now is where the content was that far
		// back.
		back := at.Sub(s.screenAt(moved).Sub(s.screenAt(geom.Point{})))
		if from == u.capture {
			c := u.capture
			u.deliver(c, input.PointerMove{Pos: u.localHeld(c, back), Mods: u.mods, Time: u.now})
		} else {
			u.dragHover(back, u.dragOverData)
		}
		u.invalid = true
		return true
	}
	return false
}

// edgeDebug is set by GUNIM_DEBUG_EDGE=1, which logs each frame of a
// held drag's scroll to standard error: the frame's time and length,
// the pointer in the scroller's space, and how far the content moved.
var edgeDebug = os.Getenv("GUNIM_DEBUG_EDGE") == "1"

func edgeLog(now time.Time, dt time.Duration, p, moved geom.Point) {
	if edgeDebug {
		fmt.Fprintf(os.Stderr, "gunim edge: frame %s, %.1f ms, pointer at %.0f, moved %.1f\n",
			now.Format("15:04:05.000"), float64(dt.Microseconds())/1000, p.Y, moved.Y)
	}
}

// edgeScrollers returns the scroll views a drag held at the point at
// may scroll, innermost first: those round from. A node holding the
// pointer gets it wherever it goes, so the views round it scroll
// however far past them the pointer is. A drag over the window is over
// whatever is under it. One that has left the view it was over, for a
// part of the window that does not scroll, keeps that view while it is
// within two zones of it.
func (u *UI) edgeScrollers(from *state, at geom.Point, over bool) []*state {
	var found []*state
	for s := from; s != nil; s = s.parent {
		if _, ok := s.node.(EdgeScroller); ok {
			found = append(found, s)
		}
	}
	if !over {
		return found
	}
	if len(found) > 0 {
		u.edgeLast = found[0]
		return found
	}
	if s := u.edgeLast; s != nil && s.presence != Exiting && s.drawn == u.seq {
		box := geom.Rect{Max: s.size.Point()}.Inset(geom.Uniform(-2 * EdgeZone))
		if box.Contains(u.local(s, at)) {
			return []*state{s}
		}
	}
	u.edgeLast = nil
	return nil
}

func holdsDrag(s *state) bool {
	h, ok := s.node.(DragHolder)
	return ok && h.DragHeld()
}
