//go:build linux || windows || darwin

package render

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// A frame in which nothing changed draws nothing new, once the canvas
// holds the frame before at its size; any change, a new size, or a big
// mask waiting to be drawn sharp, draws.
func TestAFrameWithNothingChangedIsUnchanged(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	w, h := int(benchSize.W), int(benchSize.H)
	if r.Unchanged(geom.Rect{}, w, h, 1) {
		t.Fatal("before any frame, a frame with no damage is unchanged")
	}
	frameOf(r, nil, nil, paint.Everything, time.Now())
	if !r.Unchanged(geom.Rect{}, w, h, 1) {
		t.Fatal("a frame with no damage after a whole one is changed")
	}
	if r.Unchanged(geom.Rc(1, 1, 2, 2), w, h, 1) {
		t.Error("a frame with damage is unchanged")
	}
	if r.Unchanged(geom.Rect{}, w+1, h, 1) {
		t.Error("a frame a pixel wider is unchanged")
	}
	if r.Unchanged(geom.Rect{}, w, h, 2) {
		t.Error("a frame at another scale is unchanged")
	}
	r.Stretched = geom.Rc(0, 0, 10, 10)
	if r.Unchanged(geom.Rect{}, w, h, 1) {
		t.Error("a frame with a big mask still to draw sharp is unchanged")
	}
}
