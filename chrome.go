package gunim

import (
	"errors"
	"fmt"
	"image/color"
	"slices"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// titleBar is what the engine knows of a chromeless window's title bar:
// the driver that moves and sizes the window, and the caption and
// maximize button found as the frame painted, in window space.
type titleBar struct {
	fr        driver.Framer
	caption   []geom.Rect
	maximize  geom.Rect
	sent      []geom.Rect
	sentMax   geom.Rect
	maximized bool
}

// resizeMargin is how near the window's edge, in logical pixels, a
// press sizes a chromeless window, where the system does not.
const resizeMargin = 6

// ResizeEdge is how near a chromeless window's edge, in logical pixels,
// a press may size it, on any system: Windows takes its frame's
// thickness, 8 pixels at 100%, and elsewhere gunim takes resizeMargin.
// A child window of the system's, as a plugin's own editor, takes every
// press over it, so one kept this far in from the edges leaves the
// window its edges to size it by.
const ResizeEdge = 8

// startChrome looks at whether the window's driver has taken the title
// bar away, and gives the window bar, or the registered title bar when
// bar is nil.
func (u *UI) startChrome(bar TitleBar) {
	if fr, ok := u.w.dw.(driver.Framer); ok && fr.Chromeless() {
		u.chrome = &titleBar{fr: fr, maximized: fr.Maximized()}
		if bar == nil && newTitleBar != nil {
			bar = newTitleBar()
		}
		if bar != nil && u.titleBar == nil {
			bar.SetTitle(u.w.title)
			u.giveTitleBar(bar)
		}
	}
}

// Chromeless reports whether the window's title bar is the
// application's to draw.
func (u *UI) Chromeless() bool { return u.chrome != nil }

// Maximized reports whether a chromeless window is maximized, for its
// maximize button to show restore.
func (u *UI) Maximized() bool { return u.chrome != nil && u.chrome.maximized }

// Minimize minimizes the window, for a title bar's button.
func (u *UI) Minimize() {
	if fr, ok := u.w.dw.(driver.Framer); ok {
		_ = fr.Minimize()
	}
}

// ToggleMaximize maximizes the window, or restores it, for a title
// bar's button.
func (u *UI) ToggleMaximize() {
	if u.chrome != nil && !u.fixed {
		_ = u.chrome.fr.SetMaximized(!u.chrome.maximized)
	}
}

// SetPinned keeps the window above other windows, or lets it go back among them.
func (u *UI) SetPinned(on bool) error {
	p, ok := u.w.dw.(driver.Pinner)
	if !ok {
		return fmt.Errorf("gunim: pin window: %w", errors.ErrUnsupported)
	}
	if err := p.SetPinned(on); err != nil {
		return fmt.Errorf("gunim: pin window: %w", err)
	}
	u.pinned = on
	u.Invalidate()
	return nil
}

// Pinned reports whether the window is kept above other windows.
func (u *UI) Pinned() bool { return u.pinned }

// AskToClose does what the system's close button does: asks the
// application, with the window's AskToClose, or closes the window,
// animated when it arrived so.
func (u *UI) AskToClose() {
	if !u.closeAsked() {
		u.w.Close()
	}
}

// closeAsked answers the user asking to close the window: the
// application is asked, with the window's AskToClose, or a window that
// arrived animated leaves the same way. It reports false for a window
// to close at once.
func (u *UI) closeAsked() bool {
	switch {
	case u.w.askToClose != nil:
		u.report(u.w.askToClose)
	case u.animated:
		u.startLeaving()
	default:
		return false
	}
	return true
}

// noteTitleBar notes where a node that is part of the title bar is, as
// it paints, under the transform t.
func (u *UI) noteTitleBar(n Node, t paint.Transform, size geom.Size) {
	if c, ok := n.(Caption); ok {
		for _, r := range c.CaptionRects(size) {
			u.chrome.caption = append(u.chrome.caption, windowRect(t, r))
		}
	}
	// An empty rectangle is a title bar without a maximize button, and a
	// fixed window has none whatever its title bar draws.
	if m, ok := n.(MaximizeButton); ok && !u.fixed {
		if r := m.MaximizeRect(size); !r.Empty() {
			u.chrome.maximize = windowRect(t, r)
		}
	}
}

// beginTitleBar forgets the title bar found last frame, before this
// frame paints.
func (u *UI) beginTitleBar() {
	if u.chrome != nil {
		u.chrome.caption, u.chrome.maximize = u.chrome.caption[:0], geom.Rect{}
	}
}

// sendTitleBar tells the driver where the title bar is, when it has
// changed.
func (u *UI) sendTitleBar() {
	c := u.chrome
	if c == nil || (slices.Equal(c.caption, c.sent) && c.maximize == c.sentMax) {
		return
	}
	c.sent, c.sentMax = append(c.sent[:0], c.caption...), c.maximize
	c.fr.SetTitleBar(c.sent, c.sentMax)
}

// framePress acts on a press where the system would have, on a window
// whose system leaves moving and sizing to the engine: an edge sizes
// it, and the caption moves it, or with a double click maximizes it
// when the title bar has a maximize button. It reports whether it took
// the press.
func (u *UI) framePress(p geom.Point, e input.PointerDown) bool {
	c := u.chrome
	if c == nil || c.fr.NativeFrame() || e.Button != input.ButtonPrimary {
		return false
	}
	if edge, ok := u.edgeAt(p); ok {
		_ = c.fr.StartResize(edge)
		return true
	}
	for _, r := range c.caption {
		if !r.Contains(p) {
			continue
		}
		if e.Clicks == 2 {
			if !c.maximize.Empty() {
				u.ToggleMaximize()
			}
		} else {
			_ = c.fr.StartMove()
		}
		return true
	}
	return false
}

// edgeAt is the edge or corner of the window p is near, where the
// engine sizes a chromeless window itself. A fixed window has none.
func (u *UI) edgeAt(p geom.Point) (driver.Edge, bool) {
	c := u.chrome
	if c == nil || c.fr.NativeFrame() || c.maximized || u.fixed {
		return 0, false
	}
	size := u.w.dw.Size()
	left, right := p.X < resizeMargin, p.X >= size.W-resizeMargin
	top, bottom := p.Y < resizeMargin, p.Y >= size.H-resizeMargin
	switch {
	case top && left:
		return driver.EdgeTopLeft, true
	case top && right:
		return driver.EdgeTopRight, true
	case bottom && left:
		return driver.EdgeBottomLeft, true
	case bottom && right:
		return driver.EdgeBottomRight, true
	case left:
		return driver.EdgeLeft, true
	case right:
		return driver.EdgeRight, true
	case top:
		return driver.EdgeTop, true
	case bottom:
		return driver.EdgeBottom, true
	}
	return 0, false
}

// edgeCursor is the pointer's shape over an edge or corner.
func edgeCursor(e driver.Edge) input.Cursor {
	switch e {
	case driver.EdgeTopLeft, driver.EdgeBottomRight:
		return input.CursorResizeNWSE
	case driver.EdgeTopRight, driver.EdgeBottomLeft:
		return input.CursorResizeNESW
	case driver.EdgeLeft, driver.EdgeRight:
		return input.CursorResizeH
	case driver.EdgeTop, driver.EdgeBottom:
	}
	return input.CursorResizeV
}

// Maximized reports whether the window is maximized, for a title bar's
// maximize button to draw restore as it paints.
func (f Frame) Maximized() bool { return f.u != nil && f.u.Maximized() }

// Pinned reports whether the window is kept above other windows, for a pin button to show it.
func (f Frame) Pinned() bool { return f.u != nil && f.u.Pinned() }

// Chromeless reports whether the window's title bar is the
// application's to draw, for a part of one to take no room otherwise.
func (f Frame) Chromeless() bool { return f.u != nil && f.u.Chromeless() }

// UnderTitleBar reports whether the application draws under the title bar the engine gave the window, for the bar to
// leave its fill out and let the application show through; see [WindowOptions.UnderTitleBar].
func (f Frame) UnderTitleBar() bool { return f.u != nil && f.u.underBar }

// MakeChromeless makes a window from [NewOffscreen] chromeless, with a
// pretend system that moves and sizes it itself when native, as
// Windows does, and leaves that to the engine otherwise. It is for a
// test, which reads what the pretend system was asked from the frame.
func (w *Window) MakeChromeless(native bool) *driver.OffscreenFrame {
	o := w.Offscreen()
	if o == nil {
		return nil
	}
	fr := o.MakeChromeless(native)
	w.ui.startChrome(nil)
	return fr
}

// WindowBackground is the theme's background: the colour under a
// window's tree, where the tree paints nothing. Application code reads
// and sets it as widget.Background, the same token, which is where the
// other colours of a theme are; the engine declares it here, for a
// window with no widgets.
var WindowBackground = theme.Color("background", color.NRGBA{R: 0x16, G: 0x18, B: 0x1e, A: 0xff})
