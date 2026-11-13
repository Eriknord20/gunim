//go:build linux && (arm64 || amd64)

package kmsdemo

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver/kms"
	"github.com/marrasen/gunim/widget"
)

// BenchOptions say how to run the bench.
type BenchOptions struct {
	// KMS opens the screen. Its Replay is the touch script; nil plays
	// Script.
	KMS kms.Options
	// Warm is how long the scene moves before the figures start, Moving
	// how long it is measured moving with the touch script playing, and
	// Rest how long it is measured still.
	Warm, Moving, Rest time.Duration
	// Still keeps the scene still while the touch script plays, so only
	// the fingers draw, as on a screen that mostly stands, where Moving
	// measures the scene moving on every frame. The frame rate is then
	// the touches', not the panel's.
	Still bool
	// Log, if set, hears what the bench does.
	Log func(string)
}

// Result is what the bench measured.
type Result struct {
	// Start is the time from the bench starting to the scene's first
	// frame on the screen.
	Start time.Duration
	// Rate is the screen's refresh rate.
	Rate float64
	// Moving is the figures while the scene moved, over MovingFor.
	Moving    kms.Stats
	MovingFor time.Duration
	// CPUMoving and CPURest are the process's CPU use, in percent of one
	// core, while the scene moved and while it was still. RestFrames is
	// the frames drawn while it was still.
	CPUMoving, CPURest float64
	RestFrames         int
	// RSS is the process's resident memory at the end, in bytes.
	RSS int64
	// Software says the CPU drew.
	Software bool
}

// Bench opens the screen, shows the scene, and measures it: moving with a
// touch script playing, then still.
func Bench(ctx context.Context, o BenchOptions) (Result, error) {
	logf := func(f string, args ...any) {
		if o.Log != nil {
			o.Log(fmt.Sprintf(f, args...))
		}
	}
	began := time.Now()
	var res Result
	gate := newGate(o.KMS.Replay)
	if gate.r == nil {
		gate.r = io.NopCloser(bytes.NewReader(Script(o.Moving)))
	}
	o.KMS.Replay = gate
	d, err := kms.Open(o.KMS)
	if err != nil {
		return res, err
	}
	res.Rate = d.Monitors()[0].RefreshRate
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var benchErr error
	err = gunim.MainWith(ctx, d, func(a *gunim.App) error {
		defer cancel()
		w, err := a.NewWindow(gunim.WindowOptions{Title: "kmsbench", Root: widget.NewSurface(), Theme: widget.Dark()})
		if err != nil {
			return err
		}
		Register(w, Options{Scale: d.Monitors()[0].Scale, Software: d.Software(), FrameMS: func() float64 { return float64(d.Stats().Last.Microseconds()) / 1000 }})
		c := w.Client()
		if err := c.TakeFingers(true); err != nil {
			return err
		}
		res.Software = d.Software()
		if err := c.Mount(gunim.Root, "demo", View, State{Still: o.Still}); err != nil {
			return err
		}
		go func() {
			for range c.Intents() {
			}
		}()
		// The first frame of the scene: the driver's own black one is the
		// first flip.
		for d.Stats().Frames < 2 {
			if !sleep(ctx, 5*time.Millisecond) {
				return ctx.Err()
			}
		}
		res.Start = time.Since(began)
		logf("first frame after %v; warming up for %v", res.Start.Round(time.Millisecond), o.Warm)
		if !sleep(ctx, o.Warm) {
			return ctx.Err()
		}
		logf("moving, with the touch script, for %v", o.Moving)
		d.ResetStats()
		cpu0, t0 := cpuTime(), time.Now()
		gate.open()
		if !sleep(ctx, o.Moving) {
			return ctx.Err()
		}
		res.Moving, res.MovingFor = d.Stats(), time.Since(t0)
		res.CPUMoving = percent(cpuTime()-cpu0, res.MovingFor)
		logf("still, for %v", o.Rest)
		if err := c.Update("demo", State{Still: true}); err != nil {
			return err
		}
		// The ball lands and the trails fade first.
		if !sleep(ctx, 2*time.Second) {
			return ctx.Err()
		}
		d.ResetStats()
		cpu0, t0 = cpuTime(), time.Now()
		if !sleep(ctx, o.Rest) {
			return ctx.Err()
		}
		res.CPURest = percent(cpuTime()-cpu0, time.Since(t0))
		res.RestFrames = d.Stats().Frames
		res.RSS = rss()
		return nil
	})
	<-d.Closed()
	if err != nil && !errors.Is(err, context.Canceled) {
		benchErr = err
	}
	return res, benchErr
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// cpuTime returns the CPU time the process has used.
func cpuTime() time.Duration {
	var ru unix.Rusage
	if unix.Getrusage(unix.RUSAGE_SELF, &ru) != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func percent(cpu, wall time.Duration) float64 {
	if wall <= 0 {
		return 0
	}
	return 100 * cpu.Seconds() / wall.Seconds()
}

// rss returns the process's resident memory, from /proc/self/status.
func rss() int64 {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for line := range strings.Lines(string(b)) {
		if v, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			kb, _ := strconv.ParseInt(strings.Fields(v)[0], 10, 64)
			return kb * 1024
		}
	}
	return 0
}

// gate holds a touch script back until the bench opens it, so it plays
// while the scene is measured, and not while it warms up.
type gate struct {
	r      io.ReadCloser
	opened chan struct{}
	closed chan struct{}
	once   sync.Once
	shut   sync.Once
}

func newGate(r io.ReadCloser) *gate {
	return &gate{r: r, opened: make(chan struct{}), closed: make(chan struct{})}
}

func (g *gate) open() { g.once.Do(func() { close(g.opened) }) }

// Read reads the script once the gate is open.
func (g *gate) Read(p []byte) (int, error) {
	select {
	case <-g.opened:
		return g.r.Read(p)
	case <-g.closed:
		return 0, io.ErrClosedPipe
	}
}

// Close ends a Read waiting for the gate, and closes the script.
func (g *gate) Close() error {
	g.shut.Do(func() { close(g.closed) })
	return g.r.Close()
}

// A Goal is one of the plan's goals, checked against a result.
type Goal struct {
	Name, Target, Got string
	OK                bool
}

// Goals checks the result against the goals of docs/plans/kms-touch.md,
// section 1, that the bench can measure. CPU use while moving has no target
// yet: it is recorded.
func (r Result) Goals() []Goal {
	ms := func(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000) }
	fps := float64(r.Moving.Frames) / r.MovingFor.Seconds()
	// The scene moves on every frame, so a frame handed over a refresh or
	// more after the flip before is a slow one: its vblanks count as
	// missed, and its time counts.
	missed := r.Moving.Missed + r.Moving.LateMissed
	missedPer1000 := 1000 * float64(missed) / math.Max(1, float64(r.Moving.Frames))
	gs := []Goal{
		{"frame rate while moving", fmt.Sprintf("≥ %.1f fps", r.Rate*0.99), fmt.Sprintf("%.1f fps", fps), fps >= r.Rate*0.99},
		{"missed flips", "≤ 1 in 1 000 frames", fmt.Sprintf("%d in %d (%d slow frames)", missed, r.Moving.Frames, r.Moving.Late), missedPer1000 <= 1},
		{"frame time p50", "≤ 4.0 ms", ms(r.Moving.All.P50), r.Moving.All.P50 <= 4*time.Millisecond},
		{"frame time p99", "≤ 8.0 ms", ms(r.Moving.All.P99), r.Moving.All.P99 <= 8*time.Millisecond},
		{"touch to flip p50", "≤ 20.0 ms", ms(r.Moving.Touch.P50), r.Moving.Touch.N > 0 && r.Moving.Touch.P50 <= 20*time.Millisecond},
		{"touch to flip p99", "≤ 30.0 ms", ms(r.Moving.Touch.P99), r.Moving.Touch.N > 0 && r.Moving.Touch.P99 <= 30*time.Millisecond},
		{"start to first frame", "≤ 1.0 s warm", fmt.Sprintf("%.2f s", r.Start.Seconds()), r.Start <= time.Second},
		{"CPU at rest, screen on", "≤ 0.5 % of a core", fmt.Sprintf("%.2f %%", r.CPURest), r.CPURest <= 0.5},
		{"frames drawn at rest", "0", strconv.Itoa(r.RestFrames), r.RestFrames == 0},
		{"CPU while moving", "recorded", fmt.Sprintf("%.1f %%", r.CPUMoving), true},
		{"memory", "recorded", fmt.Sprintf("%.0f MB RSS", float64(r.RSS)/(1<<20)), true},
	}
	return gs
}

