//go:build linux && (arm64 || amd64)

// Package kms is gunim's driver for a screen with no window system: one
// full-screen UI drawn straight to the display through the Linux kernel's
// DRM/KMS, with touch read from evdev. It is made for a Raspberry Pi 5
// with the Touch Display 2, and works on any Linux screen with KMS and
// Mesa.
//
// # Drawing
//
// The driver draws with gunim's own renderer on the GPU, through OpenGL
// ES 3. A GBM surface on the display's card gives the buffers, EGL the
// context, and an atomic commit shows each frame at the next vblank, with
// a page-flip event when it does. The engine draws its next frame on that
// event, so frames go at the panel's rate while something moves, and none
// go while nothing does. The driver tells the engine of a flip a lead before
// the next vblank, not at once, so the next frame reads touches as late as
// it can and still makes that vblank. It learns the lead from the frames: a
// screen that redraws little starts its frames late in the refresh, and one
// heavy on the GPU starts them at once. GUNIM_KMS_LEAD sets a fixed lead in
// milliseconds, for measuring. libgbm, libEGL and libGLESv2 load at run time
// through purego, so the build stays CGO_ENABLED=0; the program then needs
// Mesa's libraries on the machine: on Raspberry Pi OS,
//
//	apt install libgbm1 libegl1 libegl-mesa0 libgles2
//
// Where the GPU fails, the driver asks Mesa to draw on the CPU instead,
// and [Window.Software] says so. Where that fails too, [Open] returns the
// error.
//
// # The screen
//
// The driver opens each /dev/dri/card* and takes a connected screen: the
// one [Options.Connector] names, or else a DSI panel, or else an HDMI
// screen. A card with no connectors, such as the GPU's own, only renders.
// The first program to open a card is the one that sets its modes, the
// DRM master; the driver reports where another program has the screen.
//
// A screen mounted another way up than its own, as a portrait panel shown
// in landscape, is drawn turned: [Options.Rotate].
//
// # The virtual terminal
//
// The kernel's text console draws on the same screen. The driver puts its
// VT in graphics mode, where the console leaves the screen alone, and puts
// it back on exit: see [Options.VT]. The kernel asks before it switches
// VT, and the driver lets go of the screen first and takes it back on
// return.
//
// # Touch
//
// The driver reads the touch screen's multi-touch events (protocol B) from
// /dev/input. The first finger drives the pointer, marked as touch, so the
// engine makes taps, scrolls, flings and long presses of it; a window that
// takes fingers gets each finger as an [input.Finger] too.
package kms

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/internal/compose"
	"github.com/marrasen/gunim/geom"
)

