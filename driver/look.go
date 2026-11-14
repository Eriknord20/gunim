package driver

import (
	"errors"
	"image/color"
)

// ErrNoSystemLook says the platform has no settings for how applications look to read.
var ErrNoSystemLook = errors.New("the system's look settings cannot be read here")

// SystemLook is what the user chose in the system's settings for how every application looks and moves.
type SystemLook struct {
	// Dark is true when the user chose dark colours for applications.
	Dark bool
	// LessMotion is true when the user turned animations off.
	LessMotion bool
	// TextScale is how much larger the user wants text: 1 as made, up to 2.25 on Windows.
	TextScale float32
	// Contrast holds the colours of the user's contrast theme, and is nil while none is on.
	Contrast *ContrastColors
}

// ContrastColors are the colours of a contrast theme, by what each is for.
type ContrastColors struct {
	// Window is the background, and WindowText the text and lines on it.
	Window, WindowText color.NRGBA
	// Highlight marks selection and focus, and HighlightText is the text on it.
	Highlight, HighlightText color.NRGBA
	// ButtonFace fills a button, and ButtonText is the text on it.
	ButtonFace, ButtonText color.NRGBA
	// GrayText is the text of what is disabled.
	GrayText color.NRGBA
	// HotLight is the colour of a link.
	HotLight color.NRGBA
}

// A LookReader is a [Driver] that reads the system's settings for how applications look and move.
type LookReader interface {
	// SystemLook reads the settings as they are now.
	SystemLook() (SystemLook, error)
	// WatchSystemLook calls fn with the settings each time the user changes one, on a goroutine of the driver's,
	// until stop is called. fn gets an error, and no look, when the settings could not be read again. stop returns once
	// a call to fn in progress has returned, and no call comes after, so call it from outside fn.
	WatchSystemLook(fn func(SystemLook, error)) (stop func(), err error)
}
