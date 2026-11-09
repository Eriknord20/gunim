package vst3

import (
	"errors"
	"math"
	"sync"

	"github.com/ebitengine/purego/objc"

	"github.com/marrasen/gunim/internal/cocoa"
)

// On macOS an editor is an NSView the plugin adds its own to: a subview
// of the host's content view, or the content of a window of its own. It
// lives on the main thread, as AppKit's views do, and sizes in points,
// which the host's pixels are scaled to.

// platformType is the kind of window an editor attaches to here.
const platformType = "NSView"

// runLoop is for Linux, where a plugin's editor runs on the host's loop.
func runLoop() uintptr { return 0 }

var (
	selAlloc                = objc.RegisterName("alloc")
	selInitWithFrame        = objc.RegisterName("initWithFrame:")
	selInitWindow           = objc.RegisterName("initWithContentRect:styleMask:backing:defer:")
	selRelease              = objc.RegisterName("release")
	selAddSubview           = objc.RegisterName("addSubview:")
	selRemoveFromSuperview  = objc.RegisterName("removeFromSuperview")
	selSetHidden            = objc.RegisterName("setHidden:")
	selSetFrameOrigin       = objc.RegisterName("setFrameOrigin:")
	selSetFrameSize         = objc.RegisterName("setFrameSize:")
	selBounds               = objc.RegisterName("bounds")
	selWindow               = objc.RegisterName("window")
	selBackingScaleFactor   = objc.RegisterName("backingScaleFactor")
	selMainScreen           = objc.RegisterName("mainScreen")
	selSetTitle             = objc.RegisterName("setTitle:")
	selSetContentView       = objc.RegisterName("setContentView:")
	selSetContentSize       = objc.RegisterName("setContentSize:")
	selSetReleasedWhenClose = objc.RegisterName("setReleasedWhenClosed:")
	selSetDelegate          = objc.RegisterName("setDelegate:")
	selCenter               = objc.RegisterName("center")
	selMakeKeyAndOrderFront = objc.RegisterName("makeKeyAndOrderFront:")
	selClose                = objc.RegisterName("close")
	selNew                  = objc.RegisterName("new")
)

const (
	styleTitled         = 1 << 0
	styleClosable       = 1 << 1
	styleMiniaturizable = 1 << 2
	styleResizable      = 1 << 3
	backingBuffered     = 2
)

// windowsByDelegate are the editors' own windows, by their delegates.
var windowsByDelegate = map[objc.ID]*window{}

// delegateClass is the class of an own window's delegate, which hears
// the window closed or sized by the user.
var delegateClass = sync.OnceValues(func() (objc.Class, error) {
	return objc.RegisterClass("GunimVST3EditorDelegate", objc.GetClass("NSObject"),
		[]*objc.Protocol{objc.GetProtocol("NSWindowDelegate")}, nil,
		[]objc.MethodDef{
			{
				Cmd: objc.RegisterName("windowShouldClose:"),
				Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) bool {
					if w := windowsByDelegate[self]; w != nil {
						// Closing the editor closes the window.
						w.e.close()
					}
					return false
				},
			},
			{
				Cmd: objc.RegisterName("windowDidResize:"),
				Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) {
					w := windowsByDelegate[self]
					if w == nil || w.sizing {
						return
					}
					b := objc.Send[cocoa.NSRect](w.view, selBounds)
					cw, ch := int(math.Round(b.Size.Width)), int(math.Round(b.Size.Height))
					if cw != w.w || ch != w.h {
						w.w, w.h = cw, ch
						w.e.resized(cw, ch)
					}
				},
			},
		})
})

// window is an editor's view: in a window of its own, or, with child
// set, a subview of the host's. Its w and h are in points.
type window struct {
	view   objc.ID
	own    objc.ID // the window of its own, or 0
	del    objc.ID // its delegate
	parent objc.ID
	e      *editor
	w, h   int
	sizing bool
	child  bool
	// placed says the host placed a child, at x, y in its pixels.
	placed bool
	x, y   int
}

