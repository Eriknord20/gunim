package widget

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// FoldMotion carries a fold open and shut.
var FoldMotion = theme.Spring("motion.fold", anim.Spring{Response: 0.3, Damping: 0.95})

// Fold shows its child, or folds it away, such as a panel's section under
// its heading or a row that only one mode has. Opening and shutting, its
// height springs between nought and the child's, the child clipped to it
// and fading, so what lies below glides up and down rather than jumps, and
// it sounds the open or the close cue. A fold shut takes no room and no
// pointer, and its child keeps its state.
//
// Axis folds it sideways instead, so what lies after it glides along a
// row. Comes makes it grow in as it joins the tree and shrink out as it
// leaves, for something added and taken away rather than kept.
type Fold struct {
	anim.Group
	// Axis is the way the fold runs: down, the zero value of a fold made
	// by NewFold, or across with Horizontal.
	Axis Axis
	// Comes grows the fold in as it is inserted and shrinks it out as it
	// is removed.
	Comes bool

	child gunim.Node
	open  *anim.Float
	shut  bool
	box   geom.Size
	// laid is set by the first layout; a state set before it shows at once.
	laid bool
}

// NewFold returns a fold of child, open or shut as open says, at once.
func NewFold(child gunim.Node, open bool) *Fold {
	f := &Fold{Axis: Vertical, child: child, open: anim.NewFloat(0), shut: !open}
	if open {
		f.open.Jump(1)
	}
	f.Add(f.open)
	return f
}

// Open reports whether the fold is open, or opening.
func (f *Fold) Open() bool { return !f.shut }

// SetOpen opens or shuts the fold and sends no intent. Once the fold is
// laid out it glides; before that, or with a nil u, it jumps.
func (f *Fold) SetOpen(open bool, u *gunim.UI) {
	if open == !f.shut {
		return
	}
	f.shut = !open
	to := map[bool]float32{false: 0, true: 1}[open]
	if !f.laid || u == nil {
		f.open.Jump(to)
		return
	}
	if open {
		u.Cue(gunim.CueOpen, f)
	} else {
		u.Cue(gunim.CueClose, f)
	}
	f.open.Animate(to, FoldMotion.Get(u.Theme()))
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (f *Fold) Children() []gunim.Node { return []gunim.Node{f.child} }

// Transition implements [gunim.Transitioner]: a fold that Comes grows in
// and shrinks out. Any other joins and leaves at once.
func (f *Fold) Transition(p gunim.Presence, fr gunim.Frame) bool {
	if !f.Comes {
		return true
	}
	switch p {
	case gunim.Entering:
		f.shut = false
		f.open.Animate(1, FoldMotion.Get(fr.Theme))
	case gunim.Exiting:
		f.shut = true
		f.open.Animate(0, FoldMotion.Get(fr.Theme))
	case gunim.Present:
	}
	return !f.open.Active()
}

// Layout implements [gunim.Node]: down, as wide as the child and as tall
// as it is open; across, as tall as the child and as wide as it is open.
func (f *Fold) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	f.laid = true
	k := kids.At(0)
	if f.Axis == Horizontal {
		s := k.Layout(gunim.Constraints{Max: c.Max})
		k.Place(geom.Point{})
		t := min(max(f.open.Value(), 0), 1)
		f.box = c.Constrain(geom.Sz(s.W*t, s.H))
		return f.box
	}
	s := k.Layout(gunim.Constraints{Min: geom.Sz(c.Min.W, 0), Max: geom.Sz(c.Max.W, 0)})
	k.Place(geom.Point{})
	t := min(max(f.open.Value(), 0), 1)
	f.box = c.Constrain(geom.Sz(s.W, s.H*t))
	return f.box
}

// Covers implements [gunim.Shaped]: only what shows of the child takes
// the pointer.
func (f *Fold) Covers(p geom.Point) bool {
	if f.Axis == Horizontal {
		return f.box.W > 0.5 && p.X >= 0 && p.X < f.box.W
	}
	return f.box.H > 0.5 && p.Y >= 0 && p.Y < f.box.H
}

// Paint implements [gunim.Node].
func (f *Fold) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	t := min(max(f.open.Value(), 0), 1)
	if t < 0.001 {
		return
	}
	if t > 0.999 {
		kids.At(0).Paint(p)
		return
	}
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: t * t, Clip: true})()
	kids.At(0).Paint(p)
}
