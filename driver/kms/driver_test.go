//go:build linux && (arm64 || amd64)

package kms

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/input"
)

// idleDriver is a driver whose render thread has ended and that holds
// nothing, for what Run and Close do around it.
func idleDriver(t *testing.T) *Driver {
	t.Helper()
	d := &Driver{quit: make(chan struct{}), done: make(chan struct{}), ended: make(chan struct{}),
		wake: -1, stop: -1, stats: newTimeline(nil), blank: newBlanker(Blank{}, 1, time.Now()),
		lightTo: make(chan int, 1), lightDone: make(chan struct{})}
	close(d.done)
	return d
}

// SIGHUP, as an SSH session dropping sends, ends Run, which gives the
// screen back, where it would otherwise end the process with the VT left
// in graphics mode. SIGINT and SIGTERM go the same way.
func TestASignalEndsRunAndGivesTheScreenBack(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			d := idleDriver(t)
			// A render thread that runs until Close stops it: one already
			// ended would end Run before the signal came, and the signal,
			// with its default handling back, would end the test.
			d.done = make(chan struct{})
			go func() {
				<-d.quit
				close(d.done)
			}()
			d.catchSignals()
			ran := make(chan error, 1)
			go func() { ran <- d.Run(context.Background(), func() {}) }()
			if err := syscall.Kill(syscall.Getpid(), sig); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-ran:
				if err != nil {
					t.Fatalf("Run returned %v, want nil for a signal", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("Run did not end on %v", sig)
			}
			select {
			case <-d.Closed():
			default:
				t.Fatal("Run ended with the screen not given back")
			}
		})
	}
}

// Close gives the screen back once, however often it is called, before or
// after Run, and returns the error the render thread ended on.
func TestCloseIsSafeTwiceAndAfterRun(t *testing.T) {
	d := idleDriver(t)
	d.err = errors.New("the GPU lost its context")
	if err := d.Close(); !errors.Is(err, d.err) {
		t.Fatalf("Close returned %v", err)
	}
	if err := d.Close(); !errors.Is(err, d.err) {
		t.Fatalf("a second Close returned %v", err)
	}
	if err := d.Run(context.Background(), func() {}); !errors.Is(err, d.err) {
		t.Fatalf("Run after Close returned %v", err)
	}
	<-d.Closed()
}

// Each frame is timed from when the engine began it to its commit, slow
// ones too, which count as late. A frame begun as the windows heard of the
// flip before misses each vblank past the first; one begun after a rest
// has until the vblank after next.
func TestAFrameIsTimedFromItsStart(t *testing.T) {
	const period = 16667 * time.Microsecond
	tl := newTimeline(nil)
	t0 := time.Now()
	ms := func(n float64) time.Time { return t0.Add(time.Duration(n * float64(time.Millisecond))) }
	// Straight, on time.
	tl.shown(shownFrame{seq: 2, prev: ms(0), began: ms(0.2), handed: ms(2), commit: ms(3), flip: ms(16.7), straight: true, period: period})
	// Straight, laid out in 25 ms: a vblank late.
	tl.shown(shownFrame{seq: 4, prev: ms(16.7), began: ms(16.9), handed: ms(41.7), commit: ms(42), flip: ms(50), straight: true, missed: 1, period: period})
	// After a rest, on a touch: on time.
	tl.shown(shownFrame{seq: 30, prev: ms(50), began: ms(500), handed: ms(501), commit: ms(502), flip: ms(516.7), touch: ms(490), period: period})
	// After a rest, but 40 ms to build: shown at the third vblank, one late.
	tl.shown(shownFrame{seq: 40, prev: ms(516.7), began: ms(600), handed: ms(639), commit: ms(640), flip: ms(650), period: period})
	// No start known: timed from the frame handed over.
	tl.shown(shownFrame{seq: 41, prev: ms(650), handed: ms(660), commit: ms(661), flip: ms(666.7), period: period})
	s := tl.stats()
	if s.Frames != 5 || s.Missed != 2 || s.Late != 2 {
		t.Fatalf("frames %d, missed %d, late %d; want 5, 2, 2", s.Frames, s.Missed, s.Late)
	}
	if s.Frame.N != 5 || s.Frame.Max < 40*time.Millisecond || s.Frame.P50 != 2800*time.Microsecond {
		t.Fatalf("frame %+v", s.Frame)
	}
	if s.Touch.N != 1 || s.Touch.P50 < 26*time.Millisecond {
		t.Fatalf("touch %+v", s.Touch)
	}
	tl.reset()
	if s := tl.stats(); s.Frames != 0 || s.Frame.N != 0 || s.Late != 0 || s.Missed != 0 {
		t.Fatalf("after reset %+v", s)
	}
}

