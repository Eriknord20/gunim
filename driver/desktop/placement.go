//go:build linux || windows || darwin

package desktop

import (
	"math"
	"runtime"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/glfw"
)

// keptNormals is how many of the window's last normal places it keeps;
// see forgetMaximized.
const keptNormals = 4

// var _ checks the window says where it is.
var _ driver.PlacementReader = (*Window)(nil)

// Placement implements [driver.PlacementReader]. It asks on the main
// thread, where GLFW has to.
//
// GLFW cannot say where a maximized or minimized window goes back to,
// so the window keeps where it last was while it was neither, and
// reports that. A full-screen window reports the same, and is not
// maximized: full screen is not a state to open a window in.
//
// Once the window has closed, it reports where the window was as it
// closed, without asking the main thread, which may have stopped.
func (w *Window) Placement() (p driver.Placement, ok bool) {
	if p, ok, gone := w.closedAt(); gone {
		return p, ok
	}
	_ = w.d.call(func() error {
		if w.closed {
			p, ok, _ = w.closedAt()
			return nil
		}
		p, ok = w.placementNow()
		return nil
	})
	return p, ok
}

// closedAt is where the window was as it closed, and gone whether it
// has.
func (w *Window) closedAt() (p driver.Placement, ok, gone bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.final, w.kept, w.gone
}

// placementNow is where the window is. It runs on the main thread.
func (w *Window) placementNow() (p driver.Placement, ok bool) {
	if w.closed || w.popup {
		return p, false
	}
	w.noteNormal()
	if len(w.normals) == 0 {
		return p, false
	}
	n := w.normals[len(w.normals)-1]
	p.Bounds = geom.Rc(float32(n[0]), float32(n[1]), float32(n[2]), float32(n[3]))
	p.Maximized = !w.FullScreen() && !w.gw.SystemFullScreen() && attrib(w.gw, glfw.Maximized)
	return p, true
}

// noteNormal keeps where the window is and how big, if it is neither
// maximized, minimized nor full screen: the place it goes back to from
// any of those. It runs on the main thread.
func (w *Window) noteNormal() {
	// The system's own full screen too, which macOS's green button puts
	// a window in without the application asking.
	if w.closed || w.popup || w.FullScreen() || w.gw.SystemFullScreen() || attrib(w.gw, glfw.Maximized) || attrib(w.gw, glfw.Iconified) {
		return
	}
	n, ok := bounds(w.gw)
	if !ok || len(w.normals) > 0 && w.normals[len(w.normals)-1] == n {
		return
	}
	if len(w.normals) == keptNormals {
		w.normals = append(w.normals[:0], w.normals[1:]...)
	}
	w.normals = append(w.normals, n)
}

// forgetMaximized drops the places noted as normal that are really the
// window maximized. On X11 the window manager may move and size the
// window before it marks it maximized, so a move or a resize can be
// noted at the maximized size just before the window says it is
// maximized. It runs on the main thread, as the window maximizes.
func (w *Window) forgetMaximized() {
	now, ok := bounds(w.gw)
	if !ok {
		return
	}
	for len(w.normals) > 1 {
		n := w.normals[len(w.normals)-1]
		if n[2] != now[2] || n[3] != now[3] {
			return
		}
		w.normals = w.normals[:len(w.normals)-1]
	}
}

// bounds is where gw's client area is and how big, in screen
// coordinates, as x, y, width and height. It runs on the main thread.
func bounds(gw *glfw.Window) (b [4]int, ok bool) {
	x, y, err := gw.GetPos()
	if err != nil {
		return b, false
	}
	width, height, err := gw.GetSize()
	if err != nil || width <= 0 || height <= 0 {
		return b, false
	}
	return [4]int{x, y, width, height}, true
}

// attrib reports whether gw's attribute a is set. It runs on the main
// thread.
func attrib(gw *glfw.Window, a glfw.Hint) bool {
	v, err := gw.GetAttrib(a)
	return err == nil && v == glfw.True
}

// placeAt opens the window at a saved placement, first made safe with
// [driver.FitPlacement] for the monitors attached now. It returns the
// placement it used, and false where there was none to use. It runs on
// the main thread, before the window shows.
func (w *Window) placeAt(saved driver.Placement) (driver.Placement, [4]int, bool) {
	var frame geom.Insets
	if !w.Chromeless() {
		// Where the system cannot say how wide its frame is, the window is fitted without it
		if l, t, r, b, err := w.gw.GetFrameSize(); err == nil {
			frame = geom.Insets{Top: float32(t), Right: float32(r), Bottom: float32(b), Left: float32(l)}
		}
	}
	p, ok := driver.FitPlacement(saved, monitors(), frame)
	if !ok {
		return p, [4]int{}, false
	}
	n := [4]int{
		roundInt(p.Bounds.Min.X), roundInt(p.Bounds.Min.Y),
		max(1, roundInt(p.Bounds.Size().W)), max(1, roundInt(p.Bounds.Size().H)),
	}
	w.debugf("placed at %v, saved at %v", n, saved.Bounds)
	w.applyBounds(n)
	w.normals = append(w.normals[:0], n)
	return p, n, true
}

// applyBounds moves and sizes the window's client area to b: x, y,
// width and height in screen coordinates. It runs on the main thread.
//
// It moves the window twice. On Windows a window moved onto a monitor
// of another scale is sized again for that scale as it arrives, and
// the second move puts back what that shifted.
func (w *Window) applyBounds(b [4]int) {
	_ = w.gw.SetPos(b[0], b[1])
	_ = w.gw.SetSize(b[2], b[3])
	_ = w.gw.SetPos(b[0], b[1])
}

// placeAgainShown is true where a window's bounds are set again once it
// shows: Windows may size a window for its monitor's scale only as it
// shows, and the window is still hidden from the user then; see cloak.
const placeAgainShown = runtime.GOOS == "windows"

// maximizeHidden is true where a window can be maximized before it
// shows, so it never shows at its restored size first. X11 marks it
// for the window manager to open maximized; Windows and macOS would
// show it, or fit it by hand, so they maximize once it shows.
const maximizeHidden = runtime.GOOS != "windows" && runtime.GOOS != "darwin"

// roundInt is v to the nearest whole number.
func roundInt(v float32) int { return int(math.Round(float64(v))) }
