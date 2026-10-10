//go:build linux || windows || darwin

// Package desktop is gunim's driver for desktop operating systems:
// Linux and the BSDs through X11, Windows through Win32, macOS through
// Cocoa. It is built on a pure-Go port of GLFW taken from Ebitengine,
// so it needs no cgo.
//
// GLFW insists that windows are created and events pumped on the main
// thread, so [Driver.Run] runs there and everything else reaches it
// through a task queue. Each window renders on a thread of its own,
// which is what lets two windows on two monitors keep two refresh rates.
package desktop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"runtime"
	"sync"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/internal/render"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/internal/glfw"
)

// init pins the main goroutine to the main thread, which is where
// GLFW has to run.
func init() { runtime.LockOSThread() }

// errStopped is returned by calls that arrive after the event loop has
// ended.
var errStopped = errors.New("desktop: the event loop has stopped")

// Driver is the desktop implementation of [driver.Driver].
type Driver struct {
	// isES records whether the GL library loaded is OpenGL ES, which
	// decides the context to ask GLFW for and the shader dialect.
	isES bool

	mu      sync.Mutex
	tasks   []func()
	stopped bool
	// posted is signalled as a task is queued, for a main thread waiting on a window to run it.
	posted chan struct{}

	// The fields below belong to the main thread.
	windows map[*glfw.Window]*Window
	// lastUsed is the window, other than a popup, that had the keyboard
	// last, which the system's media controls speak to.
	lastUsed *Window
	// screenAway says the screen is off or locked, so no window is in
	// sight.
	screenAway bool
	// cursors holds the pointer shapes made so far.
	cursors map[input.Cursor]*glfw.Cursor
	// shareRoot is a hidden window whose context no thread ever makes
	// current. Every window's context shares with it, which puts them
	// all in one share group without sharing with a context that a render
	// thread holds: NVIDIA's driver refuses that.
	shareRoot *glfw.Window
	opened    bool
	quit      bool
	// stayOpen keeps the event loop running after the last window
	// closes, so tests can open one window after another.
	stayOpen bool

	// shared is what every window's renderer shares.
	shared render.Shared
	// dxgi is set where windows present through DXGI; see
	// present_windows.go.
	dxgi bool
	// acc is the connection to assistive technology.
	acc driverAccess //nolint:unused // used on Linux
}

// Open initialises GLFW and loads the GL library. Call it on the main
// goroutine.
func Open() (*Driver, error) {
	if err := glfw.Init(); err != nil {
		return nil, fmt.Errorf("desktop: %w", err)
	}
	probe, err := gl.NewDefaultContext()
	if err != nil {
		_ = glfw.Terminate()
		return nil, fmt.Errorf("desktop: %w", err)
	}
	d := &Driver{isES: probe.IsES(), windows: map[*glfw.Window]*Window{}, posted: make(chan struct{}, 1)}
	d.dxgi = d.presentsThroughDXGI()
	return d, nil
}

// Run implements [driver.Driver]. It pumps events on the calling
// goroutine, which must be the main one, until ctx ends or the last
// window has closed.
func (d *Driver) Run(ctx context.Context, ready func()) error {
	stop := context.AfterFunc(ctx, func() { d.post(func() { d.quit = true }) })
	defer stop()
	// Windows rest while the screen is off or locked.
	stopWatch := watchScreen(d)
	defer stopWatch()
	// A main thread held up says where, for a program that freezes.
	stopHang := watchHang(d, hangEvery, hangAfter)
	defer stopHang()

	ready()
	var err error
	for !d.quit {
		if err = glfw.WaitEvents(); err != nil {
			err = fmt.Errorf("desktop: %w", err)
			break
		}
		d.runTasks()
		if d.opened && len(d.windows) == 0 && !d.stayOpen {
			break
		}
	}

	// Refuse new work, finish what was already queued, and take down
	// whatever windows are still open.
	d.mu.Lock()
	d.stopped = true
	d.mu.Unlock()
	d.runTasks()
	// The tray's icon goes with the application, rather than staying in
	// the tray until the pointer passes over it.
	closeTray()
	for _, w := range d.windows {
		w.shutdown()
	}
	if d.shareRoot != nil {
		_ = d.shareRoot.Destroy()
		d.shareRoot = nil
	}
	return errors.Join(err, glfw.Terminate())
}

