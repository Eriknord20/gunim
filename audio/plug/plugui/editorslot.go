package plugui

import (
	"image"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// EditorSlot shows a plugin's own editor, a window of the system's, in
// the gunim window the slot stands in, over the room the slot is given:
// the window's title bar and what shows beside the editor are gunim's.
// The editor opens as the slot is first drawn, and hides while the slot
// is not.
//
// The slot closes the editor as its window closes, before the window
// goes: a plugin whose windows go under it may end the program, as
// Xlib does. A host that closes the editor first closes it with Close.
type EditorSlot struct {
	ed plug.Embedder
	// Fit grows and shrinks the window by as much as the editor's own
	// size, as it opens and as it changes, differs from the room the slot
	// has: for a slot that takes all the room the window gives it beyond
	// a fixed part of its own.
	Fit bool
	// Opened hears the editor open, and Failed an editor that would
	// not.
	Opened func(u *gunim.UI)
	Failed func(err error, u *gunim.UI)

	open, closed bool
	// tries counts the editor's failures to open, and at where the slot
	// last stood, to try again there.
	tries int
	at    image.Rectangle
	// want is the size the editor last took of itself, in device pixels,
	// and refit says the window is still to fit it.
	want  geom.Size
	refit bool
	// sized holds a size the editor took of itself, as w<<32|h, until
	// the slot's goroutine reads it.
	sized atomic.Uint64
	// scale is device pixels per logical pixel, and box the slot's size,
	// as last laid out.
	scale float32
	box   geom.Size
}

// NewEditorSlot returns a slot for ed's editor.
func NewEditorSlot(ed plug.Embedder) *EditorSlot { return &EditorSlot{ed: ed, scale: 1} }

// sizedPoll is how often an open slot looks for a size the editor took
// of itself; embedTries is how many times the slot tries to open the
// editor, embedWait apart.
const (
	sizedPoll  = 100 * time.Millisecond
	embedTries = 10
	embedWait  = 200 * time.Millisecond
)

// PlaceNative implements [gunim.NativeHost]: it opens the editor where
// it is not open yet, and puts it at r.
func (s *EditorSlot) PlaceNative(r image.Rectangle, u *gunim.UI) {
	if s.closed {
		return
	}
	if r.Empty() {
		if s.open {
			s.ed.PlaceEditor(image.Rectangle{})
		}
		return
	}
	s.at = r
	if !s.open && !s.embed(u) {
		return
	}
	w, h := s.ed.PlaceEditor(r)
	if s.want.W == 0 {
		s.want = geom.Sz(float32(w), float32(h))
	}
	s.fitWindow(r, u)
}

// embed opens the editor in u's window, and reports whether it did.
func (s *EditorSlot) embed(u *gunim.UI) bool {
	parent, err := u.NativeWindow()
	if err == nil {
		var w, h int
		w, h, err = s.ed.EmbedEditor(parent, func(w, h int) { s.sized.Store(uint64(w)<<32 | uint64(uint32(h))) })
		s.want = geom.Sz(float32(w), float32(h))
	}
	if err != nil {
		// A plugin still loading may have no editor yet: the slot tries
		// again, a while, before it gives up.
		s.tries++
		if s.tries < embedTries {
			u.After(embedWait, func(u *gunim.UI) {
				if !s.open && !s.closed && !s.at.Empty() {
					s.PlaceNative(s.at, u)
				}
			})
			return false
		}
		s.closed = true
		if s.Failed != nil {
			s.Failed(err, u)
		}
		return false
	}
	s.open, s.refit = true, s.Fit
	if s.Opened != nil {
		s.Opened(u)
	}
	var poll func(u *gunim.UI)
	poll = func(u *gunim.UI) {
		if s.closed || u.Presence(s) == gunim.Exiting {
			return
		}
		if v := s.sized.Swap(0); v != 0 {
			s.want = geom.Sz(float32(v>>32), float32(uint32(v)))
			s.refit = s.Fit
			u.Invalidate()
		}
		u.After(sizedPoll, poll)
	}
	u.After(sizedPoll, poll)
	return true
}

// fitWindow grows or shrinks the window, once for each size the editor
// takes of itself, by as much as it differs from r, the room it has.
func (s *EditorSlot) fitWindow(r image.Rectangle, u *gunim.UI) {
	if !s.refit {
		return
	}
	s.refit = false
	dw, dh := s.want.W-float32(r.Dx()), s.want.H-float32(r.Dy())
	if dw == 0 && dh == 0 {
		return
	}
	_ = u.Reshape(geom.Insets{Right: dw / s.scale, Bottom: dh / s.scale})
}

// Size is the room the editor takes, in logical pixels at scale, as it
// last said: zero before it opens.
func (s *EditorSlot) Size(scale float32) geom.Size {
	if scale <= 0 {
		scale = 1
	}
	return geom.Sz(s.want.W/scale, s.want.H/scale)
}

// Open reports whether the editor is open in the slot.
func (s *EditorSlot) Open() bool { return s.open && !s.closed }

// CloseNative implements [gunim.NativeCloser]: the editor closes with
// the window.
func (s *EditorSlot) CloseNative(*gunim.UI) { s.Close() }

// Close closes the editor, for good.
func (s *EditorSlot) Close() {
	if s.closed {
		return
	}
	s.closed = true
	if s.open {
		s.ed.CloseEditor()
	}
}

// Layout implements [gunim.Node]: the slot fills its room.
func (s *EditorSlot) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	if f.Scale > 0 {
		s.scale = f.Scale
	}
	s.box = c.Max
	return c.Max
}

// Paint implements [gunim.Node]: the room the editor leaves, as one
// that cannot be resized does in a larger window, shows the window's
// background.
func (s *EditorSlot) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(gunim.WindowBackground.Get(f.Theme)))
}
