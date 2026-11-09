package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
)

// titleStage mounts root in a chromeless offscreen window, and returns the pretend system's frame, the title bar the
// engine gave the window, a function that runs n frames, and one that runs fn on the UI goroutine.
func titleStage(t *testing.T, root gunim.Node) (
	fr *driver.OffscreenFrame, bar *TitleBar, run func(int), on func(func(u *gunim.UI)),
) {
	t.Helper()
	gunim.RegisterTitleBar(func() gunim.TitleBar { bar = NewTitleBar(""); return bar })
	t.Cleanup(func() { gunim.RegisterTitleBar(func() gunim.TitleBar { return NewTitleBar("") }) })
	w := gunimtest.New(t, geom.Sz(600, 300), nil)
	fr = w.MakeChromeless(true)
	type do struct{ fn func(u *gunim.UI) }
	gunim.RegisterView(w, "app", func(struct{}) gunim.Node { return root }, nil)
	gunim.RegisterPatch(w, "app", func(_ gunim.Node, d do, u *gunim.UI) { d.fn(u) })
	if err := w.Client().Mount(gunim.Root, "app", "app", nil); err != nil {
		t.Fatal(err)
	}
	run = func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	on = func(fn func(u *gunim.UI)) {
		if err := w.Client().Patch("app", do{fn}); err != nil {
			t.Fatal(err)
		}
		run(1)
	}
	run(2)
	return fr, bar, run, on
}

func TestAChromelessWindowGetsATitleBar(t *testing.T) {
	sp := newSpot(100, 50)
	fr, _, run, on := titleStage(t, sp)
	var r geom.Rect
	on(func(u *gunim.UI) { r, _ = u.Bounds(sp) })
	if r.Min.Y != 30 {
		t.Fatalf("the application's tree starts at %v, want below a title bar 30 high", r.Min.Y)
	}
	if want := geom.Rc(600-2*windowButtonWidth, 0, windowButtonWidth, 30); fr.MaximizeButton() != want {
		t.Fatalf("the system was told the maximize button is at %v, want %v", fr.MaximizeButton(), want)
	}
	if len(fr.Caption()) == 0 || fr.Caption()[0].Min.Y != 0 || fr.Caption()[0].Max.X > 600-3*windowButtonWidth {
		t.Fatalf("the system was told the caption is %v, want the bar left of the buttons", fr.Caption())
	}
	// Full screen, the window has no title bar
	on(func(u *gunim.UI) { u.SetFullScreen(true) })
	run(1)
	on(func(u *gunim.UI) { r, _ = u.Bounds(sp) })
	if r.Min.Y != 0 {
		t.Fatalf("full screen, the application's tree starts at %v, want 0", r.Min.Y)
	}
}

func TestTheTitleBarShowsTheWindowsTitle(t *testing.T) {
	_, bar, _, on := titleStage(t, newSpot(100, 50))
	if bar == nil {
		t.Fatal("the window has no title bar")
	}
	on(func(u *gunim.UI) { u.SetTitle("Letters") })
	if got := bar.title.label.Text; got != "Letters" {
		t.Fatalf("the title bar says %q, want %q", got, "Letters")
	}
}

func TestATitleBarWithANameKeepsItWhateverTheWindowsTitle(t *testing.T) {
	_, bar, run, on := titleStage(t, newSpot(100, 50))
	bar.Name, bar.TitleAtStart = "Music", true
	run(1)
	on(func(u *gunim.UI) { u.SetTitle("Night Drive – Music") })
	if got := bar.title.label.Text; got != "Music" {
		t.Fatalf("the title bar says %q, want its name, %q", got, "Music")
	}
	var r geom.Rect
	on(func(u *gunim.UI) { r, _ = u.Bounds(bar.title.label) })
	if r.Min.X != WindowTitleInset {
		t.Fatalf("the title starts at %v, want %v, at the bar's start", r.Min.X, float32(WindowTitleInset))
	}
}

func TestAnApplicationsOwnWindowButtonsTakeTheTitleBarsPlace(t *testing.T) {
	c := NewWindowControls()
	sp := NewSpacer()
	fr, _, _, on := titleStage(t, Column(Row(sp, c).Grow(sp, 1)))
	var r geom.Rect
	on(func(u *gunim.UI) { r, _ = u.Bounds(c) })
	if r.Min.Y != 0 {
		t.Fatalf("the application's buttons are at %v, want at the top, with no title bar above them", r.Min.Y)
	}
	if fr.MaximizeButton().Min.X != r.Min.X+windowButtonWidth {
		t.Fatalf("the system was told the maximize button is at %v, want the application's, at %v", fr.MaximizeButton(), r)
	}
}

// A title bar can leave its buttons out. Without maximize, the system is told there is no maximize button, and with
// no buttons at all the title has the whole bar, all of it caption.
func TestATitleBarCanLeaveItsButtonsOut(t *testing.T) {
	fr, bar, run, _ := titleStage(t, newSpot(100, 50))
	bar.Compact, bar.NoMaximize = true, true
	run(2)
	if got := bar.controls.buttons(); len(got) != 2 || got[0] != minimizeButton || got[1] != closeButton {
		t.Fatalf("without maximize the bar shows buttons %v, want minimize and close", got)
	}
	if !fr.MaximizeButton().Empty() {
		t.Fatalf("without maximize the system was told the maximize button is at %v", fr.MaximizeButton())
	}
	if want := float32(600 - 2*compactButtonWidth); len(fr.Caption()) == 0 || fr.Caption()[0].Max.X != want {
		t.Fatalf("the system was told the caption is %v, want it to end at %v", fr.Caption(), want)
	}
	bar.NoMinimize, bar.NoClose = true, true
	run(2)
	if got := bar.controls.buttons(); len(got) != 0 {
		t.Fatalf("with every button left out the bar shows %v", got)
	}
	if len(fr.Caption()) == 0 || fr.Caption()[0] != geom.Rc(0, 0, 600, TitleBarCompactHeight.Default()) || !fr.MaximizeButton().Empty() {
		t.Fatalf("with no buttons the caption is %v and the maximize button %v, want the whole bar and none", fr.Caption(), fr.MaximizeButton())
	}
}

// The title is centred on the whole bar, as the eye measures it, not on
// the room the buttons leave; a title too long for that moves towards
// the start, clear of the buttons.
func TestTheTitleIsCentredOnTheWholeBar(t *testing.T) {
	_, bar, _, on := titleStage(t, newSpot(100, 50))
	on(func(u *gunim.UI) { u.SetTitle("Letters") })
	var r, buttons geom.Rect
	on(func(u *gunim.UI) {
		r, _ = u.Bounds(bar.title.label)
		buttons, _ = u.Bounds(bar.controls)
	})
	if mid := (r.Min.X + r.Max.X) / 2; mid < 299 || mid > 301 {
		t.Fatalf("the title's middle is at %v, want the bar's, 300", mid)
	}
	on(func(u *gunim.UI) {
		u.SetTitle("A title long enough that centred on the bar it would run under the window's buttons")
	})
	on(func(u *gunim.UI) { r, _ = u.Bounds(bar.title.label) })
	if r.Max.X > buttons.Min.X+0.5 || r.Min.X < 0 {
		t.Fatalf("the long title spans %v to %v, and the buttons start at %v", r.Min.X, r.Max.X, buttons.Min.X)
	}
}
