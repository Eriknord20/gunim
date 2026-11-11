package gunim

import (
	"context"
	"fmt"
	"math"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/driver"
)

// HeldRate is how many frames a second a [HeldClock] draws as it advances, unless [HoldOptions.Rate] says otherwise.
const HeldRate = 120

// HoldOptions says how a [HeldClock] keeps time.
type HoldOptions struct {
	// Rate is how many frames a second Advance draws. Zero is [HeldRate].
	Rate float64
	// Start is the time the clock holds at first. Zero is the time now.
	Start time.Time
	// Sync runs before each frame Advance draws, on the goroutine calling Advance, with the frame's length. An
	// application that answers its windows on a goroutine of its own waits there until it has answered what they
	// sent, so each frame finds the commands it would have found however slowly the frames are drawn. An error
	// stops the Advance.
	Sync func(ctx context.Context, d time.Duration) error
	// PaintEvery is how often an app's window paints a frame of an Advance and hands it to the system: every
	// PaintEvery-th frame, and the last. The frames between step time and lay the windows and their popups out,
	// unpainted, so a long Advance costs a few frames of drawing, as on a slow machine live, and its last frame
	// shows where it led. Zero is HeldPaintEvery; 1 paints every frame. The frames painted are the same each run.
	PaintEvery int
}

// HeldPaintEvery is how often an app's window paints a frame of an Advance, unless [HoldOptions.PaintEvery] says
// otherwise: 15 frames a second of held time at HeldRate.
const HeldPaintEvery = 8

// A HeldClock is time that moves only when it is told to. Every window that keeps time by it, and every popup,
// menu, submenu and tooltip those windows show, steps by it alone: springs, timers, a caret's blink, scroll
// inertia, a drag's edge scroll and the times on input events. A slow machine then draws the same frames as a
// fast one, and a picture taken part way through a motion shows the same moment each run.
//
// An [App] holds its windows' time with [App.Hold]. A test that drives windows from [NewOffscreen] makes one with
// [NewHeldClock] and joins them with [HeldClock.Join].
//
// What follows another clock does not stop. [Frame.Present] is the held time, which the audio device's clock has
// long passed, so a level meter or a playhead drawn from the sound as it is heard shows it as it is in real time,
// changing from frame to frame however the clock stands. Work an application does on goroutines of its own, and
// its own timers, keep the wall clock too.
type HeldClock struct {
	rate  float64
	sync  func(context.Context, time.Duration) error
	every int
	// app is the application whose windows keep time by the clock, or nil.
	app *App
	// advancing lets one Advance run at a time.
	advancing sync.Mutex

	mu  sync.Mutex
	now time.Time
	// moved says the clock has advanced.
	moved bool
	// joined are the windows from NewOffscreen that keep time by the clock, which Advance steps itself.
	joined []*Window
}

// NewHeldClock makes a clock held at o.Start, for windows from [NewOffscreen] to join.
func NewHeldClock(o HoldOptions) *HeldClock {
	c := &HeldClock{rate: o.Rate, sync: o.Sync, now: o.Start, every: o.PaintEvery}
	if c.every <= 0 {
		c.every = HeldPaintEvery
	}
	if c.rate <= 0 {
		c.rate = HeldRate
	}
	if c.now.IsZero() {
		c.now = time.Now()
	}
	return c
}

