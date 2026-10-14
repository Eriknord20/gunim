package gunim

import (
	"errors"
	"fmt"
	"image"
	"math"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

// A NativeHost is a node a window of the system's covers: a child window the application puts in the gunim window,
// as a plugin's own editor, which gunim draws nothing over. After each frame that moves it, the node hears where it
// stands, in the window's device pixels, from the top left corner of the window's client area, for it to move the
// child window there. It hears an empty rectangle when it is not drawn, and as it leaves the tree, for it to hide
// the child window.
type NativeHost interface {
	Node
	PlaceNative(r image.Rectangle, u *UI)
}

// A NativeCloser is a [NativeHost] that closes its child window as the window it stands in closes, before the
// system's window goes: a child window the system destroys with its parent may take its program down with it, as a
// plugin's editor on X11 does.
type NativeCloser interface {
	NativeHost
	CloseNative(u *UI)
}

// NativeWindow returns the system's window behind the window: an HWND on Windows, and an X11 window's ID on Linux,
// for a child window of the system's to sit in it.
func (u *UI) NativeWindow() (uintptr, error) {
	n, ok := u.w.dw.(driver.NativeWindow)
	if !ok {
		return 0, fmt.Errorf("gunim: native window: %w", errors.ErrUnsupported)
	}
	return n.NativeHandle()
}

// Reshape moves each of the window's edges out by by, in logical pixels, or in where a side is negative, as a panel
// opening at the window's side grows it: Left grows it leftwards, so what the window showed stays where it was on the
// screen. Where that takes the window off its monitor's work area, it moves whole to keep on it. A maximized or
// full-screen window keeps its size.
func (u *UI) Reshape(by geom.Insets) error {
	r, ok := u.w.dw.(driver.Reshaper)
	if !ok {
		return fmt.Errorf("gunim: reshape window: %w", errors.ErrUnsupported)
	}
	if err := r.Reshape(by); err != nil {
		return fmt.Errorf("gunim: reshape window: %w", err)
	}
	u.invalid = true
	return nil
}

// noteNativeNode notes s while its node is a [NativeHost], so a frame tells it where it stands. [UI.forget] lets it
// go.
func (u *UI) noteNativeNode(s *state) {
	h, ok := s.node.(NativeHost)
	if !ok {
		return
	}
	if u.nativeNodes == nil {
		u.nativeNodes = map[*state]nativeNode{}
	}
	// It has been told nothing yet: an impossible place, so the first frame tells it.
	u.nativeNodes[s] = nativeNode{host: h, at: image.Rect(math.MinInt32, math.MinInt32, math.MinInt32, math.MinInt32)}
}

// nativeNode is a [NativeHost] in the tree, and where it was last told it stands.
type nativeNode struct {
	host NativeHost
	at   image.Rectangle
}

// forgetNativeNode tells s's node, as it leaves the tree, that it stands nowhere.
func (u *UI) forgetNativeNode(s *state) {
	n, ok := u.nativeNodes[s]
	if !ok {
		return
	}
	delete(u.nativeNodes, s)
	if !n.at.Empty() {
		n.host.PlaceNative(image.Rectangle{}, u)
	}
}

// placeNatives tells each [NativeHost] in the tree where it stands, where that moved since the frame before.
func (u *UI) placeNatives(scale float32) {
	for s, n := range u.nativeNodes {
		var r image.Rectangle
		if s.drawn == u.seq && !u.goingAway && u.surfaceOf(s) == nil {
			b := u.rectIn(s, u.root)
			r = image.Rect(round(b.Min.X*scale), round(b.Min.Y*scale), round(b.Max.X*scale), round(b.Max.Y*scale))
		}
		if r.Empty() {
			r = image.Rectangle{}
		}
		if r == n.at {
			continue
		}
		u.nativeNodes[s] = nativeNode{host: n.host, at: r}
		n.host.PlaceNative(r, u)
	}
}

// round is v to the nearest whole number.
func round(v float32) int { return int(math.Round(float64(v))) }

// closeNatives has each [NativeCloser] in the tree close its child window, as the window closes.
func (u *UI) closeNatives() {
	for s, n := range u.nativeNodes {
		delete(u.nativeNodes, s)
		if c, ok := n.host.(NativeCloser); ok {
			c.CloseNative(u)
		}
	}
}
