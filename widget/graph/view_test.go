package graph

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
)

// uiCall is a call to run on the UI goroutine, as a patch.
type uiCall func(u *gunim.UI)

// stage shows v in an offscreen window, showing g, and returns a
// function that runs frames and one that takes the intents sent.
func stage(t *testing.T, v *View, g Graph) (w *gunim.Window, run func(int), sent func() []gunim.Intent) {
	t.Helper()
	w = gunimtest.New(t, geom.Sz(1000, 600), nil)
	gunim.RegisterView(w, "v", func(Graph) *View { return v }, func(v *View, g Graph, u *gunim.UI) { v.SetGraph(g, u) })
	gunim.RegisterPatch(w, "v", func(_ *View, call uiCall, u *gunim.UI) { call(u) })
	if err := w.Client().Mount(gunim.Root, "v", "v", g, "v"); err != nil {
		t.Fatal(err)
	}
	run = func(k int) {
		for range k {
			w.Frame(time.Second / 60)
		}
	}
	run(2)
	sent = func() []gunim.Intent {
		var out []gunim.Intent
		for {
			select {
			case e := <-w.Client().Intents():
				out = append(out, e.Intent)
			default:
				return out
			}
		}
	}
	return w, run, sent
}

// three is a graph of a source, a filter and a sink, the source
// joined to the filter.
func three() Graph {
	return Graph{
		Nodes: []Node{
			{ID: "src", Title: "Source", At: geom.Pt(0, 0), Outs: []Port{{ID: "out", Kind: "sound"}, {ID: "ctl", Kind: "control", Shape: Diamond}}},
			{ID: "flt", Title: "Filter", At: geom.Pt(300, 0), Ins: []Port{{ID: "in", Kind: "sound"}, {ID: "cut", Kind: "control"}}, Outs: []Port{{ID: "out", Kind: "sound"}}},
			{ID: "snk", Title: "Sink", At: geom.Pt(600, 0), Ins: []Port{{ID: "in", Kind: "sound"}}},
		},
		Wires: []Wire{{ID: "w1", From: End{"src", "out"}, To: End{"flt", "in"}}},
	}
}

// portIn is where port e stands in the view.
func portIn(t *testing.T, v *View, e End, out bool) geom.Point {
	t.Helper()
	at, ok := v.endAt(portKey{e, out})
	if !ok {
		t.Fatalf("no port %v", e)
	}
	return v.ToView(at)
}

// drag presses at from, moves to the point to in steps, a frame each,
// and lets go.
func drag(w *gunim.Window, run func(int), from, to geom.Point, mods input.Mods) {
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1, Mods: mods})
	run(1)
	for i := 1; i <= 8; i++ {
		f := float32(i) / 8
		w.Input(input.PointerMove{Pos: geom.Pt(from.X+(to.X-from.X)*f, from.Y+(to.Y-from.Y)*f), Mods: mods})
		run(1)
	}
	w.Input(input.PointerUp{Pos: to, Button: input.ButtonPrimary, Mods: mods})
	run(1)
}

func click(w *gunim.Window, run func(int), at geom.Point, clicks int, mods input.Mods) {
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: clicks, Mods: mods})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary, Mods: mods})
	run(1)
}

// only returns the one intent of type T sent, failing t otherwise.
func only[T any](t *testing.T, got []gunim.Intent) T {
	t.Helper()
	var out []T
	for _, in := range got {
		if c, ok := in.(T); ok {
			out = append(out, c)
		}
	}
	if len(out) != 1 {
		var zero T
		t.Fatalf("sent %v, want one %T", got, zero)
	}
	return out[0]
}

// TestTheFirstGraphFitsInView: every box shows, at a zoom no larger
// than a little over life size.
func TestTheFirstGraphFitsInView(t *testing.T) {
	v := NewView()
	stage(t, v, three())
	for _, n := range v.g.Nodes {
		r := v.rectOf(&n)
		if a, b := v.ToView(r.Min), v.ToView(r.Max); a.X < 0 || a.Y < 0 || b.X > 1000 || b.Y > 600 {
			t.Errorf("%s stands at %v to %v, out of view", n.ID, a, b)
		}
	}
	if z := v.Zoom(); z > 1.1 || z < zoomMin {
		t.Errorf("zoom %v", z)
	}
}

