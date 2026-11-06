package desktop

import (
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/glfw"
	"github.com/marrasen/gunim/paint"
)

// A window's context, which shares, is not made while a render thread is
// drawing: Mesa crashes if it is. See glfw.ContextCreation.
func TestContextWaitsForDrawing(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	type result struct {
		w   driver.Window
		err error
	}
	glfw.ContextCreation.RLock()
	opened := make(chan result, 1)
	go func() {
		w, err := display.NewWindow(driver.Options{Title: "gunim context", Size: geom.Sz(8, 8), Hidden: true})
		opened <- result{w, err}
	}()
	select {
	case r := <-opened:
		glfw.ContextCreation.RUnlock()
		if r.err != nil {
			t.Fatal(r.err)
		}
		_ = r.w.Close()
		t.Fatal("the context was made while a render thread drew")
	case <-time.After(300 * time.Millisecond):
	}
	glfw.ContextCreation.RUnlock()
	select {
	case r := <-opened:
		if r.err != nil {
			t.Fatal(r.err)
		}
		_ = r.w.Close()
	case <-time.After(10 * time.Second):
		t.Fatal("the window never opened once drawing stopped")
	}
}

// And the other way round: no frame is drawn while a context is being made.
func TestDrawingWaitsForContext(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	w, err := display.NewWindow(driver.Options{Title: "gunim drawing", Size: geom.Sz(64, 64)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	// The first frame shows the render thread is past its start.
	if err := w.Present(nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.Presented():
	case <-time.After(5 * time.Second):
		t.Fatal("the first frame was never shown")
	}

	glfw.ContextCreation.Lock()
	locked := true
	defer func() {
		if locked {
			glfw.ContextCreation.Unlock()
		}
	}()
	if err := w.Present([]paint.Op{&paint.RRectOp{
		Rect:      geom.Rc(4, 4, 40, 40),
		Fill:      paint.Solid(color.NRGBA{G: 0xff, A: 0xff}),
		Transform: paint.Identity,
	}}, []geom.Rect{geom.Rc(0, 0, 64, 64)}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.Presented():
		t.Fatal("a frame was drawn while a context was being made")
	case <-time.After(300 * time.Millisecond):
	}
	glfw.ContextCreation.Unlock()
	locked = false
	select {
	case <-w.Presented():
	case <-time.After(5 * time.Second):
		t.Fatal("the frame was never shown once the context was made")
	}
}
