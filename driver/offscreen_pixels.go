package driver

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"sync/atomic"

	"github.com/marrasen/gunim/paint"
)

// ErrCannotDraw is what [OffscreenWindow.Pixels] fails with where nothing can draw: no renderer is built in, no
// display or OpenGL is at hand, or a window on a display already uses GLFW.
var ErrCannotDraw = errors.New("driver: offscreen windows cannot draw here")

// Who has started GLFW in this process, which keeps the state of one thread.
const (
	glfwFree int32 = iota
	glfwDesktop
	glfwOffscreen
)

// glfwOwner is who has started GLFW: the desktop driver, or the hidden window offscreen windows draw in.
var glfwOwner atomic.Int32

// ClaimGLFWForDesktop has the desktop driver own GLFW for the run, and fails where offscreen windows already draw
// their pixels with it.
func ClaimGLFWForDesktop() error {
	if glfwOwner.CompareAndSwap(glfwFree, glfwDesktop) || glfwOwner.Load() == glfwDesktop {
		return nil
	}
	return errors.New("driver: offscreen windows draw their pixels with GLFW in this process, so no window opens on a display")
}

// claimGLFWForOffscreen has the hidden window own GLFW for the run, and fails where a window on a display uses it.
func claimGLFWForOffscreen() error {
	if glfwOwner.CompareAndSwap(glfwFree, glfwOffscreen) || glfwOwner.Load() == glfwOffscreen {
		return nil
	}
	return fmt.Errorf("%w: a window on a display uses GLFW in this process", ErrCannotDraw)
}

// drawOps draws ops into a frame fbW by fbH device pixels, at scale device pixels to a logical one, as a window on a
// display draws them, over under, and returns the picture. Where no renderer is built in, it fails with
// ErrCannotDraw.
var drawOps = func([]paint.Op, int, int, float32, color.NRGBA) (*image.RGBA, error) {
	return nil, fmt.Errorf("%w: no renderer on this platform", ErrCannotDraw)
}

// Pixels draws the last frame's ops with the renderer a window on a display uses, in a hidden window of its own, and
// returns the picture: the size and scale the frame was presented at, the right way up, over the window's
// background. Text draws without the system's own text settings, and the window's corners and colour conversion are
// left out. It is for a test that looks at what the window shows, and fails with ErrCannotDraw where nothing can draw.
func (w *OffscreenWindow) Pixels() (*image.RGBA, error) {
	w.mu.Lock()
	ops, size, scale, zoom, under := w.ops, w.drawnSize, w.drawnScale, w.drawnZoom, w.under
	w.mu.Unlock()
	if ops == nil {
		return nil, errors.New("driver: the window has drawn no frame")
	}
	fbW, fbH := int(math.Ceil(float64(size.W*scale))), int(math.Ceil(float64(size.H*scale)))
	if fbW <= 0 || fbH <= 0 {
		return nil, errors.New("driver: the window has no size to draw")
	}
	return drawOps(ops, fbW, fbH, scale*zoom, under)
}