// Options configures the screen. The zero value suits a Touch Display 2
// in portrait.
type Options struct {
	// Connector names the screen, as "DSI-2" or "HDMI-A-1". Empty prefers
	// a DSI panel, then the first connected HDMI screen.
	Connector string
	// Rotate turns the picture on the screen, clockwise: 0, 90, 180 or
	// 270. 90 shows a portrait panel in landscape with its top along the
	// panel's right edge.
	Rotate int
	// Scale is device pixels per logical pixel. Zero works it out from
	// the panel's size: the nearest quarter of its pixels per inch over
	// 160, as Android does.
	Scale float32
	// Touch names the touch screen by its /dev/input path or its name.
	// Empty finds it.
	Touch string
	// Backlight names the backlight under /sys/class/backlight. Empty
	// takes the first.
	Backlight string
	// Brightness sets the backlight from 0 to 1 at start. Zero keeps the
	// level it has. The driver puts back the level it found on exit.
	Brightness float32
	// Blank is when an untouched screen dims and turns off. The zero
	// value keeps it on. The first touch on a dim or dark screen wakes it
	// and reaches no window.
	Blank Blank
	// VT is the virtual terminal to draw on: empty for the one the program
	// runs on, if it runs on one, "free" for a free one, or a number. The
	// driver switches to a VT other than the one showing, and back on
	// exit. A program started over SSH runs on no VT: with VT empty it
	// leaves the VTs alone, and "free" keeps the text console of the VT
	// showing out of its way. With no VT taken, a keyboard still types
	// into the login on the VT behind the picture.
	//
	// The driver puts the VT, and the backlight's level, back as it found
	// them when Run ends: on its context, the last window closing, and on
	// SIGHUP, SIGINT, SIGTERM and SIGQUIT, which it catches itself. It
	// catches SIGPIPE too, so a closed stdout or stderr fails the write in
	// place of ending the process with the screen held. Once it is putting
	// things back, a second such signal ends the process at once. SIGKILL,
	// os.Exit, a panic on a goroutine other than the render thread and a
	// crash in Mesa's C code skip that. The kernel still frees the screen
	// then, and the next run puts the VT and the backlight back first,
	// from the state file it keeps under /run while it runs. Under systemd,
	// TTYReset=yes with TTYPath= sets the unit's own VT back to text mode
	// with a working keyboard before and after each run; it covers neither
	// a "free" VT nor the backlight. A unit that is not root keeps the state
	// file in its RuntimeDirectory=, which systemd removes as the unit stops,
	// crash or not: such a unit needs RuntimeDirectoryPreserve=restart for
	// the next run to find it.
	VT string
	// VTSwitch takes Ctrl+Alt+F1 to F12 from a keyboard, which the VT's
	// own keyboard handling would, to switch VT. Keyboards are found as
	// the driver opens; one plugged in later is not read yet. The switch
	// waits on the render thread to let go of the screen, so a render
	// thread stuck on a hung GPU holds every VT switch.
	VTSwitch bool
	// Software draws with Mesa's software renderer even where there is a
	// GPU.
	Software bool
	// Record writes the touch screen's events to a file, for Replay. It is
	// for measuring the driver, as tools/kmsbench does.
	Record string
	// Replay plays a recording made with Record through the driver's own
	// input path, as if a finger made it, in place of the touch screen.
	// The driver closes it when it has played.
	Replay io.ReadCloser
}

// Driver is the KMS implementation of [driver.Driver].
type Driver struct {
	o     Options
	turn  turn
	scr   *screen
	rt    *renderThread
	vt    *vt
	light *backlight
	touch *inputDev
	keys  []*inputDev
	rec   *recorder
	stats *timeline
	// trace is the file GUNIM_KMS_TRACE writes to.
	trace *os.File
	// sigs hears the signals on which Run gives the screen back, and pipes
	// SIGPIPE, which it only keeps from ending the process.
	sigs, pipes chan os.Signal
	// state is the state file, locked while the run holds the screen.
	state *stateFile
	// lightMu guards light: a write to the backlight goes over I²C, and
	// holds up nothing else. lightTo carries the step the render thread
	// wants to the goroutine that writes it, and lightDone ends that.
	lightMu sync.Mutex
	lightTo chan int
	// lit is the step the backlight was last written at: a step that
	// failed to write is asked again.
	lit       atomic.Int32
	lightDone chan struct{}
	lightWG   sync.WaitGroup
	// errs carries the errors of what the driver does on its own, as the
	// backlight's writes while it fades; see Errors.
	errs chan error

	// panelW and panelH are the panel's own size in pixels; picW and picH
	// the picture's, turned.
	panelW, panelH int
	picW, picH     int
	density        float32
	rate           float64
	software       bool
	// renderer is GL's name for what draws.
	renderer string
	// lead, where GUNIM_KMS_LEAD sets it, is a fixed time before a vblank
	// at which the windows hear of the flip before it. 0 has the pacer
	// learn it from the frames.
	lead time.Duration

	// wake is an eventfd that wakes the render thread from its poll, and
	// cmds the work it is woken for besides frames.
	// wakeMu guards wake against a kick after release closed it.
	wakeMu sync.RWMutex
	wake   int
	cmds   chan func()
	// stop is an eventfd that ends the input readers' poll.
	stop int

	mu sync.Mutex
	// windows is the stack of open windows, bottom first, and gone the
	// panes of windows closed, for the render thread to free.
	windows []*Window
	gone    []*compose.Pane
	// handed is when a frame was handed over with none waiting, for the
	// figures. touchAt is when the oldest touch no frame has answered yet
	// came.
	handed  time.Time
	touchAt time.Time
	// input is the touch in progress; see touchReport.
	input touchState
	// blank is the screen's brightness and blanking.
	blank *blanker
	// away says another VT has the screen: touches then reach no window.
	away bool
	// clipboard is the process's own: there is no system to share it with.
	clipboard string

	// readers counts the input readers running, which end as stop is
	// written, before their files close.
	readers sync.WaitGroup

	quit     chan struct{}
	quitOnce sync.Once
	// done closes as the render thread ends, with err why, if it failed.
	done chan struct{}
	err  error
	// ended closes once Run has put the screen back.
	ended   chan struct{}
	endOnce sync.Once
}

