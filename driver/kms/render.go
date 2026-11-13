//go:build linux && (arm64 || amd64)

package kms

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/internal/compose"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/internal/gl"
)

// lcdLag is how long the panel's liquid crystal takes to change, after
// scan-out reaches a line: an estimate, which slow-motion video of the
// screen checks.
const lcdLag = 8 * time.Millisecond

// flipWait is how long the render thread waits for a flip it must see
// before it lets go of the screen.
const flipWait = 200 * time.Millisecond

// flipTimeout is how long a commit waits for its flip event before the
// render thread gives up, as for an event the kernel lost: the run ends
// with an error, for systemd to start it again, in place of a screen that
// silently stops.
const flipTimeout = 500 * time.Millisecond

// renderThread is the render thread's own state. One goroutine, locked to
// its OS thread, owns the EGL context and the card: it draws, commits, and
// waits in poll on the card for the flip event and on an eventfd for new
// work.
type renderThread struct {
	d *Driver
	g *gpu
	c *compose.Compositor
	// gl is the context the compositor draws with.
	gl gl.Context
	// fbs is the framebuffer made of each GBM buffer, made once each.
	fbs map[uintptr]uint32
	// shown is the buffer on the screen, and pending the one committed,
	// whose flip is on its way. flying says a commit waits for its flip.
	shown, pending uintptr
	flying         bool
	// set says the mode is set; a commit after the screen came back from
	// another VT sets it again. away says another VT has the screen.
	set, away bool
	// rotation is the plane's rotation property value, where the plane
	// turns the picture itself (rotate says it does).
	rotation uint64
	rotate   bool
	// flight is the windows whose frames are in the commit on its way.
	flight []*Window
	// held is the windows whose frames the last flip showed, which hear
	// so at releaseAt, with heldFrame.
	held      []*Window
	heldFrame driver.Frame
	releaseAt time.Time
	// paced says the windows heard of the last flip late, by pace's lead,
	// at releasedAt.
	paced      bool
	releasedAt time.Time
	pace       pacer
	// jobs, stack and gone are the frame's, kept from frame to frame; see
	// collect.
	jobs  []job
	stack []compose.Layer
	gone  []*compose.Pane
	// For the figures: when the frame on its way was handed over and
	// committed, the touch it answers, and the flip before it.
	handed, committed, touchAt time.Time
	prevFlip                   time.Time
	prevSeq                    uint32
	events                     [1024]byte
}

// render runs the render thread. It reports on started once the screen
// shows its first frame, or why it could not.
func (d *Driver) render(started chan<- error) {
	runtime.LockOSThread()
	defer close(d.done)
	// A panic here ends the run as an error, so Run still puts the screen,
	// the VT and the backlight back.
	defer func() {
		if p := recover(); p != nil {
			d.err = fmt.Errorf("render thread: %v", p)
			log.Printf("gunim: kms: %v\n%s", d.err, debug.Stack())
			select {
			case started <- d.err:
			default:
			}
		}
	}()
	r := &renderThread{d: d, fbs: map[uintptr]uint32{}}
	d.rt = r
	if err := r.start(); err != nil {
		r.stop()
		started <- err
		return
	}
	started <- nil
	if d.vt != nil {
		go d.watchVT()
	}
	err := r.loop()
	r.stop()
	if err != nil {
		log.Printf("gunim: kms: %v", err)
		d.err = err
	}
}

