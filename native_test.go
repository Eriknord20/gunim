package gunim

import (
	"image"
	"slices"
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// hole is a NativeHost that records where it was told it stands.
type hole struct{ told []image.Rectangle }

func (h *hole) PlaceNative(r image.Rectangle, _ *UI)                { h.told = append(h.told, r) }
func (h *hole) Layout(c Constraints, _ Frame, _ Children) geom.Size { return c.Max }
func (h *hole) Paint(*paint.Painter, Frame, geom.Size, Children)    {}

// placer lays its child out at at, size large.
type placer struct {
	at   geom.Point
	size geom.Size
}

func (p *placer) Layout(c Constraints, _ Frame, kids Children) geom.Size {
	for k := range kids.All {
		k.Layout(Tight(p.size))
		k.Place(p.at)
	}
	return c.Max
}

func (p *placer) Paint(pt *paint.Painter, _ Frame, _ geom.Size, kids Children) {
	for k := range kids.All {
		k.Paint(pt)
	}
}

func TestANativeHostHearsWhereItStandsAsItMoves(t *testing.T) {
	w := newTestWindow()
	p := &placer{at: geom.Pt(40, 30), size: geom.Sz(200, 100)}
	h := &hole{}
	w.ui.Insert(w.ui.Root(), p)
	w.ui.Insert(p, h)
	run(w, 3)
	want := make([]image.Rectangle, 0, 2)
	want = append(want, image.Rect(40, 30, 240, 130))
	if !slices.Equal(h.told, want) {
		t.Fatalf("after three frames in one place the host heard %v, want %v once", h.told, want)
	}
	p.at = geom.Pt(10, 30)
	w.ui.Invalidate()
	run(w, 2)
	want = append(want, image.Rect(10, 30, 210, 130))
	if !slices.Equal(h.told, want) {
		t.Fatalf("moved, the host heard %v, want %v", h.told, want)
	}
	w.ui.Remove(h)
	run(w, 60)
	if got := h.told[len(h.told)-1]; !got.Empty() {
		t.Fatalf("gone from the tree, the host last heard %v, want nowhere", got)
	}
	if len(w.ui.nativeNodes) != 0 {
		t.Fatalf("gone from the tree, the host is still watched: %v", w.ui.nativeNodes)
	}
}

func TestReshapeGrowsTheWindowLeftwards(t *testing.T) {
	w := newTestWindow()
	run(w, 1)
	before, _ := w.Placement()
	if err := w.ui.Reshape(geom.Insets{Left: 300}); err != nil {
		t.Fatal(err)
	}
	after, _ := w.Placement()
	if after.Bounds.Max != before.Bounds.Max || after.Bounds.Min.X != before.Bounds.Min.X-300 {
		t.Fatalf("grown 300 leftwards the window went from %v to %v", before.Bounds, after.Bounds)
	}
}

// closer is a hole that closes its child window as the window closes.
type closer struct {
	hole
	closed int
}

func (c *closer) CloseNative(*UI) { c.closed++ }

func TestANativeCloserClosesAsTheWindowCloses(t *testing.T) {
	w := newTestWindow()
	c := &closer{}
	w.ui.Insert(w.ui.Root(), c)
	run(w, 2)
	w.ui.closeNatives()
	if c.closed != 1 || len(w.ui.nativeNodes) != 0 {
		t.Fatalf("as the window closed, the host closed %d times, and %d hosts are watched", c.closed, len(w.ui.nativeNodes))
	}
}