// StayOpen implements [driver.StayOpener].
func (d *Driver) StayOpen(on bool) {
	d.post(func() {
		// Turning it off with none open ends the loop, once it was on:
		// not before the first window opens.
		if on {
			d.opened = true
		}
		d.stayOpen = on
	})
}

// post queues f for the main thread and wakes the event loop.
//
// The wake-up happens under the lock that Run takes to stop, so Run
// cannot terminate GLFW while a wake-up is on its way.
func (d *Driver) post(f func()) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return false
	}
	d.tasks = append(d.tasks, f)
	_ = glfw.PostEmptyEvent()
	select {
	case d.posted <- struct{}{}:
	default:
	}
	return true
}

// call runs f on the main thread and waits for its result. It must be
// called from any goroutine except the main one.
func (d *Driver) call(f func() error) error {
	done := make(chan error, 1)
	start := time.Now()
	if !d.post(func() { done <- f() }) {
		return errStopped
	}
	err := <-done
	if took := time.Since(start); took > slowCall && (pointerDebug || windowDebug) {
		fmt.Fprintf(os.Stderr, "gunim stall %s: a call to the main thread from %s took %.0f ms\n",
			time.Now().Format("15:04:05.000"), caller(2), float64(took.Microseconds())/1000)
	}
	return err
}

// slowCall is how long a call to the main thread, or a wait on a render thread, may take before GUNIM_DEBUG_POINTER
// or GUNIM_DEBUG_WINDOW says so. The engine waits all that time, and draws nothing.
const slowCall = 250 * time.Millisecond

// caller names the function skip calls up the stack, for a log.
func caller(skip int) string {
	pc, _, _, ok := runtime.Caller(skip)
	if !ok {
		return "somewhere"
	}
	if f := runtime.FuncForPC(pc); f != nil {
		return f.Name()
	}
	return "somewhere"
}

// funcName names f, for a log.
func funcName(f func()) string {
	if fn := runtime.FuncForPC(reflect.ValueOf(f).Pointer()); fn != nil {
		return fn.Name()
	}
	return "a task"
}

func (d *Driver) runTasks() {
	for {
		d.mu.Lock()
		tasks := d.tasks
		d.tasks = nil
		d.mu.Unlock()
		if len(tasks) == 0 {
			return
		}
		for _, f := range tasks {
			if !pointerDebug && !windowDebug {
				f()
				continue
			}
			start := time.Now()
			f()
			if took := time.Since(start); took > slowCall {
				fmt.Fprintf(os.Stderr, "gunim stall %s: the main thread spent %.0f ms on %s\n",
					time.Now().Format("15:04:05.000"), float64(took.Microseconds())/1000, funcName(f))
			}
		}
	}
}

// NewWindow implements [driver.Driver].
func (d *Driver) NewWindow(o driver.Options) (driver.Window, error) {
	var w *Window
	err := d.call(func() error {
		var err error
		w, err = d.openWindow(o)
		return err
	})
	if err != nil {
		return nil, err
	}
	return w, nil
}

// Monitors implements [driver.Driver].
func (d *Driver) Monitors() []driver.Monitor {
	var out []driver.Monitor
	_ = d.call(func() error {
		out = monitors()
		return nil
	})
	return out
}

// Covered implements [driver.Coverer].
// PointerOnScreen implements [driver.Pointer].
func (d *Driver) PointerOnScreen() (p geom.Point, ok bool) {
	_ = d.call(func() error {
		x, y, got := glfw.PointerOnScreen()
		p, ok = geom.Pt(float32(x), float32(y)), got
		return nil
	})
	return p, ok
}