// start makes the GL context, with the GPU or else on the CPU, and shows
// a black frame, which sets the mode.
func (r *renderThread) start() error {
	d := r.d
	if v, err := d.scr.card.cap(capTimestampMonotonic); err != nil || v != 1 {
		log.Print("gunim: kms: the card's flip times are not on the monotonic clock: the figures are off")
	}
	onCPU, err := startDrawing(d.o.Software, func(software bool) error {
		g, err := startGPU(d.scr.card.fd, d.panelW, d.panelH, software)
		if err != nil {
			return err
		}
		if !software && os.Getenv("GUNIM_KMS_FAIL_GPU") != "" {
			g.stop()
			return errors.New("GUNIM_KMS_FAIL_GPU is set")
		}
		if r.gl, err = loadGL(g); err != nil {
			g.stop()
			return err
		}
		r.g = g
		return nil
	})
	if err != nil {
		return err
	}
	if r.g.software && !onCPU {
		log.Printf("gunim: kms: Mesa draws on the CPU (%s): the GPU's driver did not load", r.g.renderer)
	}
	d.software = r.g.software
	d.renderer = r.g.renderer
	r.c, err = compose.New(r.gl, "kms")
	if err != nil {
		return err
	}
	if v, ok := d.scr.planeRotation(int(d.turn)); ok && (d.turn == 0 || d.turn == 180) {
		r.rotation, r.rotate = v, true
		r.c.SetTurn(0)
	} else {
		if v, ok := d.scr.planeRotation(0); ok {
			r.rotation, r.rotate = v, true
		}
		r.c.SetTurn(int(d.turn))
	}
	// The first frame is black, and sets the mode.
	r.gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
	r.gl.Viewport(0, 0, int32(d.panelW), int32(d.panelH))
	r.gl.ClearColor(0, 0, 0, 1)
	r.gl.Clear(0x4000)
	r.gl.ClearColor(0, 0, 0, 0)
	r.handed = time.Now()
	return r.commit()
}

// startDrawing makes the GL context with try: on the GPU, or else, where
// that fails or where software asks, on the CPU. It reports whether it
// asked for the CPU. The fallback runs in the same process, after the
// GPU's attempt has let go of what it took.
func startDrawing(software bool, try func(software bool) error) (onCPU bool, err error) {
	if !software {
		if err = try(false); err == nil {
			return false, nil
		}
		log.Printf("gunim: kms: no GPU, so the CPU draws: %v", err)
	}
	if err2 := try(true); err2 != nil {
		return true, errors.Join(err, fmt.Errorf("software drawing: %w", err2))
	}
	return true, nil
}

// loadGL loads OpenGL ES's functions for the context made current.
func loadGL(g *gpu) (gl.Context, error) {
	c, err := gl.NewDefaultContext()
	if err == nil {
		err = c.LoadFunctions()
	}
	if err != nil {
		return nil, fmt.Errorf("OpenGL ES %s from %s: %w", g.version, g.vendor, err)
	}
	return c, nil
}

// loop serves frames and the driver's work until the driver quits.
func (r *renderThread) loop() error {
	d := r.d
	fds := []unix.PollFd{{Fd: int32(d.scr.card.fd), Events: unix.POLLIN}, {Fd: int32(d.wake), Events: unix.POLLIN}}
	var wake [8]byte
	for {
		fds[0].Revents, fds[1].Revents = 0, 0
		var wait time.Duration = -1
		if r.flying {
			wait = flipTimeout
		}
		if len(r.held) > 0 {
			if w := time.Until(r.releaseAt); wait < 0 || w < wait {
				wait = max(w, 0)
			}
		}
		_, err := ppoll(fds, wait)
		if err != nil && !errors.Is(err, unix.EINTR) {
			return fmt.Errorf("poll: %w", err)
		}
		if fds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return errors.New("the card is gone")
		}
		if fds[0].Revents&unix.POLLIN != 0 {
			if err := d.scr.card.readEvents(r.events[:], r.flipped); err != nil {
				return fmt.Errorf("read the card's events: %w", err)
			}
		}
		// After the events, so a flip that came late but came counts.
		if r.flying && time.Since(r.committed) >= flipTimeout {
			return fmt.Errorf("no flip event %v after a commit", flipTimeout)
		}
		if fds[1].Revents&unix.POLLIN != 0 {
			_, _ = unix.Read(d.wake, wake[:]) // only empties it
		}
		if len(r.held) > 0 && !time.Now().Before(r.releaseAt) {
			r.release()
		}
		for {
			select {
			case fn := <-d.cmds:
				fn()
				continue
			default:
			}
			break
		}
		select {
		case <-d.quit:
			r.waitFlip()
			return nil
		default:
		}
		if !r.flying && !r.away {
			if err := r.frame(); err != nil {
				return err
			}
		}
	}
}

// waitFlip waits for the flip on its way, for a while, so its buffer can
// go back.
func (r *renderThread) waitFlip() {
	deadline := time.Now().Add(flipWait)
	fds := []unix.PollFd{{Fd: int32(r.d.scr.card.fd), Events: unix.POLLIN}}
	for r.flying && time.Now().Before(deadline) {
		fds[0].Revents = 0
		if _, err := unix.Poll(fds, int(flipWait/time.Millisecond)); err != nil && err != unix.EINTR {
			return
		}
		if fds[0].Revents&unix.POLLIN != 0 {
			if err := r.d.scr.card.readEvents(r.events[:], r.flipped); err != nil {
				break
			}
		}
	}
	r.release()
}

