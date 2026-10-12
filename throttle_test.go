package gunim

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/driver"
)

// counted is a view that records how often its update ran, so a test
// can tell one reconciliation from a hundred.
type counted struct {
	probe
	updates int
}

func newCountedWindow(t *testing.T) *Window {
	t.Helper()
	w := newTestWindow()
	RegisterView(w, "counted",
		func(s panelState) *counted {
			c := &counted{probe: *newProbe(s.Label)}
			return c
		},
		func(c *counted, s panelState, _ *UI) {
			c.updates++
			c.label = s.Label
		})
	return w
}

func countedAt(t *testing.T, w *Window, id ID) *counted {
	t.Helper()
	s, ok := w.ui.ids[id]
	if !ok {
		t.Fatalf("view %q is missing", id)
	}
	c, ok := s.node.(*counted)
	if !ok {
		t.Fatalf("view %q is a %T, want a *counted", id, s.node)
	}
	return c
}

func TestPublishBurstCostsOneUpdatePerFrame(t *testing.T) {
	w := newCountedWindow(t)
	c := w.Client()
	if err := c.Mount(Root, "panel", "counted", panelState{Label: "start"}, "jobs"); err != nil {
		t.Fatal(err)
	}
	run(w, 1)
	before := countedAt(t, w, "panel").updates

	// The application pushes far faster than any display refreshes.
	for i := range 100 {
		if err := c.Publish("jobs", panelState{Label: string(rune('a' + i%26))}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Publish("jobs", panelState{Label: "last"}); err != nil {
		t.Fatal(err)
	}
	run(w, 1)

	view := countedAt(t, w, "panel")
	if got := view.updates - before; got != 1 {
		t.Fatalf("view updated %d times for 101 publishes in one frame, want 1", got)
	}
	if view.label != "last" {
		t.Fatalf("label = %q, want the newest state to be the one that landed", view.label)
	}
	if s := w.Stats(); s.Coalesced != 100 {
		t.Fatalf("Coalesced = %d, want 100 of the 101 publishes superseded", s.Coalesced)
	}
}

func TestMountKeepsEarlierStateFromBeingCoalescedAway(t *testing.T) {
	// Coalescing must stop at a command that changes what exists, so
	// state queued ahead of a mount still reaches the views that were
	// already there.
	w := newCountedWindow(t)
	c := w.Client()
	if err := c.Mount(Root, "first", "counted", panelState{Label: "start"}, "jobs"); err != nil {
		t.Fatal(err)
	}
	run(w, 1)
	before := countedAt(t, w, "first").updates

	mustPublish(t, c, "a")
	mustPublish(t, c, "b") // supersedes a
	if err := c.Mount(Root, "second", "counted", panelState{Label: "new"}, "jobs"); err != nil {
		t.Fatal(err)
	}
	mustPublish(t, c, "c") // lands after the mount, so it stands alone
	run(w, 1)

	if got := countedAt(t, w, "first").updates - before; got != 2 {
		t.Fatalf("first view updated %d times, want 2: b before the mount and c after", got)
	}
	if got := countedAt(t, w, "first").label; got != "c" {
		t.Fatalf("label = %q, want c", got)
	}
}

func TestPatchesAreNeverCoalesced(t *testing.T) {
	// Two patches on one topic may aim at different rows, so each one
	// has to be applied.
	w := newProbeWindow(t)
	c := w.Client()
	if err := c.Mount(Root, "panel", "panel", panelState{}, "jobs"); err != nil {
		t.Fatal(err)
	}
	run(w, 1)
	coalescedBefore := w.Stats().Coalesced

	for range 5 {
		if err := c.Patch("jobs", tick{At: 1}); err != nil {
			t.Fatal(err)
		}
	}
	run(w, 1)

	if got := w.Stats().Coalesced - coalescedBefore; got != 0 {
		t.Fatalf("%d patches were coalesced away; every patch carries something of its own", got)
	}
}

func TestLoopDrawsOncePerDisplayRefresh(t *testing.T) {
	w := newCountedWindow(t)
	d := w.mustOffscreen(t)
	c := w.Client()
	go w.loop()
	defer w.Close()

	// With nothing in flight, the first frame goes out at once.
	if err := c.Mount(Root, "panel", "counted", panelState{Label: "start"}, "jobs"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return w.Stats().Frames >= 1 }, "the first frame")

	// The next frame waits for the display to take that one, however
	// much the application queues. The probe is still animating in, so
	// the window wants every frame it can get.
	for range 50 {
		mustPublish(t, c, "push")
	}
	time.Sleep(25 * time.Millisecond)
	if got := w.Stats().Frames; got != 1 {
		t.Fatalf("drew %d frames with the first still in flight, want 1", got)
	}

	d.Tick()
	waitFor(t, func() bool { return w.Stats().Frames >= 2 }, "the second frame")
	time.Sleep(25 * time.Millisecond)
	if got := w.Stats().Frames; got != 2 {
		t.Fatalf("drew %d frames for one presented frame, want 2", got)
	}
	if got := w.Stats().Coalesced; got != 49 {
		t.Fatalf("Coalesced = %d, want 49 of the 50 publishes superseded while waiting", got)
	}
}

func TestFirstFrameAfterIdleStepsOneRefresh(t *testing.T) {
	w := newProbeWindow(t)
	d := w.mustOffscreen(t)
	c := w.Client()
	go w.loop()
	defer w.Close()

	if err := c.Mount(Root, "panel", "panel", panelState{}, "jobs"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return w.Stats().Frames >= 1 }, "the first frame")

	// Present frames until the probe has finished entering and the
	// window stops drawing.
	// A frame usually follows a tick within microseconds, but the UI
	// goroutine keeps its own OS thread, and waking one can take
	// milliseconds, so idle means no frame for a generous while.
	for i := 0; ; i++ {
		if i == 500 {
			t.Fatal("window never went idle")
		}
		before := w.Stats().Frames
		d.Tick()
		if !within(50*time.Millisecond, func() bool { return w.Stats().Frames > before }) {
			break
		}
	}

	// Sleep past the longest step a spring will take, then start an
	// animation. Its first frame should advance it by one refresh, as
	// if it had started a frame ago; the time slept stays out of it.
	time.Sleep(150 * time.Millisecond)
	before := w.Stats().Frames
	if err := c.Patch("jobs", tick{At: 1}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return w.Stats().Frames > before }, "the frame after idle")

	want := anim.NewFloat(0)
	want.Animate(1, anim.Snappy)
	want.Step(time.Second / 60)
	if got := probeOf(t, w.ui.ids["panel"].node).at.Value(); got != want.Value() {
		t.Fatalf("after one frame the value is %v, want %v: one refresh of motion", got, want.Value())
	}
}

