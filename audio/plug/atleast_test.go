package plug

import (
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// A view under its least size lays out at it and clips to its box; one with room lays out in its room, unclipped.
func TestAViewUnderItsLeastSizeLaysOutAtItAndClips(t *testing.T) {
	least := geom.Sz(700, 400)
	if got := AtLeast(geom.Sz(600, 500), least); got != geom.Sz(700, 500) {
		t.Errorf("in 600×500, a view of least size 700×400 lays out at %v", got)
	}
	var p paint.Painter
	ClipTo(&p, geom.Sz(800, 500), AtLeast(geom.Sz(800, 500), least))()
	if n := len(p.Ops()); n != 0 {
		t.Errorf("a view with room paints %d ops to clip itself", n)
	}
	ClipTo(&p, geom.Sz(600, 500), AtLeast(geom.Sz(600, 500), least))()
	if l, ok := p.Ops()[0].(*paint.LayerOp); !ok || !l.Opts.Clip || l.Opts.Bounds != geom.Rc(0, 0, 600, 500) {
		t.Errorf("a view under its least size clips with %#v", p.Ops())
	}
}