// Script returns a touch script of about d: one finger drawing circles,
// taps, and two fingers dragging together, as reports 17 ms apart, as a
// Touch Display 2's touch controller polls. It is a recording in the form
// kms.Options.Record writes, for a 720×1280 panel.
func Script(d time.Duration) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "gunim-evdev 1 0 719 0 1279 5\n")
	var ev [24]byte
	at := time.Duration(0)
	put := func(typ, code uint16, v int32) {
		binary.NativeEndian.PutUint64(ev[:], uint64(at/time.Second))
		binary.NativeEndian.PutUint64(ev[8:], uint64(at%time.Second/time.Microsecond))
		binary.NativeEndian.PutUint16(ev[16:], typ)
		binary.NativeEndian.PutUint16(ev[18:], code)
		binary.NativeEndian.PutUint32(ev[20:], uint32(v))
		b.Write(ev[:])
	}
	const (
		evSyn, evAbs                    = 0, 3
		slot, posX, posY, track         = 0x2f, 0x35, 0x36, 0x39
		step                            = 17 * time.Millisecond
		circle                          = 2 * time.Second
		tapHold, tapGap                 = 80 * time.Millisecond, 400 * time.Millisecond
		cx, cy, cr              float64 = 360, 640, 220
	)
	id := int32(1)
	report := func() { put(evSyn, 0, 0); at += step }
	finger := func(s, x, y int32, down bool) {
		put(evAbs, slot, s)
		if down {
			put(evAbs, track, id)
			id++
		}
		put(evAbs, posX, x)
		put(evAbs, posY, y)
	}
	lift := func(s int32) {
		put(evAbs, slot, s)
		put(evAbs, track, -1)
	}
	for at < d {
		// A circle with one finger.
		start := at
		for first := true; at-start < circle && at < d; first = false {
			a := 2 * math.Pi * float64(at-start) / float64(circle)
			finger(0, int32(cx+cr*math.Cos(a)), int32(cy+cr*math.Sin(a)), first)
			report()
		}
		lift(0)
		report()
		// Three taps.
		for range 3 {
			at += tapGap
			finger(0, 200, 300, true)
			report()
			at += tapHold
			lift(0)
			report()
		}
		// Two fingers dragging down together.
		at += tapGap
		for k := int32(0); k < 60 && at < d; k++ {
			finger(0, 250, 300+10*k, k == 0)
			finger(1, 470, 300+10*k, k == 0)
			report()
		}
		lift(0)
		lift(1)
		report()
		at += tapGap
	}
	return b.Bytes()
}
