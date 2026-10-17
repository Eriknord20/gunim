package vst3

import (
	"fmt"
	"image"
	"unsafe"

	"github.com/marrasen/gunim/audio/plug"
)

// The plugin's editor: its view, on the plugins' thread, in a window of
// the system's that the package makes: one of its own, or a child of the
// host's window, which the host places.

type viewRect struct{ left, top, right, bottom int32 }

func (r viewRect) size() (w, h int) { return int(r.right - r.left), int(r.bottom - r.top) }

type editor struct {
	p         *Plugin
	view      uintptr
	frame     uintptr
	win       *window
	resizable bool
	// sized hears each size an embedded editor takes of itself, and w
	// and h are its size now.
	sized func(w, h int)
	w, h  int
	// placing says the host is sizing the view, which may answer by
	// asking for a size of its own: the host knows it already.
	placing bool
}

// The frame a view sits in, which it asks to be resized.

type plugFrame struct {
	object
	e *editor
}

var frameKind = &kind{
	iids: []uid{iidPlugFrame},
	make: func() []uintptr {
		return []uintptr{
			newCallback(func(this, view, rect uintptr) uintptr {
				f := self[plugFrame](this)
				if f == nil || f.e == nil || rect == 0 {
					return resultOf(resultInvalid)
				}
				r := *ptr[viewRect](rect)
				w, h := r.size()
				f.e.win.resize(w, h)
				f.e.w, f.e.h = w, h
				call(view, mOnSize, uintptr(unsafe.Pointer(&r)))
				if f.e.sized != nil && !f.e.placing {
					f.e.sized(w, h)
				}
				return resultOf(resultOK)
			}),
		}
	},
	other: func(this uintptr, id uid) uintptr {
		if id == iidRunLoop {
			return runLoop()
		}
		return 0
	},
}

// createView makes the controller's editor view, if this platform can
// show it.
func (p *Plugin) createView() uintptr {
	name := []byte("editor\x00")
	v := call(p.ctrl, mCreateView, uintptr(unsafe.Pointer(&name[0])))
	if v == 0 {
		return 0
	}
	pt := []byte(platformType + "\x00")
	if result(call(v, mIsPlatformTypeSupported, uintptr(unsafe.Pointer(&pt[0])))) != resultOK {
		release(v)
		return 0
	}
	return v
}

// EditorSize returns the size the plugin's editor opens at, in pixels,
// without opening it, or its size now where it is open; ok is false
// where it has no editor here.
func (p *Plugin) EditorSize() (w, h int, ok bool) {
	onUI(func() {
		if p.ed != nil {
			w, h, ok = p.ed.w, p.ed.h, true
			return
		}
		if p.ctrl == 0 {
			return
		}
		v := p.createView()
		if v == 0 {
			return
		}
		var r viewRect
		call(v, mGetSize, uintptr(unsafe.Pointer(&r)))
		release(v)
		w, h = r.size()
		ok = w > 0 && h > 0
	})
	return w, h, ok
}

// OpenEditor shows the plugin's editor, in a window titled title, or
// brings it to the front where it is open.
func (p *Plugin) OpenEditor(title string) error {
	var err error
	onUI(func() {
		if p.ed != nil {
			if !p.ed.win.child {
				p.ed.win.raise()
			}
			return
		}
		err = p.attachEditor(title, 0, nil)
	})
	return err
}

// EmbedEditor shows the plugin's editor in parent, a window of the
// system's: an HWND on Windows, and an X11 window's ID on Linux. It
// opens hidden, for the host to put it in place with PlaceEditor, and
// returns its size in pixels, which the host makes room for. sized
// hears each size the editor takes of itself after, in turn, on the
// plugins' thread: it notes the size and returns, waiting on nothing.
// An editor open in a window of its own closes first.
//
// The host closes the editor before it closes parent.
func (p *Plugin) EmbedEditor(parent uintptr, sized func(w, h int)) (w, h int, err error) {
	if parent == 0 {
		return 0, 0, fmt.Errorf("vst3: %s: no window to embed its editor in", p.Class.Name)
	}
	onUI(func() {
		if p.ed != nil {
			p.ed.close()
		}
		if err = p.attachEditor("", parent, sized); err == nil {
			w, h = p.ed.w, p.ed.h
		}
	})
	return w, h, err
}

// EditorEmbedded says whether the editor is open in a host's window, as
// EmbedEditor opened it.
func (p *Plugin) EditorEmbedded() bool {
	embedded := false
	onUI(func() { embedded = p.ed != nil && p.ed.win.child })
	return embedded
}

