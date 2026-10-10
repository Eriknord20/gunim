//go:build linux || windows || darwin

package desktop

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/internal/inbox"
	"github.com/marrasen/gunim/driver/internal/render"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/internal/glfw"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
)

// errClosed is returned by Present once the window has closed.
var errClosed = errors.New("desktop: window is closed")

const (
	// doubleClick is how close in time and space two presses must be to
	// count as one sequence.
	doubleClick     = 500 * time.Millisecond
	doubleClickSlop = 4
	// scrollLine is how far one notch of a wheel scrolls, in logical
	// pixels.
	scrollLine = 40
	// resizeWait is the longest a resize waits for a frame at its new
	// size.
	resizeWait = 100 * time.Millisecond
)

// holdResize is true where the operating system shows a resized window
// as soon as its resize callback returns: Windows runs a modal loop
// during a drag-resize and calls back from inside it. Holding the
// callback until a frame at the new size has been drawn keeps frames
// drawn for the old size off the screen.
const holdResize = runtime.GOOS == "windows"

// Window is the desktop implementation of [driver.Window].
type Window struct {
	d  *Driver
	gw *glfw.Window

	// chrome is the title bar a chromeless window's application draws:
	// see chrome.go.
	chrome chrome

	in        *inbox.Inbox
	presented chan driver.Frame
	frames    chan frame
	quit      chan struct{}
	done      chan struct{}
	// closeOnce runs Close once; quitOnce closes quit once, whichever
	// of Close and shutdown gets there first. They are separate so that
	// shutdown, run on the main thread while Close waits for it, waits on
	// quitOnce alone.
	closeOnce sync.Once
	quitOnce  sync.Once

	// mu guards what the main thread measures and the render thread
	// and the engine read.
	mu  sync.Mutex
	fbW int
	fbH int
	// scale is content times zoom: the monitor's scale, and the factor the user zoomed by.
	scale   float32
	content float32
	zoom    float32
	rate    float64
	// perCoord is framebuffer pixels per GLFW screen coordinate. It is 1
	// on X11 and Windows, and 2 on a Retina display.
	perCoord float32
	// origin is the client area's top left corner in screen
	// coordinates.
	origin geom.Point
	// full says the window fills its monitor, and windowed is where it
	// was and how big, in screen coordinates, to go back to.
	full     bool
	windowed [4]int
	err      error
	// under is the colour the engine last set under the window's frames.
	under color.NRGBA
	// fading says the window is last drawn partly there; see SetFade.
	fading bool
	// shadow says gunim draws the window's shadow, and shadowAt and shadowScale are the opacity and scale it was last
	// given; see startShadow.
	shadow      bool
	shadowAt    float32
	shadowScale float32
	// border is the line round a chromeless window's edge the application asked for, and edge the width, in device
	// pixels, a drawn one is drawn at now; see applyBorder.
	border driver.Border
	edge   float32
	// covered says the window is cloaked, which the drawn shadow follows. It is used on the main thread.
	covered bool
	// focusUncovered asks for the keyboard again as the window is
	// uncovered: Windows does not bring a cloaked window to the front, so
	// the focus asked for as it was shown is lost, and the window opens
	// behind the one the user started it from.
	focusUncovered bool
	// hiddenBy says the system reports the window out of sight while it
	// is open, covered by other windows or on another desktop, and
	// unseen what the window was told last, with the screen's state.
	// They are used on the main thread.
	hiddenBy, unseen bool
	// uncovered puts the window on the screen once; see uncover.
	uncovered sync.Once
	// opened is when the window was made, for GUNIM_DEBUG_WINDOW.
	opened time.Time
	// caret is the text caret the engine last reported, in window space
	// and logical pixels. Platform code places an input method's
	// composition and candidate windows from it.
	caret geom.Rect
	// drawnW and drawnH are the framebuffer size of the last frame the
	// render thread swapped, and drew is nudged after each swap.
	drawnW, drawnH int
	drew           chan struct{}
	// placing says Place is sizing the window, which then waits for no frame at the new size. It is used on the main
	// thread.
	placing bool
	// region is where the window takes the pointer, in logical pixels, and regionSet says it is limited at all; see
	// SetPointerRegion. They are used on the main thread.
	region    []geom.Rect
	regionSet bool
	// readback, when set by a test, receives each frame's pixels as
	// RGBA rows from the bottom up, read before the swap.
	readback func(pix []byte, w, h int)
	// shot, when set, receives the next frame's pixels once, the right
	// way up.
	shot func(*image.RGBA)

	// textInput is whether the application is taking text, which the
	// input method asks from the main thread.
	textInput atomic.Bool

	// ctx, where windows present through DXGI, is the hidden window
	// that holds this window's context, and pres, which belongs to the
	// render thread, presents its frames.
	ctx  *glfw.Window
	pres *presenter

	// acc is the window's place with assistive technology, and focused
	// whether it has the keyboard.
	acc     windowAccess
	focused atomic.Bool

	// parent is the window a popup belongs to, and popup is set for a
	// popup. Both are set once, before the window shows.
	parent *Window
	popup  bool
	// over puts a popup's corner at its anchor's, as
	// [driver.Options.Over] asks.
	over bool
	// above puts a popup above its anchor where there is room; see [driver.Options.Above].
	above bool
	// transparent is whether the window blends with what is behind it,
	// read once as it opens.
	transparent bool
	// textRendering is how the window draws text, settled as it opens.
	textRendering text.Rendering

	// The fields below belong to the main thread.
	//
	// anchor is where a popup was last attached, and popups are the
	// open popups that belong to this window, which follow it as it
	// moves.
	anchor     geom.Rect
	popups     []*Window
	closed     bool
	cursor     geom.Point
	mods       input.Mods
	lastPress  time.Time
	lastButton input.Button
	lastPos    geom.Point
	clicks     int
	// behind says a press left the window behind another and the button
	// is still down, and escaped that Escape was held at the last look;
	// see [glfw.Window.SetDragFromBehind]. The window hears no keys then,
	// so the modifiers and Escape are asked of the system as the pointer
	// moves.
	behind  bool
	escaped bool
	// normals are the last few places and sizes the window had while it
	// was neither maximized, minimized nor full screen, the latest last,
	// in screen coordinates as x, y, width, height. The latest is where
	// the window goes back to; see noteNormal.
	normals [][4]int
}

