package gunim

import (
	"fmt"
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// paintCount paints a box at x, and counts its paints.
type paintCount struct {
	x      float32
	paints int
}

func (c *paintCount) Layout(cs Constraints, _ Frame, _ Children) geom.Size { return cs.Max }

func (c *paintCount) Paint(p *paint.Painter, _ Frame, _ geom.Size, _ Children) {
	c.paints++
	p.RRect(geom.Rc(c.x, 0, 10, 10), 0, paint.Solid(color.NRGBA{R: 200, A: 255}))
}

// lookedAt returns an offscreen window that paints when looked at, with
// a paintCount node in it, and the node.
func lookedAt(t *testing.T) (*Window, *paintCount) {
	t.Helper()
	w := newTestWindow()
	w.PaintWhenLooked(true)
	c := &paintCount{}
	w.ui.Insert(w.ui.Root(), c)
	return w, c
}

func TestAWindowThatPaintsWhenLookedAtPaintsOnlyThen(t *testing.T) {
	w, c := lookedAt(t)
	run(w, 30)
	if c.paints != 0 {
		t.Fatalf("painted %d times with nothing looking, want none", c.paints)
	}
	c.x = 40
	run(w, 5)
	ops := w.Offscreen().Ops()
	if c.paints != 1 {
		t.Fatalf("painted %d times for a look, want once", c.paints)
	}
	r, ok := ops[len(ops)-1].(*paint.RRectOp)
	if !ok || r.Rect.Min.X != 40 {
		t.Fatalf("the last op is %#v, want the box as the last frame laid it out, at x 40", ops[len(ops)-1])
	}
	w.Offscreen().Ops()
	w.Offscreen().Damage()
	if c.paints != 1 {
		t.Fatalf("painted %d times for looks at the same frame, want once", c.paints)
	}
}

func TestAWindowPaintedWhenLookedAtDrawsWhatOnePaintedEveryFrameDraws(t *testing.T) {
	lazy, lc := lookedAt(t)
	eager := newTestWindow()
	ec := &paintCount{}
	eager.ui.Insert(eager.ui.Root(), ec)
	for i := range 20 {
		lc.x, ec.x = float32(i*7), float32(i*7)
		run(lazy, 1)
		run(eager, 1)
	}
	got, want := fmt.Sprint(opList(lazy.Offscreen().Ops())), fmt.Sprint(opList(eager.Offscreen().Ops()))
	if got != want {
		t.Fatalf("painted when looked at:\n%s\nevery frame:\n%s", got, want)
	}
	if ec.paints != 20 {
		t.Fatalf("the window that paints every frame painted %d times in 20 frames", ec.paints)
	}
}

// opList is what ops draw, for comparing.
func opList(ops []paint.Op) []string {
	out := make([]string, len(ops))
	for i, op := range ops {
		out[i] = fmt.Sprintf("%+v", op)
	}
	return out
}

func TestInputLandsWhereTheFrameLaidOutButLeftUnpainted(t *testing.T) {
	w, st, r := newStage(t, paint.Identity)
	w.PaintWhenLooked(true)
	st.t = paint.Translate(geom.Pt(300, 0))
	run(w, 3)
	w.Input(input.PointerDown{Pos: geom.Pt(350, 30), Time: time.Now()})
	if got, want := pressedAt(t, r), geom.Pt(40, 20); !near(got, want) {
		t.Fatalf("press at %v in the child's space, want %v", got, want)
	}
}

func TestATimerFindsANodeWhereTheFrameBeforeLaidItOut(t *testing.T) {
	w, _, r := newStage(t, paint.Translate(geom.Pt(300, 0)))
	w.PaintWhenLooked(true)
	run(w, 3)
	var got geom.Rect
	var ok bool
	w.ui.After(time.Second/60, func(u *UI) { got, ok = u.Bounds(r) })
	run(w, 3)
	if want := geom.Rc(310, 10, 100, 50); !ok || got != want {
		t.Fatalf("the timer found the node at %v (%v), want %v", got, ok, want)
	}
}

func TestTheAccessibilityTreeOfAWindowThatPaintsWhenLookedAtIsTheLastFrames(t *testing.T) {
	w, c := lookedAt(t)
	w.Offscreen().ListenForAccess()
	run(w, 5)
	if w.Offscreen().AccessTree() == nil || c.paints != 1 {
		t.Fatalf("the tree read after 5 unpainted frames is missing or painted %d times, want once", c.paints)
	}
	run(w, 5)
	if w.Access() == nil || c.paints != 2 {
		t.Fatalf("the window's tree is missing or painted %d times, want twice", c.paints)
	}
}

func TestAWindowPaintsEveryFrameAgainOnceToldTo(t *testing.T) {
	w, c := lookedAt(t)
	run(w, 5)
	w.PaintWhenLooked(false)
	if c.paints != 1 {
		t.Fatalf("painted %d times as it went back, want the frame owed once", c.paints)
	}
	run(w, 5)
	if c.paints != 6 {
		t.Fatalf("painted %d times, want once a frame since", c.paints)
	}
}

func TestAFramePaintedAtOnceLeavesNoOlderFrameOwed(t *testing.T) {
	w, c := lookedAt(t)
	run(w, 3)
	// A node to bring into view paints the frame at once.
	w.ui.Reveal(c)
	run(w, 1)
	if c.paints != 1 || w.ui.owed {
		t.Fatalf("painted %d times, a frame still owed: %v; want once, none owed", c.paints, w.ui.owed)
	}
	w.Offscreen().Ops()
	if c.paints != 1 {
		t.Fatalf("a look painted an older frame again: %d paints", c.paints)
	}
	if _, ok := w.ui.Bounds(c); !ok {
		t.Fatal("the node painted shows nowhere")
	}
}

func TestACommandFindsANodeWhereTheFrameBeforeLaidItOut(t *testing.T) {
	w := newTestWindow()
	w.PaintWhenLooked(true)
	c := &paintCount{}
	RegisterView(w, "box", func(struct{}) *paintCount { return c }, nil)
	var ok bool
	RegisterPatch(w, "box", func(n *paintCount, _ struct{}, u *UI) { _, ok = u.Bounds(n) })
	cl := w.Client()
	if err := cl.Mount(Root, "box", "box", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(w, 3)
	if err := cl.Patch("box", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(w, 1)
	if !ok {
		t.Fatal("a patch found the node nowhere, the frame before left unpainted")
	}
}

func TestAnInjectedClickLandsWhereTheFrameLaidOutButLeftUnpainted(t *testing.T) {
	w, st, r := newStage(t, paint.Identity)
	w.PaintWhenLooked(true)
	st.t = paint.Translate(geom.Pt(300, 0))
	run(w, 3)
	w.Inject(input.PointerDown{Pos: geom.Pt(350, 30), Time: time.Now()})
	if got, want := pressedAt(t, r), geom.Pt(40, 20); !near(got, want) {
		t.Fatalf("press at %v in the child's space, want %v", got, want)
	}
}

// shaped names a hand for the pointer over it.
type shaped struct{ recorder }

func (*shaped) Cursor(geom.Point) input.Cursor { return input.CursorHand }

func TestTheCursorReadIsTheLastFrameLaidOut(t *testing.T) {
	w := newTestWindow()
	st := &stage{t: paint.Identity}
	sh := &shaped{}
	w.ui.Insert(w.ui.Root(), st)
	w.ui.Insert(st, sh)
	run(w, 1)
	w.PaintWhenLooked(true)
	w.Input(input.PointerMove{Pos: geom.Pt(50, 30), Time: time.Now()})
	run(w, 1)
	if got := w.Offscreen().Cursor(); got != input.CursorHand {
		t.Fatalf("over the node the pointer is %v, want a hand", got)
	}
	// The node slides away from under the still pointer.
	st.t = paint.Translate(geom.Pt(300, 0))
	run(w, 3)
	if got := w.Offscreen().Cursor(); got != input.CursorArrow {
		t.Fatalf("with the node gone from under it the pointer is %v, want an arrow", got)
	}
}
