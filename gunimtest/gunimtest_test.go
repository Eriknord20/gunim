package gunimtest_test

import (
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/paint"
)

// paintCount is a node that counts its paints.
type paintCount struct{ paints int }

func (c *paintCount) Layout(cs gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size { return cs.Max }

func (c *paintCount) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, _ gunim.Children) {
	c.paints++
	p.RRect(geom.Rc(0, 0, 10, 10), 0, paint.Solid(color.NRGBA{R: 200, A: 255}))
}

// A test window steps its frames without painting them, and paints once for a look.
func TestATestWindowPaintsOnlyWhenLookedAt(t *testing.T) {
	c := &paintCount{}
	w := gunimtest.New(t, geom.Sz(100, 100), c)
	for range 20 {
		w.Frame(time.Second / 60)
	}
	if c.paints != 0 {
		t.Fatalf("painted %d times with nothing looking, want none", c.paints)
	}
	if ops := w.Offscreen().Ops(); len(ops) == 0 {
		t.Fatal("a look found no ops")
	}
	if c.paints != 1 {
		t.Fatalf("painted %d times for a look, want once", c.paints)
	}
}
