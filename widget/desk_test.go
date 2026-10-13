package widget

import (
	"image/color"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// deskStage lays a spot over the whole stage, and a desk over it, both
// size big; the stage takes the keyboard.
type deskStage struct {
	under *spot
	desk  *Desk
	size  geom.Size
}

func (s *deskStage) Children() []gunim.Node { return []gunim.Node{s.under, s.desk} }
func (s *deskStage) Focusable() bool        { return true }

func (s *deskStage) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	for k := range kids.All {
		k.Layout(gunim.Tight(s.size))
		k.Place(geom.Point{})
	}
	return c.Max
}

func (s *deskStage) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

// stageDesk mounts a desk 800 by 600 with two panels open, a at 100,100
// and b at 300,200, each 300 by 200, b in front; each panel's content a
// spot that fills it.
func stageDesk(t *testing.T) (w *gunim.Window, run func(int), st *deskStage, a, b *Panel) {
	t.Helper()
	st = &deskStage{under: newSpot(800, 600), desk: NewDesk(), size: geom.Sz(800, 600)}
	st.desk.Return = st
	w, run = stage(t, st)
	a, b = NewPanel("A", newSpot(1000, 1000)), NewPanel("B", newSpot(1000, 1000))
	a.SetRect(geom.Rc(100, 100, 300, 200))
	b.SetRect(geom.Rc(300, 200, 300, 200))
	open := func(p *Panel) {
		gunim.RegisterPatch(w, "stage", func(_ gunim.Node, _ struct{}, u *gunim.UI) { st.desk.Open(p, u) })
		if err := w.Client().Patch("stage", struct{}{}); err != nil {
			t.Fatal(err)
		}
		run(1)
	}
	open(a)
	open(b)
	run(60)
	return w, run, st, a, b
}

// front reports which panel the frame drew last, of a and b, by their
// content's place.
func frontOf(st *deskStage) *Panel {
	ps := st.desk.Panels()
	return ps[len(ps)-1]
}

func down(w *gunim.Window, at geom.Point, b input.Button) {
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: b, Clicks: 1})
}

func TestAPanelDraggedByItsTitleFollowsThePointerEveryFrame(t *testing.T) {
	w, run, st, a, b := stageDesk(t)
	// a's title bar, left of b, which overlaps a's lower right.
	from := geom.Pt(150, 115)
	down(w, from, input.ButtonPrimary)
	run(1)
	if frontOf(st) != a {
		t.Fatal("a press on a's title leaves b in front")
	}
	for i := 1; i <= 20; i++ {
		at := from.Add(geom.Pt(float32(8*i), float32(5*i)))
		w.Input(input.PointerMove{Pos: at})
		run(1)
		want := geom.Rc(100+float32(8*i), 100+float32(5*i), 300, 200)
		if a.Shown() != want {
			t.Fatalf("frame %d: a stands at %v, want %v", i, a.Shown(), want)
		}
		if got := a.Content().(*spot).at; got != want.Min.Add(geom.Pt(0, PanelTitleHeight.Default())) {
			t.Fatalf("frame %d: a's content is drawn at %v, its panel at %v", i, got, want)
		}
	}
	placed := geom.Rect{}
	a.OnPlace = func(r geom.Rect, _ *gunim.UI) gunim.Intent { placed = r; return nil }
	w.Input(input.PointerUp{Pos: from.Add(geom.Pt(160, 100)), Button: input.ButtonPrimary})
	run(1)
	if st.desk.Held() || placed != a.Rect() || a.Rect() != geom.Rc(260, 200, 300, 200) {
		t.Fatalf("let go, the desk holds %v, OnPlace gave %v, a is at %v", st.desk.Held(), placed, a.Rect())
	}
	if b.Shown() != geom.Rc(300, 200, 300, 200) {
		t.Fatalf("b moved to %v", b.Shown())
	}
}

