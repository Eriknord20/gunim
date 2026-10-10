package paint

import (
	"image"
	"image/color"
	"testing"

	"github.com/marrasen/gunim/geom"
)

// blendOf returns the blend an op was recorded with, and BlendInherit
// for an op that takes none.
func blendOf(op Op) Blend {
	switch op := op.(type) {
	case *RRectOp:
		return op.Blend
	case *MaskOp:
		return op.Blend
	case *ImageOp:
		return op.Blend
	case *TextOp:
		return op.Blend
	case *LayerOp:
		return op.Opts.Blend
	}
	return BlendInherit
}

// everyKind records one of each op that takes a blend, in each of the
// ways a node records it.
func everyKind(p *Painter, img *Image) {
	red := color.NRGBA{R: 0xff, A: 0xff}
	r := geom.Rc(0, 0, 10, 10)
	p.RRect(r, 2, Solid(red))
	p.RRectStroke(r, 2, Solid(red), Stroke{Width: 1, Color: red})
	p.ShadowRRect(r, 2, Solid(red), Shadow{Blur: 4, Color: red})
	p.DrawRRect(RRectOp{Rect: r, Fill: Solid(red)})
	p.Mask(square{true}, r, red)
	p.MaskFill(square{true}, r, Fill{Gradient: &Gradient{To: geom.Pt(10, 0), Start: red, End: red}})
	p.Image(img, r, ImageOpts{Opacity: 1})
	p.Text([]Glyph{{ID: 1}}, 12, red, r)
	p.Layer(LayerOpts{Bounds: r, Opacity: 0.5})()
}

func TestEveryShapeMaskImageTextAndLayerRecordedUnderBlendAddAdds(t *testing.T) {
	var p Painter
	p.Reset()
	img := NewImage(image.NewRGBA(image.Rect(0, 0, 2, 2)))
	end := p.Blend(BlendAdd)
	everyKind(&p, img)
	end()
	n := len(p.Ops())
	everyKind(&p, img)
	for i, op := range p.Ops() {
		want := BlendNormal
		if i < n {
			want = BlendAdd
		}
		if _, end := op.(*LayerEndOp); end {
			continue
		}
		if b := blendOf(op); b != want {
			t.Errorf("op %d, a %T, has blend %v, want %v", i, op, b, want)
		}
	}
}

func TestBlendScopesNestAndPutBackTheBlendBefore(t *testing.T) {
	var p Painter
	p.Reset()
	r := geom.Rc(0, 0, 10, 10)
	outer := p.Blend(BlendAdd)
	inner := p.Blend(BlendNormal)
	p.RRect(r, 0, Solid(color.NRGBA{A: 0xff}))
	inner()
	p.RRect(r, 0, Solid(color.NRGBA{A: 0xff}))
	outer()
	p.RRect(r, 0, Solid(color.NRGBA{A: 0xff}))
	for i, want := range []Blend{BlendNormal, BlendAdd, BlendNormal} {
		if b := blendOf(p.Ops()[i]); b != want {
			t.Errorf("op %d has blend %v, want %v", i, b, want)
		}
	}
}

func TestAShapeThatAsksToAddAddsOutsideAnyScope(t *testing.T) {
	var p Painter
	p.Reset()
	p.DrawRRect(RRectOp{Rect: geom.Rc(0, 0, 10, 10), Fill: Solid(color.NRGBA{R: 0xff, A: 0xff}), Blend: BlendAdd})
	if b := blendOf(p.Ops()[0]); b != BlendAdd {
		t.Fatalf("the shape has blend %v, want the BlendAdd it asked for", b)
	}
}

func TestBlendAllocatesNothing(t *testing.T) {
	var p Painter
	p.Reset()
	p.Blend(BlendAdd)()
	if n := testing.AllocsPerRun(100, func() { p.Blend(BlendAdd)() }); n != 0 {
		t.Fatalf("Blend allocated %v times a call, want none", n)
	}
}

func TestABlendLeftInForceEndsWithTheFrame(t *testing.T) {
	var p Painter
	p.Reset()
	p.Blend(BlendAdd)
	p.Reset()
	p.RRect(geom.Rc(0, 0, 10, 10), 0, Solid(color.NRGBA{A: 0xff}))
	if b := blendOf(p.Ops()[0]); b != BlendNormal {
		t.Fatalf("the next frame's shape has blend %v, want BlendNormal", b)
	}
}

func TestAFloatDrawsNormallyWhateverTheBlendItWasPutOffUnder(t *testing.T) {
	var p Painter
	p.Reset()
	end := p.Blend(BlendAdd)
	p.Float(func(p *Painter) { p.RRect(geom.Rc(0, 0, 10, 10), 0, Solid(color.NRGBA{A: 0xff})) })
	p.PaintFloats()
	p.RRect(geom.Rc(0, 0, 10, 10), 0, Solid(color.NRGBA{A: 0xff}))
	end()
	if b := blendOf(p.Ops()[0]); b != BlendNormal {
		t.Errorf("the float's shape has blend %v, want BlendNormal", b)
	}
	if b := blendOf(p.Ops()[1]); b != BlendAdd {
		t.Errorf("after the floats the shape has blend %v, want the BlendAdd still in force", b)
	}
}

