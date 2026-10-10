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
