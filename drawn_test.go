package gunim_test

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/paint"
)

// drawnLeaf is a node that paints nothing of its own.
type drawnLeaf struct{ _ int }

func (drawnLeaf) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size { return c.Max }
func (drawnLeaf) Paint(*paint.Painter, gunim.Frame, geom.Size, gunim.Children)          {}

// hider lays its child out and paints it only while shown.
type hider struct {
	child gunim.Node
	shown bool
}

func (h *hider) Children() []gunim.Node { return []gunim.Node{h.child} }

func (h *hider) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kids.At(0).Layout(gunim.Tight(c.Max))
	return c.Max
}

func (h *hider) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	if h.shown {
		kids.At(0).Paint(p)
	}
}

// Drawn says whether the last frame drew a node: true while its parent paints it, false once the parent stops, and
// false on a nil UI.
func TestDrawnSaysWhetherTheLastFrameDrewANode(t *testing.T) {
	leaf := drawnLeaf{}
	h := &hider{child: &leaf, shown: true}
	w := gunimtest.New(t, geom.Sz(100, 100), nil)
	gunim.RegisterView(w, "h", func(struct{}) gunim.Node { return h }, nil)
	if err := w.Client().Mount(gunim.Root, "h", "h", nil); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	var drawn []bool
	gunim.RegisterPatch(w, "h", func(h *hider, show bool, u *gunim.UI) {
		drawn = append(drawn, u.Drawn(&leaf))
		h.shown = show
		u.Invalidate()
	})
	for _, show := range []bool{false, true, true} {
		if err := w.Client().Patch("h", show); err != nil {
			t.Fatal(err)
		}
		w.Frame(time.Second / 60)
	}
	if want := []bool{true, false, true}; len(drawn) != 3 || drawn[0] != want[0] || drawn[1] != want[1] ||
		drawn[2] != want[2] {
		t.Errorf("drawn, hidden and shown again, the leaf read %v, want %v", drawn, want)
	}
	var nilUI *gunim.UI
	if nilUI.Drawn(&leaf) {
		t.Error("a nil UI says the leaf is drawn")
	}
}
