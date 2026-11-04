package widget

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// Steady holds a still part of a view apart from what moves beside it: while its key holds, the window carries what it
// drew from frame to frame, with neither Layout nor Paint. [gunim.Steady] says when the key counts and what it must
// hold. It gives a view that keeps [anim.Moves] the part it needs in one call:
//
//	body := widget.NewSteadyPaint(v.paintBody, func(gunim.Frame) (any, bool) { return v.moves.Count(), true })
//
// It has no look of its own, takes no input, and moves only as what it holds does.
type Steady struct {
	child gunim.Node
	draw  func(p *paint.Painter, f gunim.Frame, box geom.Size)
	key   func(f gunim.Frame) (key any, ok bool)
}

// NewSteady returns child, at the size it takes, carried while key holds. A nil key, or one that says not ok, draws it
// on every frame.
func NewSteady(child gunim.Node, key func(f gunim.Frame) (key any, ok bool)) *Steady {
	return &Steady{child: child, key: key}
}

// NewSteadyPaint returns a node that fills the room it is given and draws with draw, carried while key holds. A nil
// key, or one that says not ok, draws it on every frame.
func NewSteadyPaint(draw func(p *paint.Painter, f gunim.Frame, box geom.Size), key func(f gunim.Frame) (key any, ok bool)) *Steady {
	return &Steady{draw: draw, key: key}
}

// SteadyKey implements [gunim.Steady].
func (s *Steady) SteadyKey(f gunim.Frame) (any, bool) {
	if s.key == nil {
		return nil, false
	}
	return s.key(f)
}

// Children implements [gunim.Composite].
func (s *Steady) Children() []gunim.Node {
	if s.child == nil {
		return nil
	}
	return []gunim.Node{s.child}
}

// Layout implements [gunim.Node].
func (s *Steady) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	if s.child == nil {
		return c.Max
	}
	kid := kids.At(0)
	size := kid.Layout(c)
	kid.Place(geom.Point{})
	return size
}

// Paint implements [gunim.Node].
func (s *Steady) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	if s.child == nil {
		if s.draw != nil {
			s.draw(p, f, box)
		}
		return
	}
	kids.At(0).Paint(p)
}
