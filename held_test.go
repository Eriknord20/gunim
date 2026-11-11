package gunim

import (
	"context"
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
	return &slowWindow{OffscreenWindow: driver.Offscreen(size), slow: d.slow, closed: make(chan struct{})}, nil
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
	// after, where set, starts a timer at the press, and ranAt is when it ran.
	after time.Duration
	ranAt time.Time
}

func newGlider() *glider { return &glider{v: anim.NewFloat(0)} }

func (g *glider) Handle(e input.Event, u *UI) bool {
	d, ok := e.(input.PointerDown)
	if !ok {
		return false
	}
	g.pressed = d.Time
	g.v.Animate(100, anim.Snappy)
	if g.after > 0 {
		u.After(g.after, func(u *UI) { g.ranAt = u.Now() })
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
