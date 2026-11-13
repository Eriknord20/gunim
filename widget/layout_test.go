package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// spot is a fixed-size node that remembers where it was last drawn, in
// window space, and what pointer events reached it.
type spot struct {
	size   geom.Size
	at     geom.Point
	box    geom.Size
	events []input.Event
}

func newSpot(w, h float32) *spot { return &spot{size: geom.Sz(w, h)} }

func (s *spot) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Constrain(s.size)
}

func (s *spot) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	s.at, s.box = p.Transform().Apply(geom.Point{}), box
}

func (s *spot) Handle(e input.Event, _ *gunim.UI) bool {
	s.events = append(s.events, e)
	_, press := e.(input.PointerDown)
	return press
}

// frame is a node that gives its one child a fixed size, which a test
// can change to see how the child copes with being resized.
type frame struct {
	child gunim.Node
	size  geom.Size
	// wheel and keys count the wheel events and key presses that reach
	// the frame, after its child has passed them on.
	wheel, keys int
	// keysGoOn passes the key presses on past the frame, to the window's key catchers.
	keysGoOn bool
	// handle, when set, hears each event first, and takes those it
	// reports true for.
	handle func(e input.Event, u *gunim.UI) bool
}

func (f *frame) Handle(e input.Event, u *gunim.UI) bool {
	if f.handle != nil && f.handle(e, u) {
		return true
	}
	switch e := e.(type) {
	case input.Scroll:
		f.wheel++
		return true
	case input.KeyPress:
		if e.Key == input.KeyTab {
			return false // for the engine to move focus
		}
		f.keys++
		return !f.keysGoOn
	}
	return false
}

func (f *frame) Children() []gunim.Node { return []gunim.Node{f.child} }

func (f *frame) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	kid.Layout(gunim.Tight(f.size))
	kid.Place(geom.Point{})
	return c.Max
}

func (f *frame) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// spotColumn returns n spots of 100 by 50, as spots and as nodes.
func spotColumn(n int) (spots []*spot, nodes []gunim.Node) {
	for range n {
		s := newSpot(100, 50)
		spots = append(spots, s)
		nodes = append(nodes, s)
	}
	return spots, nodes
}

// stage mounts root in an offscreen window and returns the window and a
// function that runs n frames.
func stage(t *testing.T, root gunim.Node) (w *gunim.Window, run func(n int)) {
	t.Helper()
	w = gunimtest.New(t, geom.Sz(800, 600), nil)
	w.PaintWhenLooked(false)
	gunim.RegisterView(w, "stage", func(struct{}) gunim.Node { return root }, nil)
	if err := w.Client().Mount(gunim.Root, "stage", "stage", nil); err != nil {
		t.Fatal(err)
	}
	run = func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(1)
	return w, run
}

func at(t *testing.T, s *spot, want geom.Point) {
	t.Helper()
	if d := s.at.Sub(want); d.X*d.X+d.Y*d.Y > 0.01 {
		t.Fatalf("drawn at %v, want %v", s.at, want)
	}
}

func TestRowPlacesChildrenWithTheGap(t *testing.T) {
	a, b, c := newSpot(50, 20), newSpot(30, 40), newSpot(10, 10)
	stage(t, &frame{child: Row(a, b, c), size: geom.Sz(400, 100)})
	at(t, a, geom.Pt(0, 0))
	at(t, b, geom.Pt(58, 0))
	at(t, c, geom.Pt(96, 0))
}

func TestColumnStacksDown(t *testing.T) {
	a, b := newSpot(50, 20), newSpot(30, 40)
	stage(t, &frame{child: Column(a, b), size: geom.Sz(400, 300)})
	at(t, a, geom.Pt(0, 0))
	at(t, b, geom.Pt(0, 28))
}

func TestGrowSharesTheSpaceOver(t *testing.T) {
	a, b, c := newSpot(50, 20), newSpot(0, 20), newSpot(0, 20)
	row := Row(a, b, c).Grow(b, 1).Grow(c, 3)
	stage(t, &frame{child: row, size: geom.Sz(466, 100)})
	// 466 - 50 - 2*8 leaves 400: b takes a quarter, c three quarters.
	at(t, b, geom.Pt(58, 0))
	at(t, c, geom.Pt(166, 0))
}

func TestJustifyAndCross(t *testing.T) {
	a, b := newSpot(50, 20), newSpot(50, 40)
	row := Row(a, b)
	row.Justify, row.Cross = JustifyEnd, CrossCenter
	stage(t, &frame{child: row, size: geom.Sz(400, 100)})
	// 108 wide in all, pushed against the right, and centred in the
	// 100 px the frame gives the row.
	at(t, a, geom.Pt(292, 40))
	at(t, b, geom.Pt(350, 30))

	c, d := newSpot(50, 20), newSpot(50, 20)
	spread := Row(c, d)
	spread.Justify = JustifyBetween
	stage(t, &frame{child: spread, size: geom.Sz(400, 100)})
	at(t, d, geom.Pt(350, 0))
}

