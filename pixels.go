package gunim

import (
	"fmt"
	"image"
)

// Pixels draws the last frame of a window from [NewOffscreen] with the renderer a window on a display uses, and
// returns it, its popups left out, for a test that looks at what the window shows. It opens a hidden window for its
// GL context the first time, and fails with [ErrNoPixels] for a window on a display, on macOS and Android, and where
// no display or OpenGL is at hand.
func (w *Window) Pixels() (*image.RGBA, error) {
	o := w.Offscreen()
	if o == nil {
		return nil, ErrNoPixels
	}
	img, err := o.Pixels()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoPixels, err)
	}
	return img, nil
}