func newWindow(d *Driver, gw *glfw.Window) *Window {
	w := &Window{
		d:         d,
		opened:    time.Now(),
		gw:        gw,
		presented: make(chan driver.Frame, 1),
		frames:    make(chan frame, 1),
		quit:      make(chan struct{}),
		drew:      make(chan struct{}, 1),
		done:      make(chan struct{}),
		scale:     1,
		content:   1,
		zoom:      1,
		rate:      60,
		perCoord:  1,
	}
	w.in = inbox.New(w.quit)
	return w
}

// Presented implements [driver.Window].
func (w *Window) Presented() <-chan driver.Frame { return w.presented }

// Input implements [driver.Window].
func (w *Window) Input() <-chan any { return w.in.Out() }

// Present implements [driver.Window]. It hands ops to the render thread
// and returns at once.
func (w *Window) Present(ops []paint.Op, damage geom.Rect) error {
	w.mu.Lock()
	err := w.err
	w.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case <-w.quit:
		return errClosed
	case w.frames <- frame{ops, damage}:
		return nil
	default:
		return errors.New("desktop: Present called with a frame already in flight")
	}
}

// Size implements [driver.Window].
func (w *Window) Size() geom.Size {
	w.mu.Lock()
	defer w.mu.Unlock()
	return geom.Sz(float32(w.fbW)/w.scale, float32(w.fbH)/w.scale)
}

// SetBackground implements [driver.Backgrounder].
func (w *Window) SetBackground(c color.NRGBA) {
	w.mu.Lock()
	w.under = c
	w.mu.Unlock()
}

// SetFade implements [driver.Fader]. A drawn shadow and border fade and shrink with the window. The border and
// shadow the system draws round a chromeless window cannot, so they are hidden while it fades; see showFrame.
func (w *Window) SetFade(opacity, scale float32) {
	fading := opacity < 1
	w.mu.Lock()
	changed := fading != w.fading
	w.fading = fading
	shadow := w.shadow && (opacity != w.shadowAt || scale != w.shadowScale)
	w.shadowAt, w.shadowScale = opacity, scale
	w.mu.Unlock()
	if shadow {
		w.d.post(func() {
			if !w.closed && !w.covered {
				fadeShadow(w, opacity, scale)
			}
		})
	}
	if !changed {
		return
	}
	w.debugf("fading %v, at %.2f", fading, opacity)
	if !w.Chromeless() {
		return
	}
	posted := w.d.post(func() {
		if w.closed {
			w.debugf("frame left alone: the window has closed")
			return
		}
		showFrame(w, !fading)
	})
	if !posted {
		w.debugf("frame left alone: the driver has stopped")
	}
}

// SetZoom implements [driver.Zoomer]. The pointer is read again at a
// new zoom; see pointerAgain.
//
// The same zoom changes nothing, so the pointer is left alone. The
// engine gives every popup its zoom as it opens, and a menu opened by a
// press under the pointer would otherwise hear a move nobody made.
func (w *Window) SetZoom(z float32) {
	w.mu.Lock()
	same := w.zoom == z
	w.zoom = z
	w.scale = w.content * z
	w.mu.Unlock()
	if same {
		return
	}
	w.in.Push(driver.Redraw{})
	w.d.post(w.pointerAgain)
}

// pointerAgain reads the pointer again, as the window's scale changes
// under it, and where it rests over the window reports its new logical
// point as a move, so hover and the pointer's shape follow. It runs on
// the main thread.
func (w *Window) pointerAgain() {
	if w.closed {
		return
	}
	if over, err := w.gw.GetAttrib(glfw.Hovered); err != nil || over != glfw.True {
		return
	}
	x, y, err := w.gw.GetCursorPos()
	if err != nil {
		return
	}
	w.cursor = w.logical(x, y)
	w.pointerf("move to %.1f,%.1f, read again at scale %.2f", w.cursor.X, w.cursor.Y, w.Scale())
	w.in.Push(input.PointerMove{Pos: w.cursor, Mods: w.mods, Time: time.Now()})
}

// Scale implements [driver.Window].
func (w *Window) Scale() float32 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.scale
}

// RefreshRate implements [driver.Window].
func (w *Window) RefreshRate() float64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rate
}

// SetTextCaret implements [driver.CaretPlacer]. It keeps the caret for
// the platform's input method.
func (w *Window) SetTextCaret(r geom.Rect) {
	w.mu.Lock()
	w.caret = r
	w.mu.Unlock()
	caretMoved(w)
}

// SetTextInput implements [driver.TextInputter].
func (w *Window) SetTextInput(active bool) {
	if w.textInput.Swap(active) == active {
		return
	}
	w.d.post(func() { textInputChanged(w, active) })
}

// Clipboard implements [driver.Window]. It reads the clipboard on the
// main thread, where GLFW has to, and waits for the answer.
func (w *Window) Clipboard() (string, error) {
	var s string
	err := w.d.call(func() error {
		var err error
		s, err = glfw.GetClipboardString()
		return err
	})
	// Nothing in a form that reads as text is an empty clipboard, not a
	// failure: only a clipboard that could not be read is one.
	if errors.Is(err, glfw.FormatUnavailable) {
		return "", nil
	}
	return s, err
}

// ClipboardImage implements [driver.ImageClipboard]. It reads the clipboard on the main thread, where GLFW has to.
func (w *Window) ClipboardImage() ([]byte, error) {
	var b []byte
	err := w.d.call(func() error {
		var err error
		b, err = glfw.GetClipboardImage()
		return err
	})
	return b, err
}

// SetClipboardImage implements [driver.ImageClipboard]. It decodes png and readies it for the platform on the
// caller's goroutine, so a large picture does not hold up the main thread, and puts it on the clipboard on the main
// thread, where GLFW has to.
func (w *Window) SetClipboardImage(png []byte) error {
	img, err := glfw.PrepareClipboardImage(png)
	if err != nil {
		return err
	}
	return w.d.call(func() error { return glfw.SetClipboardImage(img) })
}

var _ driver.ImageClipboard = (*Window)(nil)

// SetClipboard implements [driver.Window].
func (w *Window) SetClipboard(s string) error {
	return w.d.call(func() error { return w.gw.SetClipboardString(s) })
}

