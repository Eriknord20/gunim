package gunim

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// presentSeer moves on every frame, and keeps each frame it lays out
// and the present time the UI gave its Step.
type presentSeer struct {
	ui      *UI
	frames  []Frame
	stepped []time.Time
}

func (n *presentSeer) Layout(c Constraints, f Frame, _ Children) geom.Size {
	n.ui = f.UI()
	n.frames = append(n.frames, f)
	return c.Max
}

func (n *presentSeer) Paint(*paint.Painter, Frame, geom.Size, Children) {}

func (n *presentSeer) Step(time.Duration) bool {
	if n.ui != nil {
		n.stepped = append(n.stepped, n.ui.Present())
	}
	return true
}

// presentRig drives a window's own loop, a frame at a time, on a fake
// clock: each frame is drawn, then reported shown as the test says, a
// refresh apart.
type presentRig struct {
	t    *testing.T
	off  *driver.OffscreenWindow
	w    *Window
	seer *presentSeer
	tick time.Duration
}

func newPresentRig(t *testing.T) *presentRig {
	off := driver.Offscreen(geom.Sz(200, 100))
	seer := &presentSeer{}
	w := newWindow(off, seer)
	return &presentRig{t: t, off: off, w: w, seer: seer, tick: refreshInterval(off.RefreshRate())}
}

// frame draws a frame and returns it with the wall time it was drawn
// at. The driver then reports it shown off after its due time, lighting
// the screen lag after that.
func (r *presentRig) frame(off, lag time.Duration) (Frame, time.Time) {
	r.t.Helper()
	drawn := time.Now()
	r.w.ui.invalid = true
	r.w.draw()
	if len(r.seer.frames) == 0 {
		r.t.Fatal("the frame laid out no node")
	}
	f := r.seer.frames[len(r.seer.frames)-1]
	// From the second frame, Step runs on a node laid out before, and
	// reads the frame's present time from the UI.
	if len(r.seer.frames) > 1 {
		if n := len(r.seer.stepped); n == 0 || !r.seer.stepped[n-1].Equal(f.Present) {
			r.t.Fatalf("Step read UI.Present %v, the frame laid out with Present %v", r.seer.stepped, f.Present)
		}
	}
	go r.off.TickWith(driver.Frame{Shown: r.w.due.Add(off), Lag: lag})
	for r.w.unshown > 0 {
		if !r.w.wait() {
			r.t.Fatal("the window closed")
		}
	}
	time.Sleep(r.tick)
	return f, drawn
}

// Every frame's Present is its Now plus how late the frames before lit
// the screen past their due time: the driver's Shown and Lag together,
// averaged. It is checked frame by frame, from no report, through a
// steady lag, a change in it, a frame shown a refresh off, a wild
// report and one that says frames show before they are built.
func TestEveryFrameIsAimedAtWhenItShows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newPresentRig(t)
		tick := r.tick

		// The first frame: nothing has been shown to say how late.
		f, _ := r.frame(0, 2*tick)
		if !f.Present.Equal(f.Now) {
			t.Fatalf("first frame: Present %v after Now, want Now", f.Present.Sub(f.Now))
		}

		// Two refreshes late, as an Android compositor: the first report
		// counts whole, and the frames after keep to it.
		for i := range 10 {
			f, _ = r.frame(0, 2*tick)
			if lead := f.Present.Sub(f.Now); lead != 2*tick {
				t.Fatalf("steady frame %d: Present %v after Now, want %v", i, lead, 2*tick)
			}
		}

		// The lag drops to one refresh: each frame moves an eighth of the
		// way down, never past it.
		prev := 2 * tick
		for i := range 60 {
			f, _ = r.frame(0, tick)
			lead := f.Present.Sub(f.Now)
			want := prev + (tick-prev)/8
			if i == 0 {
				want = prev // the first frame was built before any report of the new lag
			}
			if lead != want || lead < tick {
				t.Fatalf("frame %d after the lag dropped: Present %v after Now, want %v", i, lead, want)
			}
			prev = lead
		}
		if prev-tick > tick/100 {
			t.Fatalf("after 60 frames Present is %v after Now, want about %v", prev, tick)
		}

		// A frame shown a refresh after its due time, with no lag of its
		// own, is as late as one shown on time a refresh before it lit.
		for i := range 5 {
			f, _ = r.frame(tick, 0)
			if lead := f.Present.Sub(f.Now); lead < tick-tick/100 || lead > tick+tick/100 {
				t.Fatalf("frame %d shown a refresh late: Present %v after Now, want about %v", i, lead, tick)
			}
		}

		// A wild report is held to maxLate.
		for i := range 40 {
			f, _ = r.frame(0, 10*time.Second)
			if lead := f.Present.Sub(f.Now); lead > maxLate {
				t.Fatalf("frame %d after wild reports: Present %v after Now, want %v at most", i, lead, maxLate)
			}
		}

		// Reports that frames light the screen before their due time
		// leave Present before Now, but never before the frame was built.
		for i := range 60 {
			var drawn time.Time
			f, drawn = r.frame(-20*tick, 0)
			if f.Present.Before(drawn) {
				t.Fatalf("frame %d: Present %v before the frame was built", i, drawn.Sub(f.Present))
			}
			// Once the average is well below zero, the frame is taken to
			// show as it is built.
			if i >= 30 && !f.Present.Equal(drawn) {
				t.Fatalf("frame %d: Present %v after the frame was built, want at it", i, f.Present.Sub(drawn))
			}
		}
		r.w.Close()
	})
}