// newWindow makes a view of w×h points for e: in a window of its own,
// titled title, or, where parent is set, a hidden subview of parent, an
// NSView. It runs on the main thread alone.
func newWindow(title string, w, h int, e *editor, parent uintptr) (*window, error) {
	if !onMainThread() {
		return nil, errors.New("vst3: an editor opens on macOS's main thread alone, which no window runs here")
	}
	frame := cocoa.NSRect{Size: cocoa.NSSize{Width: float64(w), Height: float64(h)}}
	view := objc.ID(objc.GetClass("NSView")).Send(selAlloc).Send(selInitWithFrame, frame)
	if view == 0 {
		return nil, errors.New("vst3: AppKit made no view")
	}
	win := &window{view: view, e: e, w: w, h: h}
	if parent != 0 {
		win.child, win.parent = true, objc.ID(parent)
		view.Send(selSetHidden, true)
		win.parent.Send(selAddSubview, view)
		return win, nil
	}
	class, err := delegateClass()
	if err != nil {
		view.Send(selRelease)
		return nil, err
	}
	style := uint(styleTitled | styleClosable | styleMiniaturizable)
	if e.resizable {
		style |= styleResizable
	}
	own := objc.ID(objc.GetClass("NSWindow")).Send(selAlloc).Send(selInitWindow, frame, style, uint(backingBuffered), false)
	if own == 0 {
		view.Send(selRelease)
		return nil, errors.New("vst3: AppKit made no window")
	}
	own.Send(selSetReleasedWhenClose, false)
	t := cocoa.NSString_alloc().InitWithUTF8String(title)
	own.Send(selSetTitle, t.ID)
	t.Send(selRelease)
	own.Send(selSetContentView, view)
	win.own, win.del = own, objc.ID(class).Send(selNew)
	own.Send(selSetDelegate, win.del)
	own.Send(selCenter)
	windowsByDelegate[win.del] = win
	return win, nil
}

func (w *window) handle() uintptr { return uintptr(w.view) }

func (w *window) show() {
	if w.own != 0 {
		w.own.Send(selMakeKeyAndOrderFront, objc.ID(0))
		return
	}
	w.view.Send(selSetHidden, false)
}

func (w *window) raise() { w.show() }

func (w *window) resize(cw, ch int) {
	w.w, w.h = cw, ch
	size := cocoa.NSSize{Width: float64(cw), Height: float64(ch)}
	if w.own != 0 {
		w.sizing = true
		w.own.Send(selSetContentSize, size)
		w.sizing = false
		return
	}
	w.view.Send(selSetFrameSize, size)
	if w.placed {
		// Its top left stays where the host put it.
		w.place(w.x, w.y)
	}
}

// place puts a child's top left at px, py in the host's pixels, from its
// parent's top left, and shows it.
func (w *window) place(px, py int) {
	w.placed, w.x, w.y = true, px, py
	s := w.scale()
	b := objc.Send[cocoa.NSRect](w.parent, selBounds)
	// The parent counts up from its bottom.
	origin := cocoa.NSPoint{X: float64(px) / s, Y: b.Size.Height - float64(py)/s - float64(w.h)}
	w.view.Send(selSetFrameOrigin, origin)
	w.view.Send(selSetHidden, false)
}

// hide hides a child.
func (w *window) hide() { w.view.Send(selSetHidden, true) }

func (w *window) destroy() {
	if w.own != 0 {
		delete(windowsByDelegate, w.del)
		w.own.Send(selSetDelegate, objc.ID(0))
		w.own.Send(selClose)
		w.own.Send(selRelease)
		w.del.Send(selRelease)
	} else {
		w.view.Send(selRemoveFromSuperview)
	}
	w.view.Send(selRelease)
}

// forget lets go of a view that went with its window.
func (w *window) forget() {}

// scale is how many of the host's pixels make a point: the backing scale
// of the window the view shows in.
func (w *window) scale() float64 {
	if w != nil {
		if win := w.view.Send(selWindow); win != 0 {
			if s := objc.Send[float64](win, selBackingScaleFactor); s > 0 {
				return s
			}
		}
		if w.parent != 0 {
			if win := w.parent.Send(selWindow); win != 0 {
				if s := objc.Send[float64](win, selBackingScaleFactor); s > 0 {
					return s
				}
			}
		}
	}
	if onMainThread() {
		if screen := objc.ID(objc.GetClass("NSScreen")).Send(selMainScreen); screen != 0 {
			if s := objc.Send[float64](screen, selBackingScaleFactor); s > 0 {
				return s
			}
		}
	}
	return 1
}
