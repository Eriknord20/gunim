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
	// clear is a box the ring must stay out of: the control's own words, or a section's title beside a switch.
	clear geom.Rect
}

// ringedControls returns one of each focusable control of the package.
func ringedControls() []ringed {
	h := &host{values: map[uint32]float64{3: 100}}
	k := NewKnob(h, release)
	ck := NewKnob(h, release)
	ck.Compact = true
	tp := NewTrimPot(h, release)
	s := NewSelector("Mode", []string{"A", "B", "C"}, nil)
	grid := NewSelector("Side", []string{"A", "B", "C", "D"}, nil)
	grid.Columns = 2
	p := NewPower("gate")
	d := NewActionDial("Learn", nil)
	// words is where a knob-shaped control of cap radius r writes its name and value.
	words := func(r float32, size geom.Size) geom.Rect { return geom.Rc(-20, 4+2*r+4, size.W+40, size.H) }
	return []ringed{
		{"knob", k, func() *anim.Float { return k.ring }, geom.Sz(KnobW, KnobH), true, words(17, geom.Sz(KnobW, KnobH))},
		{"compact knob", ck, func() *anim.Float { return ck.ring }, geom.Sz(CompactW, CompactH), true,
			words(14, geom.Sz(CompactW, CompactH))},
		{"trim pot", tp, func() *anim.Float { return tp.ring }, geom.Sz(TrimPotSize, TrimPotSize), false, geom.Rect{}},
		{"selector", s, func() *anim.Float { return s.ring }, geom.Sz(64, SelectorH), true, words(17, geom.Sz(64, SelectorH))},
		{"selector in a grid", grid, func() *anim.Float { return grid.ring }, geom.Sz(48, SelectorH), true,
			words(17, geom.Sz(48, SelectorH))},
		// A section writes its title 3 px right of its switch; the ring keeps 2 px from it.
		{"power", p, func() *anim.Float { return p.ring }, geom.Sz(PowerSize, PowerSize), false,
			geom.Rc(PowerSize+1, -10, 100, 40)},
		{"action dial", d, func() *anim.Float { return d.ring }, geom.Sz(KnobW, KnobH), true, words(17, geom.Sz(KnobW, KnobH))},
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
			if c.clear != (geom.Rect{}) && overlap(r, c.clear) {
				t.Errorf("the ring runs %v, into %v, where words stand", r, c.clear)
			}
		})
	}
}

// A knob's ring runs between its cap and its arc, touching neither.
func TestAKnobsRingRunsBetweenCapAndArc(t *testing.T) {
	for _, compact := range []bool{false, true} {
		h := &host{values: map[uint32]float64{3: 100}}
		k := NewKnob(h, release)
		k.Compact = compact
		size, r := geom.Sz(KnobW, KnobH), float32(17)
		if compact {
			size, r = geom.Sz(CompactW, CompactH), 14
		}
		k.Layout(gunim.Tight(size), gunim.Frame{}, gunim.Children{})
		k.ring.Jump(1)
		var p paint.Painter
		k.Paint(&p, gunim.Frame{Scale: 1}, size, gunim.Children{})
		for _, op := range p.Ops() {
			if o, ok := op.(*paint.RRectOp); ok && o.Stroke.Width == 2 && o.Fill == (paint.Fill{}) {
				in, out := o.Rect.Size().W/2-1, o.Rect.Size().W/2+1
				if in <= r-6 || out >= r-1.75 {
					t.Errorf("compact %v: the ring runs %v to %v from the middle, want it between the cap, %v, and the arc, %v",
						compact, in, out, r-6, r-1.75)
				}
			}
		}
	}
}

// overlap reports whether a and b share any area.
func overlap(a, b geom.Rect) bool {
	return a.Min.X < b.Max.X && b.Min.X < a.Max.X && a.Min.Y < b.Max.Y && b.Min.Y < a.Max.Y
}