func TestAPanelResizesByItsCornerAndStopsAtItsLeast(t *testing.T) {
	w, run, _, _, b := stageDesk(t)
	b.Min = geom.Sz(200, 120)
	// b's bottom right corner, just inside.
	from := geom.Pt(598, 398)
	down(w, from, input.ButtonPrimary)
	for i := 1; i <= 10; i++ {
		w.Input(input.PointerMove{Pos: from.Add(geom.Pt(float32(10*i), float32(-4*i)))})
		run(1)
		want := geom.Rc(300, 200, 300+float32(10*i), 200-float32(4*i))
		if b.Shown() != want {
			t.Fatalf("frame %d: b stands at %v, want %v", i, b.Shown(), want)
		}
		if got := b.Content().(*spot).box; got != geom.Sz(want.Size().W, want.Size().H-PanelTitleHeight.Default()) {
			t.Fatalf("frame %d: b's content is %v for a panel %v", i, got, want.Size())
		}
	}
	// Past the least it stops, and past the desk's edge too.
	w.Input(input.PointerMove{Pos: geom.Pt(900, 210)})
	run(1)
	if b.Shown() != geom.Rc(300, 200, 500, 120) {
		t.Fatalf("dragged past its least and the desk's edge, b stands at %v", b.Shown())
	}
	w.Input(input.PointerUp{Pos: geom.Pt(900, 210), Button: input.ButtonPrimary})
	run(1)
}

func TestASecondButtonDropsAMoveAndThePanelGoesBack(t *testing.T) {
	w, run, st, _, b := stageDesk(t)
	down(w, geom.Pt(400, 215), input.ButtonPrimary)
	w.Input(input.PointerMove{Pos: geom.Pt(450, 300)})
	run(1)
	if b.Shown().Min != geom.Pt(350, 285) {
		t.Fatalf("the drag left b at %v", b.Shown())
	}
	w.Input(input.PointerDown{Pos: geom.Pt(450, 300), Button: input.ButtonSecondary, Clicks: 1})
	run(1)
	if st.desk.Held() || b.Shown().Min != geom.Pt(300, 200) {
		t.Fatalf("after a second button the desk holds %v and b stands at %v", st.desk.Held(), b.Shown())
	}
	w.Input(input.PointerMove{Pos: geom.Pt(500, 350)})
	w.Input(input.PointerUp{Pos: geom.Pt(500, 350), Button: input.ButtonSecondary})
	w.Input(input.PointerUp{Pos: geom.Pt(500, 350), Button: input.ButtonPrimary})
	run(1)
	if b.Shown().Min != geom.Pt(300, 200) {
		t.Fatalf("moves after the drop took b to %v", b.Shown())
	}
}

func TestPanelsStayInsideAShrinkingDeskAndGrowBack(t *testing.T) {
	_, run, st, a, b := stageDesk(t)
	for _, size := range []geom.Size{geom.Sz(700, 500), geom.Sz(450, 350), geom.Sz(250, 150)} {
		st.size = size
		run(1)
		for _, p := range []*Panel{a, b} {
			r := p.Shown()
			if r.Min.X < 0 || r.Min.Y < 0 || r.Max.X > size.W || r.Max.Y > size.H {
				t.Fatalf("in a desk %v, %s stands at %v", size, p.Title, r)
			}
		}
	}
	st.size = geom.Sz(800, 600)
	run(1)
	if a.Shown() != geom.Rc(100, 100, 300, 200) || b.Shown() != geom.Rc(300, 200, 300, 200) {
		t.Fatalf("grown back, a stands at %v and b at %v", a.Shown(), b.Shown())
	}
}

func TestAPressOnAPanelStaysOnItAndOneBesideReachesUnder(t *testing.T) {
	w, run, st, _, _ := stageDesk(t)
	n := len(st.under.events)
	// The middle of b, whose content takes presses: a press reaches it.
	down(w, geom.Pt(450, 320), input.ButtonPrimary)
	w.Input(input.PointerUp{Pos: geom.Pt(450, 320), Button: input.ButtonPrimary})
	w.Input(input.Scroll{Pos: geom.Pt(450, 320), Delta: geom.Pt(0, 3)})
	run(1)
	for _, e := range st.under.events[n:] {
		if _, ok := e.(input.PointerDown); ok {
			t.Fatal("a press on a panel reached what is under the desk")
		}
	}
	down(w, geom.Pt(700, 50), input.ButtonPrimary)
	run(1)
	got := false
	for _, e := range st.under.events[n:] {
		if _, ok := e.(input.PointerDown); ok {
			got = true
		}
	}
	if !got {
		t.Fatal("a press beside the panels never reached what is under the desk")
	}
}

