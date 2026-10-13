package desktop

import (
	"time"

	"github.com/marrasen/gunim/driver"
)

// unseenWait is how long a window goes on drawing after it goes out of
// sight. A menu fading as the user switches to another window finishes
// fading, rather than staying on the screen half gone. And the window
// keeps drawing under Windows' task switcher, held open with Alt.
var unseenWait = 10 * time.Second

// tellUnseen tells the window whether it is out of sight while open:
// covered by other windows, on another desktop, or on a screen that is
// off or locked. It runs on the main thread, and tells only changes.
// Back in sight is told at once, and out of sight once it has lasted
// unseenWait.
func (w *Window) tellUnseen() {
	unseen := w.hiddenBy || w.d.screenAway
	if !unseen {
		// A wait under way is called off: what it would tell is stale.
		w.unseenWaits++
		w.unseenWaiting = false
		w.setUnseen(false)
		return
	}
	if w.unseen || w.unseenWaiting {
		return
	}
	w.unseenWaiting = true
	n := w.unseenWaits
	time.AfterFunc(unseenWait, func() {
		w.d.post(func() {
			if n != w.unseenWaits || w.closed {
				return
			}
			w.unseenWaiting = false
			w.setUnseen(w.hiddenBy || w.d.screenAway)
		})
	})
}

// setUnseen tells the window it is out of sight, or back, if that changed.
func (w *Window) setUnseen(unseen bool) {
	if unseen == w.unseen {
		return
	}
	w.unseen = unseen
	w.debugf("out of sight %v", unseen)
	w.in.Push(driver.WindowCovered{Covered: unseen})
}
