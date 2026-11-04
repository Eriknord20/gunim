package widget

import (
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// Clicker follows one click on a widget's targets, the rule every widget that acts on a click keeps: a press of the
// primary button, then its release over the target that took the press. The press that makes a double click starts
// none, so a link or a remove cross acts once however fast the clicks come; with Repeats, each press starts a click,
// as a button counts every click. A finger that goes on to scroll lets go at [input.Away], over no target, and so
// acts on nothing.
//
// A target is any number of nought or more the widget gives its parts, such as a row's index; a widget with one
// target gives 0 for a point inside it and -1 elsewhere. Call Press from the widget's [input.PointerDown] and Release
// from its [input.PointerUp]; the zero Clicker is ready.
type Clicker struct {
	// at is the target pressed, and held says its release is still to come.
	at   int
	held bool
	// Repeats has every press start a click, a double click's second press too.
	Repeats bool
}

// Press takes e as the start of a click on target at, or on nothing for a negative at. Only the first press of the
// primary button, on a target, starts one.
func (c *Clicker) Press(e input.PointerDown, at int) {
	c.at, c.held = at, e.Button == input.ButtonPrimary && (e.Clicks <= 1 || c.Repeats) && at >= 0
}

// Release takes e as the end of the click, and reports whether the click lands: the primary button let go over
// target at, the one pressed.
func (c *Clicker) Release(e input.PointerUp, at int) bool {
	if e.Button != input.ButtonPrimary {
		return false
	}
	held := c.held
	c.held = false
	return held && at >= 0 && at == c.at
}

// over is the target for a widget of size with one target: 0 for pos inside it, and -1 elsewhere.
func over(pos geom.Point, size geom.Size) int {
	if (geom.Rect{Max: size.Point()}).Contains(pos) {
		return 0
	}
	return -1
}

// Click follows one click on the parts of a control drawn by hand: a press of the primary button, then its release over
// the part pressed. Its parts are of any comparable type, such as a struct that names a track and a button.
//
// It leaves two things to the control that [Clicker] does itself. Every press starts a click, a double click's second
// too, so a control that acts once checks Clicks. And every value is a part, so a control whose press landed on nothing
// skips Press, or a release on the same nothing lands. The zero Click is ready.
type Click[T comparable] struct {
	at     T
	held   bool
	clicks int
}

// Press starts a click on part at.
func (c *Click[T]) Press(e input.PointerDown, at T) {
	c.at, c.held, c.clicks = at, e.Button == input.ButtonPrimary, e.Clicks
}

// Release ends the click and reports whether it lands: the primary button let go over at, the part pressed.
func (c *Click[T]) Release(e input.PointerUp, at T) bool {
	if e.Button != input.ButtonPrimary {
		return false
	}
	held := c.held
	c.held = false
	return held && at == c.at
}

// Held reports whether a click is under way.
func (c *Click[T]) Held() bool { return c.held }

// At is the part pressed.
func (c *Click[T]) At() T { return c.at }

// Clicks counts the press in a run of quick clicks: 2 for the second of a double click.
func (c *Click[T]) Clicks() int { return c.clicks }

// Cancel drops the click under way, so its release does nothing.
func (c *Click[T]) Cancel() { c.held = false }

// Hear drops the click under way on a press of any button, or as the keyboard or the window's focus goes; call it
// first in the control's Handle, before Press, and from its Overhear. The window's focus going reaches only the node
// with the focus and those round it, so a control elsewhere holds its click until the next press replaces it.
func (c *Click[T]) Hear(e input.Event) {
	switch e.(type) {
	case input.PointerDown, input.FocusLost, input.WindowFocusLost:
		c.held = false
	}
}
