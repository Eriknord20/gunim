package driver

import "github.com/marrasen/gunim/geom"

// FlyoutGap is how far a flyout's card sits from the tray icon and from the edges of the work area, in logical pixels.
const FlyoutGap = 12

// FlyoutPlace returns where a flyout's card of size goes, its top-left corner in screen coordinates, beside icon, the
// tray icon's rectangle, on a monitor whose work area is work. The card sits gap from the icon on the side of the
// taskbar and keeps gap inside the work area: with the taskbar at the bottom, its bottom right sits above the icon,
// and moves left where it would leave the work area. A taskbar on another edge mirrors that. An empty icon puts the
// card in the work area's bottom right corner.
func FlyoutPlace(icon geom.Rect, size geom.Size, work geom.Rect, gap float32) geom.Point {
	var at geom.Point
	switch flyoutSide(icon, work) {
	case sideTop:
		at = geom.Pt(icon.Max.X-size.W, max(icon.Max.Y, work.Min.Y)+gap)
	case sideLeft:
		at = geom.Pt(max(icon.Max.X, work.Min.X)+gap, icon.Max.Y-size.H)
	case sideRight:
		at = geom.Pt(min(icon.Min.X, work.Max.X)-gap-size.W, icon.Max.Y-size.H)
	case sideBottom:
		at = geom.Pt(icon.Max.X-size.W, min(icon.Min.Y, work.Max.Y)-gap-size.H)
	default:
		at = geom.Pt(work.Max.X-gap-size.W, work.Max.Y-gap-size.H)
	}
	keep := func(v, lo, hi float32) float32 { return max(lo, min(v, hi)) }
	at.X = keep(at.X, work.Min.X+gap, work.Max.X-gap-size.W)
	at.Y = keep(at.Y, work.Min.Y+gap, work.Max.Y-gap-size.H)
	return at
}

// The edges a taskbar can be on.
const (
	sideNone = iota
	sideTop
	sideLeft
	sideRight
	sideBottom
)

// flyoutSide is the edge of work the taskbar holding icon is on: the side of work the icon lies past, or, for an
// icon inside work as on a taskbar that hides, the nearest edge.
func flyoutSide(icon, work geom.Rect) int {
	if icon.Empty() {
		return sideNone
	}
	switch {
	case icon.Min.Y >= work.Max.Y:
		return sideBottom
	case icon.Max.Y <= work.Min.Y:
		return sideTop
	case icon.Min.X >= work.Max.X:
		return sideRight
	case icon.Max.X <= work.Min.X:
		return sideLeft
	}
	c := icon.Center()
	side, best := sideBottom, work.Max.Y-c.Y
	for _, s := range []struct {
		side int
		d    float32
	}{{sideTop, c.Y - work.Min.Y}, {sideLeft, c.X - work.Min.X}, {sideRight, work.Max.X - c.X}} {
		if s.d < best {
			side, best = s.side, s.d
		}
	}
	return side
}

// A TrayLocator is a [Driver] that says where its tray icon is on the screen, for a flyout to open beside it.
type TrayLocator interface {
	// TrayRect returns the tray icon's rectangle in screen coordinates. It fails where no icon is up or the system
	// cannot say.
	TrayRect() (geom.Rect, error)
}
