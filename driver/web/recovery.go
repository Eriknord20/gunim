//go:build js && wasm

package web

import (
	"image"
	"log"
	"math"
	"sort"
	"syscall/js"
	"time"

	"github.com/marrasen/gunim/driver/internal/render"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/paint"
)

func (d *Driver) listenContext() {
	d.on(d.canvas, "webglcontextlost", false, func(e js.Value) {
		e.Call("preventDefault")
		d.mu.Lock()
		d.lost = true
		d.mu.Unlock()
		d.canvas.Call("setAttribute", "data-gunim-context", "lost")
	})
	d.on(d.canvas, "webglcontextrestored", false, func(js.Value) {
		// Context loss invalidates every GPU object, including glyph and mask atlases.
		g, err := gl.NewWebGLContext(d.canvas.Call("getContext", "webgl2"))
		if err != nil {
			log.Print(err)
			return
		}
		comp, err := newCompositor(g)
		if err != nil {
			log.Print(err)
			return
		}
		d.mu.Lock()
		d.g = g
		d.comp = comp
		d.shared = render.Shared{}
		d.lost = false
		for _, w := range d.windows {
			w.r = nil
			w.tex = 0
			w.fbo = 0
			w.texW = 0
			w.texH = 0
			w.onTex = false
			if w.next != nil {
				w.next.damage = []geom.Rect{paint.Everything}
			} else if w.last != nil {
				w.next = &frame{ops: w.last, damage: []geom.Rect{paint.Everything}}
			}
		}
		d.mu.Unlock()
		d.canvas.Call("setAttribute", "data-gunim-context", "restored")
		d.kick()
	})
}
func (d *Driver) sharpenAt(w *Window, at time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if w.sharpAt.Equal(at) {
		return
	}
	w.sharpAt = at
	if w.sharpTimer != nil {
		w.sharpTimer.Stop()
		w.sharpTimer = nil
	}
	if at.IsZero() || w.closed {
		return
	}
	w.sharpTimer = time.AfterFunc(time.Until(at), func() {
		d.mu.Lock()
		if !w.closed && w.sharpAt.Equal(at) {
			w.sharpen = true
		}
		d.mu.Unlock()
		d.kick()
	})
}
func upright(pix []byte, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		copy(img.Pix[y*img.Stride:(y+1)*img.Stride], pix[(h-1-y)*w*4:(h-y)*w*4])
	}
	return img
}

// Estimate the actual panel rate from consecutive RAFs; ignore gaps at idle.
func (d *Driver) observeRefresh(now float64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delta := now - d.lastRAF
	d.lastRAF = now
	if delta < 4 || delta > 50 {
		return
	}
	d.samples = append(d.samples, delta)
	if len(d.samples) > 60 {
		d.samples = d.samples[1:]
	}
	if len(d.samples) < 8 {
		return
	}
	sorted := append([]float64(nil), d.samples...)
	sort.Float64s(sorted)
	rate := 1000 / sorted[len(sorted)/2]
	if !math.IsNaN(rate) {
		d.rate = rate
	}
}
func (d *Driver) calibrateRefresh() {
	remaining := 24
	var callback js.Func
	callback = js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) > 0 {
			d.observeRefresh(args[0].Float())
		}
		remaining--
		if remaining > 0 {
			js.Global().Call("requestAnimationFrame", callback)
		} else {
			callback.Release()
		}
		return nil
	})
	js.Global().Call("requestAnimationFrame", callback)
}