// TestABoxDraggedFollowsThePointerAndMovesOnce: every frame of the
// drag, and one Move as it ends.
func TestABoxDraggedFollowsThePointerAndMovesOnce(t *testing.T) {
	v := NewView()
	w, run, sent := stage(t, v, three())
	n := v.g.Node("snk")
	from := v.ToView(v.rectOf(n).Min.Add(geom.Pt(40, 10)))
	to := from.Add(geom.Pt(-60, 120))
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	run(1)
	for i := 1; i <= 8; i++ {
		f := float32(i) / 8
		p := geom.Pt(from.X+(to.X-from.X)*f, from.Y+(to.Y-from.Y)*f)
		w.Input(input.PointerMove{Pos: p})
		run(1)
		if got := v.ToView(v.rectOf(n).Min.Add(geom.Pt(40, 10))); dist(got, p) > 0.5 && dist(p, from) > slop {
			t.Fatalf("step %d: the box's grip is at %v, the pointer at %v", i, got, p)
		}
	}
	w.Input(input.PointerUp{Pos: to, Button: input.ButtonPrimary})
	run(1)
	mv := only[Move](t, sent())
	z := v.Zoom()
	want := geom.Pt(600, 0).Add(geom.Pt(-60/z, 120/z))
	if got := mv.To["snk"]; dist(got, want) > 0.5 || len(mv.To) != 1 {
		t.Errorf("moved to %v, want snk at %v", mv.To, want)
	}
}

// TestAWireDrawnFromAPortJoinsTheNearestThatTakesIt: let go near the
// sink's input, it snaps there and asks to join; one of another kind
// is no snap, and Accepts refusing says why.
func TestAWireDrawnFromAPortJoinsTheNearestThatTakesIt(t *testing.T) {
	v := NewView()
	w, run, sent := stage(t, v, three())
	from := portIn(t, v, End{"flt", "out"}, true)
	to := portIn(t, v, End{"snk", "in"}, false).Add(geom.Pt(10, 8))
	drag(w, run, from, to, 0)
	if c := only[Connect](t, sent()); c != (Connect{From: End{"flt", "out"}, To: End{"snk", "in"}}) {
		t.Errorf("sent %+v", c)
	}
	// A control out near a sound in: no snap, nothing joined.
	drag(w, run, portIn(t, v, End{"src", "ctl"}, true), portIn(t, v, End{"snk", "in"}, false), 0)
	for _, in := range sent() {
		if _, ok := in.(Connect); ok {
			t.Fatalf("a control joined a sound in: %v", in)
		}
	}
	v.Accepts = func(from, to End) (bool, string) { return false, "Not today." }
	drag(w, run, from, portIn(t, v, End{"snk", "in"}, false), 0)
	if r := only[Refused](t, sent()); r.Why != "Not today." || r.To != (End{"snk", "in"}) {
		t.Errorf("refused as %+v", r)
	}
}

// TestAWiresEndTakenElsewhereOrOut: from its input to another, it
// replugs; let go on nothing, it comes out.
func TestAWiresEndTakenElsewhereOrOut(t *testing.T) {
	v := NewView()
	w, run, sent := stage(t, v, three())
	in := portIn(t, v, End{"flt", "in"}, false)
	drag(w, run, in, portIn(t, v, End{"snk", "in"}, false), 0)
	if r := only[Replug](t, sent()); r != (Replug{Wire: "w1", From: End{"src", "out"}, To: End{"snk", "in"}}) {
		t.Errorf("sent %+v", r)
	}
	drag(w, run, in, in.Add(geom.Pt(0, 200)), 0)
	if d := only[Delete](t, sent()); !slices.Equal(d.Wires, []string{"w1"}) || len(d.Nodes) != 0 {
		t.Errorf("pulled out, sent %+v", d)
	}
}

// TestPickingAndDeleting: a click picks a box, Shift adds another, a
// wire picked takes the boxes' place, and Delete asks to take out what
// is picked; Escape lets it go.
func TestPickingAndDeleting(t *testing.T) {
	v := NewView()
	w, run, sent := stage(t, v, three())
	mid := func(id string) geom.Point {
		r := v.rectOf(v.g.Node(id))
		return v.ToView(geom.Pt(r.Center().X, r.Min.Y+10))
	}
	click(w, run, mid("src"), 1, 0)
	click(w, run, mid("snk"), 1, input.ModShift)
	nodes, wire := v.Picked()
	if !slices.Equal(nodes, []string{"src", "snk"}) || wire != "" {
		t.Fatalf("picked %v and %q", nodes, wire)
	}
	sent()
	w.Input(input.KeyPress{Key: input.KeyDelete})
	run(1)
	if d := only[Delete](t, sent()); !slices.Equal(d.Nodes, []string{"src", "snk"}) {
		t.Errorf("Delete sent %+v", d)
	}
	sh, _ := v.shapeOf(*v.g.Wire("w1"))
	pts := sh.points()
	click(w, run, v.ToView(pts[len(pts)/2]), 1, 0)
	if nodes, wire := v.Picked(); len(nodes) != 0 || wire != "w1" {
		t.Fatalf("a click on the wire picked %v and %q", nodes, wire)
	}
	w.Input(input.KeyPress{Key: input.KeyEscape})
	run(1)
	if nodes, wire := v.Picked(); len(nodes) != 0 || wire != "" {
		t.Errorf("Escape left %v and %q picked", nodes, wire)
	}
	click(w, run, mid("flt"), 2, 0)
	if o := only[Open](t, sent()); o.Node != "flt" {
		t.Errorf("a double click sent %+v", o)
	}
}

