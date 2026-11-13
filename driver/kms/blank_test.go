//go:build linux && (arm64 || amd64)

package kms

import (
	"math"
	"os"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

var bt0 = time.Unix(5000, 0)

func after(d time.Duration) time.Time { return bt0.Add(d) }

func near(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-3 }

// Untouched, the screen dims after a minute, over a second, to 30 % of its
// brightness, and after five fades to black over 600 ms and turns off. A
// touch on a dim or dark screen wakes it and is to be swallowed; one on a
// lit screen is not.
func TestTheScreenDimsThenTurnsOffAndATouchWakesIt(t *testing.T) {
	b := newBlanker(Blank{Dim: time.Minute, Off: 5 * time.Minute}, 0.8, bt0)
	if at := b.next(bt0); !at.Equal(after(time.Minute)) {
		t.Fatalf("next look at %v, want after a minute", at.Sub(bt0))
	}
	if b.step(after(59*time.Second)) || b.state != screenOn || !near(b.bright(after(59*time.Second)), 0.8) {
		t.Fatalf("at 59 s: %v at %v", b.state, b.bright(after(59*time.Second)))
	}
	b.step(after(time.Minute))
	if b.state != screenDim {
		t.Fatalf("at 1 min: %v, want dim", b.state)
	}
	if v := b.bright(after(time.Minute + 500*time.Millisecond)); v <= 0.24 || v >= 0.8 {
		t.Fatalf("half way through the dim: %v", v)
	}
	if v := b.bright(after(time.Minute + time.Second)); !near(v, 0.24) {
		t.Fatalf("dim at %v, want 0.24", v)
	}
	if at := b.next(after(time.Minute + 2*time.Second)); !at.Equal(after(5 * time.Minute)) {
		t.Fatalf("next look at %v, want after five minutes", at.Sub(bt0))
	}
	b.step(after(5 * time.Minute))
	if b.state != screenFading {
		t.Fatalf("at 5 min: %v, want fading", b.state)
	}
	if b.step(after(5*time.Minute + 599*time.Millisecond)) {
		t.Fatal("off before the fade ended")
	}
	if !b.step(after(5*time.Minute+600*time.Millisecond)) || b.state != screenOff || b.bright(after(5*time.Minute+600*time.Millisecond)) != 0 {
		t.Fatalf("after the fade: %v", b.state)
	}
	if b.step(after(6 * time.Minute)) {
		t.Fatal("turned off twice")
	}
	if !b.next(after(6 * time.Minute)).IsZero() {
		t.Fatal("a dark screen looks again")
	}
	// A touch wakes it, from black, up to the brightness, and is swallowed.
	wake := after(10 * time.Minute)
	if !b.touched(wake) || b.state != screenOn {
		t.Fatalf("a touch on a dark screen: %v", b.state)
	}
	if v := b.bright(wake); v != 0 {
		t.Fatalf("waking starts at %v, want black", v)
	}
	if v := b.bright(wake.Add(wakeFade)); !near(v, 0.8) {
		t.Fatalf("woken to %v, want 0.8", v)
	}
	if b.touched(wake.Add(time.Second)) {
		t.Fatal("a touch on a lit screen is swallowed")
	}
	// The wait starts again from the last touch.
	if at := b.next(wake.Add(2 * time.Second)); !at.Equal(wake.Add(time.Second + time.Minute)) {
		t.Fatalf("next look %v after waking, want a minute after the last touch", at.Sub(wake))
	}
	// A touch on a dim screen is swallowed too.
	b.step(wake.Add(2 * time.Minute))
	if b.state != screenDim || !b.touched(wake.Add(2*time.Minute+time.Second)) {
		t.Fatalf("a touch on a dim screen: %v", b.state)
	}
}

// While a window keeps the screen awake, or a finger is down, it neither
// dims nor turns off; one that is dim wakes as a window asks.
func TestKeepingAwakeHoldsTheScreenOn(t *testing.T) {
	b := newBlanker(Blank{Dim: time.Minute, Off: 5 * time.Minute}, 1, bt0)
	b.step(after(time.Minute))
	b.setKeep(true, after(2*time.Minute))
	if b.state != screenOn {
		t.Fatalf("kept awake: %v", b.state)
	}
	b.step(after(time.Hour))
	if b.state != screenOn || !b.next(after(time.Hour)).IsZero() {
		t.Fatalf("an hour kept awake: %v", b.state)
	}
	b.setKeep(false, after(time.Hour))
	if at := b.next(after(time.Hour)); !at.Equal(after(time.Hour + time.Minute)) {
		t.Fatalf("let go, next look at %v", at.Sub(bt0))
	}
	b.fingers = true
	b.step(after(2 * time.Hour))
	if b.state != screenOn {
		t.Fatalf("with a finger down: %v", b.state)
	}
}

// The zero Blank never dims, and a new brightness fades in.
func TestNoBlankKeepsTheScreenOnAndANewLevelFades(t *testing.T) {
	b := newBlanker(Blank{}, 0.5, bt0)
	if b.step(after(24*time.Hour)) || b.state != screenOn || !b.next(after(24*time.Hour)).IsZero() {
		t.Fatalf("a day untouched: %v", b.state)
	}
	now := after(24 * time.Hour)
	b.setLevel(1, now)
	if v := b.bright(now.Add(levelFade / 2)); !near(v, 0.75) {
		t.Fatalf("half way: %v, want 0.75", v)
	}
	if at := b.next(now); at.IsZero() || at.After(now.Add(fadeTick)) {
		t.Fatalf("a fade looks again at %v", at.Sub(now))
	}
	if v := b.bright(now.Add(levelFade)); v != 1 {
		t.Fatalf("faded to %v", v)
	}
}

// A brightness takes the backlight's step at or above it, and black over
// the picture makes up the rest, in light, through the panel's gamma.
func TestABrightnessIsAStepAndAShade(t *testing.T) {
	for _, c := range []struct {
		v     float32
		steps int
		level int
		shade float32
	}{
		{1, 31, 31, 0},
		{15.0 / 31, 31, 15, 0},
		{0, 31, 0, 1},
		{0.5, 31, 16, 1 - float32(math.Pow(15.5/16, 1/2.2))},
		{0.01, 31, 1, 1 - float32(math.Pow(0.31, 1/2.2))},
		// No backlight: the shade does it all.
		{0.25, 0, 0, 1 - float32(math.Pow(0.25, 1/2.2))},
		{1, 0, 0, 0},
	} {
		level, shade := lightFor(c.v, c.steps)
		if level != c.level || !near(shade, c.shade) {
			t.Errorf("%v of %d: step %d shade %v, want %d and %v", c.v, c.steps, level, shade, c.level, c.shade)
		}
	}
}

// The first touch on a dark screen wakes it and reaches no window, nor
// does any finger of it, until the last lifts; the next touch does.
func TestTheTouchThatWakesTheScreenIsSwallowed(t *testing.T) {
	d := testDriver(0, 1)
	d.blank = newBlanker(Blank{Dim: time.Minute, Off: 5 * time.Minute}, 1, bt0)
	d.blank.step(after(5 * time.Minute))
	d.blank.step(after(6 * time.Minute))
	if d.blank.state != screenOff {
		t.Fatalf("the screen is %v", d.blank.state)
	}
	w, _ := d.NewWindow(driver.Options{})
	win := w.(*Window)
	defer func() { _ = win.Close() }()
	m := touchMap{x: absInfo{maximum: 719}, y: absInfo{maximum: 1279}}
	now := after(7 * time.Minute)
	d.touchReport(m, []contact{{slot: 0, id: 1, x: 10, y: 10, phase: phaseDown}}, now)
	d.touchReport(m, []contact{{slot: 1, id: 2, x: 30, y: 10, phase: phaseDown}}, now.Add(17*time.Millisecond))
	d.touchReport(m, []contact{{slot: 0, id: 1, x: 12, y: 10, phase: phaseMove}}, now.Add(34*time.Millisecond))
	d.touchReport(m, []contact{{slot: 0, id: 1, phase: phaseUp}}, now.Add(51*time.Millisecond))
	if d.blank.state != screenOn {
		t.Fatalf("a touch left the screen %v", d.blank.state)
	}
	d.touchReport(m, []contact{{slot: 1, id: 2, phase: phaseUp}}, now.Add(68*time.Millisecond))
	if ev, ok := noInput(win); !ok {
		t.Fatalf("the waking touch reached the window: %v", ev)
	}
	d.touchReport(m, []contact{{slot: 0, id: 3, x: 50, y: 50, phase: phaseDown}}, now.Add(time.Second))
	if _, ok := nextInput(t, win).(input.PointerMove); !ok {
		t.Fatal("the next touch did not reach the window")
	}
	if _, ok := nextInput(t, win).(input.PointerDown); !ok {
		t.Fatal("the next touch did not press")
	}
}

// While a frame is on its way, the fingers' moves wait for the windows to
// hear of its flip, and then go as one move each, resampled to the next
// frame's start; while nothing is drawn, they go at once.
func TestMovesWaitForTheFlipAndGoResampled(t *testing.T) {
	d := testDriver(0, 1)
	w, _ := d.NewWindow(driver.Options{})
	win := w.(*Window)
	defer func() { _ = win.Close() }()
	m := touchMap{x: absInfo{maximum: 719}, y: absInfo{maximum: 1279}}
	t0 := time.Now()
	d.touchReport(m, []contact{{slot: 0, id: 1, x: 0, y: 0, phase: phaseDown}}, t0)
	nextInput(t, win)
	nextInput(t, win)
	// Nothing on its way: a move goes at once.
	d.touchReport(m, []contact{{slot: 0, id: 1, x: 17, y: 0, phase: phaseMove}}, t0.Add(17*time.Millisecond))
	if mv, ok := nextInput(t, win).(input.PointerMove); !ok || !near(mv.Pos.X, 17.5) {
		t.Fatalf("got %+v, want a move to 17.5 at once", mv)
	}
	// A frame on its way: the next two reports wait.
	d.mu.Lock()
	d.input.holding = true
	d.mu.Unlock()
	d.touchReport(m, []contact{{slot: 0, id: 1, x: 34, y: 0, phase: phaseMove}}, t0.Add(34*time.Millisecond))
	d.touchReport(m, []contact{{slot: 0, id: 1, x: 51, y: 0, phase: phaseMove}}, t0.Add(51*time.Millisecond))
	if ev, ok := noInput(win); !ok {
		t.Fatalf("a move went while a frame was on its way: %v", ev)
	}
	// The flip is heard 2 ms after the last report: the frame's start less
	// 9 ms is between the last two reports.
	d.mu.Lock()
	d.flushTouchLocked(t0.Add(53 * time.Millisecond))
	held := d.input.holding
	d.mu.Unlock()
	mv, ok := nextInput(t, win).(input.PointerMove)
	if !ok || !near(mv.Pos.X, 44.5) || held {
		t.Fatalf("got %+v, holding %v; want one move to 44.5, at 44 ms, and nothing held", mv, held)
	}
	if ev, ok := noInput(win); !ok {
		t.Fatalf("more than one move: %v", ev)
	}
}

// noInput reports whether w hears nothing for a while, and what it heard.
func noInput(w *Window) (any, bool) {
	select {
	case ev := <-w.Input():
		return ev, false
	case <-time.After(50 * time.Millisecond):
		return nil, true
	}
}

// Two fingers in a window that takes none pinch, as on Android.
func TestTwoFingersPinchOnTheTouchScreen(t *testing.T) {
	d := testDriver(0, 1)
	w, _ := d.NewWindow(driver.Options{})
	win := w.(*Window)
	defer func() { _ = win.Close() }()
	m := touchMap{x: absInfo{maximum: 719}, y: absInfo{maximum: 1279}}
	t0 := time.Now()
	d.touchReport(m, []contact{{slot: 0, id: 1, x: 100, y: 100, phase: phaseDown}}, t0)
	d.touchReport(m, []contact{{slot: 1, id: 2, x: 200, y: 100, phase: phaseDown}}, t0.Add(17*time.Millisecond))
	d.touchReport(m, []contact{{slot: 0, id: 1, x: 50, y: 100, phase: phaseMove}, {slot: 1, id: 2, x: 250, y: 100, phase: phaseMove}}, t0.Add(34*time.Millisecond))
	var got []string
	for range 6 {
		switch ev := nextInput(t, win).(type) {
		case input.PointerMove:
			got = append(got, "move")
		case input.PointerDown:
			got = append(got, "down")
		case input.PointerUp:
			got = append(got, "up")
		case input.Pinch:
			got = append(got, map[input.PinchPhase]string{input.PinchStart: "pinch start", input.PinchMove: "pinch move"}[ev.Phase])
			if ev.Phase == input.PinchMove && !near(ev.Scale, 2) {
				t.Errorf("pinched by %v, want 2", ev.Scale)
			}
		}
		if len(got) == 5 {
			break
		}
	}
	if want := []string{"move", "down", "up", "pinch start", "pinch move"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// While another VT shows, touches reach no window, and a touch going on as
// the VT switched lets go.
func TestTouchesWhileAwayReachNothing(t *testing.T) {
	d := testDriver(0, 1)
	w, _ := d.NewWindow(driver.Options{})
	win := w.(*Window)
	defer func() { _ = win.Close() }()
	m := touchMap{x: absInfo{maximum: 719}, y: absInfo{maximum: 1279}}
	t0 := time.Now()
	d.touchReport(m, []contact{{slot: 0, id: 1, x: 10, y: 10, phase: phaseDown}}, t0)
	nextInput(t, win)
	nextInput(t, win)
	d.mu.Lock()
	d.away = true
	d.mu.Unlock()
	d.touchReport(m, []contact{{slot: 0, id: 1, x: 20, y: 10, phase: phaseMove}}, t0.Add(17*time.Millisecond))
	if up, ok := nextInput(t, win).(input.PointerUp); !ok || up.Pos != input.Away {
		t.Fatalf("got %+v, want the press let go away", up)
	}
	nextInput(t, win) // the leave
	d.touchReport(m, []contact{{slot: 1, id: 2, x: 50, y: 50, phase: phaseDown}}, t0.Add(34*time.Millisecond))
	if ev, ok := noInput(win); !ok {
		t.Fatalf("a touch while away reached the window: %v", ev)
	}
}

// Turning the display off is one blocking commit with a modeset allowed:
// the CRTC inactive, its mode and plane kept.
func TestTheDisplayTurnsOffWithItsCRTC(t *testing.T) {
	s, f := testScreen()
	if err := s.off(); err != nil {
		t.Fatal(err)
	}
	if len(f.commits) != 1 {
		t.Fatalf("%d commits", len(f.commits))
	}
	c := f.commits[0]
	if c.flags != atomicAllowModeset || !slices.Equal(c.set, []string{"2.ACTIVE=0"}) {
		t.Fatalf("committed %#x with %v; want ALLOW_MODESET, blocking, with ACTIVE=0", c.flags, c.set)
	}
}

// A popup opens by its anchor on the turned picture, lies over the window
// that opened it in the stack the compositor lays, at its place in device
// pixels, and takes the touches that land on it, at its own coordinates.
func TestAPopupLiesOverItsWindowAndTakesItsTouches(t *testing.T) {
	d := testDriver(90, 2)
	w, _ := d.NewWindow(driver.Options{})
	win := w.(*Window)
	defer func() { _ = win.Close() }()
	p, err := d.NewWindow(driver.Options{Kind: driver.KindPopup, Parent: win, Anchor: geom.Rect{Min: geom.Pt(100, 50), Max: geom.Pt(200, 80)}, Size: geom.Sz(150, 100)})
	if err != nil {
		t.Fatal(err)
	}
	pop := p.(*Window)
	defer func() { _ = pop.Close() }()
	if got := pop.Size(); got != geom.Sz(150, 100) {
		t.Fatalf("popup %v", got)
	}
	r := &renderThread{d: d}
	d.mu.Lock()
	win.next, win.hasNext = frame{}, true
	pop.next, pop.hasNext = frame{}, true
	d.mu.Unlock()
	r.collect()
	if len(r.stack) != 2 || r.stack[0].Pane != &win.pane || r.stack[1].Pane != &pop.pane {
		t.Fatalf("stack %+v, want the window then the popup", r.stack)
	}
	// The picture is 1280×720 turned, the window fills it, and the popup
	// opens below its anchor, at 100,80 logical pixels: 200,160 device.
	if l := r.stack[0]; l.X != 0 || l.Y != 0 || l.W != 1280 || l.H != 720 {
		t.Fatalf("the window lies at %+v", l)
	}
	if l := r.stack[1]; l.X != 200 || l.Y != 160 || l.W != 300 || l.H != 200 || !pop.pane.Transparent {
		t.Fatalf("the popup lies at %+v, transparent %v", l, pop.pane.Transparent)
	}
	// A touch at 110,90 on the picture is on the popup, at 10,10 of it.
	// Turned 90°, the picture's x runs down the panel and its y leftwards
	// from the panel's right edge, at 2 device pixels a logical one.
	m := touchMap{x: absInfo{maximum: 719}, y: absInfo{maximum: 1279}}
	d.touchReport(m, []contact{{slot: 0, id: 1, x: 719 - 180, y: 220, phase: phaseDown}}, time.Now())
	mv, ok := nextInput(t, pop).(input.PointerMove)
	if !ok || math.Abs(float64(mv.Pos.X-10)) > 1 || math.Abs(float64(mv.Pos.Y-10)) > 1 {
		t.Fatalf("the popup got %+v, want a touch at 10,10", mv)
	}
	if ev, ok := noInput(win); !ok {
		t.Fatalf("the window under the popup got %v", ev)
	}
	// Moved by Place, it lies at its new place from the next frame.
	if err := pop.Place(geom.Rect{Min: geom.Pt(300, 50), Max: geom.Pt(400, 80)}, geom.Sz(150, 100)); err != nil {
		t.Fatal(err)
	}
	r.collect()
	if l := r.stack[1]; l.X != 600 || l.Y != 160 {
		t.Fatalf("placed again, the popup lies at %+v", l)
	}
}

// Someone switches to another VT while the screen is blanked, and back:
// the screen wakes as they return, from off, and the wait for dimming
// starts again, so it doesn't go dark at once.
func TestReturningToTheVTWakesABlankedScreen(t *testing.T) {
	d := testDriver(0, 1)
	d.blank = newBlanker(Blank{Dim: time.Minute, Off: 5 * time.Minute}, 1, bt0)
	d.blank.step(after(5 * time.Minute))
	d.blank.step(after(6 * time.Minute))
	w, _ := d.NewWindow(driver.Options{})
	win := w.(*Window)
	defer func() { _ = win.Close() }()
	r := &renderThread{d: d, off: true}
	r.left()
	if ev, ok := nextInput(t, win).(driver.WindowShown); !ok || ev.Shown {
		t.Fatalf("leaving, the window heard %v, not that it is hidden", ev)
	}
	now := after(10 * time.Minute)
	r.returned(now)
	if r.off || r.away || r.set || d.away {
		t.Fatalf("off %v, away %v, set %v, driver away %v; want the screen taken back and set again", r.off, r.away, r.set, d.away)
	}
	if d.blank.state != screenOn || !d.blank.last.Equal(now) {
		t.Fatalf("the screen is %v, last woken %v; want on, from now", d.blank.state, d.blank.last.Sub(bt0))
	}
	if at := d.blank.next(now.Add(wakeFade)); !at.Equal(now.Add(time.Minute)) {
		t.Fatalf("dims again at %v, want a minute after the return", at.Sub(now))
	}
	if ev, ok := nextInput(t, win).(driver.WindowShown); !ok || !ev.Shown {
		t.Fatalf("the window heard %v, not that it is shown", ev)
	}
	if !d.Showing() {
		t.Fatal("the screen taken back doesn't count as showing")
	}
}

// Showing is false while the screen is off and while another VT has it,
// and true otherwise, dimmed too.
func TestShowingFollowsTheScreenAndTheVT(t *testing.T) {
	d := testDriver(0, 1)
	d.blank = newBlanker(Blank{Dim: time.Minute, Off: 5 * time.Minute}, 1, bt0)
	if !d.Showing() {
		t.Fatal("a screen just opened doesn't count as showing")
	}
	d.blank.step(after(2 * time.Minute))
	if !d.Showing() {
		t.Fatal("a dimmed screen doesn't count as showing")
	}
	d.blank.step(after(5 * time.Minute))
	d.blank.step(after(6 * time.Minute))
	if d.blank.state != screenOff || d.Showing() {
		t.Fatalf("the screen is %v and showing %v; want off, not showing", d.blank.state, d.Showing())
	}
	r := &renderThread{d: d, off: true}
	r.returned(after(7 * time.Minute))
	if !d.Showing() {
		t.Fatal("a woken screen doesn't count as showing")
	}
	d.mu.Lock()
	d.away = true
	d.mu.Unlock()
	if d.Showing() {
		t.Fatal("a screen another VT has counts as showing")
	}
}

// A backlight step whose write failed is asked again: the step counts as
// lit only once the backlight's goroutine has written it.
func TestAFailedBacklightStepIsAskedAgain(t *testing.T) {
	d := testDriver(0, 1)
	d.light = &backlight{dir: t.TempDir(), max: 31, cur: 15}
	d.lightTo = make(chan int, 1)
	d.lit.Store(15)
	r := &renderThread{d: d, lit: 15}
	asked := func() int {
		select {
		case n := <-d.lightTo:
			return n
		default:
			return -1
		}
	}
	r.light(15)
	if n := asked(); n != -1 {
		t.Fatalf("asked for %d, the step the backlight is at", n)
	}
	r.light(0)
	if n := asked(); n != 0 {
		t.Fatalf("asked for %d, want 0", n)
	}
	// The write failed: the backlight is still at 15.
	r.light(0)
	if n := asked(); n != 0 {
		t.Fatalf("after a failed write, asked for %d, want 0 again", n)
	}
	d.lit.Store(0) // written
	r.light(0)
	if n := asked(); n != -1 {
		t.Fatalf("asked for %d once written", n)
	}
}

// shown returns the window's next WindowShown, failing on any other input.
func shown(t *testing.T, w *Window) bool {
	t.Helper()
	ev, ok := nextInput(t, w).(driver.WindowShown)
	if !ok {
		t.Fatalf("the window heard %v, not whether it is shown", ev)
	}
	return ev.Shown
}

// quiet fails if the window hears anything.
func quiet(t *testing.T, w *Window, what string) {
	t.Helper()
	if ev, ok := noInput(w); !ok {
		t.Fatalf("%s, the window heard %v", what, ev)
	}
}

// The screen turning off tells the windows they are hidden, and waking
// tells them they are shown, with a frame drawn whole: once each.
func TestPowerOffAndOnTellTheWindowsOnce(t *testing.T) {
	d := testDriver(0, 1)
	d.scr, _ = testScreen()
	d.blank = newBlanker(Blank{Dim: time.Minute, Off: 5 * time.Minute}, 1, bt0)
	w, _ := d.NewWindow(driver.Options{})
	win := w.(*Window)
	defer func() { _ = win.Close() }()
	r := &renderThread{d: d}
	d.blank.step(after(5 * time.Minute))
	d.blank.step(after(6 * time.Minute))
	r.powerOff()
	if shown(t, win) || d.Showing() {
		t.Fatal("the screen off counts as shown")
	}
	quiet(t, win, "after the screen went off")
	d.blank.wake(after(7 * time.Minute))
	r.powerOn()
	if !shown(t, win) || !d.Showing() {
		t.Fatal("the screen woken counts as hidden")
	}
	if _, ok := nextInput(t, win).(driver.Redraw); !ok {
		t.Fatal("waking draws no frame whole")
	}
	quiet(t, win, "after the screen woke")
}

// Leaving for another VT while the screen is off tells the windows nothing
// more: they heard they are hidden as it went off.
func TestLeavingWhileOffTellsNoSecondHide(t *testing.T) {
	d := testDriver(0, 1)
	d.scr, _ = testScreen()
	d.blank = newBlanker(Blank{Dim: time.Minute, Off: 5 * time.Minute}, 1, bt0)
	w, _ := d.NewWindow(driver.Options{})
	win := w.(*Window)
	defer func() { _ = win.Close() }()
	r := &renderThread{d: d}
	d.blank.step(after(5 * time.Minute))
	d.blank.step(after(6 * time.Minute))
	r.powerOff()
	if shown(t, win) {
		t.Fatal("the screen off counts as shown")
	}
	r.left()
	quiet(t, win, "leaving with the screen off")
	r.returned(after(8 * time.Minute))
	if !shown(t, win) {
		t.Fatal("the return counts as hidden")
	}
}

// A wake while another VT shows, as Wake, KeepAwake or SetBlank ask,
// tells the windows nothing, and Showing stays false: the screen is the
// other VT's until it returns.
func TestAWakeWhileAwayTellsNothing(t *testing.T) {
	d := testDriver(0, 1)
	d.blank = newBlanker(Blank{Dim: time.Minute, Off: 5 * time.Minute}, 1, bt0)
	w, _ := d.NewWindow(driver.Options{})
	win := w.(*Window)
	defer func() { _ = win.Close() }()
	r := &renderThread{d: d, off: true}
	r.left()
	if shown(t, win) {
		t.Fatal("leaving counts as shown")
	}
	d.blank.wake(after(time.Minute))
	r.powerOn()
	quiet(t, win, "woken while away")
	if d.Showing() {
		t.Fatal("a screen another VT has counts as showing once woken")
	}
}

// A window that opens while the screen doesn't show hears so at once.
func TestAWindowOpenedWhileHiddenHearsItIsHidden(t *testing.T) {
	d := testDriver(0, 1)
	r := &renderThread{d: d}
	r.left()
	w, _ := d.NewWindow(driver.Options{})
	win := w.(*Window)
	defer func() { _ = win.Close() }()
	if shown(t, win) {
		t.Fatal("a window opened while away counts as shown")
	}
	r.returned(after(time.Minute))
	if !shown(t, win) {
		t.Fatal("a window opened while away doesn't hear the return")
	}
}

// The kernel switching back to the driver's VT turns Showing true at once,
// even where the render thread hangs and never takes the screen back: a
// watchdog that asks Showing then sees the hang.
func TestShowingTurnsTrueAsTheKernelSwitchesBack(t *testing.T) {
	d := testDriver(0, 1)
	d.done = make(chan struct{})
	d.vt = &vt{signals: make(chan os.Signal, 1)}
	r := &renderThread{d: d}
	r.left()
	if d.Showing() {
		t.Fatal("away counts as showing")
	}
	go d.watchVT()
	defer close(d.quit)
	defer close(d.done)
	// No render thread takes the switch: it hangs.
	d.vt.signals <- syscall.SIGUSR2
	deadline := time.Now().Add(time.Second)
	for !d.Showing() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !d.Showing() {
		t.Fatal("the switch back with the render thread hung leaves Showing false")
	}
}
