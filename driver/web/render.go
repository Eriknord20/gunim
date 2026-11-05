//go:build js && wasm

package web

import (
	"image"
	"image/color"
	"log"
	"math"
	"os"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/internal/compose"
	"github.com/marrasen/gunim/driver/internal/render"
)

// frame runs in requestAnimationFrame, and after a new size in the
// ResizeObserver's step. It draws the frames waiting into their windows,
// lays the windows on the canvas, bottom first, and tells each window its
// frame is shown. The browser shows the canvas as the callback returns.
func (d *Driver) frame() {
	d.mu.Lock()
	lost := d.lost
	d.mu.Unlock()
	if lost {
		return
	}
	g := d.g
	d.mu.Lock()
	type job struct {
		w        *Window
		f        *frame
		fbW, fbH int
		under    color.NRGBA
		shot     func(*image.RGBA)
		// again says it is the last frame drawn over, which the engine never sent.
		again bool
	}
	var jobs []job
	var stack []compose.Layer
	gone := d.gone
	d.gone = nil
	scale, surfW, surfH := d.scale, d.surfW, d.surfH
	for _, w := range d.windows {
		r := w.held.Rect(w.rectLocked(), w.next != nil)
		fbW, fbH := int(math.Round(float64(r.Size().W*scale))), int(math.Round(float64(r.Size().H*scale)))
		if w.fills {
			// The browser's count of device pixels can differ from this by one.
			fbW, fbH = surfW, surfH
		}
		if w.next != nil {
			j := job{w: w, f: w.next, fbW: fbW, fbH: fbH, under: w.under}
			if fbW > 0 && fbH > 0 {
				// A shot waits for a frame that draws.
				j.shot, w.shot = w.shot, nil
			}
			jobs = append(jobs, j)
			w.next = nil
		} else if w.sharpen && w.pane.Last != nil {
			jobs = append(jobs, job{w: w, f: &frame{ops: w.pane.Last}, fbW: fbW, fbH: fbH, under: w.under, again: true})
		}
		w.sharpen = false
		if !w.hidden && !w.stale {
			x, y := int(math.Round(float64(r.Min.X*scale))), int(math.Round(float64(r.Min.Y*scale)))
			stack = append(stack, compose.Layer{Pane: &w.pane, X: x, Y: y, W: fbW, H: fbH, Under: w.under})
		}
	}
	d.mu.Unlock()
	for _, p := range gone {
		d.comp.Release(p)
	}
	if len(jobs) == 0 && !d.comp.Dirty(stack) {
		return
	}
	d.comp.Begin(stack, surfW, surfH)
	for _, j := range jobs {
		start := time.Now()
		if !d.comp.Draw(&j.w.pane, j.f.ops, j.f.damage, j.fbW, j.fbH, j.under, scale) {
			if j.shot != nil {
				j.w.keepShot(j.shot)
			}
			continue
		}
		if j.shot != nil {
			d.comp.Shoot(&j.w.pane, j.fbW, j.fbH, j.shot)
		}
		if framesDebug {
			var gpu time.Duration
			if framesFinish {
				t := time.Now()
				g.Finish()
				gpu = time.Since(t)
			}
			d.stats.add(time.Since(start), gpu, &j.w.pane.R.Stats)
		}
	}
	d.comp.Finish(stack, surfW, surfH, scale)
	now := time.Now()
	for _, j := range jobs {
		if j.again {
			continue
		}
		select {
		case j.w.presented <- driver.Frame{Shown: now, Lag: time.Duration(float64(time.Second) / d.rate)}:
		default:
		}
	}
}

// framesDebug is set by GUNIM_DEBUG_FRAMES=1, which logs each second
// how long the frames took to draw and what they sent the GPU. In a
// browser the page sets it in the Go object's env before running.
var framesDebug = os.Getenv("GUNIM_DEBUG_FRAMES") != ""

// framesFinish is set by GUNIM_DEBUG_FRAMES=gpu, which waits for the GPU
// after each frame and logs how long it took.
var framesFinish = os.Getenv("GUNIM_DEBUG_FRAMES") == "gpu"

type frameStats struct {
	from          time.Time
	n             int
	draw, drawMax time.Duration
	gpu, gpuMax   time.Duration
	quads         int
	redrawn       float64
}

func (s *frameStats) add(draw, gpu time.Duration, sent *render.Stats) {
	now := time.Now()
	if s.from.IsZero() {
		s.from = now
	}
	s.n++
	s.draw += draw
	s.drawMax = max(s.drawMax, draw)
	s.gpu += gpu
	s.gpuMax = max(s.gpuMax, gpu)
	s.quads += sent.Quads
	s.redrawn += sent.Redrawn
	*sent = render.Stats{}
	if now.Sub(s.from) < time.Second {
		return
	}
	n := float64(s.n)
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
	log.Printf("gunim web: %d frames drawn; draw %.1f ms (max %.1f), GPU %.1f ms (max %.1f); %.0f quads and %.0f%% of the window redrawn a frame",
		s.n, ms(s.draw)/n, ms(s.drawMax), ms(s.gpu)/n, ms(s.gpuMax), float64(s.quads)/n, 100*s.redrawn/n)
	*s = frameStats{from: now}
}
