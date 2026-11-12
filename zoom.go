package gunim

import (
	"math"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// A WheelZoomer is a node that zooms something of its own with Ctrl and the wheel, such as a picture or a grid of
// tiles. While ZoomsWithWheel reports true, the window's zoom leaves Ctrl with the wheel over it alone, and the node,
// or one inside it, hears the scroll with its modifiers.
type WheelZoomer interface {
	Node
	ZoomsWithWheel() bool
}

// wheelZoomerAt reports whether the node at p, or one around it, takes Ctrl with the wheel.
func (u *UI) wheelZoomerAt(root *state, p geom.Point) bool {
	for s := u.hit(root, p); s != nil; s = s.parent {
		if z, ok := s.node.(WheelZoomer); ok && z.ZoomsWithWheel() {
			return true
		}
	}
	return false
}

// Zoomed is reported when the user zooms the window with [WindowOptions.ZoomKeys], so the application can remember
// the zoom for next time.
type Zoomed struct {
	Zoom float32
}

func init() { RegisterType[Zoomed]("gunim.zoomed") }

// MinZoom and MaxZoom bound the zoom a window takes.
const (
	MinZoom = 0.25
	MaxZoom = 5
)

// zoomSteps are the zooms the zoom keys step through, as browsers do.
var zoomSteps = []float32{0.5, 0.67, 0.75, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3}

// Zoom returns the factor the window's content is drawn larger by, 1 for none.
func (u *UI) Zoom() float32 { return u.zoom }

// SetZoom draws the window's content z times larger, within [MinZoom] and [MaxZoom], keeping the window's size on
// screen. The content is laid out again in the room left.
func (u *UI) SetZoom(z float32) {
	if math.IsNaN(float64(z)) || z <= 0 {
		z = 1
	}
	z = min(max(z, MinZoom), MaxZoom)
	if z == u.zoom {
		return
	}
	// The pointer stays where it is on the screen, and its point in the
	// window's logical pixels shrinks as the zoom grows. Taken so at
	// once, the frame that lays the zoom out finds what is under it.
	u.pointer = u.pointer.Mul(u.zoom / z)
	u.zoom = z
	setZoom(u.w.dw, z)
	u.Invalidate()
}

// setZoom zooms dw where its driver can, and reports whether it could.
func setZoom(dw driver.Window, z float32) bool {
	zm, ok := dw.(driver.Zoomer)
	if ok {
		zm.SetZoom(z)
	}
	return ok
}

// zoomBy steps the zoom up or down by steps of zoomSteps, and reports the new zoom to the application.
func (u *UI) zoomBy(steps int) {
	z := u.zoom
	for ; steps > 0; steps-- {
		z = nextStep(z, true)
	}
	for ; steps < 0; steps++ {
		z = nextStep(z, false)
	}
	u.zoomTo(z)
}

// zoomTo sets the zoom and reports it to the application when it changed.
func (u *UI) zoomTo(z float32) {
	was := u.zoom
	u.SetZoom(z)
	if u.zoom != was {
		u.report(Zoomed{Zoom: u.zoom})
	}
}

// nextStep returns the step of zoomSteps after z, or before it when up is false.
func nextStep(z float32, up bool) float32 {
	if up {
		for _, s := range zoomSteps {
			if s > z+0.001 {
				return s
			}
		}
		return zoomSteps[len(zoomSteps)-1]
	}
	for i := len(zoomSteps) - 1; i >= 0; i-- {
		if zoomSteps[i] < z-0.001 {
			return zoomSteps[i]
		}
	}
	return zoomSteps[0]
}

// zoomKey zooms for Ctrl with +, - or 0, or Ctrl with the wheel, and reports whether ev was one of them.
func (u *UI) zoomKey(ev any) bool {
	if !u.zoomKeys {
		return false
	}
	switch e := ev.(type) {
	case input.KeyPress:
		if !e.Mods.Has(input.ModControl) && !e.Mods.Has(input.ModSuper) || e.Mods.Has(input.ModAlt) {
			return false
		}
		switch {
		case e.Char == '+' || e.Char == '=' || e.Char == 0 && e.Key == input.KeyEqual || e.Key == input.KeyKPAdd:
			u.zoomBy(1)
		case e.Char == '-' || e.Char == 0 && e.Key == input.KeyMinus || e.Key == input.KeyKPSubtract:
			u.zoomBy(-1)
		// The 0 key resets whatever it types without Shift: on a French
		// layout it types à, and Ctrl with it resets the zoom all the
		// same. With Shift it types something else, as ) on a US
		// layout, and is left to it.
		case e.Char == '0' || e.Key == input.Key0 && !e.Mods.Has(input.ModShift) || e.Key == input.KeyKP0:
			u.zoomTo(1)
		default:
			return false
		}
		return true
	case input.Scroll:
		if !e.Mods.Has(input.ModControl) || e.Notches.Y == 0 {
			return false
		}
		// A touchpad's fractions of a notch add up to a step
		u.zoomNotches += e.Notches.Y
		if steps := int(u.zoomNotches); steps != 0 {
			u.zoomNotches -= float32(steps)
			u.zoomBy(steps)
		}
		return true
	}
	return false
}
