package gunim

import (
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// turner moves on every frame, as a spinner does, and counts its steps.
type turner struct{ steps int }

func (n *turner) Step(dt time.Duration) bool {
	if dt > 0 {
		n.steps++
	}
	return true
}
func (*turner) Layout(c Constraints, _ Frame, _ Children) geom.Size { return geom.Sz(20, 20) }
func (*turner) Paint(p *paint.Painter, _ Frame, s geom.Size, _ Children) {
	p.RRect(geom.Rect{Max: s.Point()}, 0, paint.Solid(color.NRGBA{A: 0xff}))
}

// ornamentTurner is a turner whose motion only changes how it looks.
type ornamentTurner struct{ turner }

func (*ornamentTurner) Ornament() {}

// viewport places its child at y inside a clip of the window's top half, as
// a scroll does, and paints it wherever it lies.
type viewport struct {
	kid Node
	y   float32
}

func (v *viewport) Children() []Node { return []Node{v.kid} }
func (v *viewport) Layout(c Constraints, _ Frame, kids Children) geom.Size {
	k := kids.At(0)
	k.Layout(Loose(c.Max))
	k.Place(geom.Pt(10, v.y))
	return c.Max
}
func (v *viewport) Paint(p *paint.Painter, _ Frame, s geom.Size, kids Children) {
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: geom.Pt(s.W, s.H/2)}, Opacity: 1, Clip: true})()
	kids.At(0).Paint(p)
}

// mountViewport mounts kid in a viewport of a 200×100 window at y.
func mountViewport(t *testing.T, kid Node, y float32) (*Window, Client) {
	t.Helper()
	w := NewOffscreen(geom.Sz(200, 100), nil)
	RegisterView(w, "viewport", func(y float32) *viewport { return &viewport{kid: kid, y: y} },
		func(v *viewport, y float32, _ *UI) { v.y = y })
	c := w.Client()
	if err := c.Mount(Root, "viewport", "viewport", y, "viewport"); err != nil {
		t.Fatal(err)
	}
	return w, c
}

// frames draws n frames and reports whether the window was still after them.
func frames(w *Window, n int) bool {
	for range n {
		w.Frame(time.Second / 60)
	}
	return w.Still()
}

// An ornament out of sight, below the clip a scroll paints it in, is not
// stepped, and lets the window rest; scrolled back into sight, it moves on.
func TestAnOrnamentOutOfSightLetsTheWindowRest(t *testing.T) {
	n := &ornamentTurner{}
	w, c := mountViewport(t, n, 10)
	if frames(w, 10) {
		t.Fatal("a window with an ornament in sight is still")
	}
	if n.steps == 0 {
		t.Fatal("an ornament in sight was not stepped")
	}
	// Below the clip, which ends half way down, though inside the window.
	if err := c.Update("viewport", float32(70)); err != nil {
		t.Fatal(err)
	}
	if !frames(w, 10) {
		t.Fatal("a window whose only motion is an ornament out of sight is not still")
	}
	steps := n.steps
	frames(w, 10)
	if n.steps != steps {
		t.Fatalf("an ornament out of sight was stepped %d times", n.steps-steps)
	}
	// Partly in sight again: it moves.
	if err := c.Update("viewport", float32(40)); err != nil {
		t.Fatal(err)
	}
	if frames(w, 5) {
		t.Fatal("a window with an ornament back in sight is still")
	}
	if n.steps == steps {
		t.Fatal("an ornament back in sight was not stepped")
	}
}

// An animator that is no ornament is stepped out of sight too: its motion
// may move what is in sight.
func TestAnAnimatorOutOfSightIsStillStepped(t *testing.T) {
	n := &turner{}
	w, _ := mountViewport(t, n, 70)
	if frames(w, 10) {
		t.Fatal("a window with an animator out of sight is still")
	}
	if n.steps < 9 {
		t.Fatalf("an animator out of sight was stepped %d times in 10 frames", n.steps)
	}
}

// An ornament outside the window, as a row scrolled past its bottom with no
// clip round it, lets the window rest too.
func TestAnOrnamentOutsideTheWindowLetsItRest(t *testing.T) {
	n := &ornamentTurner{}
	w := NewOffscreen(geom.Sz(200, 100), nil)
	RegisterView(w, "below", func(y float32) *offset { return &offset{kid: n, y: y} }, nil)
	c := w.Client()
	if err := c.Mount(Root, "below", "below", float32(300), "below"); err != nil {
		t.Fatal(err)
	}
	if !frames(w, 10) {
		t.Fatal("a window whose only motion is an ornament below it is not still")
	}
}

// offset places its child at y, with no clip.
type offset struct {
	kid Node
	y   float32
}

func (v *offset) Children() []Node { return []Node{v.kid} }
func (v *offset) Layout(c Constraints, _ Frame, kids Children) geom.Size {
	k := kids.At(0)
	k.Layout(Loose(c.Max))
	k.Place(geom.Pt(10, v.y))
	return c.Max
}
func (v *offset) Paint(p *paint.Painter, _ Frame, _ geom.Size, kids Children) { kids.At(0).Paint(p) }
