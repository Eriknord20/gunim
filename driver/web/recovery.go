//go:build js && wasm

package web

import (
	"log"
	"slices"
	"syscall/js"
	"time"

	"github.com/marrasen/gunim/driver/internal/compose"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/paint"
)

// contextWait is how long a lost GL context has to come back before the user is
// told. A GPU process that crashed and restarted takes a second or two.
var contextWait = 5 * time.Second

func (d *Driver) listenContext() {
	d.on(d.canvas, "webglcontextlost", false, func(e js.Value) {
		e.Call("preventDefault")
		d.mu.Lock()
		d.lost = true
		if d.lostTimer != nil {
			d.lostTimer.Stop()
		}
		d.lostTimer = time.AfterFunc(contextWait, d.contextGone)
		d.mu.Unlock()
		log.Print("gunim: web: the GL context is lost, and the canvas draws nothing until the browser gives it back")
		d.canvas.Call("setAttribute", "data-gunim-context", "lost")
	})
	d.on(d.canvas, "webglcontextrestored", false, func(js.Value) {
		// Context loss invalidates every GPU object, including glyph and mask atlases.
		g, err := gl.NewWebGLContext(d.canvas.Call("getContext", "webgl2"))
		if err != nil {
			log.Print(err)
			d.contextGone()
			return
		}
		comp, err := compose.New(g, "web")
		if err != nil {
			log.Print(err)
			d.contextGone()
			return
		}
		d.mu.Lock()
		if d.lostTimer != nil {
			d.lostTimer.Stop()
			d.lostTimer = nil
		}
		d.g = g
		d.comp = comp
		d.lost = false
		// What the closed windows held went with the old context.
		d.gone = nil
		for _, w := range d.windows {
			w.pane.Forget()
			if w.next != nil {
				w.next.damage = []geom.Rect{paint.Everything}
			} else if w.pane.Last != nil {
				w.next = &frame{ops: w.pane.Last, damage: []geom.Rect{paint.Everything}}
			}
		}
		d.mu.Unlock()
		if d.lostNote.Truthy() {
			d.lostNote.Call("remove")
		}
		d.canvas.Call("setAttribute", "data-gunim-context", "restored")
		d.kick()
	})
}

// contextGone tells the user the canvas stopped drawing, where it would otherwise
// show the bare page for good. The context can still come back, and the note goes
// then. A page can style the element with the id gunim-context-lost, or hide it and
// show its own by the canvas's data-gunim-context attribute, which is now "gone".
func (d *Driver) contextGone() {
	defer survive("context")
	d.mu.Lock()
	lost := d.lost
	d.mu.Unlock()
	if !lost {
		return
	}
	log.Print("gunim: web: the GL context has not come back, and the page says so")
	d.canvas.Call("setAttribute", "data-gunim-context", "gone")
	doc := js.Global().Get("document")
	if !d.lostNote.Truthy() {
		d.lostNote = doc.Call("createElement", "div")
		d.lostNote.Set("id", "gunim-context-lost")
		d.lostNote.Call("setAttribute", "role", "alert")
		d.lostNote.Set("textContent", "The graphics stopped working. Reload the page to go on. ")
		d.lostNote.Get("style").Set("cssText", "position:fixed;left:50%;top:50%;transform:translate(-50%,-50%);z-index:2147483647;"+
			"max-width:80%;padding:16px 20px;border-radius:8px;background:#222;color:#fff;font:16px system-ui,sans-serif;text-align:center")
		reload := doc.Call("createElement", "button")
		reload.Set("textContent", "Reload")
		d.on(reload, "click", true, func(js.Value) { js.Global().Get("location").Call("reload") })
		d.lostNote.Call("appendChild", reload)
	}
	doc.Get("body").Call("appendChild", d.lostNote)
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

// Limits on the gap between animation frames, in milliseconds, that says anything
// of the panel's rate. Two callbacks in one frame share a timestamp, and no panel
// is faster than 1000 Hz. A longer gap than 50 ms is the page at rest.
const (
	shortestGap = 1
	longestGap  = 50
)

// observeRefresh estimates the panel's rate from the gaps between animation
// frames.
func (d *Driver) observeRefresh(now float64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	gap := now - d.lastRAF
	d.lastRAF = now
	if gap < shortestGap || gap > longestGap {
		return
	}
	d.gaps[d.gapAt] = gap
	d.gapAt = (d.gapAt + 1) % len(d.gaps)
	d.gapCount = min(d.gapCount+1, len(d.gaps))
	if d.gapCount < 8 {
		return
	}
	sorted := d.sortedGaps[:d.gapCount]
	copy(sorted, d.gaps[:d.gapCount])
	slices.Sort(sorted)
	d.rate = 1000 / panelGap(sorted)
}

// panelGap returns the panel's refresh period from sorted gaps between animation
// frames. A frame that misses a refresh leaves a gap of two or more, and a busy
// program misses most of them, so the middle gap can say half the rate. The panel's
// period is the shortest gap seen often: the middle of those within half again of
// the shortest, leaving out the shortest tenth as the odd late frame's catching up.
func panelGap(sorted []float64) float64 {
	from := len(sorted) / 10
	to := from
	for to < len(sorted) && sorted[to] < sorted[from]*1.5 {
		to++
	}
	return sorted[(from+to)/2]
}

func (d *Driver) calibrateRefresh() {
	remaining := 24
	var callback js.Func
	callback = js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) > 0 {
			d.observeRefresh(args[0].Float())
		}
		remaining--
		d.mu.Lock()
		stopped := d.stopped
		d.mu.Unlock()
		if remaining > 0 && !stopped {
			js.Global().Call("requestAnimationFrame", callback)
		} else {
			callback.Release()
		}
		return nil
	})
	js.Global().Call("requestAnimationFrame", callback)
}
