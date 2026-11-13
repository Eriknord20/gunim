//go:build linux && (arm64 || amd64)

package kmsdemo

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver/kms"
	"github.com/marrasen/gunim/internal/gallery"
	"github.com/marrasen/gunim/widget"
)

// BenchOptions say how to run the bench.
type BenchOptions struct {
	// KMS opens the screen. Its Replay is the touch script; nil plays
	// the made-up one for the screen, with a tap at its end that wakes the
	// screen once the bench has blanked it.
	KMS kms.Options
	// Screen is what the bench shows: "demo", the test pattern, or
	// "widgets", the gallery of example/widgets.
	Screen string
	// Warm is how long the scene moves before the figures start, Moving
	// how long it is measured moving with the touch script playing, and
	// Rest how long it is measured still.
	Warm, Moving, Rest time.Duration
	// Still keeps the scene still while the touch script plays, so only
	// the fingers draw, as on a screen that mostly stands, where Moving
	// measures the scene moving on every frame. The frame rate is then
	// the touches', not the panel's.
	Still bool
	// NoBlank leaves out the blanking at the end.
	NoBlank bool
	// Shot, if set, is a PNG file the screen's window is written to as
	// the warm-up ends.
	Shot string
	// Log, if set, hears what the bench does.
	Log func(string)
}

// Result is what the bench measured.
type Result struct {
	// Screen is what the bench showed.
	Screen string
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
	// Warming is the figures over the warm-up, the start of the program
	// with them: what a person sees first, recorded apart.
	Warming kms.Stats
	// StillAfter is how long the screen took to go still once the scene
	// stopped, past the settle, and Still says it did within stillWithin.
	// RestFor is how long the rest was measured.
	StillAfter time.Duration
	Still      bool
	RestFor    time.Duration
	// AllocsPerFrame is the whole process's allocations a frame while
	// the scene moved, the engine's and the app's with the driver's.
	AllocsPerFrame float64
	// RSS is the process's resident memory at the end, in bytes, and
	// HeapStart and HeapEnd the heap in use after a collection as the
	// scene started moving and at the end.
	RSS                int64
	HeapStart, HeapEnd uint64
	// Blank is what the blanking at the end found, unless left out.
	Blank *BlankResult
	// Software says the CPU drew.
	Software bool
}

// BlankResult is what the bench found as it blanked the screen and woke it
// with a tap.
type BlankResult struct {
	// Off says the driver blanked the screen; DisplayOn what the kernel
	// said of the display controller then, Backlight the backlight's step
	// (-1 for none), and Frames the frames drawn while it was off.
	Off       bool
	DisplayOn bool
	Backlight int
	Frames    int
	// Woke says the tap woke the screen: on, the display controller on
	// and the backlight back to WokeBacklight. Swallowed says the tap's
	// touch reached no frame's figures, as a touch that reaches no window.
	Woke, Swallowed bool
	WokeBacklight   int
	// Tapped says the touch script has the tap; a script of one's own
	// has not, and the wake is not checked.
	Tapped bool
}

// The bench's blanking: it dims after blankDim and turns off after
// blankOff, is checked as off at blankCheck, and the script's tap comes at
// blankTap, all from the blanking's start.
const (
	blankDim   = time.Second
	blankOff   = 2 * time.Second
	blankCheck = 4 * time.Second
	blankTap   = 5 * time.Second
	// blankWoke is how long after the tap the screen has to wake: past
	// the 300 ms fade up.
	blankWoke = 700 * time.Millisecond
	// stillWithin is how long past the settle the screen may take to go
	// still, no frame for a second, before the bench calls it moving.
	stillWithin = 5 * time.Second
	settle      = 2 * time.Second
)