// After SYN_DROPPED the kernel sends no ABS_MT_SLOT again, so the decoder
// reads the slot the events go to now: a move for finger 1 after the drop
// moves finger 1, not finger 0.
func TestTheSlotIsReadAgainAfterADrop(t *testing.T) {
	var s stream
	s.down(0, 1, 10, 10)
	s.down(1, 2, 20, 20)
	s.abs(absMTSlot, 0)
	s.syn(synReport)
	s.syn(synDropped)
	s.abs(absMTSlot, 1) // lost with the rest
	s.syn(synReport)
	s.abs(absMTPositionX, 500)
	s.syn(synReport)
	m := newMTDecoder(2)
	m.resync = func(code uint16) []int32 {
		switch code {
		case absMTSlot:
			return []int32{1}
		case absMTTrackingID:
			return []int32{1, 2}
		case absMTPositionX:
			return []int32{10, 20}
		default:
			return []int32{10, 20}
		}
	}
	got := decode(m, s.evs)
	last := got[len(got)-1]
	if len(last) != 1 || last[0] != (contact{slot: 1, id: 2, x: 500, y: 20, phase: phaseMove}) {
		t.Fatalf("after the drop: %v, want finger 1 at 500", last)
	}
}

func TestLiftAllLiftsEveryFingerDown(t *testing.T) {
	var s stream
	s.down(0, 1, 10, 10)
	s.down(3, 4, 30, 30)
	s.syn(synReport)
	m := newMTDecoder(5)
	decode(m, s.evs)
	got := m.liftAll()
	want := []contact{{slot: 0, id: 1, x: 10, y: 10, phase: phaseUp}, {slot: 3, id: 4, x: 30, y: 30, phase: phaseUp}}
	if !slices.Equal(got, want) {
		t.Fatalf("lifted %v, want %v", got, want)
	}
	if again := m.liftAll(); again != nil {
		t.Fatalf("lifted %v again", again)
	}
}

// A stamp from before the clock was set lies decades ahead; it, and every
// stamp from a device left on the wall clock, is taken as now.
func TestEventTimesFromAnotherClockAreTakenAsNow(t *testing.T) {
	now := time.Now()
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		t.Fatal(err)
	}
	mono := time.Duration(ts.Nano()) - 5*time.Millisecond
	fresh := event{sec: int64(mono / time.Second), usec: int64(mono % time.Second / time.Microsecond)}
	if got := eventTime(fresh, false, now); got.Equal(now) || now.Sub(got) > time.Second {
		t.Fatalf("a fresh monotonic stamp came out %v before now", now.Sub(got))
	}
	wall := event{sec: now.Unix(), usec: int64(now.Nanosecond() / 1000)}
	if got := eventTime(wall, false, now); !got.Equal(now) {
		t.Fatalf("a wall-clock stamp came out %v from now", got.Sub(now))
	}
	if got := eventTime(fresh, true, now); !got.Equal(now) {
		t.Fatal("a device on the wall clock was not stamped as read")
	}
}

