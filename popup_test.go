package gunim

import (
	"fmt"
	"image/color"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// menu is popup content: 120 by 80, fading in and out, recording input.
type menu struct {
	fadePanel
	recorder
}

func newMenu() *menu {
	m := &menu{fadePanel: *newFadePanel()}
	m.Add(m.in)
	return m
}

func (m *menu) Layout(c Constraints, _ Frame, _ Children) geom.Size {
	return c.Constrain(geom.Sz(120, 80))
}

func (m *menu) Paint(p *paint.Painter, _ Frame, box geom.Size, _ Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 4, paint.Fill{})
}

// openMenu puts a recorder at (10, 10), 100 by 50, and opens a menu
// below it.
func openMenu(t *testing.T, dismiss func(*UI)) (*Window, *recorder, *menu, *Popup) {
	t.Helper()
	w, _, opener := newStage(t, paint.Identity)
	m := newMenu()
	pop := w.ui.OpenPopup(opener, m, PopupOptions{
		Anchor:  geom.Rect{Max: geom.Pt(100, 50)},
		Dismiss: dismiss,
	})
	run(w, 1)
	if len(w.ui.popups) != 1 || w.ui.popups[0].dw == nil {
		t.Fatal("the popup's window did not open")
	}
	return w, opener, m, pop
}

func popupWindow(t *testing.T, w *Window) *driver.OffscreenWindow {
	t.Helper()
	pw, ok := w.ui.popups[0].dw.(*driver.OffscreenWindow)
	if !ok {
		t.Fatalf("the popup is a %T, want an offscreen window", w.ui.popups[0].dw)
	}
	return pw
}

func TestPopupOpensAtItsContentsSizeBesideItsAnchor(t *testing.T) {
	w, _, _, _ := openMenu(t, nil)
	pw := popupWindow(t, w)
	if got := pw.Size(); got != geom.Sz(120, 80) {
		t.Errorf("popup size = %v, want 120x80", got)
	}
	// The anchor is the opener's box, where the stage drew it.
	if got, want := pw.Anchor(), geom.Rc(10, 10, 100, 50); got != want {
		t.Errorf("popup anchor = %v, want %v", got, want)
	}
	if len(pw.Ops()) == 0 {
		t.Error("the popup presented nothing")
	}
}

func TestPopupFollowsItsAnchor(t *testing.T) {
	w, _, _, pop := openMenu(t, nil)
	pop.Move(geom.Rc(0, 50, 100, 0))
	pw := popupWindow(t, w)
	pw.Tick()
	run(w, 1)
	if got, want := pw.Anchor(), geom.Rc(10, 60, 100, 0); got != want {
		t.Errorf("popup anchor = %v, want %v", got, want)
	}
}