func TestNextVsyncFollowsThePhaseOfTheLastFrame(t *testing.T) {
	const iv = 10 * time.Millisecond
	shown := time.Unix(100, 0)
	cases := []struct {
		name string
		now  time.Duration // after shown
		want time.Duration // after shown
	}{
		{"straight after a swap", 1 * time.Millisecond, 10 * time.Millisecond},
		{"late in the same refresh", 9 * time.Millisecond, 10 * time.Millisecond},
		{"after idling several refreshes", 43 * time.Millisecond, 50 * time.Millisecond},
		{"on a refresh boundary", 20 * time.Millisecond, 30 * time.Millisecond},
		{"with the clock behind the report", -5 * time.Millisecond, 10 * time.Millisecond},
	}
	for _, tc := range cases {
		if got := nextVsync(shown, iv, shown.Add(tc.now)); !got.Equal(shown.Add(tc.want)) {
			t.Errorf("%s: due %v after shown, want %v", tc.name, got.Sub(shown), tc.want)
		}
	}
	now := time.Unix(200, 0)
	if got := nextVsync(time.Time{}, iv, now); !got.Equal(now.Add(iv)) {
		t.Errorf("with nothing shown yet: due %v after now, want %v", got.Sub(now), iv)
	}
}

func TestATimerWakesTheLoopForTheFrameThatRunsIt(t *testing.T) {
	const iv = 10 * time.Millisecond
	shown := time.Unix(100, 0)
	for _, due := range []int{1, 3, 9, 10, 11, 20, 47} {
		at := shown.Add(time.Duration(due) * time.Millisecond)
		wake := wakeFor(at, shown, iv)
		// Woken on time, or a little late, the frame is stamped at or after the timer, and a refresh at most after it.
		for _, late := range []time.Duration{0, time.Millisecond, iv - 1} {
			stamp := nextVsync(shown, iv, wake.Add(late))
			if stamp.Before(at) || stamp.Sub(at) >= iv {
				t.Errorf("timer %v after shown, woken %v late: frame stamped %v after shown", due, late, stamp.Sub(shown))
			}
		}
	}
}

// mustPublish sends state to the topic these tests share.
func mustPublish(t *testing.T, c Client, label string) {
	t.Helper()
	if err := c.Publish("jobs", panelState{Label: label}); err != nil {
		t.Fatal(err)
	}
}

// within reports whether cond turns true before d has passed.
func within(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return cond()
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// With a frame on its way to the screen, a timer that is already due
// waits for that frame: the loop sleeps until the frame is shown, and
// the timer runs in the frame after. Armed early, the timer would fire
// again each time the loop waited, and spin it until the frame showed.
func TestADueTimerWaitsForTheFrameInFlight(t *testing.T) {
	w := newTestWindow()
	w.ui.After(0, func(*UI) {})
	w.inFlight = true
	woke := make(chan bool, 1)
	go func() { woke <- w.wait() }()
	select {
	case <-woke:
		t.Fatal("the loop woke with a frame in flight and nothing new")
	case <-time.After(100 * time.Millisecond):
	}
	w.Offscreen().Tick()
	select {
	case open := <-woke:
		if !open || w.inFlight {
			t.Fatalf("the frame shown left the window open %v, a frame in flight %v", open, w.inFlight)
		}
	case <-time.After(time.Second):
		t.Fatal("the frame shown left the loop asleep")
	}
}

// Out of sight, a window draws no frames, so a timer that comes due
// cannot run until it is back. The loop sleeps until then. Armed
// anyway, the timer would fire at every wait and spin one core for as
// long as the window stayed hidden or covered.
func TestADueTimerWaitsForAWindowOutOfSight(t *testing.T) {
	for _, away := range []struct {
		name string
		ev   any
	}{
		{"hidden", driver.WindowShown{Shown: false}},
		{"covered", driver.WindowCovered{Covered: true}},
	} {
		t.Run(away.name, func(t *testing.T) {
			w := newTestWindow()
			w.Frame(time.Second / 60)
			w.Input(away.ev)
			w.ui.After(0, func(*UI) {})
			woke := make(chan bool, 1)
			go func() { woke <- w.wait() }()
			select {
			case <-woke:
				t.Fatal("the loop woke for a timer it cannot run while out of sight")
			case <-time.After(100 * time.Millisecond):
			}
			w.wake <- struct{}{}
			<-woke
		})
	}
}
