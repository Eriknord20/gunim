package plug

import (
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// Room lays a view out at least at its least size, and scrolls the view where its box is smaller: by the wheel, by a
// drag on what takes no press or on a bar, and to bring the focused control into view. There it fades the view at each
// edge with more past it, and shows a bar on each axis that scrolls while the view moves. A view embeds it, calls Lay
// in Layout and Paint in Paint, and through it implements [gunim.Handler], [gunim.Revealer], [gunim.Animator],
// [gunim.Waker] and [gunim.PointerClaimer]; a view with a Handle or a Step of its own calls Room's from it.
//
// Under its least size Room takes every primary press on empty ground, so a parent hears none. Children are hit-tested
// through the scroll, but the view's own code is not: a view that compares pointer positions with its layout turns
// them with ToLaid, works out its hover again in OnScroll, and moves the parts it reports in [gunim.Accessible] by
// Scrolled.
type Room struct {
	// OnScroll, if set, runs on each frame the view's scroll moves.
	OnScroll func(u *gunim.UI)

	box, laid geom.Size
	x, y      *anim.Float
	// bar fades the bars in while the view scrolls and out once it has rested for barLinger, from stillSince, still
	// the frames' time since then.
	bar        *anim.Float
	stillSince time.Time
	still      time.Duration
	// u and th are the window's UI and theme, kept for Step; scale is the frame's scale, kept for ToLaid.
	u     *gunim.UI
	th    *theme.Live
	scale float32
	// held says a drag is scrolling the view, from grab with the view scrolled to grabAt; gripped names the bar
	// the drag holds.
	held         bool
	gripped      axis
	grab, grabAt geom.Point
}

// axis names a bar: none, the one along the bottom edge, or the one down the right edge.
type axis uint8

const (
	noAxis axis = iota
	across
	down
)

const (
	// revealPad is the room left round a control scrolled into view.
	revealPad = 8
	// barLinger is how long the bars stay once the view rests.
	barLinger = 800 * time.Millisecond
	// barInset is how far in from the box's edge a bar stands, and barGrip how far round it a press grips it.
	barInset = 2
	barGrip  = 4
)

// Lay notes the view's box and returns the size the view lays itself out at: box, grown to least on each side where
// it is smaller.
func (r *Room) Lay(box, least geom.Size) geom.Size {
	if r.x == nil {
		r.x, r.y, r.bar = anim.NewFloat(0), anim.NewFloat(0), anim.NewFloat(0)
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

// ToLaid returns p, a point in the view's box such as a pointer's position, in the space the view was laid out in.
func (r *Room) ToLaid(p geom.Point) geom.Point { return p.Add(r.shown()) }

// shown returns how far the view is drawn moved, the scroll snapped to the frame's pixels.
func (r *Room) shown() geom.Point {
	at := r.Scrolled()
	if r.scale <= 0 {
		return at
	}
	snap := func(v float32) float32 { return float32(math.Round(float64(v*r.scale))) / r.scale }
	return geom.Pt(snap(at.X), snap(at.Y))
}

// reach returns how far the view can scroll each way.
func (r *Room) reach() geom.Point {
	return geom.Pt(max(r.laid.W-r.box.W, 0), max(r.laid.H-r.box.H, 0))
}

// Paint clips what the view paints to its box, fades it at each edge with more past it and moves it by how far it is
// scrolled, where it was laid out larger than its box, and returns the function that ends these and draws the bars.
func (r *Room) Paint(p *paint.Painter, f gunim.Frame) func() {
	if r.laid.W <= r.box.W && r.laid.H <= r.box.H {
		return func() {}
	}
	r.th, r.scale = f.Theme, f.Scale
	at, reach, fade := r.Scrolled(), r.reach(), widget.ScrollFade.Get(f.Theme)
	edge := func(past float32) float32 { return max(0, min(past, fade)) }
	end := p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: r.box.Point()}, Clip: true, Opacity: 1,
		Fade: geom.Insets{Top: edge(at.Y), Right: edge(reach.X - at.X), Bottom: edge(reach.Y - at.Y),
			Left: edge(at.X)}})
	pop := p.Push(paint.Translate(r.shown().Mul(-1)))
	return func() {
		pop()
		end()
		r.paintBars(p, f.Theme)
	}
}

// paintBars draws a bar on each axis that scrolls, as strong as the bars have faded in.
func (r *Room) paintBars(p *paint.Painter, th *theme.Live) {
	t := min(r.bar.Value(), 1)
	if t <= 0.01 {
		return
	}
	col := widget.ScrollbarColor.Get(th)
	col.A = uint8(float32(col.A) * t)
	w := widget.ScrollbarWidth.Get(th)
	for _, a := range []axis{across, down} {
		if b, ok := r.thumb(a, w); ok {
			p.RRect(b, w/2, paint.Solid(col))
		}
	}
}

// thumb returns the bar on a, w thick, in the box's space, and false where the view does not scroll along a.
func (r *Room) thumb(a axis, w float32) (geom.Rect, bool) {
	reach := r.reach()
	at := r.Scrolled()
	along := func(box, laid, far, at, room float32) (float32, float32) {
		length := min(room, max(room*box/laid, 2*w, 24))
		return (room - length) * min(max(at, 0), far) / far, length
	}
	switch {
	case a == across && reach.X > 0:
		room := r.box.W - 2*barInset
		if reach.Y > 0 {
			room -= w + barInset
		}
		x, length := along(r.box.W, r.laid.W, reach.X, at.X, room)
		return geom.Rc(barInset+x, r.box.H-barInset-w, length, w), true
	case a == down && reach.Y > 0:
		room := r.box.H - 2*barInset
		if reach.X > 0 {
			room -= w + barInset
		}
		y, length := along(r.box.H, r.laid.H, reach.Y, at.Y, room)
		return geom.Rc(r.box.W-barInset-w, barInset+y, w, length), true
	}
	return geom.Rect{}, false
}