// Close implements [driver.Window]. It stops the render thread, then
// destroys the window on the main thread.
func (w *Window) Close() error {
	var err error
	w.closeOnce.Do(func() {
		w.stopRender()
		start := time.Now()
		<-w.done
		if took := time.Since(start); took > slowCall && (pointerDebug || windowDebug) {
			fmt.Fprintf(os.Stderr, "gunim stall %s: closing window %p waited %.0f ms for its render thread\n",
				time.Now().Format("15:04:05.000"), w, float64(took.Microseconds())/1000)
		}
		err = w.d.call(func() error {
			w.shutdown()
			return nil
		})
		if errors.Is(err, errStopped) {
			// Run has ended and destroyed every window already.
			err = nil
		}
	})
	return err
}

// shutdown destroys the window. It runs on the main thread, and is
// safe to call twice.
func (w *Window) shutdown() {
	if w.closed {
		return
	}
	w.closed = true
	w.debugf("closed")
	w.accessClose()
	if w.parent != nil {
		w.parent.popups = slices.DeleteFunc(w.parent.popups, func(c *Window) bool { return c == w })
	}
	w.in.Close()
	w.stopRender()
	<-w.done
	delete(w.d.windows, w.gw)
	if w.d.lastUsed == w {
		w.d.lastUsed = nil
	}
	_ = w.gw.Destroy()
	if w.ctx != nil {
		_ = w.ctx.Destroy()
	}
}

// awaitFrameAtSize blocks the main thread until the render thread has
// swapped a frame at the current framebuffer size, or resizeWait has
// passed. It runs on the main thread, inside a resize.
func (w *Window) awaitFrameAtSize() {
	timeout := time.NewTimer(resizeWait)
	defer timeout.Stop()
	start := time.Now()
	for {
		w.mu.Lock()
		// A window that has drawn nothing yet is not on the screen
		done := w.drawnW == w.fbW && w.drawnH == w.fbH || w.drawnW == 0 && w.drawnH == 0
		w.mu.Unlock()
		if done {
			w.debugf("frame at the new size after %v", time.Since(start).Round(time.Millisecond))
			return
		}
		select {
		case <-w.drew:
		case <-w.d.posted:
			// The UI goroutine may be waiting on the main thread to draw the frame
			w.d.runTasks()
		case <-timeout.C:
			w.debugf("no frame at the new size after %v", time.Since(start).Round(time.Millisecond))
			return
		case <-w.quit:
			return
		}
	}
}

// stopRender tells the render thread and the input feed to stop.
func (w *Window) stopRender() { w.quitOnce.Do(func() { close(w.quit) }) }

// position moves the window where the options ask: attached to its
// anchor, or centred on a chosen monitor. It runs on the main thread.
func (w *Window) position(o driver.Options) error {
	if p, ok := o.Parent.(*Window); ok {
		if o.Kind == driver.KindPopup {
			w.parent, w.popup, w.over, w.above = p, true, o.Over, o.Above
			return w.attach(o.Anchor)
		}
		px, py, err := p.gw.GetPos()
		if err != nil {
			return err
		}
		f := p.coordsPerLogical()
		return w.gw.SetPos(px+int(o.Anchor.Min.X*f), py+int(o.Anchor.Min.Y*f))
	}
	if o.Monitor == nil {
		return nil
	}
	ww, wh, err := w.gw.GetSize()
	if err != nil {
		return err
	}
	// On Windows a window that scales to its monitor grows by the ratio
	// of the two monitors' scales once it arrives, so centre it at that
	// size.
	if runtime.GOOS == "windows" && o.Monitor.Scale > 0 {
		if from, _, err := w.gw.GetContentScale(); err == nil && from > 0 {
			ww = int(float32(ww) * o.Monitor.Scale / from)
			wh = int(float32(wh) * o.Monitor.Scale / from)
		}
	}
	// It goes in the middle of the part of the monitor windows may use,
	// clear of the task bar, frame and all, and where it is too large
	// for that, from its top left corner.
	b := o.Monitor.WorkArea
	if b.Empty() {
		b = o.Monitor.Bounds
	}
	// A window that draws its own title bar loses the system's frame
	// once it is made, and is its content alone; one that keeps the
	// frame is that much larger.
	l, t, r, bt := 0, 0, 0, 0
	if !o.Chromeless || o.Kind != driver.KindNormal {
		var ferr error
		if l, t, r, bt, ferr = w.gw.GetFrameSize(); ferr != nil {
			l, t, r, bt = 0, 0, 0, 0
		}
	}
	ow, oh := ww+l+r, wh+t+bt
	x := int(b.Min.X) + max((int(b.Size().W)-ow)/2, 0) + l
	y := int(b.Min.Y) + max((int(b.Size().H)-oh)/2, 0) + t
	return w.gw.SetPos(x, y)
}

// coordsPerLogical is GLFW screen coordinates per logical pixel.
func (w *Window) coordsPerLogical() float32 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.scale / w.perCoord
}

// attach puts a popup beside anchor, a rectangle in its parent's
// logical space, as [driver.Options] describes. It runs on the main
// thread.
func (w *Window) attach(anchor geom.Rect) error {
	p := w.parent
	w.anchor = anchor
	if !slices.Contains(p.popups, w) {
		p.popups = append(p.popups, w)
	}
	px, py, err := p.gw.GetPos()
	if err != nil {
		return err
	}
	f := p.coordsPerLogical()
	a := geom.Rect{
		Min: geom.Pt(float32(px)+anchor.Min.X*f, float32(py)+anchor.Min.Y*f),
		Max: geom.Pt(float32(px)+anchor.Max.X*f, float32(py)+anchor.Max.Y*f),
	}
	ww, wh, err := w.gw.GetSize()
	if err != nil {
		return err
	}
	area := popupArea(a, workArea)
	x, y := popupAt(a, float32(ww), float32(wh), area, w.above)
	if w.over {
		x, y = a.Min.X, a.Min.Y
	}
	if popupDebug {
		fmt.Fprintf(os.Stderr, "gunim popup: parent at %d,%d, %.2f per logical pixel; anchor %v on screen; popup %dx%d in work area %v, put at %.0f,%.0f\n",
			px, py, f, a, ww, wh, area, x, y)
		if ms, err := glfw.GetMonitors(); err == nil {
			for _, m := range ms {
				name, _ := m.GetName()
				mx, my, mw, mh, err := m.GetWorkarea()
				fmt.Fprintf(os.Stderr, "gunim popup:   monitor %s: work area %d,%d %dx%d, %v\n", name, mx, my, mw, mh, err)
			}
		}
	}
	return w.gw.SetPos(int(x), int(y))
}

