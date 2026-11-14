package glfw

import (
	"runtime"
	"testing"
	"unsafe"
)

// A window with a least size takes the rectangle Windows suggests as its DPI changes, though the rectangle is under
// the least size at the old DPI, and keeps its limits after.
func TestADPIChangeTakesTheSuggestedRectUnderTheLimits(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := Init(); err != nil {
		t.Skip(err)
	}
	defer func() { _ = Terminate() }()
	for _, h := range [][2]int{{int(ClientAPI), NoAPI}, {int(Visible), False}} {
		if err := WindowHint(Hint(h[0]), h[1]); err != nil {
			t.Fatal(err)
		}
	}
	w, err := CreateWindow(500, 375, "gunim dpi", nil, nil)
	if err != nil {
		t.Skip(err)
	}
	defer func() { _ = w.Destroy() }()
	if err := w.SetSizeLimits(500, 375, DontCare, DontCare); err != nil {
		t.Fatal(err)
	}
	area, err := _GetWindowRect(w.platform.handle)
	if err != nil {
		t.Fatal(err)
	}
	suggested := _RECT{left: area.left, top: area.top, right: area.left + 400, bottom: area.top + 300}
	dpi := uintptr(_USER_DEFAULT_SCREEN_DPI)
	_SendMessageW(w.platform.handle, _WM_DPICHANGED, _WPARAM(dpi<<16|dpi), _LPARAM(unsafe.Pointer(&suggested)))
	got, err := _GetWindowRect(w.platform.handle)
	if err != nil {
		t.Fatal(err)
	}
	if got.right-got.left != 400 || got.bottom-got.top != 300 {
		t.Errorf("told of a new DPI with a 400×300 rectangle, the window is %d×%d", got.right-got.left,
			got.bottom-got.top)
	}
	if w.minwidth != 500 || w.minheight != 375 {
		t.Errorf("after the DPI change the least size is %d×%d, want 500×375", w.minwidth, w.minheight)
	}
}
