package gunim

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// paddedMenu is a menu with room round it for its shadow: 120 by 80, its card the 100 by 60 inside 10 of padding.
type paddedMenu struct{ menu }

func (m *paddedMenu) PopupPadding() geom.Insets { return geom.Uniform(10) }

// springCard is popup content kept at 200 by 200, whose card springs to its width.
type springCard struct {
	fadePanel
	recorder
	w *anim.Float
}

func (c *springCard) Layout(cs Constraints, _ Frame, _ Children) geom.Size {
	return cs.Constrain(geom.Sz(200, 200))
}

func (c *springCard) Paint(*paint.Painter, Frame, geom.Size, Children) {}

func (c *springCard) card() geom.Rect { return geom.Rc(10, 10, c.w.Value(), 50) }

func (c *springCard) Covers(p geom.Point) bool { return c.card().Contains(p) }

func (c *springCard) CoverRects() []geom.Rect { return []geom.Rect{c.card()} }

func openPadded(t *testing.T, dismiss func(*UI)) (*Window, *paddedMenu, *surface) {
	t.Helper()
	w, _, opener := newStage(t, paint.Identity)
	m := &paddedMenu{menu: *newMenu()}
	w.ui.OpenPopup(opener, m, PopupOptions{Anchor: geom.Rect{Max: geom.Pt(100, 50)}, Dismiss: dismiss})
	run(w, 1)
	if len(w.ui.popups) != 1 || w.ui.popups[0].dw == nil {
		t.Fatal("the popup's window did not open")
	}
	return w, m, w.ui.popups[0]
}

func TestPopupTakesThePointerInsideItsPadding(t *testing.T) {
	w, _, _ := openPadded(t, nil)
	pw := popupWindow(t, w)
	got := pw.PointerRegion()
	if want := geom.Rc(10, 10, 100, 60); len(got) != 1 || got[0] != want {
		t.Fatalf("the popup takes the pointer in %v, want [%v]", got, want)
	}
}

func TestPressInAPopupsPaddingIsAPressOutside(t *testing.T) {
	dismissed := 0
	w, m, s := openPadded(t, func(*UI) { dismissed++ })
	w.ui.popupEvent(popupEvent{s: s, ev: input.PointerMove{Pos: geom.Pt(50, 4), Time: time.Now()}})
	w.ui.popupEvent(popupEvent{s: s, ev: input.PointerDown{Pos: geom.Pt(50, 4), Time: time.Now()}})
	if m.got(input.PointerDown{}) || m.got(input.PointerMove{}) || m.got(input.PointerEnter{}) {
		t.Errorf("the menu heard the pointer in its padding: %v", m.events)
	}
	if dismissed != 1 {
		t.Errorf("a press in the padding dismissed the popup %d times, want once", dismissed)
	}
	w.ui.popupEvent(popupEvent{s: s, ev: input.PointerDown{Pos: geom.Pt(50, 30), Time: time.Now()}})
	if !m.got(input.PointerDown{}) {
		t.Error("the menu did not hear a press on its card")
	}
	if dismissed != 1 {
		t.Error("a press on the card dismissed the popup")
	}
}

func TestPopupTakesNoPointerAsItLeaves(t *testing.T) {
	w, _, s := openPadded(t, nil)
	pw := popupWindow(t, w)
	w.ui.closePopup(s)
	pw.Tick()
	run(w, 1)
	if got := pw.PointerRegion(); got == nil || len(got) != 0 {
		t.Errorf("a popup on its way out takes the pointer in %v, want nowhere", got)
	}
}

func TestPopupsPointerRegionFollowsItsCard(t *testing.T) {
	w, _, opener := newStage(t, paint.Identity)
	c := &springCard{fadePanel: *newFadePanel(), w: anim.NewFloat(50)}
	c.Add(c.w)
	w.ui.OpenPopup(opener, c, PopupOptions{Anchor: geom.Rect{Max: geom.Pt(100, 50)}})
	run(w, 1)
	pw := popupWindow(t, w)
	if got, want := pw.PointerRegion(), geom.Rc(10, 10, 50, 50); len(got) != 1 || got[0] != want {
		t.Fatalf("the popup takes the pointer in %v, want [%v]", got, want)
	}
	c.w.Animate(150, anim.Snappy)
	seen := map[float32]bool{}
	for range 120 {
		pw.Tick()
		run(w, 1)
		got := pw.PointerRegion()
		if len(got) != 1 {
			t.Fatalf("the popup takes the pointer in %v, want the card", got)
		}
		if got[0] != c.card() {
			t.Fatalf("the popup takes the pointer in %v while its card is at %v", got[0], c.card())
		}
		seen[got[0].Size().W] = true
	}
	if !seen[150] || len(seen) < 3 {
		t.Errorf("the region went through widths %v, want several on the way to 150", seen)
	}
}

// An injected press in the room round a popup's card passes through to
// the window under it, as a real click does on a display; one on the
// card reaches the popup. A popup the pointer passes through takes none.
func TestAnInjectedPressInAPopupsPaddingReachesTheWindowUnderIt(t *testing.T) {
	w, _, _ := openPadded(t, func(*UI) {})
	pw := popupWindow(t, w)
	main := w.dw.(*driver.OffscreenWindow)
	at := func(p geom.Point) geom.Point { return main.FromScreen(pw.ToScreen(p)) }
	if s, _, ok := w.ui.injectedPopup(input.PointerDown{Pos: at(geom.Pt(4, 4))}); ok {
		t.Fatalf("a press in the padding went to popup %p", s)
	}
	if _, q, ok := w.ui.injectedPopup(input.PointerDown{Pos: at(geom.Pt(30, 30))}); !ok || !near(q.(input.PointerDown).Pos, geom.Pt(30, 30)) {
		t.Fatalf("a press on the card went to the popup %v at %v", ok, q)
	}
	w.ui.popups[0].opts.Passthrough = true
	if _, _, ok := w.ui.injectedPopup(input.PointerDown{Pos: at(geom.Pt(30, 30))}); ok {
		t.Fatal("a popup the pointer passes through took a press")
	}
}
