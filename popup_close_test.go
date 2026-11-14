package gunim

import (
	"sync"
	"testing"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// An application closes its window from its own goroutine, as Client's methods allow, while the
// test that drives the offscreen window steps it. Under -race this shows whether Close touches the
// popups while a frame reads them.
func TestClosingAnOffscreenWindowFromTheAppWhileFramesRun(t *testing.T) {
	w, _, _, _ := openMenu(t, nil)
	s := w.ui.popups[0]
	var wg sync.WaitGroup
	wg.Go(func() {
		time.Sleep(time.Millisecond)
		w.Client().Close()
	})
	for range 200 {
		w.Frame(time.Second / 60)
	}
	wg.Wait()
	w.Frame(time.Second / 60)
	if len(w.ui.popups) != 0 {
		t.Fatalf("closed, the window still has %d popups", len(w.ui.popups))
	}
	select {
	case <-s.stop:
	default:
		t.Fatal("closed, the popup's goroutine was not told to stop")
	}
}

// Close from the application's goroutine is safe against every call that drives an offscreen window, not
// only Frame: input to the window, input routed to a popup, input to the popup itself, and the access tree.
func TestClosingAnOffscreenWindowFromTheAppWhileItIsDriven(t *testing.T) {
	w, _, _, pop := openMenu(t, nil)
	s := w.ui.popups[0]
	start, closed := make(chan struct{}), make(chan struct{})
	go func() {
		<-start
		w.Client().Close()
		close(closed)
	}()
	// Drive the window until the Close has returned, and a while after.
	after := 50
	for i := 0; after > 0; i++ {
		if i == 1 {
			close(start)
		}
		select {
		case <-closed:
			after--
		default:
		}
		y := float32(60 + i%20)
		w.Inject(input.PointerMove{Pos: geom.Pt(20, y)})
		w.Input(input.PointerDown{Pos: geom.Pt(300, 200), Button: input.ButtonPrimary, Clicks: 1})
		w.Input(input.PointerUp{Pos: geom.Pt(300, 200), Button: input.ButtonPrimary})
		pop.Input(input.PointerMove{Pos: geom.Pt(10, 10)})
		_ = w.Access()
		w.Frame(time.Second / 60)
	}
	if len(w.ui.popups) != 0 {
		t.Fatalf("closed, the window still has %d popups", len(w.ui.popups))
	}
	select {
	case <-s.stop:
	default:
		t.Fatal("closed, the popup's goroutine was not told to stop")
	}
}

// selfCloser closes its window from inside a frame, as a window that has left or failed to present does.
type selfCloser struct{ w *Window }

func (*selfCloser) Layout(c Constraints, _ Frame, _ Children) geom.Size {
	return c.Constrain(geom.Sz(1, 1))
}
func (*selfCloser) Paint(*paint.Painter, Frame, geom.Size, Children) {}
func (c *selfCloser) Step(time.Duration) bool {
	c.w.Close()
	return false
}

// selfInput hands its window input from inside a frame, as the file manager's scripted drag does.
type selfInput struct{ w *Window }

func (*selfInput) Layout(c Constraints, _ Frame, _ Children) geom.Size {
	return c.Constrain(geom.Sz(1, 1))
}
func (*selfInput) Paint(*paint.Painter, Frame, geom.Size, Children) {}
func (c *selfInput) Step(time.Duration) bool {
	c.w.Input(input.PointerMove{Pos: geom.Pt(20, 20)})
	c.w.Inject(input.PointerMove{Pos: geom.Pt(20, 30)})
	_ = c.w.Access()
	return false
}

// A call that drives the window, made inside a frame, runs there, and does not wait for the frame to end.
func TestDrivingAnOffscreenWindowInsideItsFrame(t *testing.T) {
	w, _, m, _ := openMenu(t, nil)
	w.ui.Insert(m, &selfInput{w: w})
	done := make(chan struct{})
	go func() {
		w.Frame(time.Second / 60)
		w.Input(input.PointerMove{Pos: geom.Pt(30, 30)})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a frame that handed its window input did not end")
	}
}

// A window that closes inside its own frame still closes its popups, by the end of that frame.
func TestAnOffscreenWindowClosedInItsFrameClosesItsPopups(t *testing.T) {
	w, _, m, _ := openMenu(t, nil)
	s := w.ui.popups[0]
	w.ui.Insert(m, &selfCloser{w: w})
	w.Frame(time.Second / 60)
	if len(w.ui.popups) != 0 {
		t.Fatalf("closed, the window still has %d popups", len(w.ui.popups))
	}
	select {
	case <-s.stop:
	default:
		t.Fatal("closed, the popup's goroutine was not told to stop")
	}
}
