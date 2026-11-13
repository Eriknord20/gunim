//go:build linux && (arm64 || amd64)

package kms

import (
	"fmt"
	"log"
	"time"

	"golang.org/x/sys/unix"

	"github.com/marrasen/gunim/driver/internal/touch"
	"github.com/marrasen/gunim/geom"
)

// touchState is the touch in progress, guarded by the driver's mu.
type touchState struct {
	// gest and contacts turn the fingers into the windows' input, as on
	// Android; see driver/internal/touch.
	gest     touch.Gestures
	contacts *touch.Contacts
	// resample holds the fingers' moves while a frame is on its way, and
	// hands the next frame one move a finger at an even step; see
	// flushTouch. holding says a frame of the windows is on its way.
	resample touch.Resampler
	holding  bool
	// swallow holds the slots of a touch that woke the screen: it reaches
	// no window, and ends as its last finger lifts.
	swallow map[int]bool
	// now and later are a report's landings and lifts, and its moves,
	// kept from report to report.
	now, later []touch.Contact
}

// touchMap maps a touch screen's positions onto the panel.
type touchMap struct {
	x, y absInfo
}

// panel returns where a touch at x, y lies on a panel of w×h pixels.
func (m touchMap) panel(x, y int32, w, h int) geom.Point {
	span := func(v int32, a absInfo, n int) float32 {
		r := a.maximum - a.minimum + 1
		if r <= 0 {
			return float32(v)
		}
		return (float32(v-a.minimum) + 0.5) * float32(n) / float32(r)
	}
	return geom.Pt(span(x, m.x, w), span(y, m.y, h))
}

// at returns where a touch at x, y lies on the picture, in logical pixels.
func (d *Driver) at(m touchMap, x, y int32) geom.Point {
	p := m.panel(x, y, d.panelW, d.panelH)
	p = d.turn.toPicture(p, float32(d.panelW), float32(d.panelH))
	return geom.Pt(p.X/d.density, p.Y/d.density)
}

// touchReport takes one report of the touch screen's fingers. Landings
// and lifts go to the windows at once, through driver/internal/touch,
// which turns them into the pointer, fingers and pinches as on Android.
// Moves go at once too while the windows draw nothing; while a frame is on
// its way they wait for the frame after it, resampled to one move a
// finger at an even step (flushTouch).
//
// A touch on a screen that is dim or off wakes it, and is swallowed: none
// of its fingers reach a window, so a tap on a dark screen never presses
// what lies under it.
func (d *Driver) touchReport(m touchMap, cs []contact, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	t := d.touchLocked()
	// While a finger is down the screen neither dims nor turns off.
	defer func() { d.blank.fingers = t.contacts.Count() > 0 || len(t.swallow) > 0 }()
	if d.away {
		// Another VT shows: what is touched is not this UI, which no one
		// sees. A touch going on as the VT switched lets go.
		t.contacts.Cancel(now)
		t.resample = touch.Resampler{}
		clear(t.swallow)
		return
	}
	if d.swallowLocked(cs, now) {
		return
	}
	if d.touchAt.IsZero() {
		d.touchAt = now
	}
	t.now, t.later = t.now[:0], t.later[:0]
	for _, c := range cs {
		at := d.at(m, c.x, c.y)
		switch c.phase {
		case phaseDown:
			t.resample.Down(c.slot, now, at)
			t.now = append(t.now, touch.Contact{ID: c.slot, Pos: at, Phase: touch.Down})
		case phaseUp:
			t.resample.Up(c.slot)
			t.now = append(t.now, touch.Contact{ID: c.slot, Pos: at, Phase: touch.Up})
		case phaseMove:
			if t.contacts.Down(c.slot) {
				t.resample.Move(c.slot, now, at)
			}
		}
	}
	t.contacts.Report(t.now, now)
	if !t.holding {
		t.later = t.resample.Newest(t.later)
		t.contacts.Report(t.later, now)
	}
}

// touchLocked returns the touch in progress, set up on first use. It runs
// with mu held.
func (d *Driver) touchLocked() *touchState {
	t := &d.input
	if t.contacts == nil {
		t.gest.Hit = func(p geom.Point) touch.Window {
			if w := d.hitLocked(p); w != nil {
				return w
			}
			return nil
		}
		t.contacts = touch.NewContacts(&t.gest)
		t.swallow = map[int]bool{}
	}
	return t
}

// swallowLocked reports whether the report belongs to a touch that woke
// the screen. A report while the screen is dim or off starts one: the
// screen wakes, and that touch's fingers reach no window until the last
// lifts. It runs with mu held.
func (d *Driver) swallowLocked(cs []contact, now time.Time) bool {
	t := &d.input
	if len(t.swallow) == 0 {
		if !d.blank.touched(now) {
			return false
		}
		// A touch going on as the screen went off, if one was, lets go.
		t.contacts.Cancel(now)
		t.resample = touch.Resampler{}
		d.kick()
	}
	for _, c := range cs {
		if c.phase == phaseUp {
			delete(t.swallow, c.slot)
		} else {
			t.swallow[c.slot] = true
		}
	}
	return true
}

// flushTouch hands the windows the fingers' moves held while a frame was
// on its way, resampled to where they were a little before start, the
// next frame's start. It runs with mu held, on the render thread, just
// before the windows hear of the flip.
func (d *Driver) flushTouchLocked(start time.Time) {
	t := &d.input
	t.holding = false
	if t.contacts == nil {
		return
	}
	t.later = t.resample.At(start, t.later[:0])
	t.contacts.Report(t.later, start)
}