// popupDebug is set by GUNIM_DEBUG_POPUP=1, which logs where each
// popup is put to standard error: the parent, the anchor on screen, the
// work area it was kept inside, and every monitor's work area.
var popupDebug = os.Getenv("GUNIM_DEBUG_POPUP") == "1"

// PopupRoom implements [driver.PopupRoomer]. Where the window or its monitor cannot say, there is no end to the room.
func (w *Window) PopupRoom(anchor geom.Rect) driver.Room {
	room := driver.NoRoomLimit
	_ = w.d.call(func() error {
		if w.closed {
			return nil
		}
		px, py, err := w.gw.GetPos()
		if err != nil {
			return err
		}
		f := w.coordsPerLogical()
		a := geom.Rect{
			Min: geom.Pt(float32(px)+anchor.Min.X*f, float32(py)+anchor.Min.Y*f),
			Max: geom.Pt(float32(px)+anchor.Max.X*f, float32(py)+anchor.Max.Y*f),
		}
		area := popupArea(a, workArea)
		if area.Empty() || f <= 0 {
			return nil
		}
		room = driver.Room{
			Below: (area.Max.Y - a.Max.Y) / f, Above: (a.Min.Y - area.Min.Y) / f,
			Left: (a.Min.X - area.Min.X) / f, Right: (area.Max.X - a.Min.X) / f,
		}
		return nil
	})
	return room
}

// popupArea is the work area a popup for anchor a is kept inside: the
// one under the anchor's middle. The anchor's corner can lie off the
// window, as a menu's shadow margin reaches past a maximized window's
// edge, onto the monitor beside it.
func popupArea(a geom.Rect, areaAt func(geom.Point) geom.Rect) geom.Rect {
	return areaAt(a.Center())
}

// popupAt returns where a popup of size w×h goes for anchor a, all in
// screen coordinates: below a, or above it when the room below runs
// out and there is more above, and slid sideways to stay inside area.
// With above it prefers above a, and goes below where the room above
// runs out and there is more below.
func popupAt(a geom.Rect, w, h float32, area geom.Rect, above bool) (x, y float32) {
	x, y = a.Min.X, a.Max.Y
	if above {
		y = a.Min.Y - h
	}
	if area.Empty() {
		return x, y
	}
	roomAbove, roomBelow := a.Min.Y-area.Min.Y, area.Max.Y-a.Max.Y
	switch {
	case !above && y+h > area.Max.Y && roomAbove > roomBelow:
		y = a.Min.Y - h
	case above && y < area.Min.Y && roomBelow > roomAbove:
		y = a.Max.Y
	}
	x = max(area.Min.X, min(x, area.Max.X-w))
	y = max(area.Min.Y, min(y, area.Max.Y-h))
	return x, y
}

// dragOutWait is how long a drag handed to another program may wait
// for it to finish taking the drop before it is given up.
const dragOutWait = 10 * time.Second

// DragOut implements [driver.DragOuter]. The platform's drag and drop
// runs on the main thread; on Windows it holds the main thread until
// the drop, so this returns at once and reports the end on Input.
func (w *Window) DragOut(paths []string) error {
	ok := w.d.post(func() {
		ended := false
		end := func(taken, back bool) {
			ended = true
			e := driver.DragOutEnded{Taken: taken, Back: back}
			if back {
				x, y, err := w.gw.GetCursorPos()
				if err != nil {
					// Without the pointer the drag cannot carry on, so it ends untaken
					w.debugf("drag back: %v", err)
					e.Back = false
				}
				e.At = w.logical(x, y)
			}
			w.in.Push(e)
		}
		if err := w.gw.StartDragOut(paths, end); err != nil {
			end(false, false)
			return
		}
		time.AfterFunc(dragOutWait, func() {
			w.d.post(func() {
				if !ended {
					w.gw.CancelDragOut()
				}
			})
		})
	})
	if !ok {
		return errStopped
	}
	return nil
}

// ToScreen implements [driver.Screener].
func (w *Window) ToScreen(p geom.Point) geom.Point {
	w.mu.Lock()
	defer w.mu.Unlock()
	f := w.scale / w.perCoord
	return geom.Pt(w.origin.X+p.X*f, w.origin.Y+p.Y*f)
}

// FromScreen implements [driver.Screener].
func (w *Window) FromScreen(p geom.Point) geom.Point {
	w.mu.Lock()
	defer w.mu.Unlock()
	f := w.scale / w.perCoord
	return geom.Pt((p.X-w.origin.X)/f, (p.Y-w.origin.Y)/f)
}

// hasFocus reports whether the window has the keyboard.
func (w *Window) hasFocus() bool { return w.focused.Load() }

// Transparent implements [driver.Transparent].
func (w *Window) Transparent() bool { return w.transparent }

// Place implements [driver.Placer].
func (w *Window) Place(anchor geom.Rect, size geom.Size) error {
	return w.d.call(func() error {
		if w.closed || w.parent == nil {
			return nil
		}
		f := w.coordsPerLogical()
		ww, wh := max(1, int(size.W*f+0.5)), max(1, int(size.H*f+0.5))
		cw, ch, gerr := w.gw.GetSize()
		if gerr == nil && (cw != ww || ch != wh) {
			// The engine waits on this call, and draws the frame at the new size after it
			w.placing = true
			err := w.gw.SetSize(ww, wh)
			w.placing = false
			if err != nil {
				return err
			}
		}
		if popupDebug {
			nw, nh, _ := w.gw.GetSize()
			w.mu.Lock()
			fbW, fbH, scale := w.fbW, w.fbH, w.scale
			w.mu.Unlock()
			fmt.Fprintf(os.Stderr, "gunim popup: a spare placed for %vx%v logical at %.2f per logical: was %dx%d, asked %dx%d, now %dx%d, framebuffer %dx%d at scale %.2f\n",
				size.W, size.H, f, cw, ch, ww, wh, nw, nh, fbW, fbH, scale)
		}
		return w.attach(anchor)
	})
}

