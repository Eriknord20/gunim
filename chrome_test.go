package gunim

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// titleStrip is a title bar 30 high across the top: caption from 100 on,
// and a maximize button at 300 to 346.
type titleStrip struct{ recorder }

func (t *titleStrip) Layout(c Constraints, _ Frame, _ Children) geom.Size { return c.Max }
func (t *titleStrip) CaptionRects(size geom.Size) []geom.Rect {
	return []geom.Rect{geom.Rc(100, 0, 200, 30)}
}
func (t *titleStrip) MaximizeRect(size geom.Size) geom.Rect { return geom.Rc(300, 0, 46, 30) }

// chromelessStage is a chromeless window holding a title strip.
func chromelessStage(t *testing.T, native bool) (*Window, *driver.OffscreenFrame, *titleStrip) {
	t.Helper()
	w := newTestWindow()
	fr := w.MakeChromeless(native)
	strip := &titleStrip{}
	w.ui.Insert(w.ui.Root(), strip)
	run(w, 1)
	return w, fr, strip
}

// The title bar the frame painted is reported to the system, which
// answers its own questions from it.
func TestAChromelessWindowReportsItsTitleBar(t *testing.T) {
	w, fr, _ := chromelessStage(t, true)
	if !w.ui.Chromeless() {
		t.Fatal("the window says it has the system's title bar")
	}
	if !slices.Equal(fr.Caption(), []geom.Rect{geom.Rc(100, 0, 200, 30)}) || fr.MaximizeButton() != geom.Rc(300, 0, 46, 30) {
		t.Fatalf("the system was told the caption is %v and the maximize button %v", fr.Caption(), fr.MaximizeButton())
	}
}

