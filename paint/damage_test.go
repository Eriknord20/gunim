package paint

import (
	"image/color"
	"slices"
	"testing"

	"github.com/marrasen/gunim/geom"
)

// damageOf paints two frames with the painter and returns the second's
// damage as rectangles.
func damageOf(first, second func(p *Painter)) []geom.Rect {
	var p Painter
	p.Reset()
	first(&p)
	p.Reset()
	second(&p)
	return p.DamageRects()
}

var (
	dark  = Solid(color.NRGBA{A: 0xff})
	light = Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
)

func TestDamageFarApartIsTwoRects(t *testing.T) {
	paint := func(top, bottom Fill) func(p *Painter) {
		return func(p *Painter) {
			p.RRect(geom.Rc(0, 0, 2000, 1000), 0, dark)
			// A playhead at the top, a meter at the bottom.
			p.RRect(geom.Rc(500, 0, 2, 100), 0, top)
			p.RRect(geom.Rc(1500, 900, 8, 80), 0, bottom)
		}
	}
	rs := damageOf(paint(dark, dark), paint(light, light))
	if len(rs) != 2 {
		t.Fatalf("damage %v, want two rects", rs)
	}
	var p Painter
	p.Reset()
	paint(dark, dark)(&p)
	p.Reset()
	paint(light, light)(&p)
	if d, want := p.Damage(), rs[0].Union(rs[1]); d != want {
		t.Fatalf("Damage is %v, want %v, the rectangle round both", d, want)
	}
}

func TestDamageNearTogetherIsOneRect(t *testing.T) {
	paint := func(f Fill) func(p *Painter) {
		return func(p *Painter) {
			// A meter's lights, a few pixels apart.
			for i := range 10 {
				p.RRect(geom.Rc(100, float32(10+i*6), 8, 4), 0, f)
			}
		}
	}
	if rs := damageOf(paint(dark), paint(light)); len(rs) != 1 {
		t.Fatalf("damage %v, want one rect", rs)
	}
}

func TestDamageOfManyScatteredChangesIsAFewRects(t *testing.T) {
	paint := func(f Fill) func(p *Painter) {
		return func(p *Painter) {
			for i := range 50 {
				p.RRect(geom.Rc(float32(i*97%1900), float32(i*53%900), 4, 4), 0, f)
			}
		}
	}
	if rs := damageOf(paint(dark), paint(light)); len(rs) > MaxDamageRects || len(rs) == 0 {
		t.Fatalf("damage is %d rects, want 1 to %d", len(rs), MaxDamageRects)
	}
}

func TestDamageThatCoversMostOfItsBoundsIsOneRect(t *testing.T) {
	paint := func(f Fill) func(p *Painter) {
		return func(p *Painter) {
			p.RRect(geom.Rc(0, 0, 100, 100), 0, f)
			p.RRect(geom.Rc(0, 140, 100, 100), 0, f)
		}
	}
	if rs := damageOf(paint(dark), paint(light)); len(rs) != 1 {
		t.Fatalf("damage %v, want one rect: the gap is small beside the two", rs)
	}
}

func TestDamageOfNothingChangedIsNone(t *testing.T) {
	paint := func(p *Painter) { p.RRect(geom.Rc(0, 0, 10, 10), 0, dark) }
	if rs := damageOf(paint, paint); len(rs) != 0 {
		t.Fatalf("damage %v, want none", rs)
	}
}

func TestDamageOfTheFirstFrameIsEverything(t *testing.T) {
	var p Painter
	p.RRect(geom.Rc(0, 0, 10, 10), 0, dark)
	if rs := p.DamageRects(); len(rs) != 1 || rs[0] != Everything {
		t.Fatalf("damage %v, want Everything", rs)
	}
}

// Ops that came early in a frame, more than the lookahead finds, damage
// where they stand: the ops after them find their step again, and
// damage nothing.
func TestDamageFindsItsStepAfterManyOpsCame(t *testing.T) {
	paint := func(extra int) func(p *Painter) {
		return func(p *Painter) {
			// A row of dots whose count changes, as particles on a wire.
			for i := range extra {
				p.RRect(geom.Rc(float32(10+i*3), 10, 2, 2), 0, light)
			}
			// Then a long still list below.
			for i := range 500 {
				p.RRect(geom.Rc(10, float32(100+i*2), 1000, 1), 0, dark)
			}
		}
	}
	rs := damageOf(paint(5), paint(40))
	var all geom.Rect
	for _, r := range rs {
		all = all.Union(r)
	}
	if all.Max.Y > 20 {
		t.Fatalf("35 dots that came damaged %v, want only the dots' row", rs)
	}
}

// A layer's end paired out of step with another op, as at a frame's
// tail where too few ops are left to look ahead, damages nothing of its
// own: the window-sized layer round everything is not redrawn whole.
func TestDamageOfALayerEndOutOfStepIsNotTheWholeLayer(t *testing.T) {
	paint := func(words int) func(p *Painter) {
		return func(p *Painter) {
			end := p.Layer(LayerOpts{Bounds: geom.Rc(0, 0, 2000, 1000), Opacity: 1, Clip: true})
			p.RRect(geom.Rc(0, 0, 2000, 1000), 0, dark)
			// A readout at the end whose runs come and go.
			for i := range words {
				p.RRect(geom.Rc(float32(1800+i*10), 10, 8, 8), 0, light)
			}
			end()
		}
	}
	for _, rs := range [][]geom.Rect{damageOf(paint(2), paint(4)), damageOf(paint(4), paint(2))} {
		for _, r := range rs {
			if s := r.Size(); s.W > 100 || s.H > 100 {
				t.Fatalf("a readout's runs coming or going damaged %v, want only the readout", rs)
			}
		}
	}
}

// What a clip cuts away damages nothing: a change scrolled out of sight
// in a clipping layer leaves the frame undamaged.
func TestDamageOutsideAClipIsNone(t *testing.T) {
	paint := func(f Fill) func(p *Painter) {
		return func(p *Painter) {
			end := p.Layer(LayerOpts{Bounds: geom.Rc(0, 0, 100, 100), Opacity: 1, Clip: true})
			p.RRect(geom.Rc(0, 0, 100, 100), 0, dark)
			p.RRect(geom.Rc(500, 500, 10, 10), 0, f)
			end()
		}
	}
	if rs := damageOf(paint(dark), paint(light)); len(rs) != 0 {
		t.Fatalf("a change cut away by the clip damaged %v", rs)
	}
}

// A driver draws a frame's damage while the engine paints the next one,
// so the slice DamageRects returns holds through the next frame's call.
func TestDamageHoldsWhileTheNextFramePaints(t *testing.T) {
	var p Painter
	frame := func(at float32) []geom.Rect {
		p.Reset()
		p.RRect(geom.Rc(0, 0, 2000, 1000), 0, dark)
		p.RRect(geom.Rc(at, 0, 10, 10), 0, light)
		return p.DamageRects()
	}
	frame(0)
	frame(100)
	handed := frame(500)
	want := append([]geom.Rect(nil), handed...)
	frame(1500)
	if !slices.Equal(handed, want) {
		t.Fatalf("damage handed on changed from %v to %v once the next frame painted", want, handed)
	}
}