// measure reads the window's framebuffer, scale and monitor. It runs
// on the main thread.
//
// A minimized window on Windows has an empty framebuffer, and sits at
// -32000, -32000. It keeps the size and place it had, so what is laid
// out in it, such as a terminal telling its shell how wide it is, stays
// as it was until the window comes back.
func (w *Window) measure() {
	fbW, fbH, err := w.gw.GetFramebufferSize()
	if err != nil || fbW <= 0 || fbH <= 0 {
		return
	}
	ww, _, err := w.gw.GetSize()
	if err != nil {
		return
	}
	scale, _, err := w.gw.GetContentScale()
	if err != nil || scale <= 0 {
		scale = 1
	}
	perCoord := float32(1)
	if ww > 0 {
		perCoord = float32(fbW) / float32(ww)
	}
	rate := w.monitorRate()
	x, y, err := w.gw.GetPos()
	if err != nil {
		x, y = 0, 0
	}

	w.mu.Lock()
	w.fbW, w.fbH, w.content, w.scale, w.perCoord = fbW, fbH, scale, scale*w.zoom, perCoord
	w.origin = geom.Pt(float32(x), float32(y))
	if rate > 0 {
		w.rate = rate
	}
	w.mu.Unlock()
	// The region is in logical pixels, and the window's own may have changed size
	w.applyRegion()
}

// monitorRate returns the refresh rate of the monitor holding the
// window's centre. It runs on the main thread.
func (w *Window) monitorRate() float64 {
	x, y, err := w.gw.GetPos()
	if err != nil {
		return 0
	}
	ww, wh, err := w.gw.GetSize()
	if err != nil {
		return 0
	}
	centre := geom.Pt(float32(x+ww/2), float32(y+wh/2))
	ms, err := glfw.GetMonitors()
	if err != nil {
		return 0
	}
	for _, m := range ms {
		if info, ok := monitorInfo(m); ok && info.Bounds.Contains(centre) {
			return info.RefreshRate
		}
	}
	if m, err := glfw.GetPrimaryMonitor(); err == nil && m != nil {
		if info, ok := monitorInfo(m); ok {
			return info.RefreshRate
		}
	}
	return 0
}

// logical converts a GLFW screen coordinate to logical pixels.
func (w *Window) logical(x, y float64) geom.Point {
	w.mu.Lock()
	f := w.perCoord / w.scale
	w.mu.Unlock()
	return geom.Pt(float32(x)*f, float32(y)*f)
}