// hitLocked returns the topmost window shown at p, in logical pixels of the
// picture, that takes the pointer. It runs with mu held.
func (d *Driver) hitLocked(p geom.Point) *Window {
	for i := len(d.windows) - 1; i >= 0; i-- {
		w := d.windows[i]
		if w.hidden || w.passthrough {
			continue
		}
		if w.rectLocked().Contains(p) {
			return w
		}
	}
	return nil
}

// startInput starts a reader for the touch screen, or the recording to play,
// and for each keyboard.
func (d *Driver) startInput() {
	run := func(fn func()) {
		d.readers.Add(1)
		go func() {
			defer d.readers.Done()
			fn()
		}()
	}
	switch {
	case d.o.Replay != nil:
		run(d.replay)
	case d.touch != nil:
		run(func() { d.readTouch(d.touch) })
	}
	for _, k := range d.keys {
		run(func() { d.readKeys(k) })
	}
}

// readTouch reads the touch screen until the driver quits. Open has taken
// the device for the driver and set its clock; see inputDev.take. Fingers
// already down as it starts come down at once, and a device that goes
// away lifts every finger down, so no node waits on a lost lift.
func (d *Driver) readTouch(dev *inputDev) {
	m := touchMap{x: dev.x, y: dev.y}
	dec := newMTDecoder(dev.slots)
	dec.resync = dev.mtSlots
	if cs := dec.resynced(); cs != nil {
		d.touchReport(m, cs, time.Now())
	}
	gone := d.readEvents(dev, func(e event) {
		if d.rec != nil {
			d.rec.write(e)
		}
		if cs := dec.feed(e); cs != nil {
			d.touchReport(m, cs, eventTime(e, dev.realtime, time.Now()))
		}
	})
	if gone {
		if cs := dec.liftAll(); cs != nil {
			d.touchReport(m, cs, time.Now())
		}
		// Nothing else wakes the screen: the program ends, to be started
		// again and find the screen afresh.
		d.lose(fmt.Errorf("%w: %s", ErrTouchGone, dev.path))
	}
}

// eventTime returns when an event happened: its stamp on the monotonic
// clock, or now where the device stamps on another clock, or where the
// stamp lies in the future or long past, as one from before the clock was
// set would.
func eventTime(e event, realtime bool, now time.Time) time.Time {
	if realtime {
		return now
	}
	t := monoTime(e.time())
	if t.After(now.Add(time.Second)) || t.Before(now.Add(-time.Minute)) {
		return now
	}
	return t
}

// readKeys reads a keyboard until the driver quits, for Ctrl+Alt+Fn.
func (d *Driver) readKeys(dev *inputDev) {
	var ctrl, alt bool
	_ = d.readEvents(dev, func(e event) {
		if e.typ != evKey {
			return
		}
		down := e.value != 0
		switch e.code {
		case keyLeftCtrl, keyRightCtrl:
			ctrl = down
		case keyLeftAlt, keyRightAlt:
			alt = down
		default:
			if n := vtKey(e.code); n > 0 && e.value == 1 && ctrl && alt && d.o.VTSwitch && d.vt != nil && n != d.vt.num {
				if err := d.vt.activate(n); err != nil {
					log.Printf("gunim: kms: switch to VT %d: %v", n, err)
				}
			}
		}
	})
}

// readEvents calls fn for each event dev reads, until the driver quits or
// the device goes away, which it reports. It sleeps in poll until the
// device or the driver's stop has something, so a reader at rest costs
// nothing.
func (d *Driver) readEvents(dev *inputDev, fn func(event)) (gone bool) {
	buf := make([]byte, 64*liveEventSize)
	fds := []unix.PollFd{{Fd: int32(dev.fd), Events: unix.POLLIN}, {Fd: int32(d.stop), Events: unix.POLLIN}}
	for {
		select {
		case <-d.quit:
			return false
		default:
		}
		fds[0].Revents, fds[1].Revents = 0, 0
		_, err := unix.Poll(fds, -1)
		if err == unix.EINTR {
			continue
		}
		if fds[1].Revents != 0 {
			return false
		}
		if err != nil || fds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 && fds[0].Revents&unix.POLLIN == 0 {
			log.Printf("gunim: kms: %s: gone: %v", dev.path, err)
			return true
		}
		n, err := unix.Read(dev.fd, buf)
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		if err != nil || n == 0 {
			log.Printf("gunim: kms: %s: gone: %v", dev.path, err)
			return true
		}
		for o := 0; o+liveEventSize <= n; o += liveEventSize {
			fn(decodeEvent(buf[o:], liveEventSize))
		}
	}
}

// replay plays a recording through the same path the touch screen's events
// take, at the pace it was made.
func (d *Driver) replay() {
	p, err := readRecording(d.o.Replay)
	if err != nil {
		log.Printf("gunim: kms: %v", err)
		return
	}
	m := touchMap{x: p.x, y: p.y}
	dec := newMTDecoder(p.slots)
	if len(p.events) == 0 {
		return
	}
	start, first := time.Now(), p.events[0].time()
	for _, e := range p.events {
		if wait := time.Until(start.Add(e.time() - first)); wait > 0 {
			select {
			case <-d.quit:
				return
			case <-time.After(wait):
			}
		}
		if cs := dec.feed(e); cs != nil {
			d.touchReport(m, cs, time.Now())
		}
	}
}