func (d *Driver) Covered(ws []driver.Window, p geom.Point) (covered, ok bool) {
	gws := make([]*glfw.Window, len(ws))
	for i, w := range ws {
		if dw, mine := w.(*Window); mine && dw.d == d {
			gws[i] = dw.gw
		}
	}
	_ = d.call(func() error {
		for i, w := range ws {
			if dw, mine := w.(*Window); mine && dw.closed {
				gws[i] = nil
			}
		}
		covered, ok = glfw.Covered(gws, int(math.Floor(float64(p.X))), int(math.Floor(float64(p.Y))))
		return nil
	})
	return covered, ok
}

// Depths implements [driver.Stacker].
func (d *Driver) Depths(ws []driver.Window) []int {
	gws := make([]*glfw.Window, len(ws))
	for i, w := range ws {
		if dw, ok := w.(*Window); ok && dw.d == d {
			gws[i] = dw.gw
		}
	}
	var out []int
	err := d.call(func() error {
		for i, w := range ws {
			if dw, ok := w.(*Window); ok && dw.closed {
				gws[i] = nil
			}
		}
		out = glfw.Depths(gws)
		return nil
	})
	if err != nil {
		out = make([]int, len(ws))
		for i := range out {
			out[i] = -1
		}
	}
	return out
}

// monitors lists the attached displays. It runs on the main thread.
func monitors() []driver.Monitor {
	ms, err := glfw.GetMonitors()
	if err != nil {
		return nil
	}
	primary, _ := glfw.GetPrimaryMonitor()
	var out []driver.Monitor
	for _, m := range ms {
		info, ok := monitorInfo(m)
		if !ok {
			continue
		}
		info.Primary = m == primary
		// Asked here, not in monitorInfo, which a window asks each time it moves
		if x, y, w, h, err := m.GetWorkarea(); err == nil && w > 0 && h > 0 {
			info.WorkArea = geom.Rc(float32(x), float32(y), float32(w), float32(h))
		}
		out = append(out, info)
	}
	return out
}

// monitorInfo describes m in screen coordinates. It runs on the main
// thread.
func monitorInfo(m *glfw.Monitor) (driver.Monitor, bool) {
	x, y, err := m.GetPos()
	if err != nil {
		return driver.Monitor{}, false
	}
	mode, err := m.GetVideoMode()
	if err != nil || mode == nil {
		return driver.Monitor{}, false
	}
	name, _ := m.GetName()
	scale, _, _ := m.GetContentScale()
	if scale <= 0 {
		scale = 1
	}
	// Screen coordinates are points on macOS, which the scale has already been taken out of
	perLogical := scale
	if runtime.GOOS == "darwin" {
		perLogical = 1
	}
	return driver.Monitor{
		Name:             name,
		Bounds:           geom.Rc(float32(x), float32(y), float32(mode.Width), float32(mode.Height)),
		RefreshRate:      float64(mode.RefreshRate),
		Scale:            scale,
		CoordsPerLogical: perLogical,
	}, true
}

// contextWindow opens the hidden window that holds a DXGI window's
// context, sharing with share. It runs on the main thread, and makes the
// window alone: its render thread makes the context, uses it, and
// deletes it, so the main thread never touches a context a render thread
// draws with. A GL driver can wait in such a call for a lock the threads
// drawing in the same share group hold, and every window waits on the
// main thread.
func (d *Driver) contextWindow(share *glfw.Window) (*glfw.Window, error) {
	if err := glfw.DefaultWindowHints(); err != nil {
		return nil, err
	}
	if err := d.setContextHints(); err != nil {
		return nil, err
	}
	if err := glfw.WindowHint(glfw.Visible, glfw.False); err != nil {
		return nil, err
	}
	ctx, err := glfw.CreateWindowDeferringContext(1, 1, "", share)
	if err != nil {
		return nil, fmt.Errorf("desktop: create context window: %w", err)
	}
	return ctx, nil
}

