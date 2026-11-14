package plug

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// roomy is a view of least size 400×300 with a button at its top left and one at its bottom right.
type roomy struct {
	Room
	first, last *widget.Button
	u           *gunim.UI
}

func newRoomy() *roomy {
	return &roomy{first: widget.NewButton("First"), last: widget.NewButton("Last")}
}

func (r *roomy) Children() []gunim.Node { return []gunim.Node{r.first, r.last} }

func (r *roomy) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	r.u = f.UI()
	laid := r.Lay(c.Max, geom.Sz(400, 300))
	kids.At(0).Layout(gunim.Tight(geom.Sz(100, 32)))
	kids.At(0).Place(geom.Pt(8, 8))
	kids.At(1).Layout(gunim.Tight(geom.Sz(100, 32)))
	kids.At(1).Place(geom.Pt(laid.W-108, laid.H-40))
	return c.Max
}

func (r *roomy) Paint(p *paint.Painter, f gunim.Frame, _ geom.Size, kids gunim.Children) {
	defer r.Room.Paint(p, f)()
	for k := range kids.All {
		k.Paint(p)
	}
}

// show returns an offscreen window of size showing n.
func show(t *testing.T, size geom.Size, n gunim.Node) *gunim.Window {
	t.Helper()
	w := gunim.NewOffscreen(size, nil)
	gunim.RegisterView(w, "room", func(struct{}) gunim.Node { return n }, nil)
	if err := w.Client().Mount(gunim.Root, "room", "room", nil); err != nil {
		t.Fatal(err)
	}
	return w
}

// frames runs n frames of a sixtieth of a second, and returns how far v was scrolled after each.
func frames(w *gunim.Window, v *roomy, n int) []geom.Point {
	at := make([]geom.Point, 0, n)
	for range n {
		w.Frame(time.Second / 60)
		at = append(at, v.Scrolled())
	}
	return at
}

// glides fails t where at jumps: a step larger than most, or a step back.
func glides(t *testing.T, what string, at []geom.Point, most float32) {
	t.Helper()
	for i := 1; i < len(at); i++ {
		dx, dy := at[i].X-at[i-1].X, at[i].Y-at[i-1].Y
		if dx < -0.5 || dy < -0.5 || dx > most || dy > most {
			t.Fatalf("%s, the view jumped from %v to %v on frame %d of %v", what, at[i-1], at[i], i, at)
		}
	}
}

// Under its least size, Tab to a control clipped away scrolls the view until the control shows, gliding there.
func TestTabScrollsAViewUnderItsLeastSizeToTheControl(t *testing.T) {
	v := newRoomy()
	w := show(t, geom.Sz(200, 150), v)
	frames(w, v, 2)
	v.u.Focus(v.first)
	v.u.FocusNext(true)
	if v.u.Focused() != v.last {
		t.Fatalf("Tab moved the keyboard to %v, want the last button", v.u.Focused())
	}
	at := frames(w, v, 60)
	glides(t, "scrolled to the last button", at, 60)
	if at[0] == at[len(at)-1] || at[0] == (geom.Point{}) && at[1] == at[len(at)-1] {
		t.Errorf("the view jumped to where it scrolled: %v", at)
	}
	b, ok := v.u.Bounds(v.last)
	if !ok || b.Min.X < 0 || b.Min.Y < 0 || b.Max.X > 200 || b.Max.Y > 150 {
		t.Errorf("scrolled to the last button, it lies at %v in a 200×150 view", b)
	}
	// Back to the first button, the view scrolls back.
	v.u.FocusNext(false)
	frames(w, v, 60)
	if b, _ := v.u.Bounds(v.first); b.Min.X < 0 || b.Min.Y < 0 {
		t.Errorf("Tab back to the first button left it at %v", b)
	}
}