// PlaceEditor puts an embedded editor at r in its parent, in pixels, and
// shows it, or hides it where r is empty. An editor that can be resized
// takes r's size as near as it can; one that cannot keeps its own. It
// returns the size the editor has.
func (p *Plugin) PlaceEditor(r image.Rectangle) (w, h int) {
	onUI(func() {
		e := p.ed
		if e == nil || !e.win.child {
			return
		}
		if r.Empty() {
			e.win.hide()
		} else {
			if cw, ch := r.Dx(), r.Dy(); e.resizable && (cw != e.w || ch != e.h) {
				e.sizeTo(cw, ch)
			}
			e.win.place(r.Min.X, r.Min.Y)
		}
		w, h = e.w, e.h
	})
	return w, h
}

// sizeTo has a resizable view take w×h, as near as it allows, and its
// window the size it took.
func (e *editor) sizeTo(w, h int) {
	r := viewRect{right: int32(w), bottom: int32(h)}
	if result(call(e.view, mCheckSizeConstraint, uintptr(unsafe.Pointer(&r)))) == resultOK {
		w, h = r.size()
		r = viewRect{right: int32(w), bottom: int32(h)}
	}
	e.win.resize(w, h)
	e.w, e.h = w, h
	e.placing = true
	call(e.view, mOnSize, uintptr(unsafe.Pointer(&r)))
	e.placing = false
}

// attachEditor makes the view and its window: one of its own, titled
// title, or, where parent is set, a child of parent's.
func (p *Plugin) attachEditor(title string, parent uintptr, sized func(w, h int)) error {
	if p.ctrl == 0 {
		return fmt.Errorf("vst3: %s: %w", p.Class.Name, plug.ErrNoEditor)
	}
	v := p.createView()
	if v == 0 {
		return fmt.Errorf("vst3: %s: %w", p.Class.Name, plug.ErrNoEditor)
	}
	var r viewRect
	call(v, mGetSize, uintptr(unsafe.Pointer(&r)))
	w, h := r.size()
	if w <= 0 || h <= 0 {
		w, h = 640, 480
	}
	e := &editor{p: p, view: v, resizable: result(call(v, mCanResize)) == resultOK, sized: sized, w: w, h: h}
	win, err := newWindow(title, w, h, e, parent)
	if err != nil {
		release(v)
		return err
	}
	e.win = win
	f := &plugFrame{e: e}
	e.frame = keep(f, frameKind, false)
	call(v, mSetFrame, e.frame)
	pt := []byte(platformType + "\x00")
	if r := result(call(v, mAttached, win.handle(), uintptr(unsafe.Pointer(&pt[0])))); r != resultOK {
		call(v, mSetFrame, 0)
		release(v)
		drop(e.frame)
		win.destroy()
		return fmt.Errorf("vst3: %s: its editor would not attach (%d)", p.Class.Name, r)
	}
	if parent == 0 {
		win.show()
	}
	p.ed = e
	p.editorOpen.Store(true)
	return nil
}

// resized is the window's own size changed, by the user, to w×h.
func (e *editor) resized(w, h int) {
	if !e.resizable || e.win.child {
		return
	}
	r := viewRect{right: int32(w), bottom: int32(h)}
	if result(call(e.view, mCheckSizeConstraint, uintptr(unsafe.Pointer(&r)))) == resultOK {
		if cw, ch := r.size(); cw != w || ch != h {
			e.win.resize(cw, ch)
		}
	}
	e.w, e.h = r.size()
	call(e.view, mOnSize, uintptr(unsafe.Pointer(&r)))
}

// close takes the view down and the window with it. It runs on the
// plugins' thread.
func (e *editor) close() {
	if e.takeDown() {
		e.win.destroy()
	}
}

// lost takes the view down as its window goes with its parent, which
// the host closed with the editor still in it. It runs on the plugins'
// thread.
func (e *editor) lost() {
	if e.takeDown() {
		e.win.forget()
	}
}

// takeDown takes the view down, and reports whether it was up.
func (e *editor) takeDown() bool {
	if e.p.ed != e {
		return false
	}
	e.p.ed = nil
	e.p.editorOpen.Store(false)
	call(e.view, mRemoved)
	call(e.view, mSetFrame, 0)
	release(e.view)
	drop(e.frame)
	return true
}

// CloseEditor closes the plugin's editor, where it is open.
func (p *Plugin) CloseEditor() {
	onUI(func() {
		if p.ed != nil {
			p.ed.close()
		}
	})
}

// EditorOpen says whether the plugin's editor is open.
func (p *Plugin) EditorOpen() bool { return p.editorOpen.Load() }
