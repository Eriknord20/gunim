package plug

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// A view under its least size lays out at it, clips to its box and moves by how far it is scrolled; one with room
// lays out in its room, unclipped.
func TestARoomLaysAViewOutAtLeastAtItsLeastSize(t *testing.T) {
	least := geom.Sz(700, 400)
	var r Room
	if got := r.Lay(geom.Sz(800, 500), least); got != geom.Sz(800, 500) {
		t.Errorf("in 800×500, a view of least size 700×400 lays out at %v", got)
	}
	var p paint.Painter
	r.Paint(&p, gunim.Frame{Scale: 1})()
	if n := len(p.Ops()); n != 0 {
		t.Errorf("a view with room paints %d ops to clip itself", n)
	}
	if got := r.Lay(geom.Sz(600, 500), least); got != geom.Sz(700, 500) {
		t.Errorf("in 600×500, a view of least size 700×400 lays out at %v", got)
	}
	r.x.Jump(40)
	r.Paint(&p, gunim.Frame{Scale: 1})()
	if l, ok := p.Ops()[0].(*paint.LayerOp); !ok || !l.Opts.Clip || l.Opts.Bounds != geom.Rc(0, 0, 600, 500) {
		t.Errorf("a view under its least size clips with %#v", p.Ops())
	}
	if got := p.Transform().Apply(geom.Point{}); got != (geom.Point{}) {
		t.Errorf("after the view painted, the painter is moved to %v", got)
	}
	// Grown to its least size, the view scrolls back to its start.
	r.Lay(geom.Sz(700, 500), least)
	if got := r.Scrolled(); got != (geom.Point{}) {
		t.Errorf("grown to its least size, the view is scrolled to %v", got)
	}
	if r.Step(time.Second / 60) {
		t.Error("a view at rest still moves")
	}
}
