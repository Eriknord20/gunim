package gunim

import (
	"context"
	"errors"
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// slowDriver opens offscreen windows that report each frame shown slow after it was handed over, as a busy
// machine's display does.
type slowDriver struct {
	slow time.Duration
	// made is told each window opened.
	made chan *slowWindow
}

func (d *slowDriver) Run(ctx context.Context, ready func()) error {
	ready()
	<-ctx.Done()
	return nil
}

func (d *slowDriver) NewWindow(o driver.Options) (driver.Window, error) {
	size := o.Size
	if size == (geom.Size{}) {
		size = geom.Sz(200, 200)
	}
	w := &slowWindow{OffscreenWindow: driver.Offscreen(size), slow: d.slow, closed: make(chan struct{})}
	if d.made != nil {
		d.made <- w
	}
	return w, nil
}

func (d *slowDriver) Monitors() []driver.Monitor { return nil }

type slowWindow struct {
	*driver.OffscreenWindow
	slow   time.Duration
	closed chan struct{}
}

func (w *slowWindow) Present(ops []paint.Op, damage []geom.Rect) error {
	err := w.OffscreenWindow.Present(ops, damage)
	go func() {
		select {
		case <-time.After(w.slow):
		case <-w.closed:
			return
		}
		shown := make(chan struct{})
		go func() {
			w.TickWith(driver.Frame{Shown: time.Now()})
			close(shown)
		}()
		select {
		case <-shown:
		case <-w.closed:
		}
	}()
	return err
}

func (w *slowWindow) Close() error {
	select {
	case <-w.closed:
	default:
		close(w.closed)
	}
	return w.OffscreenWindow.Close()
}

// glider is a node whose spring heads for 100 from the press that reaches it, and which opens a popup holding
// another glider that heads there from the same moment.
type glider struct {
	v       *anim.Float
	pressed time.Time
	popup   *glider
	opens   bool
	// after, where set, starts a timer at the press, and ranAt is when it ran. ran, where set, is told the wall
	// clock's time as it runs.
	after time.Duration
	ranAt time.Time
	ran   chan time.Time
	// presses, where set, is told of each press.
	presses chan struct{}
}

func newGlider() *glider { return &glider{v: anim.NewFloat(0)} }

func (g *glider) Handle(e input.Event, u *UI) bool {
	d, ok := e.(input.PointerDown)
	if !ok {
		return false
	}
	g.pressed = d.Time
	if g.presses != nil {
		g.presses <- struct{}{}
	}
	g.v.Animate(100, anim.Snappy)
	if g.after > 0 {
		u.After(g.after, func(u *UI) {
			g.ranAt = u.Now()
			if g.ran != nil {
				g.ran <- time.Now()
			}
		})
	}
	if g.opens {
		g.popup = newGlider()
		g.popup.v.Animate(100, anim.Snappy)
		u.OpenPopup(g, g.popup, PopupOptions{Anchor: geom.Rect{Max: geom.Pt(20, 20)}})
	}
	u.Invalidate()
	return true
}

// heldRoot is a window's root holding kid, which it puts in the tree at its first layout: the root takes no pointer.
type heldRoot struct {
	kid    Node
	placed bool
}

func (h *heldRoot) Layout(c Constraints, f Frame, kids Children) geom.Size {
	if !h.placed {
		h.placed = true
		f.UI().Insert(h, h.kid)
		f.UI().Invalidate()
	}
	for k := range kids.All {
		k.Layout(Loose(c.Max))
	}
	return c.Max
}

func (h *heldRoot) Paint(p *paint.Painter, _ Frame, _ geom.Size, kids Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

func (g *glider) Step(dt time.Duration) bool { return g.v.Step(dt) }

func (g *glider) Layout(c Constraints, _ Frame, _ Children) geom.Size {
	return c.Constrain(geom.Sz(50, 50))
}

func (g *glider) Paint(p *paint.Painter, _ Frame, _ geom.Size, _ Children) {
	p.RRect(geom.Rect{Max: geom.Pt(50, 50)}, 0, paint.Solid(color.NRGBA{A: 255}))
	p.RRect(geom.Rect{Max: geom.Pt(g.v.Value()/2, 10)}, 0, paint.Solid(color.NRGBA{R: 255, A: 255}))
}

// heldRun opens a window on a driver whose frames show slow after they are drawn, holds the app's clock, presses
// the window's glider, and returns the glider and the clock after fn has advanced it.
func heldRun(t *testing.T, slow time.Duration, fn func(c *HeldClock) error, set ...func(*glider)) (*glider, *HeldClock, time.Time) {
	t.Helper()
	g := newGlider()
	g.opens = true
	for _, f := range set {
		f(g)
	}
	var c *HeldClock
	var pressedAt time.Time
	start := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := runApp(ctx, &slowDriver{slow: slow}, func(a *App) error {
		w, err := a.NewWindow(WindowOptions{Size: geom.Sz(200, 200), Root: &heldRoot{kid: g}, Instant: true})
		if err != nil {
			return err
		}
		defer w.Close()
		c = a.Hold(HoldOptions{Start: start})
		// The first frames put the glider in and lay it out, so the press finds it.
		if err := c.Frames(ctx, 2); err != nil {
			return err
		}
		pressedAt = c.Now()
		if err := w.Client().Input(ctx, input.PointerMove{Pos: geom.Pt(10, 10), Time: time.Now()}); err != nil {
			return err
		}
		if err := w.Client().Input(ctx, input.PointerDown{Pos: geom.Pt(10, 10), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()}); err != nil {
			return err
		}
		return fn(c)
	})
	if err != nil {
		t.Fatal(err)
	}
	return g, c, pressedAt
}

// A spring under a held clock stands at the same value after Advance(60ms) however slowly its frames are drawn
// and shown, and a popup opened from it moves on the same clock.
func TestAHeldSpringLandsAlikeHoweverSlowTheFrames(t *testing.T) {
	t.Parallel()
	advance := func(c *HeldClock) error { return c.Advance(context.Background(), 60*time.Millisecond) }
	fast, c, pressed := heldRun(t, 0, advance)
	slow, _, _ := heldRun(t, 15*time.Millisecond, advance)
	v := fast.v.Value()
	if v <= 0 || v >= 100 {
		t.Fatalf("60 ms into a snappy glide to 100 it stands at %v, not part way", v)
	}
	if got := slow.v.Value(); got != v {
		t.Fatalf("with each frame shown 15 ms late the spring stands at %v after 60 ms, and at %v with frames shown at once", got, v)
	}
	if fast.popup == nil || slow.popup == nil {
		t.Fatal("the press opened no popup")
	}
	if got := fast.popup.v.Value(); got != v {
		t.Fatalf("the popup's spring stands at %v and its window's at %v: they keep different clocks", got, v)
	}
	if got := slow.popup.v.Value(); got != v {
		t.Fatalf("with frames shown late, the popup's spring stands at %v and its window's at %v", got, v)
	}
	if !fast.pressed.Equal(pressed) {
		t.Fatalf("the press was stamped %v, not the held time %v", fast.pressed, pressed)
	}
	if got, want := c.Now().Sub(pressed), 60*time.Millisecond; got != want {
		t.Fatalf("the clock moved %v, want %v", got, want)
	}
}

// A timer under a held clock runs at exactly its time, inside a frame or on its edge.
func TestAHeldTimerRunsAtItsTime(t *testing.T) {
	t.Parallel()
	for _, after := range []time.Duration{7 * time.Millisecond, 300 * time.Millisecond} {
		var g *glider
		g, _, pressed := heldRun(t, 0, func(c *HeldClock) error {
			ctx := context.Background()
			if err := c.Advance(ctx, after-time.Millisecond); err != nil {
				return err
			}
			if !g.ranAt.IsZero() {
				t.Errorf("a timer of %v ran a millisecond early", after)
			}
			return c.Advance(ctx, 5*time.Millisecond)
		}, func(gl *glider) { g = gl; gl.after = after })
		if want := pressed.Add(after); !g.ranAt.Equal(want) {
			t.Errorf("a timer of %v ran %v after the press", after, g.ranAt.Sub(pressed))
		}
	}
}

// An Advance ends when a window's loop ends on its own part way through a frame, as when the user closes a window
// that has nothing to ask, and does not wait for a frame the window will never draw.
func TestAnAdvanceEndsWhenAWindowGoes(t *testing.T) {
	t.Parallel()
	d := &slowDriver{slow: 400 * time.Millisecond, made: make(chan *slowWindow, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := runApp(ctx, d, func(a *App) error {
		if _, err := a.NewWindow(WindowOptions{Size: geom.Sz(200, 200), Root: &heldRoot{kid: newGlider()}, Instant: true}); err != nil {
			return err
		}
		dw := <-d.made
		c := a.Hold(HoldOptions{})
		go func() {
			time.Sleep(50 * time.Millisecond)
			dw.Post(driver.CloseAsked{})
		}()
		start := time.Now()
		actx, acancel := context.WithTimeout(ctx, 5*time.Second)
		defer acancel()
		if err := c.Advance(actx, 100*time.Millisecond); err != nil {
			return err
		}
		if took := time.Since(start); took > 3*time.Second {
			t.Errorf("the Advance took %v", took)
		}
		a.Release()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// What a window times runs on unbroken as its clock is held and let go: a timer set before the hold runs while
// held, at its time, however far the held clock starts from the wall's, and a timer set while held runs after
// Release as long after as it had left.
func TestTimersRunOnAcrossHoldAndRelease(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	press := func(w *Window) error {
		c := w.Client()
		if err := c.Input(ctx, input.PointerMove{Pos: geom.Pt(10, 10)}); err != nil {
			return err
		}
		return c.Input(ctx, input.PointerDown{Pos: geom.Pt(10, 10), Button: input.ButtonPrimary, Clicks: 1})
	}
	err := runApp(ctx, &slowDriver{}, func(a *App) error {
		g := newGlider()
		g.after, g.ran, g.presses = 300*time.Millisecond, make(chan time.Time, 1), make(chan struct{}, 1)
		w, err := a.NewWindow(WindowOptions{Size: geom.Sz(200, 200), Root: &heldRoot{kid: g}, Instant: true})
		if err != nil {
			return err
		}
		defer w.Close()
		// Two frames lay the glider out, then the clock runs live.
		if err := a.Hold(HoldOptions{}).Frames(ctx, 2); err != nil {
			return err
		}
		a.Release()
		if err := press(w); err != nil {
			return err
		}
		select {
		case <-g.presses:
		case <-time.After(2 * time.Second):
			return errors.New("the live press did not reach the glider")
		}
		// Held an hour back from the wall clock: the timer set live still runs 300 ms on.
		c := a.Hold(HoldOptions{Start: time.Now().Add(-time.Hour)})
		if err := c.Advance(ctx, 250*time.Millisecond); err != nil {
			return err
		}
		select {
		case <-g.ran:
			t.Error("a timer set live ran early under the held clock")
		default:
		}
		if err := c.Advance(ctx, 100*time.Millisecond); err != nil {
			return err
		}
		select {
		case <-g.ran:
		default:
			t.Error("a timer set live never ran under a held clock that starts an hour back")
		}

		// Set while held, 100 ms run, then let go: 200 ms are left, on the wall clock.
		c = a.Hold(HoldOptions{Start: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)})
		if err := press(w); err != nil {
			return err
		}
		<-g.presses
		if err := c.Frames(ctx, 1); err != nil {
			return err
		}
		if err := c.Advance(ctx, 100*time.Millisecond-time.Second/HeldRate); err != nil {
			return err
		}
		released := time.Now()
		a.Release()
		select {
		case at := <-g.ran:
			if left := at.Sub(released); left < 150*time.Millisecond {
				t.Errorf("a timer with 200 ms left ran %v after Release", left)
			}
		case <-time.After(5 * time.Second):
			t.Error("a timer set while held never ran after Release")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// An event's held time maps to the wall clock's now, and a live window's times are wall times already.
func TestWallTimeMapsHeldTimeToNow(t *testing.T) {
	t.Parallel()
	w := newTestWindow()
	at := time.Now().Add(-time.Hour)
	if got := w.ui.WallTime(at); !got.Equal(at) {
		t.Fatalf("live, %v maps to %v", at, got)
	}
	c := NewHeldClock(HoldOptions{Start: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)})
	c.Join(w)
	before := time.Now()
	got := w.ui.WallTime(w.ui.Now().Add(-time.Second))
	if d := before.Sub(got); d < time.Second-50*time.Millisecond || d > time.Second+time.Second {
		t.Fatalf("held, a second before the held time maps to %v before the wall clock's now", d)
	}
	if !w.ui.WallTime(time.Time{}).IsZero() {
		t.Fatal("a zero time maps to a time")
	}
}
