package paint

import (
	"image/color"
	"math"
	"slices"
	"testing"

	"github.com/marrasen/gunim/geom"
)

// wave returns a ribbon of n points along a wave, shifted along by dx,
// as an animated ribbon is sampled afresh each frame into one slice.
func wave(pts []RibbonPoint, dx float32) []RibbonPoint {
	for i := range pts {
		x := float32(i) * 4
		pts[i] = RibbonPoint{
			At:    geom.Pt(x, 50+20*float32(math.Sin(float64(x+dx)/30))),
			Width: 6 + 4*float32(math.Cos(float64(x+dx)/20)),
			Color: color.NRGBA{R: uint8(i), G: 0x80, B: 0xff, A: 0xff},
		}
	}
	return pts
}

// ribbonAt returns the painter's i-th op, which must be a ribbon.
func ribbonAt(t *testing.T, p *Painter, i int) *RibbonOp {
	t.Helper()
	r, ok := p.Ops()[i].(*RibbonOp)
	if !ok {
		t.Fatalf("op %d is a %T, want a ribbon", i, p.Ops()[i])
	}
	return r
}

func TestARibbonOfFewerThanTwoPointsRecordsNothing(t *testing.T) {
	var p Painter
	p.Reset()
	p.Ribbon(nil)
	p.Ribbon([]RibbonPoint{{At: geom.Pt(1, 1), Width: 4, Color: color.NRGBA{A: 0xff}}})
	if n := len(p.Ops()); n != 0 {
		t.Fatalf("ribbons of no point and of one recorded %d commands, want none", n)
	}
}

func TestARibbonKeepsItsPointsAsTheyWereWhenRecorded(t *testing.T) {
	var p Painter
	p.Reset()
	pts := wave(make([]RibbonPoint, 50), 0)
	want := slices.Clone(pts)
	end := p.Blend(BlendAdd)
	p.Ribbon(pts)
	end()
	wave(pts, 10)
	got := ribbonAt(t, &p, 0)
	if !slices.Equal(got.Points, want) {
		t.Fatal("the op's points changed with the caller's slice")
	}
	if got.Blend != BlendAdd {
		t.Errorf("the ribbon has blend %v, want the BlendAdd in force", got.Blend)
	}
	// An append to one ribbon's points does not reach the next ribbon's.
	p.Ribbon(want)
	_ = append(got.Points, RibbonPoint{Width: 99})
	if next := ribbonAt(t, &p, 1).Points; !slices.Equal(next, want) {
		t.Fatal("an append to the first ribbon's points reached the second's")
	}
}

func TestARibbonsBoundsHoldItsWidthAndBends(t *testing.T) {
	var p Painter
	p.Reset()
	defer p.Push(Translate(geom.Pt(100, 0)))()
	p.Ribbon([]RibbonPoint{
		{At: geom.Pt(10, 20), Width: 4},
		{At: geom.Pt(50, 40), Width: 8},
	})
	// Grown by half the widest width as far as a sharp bend's corner
	// reaches, and by the pixel and a half every op is grown by.
	want := geom.Rect{Min: geom.Pt(100+10-8-1.5, 20-8-1.5), Max: geom.Pt(100+50+8+1.5, 40+8+1.5)}
	if got := p.bounds[0]; got != want {
		t.Fatalf("the ribbon's bounds are %v, want %v", got, want)
	}
}

func TestARibbonThatChangesDamagesAndOneThatStaysDoesNot(t *testing.T) {
	var p Painter
	pts := wave(make([]RibbonPoint, 100), 0)
	draw := func() {
		p.Reset()
		p.Ribbon(pts)
	}
	draw()
	draw()
	if d := p.Damage(); !d.Empty() {
		t.Fatalf("a ribbon drawn the same again damaged %v", d)
	}
	// Two frames on, the buffer the first frame copied into is reused;
	// the frame compared against must still hold its own points.
	draw()
	wave(pts, 5)
	draw()
	if d := p.Damage(); d.Empty() {
		t.Fatal("a ribbon that moved damaged nothing")
	}
	draw()
	if d := p.Damage(); !d.Empty() {
		t.Fatalf("the moved ribbon drawn the same again damaged %v", d)
	}
}

func TestARibbonDrawnEveryFrameAllocatesNothing(t *testing.T) {
	var p Painter
	left, right := make([]RibbonPoint, 200), make([]RibbonPoint, 200)
	dx := float32(0)
	frame := func() {
		p.Reset()
		dx++
		p.Ribbon(wave(left, dx))
		p.Ribbon(wave(right, -dx))
		p.Damage()
	}
	// The first frames grow the painter's buffers to what a frame takes.
	for range 3 {
		frame()
	}
	if n := testing.AllocsPerRun(50, frame); n != 0 {
		t.Fatalf("a frame of two ribbons allocated %v times, want none", n)
	}
}

func TestARibbonKeptInARecordingSurvivesLaterFrames(t *testing.T) {
	var p Painter
	pts := wave(make([]RibbonPoint, 60), 0)
	want := slices.Clone(pts)
	p.Reset()
	pop := p.Push(Translate(geom.Pt(50, 0)))
	mark := p.Mark()
	p.Ribbon(pts)
	var r Recording
	p.Keep(mark, &r)
	pop()
	for i := range 4 {
		p.Reset()
		p.Ribbon(wave(pts, float32(i+1)))
	}
	for range 3 {
		p.Reset()
		p.Replay(&r)
		if len(p.Ops()) != 1 {
			t.Fatalf("replayed %d commands, want the ribbon", len(p.Ops()))
		}
		got := ribbonAt(t, &p, 0)
		if !slices.Equal(got.Points, want) {
			t.Fatal("the kept ribbon came back changed")
		}
		// It lies where it was drawn in the recording's space, now the
		// window's.
		if at := got.Transform.Apply(got.Points[0].At); at != want[0].At {
			t.Fatalf("the replayed ribbon starts at %v, want %v", at, want[0].At)
		}
	}
}

func TestARibbonCarriedOnKeepsItsPoints(t *testing.T) {
	var p Painter
	pts := wave(make([]RibbonPoint, 80), 0)
	want := slices.Clone(pts)
	p.Reset()
	mark := p.Mark()
	p.Ribbon(pts)
	run := p.RunFrom(mark)
	for i := range 3 {
		p.Reset()
		mark := p.Mark()
		if !p.Again(run) {
			t.Fatalf("frame %d: the ribbon from the frame before was refused", i)
		}
		run = p.RunFrom(mark)
		// Another ribbon, drawn anew, fills the buffers the carried one
		// was copied from.
		p.Ribbon(wave(pts, float32(i+1)))
		if d := p.Damage(); i > 0 && d.Empty() {
			t.Fatalf("frame %d: the ribbon drawn anew moved and damaged nothing", i)
		}
		if got := ribbonAt(t, &p, 0); !slices.Equal(got.Points, want) {
			t.Fatalf("frame %d: the carried ribbon's points changed", i)
		}
	}
}