// Bench opens the screen, shows the scene, and measures it: moving with a
// touch script playing, then still, then blanked and woken.
func Bench(ctx context.Context, o BenchOptions) (Result, error) {
	logf := func(f string, args ...any) {
		if o.Log != nil {
			o.Log(fmt.Sprintf(f, args...))
		}
	}
	if o.Screen == "" {
		o.Screen = "demo"
	}
	if o.Screen != "demo" && o.Screen != "widgets" {
		return Result{}, fmt.Errorf("kmsbench: no screen %q: want demo or widgets", o.Screen)
	}
	began := time.Now()
	res := Result{Screen: o.Screen}
	tapped := false
	gate := newGate(o.KMS.Replay)
	// The gallery's made-up script starts with the warm-up, so its first
	// scroll, which shapes and rasterises text never shown before, is not
	// measured.
	warmTouch := false
	if gate.r == nil {
		warmTouch = o.Screen == "widgets"
		lead := time.Duration(0)
		if warmTouch {
			lead = o.Warm
		}
		tap := time.Duration(0)
		if !o.NoBlank {
			tap = lead + o.Moving + settle + o.Rest + blankTap
			tapped = true
		}
		gate.r = io.NopCloser(bytes.NewReader(ScriptFor(o.Screen, o.KMS.Rotate, lead+o.Moving, tap)))
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
		c := w.Client()
		res.Software = d.Software()
		still := func() error { return nil }
		switch o.Screen {
		case "demo":
			Register(w, Options{Scale: d.Monitors()[0].Scale, Software: d.Software(), FrameMS: func() float64 { return float64(d.Stats().Last.Microseconds()) / 1000 }})
			if err := c.TakeFingers(true); err != nil {
				return err
			}
			if err := c.Mount(gunim.Root, "demo", View, State{Still: o.Still}); err != nil {
				return err
			}
			go func() {
				for range c.Intents() {
				}
			}()
			still = func() error { return c.Update("demo", State{Still: true}) }
		case "widgets":
			gallery.Register(w)
			go func() {
				if err := gallery.Serve(ctx, c, "", 0); err != nil {
					logf("the gallery: %v", err)
				}
			}()
		}
		// The first frame of the scene: the driver's own black one is the
		// first flip.
		for d.Stats().Frames < 2 {
			if !sleep(ctx, 5*time.Millisecond) {
				return ctx.Err()
			}
		}
		res.Start = time.Since(began)
		logf("first frame after %v; warming up for %v", res.Start.Round(time.Millisecond), o.Warm)
		if warmTouch {
			gate.open()
		}
		if !sleep(ctx, o.Warm) {
			return ctx.Err()
		}
		if o.Shot != "" {
			if err := writeShot(ctx, c, o.Shot); err != nil {
				return err
			}
		}
		res.Warming = d.Stats()
		logf("moving, with the touch script, for %v", o.Moving)
		res.HeapStart = heap()
		d.ResetStats()
		cpu0, t0, m0 := cpuTime(), time.Now(), mallocs()
		gate.open()
		if !sleep(ctx, o.Moving) {
			return ctx.Err()
		}
		res.Moving, res.MovingFor = d.Stats(), time.Since(t0)
		res.CPUMoving = percent(cpuTime()-cpu0, res.MovingFor)
		if res.Moving.Frames > 0 {
			res.AllocsPerFrame = float64(mallocs()-m0) / float64(res.Moving.Frames)
		}
		logf("still, for %v", o.Rest)
		if err := still(); err != nil {
			return err
		}
		// The ball lands, the trails fade, a fling ends and a scrollbar
		// fades first: the rest is measured from when no frame has come for
		// a second. It ends when it would have, as the touch script's tap
		// for the blanking comes at a time set in advance.
		restEnd := time.Now().Add(settle + o.Rest)
		if !sleep(ctx, settle) {
			return ctx.Err()
		}
		settled := time.Now()
		for n := d.Stats().Frames; time.Since(settled) < stillWithin+time.Second && time.Until(restEnd) > time.Second; {
			if !sleep(ctx, time.Second) {
				return ctx.Err()
			}
			m := d.Stats().Frames
			if m == n {
				res.Still = true
				break
			}
			n = m
		}
		res.StillAfter = max(time.Since(settled)-time.Second, 0)
		res.Still = res.Still && res.StillAfter <= stillWithin
		d.ResetStats()
		cpu0, t0 = cpuTime(), time.Now()
		if !sleep(ctx, time.Until(restEnd)) {
			return ctx.Err()
		}
		res.RestFor = time.Since(t0)
		res.CPURest = percent(cpuTime()-cpu0, res.RestFor)
		res.RestFrames = d.Stats().Frames
		if !o.NoBlank {
			b, err := blankAndWake(ctx, d, tapped, logf)
			if err != nil {
				return err
			}
			res.Blank = b
		}
		res.HeapEnd = heap()
		res.RSS = rss()
		return nil
	})
	<-d.Closed()
	if err != nil && !errors.Is(err, context.Canceled) {
		benchErr = err
	}
	return res, benchErr
}

