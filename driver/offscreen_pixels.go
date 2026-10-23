package driver

import (
	"errors"
	"image"
	"math"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// drawOps draws ops, laid out in size and drawn at scale, as a window on a display draws them, and returns the
// picture. Where no renderer is built in, it fails.
var drawOps = func([]paint.Op, int, int, float32) (*image.RGBA, error) {
	return nil, errors.New("driver: no renderer on this platform")
}

// Pixels draws the last frame's ops as a window on a display draws them, with the same renderer, in a hidden window
// of its own, and returns the picture: the window's size times its scale, the right way up. It is for a test that
// looks at what the window shows, and fails where no display or OpenGL is at hand.
func (w *OffscreenWindow) Pixels() (*image.RGBA, error) {
	w.mu.Lock()
	ops := w.ops
	size, scale := geom.Sz(w.size.W/w.zoom, w.size.H/w.zoom), w.scale*w.zoom
	w.mu.Unlock()
	fbW, fbH := int(math.Ceil(float64(size.W*scale))), int(math.Ceil(float64(size.H*scale)))
	if fbW <= 0 || fbH <= 0 {
		return nil, errors.New("driver: the window has no size to draw")
	}
	return drawOps(ops, fbW, fbH, scale)
}
