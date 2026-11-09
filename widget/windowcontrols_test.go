package widget

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// The buttons minimize, maximize and close the window, and the
// maximize button is reported to the system.
func TestWindowControlsWorkTheWindow(t *testing.T) {
	c := NewWindowControls()
	w := gunimtest.New(t, geom.Sz(600, 300), nil)
	fr := w.MakeChromeless(true)
	sp := NewSpacer()
	row := Row(sp, c).Grow(sp, 1)
	gunim.RegisterView(w, "bar", func(struct{}) gunim.Node { return &frame{child: row, size: geom.Sz(600, 30)} }, nil)
	if err := w.Client().Mount(gunim.Root, "bar", "bar", nil); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(2)
	x0 := float32(600 - 3*windowButtonWidth)
	if want := geom.Rc(x0+windowButtonWidth, 0, windowButtonWidth, 30); fr.MaximizeButton() != want {
		t.Fatalf("the system was told the maximize button is at %v, want %v", fr.MaximizeButton(), want)
	}
	click := func(i int) {
		p := geom.Pt(x0+float32(i)*windowButtonWidth+20, 15)
		w.Input(input.PointerMove{Pos: p, Time: time.Now()})
		w.Input(input.PointerDown{Pos: p, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
		w.Input(input.PointerUp{Pos: p, Button: input.ButtonPrimary, Time: time.Now()})
		run(1)
	}
	click(0)
	if fr.Minimized != 1 {
		t.Fatalf("minimize minimized %d times", fr.Minimized)
	}
	click(1)
	if !fr.IsMaximized {
		t.Fatal("maximize left the window as it was")
	}
	click(2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Client().Input(ctx, input.PointerMove{}); !errors.Is(err, gunim.ErrWindowClosed) {
		t.Fatalf("after close, the window takes input: %v", err)
	}
}

// The pin button keeps the window on top, and a second click lets it go; compact buttons are narrower.
func TestThePinButtonKeepsTheWindowOnTop(t *testing.T) {
	c := NewWindowControls()
	c.Pin, c.Compact = true, true
	w := gunimtest.New(t, geom.Sz(600, 300), nil)
	fr := w.MakeChromeless(true)
	sp := NewSpacer()
	row := Row(sp, c).Grow(sp, 1)
	gunim.RegisterView(w, "bar", func(struct{}) gunim.Node { return &frame{child: row, size: geom.Sz(600, 26)} }, nil)
	if err := w.Client().Mount(gunim.Root, "bar", "bar", nil); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(2)
	x0 := float32(600 - 4*compactButtonWidth)
	if want := geom.Rc(x0+2*compactButtonWidth, 0, compactButtonWidth, 26); fr.MaximizeButton() != want {
		t.Fatalf("the system was told the maximize button is at %v, want %v", fr.MaximizeButton(), want)
	}
	click := func(i int) {
		p := geom.Pt(x0+float32(i)*compactButtonWidth+10, 13)
		w.Input(input.PointerMove{Pos: p, Time: time.Now()})
		w.Input(input.PointerDown{Pos: p, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
		w.Input(input.PointerUp{Pos: p, Button: input.ButtonPrimary, Time: time.Now()})
		run(1)
	}
	click(0)
	if !w.Offscreen().Pinned() || !c.pinned {
		t.Fatalf("after the pin: window pinned %v, button shows %v", w.Offscreen().Pinned(), c.pinned)
	}
	click(0)
	if w.Offscreen().Pinned() {
		t.Fatal("a second click left the window pinned")
	}
	click(1)
	if fr.Minimized != 1 {
		t.Fatalf("minimize, after the pin, minimized %d times", fr.Minimized)
	}
}

// Controls without minimize and maximize show close alone, which still closes the window.
func TestWindowControlsCanShowCloseAlone(t *testing.T) {
	c := NewWindowControls()
	c.NoMinimize, c.NoMaximize = true, true
	w := gunimtest.New(t, geom.Sz(600, 300), nil)
	fr := w.MakeChromeless(true)
	sp := NewSpacer()
	row := Row(sp, c).Grow(sp, 1)
	gunim.RegisterView(w, "bar", func(struct{}) gunim.Node { return &frame{child: row, size: geom.Sz(600, 30)} }, nil)
	if err := w.Client().Mount(gunim.Root, "bar", "bar", nil); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		w.Frame(time.Second / 60)
	}
	if !fr.MaximizeButton().Empty() {
		t.Fatalf("with no maximize button the system was told of one at %v", fr.MaximizeButton())
	}
	if info := c.Access(); len(info.Parts) != 1 || info.Parts[0].Name != "Close" {
		t.Fatalf("the controls tell assistive technology of %v, want Close alone", info.Parts)
	}
	p := geom.Pt(600-windowButtonWidth/2, 15)
	w.Input(input.PointerMove{Pos: p, Time: time.Now()})
	w.Input(input.PointerDown{Pos: p, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	w.Input(input.PointerUp{Pos: p, Button: input.ButtonPrimary, Time: time.Now()})
	w.Frame(time.Second / 60)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Client().Input(ctx, input.PointerMove{}); !errors.Is(err, gunim.ErrWindowClosed) {
		t.Fatalf("after close, the window takes input: %v", err)
	}
}

func TestATitleOnASystemFrameDrawsNothing(t *testing.T) {
	// The system says the title; on a phone, its bar names the
	// application.
	title := NewWindowTitle("Calculator")
	w, _ := stage(t, &frame{child: title, size: geom.Sz(300, 40)})
	for _, op := range w.Offscreen().Ops() {
		if _, ok := op.(*paint.TextOp); ok {
			t.Fatal("the title drew its text on a window with the system's frame")
		}
	}
}
