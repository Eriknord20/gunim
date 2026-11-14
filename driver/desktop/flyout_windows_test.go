//go:build windows

package desktop

import (
	"image/color"
	"math"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

var procGetWindowLongPtrW = user32.NewProc("GetWindowLongPtrW")

func TestTheTrayRectNeedsATrayIcon(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	if r, err := display.TrayRect(); err == nil {
		t.Fatalf("with no tray icon up, TrayRect gave %v and no error", r)
	}
}

func TestAFlyoutOpensBesideTheTrayAndTakesTheKeyboard(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	var primary driver.Monitor
	for _, m := range display.Monitors() {
		if m.Primary {
			primary = m
		}
	}
	work := primary.WorkArea
	if work.Empty() {
		t.Skip("the primary monitor has no work area")
	}
	per := primary.CoordsPerLogical
	if per <= 0 {
		per = 1
	}
	// An icon on a taskbar at the bottom, below the work area.
	icon := geom.Rc(work.Max.X-200, work.Max.Y+4, 24*per, 32*per)
	const margin = 10
	dw, err := display.NewWindow(driver.Options{Title: "gunim flyout", Size: geom.Sz(300, 200), Kind: driver.KindFlyout,
		Anchor: icon, Margin: margin, Hidden: true})
	if err != nil {
		t.Fatal(err)
	}
	w, ok := dw.(*Window)
	if !ok {
		t.Fatalf("NewWindow returned a %T", dw)
	}
	defer func() { _ = w.Close() }()

	type box struct {
		x, y, ww, wh int
		ex           uintptr
	}
	got := make(chan box, 1)
	display.post(func() {
		x, y, _ := w.gw.GetPos()
		ww, wh, _ := w.gw.GetSize()
		hwnd, _ := w.gw.GetWin32Window()
		const gwlExStyle = ^uintptr(19) // -20
		ex, _, _ := procGetWindowLongPtrW.Call(uintptr(hwnd), gwlExStyle)
		got <- box{x, y, ww, wh, ex}
	})
	b := <-got
	const wsExTopmost, wsExToolWindow, wsExNoActivate = 0x8, 0x80, 0x08000000
	if b.ex&wsExToolWindow == 0 || b.ex&wsExTopmost == 0 || b.ex&wsExNoActivate != 0 {
		t.Errorf("the flyout's extended style is %#x, want a topmost tool window that activates", b.ex)
	}
	pad := float64(margin * per)
	near := func(a, b float64) bool { return math.Abs(a-b) <= 1.5 }
	if right, bottom := float64(b.x+b.ww)-pad, float64(b.y+b.wh)-pad; !near(right, float64(icon.Max.X)) ||
		!near(bottom, float64(work.Max.Y-driver.FlyoutGap*per)) {
		t.Errorf("the card's bottom right is at %v,%v; want under the icon's right edge %v, %v above the work area's bottom %v",
			right, bottom, icon.Max.X, driver.FlyoutGap*per, work.Max.Y)
	}
	if !w.transparent {
		t.Error("the flyout is not transparent round its card")
	}
	w.SetBackground(color.NRGBA{R: 0xff, A: 0xff})
	w.mu.Lock()
	under := w.under
	w.mu.Unlock()
	if under != (color.NRGBA{}) {
		t.Errorf("the flyout draws %v under its card, want nothing", under)
	}

	focus := func(on bool) {
		done := make(chan struct{})
		display.post(func() { w.flyoutFocus(on); close(done) })
		<-done
	}
	asked := func() bool {
		deadline := time.After(300 * time.Millisecond)
		for {
			select {
			case ev := <-w.Input():
				if _, ok := ev.(driver.CloseAsked); ok {
					return true
				}
			case <-deadline:
				return false
			}
		}
	}
	focus(false)
	if asked() {
		t.Fatal("a flyout that never had the keyboard asked to close as it lost it")
	}
	focus(true)
	focus(false)
	if !asked() {
		t.Fatal("a flyout that lost the keyboard did not ask to close")
	}
}
