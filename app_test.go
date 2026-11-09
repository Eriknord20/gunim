package gunim

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// pumpDriver runs ready and then blocks until ctx ends, the way a
// platform event loop does.
type pumpDriver struct {
	err error
}

func (d *pumpDriver) Run(ctx context.Context, ready func()) error {
	ready()
	<-ctx.Done()
	return d.err
}

func (d *pumpDriver) NewWindow(driver.Options) (driver.Window, error) {
	return driver.Offscreen(geom.Sz(800, 600)), nil
}

func (d *pumpDriver) Monitors() []driver.Monitor { return nil }

func TestMainReturnsTheErrorFromFn(t *testing.T) {
	want := errors.New("fn failed")
	err := runApp(context.Background(), &pumpDriver{}, func(*App) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestMainJoinsTheDriverError(t *testing.T) {
	fnErr := errors.New("fn failed")
	runErr := errors.New("pump failed")
	err := runApp(context.Background(), &pumpDriver{err: runErr}, func(*App) error { return fnErr })
	if !errors.Is(err, fnErr) || !errors.Is(err, runErr) {
		t.Fatalf("err = %v, want both %v and %v", err, fnErr, runErr)
	}
}

func TestMainReturnsNilWhenFnSucceeds(t *testing.T) {
	if err := runApp(context.Background(), &pumpDriver{}, func(*App) error { return nil }); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

// failingWindow is an offscreen window whose Present always fails.
type failingWindow struct {
	*driver.OffscreenWindow
	err error
}

func (w failingWindow) Present([]paint.Op, []geom.Rect) error { return w.err }

func TestFailedPresentClosesTheWindowOnce(t *testing.T) {
	want := errors.New("lost the GL context")
	w := newWindow(failingWindow{driver.Offscreen(geom.Sz(800, 600)), want}, nil)

	w.Frame(time.Second / 60)
	if !errors.Is(w.Err(), want) {
		t.Fatalf("Err = %v, want %v", w.Err(), want)
	}
	if err := w.Client().Update(Root, nil); !errors.Is(err, ErrWindowClosed) {
		t.Fatalf("Send after a failed present = %v, want ErrWindowClosed", err)
	}

	// The application closing a window that already closed itself is
	// ordinary, and must not panic on a second close of done.
	w.Close()
	w.Frame(time.Second / 60)
}

// textWindow is an offscreen window that records what the engine says
// about taking text.
type textWindow struct {
	*driver.OffscreenWindow
	active []bool
}

func (w *textWindow) SetTextInput(active bool) { w.active = append(w.active, active) }

type takesText struct{ recorder }

func (*takesText) TakesText() bool { return true }

func TestFocusTellsTheDriverWhenTextIsTaken(t *testing.T) {
	tw := &textWindow{OffscreenWindow: driver.Offscreen(geom.Sz(800, 600))}
	w := newWindow(tw, nil)
	field, other := &takesText{}, &recorder{}
	w.ui.Insert(w.ui.Root(), field)
	w.ui.Insert(w.ui.Root(), other)

	w.ui.Focus(field)
	w.ui.Focus(other)
	if len(tw.active) != 2 || !tw.active[0] || tw.active[1] {
		t.Fatalf("text input went %v, want on for the field and off after it", tw.active)
	}
}

func TestFocusMovingBetweenTextTakersEndsTheComposition(t *testing.T) {
	// A composition belongs to the node it was typed into, so moving to
	// another node that takes text ends it on the way.
	tw := &textWindow{OffscreenWindow: driver.Offscreen(geom.Sz(800, 600))}
	w := newWindow(tw, nil)
	field, notes := &takesText{}, &takesText{}
	w.ui.Insert(w.ui.Root(), field)
	w.ui.Insert(w.ui.Root(), notes)

	w.ui.Focus(field)
	w.ui.Focus(notes)
	want := []bool{true, false, true}
	if !slices.Equal(tw.active, want) {
		t.Fatalf("text input went %v, want %v", tw.active, want)
	}
}

// caretWindow is an offscreen window that records the text caret.
type caretWindow struct {
	*driver.OffscreenWindow
	carets []geom.Rect
}

func (w *caretWindow) SetTextCaret(r geom.Rect) { w.carets = append(w.carets, r) }

type caretNode struct {
	recorder
	at geom.Rect
}

func (*caretNode) TakesText() bool        { return true }
func (n *caretNode) TextCaret() geom.Rect { return n.at }
func (n *caretNode) Focusable() bool      { return true }

func TestTheFocusedCaretReachesTheDriverInWindowSpace(t *testing.T) {
	cw := &caretWindow{OffscreenWindow: driver.Offscreen(geom.Sz(800, 600))}
	w := newWindow(cw, nil)
	st := &stage{t: paint.Translate(geom.Pt(100, 50))}
	n := &caretNode{at: geom.Rc(5, 2, 1.5, 16)}
	w.ui.Insert(w.ui.Root(), st)
	w.ui.Insert(st, n)
	w.ui.Focus(n)
	run(w, 1)
	// The stage places the node at (10, 10) and paints it moved by
	// (100, 50).
	want := geom.Rc(115, 62, 1.5, 16)
	if len(cw.carets) != 1 || cw.carets[0] != want {
		t.Fatalf("carets %v, want one at %v", cw.carets, want)
	}
	run(w, 3)
	if len(cw.carets) != 1 {
		t.Fatalf("an unmoved caret was reported again: %v", cw.carets)
	}
}

// endingDriver runs ready and ends at once, as a platform event loop
// does when the last window closes.
type endingDriver struct{ pumpDriver }

func (d *endingDriver) Run(_ context.Context, ready func()) error {
	ready()
	return nil
}

func TestMainWaitsForFnToFinishAsItEnds(t *testing.T) {
	want := errors.New("fn's last word")
	finished := false
	err := runApp(context.Background(), &endingDriver{}, func(*App) error {
		// Work done as the app ends, as saving, that takes a moment.
		time.Sleep(100 * time.Millisecond)
		finished = true
		return want
	})
	if !finished || !errors.Is(err, want) {
		t.Fatalf("Main returned %v with fn finished %v, want fn's error after it finished", err, finished)
	}
}

func TestMainEndsWhenFnNeverDoes(t *testing.T) {
	was := fnGrace
	fnGrace = 50 * time.Millisecond
	defer func() { fnGrace = was }()
	start := time.Now()
	block := make(chan struct{})
	defer close(block)
	if err := runApp(context.Background(), &endingDriver{}, func(*App) error { <-block; return nil }); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Main took %v to end, with fn stuck", d)
	}
}