// blankAndWake blanks the screen, checks it is off, and where the script
// taps at the end, checks the tap wakes it and reaches nothing. The tap
// comes at blankTap by the script's clock, which started as the moving
// did; the bench's own sleeps drift from it by a few milliseconds.
func blankAndWake(ctx context.Context, d *kms.Driver, tapped bool, logf func(string, ...any)) (*BlankResult, error) {
	b := &BlankResult{Tapped: tapped}
	lit := backlightStep()
	logf("blanking: dim after %v, off after %v", blankDim, blankOff)
	start := time.Now()
	d.SetBlank(kms.Blank{Dim: blankDim, Off: blankOff})
	defer d.SetBlank(kms.Blank{})
	if !sleep(ctx, blankCheck-time.Second) {
		return nil, ctx.Err()
	}
	d.ResetStats()
	if !sleep(ctx, time.Until(start.Add(blankCheck))) {
		return nil, ctx.Err()
	}
	b.Off, b.Frames, b.Backlight = d.Blanked(), d.Stats().Frames, backlightStep()
	on, err := d.DisplayOn()
	if err != nil {
		return nil, err
	}
	b.DisplayOn = on
	if !tapped {
		return b, nil
	}
	logf("blanked; waiting for the script's tap")
	// A dark screen stays dark until touched; a dim after the tap would
	// come long after the check.
	d.SetBlank(kms.Blank{Dim: time.Minute, Off: 2 * time.Minute})
	d.ResetStats()
	if !sleep(ctx, time.Until(start.Add(blankTap))) {
		return nil, ctx.Err()
	}
	// The tap comes by the script's clock, which drifts from the bench's
	// by some milliseconds over a run: the wake is looked for until
	// blankWoke after it was due, and a little more.
	var s kms.Stats
	for deadline := time.Now().Add(blankWoke + time.Second); ; {
		s = d.Stats()
		if on, err = d.DisplayOn(); err != nil {
			return nil, err
		}
		b.WokeBacklight = backlightStep()
		b.Woke = !d.Blanked() && on && b.WokeBacklight == lit && s.Frames > 0
		if b.Woke || time.Now().After(deadline) {
			break
		}
		if !sleep(ctx, 50*time.Millisecond) {
			return nil, ctx.Err()
		}
	}
	logf("after the tap: blanked %v, display on %v, backlight %d of %d before, %d frames", d.Blanked(), on, b.WokeBacklight, lit, s.Frames)
	b.Swallowed = s.Touch.N == 0
	return b, nil
}

// backlightStep returns the first backlight's step, as the driver takes
// the first, or -1 where there is none.
func backlightStep() int {
	dirs, _ := filepath.Glob("/sys/class/backlight/*/brightness")
	if len(dirs) == 0 {
		return -1
	}
	slices.Sort(dirs)
	b, err := os.ReadFile(dirs[0])
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return -1
	}
	return n
}

// writeShot writes what the window shows to a PNG file.
func writeShot(ctx context.Context, c gunim.Client, path string) error {
	img, err := c.Shot(ctx)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return errors.Join(png.Encode(f, img), f.Close())
}

