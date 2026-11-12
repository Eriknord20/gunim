package gunim

import (
	"context"
	"testing"

	"github.com/marrasen/gunim/geom"
)

// A window asked for a least size passes it to the driver, and one that would open smaller opens at it.
func TestAWindowOpensNoSmallerThanItsLeastSize(t *testing.T) {
	d := &optionsDriver{}
	var size geom.Size
	err := runApp(context.Background(), d, func(a *App) error {
		w, err := a.NewWindow(WindowOptions{Title: "least", Size: geom.Sz(400, 300), MinSize: geom.Sz(500, 250)})
		if err != nil {
			return err
		}
		size = w.ui.MinSize()
		w.Close()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.got.MinSize != geom.Sz(500, 250) || size != geom.Sz(500, 250) {
		t.Errorf("the driver was asked for a least size of %v, and the window reports %v", d.got.MinSize, size)
	}

	w := NewOffscreenWith(WindowOptions{Size: geom.Sz(400, 300), MinSize: geom.Sz(500, 250)})
	if got := w.dw.Size(); got != geom.Sz(500, 300) {
		t.Errorf("a window of 400×300 with a least size of 500×250 opened at %v", got)
	}
	big := NewOffscreenWith(WindowOptions{Size: geom.Sz(900, 700), MinSize: geom.Sz(500, 250)})
	if got := big.dw.Size(); got != geom.Sz(900, 700) {
		t.Errorf("a window larger than its least size opened at %v", got)
	}
}

// A least size set while the window is open grows a smaller window, holds it against the user's sizing, follows the
// zoom, and lets go when set to zero.
func TestALeastSizeHoldsTheWindowWhileOpen(t *testing.T) {
	w := NewOffscreenWith(WindowOptions{Size: geom.Sz(600, 400)})
	o := w.Offscreen()
	run(w, 1)
	if err := w.ui.SetMinSize(geom.Sz(700, 300)); err != nil {
		t.Fatal(err)
	}
	run(w, 1)
	if got := w.dw.Size(); got != geom.Sz(700, 400) {
		t.Fatalf("a window of 600×400 given a least size of 700×300 is %v", got)
	}
	o.Resize(geom.Sz(200, 200))
	if got := w.dw.Size(); got != geom.Sz(700, 300) {
		t.Errorf("sized to 200×200 under a least size of 700×300, the window is %v", got)
	}
	// Zoomed, the content is laid out in fewer logical pixels, so the window grows to hold the least size.
	w.ui.SetZoom(2)
	if got := w.dw.Size(); got != geom.Sz(700, 300) {
		t.Errorf("zoomed by 2, the window lays its content out in %v, want its least size", got)
	}
	w.ui.SetZoom(1)
	if err := w.ui.SetMinSize(geom.Size{}); err != nil {
		t.Fatal(err)
	}
	o.Resize(geom.Sz(200, 200))
	if got := w.dw.Size(); got != geom.Sz(200, 200) || w.ui.MinSize() != (geom.Size{}) {
		t.Errorf("with no least size, the window sized to 200×200 is %v, and reports a least size of %v", got,
			w.ui.MinSize())
	}
}
