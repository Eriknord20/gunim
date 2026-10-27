package paint

import (
	"image/color"
	"testing"

	"github.com/marrasen/gunim/geom"
)

// cells draws n small squares, as a grid of cells draws.
func cells(p *Painter, n int, c color.NRGBA) {
	for i := range n {
		p.RRect(geom.Rc(float32(i*10), 0, 10, 10), 0, Solid(c))
	}
}

// A run drawn again in the next frame is the same commands, and the
// frame's damage is only what changed around it.
func TestARunCarriedOnIsTheSameAndDamagesNothing(t *testing.T) {
	var p Painter
	red, blue := color.NRGBA{R: 0xff, A: 0xff}, color.NRGBA{B: 0xff, A: 0xff}
	p.Reset()
	mark := p.Mark()
	cells(&p, 50, red)
	run := p.RunFrom(mark)
	p.RRect(geom.Rc(0, 100, 10, 10), 0, Solid(red))
	was := append([]Op(nil), p.Ops()...)

	p.Reset()
	if !p.Again(run) {
		t.Fatal("the run from the frame before was refused")
	}
	p.RRect(geom.Rc(0, 100, 10, 10), 0, Solid(blue))
	if len(p.Ops()) != len(was) {
		t.Fatalf("the frame holds %d commands, want %d", len(p.Ops()), len(was))
	}
	for i := range 50 {
		if !sameOp(p.Ops()[i], was[i]) {
			t.Fatalf("command %d changed in carrying", i)
		}
	}
	if d := p.Damage(); d.Min.Y < 98 {
		t.Fatalf("the damage is %v, want the one square that changed", d)
	}
}

// A run from two frames back, or from another painter, or holding a
// layer that blurs, is refused, and nothing is recorded.
func TestARunFromElsewhereIsRefused(t *testing.T) {
	var p, other Painter
	red := color.NRGBA{R: 0xff, A: 0xff}
	p.Reset()
	old := p.RunFrom(p.Mark())
	cells(&p, 5, red)
	old = p.RunFrom(old.from)
	p.Reset()
	cells(&p, 5, red)
	p.Reset()
	if p.Again(old) {
		t.Fatal("a run from two frames back was taken")
	}
	other.Reset()
	cells(&other, 5, red)
	theirs := other.RunFrom(0)
	other.Reset()
	if p.Again(theirs) {
		t.Fatal("another painter's run was taken")
	}
	p.Reset()
	mark := p.Mark()
	func() {
		defer p.Layer(LayerOpts{Bounds: geom.Rc(0, 0, 50, 50), Opacity: 1, Blur: 4})()
		cells(&p, 3, red)
	}()
	blurred := p.RunFrom(mark)
	p.Reset()
	if p.Again(blurred) || len(p.Ops()) != 0 {
		t.Fatalf("a run holding a blurred layer was taken, leaving %d commands", len(p.Ops()))
	}
}

// A run holding layers it opens and closes is carried, layers and all,
// inside a layer open round it, which takes in its bounds.
func TestARunHoldingLayersIsCarried(t *testing.T) {
	var p Painter
	red := color.NRGBA{R: 0xff, A: 0xff}
	frame := func(carry *Run) Run {
		end := p.Layer(LayerOpts{Bounds: geom.Rc(0, 0, 400, 400), Opacity: 1})
		mark := p.Mark()
		if carry == nil || !p.Again(*carry) {
			func() {
				defer p.Layer(LayerOpts{Bounds: geom.Rc(0, 0, 300, 20), Opacity: 1, Clip: true})()
				cells(&p, 20, red)
			}()
		}
		run := p.RunFrom(mark)
		end()
		return run
	}
	p.Reset()
	run := frame(nil)
	was := append([]Op(nil), p.Ops()...)
	outer := p.bounds[0]
	p.Reset()
	carried := frame(&run)
	if len(p.Ops()) != len(was) {
		t.Fatalf("the frame holds %d commands, want %d", len(p.Ops()), len(was))
	}
	for i := range was {
		if !sameOp(p.Ops()[i], was[i]) {
			t.Fatalf("command %d changed in carrying", i)
		}
	}
	if p.bounds[0] != outer {
		t.Fatalf("the layer round the run has bounds %v, want %v", p.bounds[0], outer)
	}
	if len(p.open) != 0 {
		t.Fatalf("%d layers left open", len(p.open))
	}
	if d := p.Damage(); !d.Empty() {
		t.Fatalf("carried, the frame damages %v", d)
	}
	if carried.to-carried.from != run.to-run.from {
		t.Fatalf("the carried run holds %d commands, want %d", carried.to-carried.from, run.to-run.from)
	}
}

// What a node paints again is the same as what it painted, where it
// draws the same; and not, where it draws otherwise.
func TestARunPaintedAgainIsTheSameWhereItDrawsTheSame(t *testing.T) {
	var p Painter
	red, blue := color.NRGBA{R: 0xff, A: 0xff}, color.NRGBA{B: 0xff, A: 0xff}
	p.Reset()
	run := p.RunFrom(p.Mark())
	cells(&p, 5, red)
	run = p.RunFrom(run.from)
	p.Reset()
	mark := p.Mark()
	cells(&p, 5, red)
	if !p.SameAs(run, mark) {
		t.Fatal("the same cells painted again are not the same")
	}
	again := p.RunFrom(mark)
	p.Reset()
	mark = p.Mark()
	cells(&p, 4, red)
	cells(&p, 1, blue)
	if p.SameAs(again, mark) {
		t.Fatal("a cell of another colour is the same")
	}
}

// A run that closes a layer it did not open, or leaves one open, is
// refused.
func TestARunWithALayerHalfInIsRefused(t *testing.T) {
	var p Painter
	red := color.NRGBA{R: 0xff, A: 0xff}
	p.Reset()
	end := p.Layer(LayerOpts{Bounds: geom.Rc(0, 0, 50, 50), Opacity: 1})
	mark := p.Mark()
	cells(&p, 3, red)
	end()
	closes := p.RunFrom(mark)
	opens := p.RunFrom(0)
	opens.to, opens.last = mark+3, p.ops[mark+2]
	p.Reset()
	if p.Again(closes) || p.Again(opens) || len(p.Ops()) != 0 {
		t.Fatalf("a run with a layer half in was taken, leaving %d commands", len(p.Ops()))
	}
}

// Clips alike are the same, whatever made them; another shape, or
// another clip round them, is not.
func TestClipsAlikeAreTheSame(t *testing.T) {
	var p Painter
	clipOf := func(r geom.Rect, inner bool) *Clip {
		p.Reset()
		defer p.Layer(LayerOpts{Bounds: geom.Rc(0, 0, 500, 500), Opacity: 1, Clip: true})()
		if !inner {
			return p.Clip()
		}
		defer p.Layer(LayerOpts{Bounds: r, Opacity: 1, Clip: true, Radius: 4})()
		return p.Clip()
	}
	a, b := clipOf(geom.Rc(0, 0, 50, 50), true), clipOf(geom.Rc(0, 0, 50, 50), true)
	if a == b || !a.Same(b) {
		t.Fatal("two clips made alike are not the same")
	}
	if a.Same(clipOf(geom.Rc(0, 0, 60, 50), true)) || a.Same(clipOf(geom.Rect{}, false)) || a.Same(nil) {
		t.Fatal("clips of other shapes are the same")
	}
	var none *Clip
	if !none.Same(nil) {
		t.Fatal("no clip is not the same as no clip")
	}
}
