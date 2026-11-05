//go:build !js

package desktop

import (
	"image"
	"math"
	"slices"

	"github.com/marrasen/gunim/geom"
)

// SetPointerRegion implements [driver.PointerRegioner].
func (w *Window) SetPointerRegion(rects []geom.Rect) {
	rects = slices.Clone(rects)
	w.d.post(func() {
		if w.closed {
			return
		}
		w.region, w.regionSet = rects, rects != nil
		w.applyRegion()
	})
}

// applyRegion hands the window's pointer region to the system, in the window's own coordinates, each rectangle
// grown out to whole ones. It runs on the main thread.
func (w *Window) applyRegion() {
	if !w.regionSet {
		_ = w.gw.SetInputRegion(nil)
		return
	}
	f := w.coordsPerLogical()
	px := make([]image.Rectangle, 0, len(w.region))
	for _, r := range w.region {
		if r.Empty() {
			continue
		}
		px = append(px, image.Rect(
			int(math.Floor(float64(r.Min.X*f))), int(math.Floor(float64(r.Min.Y*f))),
			int(math.Ceil(float64(r.Max.X*f))), int(math.Ceil(float64(r.Max.Y*f)))))
	}
	_ = w.gw.SetInputRegion(px)
}