// TestABandPicksTheBoxesItCovers: and those only.
func TestABandPicksTheBoxesItCovers(t *testing.T) {
	v := NewView()
	w, run, _ := stage(t, v, three())
	a := v.rectOf(v.g.Node("src"))
	b := v.rectOf(v.g.Node("flt"))
	drag(w, run, v.ToView(a.Min.Sub(geom.Pt(20, 20))), v.ToView(b.Max.Add(geom.Pt(20, 20))), input.ModShift)
	if nodes, _ := v.Picked(); !slices.Equal(nodes, []string{"src", "flt"}) {
		t.Errorf("the band picked %v", nodes)
	}
}

// TestTheWheelGlidesAndZoomsAboutThePointer: a notch glides the plane
// on every frame, never back; Ctrl and a notch zoom, keeping what is
// under the pointer there.
func TestTheWheelGlidesAndZoomsAboutThePointer(t *testing.T) {
	v := NewView()
	w, run, _ := stage(t, v, three())
	x0 := v.panX.Value()
	w.Input(input.Scroll{Pos: geom.Pt(500, 300), Delta: geom.Pt(-60, 0), Notches: geom.Pt(-1, 0)})
	last := x0
	for range 30 {
		run(1)
		if x := v.panX.Value(); x > last+0.001 {
			t.Fatalf("the glide went back, from %v to %v", last, x)
		}
		last = v.panX.Value()
	}
	if math.Abs(float64(last-(x0-60))) > 0.5 {
		t.Errorf("a notch moved the plane %v, want 60", x0-last)
	}
	at := geom.Pt(400, 200)
	under := v.ToPlane(at)
	w.Input(input.Scroll{Pos: at, Delta: geom.Pt(0, 60), Notches: geom.Pt(0, 1), Mods: input.ModControl})
	for range 40 {
		run(1)
		if d := dist(v.ToView(under), at); d > 0.5 {
			t.Fatalf("zooming, the point under the pointer moved %v", d)
		}
	}
	if z := v.Zoom(); z <= 0 {
		t.Errorf("zoom %v", z)
	}
}

// TestAWireHeldNearAnEdgeScrollsThePlane: and its end stays under the
// pointer as the plane moves.
func TestAWireHeldNearAnEdgeScrollsThePlane(t *testing.T) {
	v := NewView()
	w, run, _ := stage(t, v, three())
	from := portIn(t, v, End{"flt", "out"}, true)
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	run(1)
	edge := geom.Pt(995, 300)
	w.Input(input.PointerMove{Pos: edge})
	x0 := v.panX.Value()
	run(20)
	if v.panX.Value() >= x0 {
		t.Fatalf("held at the right edge, the plane stayed at %v", v.panX.Value())
	}
	if d := dist(v.ToView(v.pr.to), edge); d > 0.5 {
		t.Errorf("the wire's end is %v from the pointer", d)
	}
	w.Input(input.PointerUp{Pos: edge, Button: input.ButtonPrimary})
	run(1)
}

// TestABoxTheApplicationMovesGlides: on every frame, to its place; and
// SetGraph takes a nil UI before the view shows.
func TestABoxTheApplicationMovesGlides(t *testing.T) {
	v := NewView()
	g := three()
	v.SetGraph(g, nil)
	w, run, _ := stage(t, v, g)
	g.Node("snk").At = geom.Pt(600, 300)
	if err := w.Client().Update("v", g); err != nil {
		t.Fatal(err)
	}
	run(1)
	last := float32(0)
	for range 40 {
		y := v.places["snk"].y.Value()
		if y < last-0.01 {
			t.Fatalf("the box went back, from %v to %v", last, y)
		}
		last = y
		run(1)
	}
	if math.Abs(float64(last-300)) > 0.5 {
		t.Errorf("the box came to %v, want 300", last)
	}
}

