//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/marrasen/gunim/internal/glfw"
)

// On Windows a display's profile is the one Colour Management has as
// its default, which GetICMProfileW gives for a device context on the
// monitor. Where Windows manages colour itself, under Auto Color
// Management or with HDR on, the desktop compositor converts the
// window's sRGB to the display, and gunim converts nothing:
// DisplayConfigGetDeviceInfo says which, by the monitor's advanced
// colour state.

var (
	procGetICMProfileW              = gdi32.NewProc("GetICMProfileW")
	procGetDisplayConfigBufferSizes = user32.NewProc("GetDisplayConfigBufferSizes")
	procQueryDisplayConfig          = user32.NewProc("QueryDisplayConfig")
	procDisplayConfigGetDeviceInfo  = user32.NewProc("DisplayConfigGetDeviceInfo")
)

// watchColors has nothing to watch for every window: each window hears
// the messages that may mean a change; see setupColor.
func watchColors(*Driver) {}

// setupColor has the window read its profile again on the messages
// that may mean its display's colours changed.
func setupColor(w *Window) {
	w.gw.SetColorChangeCallback(func(*glfw.Window) { w.updateColor(true) })
}

// displayKey names the display the window is on: its monitor's handle.
func displayKey(w *Window) any {
	hwnd, err := w.gw.GetWin32Window()
	if err != nil {
		return uintptr(0)
	}
	const monitorDefaultToNearest = 2
	m, _, _ := procMonitorFromWindow.Call(uintptr(hwnd), monitorDefaultToNearest)
	return m
}

// displayProfile reads the profile of the monitor the window is on, or
// says Windows manages its colours.
func displayProfile(w *Window) displayColor {
	m, _ := displayKey(w).(uintptr)
	if m == 0 {
		return displayColor{err: errors.New("desktop: no monitor for the window")}
	}
	mi := monitorInfoEx{cbSize: uint32(unsafe.Sizeof(monitorInfoEx{}))}
	if ok, _, err := procGetMonitorInfoW.Call(m, uintptr(unsafe.Pointer(&mi))); ok == 0 {
		return displayColor{err: fmt.Errorf("desktop: GetMonitorInfoW: %w", err)}
	}
	device := windows.UTF16ToString(mi.device[:])
	if how := advancedColor(device); how != "" {
		return displayColor{bySystem: true, detail: how}
	}
	display, _ := windows.UTF16PtrFromString("DISPLAY")
	dc, _, err := procCreateDCW.Call(uintptr(unsafe.Pointer(display)), uintptr(unsafe.Pointer(&mi.device[0])), 0, 0)
	if dc == 0 {
		return displayColor{err: fmt.Errorf("desktop: CreateDCW %s: %w", device, err)}
	}
	defer func() { _, _, _ = procDeleteDC.Call(dc) }()
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if ok, _, _ := procGetICMProfileW.Call(dc, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&buf[0]))); ok == 0 {
		// No profile is associated with the monitor.
		return displayColor{}
	}
	path := windows.UTF16ToString(buf)
	b, rerr := os.ReadFile(path)
	if rerr != nil {
		return displayColor{err: fmt.Errorf("desktop: read display profile: %w", rerr)}
	}
	return displayColor{profile: b}
}

// The display configuration's structures and values.
type (
	dcLUID struct {
		low  uint32
		high int32
	}
	dcPath struct {
		sourceAdapter dcLUID
		sourceID      uint32
		sourceMode    uint32
		sourceStatus  uint32
		targetAdapter dcLUID
		targetID      uint32
		targetMode    uint32
		targetTech    uint32
		rotation      uint32
		scaling       uint32
		refresh       [2]uint32
		scanLine      uint32
		available     int32
		targetStatus  uint32
		flags         uint32
	}
	dcMode   [64]byte
	dcHeader struct {
		kind, size uint32
		adapter    dcLUID
		id         uint32
	}
	dcSourceName struct {
		header  dcHeader
		gdiName [32]uint16
	}
	dcAdvancedColor struct {
		header                 dcHeader
		value, encoding, depth uint32
	}
	dcAdvancedColor2 struct {
		header                       dcHeader
		value, encoding, depth, mode uint32
	}
)

const (
	qdcOnlyActivePaths      = 2
	dcGetSourceName         = 1
	dcGetAdvancedColorInfo  = 9
	dcGetAdvancedColorInfo2 = 15
	dcAdvancedColorEnabled  = 1 << 1
	dcAdvancedColorModeWCG  = 1
	dcAdvancedColorModeHDR  = 2
)

// advancedColor says how Windows manages the colours of the display
// whose GDI device name is device, such as `\\.\DISPLAY1`: "Auto Color
// Management", "HDR", or "" where it does not and leaves them to the
// application.
func advancedColor(device string) string {
	var np, nm uint32
	if r, _, _ := procGetDisplayConfigBufferSizes.Call(qdcOnlyActivePaths, uintptr(unsafe.Pointer(&np)), uintptr(unsafe.Pointer(&nm))); r != 0 || np == 0 {
		return ""
	}
	paths := make([]dcPath, np)
	modes := make([]dcMode, max(nm, 1))
	if r, _, _ := procQueryDisplayConfig.Call(qdcOnlyActivePaths, uintptr(unsafe.Pointer(&np)), uintptr(unsafe.Pointer(&paths[0])),
		uintptr(unsafe.Pointer(&nm)), uintptr(unsafe.Pointer(&modes[0])), 0); r != 0 {
		return ""
	}
	for _, p := range paths[:np] {
		name := dcSourceName{header: dcHeader{kind: dcGetSourceName, size: uint32(unsafe.Sizeof(dcSourceName{})), adapter: p.sourceAdapter, id: p.sourceID}}
		if r, _, _ := procDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(&name))); r != 0 || windows.UTF16ToString(name.gdiName[:]) != device {
			continue
		}
		// Windows 11 24H2 and later say which mode the display is in.
		info2 := dcAdvancedColor2{header: dcHeader{kind: dcGetAdvancedColorInfo2, size: uint32(unsafe.Sizeof(dcAdvancedColor2{})), adapter: p.targetAdapter, id: p.targetID}}
		if r, _, _ := procDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(&info2))); r == 0 {
			switch info2.mode {
			case dcAdvancedColorModeWCG:
				return "Windows manages the display's colour (Auto Color Management)"
			case dcAdvancedColorModeHDR:
				return "Windows manages the display's colour (HDR)"
			}
			return ""
		}
		// Earlier, advanced colour on means HDR, or Auto Color
		// Management where Windows 11 has it.
		info := dcAdvancedColor{header: dcHeader{kind: dcGetAdvancedColorInfo, size: uint32(unsafe.Sizeof(dcAdvancedColor{})), adapter: p.targetAdapter, id: p.targetID}}
		if r, _, _ := procDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(&info))); r == 0 && info.value&dcAdvancedColorEnabled != 0 {
			return "Windows manages the display's colour (advanced colour)"
		}
		return ""
	}
	return ""
}
