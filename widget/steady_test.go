package widget

import (
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// steadyOwner moves a spring, and holds a Steady part that draws by it, as a view does.
type steadyOwner struct {
	anim.Group
	moves anim.Moves
	level *anim.Float
	part  gunim.Node
}

func (o *steadyOwner) Step(dt time.Duration) bool { return o.moves.Note(o.Group.Step(dt)) }
func (o *steadyOwner) Children() []gunim.Node     { return []gunim.Node{o.part} }
func (o *steadyOwner) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	kid.Layout(c)
	kid.Place(geom.Point{})
	return c.Max
}
func (o *steadyOwner) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// On every frame the part shows the spring where it stands, the frame it lands on too, and once the spring rests the
// part is painted no more.
func TestASteadyPartKeyedOnMovesShowsEveryFrameAndRests(t *testing.T) {
	o := &steadyOwner{level: anim.NewFloat(0)}
	o.Add(o.level)
	painted, drew := 0, float32(-1)
	o.part = NewSteadyPaint(func(p *paint.Painter, _ gunim.Frame, box geom.Size) {
		painted++
		drew = o.level.Value()
		p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(color.NRGBA{R: 0xff, A: 0xff}))
	}, func(gunim.Frame) (any, bool) { return o.moves.Count(), true })
	_, run := stage(t, &frame{child: o, size: geom.Sz(200, 100)})
	run(5)
	o.level.Animate(1, anim.Gentle)
	for i := range 600 {
		run(1)
		if drew != o.level.Value() {
			t.Fatalf("frame %d: the part shows %v with the spring at %v", i, drew, o.level.Value())
		}
	}
	if o.level.Value() != 1 {
		t.Fatalf("the spring stands at %v after 10 s, want at rest at 1", o.level.Value())
	}
	before := painted
	run(30)
	if painted != before {
		t.Fatalf("with the spring at rest, the part was painted %d times more in 30 frames", painted-before)
	}
}

// steadyLeaf counts what the window asks of it.
type steadyLeaf struct {
	laid, painted int
	box           geom.Size
}

func (l *steadyLeaf) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	l.laid++
	return c.Max
}
func (l *steadyLeaf) Paint(_ *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	l.painted++
	l.box = box
}

// A child takes the room it is given, stays carried while the key holds, and is drawn anew in the frame the key changes.
func TestASteadyChildIsCarriedWhileItsKeyHolds(t *testing.T) {
	l := &steadyLeaf{}
	key := 1
	st := NewSteady(l, func(gunim.Frame) (any, bool) { return key, true })
	_, run := stage(t, &frame{child: st, size: geom.Sz(200, 100)})
	run(3)
	if l.box != geom.Sz(200, 100) {
		t.Fatalf("the child was drawn at %v in a tight 200 by 100", l.box)
	}
	laid, painted := l.laid, l.painted
	run(5)
	if l.laid != laid || l.painted != painted {
		t.Fatalf("with its key held, 5 frames laid the child out %d times and painted it %d", l.laid-laid, l.painted-painted)
	}
	key = 2
	run(1)
	if l.laid != laid+1 || l.painted != painted+1 {
		t.Fatalf("the frame its key changed laid the child out %d times and painted it %d, want 1 each", l.laid-laid, l.painted-painted)
	}
	laid, painted = l.laid, l.painted
	run(5)
	if l.laid != laid || l.painted != painted {
		t.Fatalf("with its new key held, 5 frames laid the child out %d times and painted it %d", l.laid-laid, l.painted-painted)
	}
}

// With no key, or one that says not ok, the part is drawn on every frame.
func TestASteadyWithoutAKeyDrawsEveryFrame(t *testing.T) {
	for name, key := range map[string]func(gunim.Frame) (any, bool){
		"nil":    nil,
		"not ok": func(gunim.Frame) (any, bool) { return 1, false },
	} {
		t.Run(name, func(t *testing.T) {
			painted := 0
			st := NewSteadyPaint(func(*paint.Painter, gunim.Frame, geom.Size) { painted++ }, key)
			_, run := stage(t, &frame{child: st, size: geom.Sz(200, 100)})
			run(1)
			before := painted
			run(5)
			if painted != before+5 {
				t.Fatalf("painted %d times in 5 frames, want 5", painted-before)
			}
		})
	}
}