// Open takes the screen and starts drawing on it: black, until a window
// opens. On error it gives back what it took. [Driver.Run] or
// [Driver.Close] gives back the rest.
func Open(o Options) (*Driver, error) {
	t := turn(o.Rotate)
	if !t.valid() {
		return nil, fmt.Errorf("kms: Rotate is %d: want 0, 90, 180 or 270", o.Rotate)
	}
	d := &Driver{o: o, turn: t, cmds: make(chan func(), 8), quit: make(chan struct{}), done: make(chan struct{}),
		ended: make(chan struct{}), wake: -1, stop: -1, errs: make(chan error, 8),
		lightTo: make(chan int, 1), lightDone: make(chan struct{})}
	if err := d.open(); err != nil {
		// The render thread, if it started, has ended.
		d.quitOnce.Do(func() { close(d.quit) })
		d.release()
		return nil, fmt.Errorf("kms: %w", err)
	}
	return d, nil
}

// open takes what Open takes, in order. What it took stays for release.
func (d *Driver) open() error {
	o := d.o
	d.catchSignals()
	sf, left, found, err := lockState()
	switch {
	case errors.Is(err, errStateHeld):
		// A live run holds the screen. Its VT and backlight are its own:
		// stop before touching either.
		return err
	case err != nil:
		log.Printf("gunim: kms: %v; a crash would leave the VT for the next run to find as it is", err)
	case found:
		// The VT that run took is put back, unless this run takes the same
		// one: then it takes it as that run left it; see openVT.
		reuse := false
		if num, take, _ := whichVT(o.VT, &left); take && left.VT > 0 && num == left.VT {
			reuse = true
			log.Printf("gunim: kms: a run before this one ended without giving the screen back; taking its VT %d again, and putting back the rest", num)
		} else {
			log.Print("gunim: kms: a run before this one ended without giving the screen back; putting back what it took")
		}
		if err := repair(left, !reuse); err != nil {
			log.Printf("gunim: kms: %v", err)
		}
	}
	var leftp *state
	if found {
		leftp = &left
	}
	d.state = sf
	if p := os.Getenv("GUNIM_KMS_TRACE"); p != "" {
		f, err := os.Create(p)
		if err != nil {
			return fmt.Errorf("GUNIM_KMS_TRACE: %w", err)
		}
		d.trace = f
	}
	if d.trace != nil {
		d.stats = newTimeline(d.trace)
	} else {
		d.stats = newTimeline(nil)
	}
	if o.Software {
		// Before Mesa first loads, so it reads it with no thread of its own
		// running.
		if err := softwareEnv(); err != nil {
			return err
		}
	}
	// The backlight is read first, and the state file written before the
	// VT switches, so a run that dies on the way leaves both for the next.
	if d.light, err = openBacklight(o.Backlight); err != nil {
		return err
	}
	d.light = usableBacklight(d.light)
	st := state{}
	if d.light != nil {
		st.Backlight, st.Level = d.light.dir, d.light.was
	}
	if d.vt, err = openVT(o.VT, leftp, func(v *vt) error {
		st.VT, st.Prev, st.Opened = v.num, v.prev, v.opened
		return d.state.write(st)
	}); err != nil {
		return err
	}
	if d.vt == nil {
		if err := d.state.write(st); err != nil {
			log.Printf("gunim: kms: %v", err)
		}
	}
	if d.scr, err = findScreen(o); err != nil {
		return err
	}
	d.panelW, d.panelH = d.scr.width(), d.scr.height()
	d.picW, d.picH = d.turn.size(d.panelW, d.panelH)
	d.rate = d.scr.mode.rate()
	if v, err := strconv.ParseFloat(os.Getenv("GUNIM_KMS_LEAD"), 64); err == nil {
		d.lead = time.Duration(v * float64(time.Millisecond))
	}
	d.density = o.Scale
	if d.density <= 0 {
		d.density = density(d.panelW, d.scr.conn.mmWidth)
	}
	level := float32(1)
	if d.light != nil {
		if o.Brightness > 0 {
			if err := d.light.set(o.Brightness); err != nil {
				return err
			}
		}
		level = d.light.brightness()
		d.lit.Store(int32(d.light.cur))
		d.startLight()
	}
	if o.Brightness > 0 {
		level = min(o.Brightness, 1)
	}
	d.blank = newBlanker(o.Blank, level, time.Now())
	if d.touch, d.keys, err = findInputs(o.Touch, o.Replay == nil, o.VTSwitch); err != nil {
		return err
	}
	if d.touch != nil && o.Record != "" {
		if d.rec, err = newRecorder(o.Record, d.touch); err != nil {
			return fmt.Errorf("record touch: %w", err)
		}
	}
	if d.wake, err = unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK); err != nil {
		return fmt.Errorf("eventfd: %w", err)
	}
	if d.stop, err = unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK); err != nil {
		return fmt.Errorf("eventfd: %w", err)
	}
	started := make(chan error, 1)
	go d.render(started)
	if err := <-started; err != nil {
		<-d.done
		return err
	}
	log.Printf("gunim: kms: %s on %s (%s), %dx%d at %.2f Hz, %d×%d mm, scale %.2f, turned %d°; %s",
		d.scr.conn.name(), d.scr.card.path, d.scr.card.name, d.panelW, d.panelH, d.rate,
		d.scr.conn.mmWidth, d.scr.conn.mmHeight, d.density, o.Rotate, d.inputsLine())
	d.startInput()
	return nil
}

