//go:build android

package android

/*
#include "glue.h"
*/
import "C"

import (
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"runtime"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/internal/compose"
	"github.com/marrasen/gunim/internal/gl"
)

// render is the render thread. It owns the EGL context, which every
// window draws with. Each time a window presents, it draws the frames
// waiting into their windows' textures, lays every shown window on the
// surface, bottom first, swaps, and reports the frames drawn.
//
// With no surface, as while the activity is in the background, frames
// wait. The engine waits for its frame, so it draws nothing meanwhile.
func (d *Driver) render() {
	runtime.LockOSThread()
	c, err := startGL()
	if err != nil {
		log.Printf("gunim: android: %v", err)
		d.idle()
		return
	}

	var surface *C.ANativeWindow
	attached := false
	for {
		select {
		case <-d.quit:
			return
		case sc := <-d.surfaces:
			if sc.gone != nil {
				// A surface other than the one drawn on is an old
				// activity's, going after its replacement came.
				if sc.window == nil || sc.window == surface {
					C.gunim_egl_detach()
					attached = false
					if surface != nil {
						C.gunim_window_release(surface)
						surface = nil
					}
				}
				if sc.window != nil {
					C.gunim_window_release(sc.window)
				}
				close(sc.gone)
				continue
			}
			if sc.window == surface {
				// The same surface, at a new size: Android has
				// resized it already.
				C.gunim_window_release(sc.window)
				continue
			}
			if surface != nil {
				C.gunim_window_release(surface)
			}
			surface = sc.window
			if e := C.gunim_egl_attach(surface); e != 0 {
				log.Printf("gunim: android: EGL surface: error %#x", int(e))
				continue
			}
			attached = true
			if C.gunim_egl_stamps() == 0 {
				log.Print("gunim: android: no frame timestamps: frames are taken to light the screen two refreshes after the swap")
			}
		case <-d.wake:
		}
		if !attached {
			continue
		}
		d.frame(c)
	}
}

// startGL makes the EGL context current on the render thread, loads
// GL, and builds the compositor.
func startGL() (*compose.Compositor, error) {
	if e := C.gunim_egl_init(); e != 0 {
		return nil, fmt.Errorf("EGL: error %#x", int(e))
	}
	g, err := gl.NewDefaultContext()
	if err == nil {
		err = g.LoadFunctions()
	}
	if err != nil {
		return nil, err
	}
	return compose.New(g, "android")
}

// idle takes surfaces and lets them go, for a render thread that cannot
// draw, so the UI thread handing them over never waits for good.
func (d *Driver) idle() {
	for {
		select {
		case <-d.quit:
			return
		case <-d.wake:
		case sc := <-d.surfaces:
			if sc.window != nil {
				C.gunim_window_release(sc.window)
			}
			if sc.gone != nil {
				close(sc.gone)
			}
		}
	}
}

// frame draws the frames waiting, lays the windows on the surface and
// swaps.
func (d *Driver) frame(c *compose.Compositor) {
	d.mu.Lock()
	type job struct {
		w        *Window
		f        *frame
		fbW, fbH int
		under    color.NRGBA
		shot     func(*image.RGBA)
		// again marks the window's last frame drawn again, to sharpen
		// what it drew stretched; the engine waits for none.
		again bool
	}
	var jobs []job
	var stack []compose.Layer
	gone := d.gone
	d.gone = nil
	scale, surfW, surfH, rate := d.density, d.surfW, d.surfH, d.rate
	pan, panning := d.panLocked(time.Now())
	for _, w := range d.windows {
		r := w.held.Rect(w.rectLocked(), w.next != nil)
		fbW, fbH := int(math.Round(float64(r.Size().W*scale))), int(math.Round(float64(r.Size().H*scale)))
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
			x, y := int(math.Round(float64(r.Min.X*scale))), int(math.Round(float64(r.Min.Y*scale)))-pan
			stack = append(stack, compose.Layer{Pane: &w.pane, X: x, Y: y, W: fbW, H: fbH, Under: w.under})
		}
	}
	d.mu.Unlock()
	for _, p := range gone {
		c.Release(p)
	}
	if len(jobs) == 0 && !c.Dirty(stack) {
		return
	}
	c.Begin(stack, surfW, surfH)
	for _, j := range jobs {
		if !c.Draw(&j.w.pane, j.f.ops, j.f.damage, j.fbW, j.fbH, j.under, scale) {
			if j.shot != nil {
				j.w.keepShot(j.shot)
			}
			continue
		}
		if j.shot != nil {
			c.Shoot(&j.w.pane, j.fbW, j.fbH, j.shot)
		}
	}
	c.Finish(stack, surfW, surfH, scale)
	if e := C.gunim_egl_swap(); e != 0 {
		log.Printf("gunim: android: swap: error %#x", int(e))
	}
	if panning {
		d.kick() // the next frame slides on
	}
	now := time.Now()
	lag := swapLag(rate)
	for _, j := range jobs {
		if j.again {
			continue
		}
		select {
		case j.w.presented <- driver.Frame{Shown: now, Lag: lag}:
		case <-j.w.quit:
		}
	}
}

// lagTold says the log has the first lag the frame timestamps measured.
// It belongs to the render thread.
var lagTold bool

// swapLag returns how long after its swap returns a frame lights the
// screen: as Android's frame timestamps measured it, where the surface
// keeps them, and two refreshes at rate where not. SurfaceFlinger takes
// a frame swapped in at one refresh and shows it at the next, at the
// soonest, and a frame swapped while another waits waits a refresh more.
func swapLag(rate float64) time.Duration {
	if ns := C.gunim_egl_lag(); ns >= 0 {
		if !lagTold {
			lagTold = true
			log.Printf("gunim: android: a frame lit the screen %v after its swap, as Android's frame timestamps tell",
				time.Duration(ns).Round(100*time.Microsecond))
		}
		return time.Duration(ns)
	}
	if rate <= 0 {
		rate = 60
	}
	return 2 * time.Duration(float64(time.Second)/rate)
}

// sharpenAt has w draw its last frame again at the time at, the zero
// time for never, so a big mask the frame drew stretched is drawn
// sharp. A frame drawn before then takes its place. The engine keeps a
// frame's ops until it presents the frame after, so they hold.
func (d *Driver) sharpenAt(w *Window, at time.Time) {
	d.mu.Lock()
	w.sharpAt = at
	d.mu.Unlock()
	if at.IsZero() {
		return
	}
	time.AfterFunc(time.Until(at), func() {
		d.mu.Lock()
		w.sharpen = w.sharpen || w.sharpAt.Equal(at)
		d.mu.Unlock()
		d.kick()
	})
}
