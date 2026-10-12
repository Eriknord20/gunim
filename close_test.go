package gunim

import (
	"errors"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// quitAsked is what the application hears when the user asks to close
// its window.
type quitAsked struct{}

func TestAWindowThatAsksToCloseStaysOpenUntilTheApplicationCloses(t *testing.T) {
	w := newTestWindow()
	w.askToClose = quitAsked{}
	d := w.mustOffscreen(t)
	go w.loop()
	d.Post(driver.CloseAsked{})
	select {
	case env := <-w.Client().Intents():
		if env.Intent != (quitAsked{}) {
			t.Fatalf("asked to close, the application heard %#v", env.Intent)
		}
	case <-time.After(time.Second):
		t.Fatal("asked to close, the application heard nothing")
	}
	// Still open: it takes input, and its intents stay open.
	d.Post(driver.WindowFocus{Focused: true})
	w.Client().Close()
	for range w.Client().Intents() {
	}
}

func TestAWindowCloses(t *testing.T) {
	w := newTestWindow()
	d := w.mustOffscreen(t)
	go w.loop()
	d.Post(driver.CloseAsked{})
	select {
	case _, open := <-w.Client().Intents():
		if open {
			t.Fatal("asked to close, the window sent an intent")
		}
	case <-time.After(time.Second):
		t.Fatal("asked to close, the window stayed open")
	}
}

func TestAnOffscreenWindowHasNoPixelsToShoot(t *testing.T) {
	w := newTestWindow()
	if _, err := w.Client().Shot(t.Context()); !errors.Is(err, ErrNoPixels) {
		t.Fatalf("shot offscreen: %v", err)
	}
}

// keyTaker says which keys reach it.
type keyTaker struct{}

func (keyTaker) Layout(c Constraints, _ Frame, _ Children) geom.Size { return c.Max }
func (keyTaker) Paint(*paint.Painter, Frame, geom.Size, Children)    {}
func (keyTaker) Focusable() bool                                     { return true }
func (k keyTaker) Handle(e input.Event, u *UI) bool {
	if p, ok := e.(input.KeyPress); ok {
		u.Send(k, p.Key)
		return true
	}
	return false
}

func TestInputHandedToTheWindowReachesTheFocus(t *testing.T) {
	w := newTestWindow()
	RegisterView(w, "taker", func(struct{}) Node { return keyTaker{} }, nil)
	c := w.Client()
	go w.loop()
	defer w.Close()
	if err := c.Mount(Root, "taker", "taker", struct{}{}); err != nil {
		t.Fatal(err)
	}
	if err := c.Focus("taker"); err != nil {
		t.Fatal(err)
	}
	// The focus moves at the next frame, and the key is handed over at
	// once, so it is handed over until the focus has moved.
	deadline := time.After(time.Second)
	for {
		if err := c.Input(t.Context(), input.KeyPress{Key: input.KeyA}); err != nil {
			t.Fatal(err)
		}
		select {
		case env := <-c.Intents():
			if env.Intent != input.KeyA {
				t.Fatalf("the focus heard %#v", env.Intent)
			}
			return
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatal("the key never reached the focus")
		}
	}
}

func TestAnOffscreenWindowWithAskToCloseAsksAndStaysOpen(t *testing.T) {
	w := NewOffscreenWith(WindowOptions{Title: "Song", Size: geom.Sz(320, 200), AskToClose: quitAsked{}})
	if got := w.Offscreen().Title(); got != "Song" {
		t.Fatalf("the window is titled %q, want %q", got, "Song")
	}
	w.Frame(time.Second / 60)
	w.Input(driver.CloseAsked{})
	w.Frame(time.Second / 60)
	select {
	case env := <-w.Client().Intents():
		if env.Intent != (quitAsked{}) {
			t.Fatalf("asked to close, the application heard %#v", env.Intent)
		}
	default:
		t.Fatal("asked to close, the application heard nothing")
	}
	select {
	case <-w.done:
		t.Fatal("asked to close, the window closed before the application said so")
	default:
	}
	w.Client().Close()
}

func TestAnOffscreenWindowWithNoAskToCloseCloses(t *testing.T) {
	w := NewOffscreenWith(WindowOptions{})
	if got := w.Offscreen().Size(); got != geom.Sz(800, 600) {
		t.Fatalf("a window of no size is %v, want 800 by 600", got)
	}
	w.Frame(time.Second / 60)
	w.Input(driver.CloseAsked{})
	select {
	case <-w.done:
	default:
		t.Fatal("asked to close, the window stayed open")
	}
}
