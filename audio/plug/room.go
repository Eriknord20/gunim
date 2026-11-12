package plug

import (
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// Room lays a view out at least at its least size, and scrolls the view where its box is smaller: by the wheel, by a
// drag on what takes no press, and to bring the focused control into view. A view embeds it, calls Lay in Layout and
// Paint in Paint, and through it implements [gunim.Handler], [gunim.Revealer] and [gunim.Animator].
type Room struct {
	box, laid geom.Size
	x, y      *anim.Float
	// held says a drag is scrolling the view, from grab with the view scrolled to grabAt.
	held         bool
	grab, grabAt geom.Point
}

// revealPad is the room left round a control scrolled into view.
const revealPad = 8

// Lay notes the view's box and returns the size the view lays itself out at: box, grown to least on each side where
// it is smaller.
func (r *Room) Lay(box, least geom.Size) geom.Size {
	if r.x == nil {
		r.x, r.y = anim.NewFloat(0), anim.NewFloat(0)
	}
	r.box, r.laid = box, AtLeast(box, least)
	// A box grown past where the view was scrolled to brings it back.
	reach := r.reach()
	for i, a := range []*anim.Float{r.x, r.y} {
		far := [2]float32{reach.X, reach.Y}[i]
		if a.Value() > far || a.Target() > far {
			a.Jump(min(a.Value(), far))
		}
	}
	return r.laid
}

// Laid returns the size the view was last laid out at.
func (r *Room) Laid() geom.Size { return r.laid }

// Scrolled returns how far the view is scrolled, right and down.
func (r *Room) Scrolled() geom.Point {
	if r.x == nil {
		return geom.Point{}
	}
	return geom.Pt(r.x.Value(), r.y.Value())
}

// reach returns how far the view can scroll each way.
func (r *Room) reach() geom.Point {
	return geom.Pt(max(r.laid.W-r.box.W, 0), max(r.laid.H-r.box.H, 0))
}

// Paint clips what the view paints to its box and moves it by how far it is scrolled, where it was laid out larger
// than its box, and returns the function that ends both.
func (r *Room) Paint(p *paint.Painter, f gunim.Frame) func() {
	if r.laid.W <= r.box.W && r.laid.H <= r.box.H {
		return func() {}
	}
	end := p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: r.box.Point()}, Clip: true, Opacity: 1})
	at := r.Scrolled()
	snap := func(v float32) float32 {
		if f.Scale <= 0 {
			return v
		}
		return float32(math.Round(float64(v*f.Scale))) / f.Scale
	}
	pop := p.Push(paint.Translate(geom.Pt(-snap(at.X), -snap(at.Y))))
	return func() {
		pop()
		end()
	}
}

// Handle implements [gunim.Handler]: the wheel and a drag scroll a view laid out larger than its box.
func (r *Room) Handle(e input.Event, u *gunim.UI) bool {
	reach := r.reach()
	if r.x == nil || reach == (geom.Point{}) {
		r.held = false
		return false
	}
	switch e := e.(type) {
	case input.Scroll:
		d := e.Delta
		if d.X == 0 && reach.Y == 0 {
			// A plain wheel scrolls a view that is too narrow alone.
			d.X, d.Y = d.Y, 0
		}
		return r.scrollTo(geom.Pt(r.x.Target()-d.X, r.y.Target()-d.Y), u)
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		r.held, r.grab, r.grabAt = true, e.Pos, r.Scrolled()
		return true
	case input.PointerMove:
		if !r.held {
			return false
		}
		r.x.Jump(min(max(r.grabAt.X+r.grab.X-e.Pos.X, 0), reach.X))
		r.y.Jump(min(max(r.grabAt.Y+r.grab.Y-e.Pos.Y, 0), reach.Y))
		u.Invalidate()
		return true
	case input.PointerUp:
		was := r.held
		r.held = false
		return was
	}
	return false
}

// Reveal implements [gunim.Revealer]: it scrolls just far enough to bring inView, in the view's box as it shows now,
// into view.
func (r *Room) Reveal(inView geom.Rect, u *gunim.UI) {
	if r.x == nil || r.reach() == (geom.Point{}) {
		return
	}
	to := r.Scrolled()
	along := func(at *float32, lo, hi, room float32) {
		switch {
		case hi-lo+2*revealPad > room || lo < revealPad:
			*at += lo - revealPad
		case hi > room-revealPad:
			*at += hi - room + revealPad
		}
	}
	along(&to.X, inView.Min.X, inView.Max.X, r.box.W)
	along(&to.Y, inView.Min.Y, inView.Max.Y, r.box.H)
	r.scrollTo(to, u)
}

// scrollTo glides the view to be scrolled to, kept within its reach, and reports whether it moves.
func (r *Room) scrollTo(to geom.Point, u *gunim.UI) bool {
	reach := r.reach()
	to = geom.Pt(min(max(to.X, 0), reach.X), min(max(to.Y, 0), reach.Y))
	if to.X == r.x.Target() && to.Y == r.y.Target() {
		return false
	}
	r.x.Animate(to.X, anim.Snappy)
	r.y.Animate(to.Y, anim.Snappy)
	u.Invalidate()
	return true
}

// Step implements [gunim.Animator].
func (r *Room) Step(dt time.Duration) bool {
	if r.x == nil {
		return false
	}
	x := r.x.Step(dt)
	y := r.y.Step(dt)
	return x || y
}