// Under its least size the wheel scrolls the view, both ways, no further than its ends, and a drag on what takes no
// press scrolls it too.
func TestTheWheelAndADragScrollAViewUnderItsLeastSize(t *testing.T) {
	v := newRoomy()
	w := show(t, geom.Sz(200, 150), v)
	frames(w, v, 2)
	w.Input(input.Scroll{Pos: geom.Pt(150, 100), Delta: geom.Pt(-60, -1000)})
	at := frames(w, v, 60)
	glides(t, "scrolled by the wheel", at, 160)
	if got := v.Scrolled(); got != geom.Pt(60, 150) {
		t.Errorf("scrolled by 60, 1000, a 400×300 view in 200×150 is scrolled to %v, want 60, 150", got)
	}
	w.Input(input.PointerDown{Pos: geom.Pt(150, 60), Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerMove{Pos: geom.Pt(170, 80)})
	w.Input(input.PointerUp{Pos: geom.Pt(170, 80), Button: input.ButtonPrimary})
	frames(w, v, 2)
	if got := v.Scrolled(); got != geom.Pt(40, 130) {
		t.Errorf("dragged by 20, 20 from 60, 150, the view is scrolled to %v, want 40, 130", got)
	}
}

// Under its least size the wheel is the view's along each axis it scrolls, at an end too, and with room it is not.
func TestAViewUnderItsLeastSizeSaysTheWheelIsIts(t *testing.T) {
	var _ gunim.WheelScroller = newRoomy()
	up, side := geom.Pt(0, 100), geom.Pt(100, 0)
	for _, c := range []struct {
		box      geom.Size
		up, side bool
	}{
		{geom.Sz(200, 150), true, true},
		{geom.Sz(400, 150), true, false},
		{geom.Sz(200, 300), true, true}, // a plain wheel scrolls a view that is too narrow alone
		{geom.Sz(400, 300), false, false},
		{geom.Sz(800, 600), false, false},
	} {
		v := newRoomy()
		w := show(t, c.box, v)
		frames(w, v, 2)
		if got := v.ScrollsAlong(up); got != c.up {
			t.Errorf("in %v×%v, the wheel turned up is the view's: %v, want %v", c.box.W, c.box.H, got, c.up)
		}
		if got := v.ScrollsAlong(side); got != c.side {
			t.Errorf("in %v×%v, the wheel turned sideways is the view's: %v, want %v", c.box.W, c.box.H, got, c.side)
		}
		if c.up {
			// Scrolled to its end, the wheel is still the view's.
			w.Input(input.Scroll{Pos: geom.Pt(50, 50), Delta: geom.Pt(-1000, -1000)})
			frames(w, v, 60)
			if !v.ScrollsAlong(geom.Pt(0, -100)) {
				t.Errorf("in %v×%v, scrolled to its end, the wheel is no longer the view's", c.box.W, c.box.H)
			}
		}
	}
}

// At its least size and over it, the view takes no wheel and no press, and paints unmoved.
func TestAViewWithRoomDoesNotScroll(t *testing.T) {
	v := newRoomy()
	w := show(t, geom.Sz(400, 300), v)
	frames(w, v, 2)
	if v.Handle(input.Scroll{Pos: geom.Pt(100, 100), Delta: geom.Pt(0, -100)}, v.u) {
		t.Error("a view at its least size took the wheel")
	}
	if v.Handle(input.PointerDown{Pos: geom.Pt(200, 150), Button: input.ButtonPrimary, Clicks: 1}, v.u) {
		t.Error("a view at its least size took a press")
	}
	if b, _ := v.u.Bounds(v.last); b.Min != geom.Pt(292, 260) {
		t.Errorf("at its least size, the last button lies at %v", b)
	}
}

// Under its least size, ToLaid turns a point in the view's box into the laid-out view's space, and OnScroll runs on
// each frame the scroll moves, and on none once it rests.
func TestARoomTellsTheViewWhereThePointerLiesAndWhenItScrolls(t *testing.T) {
	v := newRoomy()
	var heard []geom.Point
	v.OnScroll = func(u *gunim.UI) {
		if u == nil {
			t.Error("OnScroll was given no UI")
		}
		heard = append(heard, v.Scrolled())
	}
	w := show(t, geom.Sz(200, 150), v)
	frames(w, v, 2)
	w.Input(input.Scroll{Pos: geom.Pt(150, 100), Delta: geom.Pt(0, -60)})
	at := frames(w, v, 60)
	if len(heard) < 5 {
		t.Fatalf("a scroll that glided over %v was heard %d times", at, len(heard))
	}
	for i := 1; i < len(heard); i++ {
		if heard[i] == heard[i-1] {
			t.Errorf("OnScroll ran twice at %v", heard[i])
		}
	}
	if got := heard[len(heard)-1]; got != v.Scrolled() {
		t.Errorf("OnScroll last heard the view at %v; it rests at %v", got, v.Scrolled())
	}
	n := len(heard)
	frames(w, v, 30)
	if len(heard) != n {
		t.Errorf("at rest, OnScroll ran %d times more", len(heard)-n)
	}
	if got := v.ToLaid(geom.Pt(10, 20)); got != geom.Pt(10, 80) {
		t.Errorf("scrolled down by 60, a point at 10, 20 in the box lies at %v in the view, want 10, 80", got)
	}
	// A drag is heard on each move.
	n = len(heard)
	w.Input(input.PointerDown{Pos: geom.Pt(150, 100), Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerMove{Pos: geom.Pt(150, 90)})
	w.Input(input.PointerMove{Pos: geom.Pt(150, 80)})
	w.Input(input.PointerUp{Pos: geom.Pt(150, 80), Button: input.ButtonPrimary})
	if len(heard) != n+2 {
		t.Errorf("a drag of two moves was heard %d times", len(heard)-n)
	}
}

// fades returns the fade of the layer that clips the view, and the scroll bars drawn: their boxes, and their
// alpha.
func fades(w *gunim.Window) (fade geom.Insets, bars []geom.Rect, alpha uint8) {
	bar := widget.ScrollbarColor.Get(nil)
	for _, op := range w.Offscreen().Ops() {
		switch o := op.(type) {
		case *paint.LayerOp:
			if o.Opts.Clip && fade == (geom.Insets{}) {
				fade = o.Opts.Fade
			}
		case *paint.RRectOp:
			c := o.Fill.Solid
			if c.R == bar.R && c.G == bar.G && c.B == bar.B && c.A > 0 && o.Rect.Size().W <= 12 ||
				c.R == bar.R && c.G == bar.G && c.B == bar.B && c.A > 0 && o.Rect.Size().H <= 12 {
				bars, alpha = append(bars, o.Rect), c.A
			}
		}
	}
	return fade, bars, alpha
}

// Under its least size, the view fades at each edge with more past it, and a bar on each axis shows where it stands
// while it scrolls, fading in and out.
func TestARoomFadesItsEdgesAndShowsItsBars(t *testing.T) {
	v := newRoomy()
	w := show(t, geom.Sz(200, 150), v)
	frames(w, v, 2)
	fade, bars, _ := fades(w)
	if fade.Top != 0 || fade.Left != 0 || fade.Bottom <= 0 || fade.Right <= 0 {
		t.Errorf("scrolled to its start, the view fades by %+v", fade)
	}
	if len(bars) != 0 {
		t.Errorf("at rest, the view shows bars at %v", bars)
	}
	w.Input(input.Scroll{Pos: geom.Pt(150, 100), Delta: geom.Pt(-60, -60)})
	var alphas []uint8
	for range 8 {
		w.Frame(time.Second / 60)
		_, _, a := fades(w)
		alphas = append(alphas, a)
	}
	fade, bars, _ = fades(w)
	if fade.Top <= 0 || fade.Left <= 0 || fade.Bottom <= 0 || fade.Right <= 0 {
		t.Errorf("scrolled part way, the view fades by %+v", fade)
	}
	if len(bars) != 2 {
		t.Fatalf("scrolling both ways, the view shows bars at %v", bars)
	}
	if alphas[0] >= alphas[len(alphas)-1] || alphas[0] == 0 && alphas[1] == alphas[len(alphas)-1] {
		t.Errorf("the bars came in at alphas %v", alphas)
	}
	for _, b := range bars {
		if !(geom.Rect{Max: geom.Pt(200, 150)}).Contains(b.Min) || b.Max.X > 200 || b.Max.Y > 150 {
			t.Errorf("a bar lies at %v, outside the 200×150 box", b)
		}
	}
	// At rest a while, the bars fade out, frame by frame.
	alphas = alphas[:0]
	for range 180 {
		w.Frame(time.Second / 60)
		_, _, a := fades(w)
		alphas = append(alphas, a)
	}
	if _, bars, _ := fades(w); len(bars) != 0 {
		t.Errorf("three seconds at rest, the view shows bars at %v", bars)
	}
	steps := 0
	for i := 1; i < len(alphas); i++ {
		if alphas[i] != alphas[i-1] {
			steps++
		}
	}
	if steps < 4 {
		t.Errorf("the bars went out in %d steps: %v", steps, alphas)
	}
	if v.Step(time.Second / 60) {
		t.Error("with its bars gone, the view still moves")
	}
}

// Under its least size, a drag of a bar scrolls the view along it.
func TestADragOfARoomsBarScrollsTheView(t *testing.T) {
	v := newRoomy()
	w := show(t, geom.Sz(200, 150), v)
	frames(w, v, 2)
	w.Input(input.Scroll{Pos: geom.Pt(150, 100), Delta: geom.Pt(0, -10)})
	frames(w, v, 30)
	_, bars, _ := fades(w)
	var down geom.Rect
	for _, b := range bars {
		if b.Size().H > b.Size().W {
			down = b
		}
	}
	if down.Empty() {
		t.Fatalf("scrolled down, the view shows no bar down its side: %v", bars)
	}
	from := v.Scrolled()
	grip := down.Min.Add(geom.Pt(down.Size().W/2, down.Size().H/2))
	w.Input(input.PointerDown{Pos: grip, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerMove{Pos: grip.Add(geom.Pt(0, 20))})
	w.Input(input.PointerUp{Pos: grip.Add(geom.Pt(0, 20)), Button: input.ButtonPrimary})
	frames(w, v, 2)
	_, bars, _ = fades(w)
	var now geom.Rect
	for _, b := range bars {
		if b.Size().H > b.Size().W {
			now = b
		}
	}
	got := v.Scrolled()
	if got.X != from.X || got.Y <= from.Y || now.Min.Y < down.Min.Y+19 || now.Min.Y > down.Min.Y+21 {
		t.Errorf("a drag of the bar at %v by 20 down from %v scrolled the view to %v, the bar to %v", down, from, got,
			now)
	}
}