// Now is the clock's time.
func (c *HeldClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Join has w, a window from [NewOffscreen], keep time by the clock from now on: its time is set to the clock's,
// and Advance steps it, frame by frame, with the clock's other windows. A clock that has not moved yet first moves
// on to the window's time where that is later, with the windows that joined it before, so that time never runs
// back. Once it has joined, step the window only through the clock. It must be called from the goroutine that
// will call Advance, and may be called from Sync, for a window opened part way.
func (c *HeldClock) Join(w *Window) {
	if w.app != nil {
		panic("gunim: HeldClock.Join takes a window from NewOffscreen; an App's windows join with App.Hold")
	}
	c.mu.Lock()
	var moved []*Window
	if !c.moved && w.clock.After(c.now) {
		c.now = w.clock
		moved = append(moved, c.joined...)
	}
	now := c.now
	c.joined = append(c.joined, w)
	c.mu.Unlock()
	for _, o := range append(moved, w) {
		o.setHeldTime(c, now)
	}
}

// setHeldTime has w, a window from NewOffscreen, keep c's time, which stands at now.
func (w *Window) setHeldTime(c *HeldClock, now time.Time) {
	defer w.drive()()
	w.held.Store(c)
	w.holdUI(now)
	w.clock = now
}

// Step draws one frame of exactly d in every window of the clock, with no frames between and none cut at a timer:
// for a test that steps time in coarse frames, as a tenth of a second at a time to settle. Sync runs first, as
// for a frame of Advance.
func (c *HeldClock) Step(ctx context.Context, d time.Duration) error {
	if d < 0 {
		return fmt.Errorf("gunim: HeldClock.Step: %v is before now", d)
	}
	c.advancing.Lock()
	defer c.advancing.Unlock()
	if c.sync != nil {
		if err := c.sync(ctx, d); err != nil {
			return err
		}
	}
	_, err := c.stepAll(ctx, c.Now().Add(d), d, true)
	return err
}

// Frames advances the clock by n frames of the clock's rate; see [HeldClock.Advance].
func (c *HeldClock) Frames(ctx context.Context, n int) error {
	return c.Advance(ctx, c.span(n))
}

// Advance moves the clock on by d, in frames of the clock's rate, the last cut short to land on d. Every window
// of the clock draws each frame, with its popups. A timer that comes due part way through a frame ends the frame
// there, so it runs at exactly its time: a submenu that closes 300 ms after the pointer left closes at 300 ms. A
// window opened during the Advance joins it at the clock's time.
//
// Advance waits for each frame of each window, however long the frames take to draw, or for ctx to end. One
// Advance runs at a time.
func (c *HeldClock) Advance(ctx context.Context, d time.Duration) error {
	if d < 0 {
		return fmt.Errorf("gunim: HeldClock.Advance: %v is before now", d)
	}
	c.advancing.Lock()
	defer c.advancing.Unlock()
	start := c.Now()
	end := start.Add(d)
	timer, err := c.stepAll(ctx, time.Time{}, -1, false)
	if err != nil {
		return err
	}
	k, drawn := 1, 0
	for now := start; now.Before(end); {
		at := start.Add(c.span(k))
		for !at.After(now) {
			// A frame cut at a timer ended past this point.
			k++
			at = start.Add(c.span(k))
		}
		at = earliest(at, end)
		if timer.After(now) {
			at = earliest(at, timer)
		}
		if c.sync != nil {
			if err := c.sync(ctx, at.Sub(now)); err != nil {
				return err
			}
		}
		drawn++
		if timer, err = c.stepAll(ctx, at, at.Sub(now), !at.Before(end) || drawn%c.every == 0); err != nil {
			return err
		}
		now = at
	}
	return nil
}

// span is how long n frames at the clock's rate take, counted whole so that 36 frames at 120 a second are
// exactly 300 ms.
func (c *HeldClock) span(n int) time.Duration {
	return time.Duration(math.Round(float64(n) * float64(time.Second) / c.rate))
}

// earliest is the earlier of a and b.
func earliest(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// heldStep asks a window's loop for a frame at now, delta after the one before; a negative delta only asks when
// the window's next timer is due. The loop answers on done with that time, or zero where it has none.
type heldStep struct {
	now   time.Time
	delta time.Duration
	// paint says the frame is painted and handed to the system, not only laid out.
	paint bool
	done  chan time.Time
}

// stepAll has every window of the clock draw the frame at now, and returns when the earliest of their timers is
// due, zero for none. A negative delta draws nothing and only asks.
func (c *HeldClock) stepAll(ctx context.Context, now time.Time, delta time.Duration, paint bool) (time.Time, error) {
	var next time.Time
	soonest := func(t time.Time) {
		if !t.IsZero() && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	c.mu.Lock()
	joined := append([]*Window(nil), c.joined...)
	c.mu.Unlock()
	for _, w := range joined {
		select {
		case <-w.done:
			continue
		default:
		}
		soonest(w.joinedFrame(now, delta))
	}
	if c.app == nil {
		c.moveTo(now, delta)
		return next, nil
	}
	// The app's windows draw the frame side by side, each on its own goroutine, as they do live.
	var asked []heldStep
	var wins []*Window
	for _, w := range c.app.windows.all() {
		if w.held.Load() != c {
			continue
		}
		st := heldStep{now: now, delta: delta, paint: paint, done: make(chan time.Time, 1)}
		select {
		case w.steps <- st:
			asked, wins = append(asked, st), append(wins, w)
		case <-w.done:
		case <-ctx.Done():
			return next, ctx.Err()
		}
	}
	for i, st := range asked {
		select {
		case t := <-st.done:
			soonest(t)
		case <-wins[i].done:
		case <-ctx.Done():
			return next, ctx.Err()
		}
	}
	c.moveTo(now, delta)
	return next, nil
}

// moveTo moves the clock to now once every window has drawn the frame there; a negative delta drew none.
func (c *HeldClock) moveTo(now time.Time, delta time.Duration) {
	if delta < 0 {
		return
	}
	c.mu.Lock()
	c.now, c.moved = now, true
	c.mu.Unlock()
}

// joinedFrame draws a frame of a window from NewOffscreen that joined a clock, at now, delta after the one before,
// and returns when its next timer is due. A negative delta only asks that.
func (w *Window) joinedFrame(now time.Time, delta time.Duration) time.Time {
	if delta >= 0 {
		w.frameAt(now, delta)
	}
	defer w.drive()()
	next := w.ui.nextHeldTimer()
	return next
}

// Hold stops the clock of every window of the app, and of each window it opens from now on, and returns the clock
// they keep time by; see [HeldClock]. Time then moves only through [HeldClock.Advance]. A window draws a frame of
// its own only to show for the first time, and for [Client.Shot], which shows the moment the clock stands at.
// Input given through [Client.Input] or from the system is stamped with the clock's time.
//
// What was moving as the clock stopped goes on from where it was at the next Advance. [App.Release] lets time run
// again.
//
// A Hold while the clock is held lets the old clock go, as Release does, and holds the windows to the new one, at
// its time.
func (a *App) Hold(o HoldOptions) *HeldClock {
	a.Release()
	c := NewHeldClock(o)
	c.app = a
	a.windows.hold(&a.held, c)
	return c
}

// Release lets the app's windows keep time by the wall clock again, as they did before [App.Hold]. The time each
// window keeps runs on unbroken: a timer set while held runs as long after Release as it had left to run. Release
// waits for an Advance under way, so it must not be called from [HoldOptions.Sync].
func (a *App) Release() {
	if c := a.held.Load(); c != nil {
		// After the Advance under way, if any.
		c.advancing.Lock()
		defer c.advancing.Unlock()
	}
	a.windows.hold(&a.held, nil)
}

// Held returns the clock the app's windows keep time by, or nil while they keep the wall clock's.
func (a *App) Held() *HeldClock { return a.held.Load() }

// hold has the app's clock, held, be c, nil for the wall clock's, and every window keep time by it.
func (ws *windows) hold(held *atomic.Pointer[HeldClock], c *HeldClock) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	held.Store(c)
	for _, w := range ws.list {
		w.held.Store(c)
		w.nudge()
	}
}

// addHeld adds w to the open windows, keeping time by the app's clock, held: under the lock hold takes, so a Hold
// or a Release can't fall between the two.
func (ws *windows) addHeld(w *Window, held *atomic.Pointer[HeldClock]) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.list = append(ws.list, w)
	w.held.Store(held.Load())
}

// all returns the open windows.
func (ws *windows) all() []*Window {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return append([]*Window(nil), ws.list...)
}

// nudge wakes the window's loop.
func (w *Window) nudge() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// holdUI sets the window's UI to keep the held time, now, keeping how it kept time before for when it is let go.
// The window's time moves to now unbroken: what it waits for, waits as long as it had left.
func (w *Window) holdUI(now time.Time) {
	u := w.ui
	if !u.onHeld {
		u.wasStepped, u.wasByFrames = u.stepped, u.byFrames
	}
	u.onHeld, u.stepped, u.byFrames, u.everHeld = true, true, true, true
	u.moveTime(now)
	if u.arriving && u.arrivedAt.After(now) {
		// Shown by the wall clock ahead of the held time: the window arrives from now.
		u.arrivedAt = now
	}
}

// moveTime moves the window's time to now, and everything it timed with it: the timers, and when it began to
// arrive, leave and fade. A window that has drawn no frame has timed nothing yet.
func (u *UI) moveTime(now time.Time) {
	if !u.now.IsZero() {
		d := now.Sub(u.now)
		for _, t := range u.timers {
			t.at = t.at.Add(d)
		}
		for _, at := range []*time.Time{&u.wakeAt, &u.arrivedAt, &u.leftAt, &u.fadeAt} {
			if !at.IsZero() {
				*at = at.Add(d)
			}
		}
	}
	u.now = now
}

// followClock has the loop take up the clock the window keeps time by, held or the wall's, as the app changed it.
func (w *Window) followClock() {
	c := w.held.Load()
	if c == w.heldBy {
		return
	}
	w.heldBy = c
	u := w.ui
	if st := w.step; st != nil {
		// A frame of the clock let go.
		w.step = nil
		st.done <- time.Time{}
	}
	if c != nil {
		w.holdUI(c.Now())
		w.stampClocks()
		return
	}
	u.onHeld, u.stepped, u.byFrames = false, u.wasStepped, u.wasByFrames
	u.moveTime(time.Now())
	for _, t := range u.timers {
		t.held = false
	}
	w.stampClocks()
	// Back on the wall clock, the next frame steps a refresh, as after a sleep.
	w.resumed = true
	u.invalid = true
}

// stampClocks tells the window's driver windows, its own and its popups', the clock it now keeps, before the next
// frame: a held one, so they draw no frame of their own meanwhile, or the wall's.
func (w *Window) stampClocks() {
	w.ui.stampClock(w.dw)
	for _, s := range w.ui.popups {
		if s.dw != nil {
			w.ui.stampClock(s.dw)
		}
	}
}

// heldFrame draws the frame a held window owes, if any, and reports whether it drew or answered: the step Advance
// asked for, the window's first frame, or one for a shot. A frame of its own steps no time.
func (w *Window) heldFrame() bool {
	if st := w.step; st != nil && !w.draws() {
		// Out of sight, the window draws no frame and its time holds, as live: it answers at once, whatever frame
		// it handed last and the system has yet to show.
		w.step = nil
		st.done <- w.ui.nextHeldTimer()
		return true
	}
	if st := w.step; st != nil && !st.paint {
		// Laid out, unpainted: it waits for no frame to be shown.
		w.step = nil
		w.applyPending()
		w.ui.heldSkip = true
		w.ui.frame(st.now, st.now, st.delta)
		w.ui.heldSkip = false
		st.done <- w.ui.nextHeldTimer()
		return true
	}
	if !w.free() || w.waitsForPopups() {
		return false
	}
	if st := w.step; st != nil {
		w.step = nil
		w.applyPending()
		w.drawAt(st.now, st.delta)
		st.done <- w.ui.nextHeldTimer()
		return true
	}
	first := w.ui.seq == 0 && w.draws()
	// A shot waits for the next frame: one drawn as time stands shows it.
	shot := w.ui.shotsOwed.Load() > 0 && w.unshown == 0
	if !first && !shot {
		return false
	}
	if first {
		w.applyPending()
	}
	w.drawAt(w.ui.now, 0)
	return true
}

// popupWaitMost is the longest a held frame waits for a popup to show the frame before. A popup's window that
// never shows it, as one the system keeps hidden, then skips frames, as it would live.
const popupWaitMost = 250 * time.Millisecond

// waitsForPopups reports whether the frame Advance asked for waits for a popup to show the frame before: one still
// on its way would skip this one, and what the popup draws would then hang on how fast its window is. It waits
// popupWaitMost at most.
func (w *Window) waitsForPopups() bool {
	if w.step == nil || !w.ui.popupsInFlight() {
		w.popupWait = time.Time{}
		return false
	}
	if w.popupWait.IsZero() {
		w.popupWait = time.Now()
	}
	if time.Since(w.popupWait) < popupWaitMost {
		return true
	}
	if popupDebug {
		fmt.Fprintf(os.Stderr, "gunim popup: a held frame waited %v for a popup to show the frame before, and goes on\n", popupWaitMost)
	}
	for _, s := range w.ui.popups {
		s.unshown = s.unshown || s.inFlight
	}
	w.popupWait = time.Time{}
	return false
}

// drawAt builds the frame at the held time now, delta after the frame before, and hands it to the driver.
func (w *Window) drawAt(now time.Time, delta time.Duration) {
	// Held, the frame's time says nothing of when it reaches the screen: it teaches presentAt nothing.
	w.due = now
	start := time.Now()
	w.ui.frame(now, now, delta)
	w.sent = time.Now()
	if uiFramesDebug {
		w.uiTimes.add(w, 0, w.sent.Sub(start), 0)
	}
	w.unshown++
	w.unreleased++
}

// heldAsked takes a step Advance asks of the loop: a question about the next timer is answered at once, and a
// frame waits for the loop to be free to draw it.
func (w *Window) heldAsked(st heldStep) {
	if st.delta < 0 {
		next := w.ui.nextHeldTimer()
		st.done <- next
		return
	}
	if old := w.step; old != nil {
		// Left by an Advance that gave up waiting: the newer frame takes its place.
		old.done <- time.Time{}
	}
	w.step = &st
}

// heldTime stamps ev, an input event, with the held time, in a window that keeps one.
func (u *UI) heldTime(ev any) any {
	if !u.onHeld || ev == nil {
		return ev
	}
	v := reflect.ValueOf(ev)
	if v.Kind() != reflect.Struct {
		return ev
	}
	f := v.FieldByName("Time")
	if !f.IsValid() || f.Type() != reflect.TypeFor[time.Time]() {
		return ev
	}
	c := reflect.New(v.Type()).Elem()
	c.Set(v)
	c.FieldByName("Time").Set(reflect.ValueOf(u.now))
	return c.Interface()
}

// nextHeldTimer returns when the earliest timer set under the held clock is due, or zero for none. A timer set
// before the clock was held is due at a time the wall clock gave, which falls anywhere between two frames; it runs
// in the frame after its time, as it would live, and cuts no frame short, so the frames Advance draws are the same
// each run.
func (u *UI) nextHeldTimer() time.Time {
	var at time.Time
	for _, t := range u.timers {
		if t.held && (at.IsZero() || t.at.Before(at)) {
			at = t.at
		}
	}
	return at
}

// stampClock tells dw, a driver's window about to be handed a frame, the frame's held time while the window keeps a
// held clock's, and that it keeps the wall clock's again once it no longer does; see [driver.ClockHolder].
func (u *UI) stampClock(dw driver.Window) {
	if !u.everHeld {
		return
	}
	if h, ok := dw.(driver.ClockHolder); ok {
		at := u.now
		if !u.onHeld {
			at = time.Time{}
		}
		h.HoldClock(at)
	}
}

// popupsInFlight reports whether a popup's window has yet to show the frame it was handed last.
func (u *UI) popupsInFlight() bool {
	for _, s := range u.popups {
		if s.dw != nil && s.inFlight && !s.unshown {
			return true
		}
	}
	return false
}

// WallTime returns the wall clock's time for t, a time on the window's clock, as an input event's. Live, that is
// t. While the window keeps a held clock's time, it is the wall clock's time now, less how long before the held
// time now t is: an event stamped with the held time happened now. It is for what meets the sound, which keeps the
// wall clock: a key played on the computer's keys is heard as it was pressed, held clock or not. A zero t stays
// zero.
func (u *UI) WallTime(t time.Time) time.Time {
	if u == nil || !u.onHeld || t.IsZero() {
		return t
	}
	return time.Now().Add(t.Sub(u.now))
}