// install connects GLFW's callbacks. They all run on the main thread.
func (w *Window) install() {
	gw := w.gw
	watchMoveSize(w)
	remeasure := func() {
		was := w.Scale()
		w.measure()
		w.in.Push(driver.Redraw{})
		if w.Scale() != was {
			// Onto a monitor of another scale: the pointer resting over
			// the window is at another logical point.
			w.pointerAgain()
		}
	}
	_, _ = gw.SetFramebufferSizeCallback(func(_ *glfw.Window, width, height int) {
		w.debugf("framebuffer %dx%d", width, height)
		remeasure()
		w.noteNormal()
		// A minimized window draws nothing to wait for, and a popup being placed draws only once Place returns.
		if holdResize && width > 0 && height > 0 && !w.placing {
			w.awaitFrameAtSize()
		}
	})
	_, _ = gw.SetContentScaleCallback(func(_ *glfw.Window, x, _ float32) {
		w.debugf("content scale %v", x)
		remeasure()
	})
	_, _ = gw.SetPosCallback(func(_ *glfw.Window, x, y int) {
		w.debugf("moved to %d,%d", x, y)
		remeasure()
		w.noteNormal()
		for _, c := range w.popups {
			_ = c.attach(c.anchor)
		}
	})
	_, _ = gw.SetMaximizeCallback(func(_ *glfw.Window, maximized bool) {
		w.chrome.mu.Lock()
		w.chrome.maximized = maximized
		w.chrome.mu.Unlock()
		if maximized {
			w.forgetMaximized()
		}
		w.in.Push(driver.WindowMaximized{Maximized: maximized})
	})
	_, _ = gw.SetRefreshCallback(func(*glfw.Window) { w.in.Push(driver.Redraw{}) })
	_, _ = gw.SetCoveredCallback(func(_ *glfw.Window, covered bool) {
		w.hiddenBy = covered
		w.tellUnseen()
	})
	_, _ = gw.SetIconifyCallback(func(_ *glfw.Window, iconified bool) {
		w.in.Push(driver.WindowShown{Shown: !iconified})
	})
	_, _ = gw.SetFocusCallback(func(_ *glfw.Window, focused bool) {
		w.focused.Store(focused)
		if focused {
			if w.parent == nil {
				// The system's media controls speak to the window used
				// last.
				w.d.lastUsed = w
			}
			// The window hears keys itself again.
			w.behind = false
			// A modifier let go while another window had the keyboard
			// sent this one no event.
			w.mods = modsOf(gw.HeldModifiers())
		}
		w.accessFocus(focused)
		w.pointerf("keyboard %v", focused)
		w.in.Push(driver.WindowFocus{Focused: focused})
	})
	_, _ = gw.SetCloseCallback(func(gw *glfw.Window) {
		// The engine decides: it closes the window, or asks the
		// application first.
		_ = gw.SetShouldClose(false)
		w.in.Push(driver.CloseAsked{})
	})

	_, _ = gw.SetCursorPosCallback(func(_ *glfw.Window, x, y float64) {
		w.cursor = w.logical(x, y)
		w.pointerf("move to %.1f,%.1f", w.cursor.X, w.cursor.Y)
		if w.behind {
			w.mods = modsOf(gw.HeldModifiers())
			esc := gw.EscapeHeld()
			if esc && !w.escaped {
				w.in.Push(input.KeyPress{Key: input.KeyEscape, Mods: w.mods, Time: time.Now()})
			}
			w.escaped = esc
		}
		w.in.Push(input.PointerMove{Pos: w.cursor, Mods: w.mods, Time: time.Now()})
	})
	_, _ = gw.SetCursorEnterCallback(func(_ *glfw.Window, entered bool) {
		w.pointerf("entered %v", entered)
		if !entered {
			w.in.Push(input.PointerLeave{Time: time.Now()})
		}
	})
	_, _ = gw.SetMouseButtonCallback(func(_ *glfw.Window, b glfw.MouseButton, action glfw.Action, mods glfw.ModifierKey) {
		button, ok := buttonOf(b)
		if !ok {
			return
		}
		w.mods = modsOf(mods)
		now := time.Now()
		w.pointerf("button %v %v at %.1f,%.1f", button, action == glfw.Release, w.cursor.X, w.cursor.Y)
		if action == glfw.Release {
			if w.behind {
				w.mods = modsOf(gw.HeldModifiers())
				w.behind = false
			}
			w.in.Push(input.PointerUp{Pos: w.cursor, Button: button, Mods: w.mods, Time: now})
			return
		}
		behind := gw.TakePressedBehind()
		if behind {
			// Another program may have the keyboard, and the modifiers
			// the press carries may be old.
			w.mods = modsOf(gw.HeldModifiers())
			w.behind, w.escaped = true, gw.EscapeHeld()
		}
		d := w.cursor.Sub(w.lastPos)
		if button == w.lastButton && now.Sub(w.lastPress) < doubleClick &&
			abs(d.X) <= doubleClickSlop && abs(d.Y) <= doubleClickSlop {
			w.clicks++
		} else {
			w.clicks = 1
		}
		w.lastPress, w.lastButton, w.lastPos = now, button, w.cursor
		w.in.Push(input.PointerDown{Pos: w.cursor, Button: button, Mods: w.mods, Clicks: w.clicks, Behind: behind, Time: now})
	})
	_, _ = gw.SetDropCallback(func(gw *glfw.Window, names []string) {
		// GLFW moves the cursor to where the files were let go first.
		w.in.Push(input.Drop{Pos: w.cursor, Paths: names, Mods: modsOf(gw.HeldModifiers()), Time: time.Now()})
	})
	_, _ = gw.SetDragOverCallback(func(gw *glfw.Window, x, y float64, paths []string, over bool) {
		if !over {
			// The pointer went with the drag, and crossing the window's
			// edge during a drag tells the window nothing, so it hears
			// here that the pointer left.
			w.in.Push(driver.FilesLeft{})
			w.in.Push(input.PointerLeave{Time: time.Now()})
			return
		}
		w.in.Push(driver.FilesOver{Pos: w.logical(x, y), Paths: paths, Mods: modsOf(gw.HeldModifiers())})
	})
	_, _ = gw.SetScrollCallback(func(gw *glfw.Window, x, y float64) {
		// Asked of the system, as a wheel turns with no key event to
		// say what is held: Ctrl pressed just before, or let go
		// elsewhere, is known all the same.
		w.mods = modsOf(gw.HeldModifiers())
		w.in.Push(input.Scroll{
			Pos:     w.cursor,
			Delta:   geom.Pt(float32(x)*scrollLine, float32(y)*scrollLine),
			Notches: geom.Pt(float32(x), float32(y)),
			Mods:    w.mods,
			Time:    time.Now(),
		})
	})
	_, _ = gw.SetKeyCallback(func(gw *glfw.Window, k glfw.Key, scancode int, action glfw.Action, mods glfw.ModifierKey) {
		w.mods = modsOf(modsAfter(k, action, mods))
		now := time.Now()
		switch action {
		case glfw.Press, glfw.Repeat:
			w.in.Push(input.KeyPress{Key: keyOf(k), Mods: w.mods, Repeat: action == glfw.Repeat, Typed: gw.KeyTyped(), Char: charOf(k, scancode), Time: now})
		case glfw.Release:
			w.in.Push(input.KeyRelease{Key: keyOf(k), Mods: w.mods, Time: now})
		}
	})
	installText(w)
}

// ContentOrigin implements [driver.Positioner]. It asks GLFW on the main thread, where it has to.
func (w *Window) ContentOrigin() (image.Point, error) {
	var x, y int
	err := w.d.call(func() error {
		var err error
		x, y, err = w.gw.GetPos()
		return err
	})
	if err != nil {
		return image.Point{}, fmt.Errorf("desktop: window position: %w", err)
	}
	w.mu.Lock()
	f := float64(w.perCoord)
	w.mu.Unlock()
	return image.Pt(int(math.Round(float64(x)*f)), int(math.Round(float64(y)*f))), nil
}

var _ driver.Positioner = (*Window)(nil)

// Shoot implements [driver.Shooter].
func (w *Window) Shoot(fn func(*image.RGBA)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.shot = fn
}

// shotBack is a readback that turns the pixels, read from the bottom
// up, into a picture the right way up for shot, and hands them on to
// the readback there was.
func shotBack(was func([]byte, int, int), shot func(*image.RGBA)) func([]byte, int, int) {
	return func(pix []byte, w, h int) {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := range h {
			copy(img.Pix[y*img.Stride:(y+1)*img.Stride], pix[(h-1-y)*w*4:(h-y)*w*4])
		}
		go shot(img)
		if was != nil {
			was(pix, w, h)
		}
	}
}

// frame is one frame for the render thread: its ops, and the part of
// the window that changed since the frame before.
type frame struct {
	ops    []paint.Op
	damage geom.Rect
}