func TestChildrenSpringIntoAGap(t *testing.T) {
	a, b, c := newSpot(50, 20), newSpot(50, 20), newSpot(50, 20)
	row := Row(a, b, c)
	w, run := stage(t, &frame{child: row, size: geom.Sz(400, 100)})
	at(t, c, geom.Pt(116, 0))

	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, _ removeMiddle, u *gunim.UI) { u.Remove(b) })
	if err := w.Client().Patch("stage", removeMiddle{}); err != nil {
		t.Fatal(err)
	}
	run(3)
	if c.at.X >= 116 || c.at.X <= 58 {
		t.Fatalf("three frames after the middle child left, c is at x %v, want on its way from 116 to 58", c.at.X)
	}
	run(120)
	at(t, c, geom.Pt(58, 0))
}

type removeMiddle struct{}

func TestResizingMovesChildrenAtOnce(t *testing.T) {
	a := newSpot(50, 20)
	row := Row(a)
	row.Justify = JustifyEnd
	fr := &frame{child: row, size: geom.Sz(400, 100)}
	_, run := stage(t, fr)
	at(t, a, geom.Pt(350, 0))
	fr.size = geom.Sz(300, 100)
	run(1)
	at(t, a, geom.Pt(250, 0))
}

func TestScrollGlidesToTheWheelsTarget(t *testing.T) {
	spots, nodes := spotColumn(20)
	first := spots[0]
	sc := NewScroll(Column(nodes...))
	w, run := stage(t, &frame{child: sc, size: geom.Sz(200, 300)})

	w.Input(input.Scroll{Pos: geom.Pt(10, 10), Delta: geom.Pt(0, -200)})
	run(3)
	if y := first.at.Y; y >= 0 || y <= -200 {
		t.Fatalf("three frames in, the first row is at y %v, want on its way to -200", y)
	}
	run(120)
	at(t, first, geom.Pt(0, -200))

	// A click lands on what is under the pointer after scrolling: y 10
	// is 210 down the content, 36 into the fourth row, which starts at
	// 3 * (50 + 8).
	w.Input(input.PointerDown{Pos: geom.Pt(10, 10)})
	fourth := spots[3]
	if len(fourth.events) == 0 {
		t.Fatal("the click missed the row scrolled under the pointer")
	}
	press, ok := fourth.events[len(fourth.events)-1].(input.PointerDown)
	if !ok || press.Pos.Y < 35.9 || press.Pos.Y > 36.1 {
		t.Fatalf("the press reached the fourth row as %v, want at y 36", fourth.events)
	}
}

func TestScrollStopsAtTheEnds(t *testing.T) {
	spots, nodes := spotColumn(10)
	first := spots[0]
	fr := &frame{child: NewScroll(Column(nodes...)), size: geom.Sz(200, 300)}
	w, run := stage(t, fr)

	w.Input(input.Scroll{Pos: geom.Pt(10, 10), Delta: geom.Pt(0, -10000)})
	run(200)
	// 10 rows of 50 with 8 between is 572 tall; 300 of it shows.
	at(t, first, geom.Pt(0, -272))

	w.Input(input.Scroll{Pos: geom.Pt(10, 10), Delta: geom.Pt(0, 10000)})
	run(200)
	at(t, first, geom.Pt(0, 0))
	if fr.wheel != 0 {
		t.Fatalf("setup: %d wheel events passed on while the scroll could move", fr.wheel)
	}
	w.Input(input.Scroll{Pos: geom.Pt(10, 10), Delta: geom.Pt(0, 50)})
	if fr.wheel != 1 {
		t.Fatal("at the top, scrolling further up was taken instead of passed on")
	}
}

func TestLabelWrapsToItsWidth(t *testing.T) {
	l := NewLabel("the quick brown fox jumps over the lazy dog")
	narrow, wide := &frame{child: l, size: geom.Sz(120, 400)}, geom.Sz(1000, 400)
	w, run := stage(t, narrow)
	_ = w
	tall := l.laid.p.Size.H
	narrow.size = wide
	run(1)
	if short := l.laid.p.Size.H; short >= tall {
		t.Fatalf("at 1000 px the label is %v tall, at 120 px %v; want it shorter when wider", short, tall)
	}
}

func TestLabelSetsTextInItsFace(t *testing.T) {
	sans, mono := NewLabel("iiii"), NewLabel("iiii")
	mono.Face = MonoFont
	stage(t, &frame{child: Column(sans, mono), size: geom.Sz(400, 400)})
	if got := sans.laid.face; got != text.Default() {
		t.Fatalf("a label with no face is set in %v, want the default face", got)
	}
	wide := text.GoMono(false, false).Shape("MMMM", 14).Advance
	if got := mono.laid.p.Size.W; got != wide {
		t.Fatalf("iiii in the mono face is %v wide, want %v, as wide as MMMM", got, wide)
	}
}

func TestThemeFontReachesLabels(t *testing.T) {
	l := NewLabel("text")
	w, run := stage(t, &frame{child: l, size: geom.Sz(400, 100)})
	w.RegisterTheme(theme.Make("code", theme.Set(Font, text.GoMono(false, false))))
	if err := w.Client().SetTheme("code"); err != nil {
		t.Fatal(err)
	}
	run(120)
	if got := l.laid.face; got != text.GoMono(false, false) {
		t.Fatalf("after switching to a theme with a mono font, the label is set in %v", got)
	}
}