func TestTheCrossClosesAPanelAndTheKeyboardGoesBack(t *testing.T) {
	w, run, st, a, b := stageDesk(t)
	field := NewTextField()
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, _ struct{}, u *gunim.UI) {
		b.SetContent(field, u)
		u.Focus(field)
	})
	if err := w.Client().Patch("stage", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(2)
	closed := 0
	b.OnClose = func(*gunim.UI) gunim.Intent { closed++; return nil }
	r := b.shutRect(b.Shown().Size(), nil).Add(b.Shown().Min)
	down(w, r.Center(), input.ButtonPrimary)
	w.Input(input.PointerUp{Pos: r.Center(), Button: input.ButtonPrimary})
	run(60)
	if st.desk.IsOpen(b) || closed != 1 || st.desk.Front() != a {
		t.Fatalf("the cross leaves b open %v, OnClose run %d times", st.desk.IsOpen(b), closed)
	}
	focused := false
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, _ struct{}, u *gunim.UI) { focused = u.HasFocus(st) && u.Focused() == st })
	if err := w.Client().Patch("stage", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	if !focused {
		t.Fatal("closing the panel holding the keyboard left it elsewhere than Return")
	}
}

func TestOpeningAnOpenPanelRaisesIt(t *testing.T) {
	w, run, st, a, _ := stageDesk(t)
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, _ struct{}, u *gunim.UI) { st.desk.Open(a, u) })
	if err := w.Client().Patch("stage", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	if frontOf(st) != a || len(st.desk.Panels()) != 2 {
		t.Fatalf("opening a again leaves %s in front of %d panels", frontOf(st).Title, len(st.desk.Panels()))
	}
	// a drawn after b: a press where they overlap lands on a.
	down(w, geom.Pt(350, 250), input.ButtonPrimary)
	run(1)
	if st.desk.Held() {
		t.Fatal("a press inside a's content started a drag")
	}
	if frontOf(st) != a {
		t.Fatal("a press where a covers b brought b forward")
	}
}

// spiller paints a bar above its own box, where a plugin's view laid out
// in too little room may.
type spiller struct{}

func (spiller) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size { return c.Max }
func (spiller) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, _ gunim.Children) {
	p.RRect(geom.Rc(0, -30, 50, 20), 0, paint.Solid(color.NRGBA{R: 1, G: 2, B: 3, A: 0xff}))
}

func TestAPanelsContentStaysUnderItsBar(t *testing.T) {
	w, run, _, _, b := stageDesk(t)
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, _ struct{}, u *gunim.UI) { b.SetContent(spiller{}, u) })
	if err := w.Client().Patch("stage", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(2)
	clips := []geom.Rect{{Min: geom.Pt(-1e9, -1e9), Max: geom.Pt(1e9, 1e9)}}
	found := false
	for _, op := range w.Offscreen().Ops() {
		switch o := op.(type) {
		case *paint.LayerOp:
			c := clips[len(clips)-1]
			if o.Opts.Clip {
				r := geom.Rect{Min: o.Transform.Apply(o.Opts.Bounds.Min), Max: o.Transform.Apply(o.Opts.Bounds.Max)}
				c = geom.Rect{Min: geom.Pt(max(c.Min.X, r.Min.X), max(c.Min.Y, r.Min.Y)), Max: geom.Pt(min(c.Max.X, r.Max.X), min(c.Max.Y, r.Max.Y))}
			}
			clips = append(clips, c)
		case *paint.LayerEndOp:
			clips = clips[:len(clips)-1]
		case *paint.RRectOp:
			if o.Fill.Solid != (color.NRGBA{R: 1, G: 2, B: 3, A: 0xff}) {
				continue
			}
			found = true
			bottom := o.Transform.Apply(o.Rect.Max).Y
			if c := clips[len(clips)-1]; bottom > c.Min.Y {
				t.Fatalf("content painted up to y %v shows over the bar, clipped from y %v", bottom, c.Min.Y)
			}
		}
	}
	if !found {
		t.Fatal("the content's bar was never painted")
	}
}