// Where the system leaves moving and sizing to the engine, a press on
// the caption moves the window, a double click maximizes it, and a
// press at an edge sizes it; none reaches the nodes.
func TestTheEngineMovesAndSizesAChromelessWindow(t *testing.T) {
	w, fr, strip := chromelessStage(t, false)
	w.Input(input.PointerDown{Pos: geom.Pt(150, 15), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	if fr.Moves != 1 || len(strip.events) != 0 {
		t.Fatalf("a press on the caption started %d moves, and the strip saw %v", fr.Moves, strip.events)
	}
	w.Input(input.PointerDown{Pos: geom.Pt(150, 15), Button: input.ButtonPrimary, Clicks: 2, Time: time.Now()})
	if !fr.IsMaximized {
		t.Fatal("a double click on the caption left the window as it was")
	}
	w.ui.handlePlatform(driver.WindowMaximized{Maximized: true})
	if !w.ui.Maximized() {
		t.Fatal("the system said maximized, and the window does not know")
	}
	w.ui.handlePlatform(driver.WindowMaximized{Maximized: false})
	w.Input(input.PointerDown{Pos: geom.Pt(799, 599), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	if !slices.Equal(fr.Resizes, []driver.Edge{driver.EdgeBottomRight}) {
		t.Fatalf("a press in the bottom right corner sized by %v", fr.Resizes)
	}
	w.Input(input.PointerDown{Pos: geom.Pt(50, 300), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	if len(strip.events) == 0 {
		t.Fatal("a press on the content did not reach it")
	}
}

// fixedStrip is a title strip without a maximize button.
type fixedStrip struct{ titleStrip }

func (t *fixedStrip) MaximizeRect(geom.Size) geom.Rect { return geom.Rect{} }

// A title bar without a maximize button reports none to the system,
// and a double click on its caption leaves the window as it is.
func TestATitleBarWithoutMaximizeKeepsTheWindowsSize(t *testing.T) {
	w := newTestWindow()
	fr := w.MakeChromeless(false)
	strip := &fixedStrip{}
	w.ui.Insert(w.ui.Root(), strip)
	run(w, 1)
	if !fr.MaximizeButton().Empty() || len(fr.Caption()) != 1 {
		t.Fatalf("the system was told the caption is %v and the maximize button %v, want no button", fr.Caption(), fr.MaximizeButton())
	}
	w.Input(input.PointerDown{Pos: geom.Pt(150, 15), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	w.Input(input.PointerDown{Pos: geom.Pt(150, 15), Button: input.ButtonPrimary, Clicks: 2, Time: time.Now()})
	if fr.IsMaximized || fr.Moves != 1 {
		t.Fatalf("after a double click on the caption: maximized %v, %d moves; want 1 move and no maximize", fr.IsMaximized, fr.Moves)
	}
}

// Where the system moves and sizes the window itself, the engine takes
// no press.
func TestANativeFrameLeavesPressesToTheNodes(t *testing.T) {
	w, fr, strip := chromelessStage(t, true)
	w.Input(input.PointerDown{Pos: geom.Pt(150, 15), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	if fr.Moves != 0 || len(strip.events) == 0 {
		t.Fatalf("with a native frame, the engine started %d moves and the strip saw %v", fr.Moves, strip.events)
	}
}

// A fixed window keeps its size against the user: the driver is asked
// for it, an edge sizes nothing, and neither a double click on the
// caption nor the maximize button maximizes it, which the system is
// not told of.
func TestAFixedWindowKeepsItsSize(t *testing.T) {
	d := &optionsDriver{}
	err := runApp(context.Background(), d, func(a *App) error {
		w, err := a.NewWindow(WindowOptions{Title: "fixed", Fixed: true})
		if err != nil {
			return err
		}
		w.Close()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !d.got.Fixed {
		t.Error("the driver was not asked for a fixed window")
	}

	w := newTestWindow()
	w.ui.fixed = true
	fr := w.MakeChromeless(false)
	strip := &titleStrip{}
	w.ui.Insert(w.ui.Root(), strip)
	run(w, 1)
	if !fr.MaximizeButton().Empty() || len(fr.Caption()) != 1 {
		t.Fatalf("the system was told the caption is %v and the maximize button %v, want no button", fr.Caption(), fr.MaximizeButton())
	}
	w.Input(input.PointerDown{Pos: geom.Pt(799, 599), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	if len(fr.Resizes) != 0 {
		t.Fatalf("a press in the bottom right corner sized by %v", fr.Resizes)
	}
	w.Input(input.PointerDown{Pos: geom.Pt(150, 15), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	w.Input(input.PointerDown{Pos: geom.Pt(150, 15), Button: input.ButtonPrimary, Clicks: 2, Time: time.Now()})
	w.ui.ToggleMaximize()
	if fr.IsMaximized || fr.Moves != 1 {
		t.Fatalf("maximized %v after %d moves; want 1 move and no maximize", fr.IsMaximized, fr.Moves)
	}
}

// While a popup a press outside dismisses is open, the system is told
// of no caption, so a press on it reaches the window and dismisses the
// popup; a popup that stays, as a tooltip, leaves the caption as it is.
func TestAPopupOpenTakesTheCaptionAway(t *testing.T) {
	w, fr, strip := chromelessStage(t, true)
	caption := []geom.Rect{geom.Rc(100, 0, 200, 30)}
	tip := w.ui.OpenPopup(strip, newMenu(), PopupOptions{Anchor: geom.Rect{Max: geom.Pt(100, 30)}})
	run(w, 2)
	if !slices.Equal(fr.Caption(), caption) {
		t.Fatalf("with a tooltip open, the system was told the caption is %v", fr.Caption())
	}
	tip.Close()
	dismissed := 0
	pop := w.ui.OpenPopup(strip, newMenu(), PopupOptions{Anchor: geom.Rect{Max: geom.Pt(100, 30)},
		Dismiss: func(*UI) { dismissed++ }})
	run(w, 2)
	if len(fr.Caption()) != 0 || fr.MaximizeButton() != geom.Rc(300, 0, 46, 30) {
		t.Fatalf("with a menu open, the system was told the caption is %v and the maximize button %v", fr.Caption(), fr.MaximizeButton())
	}
	w.Input(input.PointerDown{Pos: geom.Pt(150, 15), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	if dismissed != 1 {
		t.Fatalf("a press where the caption was dismissed the menu %d times", dismissed)
	}
	pop.Close()
	run(w, 30)
	if !slices.Equal(fr.Caption(), caption) {
		t.Fatalf("with the menu closed, the system was told the caption is %v", fr.Caption())
	}
}