// stopSignals are the signals that end Run, which gives the screen back,
// in place of ending the process with the screen held.
var stopSignals = []os.Signal{syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT}

// catchSignals catches stopSignals for Run, and SIGPIPE, which would end
// the process on a write to a closed stdout or stderr.
func (d *Driver) catchSignals() {
	d.sigs = make(chan os.Signal, 1)
	signal.Notify(d.sigs, stopSignals...)
	d.pipes = make(chan os.Signal, 1)
	signal.Notify(d.pipes, syscall.SIGPIPE)
}

// uncatchSignals gives stopSignals their default handling back, so a
// second one ends the process, and with pipes stops catching SIGPIPE too,
// once the screen is back. It is safe to call twice. signal.Reset clears
// every handler for those signals in the process, the application's own
// too: one that still wants them after Close, as to save work on SIGTERM
// while it ends, calls signal.Notify again.
func (d *Driver) uncatchSignals(pipes bool) {
	if d.sigs != nil {
		signal.Stop(d.sigs)
		signal.Reset(stopSignals...)
	}
	if pipes && d.pipes != nil {
		signal.Stop(d.pipes)
	}
}

// density works out the scale for a panel w pixels wide and mm millimetres
// wide: the nearest quarter of its pixels per inch over 160, and 1 where
// the panel doesn't say its size.
func density(w int, mm uint32) float32 {
	if mm == 0 || w == 0 {
		return 1
	}
	ppi := float64(w) / (float64(mm) / 25.4)
	return float32(max(1, math.Round(ppi/160*4)/4))
}

