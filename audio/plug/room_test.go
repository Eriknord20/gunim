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
