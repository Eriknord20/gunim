//go:build linux || windows || darwin

package desktop

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

// TestWindowOpensAtItsPlacement opens a window at a saved placement, and
// reads it back, maximized and not.
func TestWindowOpensAtItsPlacement(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	var work geom.Rect
	for _, m := range display.Monitors() {
		if m.Primary {
			work = m.WorkArea
		}
	}
	if work.Size().W < 400 || work.Size().H < 300 {
		t.Skip("no primary monitor with room for the window")
	}
	saved := geom.Rc(work.Min.X+60, work.Min.Y+80, 320, 200)
	for i, maximized := range []bool{false, true, true} {
		// The last one opens restored, then maximizes, as the user would
		opened := maximized && i < 2
		w, err := display.NewWindow(driver.Options{
			Title: "gunim placement test", Place: &driver.Placement{Bounds: saved, Maximized: opened},
		})
		if err != nil {
			t.Fatal(err)
		}
		if maximized && !opened {
			time.Sleep(100 * time.Millisecond)
			fr, _ := w.(driver.Framer)
			if err := fr.SetMaximized(true); err != nil {
				t.Fatal(err)
			}
		}
		r, _ := w.(driver.PlacementReader)
		var p driver.Placement
		deadline := time.Now().Add(3 * time.Second)
		for {
			var ok bool
			p, ok = r.Placement()
			if ok && p.Maximized == maximized || time.Now().After(deadline) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		_ = w.Close()
		if p.Maximized != maximized {
			// A window manager may refuse to maximize, or take its time
			t.Logf("maximized %v: the window manager left it maximized %v", maximized, p.Maximized)
			continue
		}
		// A window manager may nudge a window a little, as to keep it clear of a panel
		if d := p.Bounds.Min.Sub(saved.Min); abs(d.X) > 2 || abs(d.Y) > 2 || p.Bounds.Size() != saved.Size() {
			t.Errorf("maximized %v: Placement = %v, want %v", maximized, p.Bounds, saved)
		}
	}
}

// A window opened on a monitor sits in the middle of the part windows
// may use, clear of a task bar, and not in the middle of the whole
// screen, where the task bar would cover its bottom.
func TestAWindowOnAMonitorStaysClearOfTheTaskBar(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	var m driver.Monitor
	for _, mon := range display.Monitors() {
		if mon.Primary {
			m = mon
		}
	}
	if m.Bounds.Size().H < 600 || m.Bounds.Size().W < 400 {
		t.Skip("no primary monitor with room for the test")
	}
	// A task bar of 300 pixels along the bottom.
	m.WorkArea = geom.Rect{Min: m.Bounds.Min, Max: geom.Pt(m.Bounds.Max.X, m.Bounds.Max.Y-300)}
	scale := max(m.CoordsPerLogical, 1)
	h := (m.WorkArea.Size().H - 60) / scale
	w, err := display.NewWindow(driver.Options{Title: "gunim work area test", Size: geom.Sz(320, h), Monitor: &m})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	r, _ := w.(driver.PlacementReader)
	var p driver.Placement
	deadline := time.Now().Add(3 * time.Second)
	for {
		var ok bool
		if p, ok = r.Placement(); ok && p.Bounds.Max.Y <= m.WorkArea.Max.Y+1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if p.Bounds.Max.Y > m.WorkArea.Max.Y+1 || p.Bounds.Min.Y < m.WorkArea.Min.Y-1 {
		t.Errorf("the window is at %v, past the work area %v", p.Bounds, m.WorkArea)
	}
}

// A window that draws its own title bar, as the installer does, opens
// in the very middle of the work area: the system's frame it loses as
// it is made counts for nothing.
func TestAChromelessWindowOpensInTheMiddle(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	var m driver.Monitor
	for _, mon := range display.Monitors() {
		if mon.Primary {
			m = mon
		}
	}
	if m.Bounds.Size().H < 600 || m.Bounds.Size().W < 600 {
		t.Skip("no primary monitor with room for the test")
	}
	m.WorkArea = geom.Rect{Min: m.Bounds.Min, Max: geom.Pt(m.Bounds.Max.X, m.Bounds.Max.Y-300)}
	scale := max(m.CoordsPerLogical, 1)
	w, err := display.NewWindow(driver.Options{Title: "gunim chromeless centre test", Size: geom.Sz(320, 200),
		Monitor: &m, Chromeless: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	r, _ := w.(driver.PlacementReader)
	want := geom.Pt((m.WorkArea.Min.X+m.WorkArea.Max.X)/2, (m.WorkArea.Min.Y+m.WorkArea.Max.Y)/2)
	var got geom.Point
	deadline := time.Now().Add(3 * time.Second)
	for {
		if p, ok := r.Placement(); ok {
			got = geom.Pt((p.Bounds.Min.X+p.Bounds.Max.X)/2, (p.Bounds.Min.Y+p.Bounds.Max.Y)/2)
			if d := got.Sub(want); d.X*d.X+d.Y*d.Y <= 4*scale*scale {
				return
			}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("the window's middle is at %v, want the work area's, %v", got, want)
}

// A window opened beside a parent near the screen's edge opens on the
// screen, not past its edge where no one sees it.
func TestAWindowBesideItsParentOpensOnTheScreen(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	var work geom.Rect
	for _, m := range display.Monitors() {
		if m.Primary {
			work = m.WorkArea
		}
	}
	if work.Size().W < 400 || work.Size().H < 300 {
		t.Skip("no primary monitor with room for the window")
	}
	parent, err := display.NewWindow(driver.Options{
		Title: "gunim parent test", Place: &driver.Placement{Bounds: geom.Rc(work.Max.X-200, work.Max.Y-150, 160, 120)},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close() }()
	w, err := display.NewWindow(driver.Options{
		Title: "gunim beside test", Size: geom.Sz(320, 200), Parent: parent, Anchor: geom.Rc(400, 400, 0, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	r, _ := w.(driver.PlacementReader)
	var p driver.Placement
	deadline := time.Now().Add(3 * time.Second)
	for {
		var ok bool
		p, ok = r.Placement()
		if ok || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if p.Bounds.Min.X > work.Max.X-40 || p.Bounds.Min.Y > work.Max.Y-20 {
		t.Errorf("Placement = %v, off the work area %v", p.Bounds, work)
	}
}