// A window stepped by Window.Frame shows nothing: each frame's Present
// is its Now.
func TestASteppedFrameIsPresentAtItsTime(t *testing.T) {
	seer := &presentSeer{}
	w := NewOffscreen(geom.Sz(200, 100), seer)
	for i := range 5 {
		w.Frame(time.Second / 60)
		f := seer.frames[len(seer.frames)-1]
		if !f.Present.Equal(f.Now) {
			t.Fatalf("stepped frame %d: Present %v after Now", i, f.Present.Sub(f.Now))
		}
		if got := w.ui.Present(); !got.Equal(f.Now) {
			t.Fatalf("stepped frame %d: UI.Present %v after Now", i, got.Sub(f.Now))
		}
	}
}

// With two frames in flight, as a driver that releases a frame before
// it shows it allows, each report is of the older frame, and is taken
// against that frame's due time, a refresh before the newer one's.
// Paired with the newer frame, each frame would seem a refresh early,
// and Present would be a refresh short.
func TestAReportIsTakenAgainstItsOwnFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		off := driver.Offscreen(geom.Sz(200, 100))
		dw := &releasing{OffscreenWindow: off, released: make(chan struct{}, 2)}
		seer := &presentSeer{}
		w := newWindow(dw, seer)
		tick := refreshInterval(off.RefreshRate())
		lag := 3 * tick
		var dues []time.Time
		draw := func() Frame {
			w.ui.invalid = true
			w.draw()
			dues = append(dues, w.due)
			return seer.frames[len(seer.frames)-1]
		}
		// show releases the oldest frame, then reports it shown at its
		// due time, lighting the screen lag after.
		show := func() {
			due := dues[0]
			dues = dues[1:]
			unreleased, unshown := w.unreleased, w.unshown
			dw.released <- struct{}{}
			for w.unreleased == unreleased {
				w.wait()
			}
			go off.TickWith(driver.Frame{Shown: due, Lag: lag})
			for w.unshown == unshown {
				w.wait()
			}
		}
		draw()
		draw()
		for i := range 30 {
			if len(w.dues) != 2 {
				t.Fatalf("frame %d: %d frames in flight, want 2", i, len(w.dues))
			}
			if d := dues[1].Sub(dues[0]); d != tick {
				t.Fatalf("frame %d: the two frames in flight are due %v apart, want a refresh, %v", i, d, tick)
			}
			show()
			f := draw()
			if lead := f.Present.Sub(f.Now); lead != lag {
				t.Fatalf("frame %d: Present %v after Now, want %v", i, lead, lag)
			}
			time.Sleep(tick)
		}
		w.Close()
	})
}
