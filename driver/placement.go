package driver

import (
	"math"

	"github.com/marrasen/gunim/geom"
)

// Placement is where a window is on the screen, and whether it is
// maximized. An application keeps it between launches, and opens the
// window there again with [Options.Place].
//
// A minimized window is never reported as minimized: it reports the
// placement it goes back to, since opening a window minimized is not
// what anyone restoring a session wants.
type Placement struct {
	// Bounds is the window's client area: the part the application
	// draws, without the system's title bar and frame round it. It is in
	// screen coordinates, the space [Monitor.Bounds] is in: device pixels
	// on Windows and X11, and points on macOS. Multiply a length in
	// logical pixels by [Monitor.CoordsPerLogical] to get there.
	//
	// For a maximized window it is the size and place the window goes
	// back to when it is restored, not the ones it fills while
	// maximized, so opening it at a saved placement and restoring it
	// later gives the size the user chose.
	Bounds geom.Rect
	// Maximized says the window fills its monitor's work area.
	Maximized bool
	// CoordsPerLogical is screen coordinates per logical pixel on the
	// monitor the window was on, as [Monitor.CoordsPerLogical] says. A
	// window opened at the placement on a monitor of another scale, or
	// after the user changed the scale, keeps its size and its place on
	// the monitor in logical pixels. Zero, for a placement kept before
	// it was recorded, leaves the bounds as they are.
	CoordsPerLogical float32 `json:",omitempty"`
}

// A PlacementReader is a [Window] that can say where it is on the
// screen, for the application to save and open it there again next
// time. ok is false where the window cannot say, as when it has
// closed. It is safe to call from any goroutine but the main one.
type PlacementReader interface {
	Placement() (p Placement, ok bool)
}

// placementGrip is how much of a window's top, in logical pixels, must
// lie on a monitor's work area for the window to be put back where it
// was saved: as tall as a title bar, and as wide as a pointer needs to
// take hold of it and drag it.
const placementGrip = 40

const (
	// maxPlacementRatio and maxMonitorScale bound a placement's scale, past which
	// it isn't one a system reported.
	maxPlacementRatio = 8
	maxMonitorScale   = 4
)

// monitorHolding returns the index of the monitor of monitors holding most of the
// strip along the top of outer, by which a window is dragged, or -1 for none with
// enough of it to grip.
func monitorHolding(outer geom.Rect, monitors []Monitor) int {
	chosen, kept := -1, float32(0)
	for i, m := range monitors {
		grip := placementGrip * m.coordsPerLogical()
		strip := geom.Rect{Min: outer.Min, Max: geom.Pt(outer.Max.X, outer.Min.Y+min(grip, outer.Size().H))}
		on := intersect(strip, m.workArea()).Size()
		enough := on.W >= min(grip, strip.Size().W) && on.H >= min(grip/2, strip.Size().H)
		if area := on.W * on.H; enough && area > kept {
			chosen, kept = i, area
		}
	}
	return chosen
}

