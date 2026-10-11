package driver

// Colour management: gunim draws in sRGB, as the web does. Where a
// window is on a display whose colours are not sRGB's, such as a wide
// gamut monitor, the driver converts each finished frame from sRGB to
// the display's colours, from the display's ICC profile, so a red is
// the red it would be on an sRGB display, not a deeper one. Where the
// system converts windows itself, as macOS does, or Windows does under
// Auto Color Management or HDR, the driver leaves it to the system.
// It is on unless [Options.NoColorManagement] or GUNIM_COLOR=off turns
// it off.

// ColorState says whether a window's frames are converted to its
// display's colours, and why not where they are not.
type ColorState uint8

const (
	// ColorUnknown is the state of a window whose driver does no
	// colour management at all, such as Android's.
	ColorUnknown ColorState = iota
	// ColorConverted says the window converts its frames from sRGB to
	// the display's profile.
	ColorConverted
	// ColorNoProfile says the display has no profile, so its colours
	// are taken to be sRGB's.
	ColorNoProfile
	// ColorSRGB says the display's profile is sRGB, or so close to it
	// that converting would change nothing seen.
	ColorSRGB
	// ColorBySystem says the system converts the window's sRGB to the
	// display's colours itself: macOS always, Windows under Auto Color
	// Management or HDR.
	ColorBySystem
	// ColorUnsupported says the display's profile is one gunim cannot
	// read, such as one with lookup tables alone, or a broken one.
	ColorUnsupported
	// ColorOff says colour management is turned off, by
	// [Options.NoColorManagement] or GUNIM_COLOR=off.
	ColorOff
	// ColorUnmanaged says the platform gives gunim no way to know the
	// display's colours, such as a Wayland session, whose compositor
	// decides.
	ColorUnmanaged
)

func (s ColorState) String() string {
	switch s {
	case ColorConverted:
		return "converted to the display's profile"
	case ColorNoProfile:
		return "no display profile"
	case ColorSRGB:
		return "display profile is sRGB"
	case ColorBySystem:
		return "converted by the system"
	case ColorUnsupported:
		return "display profile not supported"
	case ColorOff:
		return "turned off"
	case ColorUnmanaged:
		return "not managed on this platform"
	}
	return "unknown"
}

// ColorInfo says how a window's colours reach its display.
type ColorInfo struct {
	// Profile is the description of the display's ICC profile, where
	// there is one and it has one.
	Profile string
	// State says whether the window converts its frames, and why not.
	State ColorState
	// Detail says more where there is more to say, such as why a
	// profile is not supported.
	Detail string
}

// Converting reports whether the window converts its frames to the
// display's profile.
func (i ColorInfo) Converting() bool { return i.State == ColorConverted }

func (i ColorInfo) String() string {
	s := i.State.String()
	if i.Profile != "" {
		s += " (" + i.Profile + ")"
	}
	if i.Detail != "" {
		s += ": " + i.Detail
	}
	return s
}

// A ColorManaged window can say how its colours reach its display.
type ColorManaged interface {
	// ColorProfile returns how the window's frames reach the display
	// it is on now. It is safe from any goroutine.
	ColorProfile() ColorInfo
}
