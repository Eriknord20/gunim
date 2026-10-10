package paint

import (
	"image/color"
	"strings"
	"testing"

	"github.com/marrasen/gunim/geom"
)

// square is a Shape that covers every pixel.
type square struct{ settled bool }

func (square) Coverage(w, h int) []byte {
	b := make([]byte, w*h)
	for i := range b {
		b[i] = 255
	}
	return b
}

func (s square) Settled() bool { return s.settled }

func TestAMaskRecordsItsShapeColourAndPlace(t *testing.T) {
	var p Painter
	p.Reset()
	red := color.NRGBA{R: 0xff, A: 0xff}
	func() {
		defer p.Push(Translate(geom.Pt(10, 20)))()
		p.Mask(square{true}, geom.Rc(0, 0, 16, 16), red)
		p.Mask(square{true}, geom.Rc(0, 0, 16, 16), color.NRGBA{})
		p.Mask(nil, geom.Rc(0, 0, 16, 16), red)
	}()
	if len(p.Ops()) != 1 {
		t.Fatalf("recorded %d ops, want the one visible mask", len(p.Ops()))
	}
	m, ok := p.Ops()[0].(*MaskOp)
	if !ok || m.Color != red || m.Transform != Translate(geom.Pt(10, 20)) || m.Shape != (square{true}) {
		t.Fatalf("recorded %+v", p.Ops()[0])
	}
}

// A mask whose colour changes damages its bounds; one drawn the same again damages nothing.
func TestAMaskDamagesOnlyWhenItChanges(t *testing.T) {
	var p Painter
	red, blue := color.NRGBA{R: 0xff, A: 0xff}, color.NRGBA{B: 0xff, A: 0xff}
	r := geom.Rc(40, 40, 16, 16)
	p.Reset()
	p.Mask(square{true}, r, red)
	p.Reset()
	p.Mask(square{true}, r, red)
	if d := p.Damage(); !d.Empty() {
		t.Fatalf("the same mask again damaged %v", d)
	}
	p.Reset()
	p.Mask(square{true}, r, blue)
	if d := p.Damage(); d.Empty() || d.Min.X > 40 || d.Max.X < 56 {
		t.Fatalf("a recoloured mask damaged %v, want its bounds", d)
	}
}

func TestAMaskIsCarriedAndKept(t *testing.T) {
	var p Painter
	red := color.NRGBA{R: 0xff, A: 0xff}
	p.Reset()
	mark := p.Mark()
	p.Mask(square{true}, geom.Rc(0, 0, 16, 16), red)
	run := p.RunFrom(mark)
	var rec Recording
	p.Keep(mark, &rec)
	p.Reset()
	if !p.Again(run) {
		t.Fatal("a run holding a mask was refused")
	}
	func() {
		defer p.Push(Translate(geom.Pt(100, 0)))()
		p.Replay(&rec)
	}()
	if len(p.Ops()) != 2 {
		t.Fatalf("the frame holds %d ops, want the carried mask and the replayed one", len(p.Ops()))
	}
	if m, ok := p.Ops()[1].(*MaskOp); !ok || m.Transform != Translate(geom.Pt(100, 0)) {
		t.Fatalf("replayed %+v", p.Ops()[1])
	}
}

// sliced is a Shape that cannot be compared, for it holds a slice.
type sliced struct{ pts []float32 }

func (sliced) Coverage(w, h int) []byte { return make([]byte, w*h) }

func (s sliced) Settled() bool { return len(s.pts) == 0 }

// A shape that cannot be compared is refused where it is drawn, with its type named, and not in a later frame.
func TestAMaskOfAShapeThatCannotBeComparedPanicsAtOnce(t *testing.T) {
	defer func() {
		if r, ok := recover().(string); !ok || !strings.Contains(r, "paint.sliced") {
			t.Fatalf("Mask panicked with %v, want the type named", r)
		}
	}()
	var p Painter
	p.Mask(sliced{}, geom.Rc(0, 0, 10, 10), color.NRGBA{A: 0xff})
	t.Fatal("Mask took a shape that cannot be compared")
}

func TestMasksDrawnEveryFrameAllocateNothing(t *testing.T) {
	var p Painter
	var shape Shape = square{true}
	shade := &Gradient{To: geom.Pt(16, 0), Start: color.NRGBA{A: 0xff}, End: color.NRGBA{R: 0xff, A: 0xff}}
	frame := func() {
		p.Reset()
		for i := range 500 {
			r := geom.Rc(float32(i), 0, 16, 16)
			p.Mask(shape, r, color.NRGBA{R: 0xff, A: 0xff})
			p.MaskFill(shape, r, Fill{Gradient: shade})
		}
	}
	frame()
	frame()
	if n := testing.AllocsPerRun(20, frame); n != 0 {
		t.Fatalf("a frame of a thousand masks allocated %v times, want none", n)
	}
}