// FitPlacement makes a saved placement safe to open a window at on the
// monitors attached now, which may not be the ones attached when it was
// saved.
//
// frame is the system's title bar and frame round the client area, in
// screen coordinates, or zero for a window that draws its own title
// bar. The window's outer edge, frame and all, is what must fit.
//
// The placement is kept where its top strip, [placementGrip] logical
// pixels tall, overlaps some monitor's work area by that much across
// and by half of it down, so the user can still grab the title bar.
// Otherwise the window is centred on the primary monitor's work area.
// Either way, a window larger than its monitor's work area shrinks to
// fit, and moves onto it along the side that shrank. Maximized is kept
// as it was.
//
// A placement that says its scale, saved on a monitor of another scale
// than the one it lands on, first grows or shrinks by the ratio of the
// two, keeping its size and its place from the monitor's corner in
// logical pixels. The placement that comes back says the monitor's
// scale.
//
// ok is false where there is nothing to fit: the bounds are empty, or
// there are no monitors to fit them to. p comes back as it was then.
func FitPlacement(p Placement, monitors []Monitor, frame geom.Insets) (fit Placement, ok bool) {
	b := p.Bounds
	if b.Empty() || len(monitors) == 0 {
		return p, false
	}
	if c := p.CoordsPerLogical; c < 1.0/maxPlacementRatio || c > maxPlacementRatio*maxMonitorScale {
		// Nothing a system reports: an old file's, or a damaged one's.
		p.CoordsPerLogical = 0
	}
	outer := geom.Rect{
		Min: geom.Pt(b.Min.X-frame.Left, b.Min.Y-frame.Top),
		Max: geom.Pt(b.Max.X+frame.Right, b.Max.Y+frame.Bottom),
	}

	chosen := monitorHolding(outer, monitors)
	rescaled := false
	// Where the scale changes the whole screen's coordinates, as on X11 under
	// Wayland, the place it was saved at may mean another monitor now, or none,
	// so the monitor is the one holding it once it's scaled about the origin.
	if p.CoordsPerLogical > 0 && (chosen < 0 || monitors[chosen].coordsPerLogical() != p.CoordsPerLogical) {
		for i, m := range monitors {
			r := m.coordsPerLogical() / p.CoordsPerLogical
			if r == 1 || i == chosen {
				continue
			}
			s := geom.Rect{Min: geom.Pt(b.Min.X*r, b.Min.Y*r), Max: geom.Pt(b.Max.X*r, b.Max.Y*r)}
			so := geom.Rect{
				Min: geom.Pt(s.Min.X-frame.Left, s.Min.Y-frame.Top),
				Max: geom.Pt(s.Max.X+frame.Right, s.Max.Y+frame.Bottom),
			}
			if monitorHolding(so, monitors[i:i+1]) == 0 {
				chosen, rescaled, b, outer = i, true, s, so
				break
			}
		}
	}
	centre := chosen < 0
	if centre {
		chosen = primary(monitors)
	}
	m := monitors[chosen]
	work := m.workArea()
	if r := m.coordsPerLogical() / p.CoordsPerLogical; !rescaled && p.CoordsPerLogical > 0 && r != 1 {
		rescaled = true
		// Measured from the monitor's corner, so it keeps its place on
		// a monitor that is its own size in logical pixels.
		o := m.Bounds.Min
		b = geom.Rect{
			Min: geom.Pt(o.X+(b.Min.X-o.X)*r, o.Y+(b.Min.Y-o.Y)*r),
			Max: geom.Pt(o.X+(b.Max.X-o.X)*r, o.Y+(b.Max.Y-o.Y)*r),
		}
		outer = geom.Rect{
			Min: geom.Pt(b.Min.X-frame.Left, b.Min.Y-frame.Top),
			Max: geom.Pt(b.Max.X+frame.Right, b.Max.Y+frame.Bottom),
		}
	}

	size := outer.Size()
	w, h := min(size.W, work.Size().W), min(size.H, work.Size().H)
	x, y := outer.Min.X, outer.Min.Y
	switch {
	case centre:
		x = work.Min.X + float32(math.Floor(float64(work.Size().W-w)/2))
		y = work.Min.Y + float32(math.Floor(float64(work.Size().H-h)/2))
	default:
		// A side that shrank is as long as the work area, so it lines up with it
		if w < size.W {
			x = work.Min.X
		}
		if h < size.H {
			y = work.Min.Y
		}
		if rescaled {
			// Grown, it may reach past the work area's far edges, which
			// it was clear of at the scale it was saved at.
			x = max(min(x, work.Max.X-w), work.Min.X)
			y = max(min(y, work.Max.Y-h), work.Min.Y)
		}
	}
	fit = Placement{Maximized: p.Maximized, CoordsPerLogical: p.CoordsPerLogical}
	if p.CoordsPerLogical > 0 {
		fit.CoordsPerLogical = m.coordsPerLogical()
	}
	fit.Bounds = geom.Rect{
		Min: geom.Pt(x+frame.Left, y+frame.Top),
		Max: geom.Pt(x+w-frame.Right, y+h-frame.Bottom),
	}
	if fit.Bounds.Empty() {
		// A work area smaller than the frame leaves no room for the client area, so it keeps its saved size rather than
		// open inside out
		fit.Bounds = geom.Rect{Min: fit.Bounds.Min, Max: fit.Bounds.Min.Add(b.Size().Point())}
	}
	return fit, true
}

// workArea is the part of the monitor windows may use, or all of it
// where the platform did not say.
func (m Monitor) workArea() geom.Rect {
	if m.WorkArea.Empty() {
		return m.Bounds
	}
	return m.WorkArea
}

// coordsPerLogical is [Monitor.CoordsPerLogical], or 1 where it is not
// set.
func (m Monitor) coordsPerLogical() float32 {
	if m.CoordsPerLogical <= 0 {
		return 1
	}
	return m.CoordsPerLogical
}

// primary is the index of the primary monitor, or of the first one
// where none says it is.
func primary(ms []Monitor) int {
	for i, m := range ms {
		if m.Primary {
			return i
		}
	}
	return 0
}

// intersect is the part r and s share, empty where they share none.
func intersect(r, s geom.Rect) geom.Rect {
	o := geom.Rect{
		Min: geom.Pt(max(r.Min.X, s.Min.X), max(r.Min.Y, s.Min.Y)),
		Max: geom.Pt(min(r.Max.X, s.Max.X), min(r.Max.Y, s.Max.Y)),
	}
	if o.Empty() {
		return geom.Rect{}
	}
	return o
}