// setContextHints asks GLFW for a context the renderer can use: OpenGL
// 3.2 core, or OpenGL ES 3.0 where only ES was found. It runs on the
// main thread.
func (d *Driver) setContextHints() error {
	hints := [][2]int{{int(glfw.ContextVersionMajor), 3}}
	if d.isES {
		hints = append(hints,
			[2]int{int(glfw.ClientAPI), glfw.OpenGLESAPI},
			[2]int{int(glfw.ContextVersionMinor), 0},
			[2]int{int(glfw.ContextCreationAPI), glfw.EGLContextAPI})
	} else {
		hints = append(hints,
			[2]int{int(glfw.ClientAPI), glfw.OpenGLAPI},
			[2]int{int(glfw.ContextVersionMinor), 2},
			[2]int{int(glfw.OpenGLProfile), glfw.OpenGLCoreProfile},
			[2]int{int(glfw.OpenGLForwardCompat), glfw.True})
	}
	for _, h := range hints {
		if err := glfw.WindowHint(glfw.Hint(h[0]), h[1]); err != nil {
			return err
		}
	}
	return nil
}

// shareGroup returns the hidden window every window's context shares
// with, creating it on first use with the context hints already set. It
// runs on the main thread.
func (d *Driver) shareGroup() (*glfw.Window, error) {
	if d.shareRoot != nil {
		return d.shareRoot, nil
	}
	if err := glfw.WindowHint(glfw.Visible, glfw.False); err != nil {
		return nil, err
	}
	root, err := glfw.CreateWindow(1, 1, "", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("desktop: create share context: %w", err)
	}
	d.shareRoot = root
	return root, nil
}

