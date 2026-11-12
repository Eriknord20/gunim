package plugui

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// row lays its children out side by side at their own sizes, with no gap, as a tight row of knobs.
type row struct{ kids []gunim.Node }

func (r *row) Children() []gunim.Node { return r.kids }

func (r *row) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	x := float32(0)
	for i := range kids.Len() {
		s := kids.At(i).Layout(gunim.Constraints{Max: c.Max})
		kids.At(i).Place(geom.Pt(x, 0))
		x += s.W
	}
	return c.Max
}

func (r *row) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for i := range kids.Len() {
		kids.At(i).Paint(p)
	}
}

// stageRow mounts kids side by side in a window and returns the window.
func stageRow(t *testing.T, kids ...gunim.Node) *gunim.Window {
	t.Helper()
	w := gunimtest.New(t, geom.Sz(400, 120), nil)
	r := &row{kids: kids}
	gunim.RegisterView(w, "row", func(struct{}) gunim.Node { return r }, nil)
	if err := w.Client().Mount(gunim.Root, "row", "row", nil); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	return w
}

// ringed is a focusable control of the package, with its ring's spring and its size.
type ringed struct {
	name string
	node gunim.Node
	ring func() *anim.Float
	size geom.Size
	// inside says the ring stays inside the control's width, clear of a neighbour's words.
	inside bool
}

// ringedControls returns one of each focusable control of the package.
func ringedControls() []ringed {
	h := &host{values: map[uint32]float64{3: 100}}
	k := NewKnob(h, release)
	ck := NewKnob(h, release)
	ck.Compact = true
	tp := NewTrimPot(h, release)
	s := NewSelector("Mode", []string{"A", "B", "C"}, nil)
	p := NewPower("gate")
	d := NewActionDial("Learn", nil)
	return []ringed{
		{"knob", k, func() *anim.Float { return k.ring }, geom.Sz(KnobW, KnobH), true},
		{"compact knob", ck, func() *anim.Float { return ck.ring }, geom.Sz(CompactW, CompactH), true},
		{"trim pot", tp, func() *anim.Float { return tp.ring }, geom.Sz(TrimPotSize, TrimPotSize), false},
		{"selector", s, func() *anim.Float { return s.ring }, geom.Sz(64, SelectorH), true},
		{"power", p, func() *anim.Float { return p.ring }, geom.Sz(PowerSize, PowerSize), false},
		{"action dial", d, func() *anim.Float { return d.ring }, geom.Sz(KnobW, KnobH), true},
	}
}

// Tab brings the keyboard to each control, and its ring fades in on a spring; Tab on to the next fades it out.
func TestTabRingsEachControl(t *testing.T) {
	for _, c := range ringedControls() {
		t.Run(c.name, func(t *testing.T) {
			other := NewPower("other")
			w := stageRow(t, c.node, other)
			w.Input(input.KeyPress{Key: input.KeyTab})
			w.Frame(time.Second / 60)
			if v := c.ring().Value(); v <= 0 || v >= 0.99 {
				t.Fatalf("a frame after Tab the ring is at %v, want it fading in", v)
			}
			frames(w, 30)
			if v := c.ring().Value(); v != 1 {
				t.Fatalf("after Tab the ring settles at %v, want 1", v)
			}
			w.Input(input.KeyPress{Key: input.KeyTab})
			w.Frame(time.Second / 60)
			if v := c.ring().Value(); v <= 0.01 || v >= 1 {
				t.Fatalf("a frame after Tab on, the ring is at %v, want it fading out", v)
			}
			frames(w, 30)
			if v := c.ring().Value(); v != 0 {
				t.Fatalf("after Tab on the ring settles at %v, want 0", v)
			}
		})
	}
}

// The ring stays within the control's box and the 3 pixels round it; on a knob-sized control it stays inside the box's
// width, so it never crosses a neighbour's name in a tight row.
func TestTheRingFitsTheControl(t *testing.T) {
	for _, c := range ringedControls() {
		t.Run(c.name, func(t *testing.T) {
			c.node.Layout(gunim.Tight(c.size), gunim.Frame{}, gunim.Children{})
			c.ring().Jump(1)
			var p paint.Painter
			c.node.Paint(&p, gunim.Frame{Scale: 1}, c.size, gunim.Children{})
			var rings []geom.Rect
			for _, op := range p.Ops() {
				if r, ok := op.(*paint.RRectOp); ok && r.Stroke.Width == 2 && r.Fill == (paint.Fill{}) {
					half := r.Stroke.Width / 2
					rings = append(rings, geom.Rect{Min: r.Transform.Apply(r.Rect.Min).Sub(geom.Pt(half, half)),
						Max: r.Transform.Apply(r.Rect.Max).Add(geom.Pt(half, half))})
				}
			}
			if len(rings) != 1 {
				t.Fatalf("focused, it draws %d rings, want 1", len(rings))
			}
			r, reach := rings[0], float32(3)
			if r.Min.X < -reach || r.Min.Y < -reach || r.Max.X > c.size.W+reach || r.Max.Y > c.size.H+reach {
				t.Errorf("the ring runs %v, past the box %v and 3 pixels round it", r, c.size)
			}
			if c.inside && (r.Min.X < 0 || r.Max.X > c.size.W) {
				t.Errorf("the ring runs %v across, past the box's width %v, into a neighbour", r, c.size.W)
			}
		})
	}
}