// pacer learns how late in a refresh the windows can start a frame and
// still have it reach the next vblank, so that frame reads touches as late
// as it can. Its lead is how long before the vblank they hear of the flip
// before it. It starts at a whole refresh, which tells them at once, and
// shortens a step at a time while frames make their vblank, a little at a
// time so misses stay rare. A frame that misses lengthens it at once by
// more, and holds it there a while: the frame's cost, the GPU's above all,
// sets how late a frame can start.
type pacer struct {
	lead time.Duration
	// good counts frames made since the lead last moved, and hold how many
	// more to make before it may shorten again.
	good, hold int
}

const (
	paceStep    = 500 * time.Microsecond // shorter, after paceEvery frames made
	paceEvery   = 30
	paceBack    = 3 * time.Millisecond // longer, on a miss
	paceHold    = 1800                 // frames before shortening again
	paceMinLead = 4 * time.Millisecond
)

// now returns the lead for the next frame: fixed, where GUNIM_KMS_LEAD sets
// it, else the one learnt.
func (p *pacer) now(fixed, period time.Duration) time.Duration {
	if fixed > 0 {
		return fixed
	}
	if p.lead <= 0 || p.lead > period {
		p.lead = period
	}
	return p.lead
}

// flipped learns from a flip: whether its frame was drawn straight after
// the flip before, whether the windows heard of that flip late, and how
// many vblanks the frame missed.
func (p *pacer) flipped(straight, paced bool, missed int, period time.Duration) {
	if !straight {
		return
	}
	if missed > 0 && paced {
		p.lead = min(p.lead+paceBack, period)
		p.good, p.hold = 0, paceHold
		return
	}
	if p.hold > 0 {
		p.hold--
		return
	}
	p.good++
	if p.good >= paceEvery {
		p.good = 0
		p.lead = max(p.lead-paceStep, paceMinLead)
	}
}

