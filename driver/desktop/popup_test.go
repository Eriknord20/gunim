//go:build !js

package desktop

import (
	"testing"

	"github.com/marrasen/gunim/geom"
)

func TestPopupAt(t *testing.T) {
	area := geom.Rc(0, 0, 1920, 1080)
	tests := []struct {
		name   string
		anchor geom.Rect
		w, h   float32
		x, y   float32
	}{
		{"below, at the left edge", geom.Rc(100, 100, 120, 36), 200, 300, 100, 136},
		{"above where the bottom runs out", geom.Rc(100, 900, 120, 36), 200, 300, 100, 600},
		{"below, slid up, when above has less room", geom.Rc(100, 150, 120, 720), 200, 300, 100, 780},
		{"slid in from the right edge", geom.Rc(1850, 100, 60, 36), 200, 300, 1720, 136},
		{"slid in from the left edge", geom.Rc(-50, 100, 60, 36), 200, 300, 0, 136},
	}
	for _, tt := range tests {
		x, y := popupAt(tt.anchor, tt.w, tt.h, area, false)
		if x != tt.x || y != tt.y {
			t.Errorf("%s: at (%v, %v), want (%v, %v)", tt.name, x, y, tt.x, tt.y)
		}
	}
}

// A maximized window on the middle of three monitors: the File menu's
// anchor, shadow margin and all, starts left of the window's edge. The
// menu stays on the window's monitor. The numbers are the ones logged
// on the machine where it went to the monitor on the left.
func TestAPopupStaysOnTheMonitorOfItsAnchor(t *testing.T) {
	left := geom.Rc(-1920, 0, 1920, 1200)
	middle := geom.Rc(0, 0, 2560, 1540)
	right := geom.Rc(2560, 0, 1920, 1080)
	areaAt := func(p geom.Point) geom.Rect {
		for _, r := range []geom.Rect{left, middle, right} {
			if r.Contains(p) {
				return r
			}
		}
		return geom.Rect{}
	}
	a := geom.Rect{Min: geom.Pt(-12.5, 46.5), Max: geom.Pt(64.41406, 49)}
	area := popupArea(a, areaAt)
	if area != middle {
		t.Fatalf("the menu is kept in %v, want the middle monitor %v", area, middle)
	}
	if x, y := popupAt(a, 301, 207, area, false); x != 0 || y != 49 {
		t.Fatalf("the menu is put at %v,%v, want 0,49", x, y)
	}
}

func TestAPopupThatPrefersAboveGoesBelowOnlyWhereThereIsNoRoom(t *testing.T) {
	area := geom.Rc(0, 0, 1000, 800)
	// A caret near the bottom: the list opens above it.
	if _, y := popupAt(geom.Rc(100, 700, 1, 20), 200, 150, area, true); y != 550 {
		t.Fatalf("near the bottom the list is at %v, want 550, above the caret", y)
	}
	// A caret near the top, with no room above: the list opens below it.
	if _, y := popupAt(geom.Rc(100, 40, 1, 20), 200, 150, area, true); y != 60 {
		t.Fatalf("near the top the list is at %v, want 60, below the caret", y)
	}
}