// barAt returns the bar showing under p, in the box's space, or none.
func (r *Room) barAt(p geom.Point) axis {
	if r.bar == nil || r.bar.Value() < 0.05 {
		return noAxis
	}
	w := widget.ScrollbarWidth.Get(r.th)
	for _, a := range []axis{across, down} {
		if b, ok := r.thumb(a, w); ok && b.Inset(geom.Uniform(-barGrip)).Contains(p) {
			return a
		}
	}
	return noAxis
}

// ClaimsPointer implements [gunim.PointerClaimer]: a bar that shows takes the pointer over what lies under it.
func (r *Room) ClaimsPointer(p geom.Point) bool { return r.gripped != noAxis || r.barAt(p) != noAxis }

// Handle implements [gunim.Handler]: the wheel and a drag scroll a view laid out larger than its box.
func (r *Room) Handle(e input.Event, u *gunim.UI) bool {
	reach := r.reach()
	if r.x == nil || reach == (geom.Point{}) {
		r.held, r.gripped = false, noAxis
		return false
	}
	r.u = u
	switch e := e.(type) {
	case input.PointerEnter:
		r.showBars()
		u.Invalidate()
		return false
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
		r.gripped = r.barAt(e.Pos)
		r.held, r.grab, r.grabAt = r.gripped == noAxis, e.Pos, r.Scrolled()
		r.showBars()
		return true
	case input.PointerMove:
		var to geom.Point
		switch {
		case r.gripped != noAxis:
			to = r.grabAt
			w := widget.ScrollbarWidth.Get(u.Theme())
			b, _ := r.thumb(r.gripped, w)
			if r.gripped == across {
				if span := r.box.W - 2*barInset - b.Size().W - r.corner(); span > 0 {
					to.X += (e.Pos.X - r.grab.X) * reach.X / span
				}
			} else if span := r.box.H - 2*barInset - b.Size().H - r.corner(); span > 0 {
				to.Y += (e.Pos.Y - r.grab.Y) * reach.Y / span
			}
		case r.held:
			to = r.grabAt.Add(r.grab).Sub(e.Pos)
		default:
			return false
		}
		r.jumpTo(to, u)
		return true
	case input.PointerUp:
		was := r.held || r.gripped != noAxis
		r.held, r.gripped = false, noAxis
		return was
	}
	return false
}

// corner returns the room the other bar takes at the end of a bar, where the view scrolls both ways.
func (r *Room) corner() float32 {
	if reach := r.reach(); reach.X > 0 && reach.Y > 0 {
		return widget.ScrollbarWidth.Get(r.th) + barInset
	}
	return 0
}

// jumpTo puts the view at to at once, kept within its reach, as a drag does, and tells the view.
func (r *Room) jumpTo(to geom.Point, u *gunim.UI) {
	reach := r.reach()
	to = geom.Pt(min(max(to.X, 0), reach.X), min(max(to.Y, 0), reach.Y))
	if to == r.Scrolled() {
		return
	}
	r.x.Jump(to.X)
	r.y.Jump(to.Y)
	r.showBars()
	if r.OnScroll != nil {
		r.OnScroll(u)
	}
	u.Invalidate()
}

// Reveal implements [gunim.Revealer]: it scrolls just far enough to bring inView, in the view's box as it shows now,
// into view.
func (r *Room) Reveal(inView geom.Rect, u *gunim.UI) {
	if r.x == nil || r.reach() == (geom.Point{}) {
		return
	}
	r.u = u
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
	r.showBars()
	u.Invalidate()
	return true
}

// showBars fades the bars in, and keeps them while the view moves.
func (r *Room) showBars() {
	if r.bar.Target() != 1 {
		r.bar.Animate(1, widget.Quick.Get(r.th))
	}
	r.stillSince, r.still = time.Time{}, 0
}

// Step implements [gunim.Animator]: the scroll glides, and the bars fade out once the view has rested a moment.
func (r *Room) Step(dt time.Duration) bool {
	if r.x == nil {
		return false
	}
	was := r.Scrolled()
	x := r.x.Step(dt)
	y := r.y.Step(dt)
	moving := x || y
	if r.Scrolled() != was && r.OnScroll != nil {
		r.OnScroll(r.u)
	}
	switch {
	case moving || r.held || r.gripped != noAxis:
		r.stillSince, r.still = time.Time{}, 0
	case r.bar.Target() > 0:
		if r.stillSince.IsZero() {
			r.stillSince = r.u.Clock()
		}
		r.still += dt
		if r.lingered() >= barLinger {
			r.bar.Animate(0, widget.Settle.Get(r.th))
			r.stillSince, r.still = time.Time{}, 0
		}
	}
	bar := r.bar.Step(dt)
	return moving || bar
}

// WakeIn implements [gunim.Waker]: the frame that fades the bars, once the view has rested for barLinger.
func (r *Room) WakeIn() time.Duration {
	if r.bar == nil || r.stillSince.IsZero() || r.bar.Target() <= 0 {
		return 0
	}
	return max(barLinger-r.lingered(), time.Millisecond)
}

// lingered returns how long the bars have stayed since the view came to rest: the frames' time, or the clock's where
// the window slept through it.
func (r *Room) lingered() time.Duration { return max(r.still, r.u.Clock().Sub(r.stillSince)) }
