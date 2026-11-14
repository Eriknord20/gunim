//go:build windows

package desktop

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/glfw"
)

// hiddenFlyout opens a flyout the test never shows, beside an icon at the bottom right of the primary work area.
func hiddenFlyout(t *testing.T) *Window {
	t.Helper()
	if display == nil {
		t.Skip("no display")
	}
	var work geom.Rect
	for _, m := range display.Monitors() {
		if m.Primary {
			work = m.WorkArea
		}
	}
	if work.Empty() {
		t.Skip("the primary monitor has no work area")
	}
	icon := geom.Rc(work.Max.X-200, work.Max.Y+4, 24, 32)
	dw, err := display.NewWindow(driver.Options{Title: "gunim flyout", Size: geom.Sz(300, 200), Kind: driver.KindFlyout,
		Anchor: icon, Margin: 10, Hidden: true})
	if err != nil {
		t.Fatal(err)
	}
	w, ok := dw.(*Window)
	if !ok {
		t.Fatalf("NewWindow returned a %T", dw)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

// onMain runs f on the main thread and waits for it.
func onMain(f func()) {
	done := make(chan struct{})
	display.post(func() { f(); close(done) })
	<-done
}

// askedToClose reports whether w asks to close within a short while.
func askedToClose(w *Window) bool {
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

func TestAPressOutsideClosesAFlyoutThatNeverHadTheKeyboard(t *testing.T) {
	w := hiddenFlyout(t)
	display.flyoutGone.Store(0)
	var rect [4]int
	var err error
	onMain(func() {
		// The watch startFlyout sets, without taking the foreground for a window the test keeps hidden.
		w.outside, err = glfw.WatchOutsidePress(w.gw, w.flyoutLeft)
		x, y, _ := w.gw.GetPos()
		ww, wh, _ := w.gw.GetSize()
		rect = [4]int{x, y, ww, wh}
	})
	if err != nil {
		t.Fatal(err)
	}
	press := func(x, y int) { onMain(func() { glfw.OutsidePressAt(int32(x), int32(y)) }) }

	press(rect[0]+rect[2]/2, rect[1]+rect[3]/2)
	if askedToClose(w) {
		t.Fatal("a press inside the flyout asked it to close")
	}
	press(rect[0]-50, rect[1]-50)
	if !askedToClose(w) {
		t.Fatal("a press outside a flyout that never had the keyboard did not ask it to close")
	}
	if !display.flyoutJustLeft() {
		t.Fatal("the driver did not note that the flyout left, for the click on the icon after it")
	}

	onMain(func() { stopFlyout(w) })
	press(rect[0]-50, rect[1]-50)
	if askedToClose(w) {
		t.Fatal("after its watch ended, a press outside still asked the flyout to close")
	}
}

func TestAClickOnTheIconThatClosedTheFlyoutOpensNothing(t *testing.T) {
	d := &Driver{}
	clicked := make(chan struct{}, 2)
	click := d.trayClick(func() { clicked <- struct{}{} })

	d.flyoutGone.Store(time.Now().UnixNano())
	click()
	select {
	case <-clicked:
		t.Fatal("a click on the icon just as a flyout closed ran OnClick, which would open it again")
	case <-time.After(100 * time.Millisecond):
	}

	d.flyoutGone.Store(time.Now().Add(-time.Second).UnixNano())
	click()
	select {
	case <-clicked:
	case <-time.After(time.Second):
		t.Fatal("a click on the icon long after a flyout closed did not run OnClick")
	}
}

func TestAFlyoutGoesBackBesideTheIconAfterAScaleChange(t *testing.T) {
	w := hiddenFlyout(t)
	var at, moved, back [2]int
	onMain(func() {
		at[0], at[1], _ = w.gw.GetPos()
		// Where Windows' suggested rectangle might put it on another monitor.
		_ = w.gw.SetPos(at[0]-300, at[1]-200)
		moved[0], moved[1], _ = w.gw.GetPos()
		w.flyoutRescaled()
		back[0], back[1], _ = w.gw.GetPos()
	})
	if moved == at {
		t.Fatal("setup: the flyout did not move")
	}
	if back != at {
		t.Fatalf("after the scale change the flyout is at %v, want back beside the icon at %v", back, at)
	}
}
