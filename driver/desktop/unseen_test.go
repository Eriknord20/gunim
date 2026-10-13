package desktop

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/internal/inbox"
)

// unseenWindow returns a window whose events can be read, with the wait
// for out of sight cut short.
func unseenWindow(t *testing.T) (*Window, *Driver) {
	t.Helper()
	old := unseenWait
	unseenWait = 20 * time.Millisecond
	quit := make(chan struct{})
	t.Cleanup(func() {
		unseenWait = old
		close(quit)
	})
	d := &Driver{}
	return &Window{d: d, in: inbox.New(quit)}, d
}

// waitTasks waits out unseenWait and runs what it posted.
func waitTasks(d *Driver) {
	time.Sleep(4 * unseenWait)
	d.runTasks()
}

// nextEvent returns the window's next event, or nil if none comes soon.
func nextEvent(w *Window) any {
	select {
	case ev := <-w.in.Out():
		return ev
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}

// A window covered goes on drawing for a while, so a menu fading out as
// the user switches away finishes, and is told it is out of sight only
// after unseenWait.
func TestUnseenWaits(t *testing.T) {
	w, d := unseenWindow(t)
	w.hiddenBy = true
	w.tellUnseen()
	if ev := nextEvent(w); ev != nil {
		t.Fatalf("covered, the window heard %#v at once, want nothing until the wait is over", ev)
	}
	waitTasks(d)
	if ev := nextEvent(w); ev != (driver.WindowCovered{Covered: true}) {
		t.Fatalf("after the wait, the window heard %#v, want covered", ev)
	}
	w.hiddenBy = false
	w.tellUnseen()
	if ev := nextEvent(w); ev != (driver.WindowCovered{Covered: false}) {
		t.Fatalf("uncovered, the window heard %#v, want uncovered at once", ev)
	}
}

// A window covered and uncovered again within the wait, as under Alt+Tab
// let go quickly, hears nothing.
func TestUnseenCalledOff(t *testing.T) {
	w, d := unseenWindow(t)
	w.hiddenBy = true
	w.tellUnseen()
	w.hiddenBy = false
	w.tellUnseen()
	waitTasks(d)
	if ev := nextEvent(w); ev != nil {
		t.Fatalf("covered and uncovered within the wait, the window heard %#v, want nothing", ev)
	}
	// Left covered after going back and forth, it is told so.
	w.hiddenBy = true
	w.tellUnseen()
	w.hiddenBy = false
	w.tellUnseen()
	w.hiddenBy = true
	w.tellUnseen()
	waitTasks(d)
	if ev := nextEvent(w); ev != (driver.WindowCovered{Covered: true}) {
		t.Fatalf("covered at the end, the window heard %#v, want covered", ev)
	}
}
