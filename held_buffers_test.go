package gunim

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// readingDriver opens windows that read a frame's ops for a while after they are handed them, as the desktop
// driver's render thread does, and tell when they let them go.
type readingDriver struct {
	reads time.Duration
	// first is the first window opened: the app's, before any spare the engine keeps for a popup.
	mu    sync.Mutex
	first *readingWindow
}

func (d *readingDriver) Run(ctx context.Context, ready func()) error {
	ready()
	<-ctx.Done()
	return nil
}

func (d *readingDriver) NewWindow(o driver.Options) (driver.Window, error) {
	w := &readingWindow{
		OffscreenWindow: driver.Offscreen(o.Size),
		reads:           d.reads,
		released:        make(chan struct{}, 2),
		handed:          make(chan *paint.Op, 16),
		closed:          make(chan struct{}),
	}
	go w.read()
	d.mu.Lock()
	if d.first == nil {
		d.first = w
	}
	d.mu.Unlock()
	return w, nil
}

func (d *readingDriver) Monitors() []driver.Monitor { return nil }

// readingWindow keeps each frame's ops for reads, one frame after another, then lets them go and shows the frame.
type readingWindow struct {
	*driver.OffscreenWindow
	reads    time.Duration
	released chan struct{}
	handed   chan *paint.Op
	closed   chan struct{}

	mu sync.Mutex
	// reading holds where the ops of each frame not yet let go start.
	reading []*paint.Op
	frames  int
	// clash tells of the first frame recorded into ops still read.
	clash string
}

func (w *readingWindow) Released() <-chan struct{} { return w.released }

func (w *readingWindow) Present(ops []paint.Op, damage []geom.Rect) error {
	if len(ops) == 0 {
		return errors.New("a frame of no ops")
	}
	at := &ops[0]
	w.mu.Lock()
	w.frames++
	for i, r := range w.reading {
		if r == at && w.clash == "" {
			w.clash = fmt.Sprintf("frame %d was recorded into the ops of frame %d, which the driver had not let go",
				w.frames, w.frames-len(w.reading)+i)
		}
	}
	w.reading = append(w.reading, at)
	w.mu.Unlock()
	select {
	case w.handed <- at:
	default:
		return fmt.Errorf("frame %d was handed with %d already waiting", w.frames, cap(w.handed))
	}
	return w.OffscreenWindow.Present(ops, damage)
}

// read is the render thread: it takes the frames in order, reads each for a while, lets it go, and shows it.
func (w *readingWindow) read() {
	for {
		select {
		case <-w.handed:
		case <-w.closed:
			return
		}
		select {
		case <-time.After(w.reads):
		case <-w.closed:
			return
		}
		w.mu.Lock()
		w.reading = w.reading[1:]
		w.mu.Unlock()
		select {
		case w.released <- struct{}{}:
		default:
		}
		shown := make(chan struct{})
		go func() {
			w.TickWith(driver.Frame{Shown: time.Now()})
			close(shown)
		}()
		select {
		case <-shown:
		case <-w.closed:
			return
		}
	}
}

func (w *readingWindow) Close() error {
	select {
	case <-w.closed:
	default:
		close(w.closed)
	}
	return w.OffscreenWindow.Close()
}

// frameTimer is a node that paints a square, and keeps a timer due in every frame of a clock held at 100 frames a
// second.
type frameTimer struct {
	armed bool
	ran   int
}

func (k *frameTimer) tick(u *UI) {
	k.ran++
	u.After(10*time.Millisecond, k.tick)
}

func (k *frameTimer) Layout(c Constraints, f Frame, _ Children) geom.Size {
	if !k.armed {
		k.armed = true
		f.UI().After(10*time.Millisecond, k.tick)
	}
	return c.Max
}

func (k *frameTimer) Paint(p *paint.Painter, _ Frame, size geom.Size, _ Children) {
	p.RRect(geom.Rect{Max: size.Point()}, 0, paint.Solid(WindowBackground.Get(nil)))
}

// A held clock's frame that only lays out is owed its painting, and a timer of the next frame has it painted
// first, to ask where things were drawn. The frame the timer runs in then paints too. Two frames painted where the
// window checked for room for one recorded the second into the ops of the frame before the owed one, while the
// render thread was still reading them: its replay then met a layer's end with no layer open, and panicked with
// "index out of range [-1]" in closeLayer.
func TestAHeldFrameNeverRecordsIntoOpsTheDriverStillReads(t *testing.T) {
	t.Parallel()
	drv := &readingDriver{reads: 30 * time.Millisecond}
	k := &frameTimer{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := runApp(ctx, drv, func(a *App) error {
		w, err := a.NewWindow(WindowOptions{Size: geom.Sz(200, 200), Root: k, Instant: true})
		if err != nil {
			return err
		}
		defer w.Close()
		c := a.Hold(HoldOptions{Rate: 100, PaintEvery: 2, Start: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)})
		// Each second frame paints, with a frame owed from the one before and a timer due.
		return c.Advance(ctx, 200*time.Millisecond)
	})
	if err != nil {
		t.Fatal(err)
	}
	if k.ran < 10 {
		t.Fatalf("the timer ran %d times in 20 frames: it was not due in each frame painted", k.ran)
	}
	dw := drv.first
	dw.mu.Lock()
	defer dw.mu.Unlock()
	if dw.frames < 5 {
		t.Fatalf("the window was handed %d frames, want the 10 painted or near it", dw.frames)
	}
	if dw.clash != "" {
		t.Fatal(dw.clash)
	}
}

// A clock let go with a frame owed, as after an Advance cut short on a frame that only laid out, owes it no more:
// the wall clock's next frame would paint it first, for a timer, with no room asked for, and then record itself
// into ops the driver still reads.
func TestAClockLetGoOwesNoFrame(t *testing.T) {
	t.Parallel()
	drv := &readingDriver{reads: 30 * time.Millisecond}
	k := &frameTimer{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := runApp(ctx, drv, func(a *App) error {
		w, err := a.NewWindow(WindowOptions{Size: geom.Sz(200, 200), Root: k, Instant: true})
		if err != nil {
			return err
		}
		defer w.Close()
		// The Advance ends before its fourth frame: the second painted, the third only laid out.
		short, stop := context.WithCancel(ctx)
		defer stop()
		frames := 0
		c := a.Hold(HoldOptions{Rate: 100, PaintEvery: 2, Start: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC),
			Sync: func(ctx context.Context, _ time.Duration) error {
				frames++
				if frames == 4 {
					stop()
				}
				return ctx.Err()
			}})
		if err := c.Advance(short, 200*time.Millisecond); err == nil {
			return errors.New("the Advance ran to its end")
		}
		a.Release()
		// The wall clock's frames, each with the timer due.
		time.Sleep(200 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	dw := drv.first
	dw.mu.Lock()
	defer dw.mu.Unlock()
	if dw.frames < 4 {
		t.Fatalf("the window was handed %d frames, want those of the wall clock after the held ones", dw.frames)
	}
	if dw.clash != "" {
		t.Fatal(dw.clash)
	}
}