// openWindow opens a window on the main thread and starts its render
// thread.
func (d *Driver) openWindow(o driver.Options) (*Window, error) {
	if err := glfw.DefaultWindowHints(); err != nil {
		return nil, err
	}
	if err := d.setContextHints(); err != nil {
		return nil, err
	}
	// Every window shares GL objects with the one share group.
	share, shareErr := d.shareGroup()
	if shareErr != nil {
		return nil, shareErr
	}
	share0 := share
	hints := [][2]int{
		{int(glfw.Visible), glfw.False},
		{int(glfw.DoubleBuffer), glfw.True},
		// Size the window in logical pixels on a scaled monitor.
		{int(glfw.ScaleToMonitor), glfw.True},
	}
	if d.dxgi {
		// The window shows what DXGI presents and holds no context of
		// its own; a hidden window holds it. With no redirection surface,
		// nothing the window would otherwise draw shows under the frame.
		hints = append(hints,
			[2]int{int(glfw.ClientAPI), glfw.NoAPI},
			[2]int{int(glfw.Win32NoRedirectionBitmap), glfw.True})
		share = nil
	}
	switch o.Kind {
	case driver.KindPopup:
		hints = append(hints,
			[2]int{int(glfw.Popup), glfw.True},
			// Cocoa has no popup hint yet; there, a popup is a borderless
			// window floating above the rest.
			[2]int{int(glfw.Floating), glfw.True},
			[2]int{int(glfw.Decorated), glfw.False},
			[2]int{int(glfw.FocusOnShow), glfw.False})
		if !d.dxgi {
			// DXGI's frames carry alpha; OpenGL's need a transparent
			// framebuffer to.
			hints = append(hints, [2]int{int(glfw.TransparentFramebuffer), glfw.True})
		}
	case driver.KindUtility:
		hints = append(hints, [2]int{int(glfw.Floating), glfw.True})
	case driver.KindNormal:
	}
	if o.Passthrough {
		hints = append(hints, [2]int{int(glfw.MousePassthrough), glfw.True})
	}
	if o.Fixed {
		// Not resizable: on Windows no thick frame or maximize box, so
		// neither the edges, Snap nor the Windows key and an arrow size
		// it; on X11 a minimum size the same as the maximum; on macOS no
		// resizable style.
		hints = append(hints, [2]int{int(glfw.Resizable), glfw.False})
	}
	for _, h := range hints {
		if err := glfw.WindowHint(glfw.Hint(h[0]), h[1]); err != nil {
			return nil, err
		}
	}

	size := o.Size
	if size.W <= 0 || size.H <= 0 {
		size = geom.Sz(800, 600)
	}
	gw, err := glfw.CreateWindow(int(size.W), int(size.H), o.Title, nil, share)
	if err != nil {
		return nil, fmt.Errorf("desktop: create window: %w", err)
	}

	w := newWindow(d, gw)
	if d.dxgi {
		ctx, err := d.contextWindow(share0)
		if err != nil {
			_ = gw.Destroy()
			return nil, err
		}
		w.ctx = ctx
	}
	if o.Kind == driver.KindPopup {
		t, err := gw.GetAttrib(glfw.TransparentFramebuffer)
		w.transparent = d.dxgi || (err == nil && t == glfw.True)
	}
	if err := w.position(o); err != nil {
		_ = gw.Destroy()
		return nil, err
	}
	if p, ok := o.Parent.(*Window); ok && o.Kind == driver.KindPopup && o.Owned {
		if err := own(w, p); err != nil {
			_ = gw.Destroy()
			return nil, fmt.Errorf("desktop: own popup: %w", err)
		}
	}
	if p, ok := o.Parent.(*Window); ok {
		w.textRendering = o.Text.Or(p.textRendering)
	} else {
		sys, err := systemText()
		if err != nil {
			_ = gw.Destroy()
			return nil, err
		}
		w.textRendering = o.Text.Or(sys)
	}
	if len(o.Icons) > 0 {
		// macOS has no window icons, and says so; a window without one
		// is no reason to fail.
		_ = gw.SetIcon(o.Icons)
	}
	w.install()
	if o.DragFromBehind && o.Kind == driver.KindNormal {
		gw.SetDragFromBehind(true)
	}
	if o.Chromeless && o.Kind == driver.KindNormal {
		w.mu.Lock()
		w.border = o.Border
		w.mu.Unlock()
		w.setChromeless()
		if err := applyBorder(w); err != nil {
			_ = gw.Destroy()
			return nil, err
		}
		if o.Monitor != nil {
			// Windows keeps the outer corner as the frame goes, so the
			// content moves up and left by the frame: placed again, the
			// window is its content alone, and lands where it should.
			if err := w.position(o); err != nil {
				_ = gw.Destroy()
				return nil, err
			}
		}
	}
	// A saved placement, for an ordinary window, takes the place of Size and Monitor
	var placed driver.Placement
	var place bool
	var bounds [4]int
	if o.Place != nil && o.Kind == driver.KindNormal && o.Parent == nil {
		placed, bounds, place = w.placeAt(*o.Place)
	}
	if o.Fixed {
		// A fixed window opens at its bounds, never maximized.
		placed.Maximized = false
	}
	if place && placed.Maximized && maximizeHidden {
		_ = gw.Maximize()
	}
	w.accessOpen()
	if x, y, err := gw.GetPos(); err == nil {
		fw, fh, _ := gw.GetFramebufferSize()
		w.debugf("made at %d,%d, framebuffer %dx%d, kind %d", x, y, fw, fh, o.Kind)
	}
	if !o.Hidden {
		if o.Kind == driver.KindNormal {
			// Kept off the screen until its first frame is there; see
			// cloak. A frame that never comes uncovers it all the same.
			cloak(w, true)
			w.covered = true
			w.focusUncovered = runtime.GOOS == "windows"
			time.AfterFunc(time.Second, w.uncover)
		}
		if err := gw.Show(); err != nil {
			_ = gw.Destroy()
			return nil, err
		}
		w.debugf("shown")
	}
	if place && placeAgainShown && !o.Hidden {
		// The bounds chosen, not the newest ones noted: a window sized
		// again for a monitor's scale as it showed noted those too.
		w.applyBounds(bounds)
		w.normals = append(w.normals[:0], bounds)
	}
	if place && placed.Maximized && !maximizeHidden {
		_ = gw.Maximize()
	}
	w.measure()
	d.windows[gw] = w
	d.opened = true
	go w.render()
	return w, nil
}
