//go:build windows

package desktop

import (
	"testing"
	"unsafe"
)

// TestDisplayConfigLayouts checks the display configuration's
// structures are the size Windows' headers make them.
func TestDisplayConfigLayouts(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want uintptr
	}{
		{"DISPLAYCONFIG_PATH_INFO", unsafe.Sizeof(dcPath{}), 72},
		{"DISPLAYCONFIG_MODE_INFO", unsafe.Sizeof(dcMode{}), 64},
		{"DISPLAYCONFIG_DEVICE_INFO_HEADER", unsafe.Sizeof(dcHeader{}), 20},
		{"DISPLAYCONFIG_SOURCE_DEVICE_NAME", unsafe.Sizeof(dcSourceName{}), 84},
		{"DISPLAYCONFIG_GET_ADVANCED_COLOR_INFO", unsafe.Sizeof(dcAdvancedColor{}), 32},
		{"DISPLAYCONFIG_GET_ADVANCED_COLOR_INFO_2", unsafe.Sizeof(dcAdvancedColor2{}), 36},
	} {
		if c.got != c.want {
			t.Errorf("%s is %d bytes, want %d", c.name, c.got, c.want)
		}
	}
}

// TestTheDisplaysColoursCanBeRead reads the colours of the primary
// display's device as a window would, which must not fail however the
// machine is set up.
func TestTheDisplaysColoursCanBeRead(t *testing.T) {
	t.Logf("advanced colour on DISPLAY1: %q", advancedColor(`\\.\DISPLAY1`))
}