// heap returns the heap in use after a collection.
func heap() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// mallocs returns the allocations the process has made.
func mallocs() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Mallocs
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
// yet: it is recorded. NotHere lists the goals measured elsewhere.
func (r Result) Goals() []Goal {
	ms := func(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000) }
	mb := func(n uint64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }
	m := r.Moving
	// The frame rate while something moves: the panel's, less the vblanks
	// that frames drawn one straight after another missed. A screen that
	// rests between touches, as the gallery does, draws fewer frames, and
	// those rests are no misses.
	fps := 0.0
	if m.Straight > 0 {
		fps = r.Rate * float64(m.Straight) / float64(m.Straight+m.StraightMissed)
	}
	missedPer1000 := 1000 * float64(m.Missed) / math.Max(1, float64(m.Frames))
	growth := int64(r.HeapEnd) - int64(r.HeapStart)
	gs := []Goal{
		{"frame rate while moving", fmt.Sprintf("≥ %.1f fps", r.Rate*0.99), fmt.Sprintf("%.1f fps (%d frames straight)", fps, m.Straight), m.Straight > 0 && fps >= r.Rate*0.99},
		{"missed flips", "≤ 1 in 1 000 frames", fmt.Sprintf("%d in %d (%d slow frames)", m.Missed, m.Frames, m.Late), missedPer1000 <= 1},
		{"frame time p50", "≤ 4.0 ms", ms(m.Frame.P50), m.Frame.N > 0 && m.Frame.P50 <= 4*time.Millisecond},
		{"frame time p99", "≤ 8.0 ms", ms(m.Frame.P99), m.Frame.N > 0 && m.Frame.P99 <= 8*time.Millisecond},
		{"touch to flip p50", "≤ 20.0 ms", ms(m.Touch.P50), m.Touch.N > 0 && m.Touch.P50 <= 20*time.Millisecond},
		{"touch to flip p99", "≤ 30.0 ms", ms(m.Touch.P99), m.Touch.N > 0 && m.Touch.P99 <= 30*time.Millisecond},
		{"start to first frame", "≤ 1.0 s warm", fmt.Sprintf("%.2f s", r.Start.Seconds()), r.Start <= time.Second},
		{"memory", "≤ 120 MB RSS", fmt.Sprintf("%.0f MB RSS", float64(r.RSS)/(1<<20)), r.RSS > 0 && r.RSS <= 120<<20},
		{"heap growth over the run", "≤ 2 MB after GC", fmt.Sprintf("%+.1f MB (%s to %s)", float64(growth)/(1<<20), mb(r.HeapStart), mb(r.HeapEnd)), growth <= 2<<20},
		{"went still once stopped", fmt.Sprintf("within %v", stillWithin), stillGot(r), r.Still},
		{"CPU at rest, screen on", "≤ 0.5 % of a core", fmt.Sprintf("%.2f %% over %.0f s", r.CPURest, r.RestFor.Seconds()), r.Still && r.CPURest <= 0.5},
		{"frames drawn at rest", "0", fmt.Sprintf("%d in %.0f s", r.RestFrames, r.RestFor.Seconds()), r.Still && r.RestFrames == 0},
	}
	if b := r.Blank; b != nil {
		light := strconv.Itoa(b.Backlight)
		if b.Backlight < 0 {
			light = "none"
		}
		gs = append(gs, Goal{"screen blanked", "backlight 0, CRTC off, 0 frames",
			fmt.Sprintf("backlight %s, CRTC %s, %d frames", light, map[bool]string{true: "on", false: "off"}[b.DisplayOn], b.Frames),
			b.Off && !b.DisplayOn && b.Backlight <= 0 && b.Frames == 0})
		if b.Tapped {
			gs = append(gs, Goal{"a tap wakes it, swallowed", "on, backlight back, no touch",
				fmt.Sprintf("woke %v, backlight %d, swallowed %v", b.Woke, b.WokeBacklight, b.Swallowed), b.Woke && b.Swallowed})
		}
	}
	w := r.Warming
	gs = append(gs,
		Goal{"missed flips while warming up", "recorded", fmt.Sprintf("%d in %d (%d slow frames)", w.Missed, w.Frames, w.Late), true},
		Goal{"CPU while moving", "recorded", fmt.Sprintf("%.1f %%", r.CPUMoving), true},
		Goal{"allocations a frame, whole process", "recorded", fmt.Sprintf("%.0f", r.AllocsPerFrame), true},
	)
	return gs
}

// stillGot says how long the screen took to go still, or that it didn't.
func stillGot(r Result) string {
	if !r.Still {
		return fmt.Sprintf("still drawing after %.0f s", r.StillAfter.Seconds())
	}
	return fmt.Sprintf("after %.1f s", r.StillAfter.Seconds())
}

// NotHere lists the goals of section 1 that the bench does not measure,
// and where they are measured.
var NotHere = []string{
	"driver allocations a frame: 0, by driver/kms's TestCollectingAFrameAllocatesNothing and compose's TestAFrameAllocatesNothing",
	"touch to light: by slow-motion video, by hand",
	"start straight after boot, and boot to a touchable screen: need a reboot",
	"the store's speed while the UI animates: Hjortron's, in K4",
}

