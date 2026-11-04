// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

//go:build freebsd || linux || netbsd

package glfw

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// patience is how long a test gives what it waits for on a loaded machine, before it calls the wait endless.
const patience = 30 * time.Second

// startX11 readies GLFW on a display of the test's own, as these tests take the clipboard, which on a real display
// would lose what the user copied. It returns a second connection to that display, to play another program, and leaves
// the test on one OS thread, as GLFW needs.
func startX11(t *testing.T) (other uintptr) {
	t.Helper()
	xvfb, err := exec.LookPath("Xvfb")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("CI needs Xvfb, or these tests guard nothing")
		}
		t.Skip("no Xvfb")
	}
	cmd := exec.Command(xvfb, "-displayfd", "1", "-screen", "0", "64x64x24", "-nolisten", "tcp")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	number, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("Xvfb said no display: %v", err)
	}
	t.Setenv("DISPLAY", ":"+strings.TrimSpace(number))

	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Terminate() })
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

// watchdog panics if the wait it watches outlasts patience, as a wait with no end would stall the whole run. Calling
// what it returns ends the watch.
func watchdog(what string) (stop func()) {
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-time.After(patience):
			panic(what + " still waits after " + patience.String())
		}
	}()
	return func() { close(done) }
}

// nextRequest returns the next request to convert a selection owned by w, or false where none comes in time.
func nextRequest(display uintptr, w _XID) (_XSelectionRequestEvent, bool) {
	var ev _XEvent
	for end := time.Now().Add(patience); !xCheckTypedWindowEvent(display, w, _SelectionRequest, &ev); {
		if time.Now().After(end) {
			return _XSelectionRequestEvent{}, false
		}
		time.Sleep(time.Millisecond)
	}
	return *ev.xselectionrequest(), true
}

// answer tells the requestor of req that its answer is in req's property.
func answer(display uintptr, req _XSelectionRequestEvent) {
	var reply _XEvent
	sel := reply.xselection()
	sel.Type, sel.Display, sel.Requestor = _SelectionNotify, display, req.Requestor
	sel.Selection, sel.Target, sel.Property, sel.Time = req.Selection, req.Target, req.Property, req.Time
	xSendEvent(display, req.Requestor, false, 0, &reply)
	xFlush(display)
}

// answerText puts text in req's property, and says so.
func answerText(display uintptr, req _XSelectionRequestEvent, text string) {
	xChangePropertyGeneric(display, req.Requestor, req.Property, req.Target, _PropModeReplace, []byte(text))
	answer(display, req)
}

// readTimed reads the clipboard, and returns what it read and how long that took.
func readTimed() (string, error, time.Duration) {
	defer watchdog("the read of the clipboard")()
	began := time.Now()
	text, err := GetClipboardString()
	return text, err, time.Since(began)
}

// terminateTimed quits GLFW, holding the clipboard, and returns how long that took.
func terminateTimed(t *testing.T) time.Duration {
	t.Helper()
	if err := platformSetClipboardString("hej"); err != nil {
		t.Fatal(err)
	}
	defer watchdog("the quit")()
	began := time.Now()
	if err := Terminate(); err != nil {
		t.Fatal(err)
	}
	return time.Since(began)
}

// A clipboard whose owner never answers leaves the read empty after a second, and the next reads leave it alone, so the
// window doesn't stop on each.
func TestAClipboardOwnerThatNeverAnswersDoesNotHoldTheWindow(t *testing.T) {
	silent := startX11(t)
	own(t, silent, _glfw.platformWindow.CLIPBOARD)

	text, err, took := readTimed()
	if !errors.Is(err, PlatformError) || text != "" {
		t.Errorf("read %q, %v: want nothing and a platform error", text, err)
	}
	if took < selectionWait {
		t.Errorf("the read gave up after %v, before %v", took, selectionWait)
	}

	text, err, took = readTimed()
	if !errors.Is(err, PlatformError) || text != "" {
		t.Errorf("the second read found %q, %v: want nothing and a platform error", text, err)
	}
	if took >= selectionWait/2 {
		t.Errorf("the second read took %v: it waited for an owner known to be silent", took)
	}
}

