package gunim

import (
	"errors"
	"fmt"
	"image"

	"github.com/marrasen/gunim/driver"
)

// Pixels draws the last frame of a window from [NewOffscreen] with the renderer a window on a display uses, its
// popups left out, and returns it, for a test that looks at what the window shows. It opens a hidden window for its
// GL context the first time. It fails with [ErrNoPixels] for a window on a display, on macOS and Android, where no
// display or OpenGL is at hand, and in a process with a window on a display; any other failure is its own.
func (w *Window) Pixels() (*image.RGBA, error) {
	o := w.Offscreen()
	if o == nil {
		return nil, ErrNoPixels
	}
	img, err := o.Pixels()
	if errors.Is(err, driver.ErrCannotDraw) {
		return nil, fmt.Errorf("%w: %w", ErrNoPixels, err)
	}
	return img, err
}
