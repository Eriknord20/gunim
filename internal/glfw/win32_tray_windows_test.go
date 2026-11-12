package glfw

import (
	"testing"

	"golang.org/x/sys/windows"
)

// The tests here set the package's tray, trayNow, and the foreground hook, so none of them may run in parallel.

// stubTray puts a tray with t up on the window hwnd for a test, with the foreground hook recording what it is asked,
// and takes both away as the test ends.
func stubTray(t *testing.T, hwnd windows.HWND, icon TrayIcon) *[]windows.HWND {
	t.Helper()
	var asked []windows.HWND
	was := takeForeground
	takeForeground = func(h windows.HWND) { asked = append(asked, h) }
	trayNow = &trayState{hwnd: hwnd, t: icon}
	t.Cleanup(func() {
		trayNow = nil
		takeForeground = was
	})
	return &asked
}

// A click on the tray's message reaches OnNotifyClick, once, and not the icon's own click, with the program in the
// foreground before it runs.
func TestAClickOnTheTraysMessageCallsOnNotifyClick(t *testing.T) {
	var notified, clicked int
	const hwnd = windows.HWND(0x1234)
	var foregroundFirst bool
	var asked *[]windows.HWND
	asked = stubTray(t, hwnd, TrayIcon{
		OnNotifyClick: func() { notified++; foregroundFirst = len(*asked) == 1 },
		OnClick:       func() { clicked++ },
	})
	if r := trayProc(hwnd, trayMessage, 0, ninBalloonUserClick); r != 0 {
		t.Fatalf("the tray answered the click with %d, want 0", r)
	}
	if notified != 1 || clicked != 0 {
		t.Fatalf("a click on the message called OnNotifyClick %d times and OnClick %d; want once and never",
			notified, clicked)
	}
	if !foregroundFirst || (*asked)[0] != hwnd {
		t.Fatalf("the tray took the foreground through %v before OnNotifyClick ran: %v; want its own window first",
			*asked, foregroundFirst)
	}
	// With no OnNotifyClick, the click does nothing.
	trayNow.t.OnNotifyClick = nil
	trayProc(hwnd, trayMessage, 0, ninBalloonUserClick)
	if clicked != 0 {
		t.Fatalf("with no OnNotifyClick, a click on the message called OnClick %d times", clicked)
	}
}

// A message that leaves the screen, closed or timed out, reaches OnNotifyGone.
func TestAMessageLeavingTheScreenCallsOnNotifyGone(t *testing.T) {
	gone := 0
	const hwnd = windows.HWND(0x1234)
	stubTray(t, hwnd, TrayIcon{OnNotifyGone: func() { gone++ }})
	trayProc(hwnd, trayMessage, 0, ninBalloonHide)
	trayProc(hwnd, trayMessage, 0, ninBalloonTimeout)
	if gone != 2 {
		t.Fatalf("a message closed and one timed out called OnNotifyGone %d times, want twice", gone)
	}
}
