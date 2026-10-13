package desktop

import (
	"testing"

	"golang.org/x/sys/windows"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

var (
	procGetWindow     = windows.NewLazySystemDLL("user32.dll").NewProc("GetWindow")
	procGetWindowLong = windows.NewLazySystemDLL("user32.dll").NewProc("GetWindowLongW")
)

// ownerAndTopmost reports w's owner window and whether it stays above every window.
func ownerAndTopmost(t *testing.T, dw driver.Window) (windows.HWND, bool) {
	t.Helper()
	w, ok := dw.(*Window)
	if !ok {
		t.Fatalf("the window is a %T", dw)
	}
	var owner windows.HWND
	var ex uintptr
	if err := display.call(func() error {
		h, err := w.gw.GetWin32Window()
		if err != nil {
			return err
		}
		o, _, _ := procGetWindow.Call(uintptr(h), 4) // GW_OWNER
		owner = windows.HWND(o)
		ex, _, _ = procGetWindowLong.Call(uintptr(h), ^uintptr(19)) // GWL_EXSTYLE, -20
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return owner, ex&0x8 != 0 // WS_EX_TOPMOST
}

func TestAnOwnedPopupStaysWithItsWindow(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	p, err := display.NewWindow(driver.Options{Title: "gunim test", Size: geom.Sz(200, 120)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	parent, ok := p.(*Window)
	if !ok {
		t.Fatalf("the window is a %T", p)
	}
	var ph windows.HWND
	if err := display.call(func() (err error) { ph, err = parent.gw.GetWin32Window(); return err }); err != nil {
		t.Fatal(err)
	}
	for _, owned := range []bool{false, true} {
		dw, err := display.NewWindow(driver.Options{Kind: driver.KindPopup, Parent: parent, Size: geom.Sz(50, 50),
			Owned: owned})
		if err != nil {
			t.Fatal(err)
		}
		owner, top := ownerAndTopmost(t, dw)
		_ = dw.Close()
		switch {
		case owned && (owner != ph || top):
			t.Errorf("an owned popup has owner %#x, want %#x, and is topmost %v", owner, ph, top)
		case !owned && (owner != 0 || !top):
			t.Errorf("a popup has owner %#x, want none, and is topmost %v", owner, top)
		}
	}
}

// An ordinary window that is Owned belongs to its parent: it stays in front of it and is not topmost; one that is
// not has no owner.
func TestAnOwnedWindowBelongsToItsParent(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	p, err := display.NewWindow(driver.Options{Title: "gunim test", Size: geom.Sz(200, 120)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	parent, ok := p.(*Window)
	if !ok {
		t.Fatalf("the window is a %T", p)
	}
	var ph windows.HWND
	if err := display.call(func() (err error) { ph, err = parent.gw.GetWin32Window(); return err }); err != nil {
		t.Fatal(err)
	}
	for _, owned := range []bool{false, true} {
		dw, err := display.NewWindow(driver.Options{Title: "gunim owned", Parent: parent, Size: geom.Sz(160, 100), Owned: owned})
		if err != nil {
			t.Fatal(err)
		}
		owner, top := ownerAndTopmost(t, dw)
		_ = dw.Close()
		switch {
		case owned && (owner != ph || top):
			t.Errorf("an owned window has owner %#x, want %#x, and is topmost %v", owner, ph, top)
		case !owned && owner != 0:
			t.Errorf("a window has owner %#x, want none", owner)
		}
	}
}