// An answer that comes after its read gave up is not the answer to the next read, which gets its new owner's.
func TestALateAnswerIsNotTakenForTheNextRead(t *testing.T) {
	other := startX11(t)
	first := own(t, other, _glfw.platformWindow.CLIPBOARD)
	if _, err, _ := readTimed(); err == nil {
		t.Fatal("the read of a silent owner found no fault")
	}
	late, ok := nextRequest(other, first)
	if !ok {
		t.Fatal("the read never asked the owner")
	}
	second := own(t, other, _glfw.platformWindow.CLIPBOARD)

	asked := make(chan bool)
	go func() {
		req, ok := nextRequest(other, second)
		if ok {
			answerText(other, late, "old")
			time.Sleep(50 * time.Millisecond)
			answerText(other, req, "new")
		}
		asked <- ok
	}()
	text, err, _ := readTimed()
	if !<-asked {
		t.Fatal("the read never asked the new owner")
	}
	if err != nil || text != "new" {
		t.Errorf("read %q, %v: want the new owner's", text, err)
	}
}

// An owner that stops halfway through a transfer in pieces is left after a second too.
func TestAClipboardOwnerThatStopsHalfwayDoesNotHoldTheWindow(t *testing.T) {
	other := startX11(t)
	w := own(t, other, _glfw.platformWindow.CLIPBOARD)
	incr := _glfw.platformWindow.INCR

	asked := make(chan bool)
	go func() {
		req, ok := nextRequest(other, w)
		if ok {
			xChangePropertyGeneric(other, req.Requestor, req.Property, incr, _PropModeReplace, []_Clong{1 << 20})
			answer(other, req)
		}
		asked <- ok
	}()
	text, err, took := readTimed()
	if !<-asked {
		t.Fatal("the read never asked the owner")
	}
	if !errors.Is(err, PlatformError) || text != "" {
		t.Errorf("read %q, %v: want nothing and a platform error", text, err)
	}
	if took < selectionWait {
		t.Errorf("the read gave up after %v, before %v", took, selectionWait)
	}
}

// A clipboard manager that never answers holds the quit up for a second, not for good.
func TestAClipboardManagerThatNeverAnswersDoesNotHoldTheQuit(t *testing.T) {
	silent := startX11(t)
	own(t, silent, _glfw.platformWindow.CLIPBOARD_MANAGER)
	if took := terminateTimed(t); took < selectionWait {
		t.Errorf("the quit gave up after %v, before %v", took, selectionWait)
	}
}

// With no clipboard manager there is nothing to wait for.
func TestAQuitWithNoClipboardManagerDoesNotWait(t *testing.T) {
	startX11(t)
	if took := terminateTimed(t); took >= selectionWait {
		t.Errorf("the quit took %v, want less than %v", took, selectionWait)
	}
}

// keepAsking plays a clipboard manager: told to save, it asks for the clipboard every gap, a number of times, and then
// says it is done, if it is ever to be. It reports on the channel whether it was told, as it stops.
func keepAsking(display uintptr, w _XID, asks int, gap time.Duration, stop <-chan struct{}, say bool) <-chan bool {
	clipboard, utf8 := _glfw.platformWindow.CLIPBOARD, _glfw.platformWindow.UTF8_STRING
	property := xInternAtom(display, "HEJ_TEST", false)
	told := make(chan bool, 1)
	go func() {
		req, ok := nextRequest(display, w)
		defer func() { told <- ok }()
		if !ok {
			return
		}
		for range asks {
			select {
			case <-stop:
				return
			case <-time.After(gap):
			}
			xConvertSelection(display, clipboard, utf8, property, w, _CurrentTime)
			xFlush(display)
		}
		if say {
			answer(display, req)
		}
	}()
	return told
}

// A clipboard manager that keeps asking gets the time it needs, past the wait for a silent one, and the quit ends as
// it says it is done.
func TestAClipboardManagerThatKeepsAskingKeepsTheQuitWaiting(t *testing.T) {
	other := startX11(t)
	w := own(t, other, _glfw.platformWindow.CLIPBOARD_MANAGER)
	const asks = 4
	gap := selectionWait / 2
	told := keepAsking(other, w, asks, gap, nil, true)
	took := terminateTimed(t)
	if !<-told {
		t.Fatal("the quit never told the manager to save")
	}
	if took < asks*gap {
		t.Errorf("the quit took %v: it left before the manager was done, at %v", took, asks*gap)
	}
}

// Asking without end doesn't hold the quit up for good either.
func TestAClipboardManagerThatNeverStopsAskingDoesNotHoldTheQuit(t *testing.T) {
	other := startX11(t)
	w := own(t, other, _glfw.platformWindow.CLIPBOARD_MANAGER)
	stop := make(chan struct{})
	told := keepAsking(other, w, 1<<30, selectionWait/10, stop, false)
	took := terminateTimed(t)
	close(stop)
	if !<-told {
		t.Fatal("the quit never told the manager to save")
	}
	if took < selectionMost {
		t.Errorf("the quit gave up after %v, before %v", took, selectionMost)
	}
}
