package gunim

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// stillNode heads its spring for where it is told.
type stillNode struct{ v *anim.Float }

func (n *stillNode) Step(dt time.Duration) bool                        { return n.v.Step(dt) }
func (*stillNode) Layout(c Constraints, _ Frame, _ Children) geom.Size { return c.Max }
func (*stillNode) Paint(*paint.Painter, Frame, geom.Size, Children)    {}

func TestAWindowIsStillOnceItsSpringsLand(t *testing.T) {
	w := NewOffscreen(geom.Sz(200, 100), nil)
	n := &stillNode{v: anim.NewFloat(0)}
	RegisterView(w, "still", func(float32) *stillNode { return n }, func(n *stillNode, to float32, _ *UI) {
		n.v.Animate(to, anim.Snappy)
	})
	c := w.Client()
	if err := c.Mount(Root, "still", "still", float32(0), "still"); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		w.Frame(time.Second / 60)
	}
	if !w.Still() {
		t.Fatal("a window with nothing to do is not still")
	}
	if err := c.Publish("still", float32(100)); err != nil {
		t.Fatal(err)
	}
	if w.Still() {
		t.Fatal("a window with a command queued is still")
	}
	w.Frame(time.Second / 60)
	if w.Still() {
		t.Fatal("a window whose spring has just set off is still")
	}
	frames := 0
	for !w.Still() {
		frames++
		if frames > 120 {
			t.Fatal("the spring never landed")
		}
		w.Frame(time.Second / 60)
	}
	if got := n.v.Value(); got != 100 {
		t.Fatalf("the window is still with the spring at %v, want 100", got)
	}
}
