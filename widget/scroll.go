package widget

import (
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// Scroll shows one child through a window it can scroll, vertically.
//
// The wheel sets a target and a spring carries the content there, so a
// fast flick of the wheel glides rather than jumps, and a new flick
// mid-glide carries on from the current speed. With DragScroll, the
// pointer drags the content and flings it, and it stretches past
// either end and springs back. A thin bar fades in while the content
// moves or the pointer is over it, and out once both stop; the pointer
// can drag the bar's thumb and press its track to page. At either end the wheel passes on to
// whatever scrolls outside, so scroll views nest.
//
// The child is laid out with unbounded height and the width the Scroll
// is given.
type Scroll struct {
	scrolling
	child gunim.Node
}

// NewScroll returns a Scroll showing child.
func NewScroll(child gunim.Node) *Scroll {
	return &Scroll{scrolling: newScrolling(), child: child}
}

// Children implements [gunim.Composite].
func (s *Scroll) Children() []gunim.Node { return []gunim.Node{s.child} }

// Handle implements [gunim.Handler].
func (s *Scroll) Handle(e input.Event, u *gunim.UI) bool { return s.handle(e, u) }

// Reveal implements [gunim.Revealer]: it scrolls just far enough to
// bring r, in the Scroll's own space, fully into view, with a little
// room around it.
func (s *Scroll) Reveal(r geom.Rect, u *gunim.UI) { s.reveal(r, u) }

// EdgeScroll implements [gunim.EdgeScroller]: a drag held near the top
// or the bottom scrolls the view.
func (s *Scroll) EdgeScroll(p geom.Point, dt time.Duration, u *gunim.UI) geom.Point {
	return s.edgeScroll(p, dt, u)
}

// Layout implements [gunim.Node].
func (s *Scroll) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	own := c.Max
	kid := kids.At(0)
	size := kid.Layout(gunim.Constraints{Min: geom.Sz(own.W, 0), Max: geom.Sz(own.W, 0)})
	// Asked for no particular height, a scroll is as tall as what it
	// holds: there is nothing to scroll within. That is what lets a
	// parent measure the content first and then hand back the room it
	// can spare, as [Dialog] does with a long form.
	if own.H <= 0 {
		// Only measured: how far it is scrolled is left as it is, for
		// the layout with the room it is given, which follows. Fitted
		// to the whole of what it holds, it would scroll back to the
		// top every frame.
		kid.Place(geom.Pt(0, -onPixels(s.offset.Value(), f.Scale)))
		return geom.Sz(own.W, max(size.H, c.Min.H))
	}
	s.fit(size.H, own, f.Theme)
	kid.Place(geom.Pt(0, -onPixels(s.offset.Value(), f.Scale)))
	return own
}

// Paint implements [gunim.Node].
func (s *Scroll) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	func() {
		defer s.layer(p, geom.Rect{Max: box.Point()}, f.Theme)()
		kids.At(0).Paint(p)
	}()
	s.paintBar(p, f)
}