func TestOnlyTheBlendChangingDamagesTheOp(t *testing.T) {
	img := NewImage(image.NewRGBA(image.Rect(0, 0, 2, 2)))
	red := color.NRGBA{R: 0xff, A: 0xff}
	r := geom.Rc(20, 30, 10, 10)
	for name, draw := range map[string]func(*Painter){
		"shape": func(p *Painter) { p.RRect(r, 2, Solid(red)) },
		"mask":  func(p *Painter) { p.Mask(square{true}, r, red) },
		"image": func(p *Painter) { p.Image(img, r, ImageOpts{Opacity: 1}) },
		"text":  func(p *Painter) { p.Text([]Glyph{{ID: 1}}, 12, red, r) },
		"layer": func(p *Painter) { p.Layer(LayerOpts{Bounds: r, Opacity: 0.5})() },
	} {
		var p Painter
		p.Reset()
		draw(&p)
		p.Reset()
		draw(&p)
		if d := p.Damage(); !d.Empty() {
			t.Errorf("the %s drawn the same again damaged %v", name, d)
		}
		p.Reset()
		end := p.Blend(BlendAdd)
		draw(&p)
		end()
		if d := p.Damage(); !d.Contains(geom.Pt(25, 35)) {
			t.Errorf("the %s drawn adding after drawn normally damaged %v, want it", name, d)
		}
	}
}

func TestAnAddedShapeKeepsItsBlendThroughAgain(t *testing.T) {
	var p Painter
	p.Reset()
	mark := p.Mark()
	end := p.Blend(BlendAdd)
	cells(&p, 3, color.NRGBA{R: 0xff, A: 0xff})
	end()
	run := p.RunFrom(mark)

	p.Reset()
	if !p.Again(run) {
		t.Fatal("the run from the frame before was refused")
	}
	for i, op := range p.Ops() {
		if b := blendOf(op); b != BlendAdd {
			t.Errorf("carried op %d has blend %v, want BlendAdd", i, b)
		}
	}
	if d := p.Damage(); !d.Empty() {
		t.Errorf("the carried run damaged %v, want nothing", d)
	}
}

func TestAReplayKeepsTheBlendsItWasRecordedWith(t *testing.T) {
	var p Painter
	p.Reset()
	r := geom.Rc(0, 0, 10, 10)
	red := color.NRGBA{R: 0xff, A: 0xff}
	mark := p.Mark()
	p.RRect(r, 0, Solid(red))
	end := p.Blend(BlendAdd)
	p.Mask(square{true}, r, red)
	end()
	var rec Recording
	p.Keep(mark, &rec)

	p.Reset()
	p.Replay(&rec)
	end = p.Blend(BlendAdd)
	p.Replay(&rec)
	end()
	for i, want := range []Blend{BlendNormal, BlendAdd, BlendNormal, BlendAdd} {
		if b := blendOf(p.Ops()[i]); b != want {
			t.Errorf("replayed op %d, a %T, has blend %v, want %v", i, p.Ops()[i], b, want)
		}
	}
}

func TestAShapeThatAsksToDrawNormallyDoesSoInsideAnAddScope(t *testing.T) {
	var p Painter
	p.Reset()
	r := geom.Rc(0, 0, 10, 10)
	defer p.Blend(BlendAdd)()
	p.DrawRRect(RRectOp{Rect: r, Fill: Solid(color.NRGBA{A: 0xff}), Blend: BlendNormal})
	p.Layer(LayerOpts{Bounds: r, Opacity: 1, Blend: BlendNormal})()
	p.DrawRRect(RRectOp{Rect: r, Fill: Solid(color.NRGBA{A: 0xff})})
	// The ops are the shape, the layer and its end, and the shape that
	// takes the blend in force.
	ops := p.Ops()
	for i, want := range map[int]Blend{0: BlendNormal, 1: BlendNormal, 3: BlendAdd} {
		if b := blendOf(ops[i]); b != want {
			t.Errorf("op %d, a %T, has blend %v, want %v", i, ops[i], b, want)
		}
	}
}

func TestBlendInheritKeepsTheBlendInForce(t *testing.T) {
	var p Painter
	p.Reset()
	r := geom.Rc(0, 0, 10, 10)
	outer := p.Blend(BlendAdd)
	inner := p.Blend(BlendInherit)
	p.RRect(r, 0, Solid(color.NRGBA{A: 0xff}))
	inner()
	outer()
	p.RRect(r, 0, Solid(color.NRGBA{A: 0xff}))
	for i, want := range []Blend{BlendAdd, BlendNormal} {
		if b := blendOf(p.Ops()[i]); b != want {
			t.Errorf("op %d has blend %v, want %v", i, b, want)
		}
	}
}

func TestAZeroPainterRecordsBlendNormal(t *testing.T) {
	var p Painter
	p.RRect(geom.Rc(0, 0, 10, 10), 0, Solid(color.NRGBA{A: 0xff}))
	end := p.Blend(BlendInherit)
	p.RRect(geom.Rc(0, 0, 10, 10), 0, Solid(color.NRGBA{A: 0xff}))
	end()
	for i, op := range p.Ops() {
		if b := blendOf(op); b != BlendNormal {
			t.Errorf("op %d has blend %v, want BlendNormal", i, b)
		}
	}
}
