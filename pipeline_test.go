package gunim

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

// releasing is an offscreen window that tells apart when it lets a
// frame's ops go, as the desktop driver does.
type releasing struct {
	*driver.OffscreenWindow
	released chan struct{}
}

func (r *releasing) Released() <-chan struct{} { return r.released }

// With a driver that is a [driver.Releaser], the window builds the next
// frame while the one before waits for the screen: two frames handed
// and not yet shown at most, the next built only once the frame before
// the last is released and the first is shown, each due a refresh after
// the one before.
func TestAFrameIsBuiltWhileTheOneBeforeWaitsForTheScreen(t *testing.T) {
	off := driver.Offscreen(geom.Sz(200, 100))
	dw := &releasing{OffscreenWindow: off, released: make(chan struct{}, 2)}
	w := newWindow(dw, nil)
	w.ui.invalid = true
	if !w.free() {
		t.Fatal("a window with no frame handed may not build one")
	}
	w.draw()
	first := w.due
	if !w.free() {
		t.Fatal("with one frame handed, the window may not build the next while it waits for the screen")
	}
	w.ui.invalid = true
	w.draw()
	if interval := refreshInterval(dw.RefreshRate()); w.due.Sub(first) < interval {
		t.Fatalf("the second frame is due %v after the first, want a refresh, %v, at least", w.due.Sub(first), interval)
	}
	if w.free() {
		t.Fatal("with two frames handed, the window builds a third")
	}
	// The first is drawn and let go: still two wait for the screen.
	dw.released <- struct{}{}
	if !w.wait() || w.free() {
		t.Fatalf("with the first frame released and none shown, free %v", w.free())
	}
	// The first is shown: the third may be built.
	go off.Tick()
	if !w.wait() || !w.free() {
		t.Fatalf("with the first frame released and shown, the window may not build the next")
	}
	// A frame shown but not released holds the next frame back.
	w.ui.invalid = true
	w.draw()
	go off.Tick()
	w.wait()
	if w.free() {
		t.Fatal("with the second frame shown but not released, the window builds into its buffers")
	}
	dw.released <- struct{}{}
	w.wait()
	if !w.free() {
		t.Fatal("with the second frame released and shown, the window may not build the next")
	}
}

// A driver that is no Releaser is handed one frame at a time, which it
// lets go as it shows it.
func TestAFrameWaitsForTheOneBeforeWithoutRelease(t *testing.T) {
	w := newTestWindow()
	w.ui.invalid = true
	w.draw()
	if w.free() {
		t.Fatal("a driver that tells no release is handed a second frame before the first is shown")
	}
	go w.Offscreen().Tick()
	done := make(chan bool, 1)
	go func() { done <- w.wait() }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the frame shown never woke the window")
	}
	if !w.free() {
		t.Fatal("the frame before shown, the window may not build the next")
	}
}
