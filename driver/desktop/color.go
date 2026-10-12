//go:build linux || windows || darwin

package desktop

import (
	"errors"
	"fmt"
	"os"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/internal/render"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/glfw"
	"github.com/marrasen/gunim/internal/icc"
)

// Colour management: each window reads the ICC profile of the display
// it is on, and where that is not sRGB, its render thread converts each
// frame from sRGB to it as the frame goes to the window; see
// driver/internal/render/color.go. The profile is read again as the
// window moves to another monitor and as the platform says the
// profiles changed. The platform's part is in color_linux.go,
// color_windows.go and color_darwin.go.

// colorOff is set by GUNIM_COLOR=off, which leaves every window's
// colours unconverted, for debugging.
var colorOff = os.Getenv("GUNIM_COLOR") == "off"

// displayColor is what a platform says of a display's colours.
type displayColor struct {
	// profile is the display's ICC profile, or nil for none.
	profile []byte
	// bySystem says the system converts the window's sRGB itself, and
	// name names the display's colours there; detail says how, such as
	// HDR.
	bySystem     bool
	name, detail string
	// unmanaged, where set, says why the platform cannot say.
	unmanaged string
	// err is a failure to read the profile.
	err error
}

// colorEntry is a profile read, kept by its bytes so windows on the
// same display share its table.
type colorEntry struct {
	info driver.ColorInfo
	lut  *icc.LUT
}

// maxColorEntries is the most profiles a driver keeps.
const maxColorEntries = 8

// colorFor turns what the platform says into the window's colour state
// and its table, nil for none. It runs on the main thread.
func (d *Driver) colorFor(dc displayColor) (driver.ColorInfo, *icc.LUT) {
	switch {
	case dc.unmanaged != "":
		return driver.ColorInfo{State: driver.ColorUnmanaged, Detail: dc.unmanaged}, nil
	case dc.bySystem:
		return driver.ColorInfo{State: driver.ColorBySystem, Profile: dc.name, Detail: dc.detail}, nil
	case dc.err != nil:
		return driver.ColorInfo{State: driver.ColorUnsupported, Detail: dc.err.Error()}, nil
	case len(dc.profile) == 0:
		return driver.ColorInfo{State: driver.ColorNoProfile}, nil
	}
	key := string(dc.profile)
	if e, ok := d.colors[key]; ok {
		return e.info, e.lut
	}
	var e colorEntry
	p, err := icc.Parse(dc.profile)
	if p != nil {
		e.info.Profile = p.Description
	}
	switch {
	case errors.Is(err, icc.ErrUnsupported):
		e.info.State, e.info.Detail = driver.ColorUnsupported, err.Error()
	case err != nil:
		e.info.State, e.info.Detail = driver.ColorUnsupported, fmt.Sprintf("unreadable profile: %v", err)
	default:
		lut := p.LUT(icc.LUTSize)
		if lut.Identity(1.0 / 255) {
			e.info.State = driver.ColorSRGB
		} else {
			e.info.State, e.lut = driver.ColorConverted, lut
		}
	}
	if d.colors == nil || len(d.colors) >= maxColorEntries {
		d.colors = map[string]colorEntry{}
	}
	d.colors[key] = e
	return e.info, e.lut
}

// ColorProfile implements [driver.ColorManaged].
func (w *Window) ColorProfile() driver.ColorInfo {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.colorInfo
}

var _ driver.ColorManaged = (*Window)(nil)

// updateColor reads the colours of the window's display, where it is on
// another display than when last read, or whatever display it is on
// with again, and hands the render thread a changed table with a frame
// to draw it. It runs on the main thread.
func (w *Window) updateColor(again bool) {
	if w.closed {
		return
	}
	if w.noColor {
		w.setColor(driver.ColorInfo{State: driver.ColorOff}, nil)
		return
	}
	key := displayKey(w)
	if !again && w.colorRead && key == w.colorKey {
		return
	}
	w.colorKey, w.colorRead = key, true
	info, lut := w.d.colorFor(displayProfile(w))
	w.setColor(info, lut)
}

// setColor sets the window's colour state and table, and asks for a
// frame where either changed. It runs on the main thread.
func (w *Window) setColor(info driver.ColorInfo, lut *icc.LUT) {
	w.mu.Lock()
	if w.colorFail != nil && lut != nil {
		// The render thread could not build the converting program.
		info.State, info.Detail, lut = driver.ColorUnsupported, w.colorFail.Error(), nil
	}
	changed := info != w.colorInfo || lut != w.colorLUT
	w.colorInfo, w.colorLUT = info, lut
	w.mu.Unlock()
	if !changed {
		return
	}
	w.debugf("colour: %v", info)
	w.in.Push(driver.Redraw{})
}

// colorsChanged reads every window's display colours again, as the
// platform says they may have changed. It runs on the main thread.
func (d *Driver) colorsChanged() {
	clear(d.colors)
	for _, w := range d.windows {
		w.updateColor(true)
	}
}

// startColor follows the displays' colours for every window. It runs on
// the main thread, once GLFW is up.
func (d *Driver) startColor() {
	// Queued, so the monitors are all known by the time it runs.
	_, _ = glfw.SetMonitorCallback(func(*glfw.Monitor, glfw.PeripheralEvent) { d.post(d.colorsChanged) })
	watchColors(d)
}

// windowMonitor returns the monitor holding the window's centre, or
// the primary one. It runs on the main thread.
func (w *Window) windowMonitor() *glfw.Monitor {
	x, y, err := w.gw.GetPos()
	if err != nil {
		return nil
	}
	ww, wh, err := w.gw.GetSize()
	if err != nil {
		return nil
	}
	centre := geom.Pt(float32(x+ww/2), float32(y+wh/2))
	ms, err := glfw.GetMonitors()
	if err != nil {
		return nil
	}
	for _, m := range ms {
		if info, ok := monitorInfo(m); ok && info.Bounds.Contains(centre) {
			return m
		}
	}
	m, _ := glfw.GetPrimaryMonitor()
	return m
}

// colorConvertFailed notes that the render thread cannot convert the
// window's colours, which it then leaves as they are. It runs on the
// render thread.
func (w *Window) colorConvertFailed(err error) {
	err = fmt.Errorf("the GPU cannot convert colours: %w", err)
	w.mu.Lock()
	w.colorFail = err
	w.colorInfo.State, w.colorInfo.Detail = driver.ColorUnsupported, err.Error()
	w.colorLUT = nil
	w.mu.Unlock()
}

// convertPixels converts pixels read back from the canvas, in sRGB, as
// the window shows them: premultiplied RGBA, converted straight.
func convertPixels(r *render.Renderer, pix []byte) {
	for i := 0; i+3 < len(pix); i += 4 {
		a := pix[i+3]
		if a == 0 {
			continue
		}
		k := float32(a) / 255
		c := r.ToDisplay([3]float32{float32(pix[i]) / 255 / k, float32(pix[i+1]) / 255 / k, float32(pix[i+2]) / 255 / k})
		for j := range 3 {
			pix[i+j] = byte(min(max(c[j]*k*255+0.5, 0), 255))
		}
	}
}