// ScriptFor returns the touch script for a screen, turned by rotate, of
// about d, and where tap is not zero, a tap at tap from its start, for the
// bench's wake. For "widgets" it drags the gallery up and down, with a
// fling each way; for the demo it is Script.
func ScriptFor(screen string, rotate int, d, tap time.Duration) []byte {
	var b []byte
	if screen == "widgets" {
		b = dragScript(rotate, d)
	} else {
		b = Script(d)
	}
	if tap > 0 {
		b = append(b, tapAt(tap, 600, 640)...)
	}
	return b
}

// dragScript drags along the picture's height, in reports 17 ms apart:
// up 500 panel pixels in 300 ms and let go, which flings the gallery on
// down the page, then, after a wait, back down the same way. It ends with
// one more drag up, so the page rests scrolled down, with the gallery's
// spinner out of sight, for the bench's rest.
func dragScript(rotate int, d time.Duration) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "gunim-evdev 1 0 719 0 1279 5\n")
	at := time.Duration(0)
	const step = 17 * time.Millisecond
	id := int32(1)
	// up is a finger's way towards the picture's top, on the panel: the
	// picture's top lies along the panel's right edge turned 90°, and its
	// left edge turned 270°.
	var x0, y0, x1, y1 int32
	switch rotate {
	case 90:
		x0, y0, x1, y1 = 100, 640, 600, 640
	case 180:
		x0, y0, x1, y1 = 360, 200, 360, 1000
	case 270:
		x0, y0, x1, y1 = 600, 640, 100, 640
	default:
		x0, y0, x1, y1 = 360, 1000, 360, 200
	}
	drag := func(fx, fy, tx, ty int32) {
		const n = 18 // 300 ms
		for k := int32(0); k <= n; k++ {
			if k == 0 {
				putEvent(&b, at, evAbsT, absSlot, 0)
				putEvent(&b, at, evAbsT, absTrack, id)
				id++
			}
			putEvent(&b, at, evAbsT, absX, fx+(tx-fx)*k/n)
			putEvent(&b, at, evAbsT, absY, fy+(ty-fy)*k/n)
			putEvent(&b, at, evSynT, 0, 0)
			at += step
		}
		putEvent(&b, at, evAbsT, absTrack, -1)
		putEvent(&b, at, evSynT, 0, 0)
		at += step
	}
	const wait = 1500 * time.Millisecond
	if rotate == 90 {
		// On the landscape gallery at the Pi's scale, a tap on the "List"
		// drop-down opens its menu, a popup, and a tap on the empty header
		// closes it, before the drags.
		b.Write(tapAt(500*time.Millisecond, 719-420, 325))
		b.Write(tapAt(1500*time.Millisecond, 719-40, 1100))
		at = 2500 * time.Millisecond
	}
	for at < d {
		drag(x0, y0, x1, y1)
		at += wait
		if at >= d {
			return b.Bytes()
		}
		drag(x1, y1, x0, y0)
		at += wait
	}
	drag(x0, y0, x1, y1)
	return b.Bytes()
}

// tapAt returns the events of a tap at x, y on the panel, at, as a script's
// tail.
func tapAt(at time.Duration, x, y int32) []byte {
	var b bytes.Buffer
	putEvent(&b, at, evAbsT, absSlot, 0)
	putEvent(&b, at, evAbsT, absTrack, 9999)
	putEvent(&b, at, evAbsT, absX, x)
	putEvent(&b, at, evAbsT, absY, y)
	putEvent(&b, at, evSynT, 0, 0)
	at += 80 * time.Millisecond
	putEvent(&b, at, evAbsT, absTrack, -1)
	putEvent(&b, at, evSynT, 0, 0)
	return b.Bytes()
}

// The kernel's codes a script uses.
const (
	evSynT, evAbsT                = 0, 3
	absSlot, absX, absY, absTrack = 0x2f, 0x35, 0x36, 0x39
)

// putEvent writes one 24-byte input_event at at, as a recording keeps it.
func putEvent(b *bytes.Buffer, at time.Duration, typ, code uint16, v int32) {
	var ev [24]byte
	binary.NativeEndian.PutUint64(ev[:], uint64(at/time.Second))
	binary.NativeEndian.PutUint64(ev[8:], uint64(at%time.Second/time.Microsecond))
	binary.NativeEndian.PutUint16(ev[16:], typ)
	binary.NativeEndian.PutUint16(ev[18:], code)
	binary.NativeEndian.PutUint32(ev[20:], uint32(v))
	b.Write(ev[:])
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