// frameStart returns when the engine could start the frame now flipping:
// as the flip before came, or where the driver held that flip's report back
// for the pacer, as it let it go. A frame is drawn straight after the one
// before where it was handed over within a refresh of that: so a frame
// that took longer to build than the lead left it, and missed its vblank,
// counts as a miss the pacer learns from, not as one after a rest.
func frameStart(prevFlip, released time.Time, paced bool) time.Time {
	if paced && !released.IsZero() {
		return released
	}
	return prevFlip
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// ppoll waits on fds for up to wait, or for ever where wait is negative,
// to the microsecond.
func ppoll(fds []unix.PollFd, wait time.Duration) (int, error) {
	if wait < 0 {
		return unix.Ppoll(fds, nil, nil)
	}
	ts := unix.NsecToTimespec(int64(wait))
	return unix.Ppoll(fds, &ts, nil)
}

// release tells the windows whose frames the last flip showed that it
// did, so they draw their next frame.
func (r *renderThread) release() {
	r.releasedAt = time.Now()
	for _, w := range r.held {
		select {
		case w.presented <- r.heldFrame:
		case <-w.quit:
		case <-r.d.quit:
		}
	}
	clear(r.held)
	r.held = r.held[:0]
}

// collect takes, under the driver's lock, the frames waiting into r.jobs
// and the windows showing into r.stack, and the panes of windows closed,
// which belong to the render thread from then on. It keeps the slices from
// frame to frame, so a frame allocates nothing here.
func (r *renderThread) collect() (gone []*compose.Pane, handed, touchAt time.Time) {
	d := r.d
	d.mu.Lock()
	defer d.mu.Unlock()
	r.jobs, r.stack = r.jobs[:0], r.stack[:0]
	r.gone = append(r.gone[:0], d.gone...)
	clear(d.gone)
	d.gone = d.gone[:0]
	scale := d.density
	for _, w := range d.windows {
		rect := w.held.Rect(w.rectLocked(), w.hasNext)
		fbW, fbH := int(math.Round(float64(rect.Size().W*scale))), int(math.Round(float64(rect.Size().H*scale)))
		switch {
		case w.hasNext:
			j := job{w: w, f: w.next, fbW: fbW, fbH: fbH, under: w.under}
			if fbW > 0 && fbH > 0 {
				j.shot, w.shot = w.shot, nil
			}
			r.jobs = append(r.jobs, j)
			w.next, w.hasNext = frame{}, false
		case w.sharpen && w.pane.Last != nil:
			r.jobs = append(r.jobs, job{w: w, f: frame{ops: w.pane.Last}, fbW: fbW, fbH: fbH, under: w.under, again: true})
		}
		w.sharpen = false
		if !w.hidden && !w.stale {
			x, y := int(math.Round(float64(rect.Min.X*scale))), int(math.Round(float64(rect.Min.Y*scale)))
			r.stack = append(r.stack, compose.Layer{Pane: &w.pane, X: x, Y: y, W: fbW, H: fbH, Under: w.under})
		}
	}
	handed, touchAt = d.handed, d.touchAt
	if len(r.jobs) > 0 {
		d.handed, d.touchAt = time.Time{}, time.Time{}
	}
	return r.gone, handed, touchAt
}

// job is a window's frame to draw.
type job struct {
	w        *Window
	f        frame
	fbW, fbH int
	under    color.NRGBA
	shot     func(*image.RGBA)
	// again marks the window's last frame drawn again, to sharpen what it
	// drew stretched; the engine waits for none.
	again bool
}

// frame draws the frames waiting, lays the windows on the screen, and
// commits the result, if anything changed.
func (r *renderThread) frame() error {
	d := r.d
	gone, handed, touchAt := r.collect()
	for _, p := range gone {
		r.c.Release(p)
	}
	jobs, stack := r.jobs, r.stack
	scale := d.density
	if len(jobs) == 0 && !r.c.Dirty(stack) {
		return nil
	}
	if handed.IsZero() {
		handed = time.Now()
	}
	c := r.c
	c.Begin(stack, d.picW, d.picH)
	for _, j := range jobs {
		if !c.Draw(&j.w.pane, j.f.ops, j.f.damage, j.fbW, j.fbH, j.under, scale) {
			if j.shot != nil {
				j.w.keepShot(j.shot)
			}
			continue
		}
		if j.shot != nil {
			c.Shoot(&j.w.pane, j.fbW, j.fbH, j.shot)
		}
	}
	c.Finish(stack, d.picW, d.picH, scale)
	r.flight = r.flight[:0]
	for _, j := range jobs {
		if !j.again {
			r.flight = append(r.flight, j.w)
		}
	}
	r.handed, r.touchAt = handed, touchAt
	return r.commit()
}

// commit ends the frame drawn, and shows it from the next vblank: it sets
// the mode on the first frame, and after the screen comes back from
// another VT.
func (r *renderThread) commit() error {
	d := r.d
	bo, err := r.g.swap()
	if err != nil {
		var e eglErr
		if errors.As(err, &e) && e.code == eglContextLost {
			return fmt.Errorf("the GPU lost its context: %w", err)
		}
		return err
	}
	// Mesa keeps a surface's buffers for the surface's life, so a buffer's
	// address names it for as long as the cache lives.
	fb, ok := r.fbs[bo]
	if !ok {
		handle, stride, w, h, format := boInfo(bo)
		fb, err = d.scr.card.addFB(w, h, format, handle, stride)
		if err != nil {
			r.g.release(bo)
			return fmt.Errorf("add a framebuffer: %w", err)
		}
		r.fbs[bo] = fb
	}
	if !r.set {
		err = d.scr.modeset(fb, r.rotation, r.rotate, 1)
		r.set = err == nil
	} else {
		err = d.scr.flip(fb, 1)
	}
	if err != nil {
		r.g.release(bo)
		return err
	}
	r.committed = time.Now()
	r.pending, r.flying = bo, true
	return nil
}

// flipped takes a flip event: the frame committed is on the screen. The
// buffer shown before goes back to the surface, and each window whose
// frame it held hears so.
func (r *renderThread) flipped(ev flipEvent) {
	if !r.flying {
		return
	}
	d := r.d
	at := monoTime(time.Duration(ev.sec)*time.Second + time.Duration(ev.usec)*time.Microsecond)
	period := time.Duration(float64(time.Second) / d.rate)
	missed := 0
	if r.prevSeq != 0 && ev.sequence > r.prevSeq+1 {
		missed = int(ev.sequence - r.prevSeq - 1)
	}
	from := frameStart(r.prevFlip, r.releasedAt, r.paced)
	straight := !from.IsZero() && r.handed.Sub(from) < period
	d.stats.shown(ev.sequence, from, r.handed, r.committed, at, r.touchAt, straight, missed)
	r.prevFlip, r.prevSeq = at, ev.sequence
	r.pace.flipped(straight, r.paced, missed, period)
	r.g.release(r.shown)
	r.shown, r.pending, r.flying = r.pending, 0, false
	// The windows hear of the flip lead before the next vblank, not at
	// once, so the frame they draw next reads touches that late, and
	// still reaches the screen at that vblank.
	r.heldFrame = driver.Frame{Shown: at, Lag: period/2 + lcdLag}
	r.held = append(r.held, r.flight...)
	r.flight = r.flight[:0]
	r.releaseAt = time.Time{}
	now := time.Now()
	if lead := r.pace.now(r.d.lead, period); lead < period {
		// A flip stamped off the monotonic clock could put the release far
		// ahead: never more than a refresh from now.
		r.releaseAt = minTime(at.Add(period-lead), now.Add(period))
	}
	r.paced = now.Before(r.releaseAt)
	if !r.paced {
		r.release()
	}
}

// monoTime turns a time on the monotonic clock, as the kernel stamps
// flips and touches with, into a time.Time.
func monoTime(mono time.Duration) time.Time {
	now := time.Now()
	var ts unix.Timespec
	if unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts) != nil {
		return now
	}
	return now.Add(mono - time.Duration(ts.Nano()))
}