func TestAPopupBesideItsAnchorOpensRightOrLeftAndMovesUpOntoTheScreen(t *testing.T) {
	for _, tt := range []struct {
		name string
		area geom.Rect
		want geom.Point
	}{
		// The recorder is at (10, 10), 100 by 50, and the menu 120 by 80.
		{"right of it, top to top", geom.Rc(0, 0, 800, 600), geom.Pt(110, 10)},
		{"left of it, the screen ending on the right", geom.Rc(-300, 0, 500, 600), geom.Pt(-110, 10)},
		{"moved up, the screen ending below", geom.Rc(0, -100, 800, 160), geom.Pt(110, -21)},
		{"no higher than the screen's top", geom.Rc(0, 0, 800, 60), geom.Pt(110, 0)},
	} {
		w, _, opener := newStage(t, paint.Identity)
		w.mustOffscreen(t).SetWorkArea(tt.area)
		w.ui.OpenPopup(opener, newMenu(), PopupOptions{Anchor: geom.Rect{Max: geom.Pt(100, 50)}, Beside: true})
		run(w, 1)
		if got := popupWindow(t, w).Anchor(); got != (geom.Rect{Min: tt.want, Max: tt.want}) {
			t.Errorf("%s: the popup is put at %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestPressInPopupReachesItAndLeavesFocus(t *testing.T) {
	w, opener, m, _ := openMenu(t, func(*UI) { t.Error("a press inside the popup dismissed it") })
	w.ui.Focus(opener)
	s := w.ui.popups[0]
	w.ui.popupEvent(popupEvent{s: s, ev: input.PointerDown{Pos: geom.Pt(30, 20), Time: time.Now()}})
	if !m.got(input.PointerDown{}) {
		t.Fatal("the menu saw no press")
	}
	if got := pressedAt(t, &m.recorder); !near(got, geom.Pt(30, 20)) {
		t.Errorf("press at %v in the menu's space, want (30, 20)", got)
	}
	if w.ui.Focused() != opener {
		t.Error("a press in the popup moved focus")
	}
}

func TestPressOutsideDismisses(t *testing.T) {
	dismissed := 0
	w, _, _, pop := openMenu(t, func(*UI) { dismissed++ })
	press(w, 50, 30) // on the opener
	if dismissed != 0 {
		t.Fatal("a press on the opener dismissed the popup")
	}
	press(w, 500, 500)
	if dismissed != 1 {
		t.Fatalf("a press outside dismissed %d times, want once", dismissed)
	}
	w.ui.handlePlatform(driver.WindowFocus{Focused: false})
	if dismissed != 2 {
		t.Fatal("losing the keyboard did not dismiss the popup")
	}
	if !pop.Open() {
		t.Error("dismissing closed the popup by itself")
	}
}

func TestClosedPopupAnimatesOutThenCloses(t *testing.T) {
	w, _, m, pop := openMenu(t, nil)
	run(w, 60)
	pop.Close()
	if pop.Open() {
		t.Fatal("Open is true after Close")
	}
	run(w, 2)
	if len(w.ui.popups) != 1 || w.ui.Presence(m) != Exiting {
		t.Fatal("the popup closed before its content left")
	}
	run(w, 120)
	if len(w.ui.popups) != 0 {
		t.Fatal("the popup stayed open after its content left")
	}
	if inTree(w.ui, m) {
		t.Error("the content is still in the tree")
	}
}

func TestRemovingTheOpenerClosesThePopup(t *testing.T) {
	w, opener, _, pop := openMenu(t, nil)
	w.ui.Remove(opener)
	if pop.Open() {
		t.Error("the popup stayed open after its opener was removed")
	}
}

// holder records input and holds one child, 50 by 50, at (10, 10).
type holder struct{ recorder }

func (h *holder) Layout(c Constraints, _ Frame, kids Children) geom.Size {
	for k := range kids.All {
		k.Layout(Tight(geom.Sz(50, 50)))
		k.Place(geom.Pt(10, 10))
	}
	return c.Max
}

func (h *holder) Paint(p *paint.Painter, _ Frame, _ geom.Size, kids Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

func TestHoverEntersEveryNodeAroundThePointer(t *testing.T) {
	w := newTestWindow()
	h := &holder{}
	in := &recorder{}
	w.ui.Insert(w.ui.Root(), h)
	w.ui.Insert(h, in)
	run(w, 1)

	move := func(x, y float32) {
		w.ui.handlePlatform(input.PointerMove{Pos: geom.Pt(x, y), Time: time.Now()})
	}
	move(30, 30) // over the child, inside the holder
	if count[input.PointerEnter](&h.recorder) != 1 || count[input.PointerEnter](in) != 1 {
		t.Fatal("entering the child did not enter both")
	}
	move(100, 100) // over the holder alone
	if count[input.PointerLeave](in) != 1 || count[input.PointerLeave](&h.recorder) != 0 {
		t.Fatal("leaving the child for its holder should leave the child alone")
	}
	move(30, 30)
	if count[input.PointerEnter](&h.recorder) != 1 || count[input.PointerEnter](in) != 2 {
		t.Fatal("coming back to the child entered the holder again")
	}
	w.ui.handlePlatform(input.PointerLeave{Time: time.Now()})
	if count[input.PointerLeave](in) != 2 || count[input.PointerLeave](&h.recorder) != 1 {
		t.Fatal("leaving the window did not leave both")
	}
}

// builder builds one child, 50 by 50 at (10, 10), in its first layout.
type builder struct {
	made *recorder
}

func (b *builder) Layout(c Constraints, _ Frame, kids Children) geom.Size {
	if b.made == nil {
		b.made = &recorder{}
		k := kids.Build(b.made)
		k.Layout(Tight(geom.Sz(50, 50)))
		k.Place(geom.Pt(10, 10))
	}
	for k := range kids.All {
		k.Layout(Tight(geom.Sz(50, 50)))
		k.Place(geom.Pt(10, 10))
	}
	return c.Max
}

func (b *builder) Paint(p *paint.Painter, _ Frame, _ geom.Size, kids Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

func TestAChildBuiltInLayoutPaintsAndTakesInputThatFrame(t *testing.T) {
	w := newTestWindow()
	b := &builder{}
	w.ui.Insert(w.ui.Root(), b)
	run(w, 1)
	press(w, 30, 30)
	if !b.made.got(input.PointerDown{}) {
		t.Fatal("the built child took no press after its first frame")
	}
}

// A popup opened from a big node, hanging from a small part of it, as a
// palette hangs from a line across the window: a press on the node but
// off the anchor dismisses it, and a press on the anchor is left to the
// opener.
func TestAPressOnTheOpenerButOffTheAnchorDismisses(t *testing.T) {
	w, _, opener := newStage(t, paint.Identity)
	dismissed := 0
	w.ui.OpenPopup(opener, newMenu(), PopupOptions{
		Anchor:  geom.Rect{Max: geom.Pt(100, 10)},
		Dismiss: func(*UI) { dismissed++ },
	})
	run(w, 1)
	press(w, 50, 15) // on the anchor, the opener at (10, 10)
	if dismissed != 0 {
		t.Fatal("a press on the anchor dismissed the popup")
	}
	press(w, 50, 45) // on the opener, below the anchor
	if dismissed != 1 {
		t.Fatalf("a press on the opener off the anchor dismissed %d times, want once", dismissed)
	}
}

// shown reports the frame a popup's window holds as shown, as a screen
// would, and waits for the report to reach the engine.
func shown(w *Window, pw *driver.OffscreenWindow) {
	pw.Tick()
	for len(w.popupIn) == 0 {
		runtime.Gosched()
	}
}

// A popup's window, once the popup has gone, is hidden and kept, and
// the next popup opens in it rather than in a new one.
func TestAClosedPopupsWindowIsKeptForTheNext(t *testing.T) {
	w, opener, _, pop := openMenu(t, nil)
	first := popupWindow(t, w)
	pop.Close()
	for range 120 {
		if len(w.ui.popups) == 0 {
			break
		}
		shown(w, first)
		run(w, 1)
	}
	if len(w.ui.popups) != 0 || len(w.ui.spare) != 1 || !first.Hidden() {
		t.Fatalf("closed, there are %d popups and %d spare windows, the first hidden %v", len(w.ui.popups), len(w.ui.spare), first.Hidden())
	}
	w.ui.OpenPopup(opener, newMenu(), PopupOptions{Anchor: geom.Rect{Max: geom.Pt(100, 50)}})
	run(w, 1)
	if popupWindow(t, w) != first || first.Hidden() || len(w.ui.spare) != 0 {
		t.Fatalf("the next popup opened in a new window, or the kept one stayed hidden %v", first.Hidden())
	}
}

// card is popup content that draws a card on the top half of its box,
// as a palette kept at its tallest does.
type card struct{ menu }

func (c *card) Covers(p geom.Point) bool { return p.Y < 40 }

// A press in a popup's window where its content draws nothing is a
// press outside: it dismisses the popup, and the content never sees it.
func TestAPressWhereAPopupDrawsNothingDismisses(t *testing.T) {
	w, _, opener := newStage(t, paint.Identity)
	c := &card{menu: *newMenu()}
	c.Add(c.in)
	dismissed := 0
	w.ui.OpenPopup(opener, c, PopupOptions{Anchor: geom.Rect{Max: geom.Pt(100, 50)}, Dismiss: func(*UI) { dismissed++ }})
	run(w, 1)
	s := w.ui.popups[0]
	w.ui.popupEvent(popupEvent{s: s, ev: input.PointerDown{Pos: geom.Pt(30, 20), Time: time.Now()}})
	if dismissed != 0 || !c.got(input.PointerDown{}) {
		t.Fatalf("a press on the card dismissed %d times, and the card saw it %v", dismissed, c.got(input.PointerDown{}))
	}
	c.events = nil
	w.ui.popupEvent(popupEvent{s: s, ev: input.PointerDown{Pos: geom.Pt(30, 60), Time: time.Now()}})
	if dismissed != 1 || c.got(input.PointerDown{}) {
		t.Fatalf("a press below the card dismissed %d times, and the card saw it %v", dismissed, c.got(input.PointerDown{}))
	}
}

// An owned popup opens in an owned window, and a menu after it does not open in that window once it is kept.
func TestAnOwnedPopupKeepsItsWindowToItself(t *testing.T) {
	w, _, opener := newStage(t, paint.Identity)
	var asked []driver.Options
	open := w.open
	w.open = func(o driver.Options) (driver.Window, error) {
		asked = append(asked, o)
		return open(o)
	}
	pop := w.ui.OpenPopup(opener, newMenu(), PopupOptions{Anchor: geom.Rect{Max: geom.Pt(100, 50)}, Owned: true})
	run(w, 1)
	if len(asked) == 0 || !asked[len(asked)-1].Owned {
		t.Fatalf("the owned popup asked for %+v, want a new owned window", asked)
	}
	owned := popupWindow(t, w)
	pop.Close()
	for range 120 {
		if len(w.ui.popups) == 0 {
			break
		}
		shown(w, owned)
		run(w, 1)
	}
	if !owned.Hidden() {
		t.Fatal("the owned popup's window was not kept")
	}
	w.ui.OpenPopup(opener, newMenu(), PopupOptions{Anchor: geom.Rect{Max: geom.Pt(100, 50)}})
	run(w, 1)
	if popupWindow(t, w) == owned {
		t.Fatal("the menu opened in the owned popup's window")
	}
}

// sprites is popup content that draws something new each frame from
// every buffer a painter reuses: a shape, a mask, text and a scene.
type sprites struct {
	menu
	n    int
	mesh *paint.Mesh
}

// fill is a mask shape that covers its box.
type fill struct{}

func (fill) Coverage(w, h int) []byte { return make([]byte, w*h) }
func (fill) Settled() bool            { return true }

func (s *sprites) Paint(p *paint.Painter, _ Frame, box geom.Size, _ Children) {
	s.n++
	at := float32(s.n)
	p.RRect(geom.Rc(at, 0, 10, 10), 2, paint.Solid(color.NRGBA{R: uint8(s.n), A: 0xff}))
	p.Mask(fill{}, geom.Rc(at, 10, 16, 16), color.NRGBA{G: uint8(s.n), A: 0xff})
	p.Text([]paint.Glyph{{ID: uint32(s.n), At: geom.Pt(at, 40)}}, 12, color.NRGBA{A: 0xff}, geom.Rc(at, 30, 20, 12))
	items := []paint.SceneItem{{Mesh: s.mesh, Model: geom.Move3(geom.V3(at, 0, 0))}}
	p.Scene(geom.Rect{Max: box.Point()}, paint.Scene{Items: items})
}

// watched is a popup's window that keeps every frame it is given twice
// over: the engine's own ops, and the copy the offscreen window makes.
type watched struct {
	*driver.OffscreenWindow
	given, copies [][]paint.Op
}

func (w *watched) Present(ops []paint.Op, damage geom.Rect) error {
	err := w.OffscreenWindow.Present(ops, damage)
	w.given, w.copies = append(w.given, ops), append(w.copies, w.Ops())
	return err
}

// A popup records frames while one is on its way to its window, and
// none of them reuses the buffers of the frame in flight or of the one
// the window last showed, which it may draw again at any time.
func TestAPopupLeavesTheFramesItsWindowHoldsAsTheyWere(t *testing.T) {
	w, _, opener := newStage(t, paint.Identity)
	var pw *watched
	open := w.open
	w.open = func(o driver.Options) (driver.Window, error) {
		dw, err := open(o)
		if off, ok := dw.(*driver.OffscreenWindow); ok && err == nil {
			pw = &watched{OffscreenWindow: off}
			return pw, nil
		}
		return dw, err
	}
	content := &sprites{menu: *newMenu(), mesh: paint.NewBox(geom.V3(1, 1, 1), color.NRGBA{A: 0xff})}
	w.ui.OpenPopup(opener, content, PopupOptions{Anchor: geom.Rect{Max: geom.Pt(100, 50)}})
	run(w, 1)
	if pw == nil || len(pw.given) != 1 {
		t.Fatal("the popup's window was not given its first frame")
	}
	// held checks the frames the window holds: the one in flight, and
	// the one it showed before, -1 for none.
	shownAt := -1
	held := func(when string) {
		t.Helper()
		for _, i := range []int{shownAt, len(pw.given) - 1} {
			if i >= 0 && !reflect.DeepEqual(pw.given[i], pw.copies[i]) {
				t.Fatalf("%s, frame %d of %d given to the window changed", when, i, len(pw.given))
			}
		}
	}
	// The window takes one frame, two, three, none, then one again,
	// while the popup records on.
	for cycle, waits := range []int{1, 2, 3, 0, 1, 4} {
		for f := range waits {
			run(w, 1)
			held(fmt.Sprintf("cycle %d, %d frames on", cycle, f+1))
		}
		shownAt = len(pw.given) - 1
		shown(w, pw.OffscreenWindow)
		run(w, 1)
		if len(pw.given) != shownAt+2 {
			t.Fatalf("cycle %d: the frame after the one shown was never given", cycle)
		}
		held(fmt.Sprintf("cycle %d, as the next frame went", cycle))
	}
	if content.n < 15 {
		t.Fatalf("the popup recorded %d frames, want one each run", content.n)
	}
}
