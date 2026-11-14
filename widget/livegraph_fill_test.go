package widget

import (
	"math"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

const fillFrame = time.Second / 60

// graphOps is what a staged graph drew last: its head's centre, the left and right ends of its fill, whether it drew
// its baseline, and how many text runs.
type graphOps struct {
	head        geom.Point
	hasHead     bool
	left, right float32
	baseline    bool
	texts       int
}

func graphDrawn(w *gunim.Window, width float32) graphOps {
	o := graphOps{left: float32(math.Inf(1)), right: float32(math.Inf(-1))}
	for _, op := range w.Offscreen().Ops() {
		switch r := op.(type) {
		case *paint.RRectOp:
			if r.Fill.Gradient != nil {
				o.left = min(o.left, r.Transform.Apply(r.Rect.Min).X)
				o.right = max(o.right, r.Transform.Apply(r.Rect.Max).X)
			}
			if r.Radius == 2 {
				o.head, o.hasHead = r.Transform.Apply(r.Rect.Center()), true
			}
			if r.Rect.Size().H == 1 && r.Rect.Size().W == width {
				o.baseline = true
			}
		case *paint.TextOp:
			o.texts++
		}
	}
	return o
}

// still reports what of g still glides: its scale, its width in samples, or a drawn value.
func still(t *testing.T, g *LiveGraph, when string) {
	t.Helper()
	if g.top.Active() || g.across.Active() {
		t.Fatalf("%s the scale or the width is still gliding", when)
	}
	for k := range g.shown {
		if g.vel[k] != 0 || g.shown[k] != g.weighed(k) {
			t.Fatalf("%s sample %d is drawn at %v, moving at %v, gliding to %v", when, k, g.shown[k], g.vel[k], g.weighed(k))
		}
	}
}

// A graph filled with a span of samples shows them at once: the head is on the newest, at the right edge, the history
// is drawn to its left across the width, and the label says the newest second.
func TestAFilledLiveGraphStartsAtTheNewestSample(t *testing.T) {
	g := NewLiveGraph(300*time.Millisecond, 60)
	var said float64
	g.Label = func(v float64) string { said = v; return "rate" }
	history := make([]float64, 400)
	for i := range history {
		history[i] = 100
		if i >= len(history)-10 {
			history[i] = 5
		}
	}
	g.Fill(history)
	g.SetRunning(true)
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 60)})
	run(1)
	if newest := float64(len(g.samples) - 1); g.pos != newest {
		t.Fatalf("after Fill the head is at sample %v of %v; want it on the newest", g.pos, newest)
	}
	if math.Abs(said-5) > 1e-9 {
		t.Fatalf("after Fill the label says %v, want the newest second's 5, not the old traffic", said)
	}
	o := graphDrawn(w, 400)
	if o.left > 1 || o.right < 390 || o.head.X < 390 {
		t.Fatalf("the fill runs from x %v to %v and the head is at %v; want the history across 400 px, the head "+
			"at the right edge", o.left, o.right, o.head.X)
	}
}

// Fill on a graph on screen, with its scale, its width and its values all gliding, swaps them at once. Until the next
// sample the head stays on the newest and nothing glides; through the samples after, the head stays at the right
// edge, moves only a little up or down, and keeps up; stopped, the graph rests.
func TestFillingAMovingLiveGraphSwapsItAtOnce(t *testing.T) {
	const every = 200 * time.Millisecond
	g := NewLiveGraph(every, 50)
	g.Label = func(v float64) string { return "rate" }
	g.SetRunning(true)
	w, _ := stage(t, &frame{child: g, size: geom.Sz(400, 60)})
	// More samples than the 20 the width starts at, swinging, one a frame, so all three glide.
	for i := range 25 {
		g.Add(float64(10 + 190*(i%2)))
		w.Frame(fillFrame)
	}
	gliding := false
	for k := range g.vel {
		gliding = gliding || g.vel[k] != 0 || g.shown[k] != g.weighed(k)
	}
	if !g.top.Active() || !g.across.Active() || !gliding {
		t.Fatal("setup: the scale, the width and the newest values should all be gliding before the Fill")
	}

	history := make([]float64, 120)
	for i := range history {
		history[i] = 40 + 20*math.Sin(float64(i)/5)
	}
	g.Fill(history)
	still(t, g, "right after Fill")
	newest := float64(len(g.samples) - 1)
	var was geom.Point
	for i := range 10 {
		w.Frame(fillFrame)
		still(t, g, "a frame after Fill")
		if g.pos != newest {
			t.Fatalf("frame %d after Fill: the head is at sample %v, want the newest, %v", i, g.pos, newest)
		}
		o := graphDrawn(w, 400)
		if !o.hasHead {
			t.Fatalf("frame %d after Fill: no head drawn", i)
		}
		if i > 0 && o.head != was {
			t.Fatalf("frame %d after Fill: the head moved from %v to %v", i, was, o.head)
		}
		was = o.head
	}

	for _, v := range []float64{10, 90, 20, 80, 50, 70} {
		g.Add(v)
		for range 12 {
			w.Frame(fillFrame)
			o := graphDrawn(w, 400)
			if o.head.X != was.X {
				t.Fatalf("after Add(%v) the head moved across, from %v to %v", v, was, o.head)
			}
			if d := o.head.Y - was.Y; d > 3 || d < -3 {
				t.Fatalf("after Add(%v) the head jumped from %v to %v in a frame", v, was, o.head)
			}
			if lag := float64(len(g.samples)-1) - g.pos; lag > 2 {
				t.Fatalf("after Add(%v) the head is %v samples behind the newest", v, lag)
			}
			was = o.head
		}
	}

	g.SetRunning(false)
	for range 300 {
		g.painted = true
		if !g.Step(fillFrame) {
			return
		}
	}
	t.Fatal("stopped after a Fill, the graph asks for frames five seconds on")
}

