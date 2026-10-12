package gunim

import (
	"slices"
	"testing"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// edgeBand keeps the swipes in a band wide down its left side, as tall as it is laid out.
type edgeBand struct {
	recorder
	wide float32
}

func (b *edgeBand) EdgeSwipes(size geom.Size) []geom.Rect {
	if b.wide == 0 {
		return nil
	}
	return []geom.Rect{geom.Rc(0, 0, b.wide, size.H)}
}

func wantBands(t *testing.T, w *driver.OffscreenWindow, when string, want ...geom.Rect) {
	t.Helper()
	if got, _ := w.EdgeSwipesKept(); !slices.Equal(got, want) {
		t.Fatalf("%s, the window keeps %v, want %v", when, got, want)
	}
}

func TestAKeeperDrawnHandsTheWindowItsBandOnce(t *testing.T) {
	w, st, _ := newStage(t, paint.Translate(geom.Pt(30, 40)))
	b := &edgeBand{wide: 20}
	w.ui.Insert(st, b)
	run(w, 1)
	// The stage lays its child at (10, 10), 100 by 50, and draws it moved by (30, 40).
	wantBands(t, w.Offscreen(), "with a keeper drawn", geom.Rc(40, 50, 20, 50))
	run(w, 5)
	if _, calls := w.Offscreen().EdgeSwipesKept(); calls != 1 {
		t.Fatalf("over frames that changed nothing the window was handed bands %d times, want once", calls)
	}
}

func TestAWindowWithNoKeeperIsHandedNothing(t *testing.T) {
	w, st, _ := newStage(t, paint.Identity)
	w.ui.Insert(st, &edgeBand{})
	run(w, 3)
	if _, calls := w.Offscreen().EdgeSwipesKept(); calls != 0 {
		t.Fatalf("a keeper with no bands had the window handed bands %d times, want none", calls)
	}
}

func TestTheBandFollowsTheKeeperFrameByFrame(t *testing.T) {
	w, st, _ := newStage(t, paint.Identity)
	b := &edgeBand{wide: 20}
	w.ui.Insert(st, b)
	run(w, 1)
	wantBands(t, w.Offscreen(), "at first", geom.Rc(10, 10, 20, 50))

	// The stage moves it.
	for i := range 3 {
		st.t = paint.Translate(geom.Pt(float32(i+1)*5, 0))
		w.ui.Invalidate()
		run(w, 1)
		wantBands(t, w.Offscreen(), "moved", geom.Rc(10+float32(i+1)*5, 10, 20, 50))
	}

	// It widens its band.
	b.wide = 30
	w.ui.Invalidate()
	run(w, 1)
	wantBands(t, w.Offscreen(), "widened", geom.Rc(25, 10, 30, 50))

	// The stage stops drawing it, then draws it again.
	st.skip = true
	w.ui.Invalidate()
	run(w, 1)
	wantBands(t, w.Offscreen(), "undrawn")
	st.skip = false
	w.ui.Invalidate()
	run(w, 1)
	wantBands(t, w.Offscreen(), "drawn again", geom.Rc(25, 10, 30, 50))
}

func TestTheBandFollowsTheWindowsSize(t *testing.T) {
	w := newTestWindow()
	b := &edgeBand{wide: 24}
	w.ui.Insert(w.ui.Root(), b)
	run(w, 1)
	wantBands(t, w.Offscreen(), "at first", geom.Rc(0, 0, 24, 600))
	// As on rotation.
	w.Offscreen().Resize(geom.Sz(600, 800))
	run(w, 1)
	wantBands(t, w.Offscreen(), "after the window turned", geom.Rc(0, 0, 24, 800))
}

func TestAKeeperRemovedGivesTheEdgesBackAtOnce(t *testing.T) {
	w, st, _ := newStage(t, paint.Identity)
	b, other := &edgeBand{wide: 20}, &edgeBand{wide: 8}
	w.ui.Insert(st, b)
	w.ui.Insert(st, other)
	run(w, 1)
	wantBands(t, w.Offscreen(), "with two keepers", geom.Rc(10, 10, 20, 50), geom.Rc(10, 10, 8, 50))
	w.ui.Remove(b)
	run(w, 1)
	wantBands(t, w.Offscreen(), "the first frame after one was removed", geom.Rc(10, 10, 8, 50))
	w.ui.Remove(other)
	run(w, 1)
	wantBands(t, w.Offscreen(), "with both removed")
	run(w, 60)
	if len(w.ui.edgeSwipeNodes) != 0 {
		t.Fatalf("with the keepers gone, frames still look for them among %d nodes", len(w.ui.edgeSwipeNodes))
	}
}

func TestTheEdgesGoBackWhileTheWindowIsHidden(t *testing.T) {
	w, st, _ := newStage(t, paint.Identity)
	w.ui.Insert(st, &edgeBand{wide: 20})
	run(w, 1)
	w.Input(driver.WindowShown{Shown: false})
	wantBands(t, w.Offscreen(), "with the window hidden")
	w.Input(driver.WindowShown{Shown: true})
	run(w, 1)
	wantBands(t, w.Offscreen(), "with the window shown again", geom.Rc(10, 10, 20, 50))
}

func TestTheEdgesGoBackAsTheWindowLeaves(t *testing.T) {
	w, st, _ := newStage(t, paint.Identity)
	w.ui.Insert(st, &edgeBand{wide: 20})
	run(w, 1)
	w.ui.startLeaving()
	run(w, 1)
	wantBands(t, w.Offscreen(), "with the window leaving")
}

// bandMenu is a menu that keeps a band down its left side.
type bandMenu struct {
	menu
}

func (m *bandMenu) EdgeSwipes(size geom.Size) []geom.Rect {
	return []geom.Rect{geom.Rc(0, 0, 6, size.H)}
}

func TestAPopupsKeeperGoesToThePopupsWindow(t *testing.T) {
	w, _, opener := newStage(t, paint.Identity)
	m := &bandMenu{menu: *newMenu()}
	m.Add(m.in)
	w.ui.OpenPopup(opener, m, PopupOptions{Anchor: geom.Rect{Max: geom.Pt(100, 50)}})
	run(w, 1)
	if len(w.ui.popups) != 1 || w.ui.popups[0].dw == nil {
		t.Fatal("the popup's window did not open")
	}
	wantBands(t, popupWindow(t, w), "in the popup", geom.Rc(0, 0, 6, 80))
	if _, calls := w.Offscreen().EdgeSwipesKept(); calls != 0 {
		t.Fatalf("the popup's keeper had the main window handed bands %d times, want none", calls)
	}
}