// render is the window's render thread. It owns the GL context: it
// replays each frame the engine presents, swaps, and reports the frame
// shown once the swap returns.
func (w *Window) render() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(w.done)

	r, err := w.startGL()
	if err != nil {
		w.fail(err)
	}
	if w.ctx != nil {
		// The context made here goes here, once the renderer has let it
		// go: deleted on another thread while current here, it would be
		// in use.
		defer func() { _ = w.ctx.DeleteContext() }()
	}
	if r != nil {
		defer func() {
			if w.pres != nil {
				w.pres.close()
			}
			r.Release()
			_ = (*glfw.Window)(nil).MakeContextCurrent()
		}()
	}

	vb := newVBlank(w.gw)
	defer vb.close()

	var last time.Time
	var ft frameTimes
	// shownEdge is the edge the last frame presented left for the border
	shownEdge := float32(-1)
	// When a frame draws a big mask stretched, sharpen fires once the mask can be sharp, to draw the frame again,
	// as again. A frame drawn before then takes its place. The engine keeps a frame's ops until it presents the
	// frame after, so they hold.
	var sharpen <-chan time.Time
	var again frame
	for {
		var f frame
		redraw := false
		select {
		case <-w.quit:
			return
		case f = <-w.frames:
		case <-sharpen:
			f, redraw = again, true
		}
		sharpen = nil
		if r != nil {
			r.Corner, r.Edge = w.cornerRadius()
			w.mu.Lock()
			fbW, fbH, scale, rate := w.fbW, w.fbH, w.scale, w.rate
			r.Under = w.under
			readback, shot := w.readback, w.shot
			w.shot = nil
			w.mu.Unlock()
			if shot != nil {
				readback = shotBack(readback, shot)
			}
			t0 := time.Now()
			if w.pres != nil {
				fbo, err := w.pres.begin(fbW, fbH)
				if err != nil {
					w.fail(err)
				}
				r.WindowFBO = fbo
			}
			r.Draw(f.ops, f.damage, fbW, fbH, scale)
			if !r.Stretched.Empty() {
				again = frame{ops: f.ops}
				sharpen = time.After(time.Until(r.SharpAt))
			}
			var gpu time.Duration
			if framesFinish {
				// Waits for the GPU, to time it apart from the rest.
				tf := time.Now()
				r.GL.Finish()
				gpu = time.Since(tf)
			}
			if readback != nil {
				if w.pres != nil {
					// The window's texture is upside down for Direct3D;
					// the canvas holds the frame the right way up.
					r.GL.BindFramebuffer(gl.FRAMEBUFFER, r.Canvas())
				}
				pix := make([]byte, fbW*fbH*4)
				r.GL.ReadPixels(pix, 0, 0, int32(fbW), int32(fbH), gl.RGBA, gl.UNSIGNED_BYTE)
				readback(pix, fbW, fbH)
			}
			t1 := time.Now()
			synced := vb.wait()
			t2 := time.Now()
			if w.pres != nil {
				if err := w.pres.present(r.Redrawn); err != nil {
					w.fail(err)
				}
			} else if err := w.gw.SwapBuffers(); err != nil {
				w.fail(fmt.Errorf("desktop: swap buffers: %w", err))
			}
			if framesDebug {
				ft.add(w, t1.Sub(t0), t2.Sub(t1), time.Since(t2), gpu, &r.Stats, rate, fbW, fbH)
			}
			w.uncover()
			if r.Edge != shownEdge {
				shownEdge = r.Edge
				edgeShown(w, shownEdge)
			}
			w.mu.Lock()
			w.drawnW, w.drawnH = fbW, fbH
			w.mu.Unlock()
			select {
			case w.drew <- struct{}{}:
			default:
			}
			if synced {
				// The frame went out on the monitor's vertical blank,
				// so that is when it reached the screen.
				last = time.Now()
			} else {
				last = pace(last, rate)
			}
		}
		if redraw {
			// The engine waits for none.
			continue
		}
		select {
		case w.presented <- driver.Frame{Shown: last}:
		case <-w.quit:
			return
		}
	}
}

// framesDebug is set by GUNIM_DEBUG_FRAMES=1, which logs to standard
// error, each second, how long each window took over its frames: to
// draw one, to wait for the monitor's vertical blank, and to hand it
// to the screen.
var framesDebug = os.Getenv("GUNIM_DEBUG_FRAMES") != ""

// framesFinish is set by GUNIM_DEBUG_FRAMES=gpu, which also waits for
// the GPU to finish each frame before the vertical blank, and logs how
// long it took. The wait slows frames down; it is only for measuring.
var framesFinish = os.Getenv("GUNIM_DEBUG_FRAMES") == "gpu"

// frameTimes sums a window's frame times for framesDebug.
type frameTimes struct {
	from                time.Time
	n, late             int
	draw, wait, present time.Duration
	drawMax, presentMax time.Duration
	waitMax             time.Duration
	gpu, gpuMax         time.Duration
	sent                render.Stats
}

// add counts a frame that took draw, wait and present, and logs the
// second's sums once one has passed. A frame is late where the three
// together took longer than a refresh at rate.
func (ft *frameTimes) add(w *Window, draw, wait, present, gpu time.Duration, sent *render.Stats, rate float64, fbW, fbH int) {
	ft.gpu += gpu
	ft.gpuMax = max(ft.gpuMax, gpu)
	ft.sent.Flushes += sent.Flushes
	ft.sent.Quads += sent.Quads
	ft.sent.Bytes += sent.Bytes
	ft.sent.Layers += sent.Layers
	ft.sent.FlushTime += sent.FlushTime
	*sent = render.Stats{}
	now := time.Now()
	if ft.from.IsZero() {
		ft.from = now
	}
	ft.n++
	ft.draw += draw
	ft.wait += wait
	ft.present += present
	ft.drawMax = max(ft.drawMax, draw)
	ft.waitMax = max(ft.waitMax, wait)
	ft.presentMax = max(ft.presentMax, present)
	if rate > 0 && draw+wait+present > time.Duration(float64(time.Second)/rate)*11/10 {
		ft.late++
	}
	if now.Sub(ft.from) < time.Second {
		return
	}
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	n := time.Duration(ft.n)
	log.Printf("gunim frames %p %dx%d at %.0f Hz: %d frames, %d late; draw %.1f ms (max %.1f), vblank wait %.1f (max %.1f), present %.1f (max %.1f)",
		w, fbW, fbH, rate, ft.n, ft.late, ms(ft.draw/n), ms(ft.drawMax), ms(ft.wait/n), ms(ft.waitMax), ms(ft.present/n), ms(ft.presentMax))
	log.Printf("gunim frames %p sent each frame: %d quads, %.1f MB in %d draws, %d layers; handing them over took %.1f ms; gpu %.1f ms (max %.1f)",
		w, ft.sent.Quads/ft.n, float64(ft.sent.Bytes)/float64(ft.n)/1e6, ft.sent.Flushes/ft.n, ft.sent.Layers/ft.n,
		ms(ft.sent.FlushTime/n), ms(ft.gpu/n), ms(ft.gpuMax))
	*ft = frameTimes{from: now}
}

