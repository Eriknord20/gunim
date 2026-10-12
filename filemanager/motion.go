package filemanager

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// slideDistance is how far a page slides as it comes and goes.
const slideDistance = 56

// page holds one screenful in a deck, such as a folder's listing. It
// slides in from the side its travel says and leaves the other way, or
// with no travel rises a little as it fades.
type page struct {
	anim.Group
	child   gunim.Node
	travel  int
	in      *anim.Float
	leaving bool
	// swapped says the page goes at once, for one that takes its place
	// showing the same thing as it was.
	swapped bool
}

func newPage(child gunim.Node, travel int) *page {
	p := &page{child: child, travel: travel, in: anim.NewFloat(0)}
	p.Add(p.in)
	return p
}

// Children implements [gunim.Composite].
func (p *page) Children() []gunim.Node { return []gunim.Node{p.child} }

// Transition implements [gunim.Transitioner].
func (p *page) Transition(pr gunim.Presence, f gunim.Frame) bool {
	switch pr {
	case gunim.Entering:
		p.in.Animate(1, Page.Get(f.Theme))
	case gunim.Exiting:
		p.leaving = true
		if p.swapped {
			p.in.Jump(0)
			return true
		}
		p.in.Animate(0, widget.Settle.Get(f.Theme))
	case gunim.Present:
	}
	return !p.in.Active()
}

// Layout implements [gunim.Node].
func (p *page) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	kid.Layout(gunim.Tight(c.Max))
	kid.Place(geom.Point{})
	return c.Max
}

// Paint implements [gunim.Node].
func (p *page) Paint(pt *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	t := min(max(p.in.Value(), 0), 1)
	if t >= 0.999 {
		kids.At(0).Paint(pt)
		return
	}
	if t <= 0.001 {
		return
	}
	// A page on its way out goes the way the new one came from.
	dir := float32(p.travel)
	off := geom.Pt(0, 12*(1-t))
	if dir != 0 {
		off = geom.Pt(dir*slideDistance*(1-t), 0)
		if p.leaving {
			off.X = -off.X
		}
	}
	defer pt.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: t})()
	defer pt.Push(paint.Translate(off))()
	kids.At(0).Paint(pt)
}

// deck stacks pages, each filling it, clipped to its box: the page
// leaving under the page arriving.
type deck struct {
	top *page
}

// show puts child on the deck as a new page that arrives with travel,
// and sends the page before it away.
func (d *deck) show(child gunim.Node, travel int, u *gunim.UI) {
	if d.top != nil {
		// The old page leaves the way the new one pushes it.
		d.top.travel = travel
		u.Remove(d.top)
	}
	d.top = newPage(child, travel)
	u.Insert(d, d.top)
}

// swap puts child on the deck in place of the page on top, where it is,
// with no page coming or going: for the same thing shown again, changed.
func (d *deck) swap(child gunim.Node, u *gunim.UI) {
	if d.top == nil || d.top.leaving {
		d.show(child, 0, u)
		return
	}
	d.top.swapped = true
	u.Remove(d.top)
	d.top = newPage(child, 0)
	d.top.in.Jump(1)
	u.Insert(d, d.top)
}

// Layout implements [gunim.Node].
func (d *deck) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	for kid := range kids.All {
		kid.Layout(gunim.Tight(c.Max))
		kid.Place(geom.Point{})
	}
	return c.Max
}

// Paint implements [gunim.Node].
func (d *deck) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: 1, Clip: true})()
	for kid := range kids.All {
		kid.Paint(p)
	}
}

// fold shows its child at the child's own height while open and folds it
// away to nothing while shut, sliding it as it goes, as the progress
// panel and the banner come and go.
type fold struct {
	anim.Group
	child gunim.Node
	open  *anim.Float
	inner float32
	// height is the height the fold took at its last layout.
	height float32
}

func newFold(child gunim.Node) *fold {
	f := &fold{child: child, open: anim.NewFloat(0)}
	f.Add(f.open)
	return f
}

// set opens or shuts the fold.
func (f *fold) set(open bool, u *gunim.UI) {
	to := float32(0)
	motion := widget.Settle.Get(u.Theme())
	if open {
		to, motion = 1, Page.Get(u.Theme())
	}
	f.open.Animate(to, motion)
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (f *fold) Children() []gunim.Node { return []gunim.Node{f.child} }

// Layout implements [gunim.Node].
func (f *fold) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	s := kid.Layout(gunim.Constraints{Min: geom.Sz(c.Max.W, 0), Max: geom.Sz(c.Max.W, 0)})
	f.inner = s.H
	t := min(max(f.open.Value(), 0), 1)
	kid.Place(geom.Pt(0, 0))
	f.height = s.H * t
	return geom.Sz(c.Max.W, f.height)
}

// Paint implements [gunim.Node].
func (f *fold) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	t := min(max(f.open.Value(), 0), 1)
	if t <= 0.001 || box.H <= 0 {
		return
	}
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: t, Clip: true})()
	defer p.Push(paint.Translate(geom.Pt(0, box.H-f.inner)))()
	kids.At(0).Paint(p)
}