// A touch screen that goes away mid-touch lifts its fingers, so no window
// keeps a press that never ends.
func TestATouchScreenGoneLiftsItsFingers(t *testing.T) {
	if liveEventSize != 24 {
		t.Skip("the events are written as 64-bit Linux's")
	}
	d := testDriver(0, 1)
	stop, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(stop) }()
	d.stop = stop
	w, _ := d.NewWindow(driver.Options{})
	win := w.(*Window)
	defer func() { _ = win.Close() }()
	var p [2]int
	if err := unix.Pipe2(p[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(p[0]) }()
	var s stream
	s.down(0, 1, 100, 100)
	s.syn(synReport)
	buf := make([]byte, 24*len(s.evs))
	for i, e := range s.evs {
		encodeEvent(buf[24*i:], e)
	}
	if _, err := unix.Write(p[1], buf); err != nil {
		t.Fatal(err)
	}
	if err := unix.Close(p[1]); err != nil {
		t.Fatal(err)
	}
	dev := &inputDev{fd: p[0], path: "a pipe", slots: 2, x: absInfo{maximum: 719}, y: absInfo{maximum: 1279}}
	done := make(chan struct{})
	go func() {
		d.readTouch(dev)
		close(done)
	}()
	var got []string
	for len(got) < 4 {
		switch nextInput(t, win).(type) {
		case input.PointerMove:
			got = append(got, "move")
		case input.PointerDown:
			got = append(got, "down")
		case input.PointerUp:
			got = append(got, "up")
		case input.PointerLeave:
			got = append(got, "leave")
		}
	}
	if want := []string{"move", "down", "up", "leave"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the reader did not end")
	}
}

