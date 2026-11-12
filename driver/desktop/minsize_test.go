//go:build linux || windows || darwin

package desktop

import (
	"image/color"
	"runtime"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// leastWindow opens a window with o on the shared display, and closes it as the test ends. It skips the test where
// the window cannot be checked: with no display, or off Windows, where the window manager sizes the window its own way.
func leastWindow(t *testing.T, o driver.Options) *Window {
	t.Helper()
	if display == nil {
		t.Skip("no display")
	}
	if runtime.GOOS != "windows" {
		t.Skip("the system keeps the least size its own way")
	}
	dw, err := display.NewWindow(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := dw.Close(); err != nil {
			t.Error(err)
		}
	})
	return dw.(*Window)
}

// outer returns w's bounds with its frame, in screen coordinates, and the work area of the monitor at its centre.
func outer(t *testing.T, w *Window) (r, area geom.Rect) {
	t.Helper()
	if err := display.call(func() error {
		x, y, err := w.gw.GetPos()
		if err != nil {
			return err
		}
		ww, wh, err := w.gw.GetSize()
		if err != nil {
			return err
		}
		fl, ft, fr, fb := w.frameSize()
		r = geom.Rect{Min: geom.Pt(float32(x-fl), float32(y-ft)), Max: geom.Pt(float32(x+ww+fr), float32(y+wh+fb))}
		area = workArea(geom.Pt(float32(x+ww/2), float32(y+wh/2)))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return r, area
}

// onArea fails t where r does not lie on area.
func onArea(t *testing.T, what string, r, area geom.Rect) {
	t.Helper()
	if r.Min.X < area.Min.X || r.Min.Y < area.Min.Y || r.Max.X > area.Max.X || r.Max.Y > area.Max.Y {
		t.Errorf("%s, the window lies at %v, off the work area %v", what, r, area)
	}
}

// A window saved near the bottom right corner of its work area that grows to its least size, as it opens or later,
// moves to keep on the work area.
func TestAWindowGrowingToItsLeastSizeKeepsOnTheWorkArea(t *testing.T) {
	probe := leastWindow(t, driver.Options{Title: "gunim least probe", Size: geom.Sz(100, 100)})
	_, area := outer(t, probe)
	f := probe.coordsPerLogical()
	for _, chromeless := range []bool{false, true} {
		place := &driver.Placement{CoordsPerLogical: f, Bounds: geom.Rect{
			Min: geom.Pt(area.Max.X-20-300*f, area.Max.Y-20-200*f), Max: geom.Pt(area.Max.X-20, area.Max.Y-20)}}
		w := leastWindow(t, driver.Options{Title: "gunim least corner", Size: geom.Sz(300, 200), Place: place,
			MinSize: geom.Sz(700, 500), Chromeless: chromeless})
		r, area := outer(t, w)
		onArea(t, "opened near the corner with a least size of 700×500", r, area)
		if r.Size().W < 700*f || r.Size().H < 500*f {
			t.Errorf("opened with a least size of 700×500 at %v, the window is %v", f, r.Size())
		}

		w = leastWindow(t, driver.Options{Title: "gunim least corner", Size: geom.Sz(300, 200), Place: place,
			Chromeless: chromeless})
		if err := w.SetMinSize(geom.Sz(700, 500)); err != nil {
			t.Fatal(err)
		}
		r, area = outer(t, w)
		onArea(t, "given a least size of 700×500 near the corner", r, area)
	}
}

// A window that has drawn a frame grows to a larger least size without waiting for a frame at the new size.
func TestSetMinSizeDoesNotWaitForAFrame(t *testing.T) {
	w := leastWindow(t, driver.Options{Title: "gunim least wait", Size: geom.Sz(300, 200)})
	ops := []paint.Op{&paint.RRectOp{Rect: geom.Rc(10, 10, 100, 60), Fill: paint.Solid(color.NRGBA{R: 0xff, A: 0xff}),
		Transform: paint.Identity}}
	if err := w.Present(ops, nil); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		w.mu.Lock()
		drawn := w.drawnW > 0
		w.mu.Unlock()
		if drawn {
			break
		}
		if time.Now().After(end) {
			t.Fatal("the window drew no frame")
		}
	}
	start := time.Now()
	if err := w.SetMinSize(geom.Sz(500, 400)); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took >= resizeWait*3/4 {
		t.Errorf("growing to a least size of 500×400 took %v", took)
	}
}

// A least size larger than the work area is capped at it: the window opens on the work area and the user can size
// it down to the work area, with the least size set as it opens, while open, and through the zoom.
func TestALeastSizeIsCappedAtTheWorkArea(t *testing.T) {
	sizeTo := func(w *Window, ww, wh int) {
		t.Helper()
		if err := display.call(func() error { return w.gw.SetSize(ww, wh) }); err != nil {
			t.Fatal(err)
		}
	}
	// fits fails t where w lies off its work area, or does not shrink to the work area less its frame.
	fits := func(what string, w *Window) {
		t.Helper()
		r, area := outer(t, w)
		onArea(t, what, r, area)
		sizeTo(w, 100, 100)
		r, area = outer(t, w)
		if r.Size().W > area.Size().W || r.Size().H > area.Size().H {
			t.Errorf("%s, sized to 100×100 the window is %v, larger than the work area %v", what, r.Size(), area)
		}
	}
	w := leastWindow(t, driver.Options{Title: "gunim least huge", Size: geom.Sz(300, 200), MinSize: geom.Sz(6000, 4000)})
	fits("opened with a least size of 6000×4000", w)

	w = leastWindow(t, driver.Options{Title: "gunim least huge", Size: geom.Sz(300, 200)})
	if err := w.SetMinSize(geom.Sz(6000, 4000)); err != nil {
		t.Fatal(err)
	}
	fits("given a least size of 6000×4000", w)

	w = leastWindow(t, driver.Options{Title: "gunim least zoomed", Size: geom.Sz(300, 200)})
	_, area := outer(t, w)
	f := w.coordsPerLogical()
	if err := w.SetMinSize(geom.Sz(area.Size().W*0.75/f, area.Size().H*0.75/f)); err != nil {
		t.Fatal(err)
	}
	w.SetZoom(2)
	if err := display.call(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	fits("zoomed by 2 with a least size of three quarters of the work area", w)
}

// A negative side of a least size counts as none, as the window opens and while it is open.
func TestANegativeLeastSizeIsNone(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	dw, err := display.NewWindow(driver.Options{Title: "gunim least negative", Size: geom.Sz(300, 200),
		MinSize: geom.Sz(-50, 250)})
	if err != nil {
		t.Fatalf("a window with a least size of -50×250 did not open: %v", err)
	}
	defer func() {
		if err := dw.Close(); err != nil {
			t.Error(err)
		}
	}()
	w := dw.(*Window)
	if err := w.SetMinSize(geom.Sz(320, -1)); err != nil {
		t.Errorf("a least size of 320×-1 was refused: %v", err)
	}
}