func (d *Driver) inputsLine() string {
	var s string
	switch {
	case d.o.Replay != nil:
		s = "touch from a recording"
	case d.touch != nil:
		s = fmt.Sprintf("touch %q on %s, %d fingers", d.touch.name, d.touch.path, d.touch.slots)
	default:
		s = "no touch screen"
	}
	if d.vt != nil {
		s += fmt.Sprintf("; VT %d", d.vt.num)
	}
	if d.light != nil {
		s += fmt.Sprintf("; backlight %d of %d", d.light.cur, d.light.max)
	}
	s += "; drawn by " + d.renderer
	if d.software {
		s += ", on the CPU"
	}
	return s
}

// Run implements [driver.Driver]. The render thread and the input readers
// pump the events, so Run waits for ctx to end, the last window to close,
// the render thread to fail, or SIGHUP, SIGINT or SIGTERM. It then gives
// back the screen, the VT and the backlight as it found them. A signal is
// a normal end, as ctx ending is: Run returns nil for it.
func (d *Driver) Run(ctx context.Context, ready func()) error {
	ready()
	var stop <-chan time.Time
	if os.Getenv("GUNIM_DEBUG_FRAMES") != "" {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		stop = t.C
	}
	for {
		select {
		case <-ctx.Done():
		case <-d.quit:
		case <-d.done:
		case s := <-d.sigs:
			log.Printf("gunim: kms: %v: giving the screen back", s)
		case <-stop:
			d.logSecond()
			continue
		}
		break
	}
	return d.Close()
}