// A span of no traffic keeps the scale a graph had, as Add does: the baseline, the head and the label still draw, and
// traffic that comes back is not clipped by a scale growing from nothing.
func TestFillingZerosKeepsALiveGraphsScale(t *testing.T) {
	g := NewLiveGraph(200*time.Millisecond, 50)
	g.Label = func(v float64) string { return "rate" }
	g.SetRunning(true)
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 60)})
	for _, v := range []float64{40, 80, 60, 100, 70} {
		g.Add(v)
		w.Frame(fillFrame)
	}
	run(120)
	top := g.top.Value()
	g.Fill(make([]float64, 120))
	run(1)
	if g.top.Value() != top {
		t.Fatalf("after a Fill of zeros the scale is %v, want it kept at %v", g.top.Value(), top)
	}
	o := graphDrawn(w, 400)
	if !o.baseline || !o.hasHead || o.texts == 0 {
		t.Fatalf("after a Fill of zeros the graph drew baseline %v, head %v and %d text runs; want all three",
			o.baseline, o.hasHead, o.texts)
	}
	g.Add(40)
	w.Frame(fillFrame)
	if g.top.Value() < 40 {
		t.Fatalf("traffic back after zeros has a scale of %v; it would be clipped", g.top.Value())
	}
}

// Fill takes the place of what the graph held, keeps the newest that fit, and handles one sample and none.
func TestFillReplacesALiveGraphsHistory(t *testing.T) {
	g := NewLiveGraph(100*time.Millisecond, 50)
	for _, v := range []float64{1, 2, 3} {
		g.Add(v)
	}
	g.Fill([]float64{-4, 7, 9})
	if len(g.samples) != 3 || g.samples[0] != 0 || g.samples[2] != 9 {
		t.Fatalf("after Fill the graph holds %v; want 0, 7, 9", g.samples)
	}
	big := make([]float64, 500)
	for i := range big {
		big[i] = float64(i)
	}
	g.Fill(big)
	if n := len(g.samples); n != g.Span+2 || g.samples[0] != float64(500-n) || g.samples[n-1] != 499 {
		t.Fatalf("filled with 0 to 499 the graph keeps %d samples, %v to %v; want the newest %d, %d to 499",
			n, g.samples[0], g.samples[n-1], g.Span+2, 500-g.Span-2)
	}
	if len(g.shown) != len(g.samples) || len(g.vel) != len(g.samples) {
		t.Fatal("the drawn values are not one a sample")
	}
	g.Fill([]float64{6})
	if len(g.samples) != 1 || g.pos != 0 || g.shown[0] != 6 {
		t.Fatalf("filled with one sample the graph holds %v, the head at %v", g.samples, g.pos)
	}
	g.Fill(nil)
	if len(g.samples) != 0 || g.pos != 0 {
		t.Fatalf("an empty Fill leaves %d samples, the head at %v", len(g.samples), g.pos)
	}
	g.Add(5)
	g.Add(7)
	if len(g.samples) != 2 || g.samples[1] != 7 {
		t.Fatalf("Add after an empty Fill gives %v", g.samples)
	}
}

// A graph laid out at full height keeps its room when Fill empties it, so what is under it does not jump.
func TestAnEmptyFillKeepsALiveGraphsRoom(t *testing.T) {
	g := NewLiveGraph(100*time.Millisecond, 50)
	g.Fill([]float64{1, 2, 3})
	_, run := stage(t, &frame{child: g, size: geom.Sz(400, 60)})
	run(1)
	lay := func() float32 {
		return g.Layout(gunim.Loose(geom.Sz(400, 100)), gunim.Frame{Scale: 1}, gunim.Children{}).H
	}
	tall := lay()
	for _, span := range [][]float64{nil, {4}} {
		g.Fill(span)
		if h := lay(); h != tall {
			t.Fatalf("filled with %v the graph is %v high, was %v", span, h, tall)
		}
	}
}
