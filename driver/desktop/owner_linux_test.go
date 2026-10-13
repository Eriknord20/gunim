package desktop

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

// An ordinary window that is Owned is a transient window of its parent's, which the window manager keeps in front
// of the parent; one that is not has no owner. A saved place still places it.
func TestAnOwnedWindowIsATransientOfItsParent(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	p, err := display.NewWindow(driver.Options{Title: "gunim test", Size: geom.Sz(200, 120)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	parent, ok := p.(*Window)
	if !ok {
		t.Fatalf("the window is a %T", p)
	}
	var ph uintptr
	if err := display.call(func() (err error) { ph, err = parent.gw.GetX11Window(); return err }); err != nil {
		t.Fatal(err)
	}
	var work geom.Rect
	for _, m := range display.Monitors() {
		if m.Primary {
			work = m.WorkArea
		}
	}
	saved := geom.Rc(work.Min.X+90, work.Min.Y+110, 240, 160)
	for _, owned := range []bool{false, true} {
		dw, err := display.NewWindow(driver.Options{Title: "gunim owned", Parent: parent, Size: geom.Sz(160, 100), Owned: owned,
			Place: &driver.Placement{Bounds: saved}})
		if err != nil {
			t.Fatal(err)
		}
		w, ok := dw.(*Window)
		if !ok {
			t.Fatalf("the window is a %T", dw)
		}
		var owner uintptr
		if err := display.call(func() (err error) { owner, err = w.gw.Owner(); return err }); err != nil {
			t.Fatal(err)
		}
		var at driver.Placement
		if owned && work.Size().W >= 400 {
			r, _ := dw.(driver.PlacementReader)
			deadline := time.Now().Add(3 * time.Second)
			for {
				at, _ = r.Placement()
				if at.Bounds.Size() == saved.Size() || time.Now().After(deadline) {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if d := at.Bounds.Min.Sub(saved.Min); abs(d.X) > 2 || abs(d.Y) > 2 || at.Bounds.Size() != saved.Size() {
				t.Errorf("an owned window opened at %v, want its place %v", at.Bounds, saved)
			}
		}
		_ = dw.Close()
		switch {
		case owned && owner != ph:
			t.Errorf("an owned window is a transient of %#x, want %#x", owner, ph)
		case !owned && owner != 0:
			t.Errorf("a window is a transient of %#x, want none", owner)
		}
	}
}