// logSecond logs the last second's frames, for GUNIM_DEBUG_FRAMES.
func (d *Driver) logSecond() {
	s := d.stats.second()
	n, missed, slow, f, t, sent, drawn := s.frames, s.missed, s.slow, s.frame, s.touch, s.sent, s.drawn
	if n == 0 {
		return
	}
	line := fmt.Sprintf("gunim: kms: %d frames, %d missed, %d slow, frame p50 %.1f ms p99 %.1f ms", n, missed, slow, ms(f.P50), ms(f.P99))
	if t.N > 0 {
		line += fmt.Sprintf(", touch to flip p50 %.1f ms p99 %.1f ms", ms(t.P50), ms(t.P99))
	}
	if drawn > 0 {
		k := float64(drawn)
		line += fmt.Sprintf("; a window's frame: %.0f quads, %.1f layers, %.1f flushes, %.0f %% redrawn in %.1f boxes, %.2f MB",
			float64(sent.Quads)/k, float64(sent.Layers)/k, float64(sent.Flushes)/k, 100*sent.Redrawn/k, float64(sent.Boxes)/k, float64(sent.Bytes)/k/(1<<20))
	}
	log.Print(line)
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// Close stops the render thread and gives back the screen, the VT and the
// backlight as the driver found them, for a program that opened the driver
// and ends without running it. It is safe to call more than once, and
// after Run, which calls it; it returns the error the render thread ended
// on, if any.
func (d *Driver) Close() error {
	d.endOnce.Do(func() {
		// From here a second signal ends the process at once, should
		// putting things back hang.
		d.uncatchSignals(false)
		d.quitOnce.Do(func() { close(d.quit) })
		d.kick()
		select {
		case <-d.done:
			d.releaseDevices()
		case <-time.After(closeWait):
			// The render thread is stuck, as in a hung GPU's ioctl: its card
			// and buffers stay for the process's end to free.
			log.Printf("gunim: kms: the render thread did not stop within %v; putting the VT and the backlight back without it", closeWait)
		}
		d.releaseHeld()
		close(d.ended)
	})
	return d.err
}

// closeWait is how long Close waits for the render thread before it puts
// the VT and the backlight back without it; a test shortens it.
var closeWait = 3 * time.Second

// release gives back everything Open took, for an Open that failed.
func (d *Driver) release() {
	d.releaseDevices()
	d.releaseHeld()
}

// releaseDevices gives back the input and the card, whose last close has
// the kernel show its text console again. It runs once the render thread
// has ended.
func (d *Driver) releaseDevices() {
	d.quitOnce.Do(func() { close(d.quit) })
	// A recording still being read ends as it closes, and the readers as
	// stop is written.
	if d.o.Replay != nil {
		_ = d.o.Replay.Close()
	}
	if d.stop >= 0 {
		one := putUint64(1)
		_, _ = unix.Write(d.stop, one[:])
	}
	d.readers.Wait()
	if d.rec != nil {
		if err := d.rec.close(); err != nil {
			log.Printf("gunim: kms: touch recording: %v", err)
		}
	}
	if d.touch != nil {
		d.touch.close()
	}
	for _, k := range d.keys {
		k.close()
	}
	if d.scr != nil {
		d.scr.close()
	}
	d.wakeMu.Lock()
	for _, fd := range []int{d.wake, d.stop} {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
	}
	d.wake, d.stop = -1, -1
	d.wakeMu.Unlock()
}

// releaseHeld puts back what outlives the process, the backlight's level
// and the VT, and removes the state file once both are back. Where either
// fails, the file stays for the next run to finish the job.
func (d *Driver) releaseHeld() {
	d.stopLight()
	d.lightMu.Lock()
	errLight := d.light.restore()
	d.lightMu.Unlock()
	errVT := d.vt.close()
	if err := errors.Join(errLight, errVT); err != nil {
		log.Printf("gunim: kms: %v; the state file stays for the next run", err)
		d.state.keep()
	} else {
		d.state.done()
	}
	d.state = nil
	if d.trace != nil {
		_ = d.trace.Close()
		d.trace = nil
	}
	d.uncatchSignals(true)
}

// Closed closes once the driver has given the screen back.
func (d *Driver) Closed() <-chan struct{} { return d.ended }

// Stats returns the figures since the driver opened or ResetStats.
func (d *Driver) Stats() Stats { return d.stats.stats() }

// ResetStats starts the figures again.
func (d *Driver) ResetStats() { d.stats.reset() }

// Monitors implements [driver.Driver]: the one screen, turned as the
// picture is.
func (d *Driver) Monitors() []driver.Monitor {
	b := geom.Rect{Max: geom.Pt(float32(d.picW), float32(d.picH))}
	return []driver.Monitor{{
		Name: d.scr.conn.name(), Bounds: b, WorkArea: b, RefreshRate: d.rate,
		Scale: d.density, CoordsPerLogical: d.density, Primary: true,
	}}
}

// NewWindow implements [driver.Driver]. The first window fills the
// screen. A popup opens by its anchor, a utility window at its anchor, and
// any other window fills the screen over the ones before it.
func (d *Driver) NewWindow(o driver.Options) (driver.Window, error) {
	select {
	case <-d.quit:
		return nil, errors.New("kms: the driver has closed")
	default:
	}
	w := newWindow(d, o)
	d.mu.Lock()
	defer d.mu.Unlock()
	if p, ok := o.Parent.(*Window); ok {
		w.parent = p
	}
	switch {
	case o.Kind == driver.KindPopup && w.parent != nil:
		w.size = o.Size
		w.pos = compose.Place(o.Anchor.Add(w.parent.pos), w.size, w.over, w.above, d.screenLocked())
		if debugBlank {
			log.Printf("gunim: kms: a popup of %v opens at %v", w.size, w.pos)
		}
	case o.Kind == driver.KindUtility && w.parent != nil:
		w.size = o.Size
		w.pos = o.Anchor.Min.Add(w.parent.pos)
	default:
		w.fills = true
	}
	w.hidden = o.Hidden
	d.windows = append(d.windows, w)
	d.kick()
	return w, nil
}

// screenLocked returns the picture in logical pixels. It runs with mu
// held, though nothing in it changes.
func (d *Driver) screenLocked() geom.Rect {
	return geom.Rect{Max: geom.Pt(float32(d.picW)/d.density, float32(d.picH)/d.density)}
}

// windowClosed takes a window off the stack, for the render thread to free its
// pane. The last window closing ends Run.
func (d *Driver) windowClosed(w *Window) {
	d.mu.Lock()
	for i, x := range d.windows {
		if x == w {
			d.windows = append(d.windows[:i], d.windows[i+1:]...)
			d.gone = append(d.gone, &w.pane)
			break
		}
	}
	last := len(d.windows) == 0
	d.input.gest.Forget(w)
	d.keepAwakeLocked()
	d.mu.Unlock()
	d.kick()
	if last {
		d.quitOnce.Do(func() { close(d.quit) })
	}
}

// keepAwakeLocked holds the screen awake while any window asks. It runs
// with mu held.
func (d *Driver) keepAwakeLocked() {
	keep := false
	for _, w := range d.windows {
		keep = keep || w.keepAwake && !w.closed
	}
	d.blank.setKeep(keep, time.Now())
}

// SetBlank changes when an untouched screen dims and turns off, counting
// from now. The zero Blank keeps it on, and wakes one that is dim or off.
func (d *Driver) SetBlank(b Blank) {
	d.mu.Lock()
	now := time.Now()
	d.blank.o, d.blank.last = b, now
	if b == (Blank{}) {
		d.blank.wake(now)
	}
	d.mu.Unlock()
	d.kick()
}

// Blanked reports whether the screen is off: faded to black, with the
// backlight at 0 and the display controller off, and no frames drawn.
func (d *Driver) Blanked() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.blank.state == screenOff
}