// TestApplyCarriesOutThePlainChanges: a wire added once, replugged,
// a box moved, and a box taken out with its wires.
func TestApplyCarriesOutThePlainChanges(t *testing.T) {
	g := three()
	if !g.Apply(Connect{From: End{"flt", "out"}, To: End{"snk", "in"}}) || len(g.Wires) != 2 {
		t.Fatal("Connect added no wire")
	}
	if g.Apply(Connect{From: End{"flt", "out"}, To: End{"snk", "in"}}) {
		t.Error("the same wire twice")
	}
	id := WireID(End{"flt", "out"}, End{"snk", "in"})
	if !g.Apply(Replug{Wire: id, From: End{"flt", "out"}, To: End{"flt", "cut"}}) || g.Wire(id).To != (End{"flt", "cut"}) {
		t.Error("Replug moved nothing")
	}
	if !g.Apply(Move{To: map[string]geom.Point{"src": geom.Pt(5, 5)}}) || g.Node("src").At != geom.Pt(5, 5) {
		t.Error("Move moved nothing")
	}
	if !g.Apply(Delete{Nodes: []string{"flt"}}) || len(g.Nodes) != 2 || len(g.Wires) != 0 {
		t.Errorf("Delete left %d boxes and %d wires", len(g.Nodes), len(g.Wires))
	}
	if g.Apply(Select{}) || g.Apply(Open{}) {
		t.Error("a change that changes nothing changed the graph")
	}
}

// TestAWireConnectedAfterAReplugHasANameOfItsOwn: the replugged wire
// keeps its name, and the new one takes another.
func TestAWireConnectedAfterAReplugHasANameOfItsOwn(t *testing.T) {
	g := three()
	a, b := End{"flt", "out"}, End{"snk", "in"}
	g.Apply(Connect{From: a, To: b})
	g.Apply(Replug{Wire: WireID(a, b), From: a, To: End{"flt", "cut"}})
	if !g.Apply(Connect{From: a, To: b}) {
		t.Fatal("the second wire was refused")
	}
	seen := map[string]bool{}
	for _, w := range g.Wires {
		if seen[w.ID] {
			t.Fatalf("two wires are named %s", w.ID)
		}
		seen[w.ID] = true
	}
}

// TestATouchpadPansAsTheFingersGo: fractions of a notch move the plane
// at once, by the scroll's own distance.
func TestATouchpadPansAsTheFingersGo(t *testing.T) {
	v := NewView()
	w, run, _ := stage(t, v, three())
	x0 := v.panX.Value()
	w.Input(input.Scroll{Pos: geom.Pt(500, 300), Delta: geom.Pt(-7, 0), Notches: geom.Pt(-0.17, 0)})
	run(1)
	if d := x0 - v.panX.Value(); math.Abs(float64(d-7)) > 0.01 || v.panX.Active() {
		t.Errorf("a touchpad's scroll moved the plane %v, gliding %v; want 7 at once", d, v.panX.Active())
	}
}

// TestAFingerDragsABox: on a touch screen, a box pressed drags, and
// empty room pans as it scrolls.
func TestAFingerDragsABox(t *testing.T) {
	v := NewView()
	_, _, _ = stage(t, v, three())
	v.pr = press{kind: pressMove}
	if !v.DragsTouch() {
		t.Error("a finger on a box does not drag it")
	}
	v.pr = press{kind: pressPan}
	if v.DragsTouch() {
		t.Error("a finger on empty room drags rather than scrolls")
	}
	v.pr = press{}
}

// TestABoxMovedAsItIsPressedMovesFromWhereItIsNow: the application
// moved it before the drag began.
func TestABoxMovedAsItIsPressedMovesFromWhereItIsNow(t *testing.T) {
	v := NewView()
	g := three()
	w, run, sent := stage(t, v, g)
	n := v.g.Node("snk")
	from := v.ToView(v.rectOf(n).Min.Add(geom.Pt(40, 10)))
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	run(1)
	g.Node("snk").At = geom.Pt(600, 100)
	if err := w.Client().Update("v", g); err != nil {
		t.Fatal(err)
	}
	run(1)
	to := from.Add(geom.Pt(0, 50))
	w.Input(input.PointerMove{Pos: to})
	w.Input(input.PointerUp{Pos: to, Button: input.ButtonPrimary})
	run(1)
	mv := only[Move](t, sent())
	if want := geom.Pt(600, 100+50/v.Zoom()); dist(mv.To["snk"], want) > 0.5 {
		t.Errorf("moved to %v, want %v, from where the application put it", mv.To["snk"], want)
	}
}
