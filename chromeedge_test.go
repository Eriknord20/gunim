package gunim

import (
	"testing"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

func TestAWindowsCornersReachAlongItsEdgesAndIntoTheBottomCorners(t *testing.T) {
	size := geom.Sz(400, 300)
	cases := []struct {
		x, y float32
		want driver.Edge
		ok   bool
		why  string
	}{
		{399, 150, driver.EdgeRight, true, "on the right edge"},
		{392.5, 150, driver.EdgeRight, true, "8 px in from the right edge, the last pixel of it"},
		{391, 150, 0, false, "past the edge's margin"},
		{399, 299, driver.EdgeBottomRight, true, "the corner's own pixel"},
		{399, 281, driver.EdgeBottomRight, true, "on the right edge, 19 px up from the corner"},
		{381, 299, driver.EdgeBottomRight, true, "on the bottom edge, 19 px left of the corner"},
		{399, 279, driver.EdgeRight, true, "on the right edge, past the corner's reach"},
		{389, 289, driver.EdgeBottomRight, true, "inside the bottom right corner's square"},
		{387, 287, 0, false, "inside the window, past the corner's square"},
		{1, 280, driver.EdgeBottomLeft, true, "on the left edge, near the bottom"},
		{10, 290, driver.EdgeBottomLeft, true, "inside the bottom left corner's square"},
		{0, 15, driver.EdgeTopLeft, true, "on the left edge, near the top"},
		{385, 0, driver.EdgeTopRight, true, "on the top edge, near the right"},
		{390, 10, 0, false, "inside the top right corner, where the title bar's buttons are"},
		{200, 0, driver.EdgeTop, true, "on the top edge"},
		{200, 150, 0, false, "in the middle"},
		{380, 299, driver.EdgeBottomRight, true, "on the bottom edge, at the corner's reach"},
		{379, 299, driver.EdgeBottom, true, "on the bottom edge, just past the corner's reach"},
	}
	for _, c := range cases {
		got, ok := edgeIn(geom.Pt(c.x, c.y), size)
		if ok != c.ok || got != c.want {
			t.Errorf("%s (%v,%v): got %v %v, want %v %v", c.why, c.x, c.y, got, ok, c.want, c.ok)
		}
	}
}

// A small window keeps the middle of each side a plain edge: corners reach a third of the way along it at most.
func TestASmallWindowKeepsThePlainEdgesBetweenItsCorners(t *testing.T) {
	size := geom.Sz(300, 30)
	if got, ok := edgeIn(geom.Pt(0, 15), size); !ok || got != driver.EdgeLeft {
		t.Errorf("the middle of a 30 px tall window's left edge sizes by %v %v, want the left edge", got, ok)
	}
	if got, ok := edgeIn(geom.Pt(0, 2), size); !ok || got != driver.EdgeTopLeft {
		t.Errorf("the top of its left edge sizes by %v %v, want the top left corner", got, ok)
	}
}
