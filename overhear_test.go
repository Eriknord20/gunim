package gunim

import (
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// listener is an [Overhearer] that records what it heard.
type listener struct{ heard []input.Event }

func (l *listener) Overhear(e input.Event, _ *UI) { l.heard = append(l.heard, e) }

func (l *listener) Layout(cs Constraints, _ Frame, _ Children) geom.Size {
	return geom.Sz(cs.Max.W, 10)
}
func (l *listener) Paint(*paint.Painter, Frame, geom.Size, Children) {}

func TestAnOverhearerHearsInputOthersTake(t *testing.T) {
	w := newTestWindow()
	l, k := &listener{}, &taker{}
	w.ui.Insert(w.ui.Root(), l)
	w.ui.Insert(w.ui.Root(), k)
	run(w, 1)
	w.ui.Focus(k)
	w.ui.handlePlatform(input.KeyPress{Key: input.KeyF1})
	w.ui.handlePlatform(input.PointerMove{Pos: geom.Pt(5, 15)})
	w.ui.handlePlatform(input.KeyRelease{Key: input.KeyF1})
	if k.took != 1 {
		t.Fatalf("the focused node took %d keys, want 1: overhearing must not take the key", k.took)
	}
	if len(l.heard) != 2 {
		t.Fatalf("the overhearer heard %v, want the key press and the move", l.heard)
	}
	if _, ok := l.heard[0].(input.KeyPress); !ok {
		t.Fatalf("first heard %T, want the key press", l.heard[0])
	}
	if m, ok := l.heard[1].(input.PointerMove); !ok || m.Pos != geom.Pt(5, 15) {
		t.Fatalf("then heard %v, want the move at (5, 15) in the window's space", l.heard[1])
	}
}

// grabber takes the primary press, keeping the pointer until the
// release, and records what it overhears.
type grabber struct{ listener }

func (g *grabber) Handle(e input.Event, _ *UI) bool {
	d, ok := e.(input.PointerDown)
	return ok && d.Button == input.ButtonPrimary
}

// hider lays its child out and paints it only while show says.
type hider struct{ show bool }

func (h *hider) Layout(cs Constraints, _ Frame, kids Children) geom.Size {
	for k := range kids.All {
		k.Layout(Tight(geom.Sz(cs.Max.W, 10)))
	}
	return geom.Sz(cs.Max.W, 10)
}

func (h *hider) Paint(p *paint.Painter, _ Frame, _ geom.Size, kids Children) {
	if !h.show {
		return
	}
	for k := range kids.All {
		k.Paint(p)
	}
}

// A node holding the pointer hears a second button pressed while the
// last frame left it undrawn, as a fader scrolled out of view mid-drag
// is, and hears it once while drawn.
func TestTheNodeHoldingThePointerHearsASecondButtonUndrawn(t *testing.T) {
	for _, show := range []bool{true, false} {
		w := newTestWindow()
		h := &hider{show: true}
		g := &grabber{}
		w.ui.Insert(w.ui.Root(), h)
		w.ui.Insert(h, g)
		run(w, 1)
		w.ui.handlePlatform(input.PointerDown{Pos: geom.Pt(5, 5), Button: input.ButtonPrimary, Clicks: 1})
		h.show = show
		run(w, 1)
		g.heard = nil
		w.ui.handlePlatform(input.PointerDown{Pos: geom.Pt(5, 300), Button: input.ButtonSecondary, Clicks: 1})
		if len(g.heard) != 1 {
			t.Fatalf("drawn %v, the node holding the pointer heard %v, want the second press once", show, g.heard)
		}
		if d, ok := g.heard[0].(input.PointerDown); !ok || d.Button != input.ButtonSecondary {
			t.Fatalf("drawn %v, it heard %v", show, g.heard[0])
		}
	}
}