// The GPU's attempt failing leaves the CPU to draw, in the same process;
// both failing is an error that holds both reasons; software asks for the
// CPU at once.
func TestTheCPUDrawsWhereTheGPUFails(t *testing.T) {
	gpuErr, cpuErr := errors.New("no V3D"), errors.New("no llvmpipe")
	for _, c := range []struct {
		name     string
		software bool
		fail     map[bool]error
		tries    []bool
		onCPU    bool
		err      []error
	}{
		{"the GPU draws", false, nil, []bool{false}, false, nil},
		{"the GPU fails", false, map[bool]error{false: gpuErr}, []bool{false, true}, true, nil},
		{"both fail", false, map[bool]error{false: gpuErr, true: cpuErr}, []bool{false, true}, true, []error{gpuErr, cpuErr}},
		{"asked for the CPU", true, nil, []bool{true}, true, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			var tries []bool
			onCPU, err := startDrawing(c.software, func(software bool) error {
				tries = append(tries, software)
				return c.fail[software]
			})
			if !slices.Equal(tries, c.tries) || onCPU != c.onCPU {
				t.Fatalf("tried %v, on the CPU %v", tries, onCPU)
			}
			for _, want := range c.err {
				if !errors.Is(err, want) {
					t.Fatalf("error %v, want it to hold %v", err, want)
				}
			}
			if c.err == nil && err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Collecting a frame allocates nothing once the slices have grown.
func TestCollectingAFrameAllocatesNothing(t *testing.T) {
	d := testDriver(90, 1.25)
	w, _ := d.NewWindow(driver.Options{})
	win := w.(*Window)
	defer func() { _ = win.Close() }()
	r := &renderThread{d: d}
	step := func() {
		d.mu.Lock()
		win.next, win.hasNext = frame{}, true
		d.mu.Unlock()
		r.collect()
		if len(r.jobs) != 1 || len(r.stack) != 1 {
			t.Fatalf("collected %d jobs over %d layers", len(r.jobs), len(r.stack))
		}
	}
	step()
	if n := testing.AllocsPerRun(100, step); n != 0 {
		t.Fatalf("%v allocations a frame", n)
	}
}

// The pacer starts by telling windows of a flip at once, shortens its lead
// a step at a time while frames make their vblank, and on a miss lengthens
// it at once and holds it. Frames drawn after a rest teach it nothing.
func TestThePacerLearnsHowLateAFrameCanStart(t *testing.T) {
	const period = 16667 * time.Microsecond
	var p pacer
	if got := p.now(0, period); got != period {
		t.Fatalf("starts with a lead of %v, want a whole refresh", got)
	}
	for range paceEvery {
		p.flipped(true, false, 0, period)
	}
	if got := p.now(0, period); got != period-paceStep {
		t.Fatalf("after %d frames made: %v, want a step shorter", paceEvery, got)
	}
	for range 100 * paceEvery {
		p.flipped(true, true, 0, period)
	}
	if got := p.now(0, period); got != paceMinLead {
		t.Fatalf("after many frames made: %v, want the floor, %v", got, paceMinLead)
	}
	p.flipped(false, true, 3, period)
	if got := p.now(0, period); got != paceMinLead {
		t.Fatalf("a frame after a rest moved the lead to %v", got)
	}
	p.flipped(true, true, 1, period)
	if got := p.now(0, period); got != period {
		t.Fatalf("after a miss: %v, want a whole refresh", got)
	}
	for range paceHold {
		p.flipped(true, false, 0, period)
	}
	if got := p.now(0, period); got != period {
		t.Fatalf("shortened to %v while holding after a miss", got)
	}
	if got := p.now(5*time.Millisecond, period); got != 5*time.Millisecond {
		t.Fatalf("a fixed lead came out %v", got)
	}
}

// screenDev takes a screen's commits, as the kernel would, and keeps each:
// its flags and the properties it set, as object.property = value.
type screenDev struct {
	s       *screen
	names   map[uint32]string
	commits []commitSeen
	blobs   int
}

type commitSeen struct {
	flags uint32
	set   []string
}

func (f *screenDev) atomic(r *modeAtomic) error {
	c := commitSeen{flags: r.flags}
	k := 0
	for i, obj := range f.s.req.objs {
		for range f.s.req.counts[i] {
			c.set = append(c.set, fmt.Sprintf("%d.%s=%d", obj, f.names[f.s.req.props[k]], f.s.req.values[k]))
			k++
		}
	}
	f.commits = append(f.commits, c)
	return nil
}

func (f *screenDev) createBlob([]byte) (uint32, error) { f.blobs++; return 77, nil }

// testScreen is a screen with a connector 1, a CRTC 2 and a primary plane
// 3, with their properties, committing to a screenDev.
func testScreen() (*screen, *screenDev) {
	s := &screen{conn: connector{id: 1}, crtc: 2, plane: 3, mode: modeInfo{HDisplay: 720, VDisplay: 1280}}
	names := map[uint32]string{}
	id := uint32(100)
	props := func(ns ...string) map[string]prop {
		m := map[string]prop{}
		for _, n := range ns {
			id++
			m[n] = prop{id: id}
			names[id] = n
		}
		return m
	}
	s.connProps = props("CRTC_ID")
	s.crtcProps = props("MODE_ID", "ACTIVE")
	s.planeProps = props("FB_ID", "CRTC_ID", "SRC_X", "SRC_Y", "SRC_W", "SRC_H", "CRTC_X", "CRTC_Y", "CRTC_W", "CRTC_H")
	f := &screenDev{s: s, names: names}
	s.dev = f
	return s, f
}

// The first frame sets the mode: a test commit first, with
// ALLOW_MODESET, then the same commit for real, with a flip event, which
// blocks until the kernel takes it. It sets the mode's blob and ACTIVE on
// the CRTC, the connector on the CRTC, and the whole plane.
func TestTheModesetIsTestedFirstAndAllowsAModeset(t *testing.T) {
	s, f := testScreen()
	if err := s.modeset(9, 0, false, 1); err != nil {
		t.Fatal(err)
	}
	if len(f.commits) != 2 || f.blobs != 1 {
		t.Fatalf("%d commits, %d blobs", len(f.commits), f.blobs)
	}
	test, set := f.commits[0], f.commits[1]
	if test.flags != atomicTestOnly|atomicAllowModeset {
		t.Errorf("the first commit's flags are %#x, want TEST_ONLY | ALLOW_MODESET", test.flags)
	}
	if set.flags != atomicAllowModeset|atomicPageFlipEvent {
		t.Errorf("the modeset's flags are %#x, want ALLOW_MODESET | PAGE_FLIP_EVENT, and blocking", set.flags)
	}
	want := []string{
		"1.CRTC_ID=2", "2.MODE_ID=77", "2.ACTIVE=1",
		"3.FB_ID=9", "3.CRTC_ID=2", "3.SRC_X=0", "3.SRC_Y=0", "3.SRC_W=47185920", "3.SRC_H=83886080",
		"3.CRTC_X=0", "3.CRTC_Y=0", "3.CRTC_W=720", "3.CRTC_H=1280",
	}
	if !slices.Equal(test.set, want) || !slices.Equal(set.set, want) {
		t.Fatalf("set %v then %v, want %v", test.set, set.set, want)
	}
	// A frame after: the framebuffer alone, without blocking, with a flip
	// event.
	if err := s.flip(10, 1); err != nil {
		t.Fatal(err)
	}
	flip := f.commits[2]
	if flip.flags != atomicNonblock|atomicPageFlipEvent || !slices.Equal(flip.set, []string{"3.FB_ID=10"}) {
		t.Fatalf("a flip committed %#x with %v", flip.flags, flip.set)
	}
}

// A kick after Close writes nothing: the eventfd is closed, and its number
// may name another file by then.
func TestAKickAfterCloseWritesNothing(t *testing.T) {
	d := idleDriver(t)
	fd, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		t.Fatal(err)
	}
	d.wake = fd
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if d.wake != -1 {
		t.Fatalf("wake is %d after Close", d.wake)
	}
	d.kick() // must not touch fd, which the test's next file may reuse
}

// A render thread that never stops, as one stuck on a hung GPU, holds
// Close up only so long: the VT and the backlight still go back.
func TestCloseGivesUpOnAStuckRenderThread(t *testing.T) {
	was := closeWait
	closeWait = 50 * time.Millisecond
	t.Cleanup(func() { closeWait = was })
	d := &Driver{quit: make(chan struct{}), done: make(chan struct{}), ended: make(chan struct{}),
		wake: -1, stop: -1, stats: newTimeline(nil)}
	c := newFakeConsole(1)
	v, err := takeVT(c, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	d.vt = v
	closed := make(chan error, 1)
	go func() { closed <- d.Close() }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited for ever on the render thread")
	}
	if c.active != 1 || c.mode[2] != kdText {
		t.Fatalf("the VT was not put back: showing %d, mode %d", c.active, c.mode[2])
	}
}

// A frame that takes longer to build than the lead leaves it is handed over
// after its vblank. Counted from the flip before, it would look like a
// frame after a rest, and the pacer would never learn: 12 s of light frames
// teach a short lead, and a heavier screen after it would stay at 30 fps.
// Counted from the release, it is a miss, and the pacer backs off.
func TestThePacerBacksOffWhenFramesGetHeavier(t *testing.T) {
	const (
		period = 16667 * time.Microsecond
		gpu    = time.Millisecond
	)
	var p pacer
	t0 := time.Now()
	vblank := t0 // the vblank the frame before flipped on
	// run draws n frames that take cost to build, and returns the
	// vblanks they missed.
	run := func(n int, cost time.Duration) (misses int) {
		for range n {
			lead := p.now(0, period)
			paced := lead < period
			release := vblank
			if paced {
				release = vblank.Add(period - lead)
			}
			handed := release.Add(cost)
			next := vblank.Add(period)
			missed := 0
			for handed.Add(gpu).After(next) {
				next = next.Add(period)
				missed++
			}
			start := frameStart(vblank, release, paced)
			straight := handed.Sub(start) < period
			p.flipped(straight, paced, missed, period)
			misses += missed
			vblank = next
		}
		return misses
	}
	run(int(12*time.Second/period), 2*time.Millisecond)
	if got := p.now(0, period); got >= 6*time.Millisecond {
		t.Fatalf("12 s of light frames left a lead of %v, want it learnt short", got)
	}
	run(int(10*time.Second/period), 6*time.Millisecond)
	if got := p.now(0, period); got < 7*time.Millisecond {
		t.Fatalf("heavier frames left a lead of %v, want at least their 7 ms", got)
	}
	if misses := run(1000, 6*time.Millisecond); misses > 1 {
		t.Fatalf("%d vblanks missed in 1 000 frames once the lead was learnt", misses)
	}
}

// A screen that rests between bursts of heavy frames, as a list between
// scrolls, misses once as a burst starts after the lead was learnt short,
// and not again for a long while.
func TestABurstOfHeavyFramesMissesOnce(t *testing.T) {
	const (
		period = 16667 * time.Microsecond
		gpu    = time.Millisecond
	)
	var p pacer
	vblank := time.Now()
	run := func(n int, cost time.Duration) (misses int) {
		for range n {
			lead := p.now(0, period)
			paced := lead < period
			release := vblank
			if paced {
				release = vblank.Add(period - lead)
			}
			handed := release.Add(cost)
			next := vblank.Add(period)
			missed := 0
			for handed.Add(gpu).After(next) {
				next = next.Add(period)
				missed++
			}
			p.flipped(true, paced, missed, period)
			misses += missed
			vblank = next
		}
		return misses
	}
	run(int(20*time.Second/period), time.Millisecond)
	misses := 0
	// A minute of scrolls: 0.3 s of 13 ms frames, then 1.5 s of 1 ms ones.
	for range 33 {
		misses += run(18, 13*time.Millisecond)
		misses += run(90, time.Millisecond)
	}
	if misses > 2 {
		t.Fatalf("%d vblanks missed in a minute of scrolls, want at most 2", misses)
	}
}

// A second start while a live run holds the state file stops before it
// touches a VT: it would reset the live run's VT to text mode with its
// keyboard on, and take its switching.
func TestASecondStartLeavesTheLiveRunsVTAlone(t *testing.T) {
	p := filepath.Join(t.TempDir(), "gunim-kms.json")
	wasPath, wasConsoles := statePath, consoles
	statePath = func() string { return p }
	c := newFakeConsole(7)
	c.mode[7], c.kb[7], c.process[7] = kdGraphics, kOff, true
	consoles = c
	t.Cleanup(func() { statePath, consoles = wasPath, wasConsoles })
	live, _, _, err := lockState()
	if err != nil {
		t.Fatal(err)
	}
	defer live.done()
	d, err := Open(Options{VT: "7"})
	if !errors.Is(err, errStateHeld) || d != nil {
		t.Fatalf("Open beside a live run: %v, %v", d, err)
	}
	if len(c.calls) != 0 || len(c.fds) != 0 || c.mode[7] != kdGraphics || c.kb[7] != kOff || !c.process[7] {
		t.Fatalf("the live run's VT was touched: %v", c.calls)
	}
}

// A touch screen that goes away ends Run with ErrTouchGone, after the
// screen is given back: nothing else would wake a screen that is off.
func TestATouchScreenThatGoesEndsRun(t *testing.T) {
	d := idleDriver(t)
	d.lost = make(chan error, 1)
	d.done = make(chan struct{})
	go func() {
		<-d.quit
		close(d.done)
	}()
	var p [2]int
	if err := unix.Pipe(p[:]); err != nil {
		t.Fatal(err)
	}
	_ = unix.Close(p[1]) // the device is gone: its end hangs up
	defer func() { _ = unix.Close(p[0]) }()
	dev := &inputDev{fd: p[0], path: "/dev/input/event1", touch: true, slots: 5,
		x: absInfo{maximum: 719}, y: absInfo{maximum: 1279}}
	ran := make(chan error, 1)
	go func() { ran <- d.Run(context.Background(), func() {}) }()
	d.readTouch(dev)
	select {
	case err := <-ran:
		if !errors.Is(err, ErrTouchGone) {
			t.Fatalf("Run returned %v, want ErrTouchGone", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not end as the touch screen went")
	}
	select {
	case <-d.Closed():
	default:
		t.Fatal("Run ended with the screen not given back")
	}
}

// With RequireTouch, finding no touch screen fails with ErrNoTouchScreen,
// so a program started again waits for one; without it, the driver runs
// with none.
func TestNoTouchScreenFailsOnlyWhereRequired(t *testing.T) {
	none := filepath.Join(t.TempDir(), "event*")
	if _, _, err := findInputsIn(none, "", true, true, false); !errors.Is(err, ErrNoTouchScreen) {
		t.Fatalf("required and none found: %v, want ErrNoTouchScreen", err)
	}
	if touch, _, err := findInputsIn(none, "", true, false, false); err != nil || touch != nil {
		t.Fatalf("not required and none found: %v, %v", touch, err)
	}
	if _, _, err := findInputsIn(none, "", false, true, false); err != nil {
		t.Fatalf("a replay with RequireTouch: %v", err)
	}
}

// Run keeps both errors: why it ended, and what the render thread ended on.
func TestRunKeepsBothErrors(t *testing.T) {
	d := idleDriver(t)
	d.err = errors.New("the GPU lost its context")
	// A render thread that runs until Close stops it, so the touch screen's
	// going is what ends Run.
	d.done = make(chan struct{})
	go func() {
		<-d.quit
		close(d.done)
	}()
	d.lost = make(chan error, 1)
	d.lose(ErrTouchGone)
	err := d.Run(context.Background(), func() {})
	if !errors.Is(err, ErrTouchGone) || !errors.Is(err, d.err) {
		t.Fatalf("Run returned %v, want both", err)
	}
}