// stop lets go of every buffer and framebuffer, and the context. The card
// stays open for release to close.
func (r *renderThread) stop() {
	d := r.d
	if r.c != nil {
		d.mu.Lock()
		panes := d.gone
		d.gone = nil
		for _, w := range d.windows {
			panes = append(panes, &w.pane)
		}
		d.mu.Unlock()
		for _, p := range panes {
			r.c.Release(p)
		}
	}
	if r.g != nil {
		r.g.release(r.pending)
		r.g.release(r.shown)
		r.pending, r.shown = 0, 0
		for bo, fb := range r.fbs {
			_ = d.scr.card.rmFB(fb) // the card frees it as it closes, if not
			delete(r.fbs, bo)
		}
		r.g.stop()
	}
}

// watchVT takes the kernel's asks to switch VT, and lets go of the screen
// or takes it back on the render thread.
func (d *Driver) watchVT() {
	for {
		select {
		case <-d.quit:
			return
		case s := <-d.vt.signals:
			switch s {
			case syscall.SIGUSR1:
				d.do(d.leaveVT)
			case syscall.SIGUSR2:
				d.do(d.returnVT)
			}
		}
	}
}

// leaveVT lets go of the screen for another VT: it waits for the flip on
// its way, drops DRM master, and tells the kernel to switch.
func (d *Driver) leaveVT() {
	r := d.rt
	r.waitFlip()
	r.away = true
	if err := d.scr.card.dropMaster(); err != nil {
		log.Printf("gunim: kms: let go of the screen: %v", err)
	}
	if err := d.vt.released(); err != nil {
		log.Printf("gunim: kms: switch VT: %v", err)
	}
	d.pushAll(input.WindowHidden{Time: time.Now()})
}

// returnVT takes the screen back as its VT shows again: DRM master, then
// the mode, with every window drawn whole.
func (d *Driver) returnVT() {
	r := d.rt
	if err := d.vt.acquired(); err != nil {
		log.Printf("gunim: kms: take the VT back: %v", err)
	}
	if err := d.scr.card.setMaster(); err != nil {
		log.Printf("gunim: kms: take the screen back: %v", err)
	}
	r.away, r.set = false, false
	r.prevFlip, r.prevSeq = time.Time{}, 0
	d.pushAll(input.WindowShown{Time: time.Now()}, driver.Redraw{})
}

// pushAll sends evs to every window.
func (d *Driver) pushAll(evs ...any) {
	d.mu.Lock()
	ws := append([]*Window(nil), d.windows...)
	d.mu.Unlock()
	for _, w := range ws {
		for _, ev := range evs {
			w.in.Push(ev)
		}
	}
}

// putUint64 is the eventfd's count, as it takes it.
func putUint64(n uint64) [8]byte {
	var b [8]byte
	binary.NativeEndian.PutUint64(b[:], n)
	return b
}