// pace returns the time a frame reached the screen, given when the
// last one did. It serves where the swap is the only signal of the
// display's timing: Linux and macOS, and Windows when the vertical
// blank wait is unavailable.
//
// A swap interval of one should make SwapBuffers wait for the display.
// Some setups skip the wait: a virtual machine, a remote desktop, a
// driver with vsync forced off. A swap that returns well inside one
// refresh has skipped it, so pace sleeps out the rest of the refresh
// itself, which holds such a window to the display's rate.
//
// How long the swap took proves nothing either way. With software GL a
// swap spends milliseconds copying pixels with no vsync behind it.
func pace(last time.Time, rate float64) time.Time {
	now := time.Now()
	if last.IsZero() || rate <= 0 {
		return now
	}
	interval := time.Duration(float64(time.Second) / rate)
	if next := last.Add(interval); now.Sub(last) < interval*3/4 {
		time.Sleep(time.Until(next))
		return next
	}
	return now
}

// startGL makes the context current on this thread, turns on vsync and
// builds the renderer.
func (w *Window) startGL() (*render.Renderer, error) {
	holder := w.gw
	if w.ctx != nil {
		holder = w.ctx
		// Made here, on the thread that draws with it: see contextWindow.
		if err := w.ctx.CreateDeferredContext(); err != nil {
			return nil, fmt.Errorf("desktop: create context: %w", err)
		}
	}
	if err := holder.MakeContextCurrent(); err != nil {
		return nil, fmt.Errorf("desktop: make context current: %w", err)
	}
	// An error here leaves the swap unpaced, which pace makes up for.
	_ = holder.SwapInterval(swapInterval)
	ctx, err := gl.NewDefaultContext()
	if err != nil {
		return nil, fmt.Errorf("desktop: %w", err)
	}
	if err = ctx.LoadFunctions(); err != nil {
		return nil, fmt.Errorf("desktop: %w", err)
	}
	r, err := render.New(ctx, w.d.isES, &w.d.shared)
	if err != nil {
		return nil, err
	}
	r.SetText(w.textRendering, w.transparent)
	if w.ctx != nil {
		if w.pres, err = w.startPresenter(ctx); err != nil {
			r.Release()
			return nil, err
		}
		r.FlipWindow = true
	}
	return r, nil
}

// fail records an error for the next Present to return.
func (w *Window) fail(err error) {
	w.mu.Lock()
	if w.err == nil {
		w.err = err
	}
	w.mu.Unlock()
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// charOf is the character a key types on the layout in use, without
// Shift, or zero for a key that types none or more than one.
func charOf(k glfw.Key, scancode int) rune {
	name, err := glfw.GetKeyName(k, scancode)
	if err != nil {
		return 0
	}
	r := []rune(name)
	if len(r) != 1 {
		return 0
	}
	return r[0]
}

// SetCloaked implements [driver.Cloaker].
func (w *Window) SetCloaked(on bool) {
	w.debugf("cloaked %v", on)
	w.d.post(func() {
		if !w.closed {
			setCloaked(w, on)
		}
	})
}

// Hide implements [driver.Recycler].
func (w *Window) Hide() error {
	return w.d.call(func() error {
		if w.closed {
			return nil
		}
		return w.gw.Hide()
	})
}

// Show implements [driver.Recycler].
func (w *Window) Show() error {
	return w.d.call(func() error {
		if w.closed {
			return errStopped
		}
		if err := w.gw.Show(); err != nil {
			return err
		}
		w.measure()
		return nil
	})
}

// cornerRadius is the radius, in device pixels, a window with a drawn shadow cuts its corners to, as Windows 11
// rounds its own, and the edge it leaves for the shadow's border, or 0 and 0 while it is maximized or fills its
// monitor. The radius follows the monitor's scale alone, not the window's zoom, as the system's does, and the edge is
// as wide as the border is drawn now.
func (w *Window) cornerRadius() (radius, edge float32) {
	if w.Maximized() || w.FullScreen() {
		return 0, 0
	}
	w.mu.Lock()
	on, k, edge := w.shadow, w.content, w.edge
	w.mu.Unlock()
	if !on {
		return 0, 0
	}
	return 8 * k, edge
}

// Outline implements [driver.Outliner].
func (w *Window) Outline() (radius, edge float32, blends bool) {
	radius, edge = w.cornerRadius()
	w.mu.Lock()
	scale := w.scale
	w.mu.Unlock()
	if scale <= 0 {
		scale = 1
	}
	return radius / scale, edge / scale, w.d.dxgi || w.transparent
}

// uncover puts the window on the screen once, after its first frame is
// presented, if it was kept off it; see cloak.
func (w *Window) uncover() {
	w.uncovered.Do(func() {
		w.d.post(func() {
			if !w.closed {
				w.debugf("uncovered")
				cloak(w, false)
				w.covered = false
				if w.focusUncovered {
					_ = w.gw.Focus()
				}
				w.mu.Lock()
				o, s := w.shadowAt, w.shadowScale
				w.mu.Unlock()
				fadeShadow(w, o, s)
			}
		})
	})
}

// windowDebug is set by GUNIM_DEBUG_WINDOW=1, which logs to standard
// error how each window moves, sizes and scales, and when it is shown
// and uncovered, with the time since it opened: for finding where a
// window first shows up, and why.
var windowDebug = os.Getenv("GUNIM_DEBUG_WINDOW") == "1"

// debugf logs one line about the window, with GUNIM_DEBUG_WINDOW set.
// pointerDebug is set by GUNIM_DEBUG_POINTER=1, which logs to standard
// error each pointer event a window hears, and when it gains and loses
// the keyboard, to find where the pointer goes astray.
var pointerDebug = os.Getenv("GUNIM_DEBUG_POINTER") == "1"

// pointerf logs a pointer event of w, under GUNIM_DEBUG_POINTER=1.
func (w *Window) pointerf(format string, args ...any) {
	if !pointerDebug {
		return
	}
	kind := "window"
	if w.popup {
		kind = "popup"
	}
	fmt.Fprintf(os.Stderr, "gunim pointer %s %p %s: %s\n", kind, w, time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

func (w *Window) debugf(format string, args ...any) {
	if !windowDebug {
		return
	}
	fmt.Fprintf(os.Stderr, "gunim window %p %6.1f ms: %s\n", w, float64(time.Since(w.opened).Microseconds())/1000, fmt.Sprintf(format, args...))
}
