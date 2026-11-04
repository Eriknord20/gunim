// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

//go:build freebsd || linux || netbsd

package glfw

import (
	"errors"
	"os"
	"runtime"
	"testing"
	"time"
)

// startX11 readies GLFW for a test on a display, or skips the test where there is none, and returns a second
// connection to the server, to play another program. It leaves the test on one OS thread, as GLFW needs.
func startX11(t *testing.T) (other uintptr) {
	t.Helper()
	if os.Getenv("DISPLAY") == "" {
		t.Skip("no X display")
	}
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	if err := Init(); err != nil {
		t.Skipf("no X display to use: %v", err)
	}
	other = xOpenDisplay(0)
	if other == 0 {
		t.Fatal("can't open a second connection")
	}
	t.Cleanup(func() { xCloseDisplay(other) })
	return other
}

// own has a new window of the connection own the selection, and returns the window.
func own(t *testing.T, display uintptr, selection _Atom) _XID {
	t.Helper()
	w := xCreateWindow(display, _glfw.platformWindow.root, 0, 0, 1, 1, 0, 0, _InputOnly, 0, 0, nil)
	xSetSelectionOwner(display, selection, w, _CurrentTime)
	if xSync(display, false); xGetSelectionOwner(display, selection) != w {
		t.Fatal("the second connection doesn't own the selection")
	}
	return w
}

// terminateTimed quits GLFW, holding the clipboard, and returns how long that took. A wait that never ends fails the
// test rather than stalling the run.
func terminateTimed(t *testing.T) time.Duration {
	t.Helper()
	if err := platformSetClipboardString("hej"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			panic("the quit still waits after 30 s")
		}
	}()
	began := time.Now()
	if err := Terminate(); err != nil {
		t.Fatal(err)
	}
	return time.Since(began)
}

// A clipboard whose owner never answers leaves the read empty after a second, and doesn't hold the window up for good.
func TestAClipboardOwnerThatNeverAnswersDoesNotHoldTheWindow(t *testing.T) {
	silent := startX11(t)
	defer func() { _ = Terminate() }()
	// The second connection takes the clipboard, and never reads its events.
	own(t, silent, _glfw.platformWindow.CLIPBOARD)

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			panic("the read of the clipboard still waits after 30 s")
		}
	}()
	began := time.Now()
	text, err := GetClipboardString()
	took := time.Since(began)
	if !errors.Is(err, PlatformError) || text != "" {
		t.Errorf("read %q, %v: want nothing and a platform error", text, err)
	}
	if took < selectionWait || took > selectionWait+2*time.Second {
		t.Errorf("the read took %v, want about %v", took, selectionWait)
	}
}

// A clipboard manager that never answers holds the quit up for a second, not for good.
func TestAClipboardManagerThatNeverAnswersDoesNotHoldTheQuit(t *testing.T) {
	silent := startX11(t)
	own(t, silent, _glfw.platformWindow.CLIPBOARD_MANAGER)
	if took := terminateTimed(t); took < selectionWait || took > selectionWait+2*time.Second {
		t.Errorf("the quit took %v, want about %v", took, selectionWait)
	}
}

// With no clipboard manager there is nothing to wait for.
func TestAQuitWithNoClipboardManagerDoesNotWait(t *testing.T) {
	startX11(t)
	if took := terminateTimed(t); took >= selectionWait {
		t.Errorf("the quit took %v, want less than %v", took, selectionWait)
	}
}

// A clipboard manager that keeps asking gets all the time it needs, past the wait for a silent one, and the quit ends
// as it says it is done.
func TestAClipboardManagerThatKeepsAskingKeepsTheQuitWaiting(t *testing.T) {
	manager := startX11(t)
	display := manager
	CLIPBOARD, UTF8, SAVE, MANAGER := _glfw.platformWindow.CLIPBOARD, _glfw.platformWindow.UTF8_STRING,
		_glfw.platformWindow.SAVE_TARGETS, _glfw.platformWindow.CLIPBOARD_MANAGER
	w := own(t, display, MANAGER)
	prop := xInternAtom(display, "HEJ_TEST", false)
	const asks = 4
	gap := selectionWait * 3 / 4

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		// Wait for the quit to ask this manager to save.
		var ev _XEvent
		for !xCheckTypedWindowEvent(display, w, _SelectionRequest, &ev) {
			time.Sleep(time.Millisecond)
		}
		req := *ev.xselectionrequest()
		for range asks {
			time.Sleep(gap)
			xConvertSelection(display, CLIPBOARD, UTF8, prop, w, _CurrentTime)
			xFlush(display)
		}
		time.Sleep(gap / 4)
		var reply _XEvent
		sel := reply.xselection()
		sel.Type, sel.Display, sel.Requestor = _SelectionNotify, display, req.Requestor
		sel.Selection, sel.Target, sel.Property, sel.Time = MANAGER, SAVE, req.Property, req.Time
		xSendEvent(display, req.Requestor, false, 0, &reply)
		xFlush(display)
	}()
	took := terminateTimed(t)
	<-finished
	if took < asks*gap {
		t.Errorf("the quit took %v: it left before the manager was done, at %v", took, asks*gap)
	}
	if took > asks*gap+selectionWait {
		t.Errorf("the quit took %v, past the manager's %v and the wait", took, asks*gap)
	}
}