// DisplayOn asks the kernel whether the display controller is on: the
// screen's CRTC active. It is false while the screen is blanked.
func (d *Driver) DisplayOn() (bool, error) {
	ps, err := d.scr.card.props(d.scr.crtc, objCrtc)
	if err != nil {
		return false, fmt.Errorf("kms: %w", err)
	}
	return ps["ACTIVE"].value != 0, nil
}

// Errors carries the errors of what the driver does on its own, such as a
// write to the backlight that failed while the screen faded. The driver
// keeps the last few that nobody has read.
func (d *Driver) Errors() <-chan error { return d.errs }

// fail hands err to Errors, or drops it where the channel is full.
func (d *Driver) fail(err error) {
	select {
	case d.errs <- err:
	default:
	}
}

// startLight starts the goroutine that writes the backlight's steps the
// render thread asks for, so a slow write never holds a frame.
func (d *Driver) startLight() {
	d.lightWG.Add(1)
	go func() {
		defer d.lightWG.Done()
		for {
			select {
			case n := <-d.lightTo:
				d.lightMu.Lock()
				err := d.light.setLevel(n)
				d.lightMu.Unlock()
				if err != nil {
					d.fail(err)
				} else {
					d.lit.Store(int32(n))
				}
			case <-d.lightDone:
				return
			}
		}
	}()
}

// wantLight asks for the backlight at step n, in place of a step asked
// for and not yet written. Only the render thread asks.
func (d *Driver) wantLight(n int) {
	select {
	case <-d.lightTo:
	default:
	}
	d.lightTo <- n
}

// stopLight ends the backlight's goroutine, once it has written what it
// was asked. It is safe to call twice.
func (d *Driver) stopLight() {
	if d.lightDone == nil {
		return
	}
	select {
	case <-d.lightDone:
		return
	default:
	}
	close(d.lightDone)
	d.lightWG.Wait()
}

// kick wakes the render thread.
func (d *Driver) kick() {
	d.wakeMu.RLock()
	defer d.wakeMu.RUnlock()
	if d.wake < 0 {
		return
	}
	one := putUint64(1)
	// A full count already wakes the thread.
	_, _ = unix.Write(d.wake, one[:])
}

// do runs fn on the render thread.
func (d *Driver) do(fn func()) {
	select {
	case d.cmds <- fn:
		d.kick()
	case <-d.done:
	}
}

// Software reports whether the CPU draws, as where the GPU failed; see
// [Window.Software].
func (d *Driver) Software() bool { return d.software }
