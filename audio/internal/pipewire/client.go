//go:build linux && !android && (amd64 || arm64)

package pipewire

import (
	"errors"
	"fmt"
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// maxQuantum is the largest cycle the rings make room for, beyond Hold:
// PipeWire's largest quantum.
const maxQuantum = 8192

// openWait is how long Open waits for the server to take the streams.
const openWait = 3 * time.Second

// A Client plays a stream to the default output, and records one from
// the default input where Config.Record says. Write, Read and Poll are
// one goroutine's; the rest are safe from any.
type Client struct {
	a    *api
	loop uintptr
	st   *rtState
	log  func(format string, args ...any)
	// events are the streams' pw_stream_events, version 0, held here
	// while PipeWire reads them.
	events [2][10]uintptr
	// out and in are the rings' samples, held here while the streams
	// write and read them through st.
	out, in []float32
	size    uint64
	info    Info
	// What Poll last read, and Read: the cycles read.
	polled struct{ short, empty, lost, outStates, inStates uint64 }
	failed string
	seen   uint64
	// mu guards closed, so Close and Err's call into the library come
	// one at a time.
	mu     sync.Mutex
	closed bool
}

// Available reports whether the library loads and a server listens.
func Available() bool {
	if _, err := load(); err != nil {
		return false
	}
	return testAPI != nil || serverListens()
}

// Open opens a client of the server, as cfg says, and starts its
// streams. It returns once the server has taken them.
func Open(cfg Config) (*Client, error) {
	a, err := load()
	if err != nil {
		return nil, err
	}
	if a != testAPI && !serverListens() {
		return nil, errNoServer
	}
	rate := cfg.Rate
	if rate <= 0 {
		rate = 48000
	}
	quantum := cfg.Quantum
	if quantum <= 0 {
		quantum = 256
	}
	quantum = min(quantum, maxQuantum)
	cl := &Client{a: a, st: new(rtState), log: cfg.Log,
		info: Info{Library: a.version, Rate: rate, Quantum: quantum, Recording: cfg.Record}}
	hold := max(cfg.Hold, 0)
	cl.size = 1 << bits.Len64(uint64(int64(hold)*int64(rate)/int64(time.Second)+2*maxQuantum+4096-1))
	cl.out = make([]float32, int(cl.size)*Channels)
	st := cl.st
	st.dequeue, st.queue, st.getTime = a.dequeue, a.queue, a.getTime
	st.fallback, st.on = uint64(quantum), 1
	st.outBuf, st.outMask = uintptr(unsafe.Pointer(&cl.out[0])), cl.size-1
	if cfg.Record {
		cl.in = make([]float32, int(cl.size)*Channels)
		st.inBuf, st.inMask = uintptr(unsafe.Pointer(&cl.in[0])), cl.size-1
	}
	st.inState = uint64(StateUnconnected)
	name := cfg.Name
	if name == "" {
		name = programName()
	}
	cl.loop = a.loopNew("gunim-pipewire", 0)
	if cl.loop == 0 {
		return nil, errors.New("pipewire: making a thread loop failed")
	}
	if a.loopStart(cl.loop) != 0 {
		a.loopDestroy(cl.loop)
		return nil, errors.New("pipewire: starting the thread loop failed")
	}
	cl.locked(func() { err = cl.connect(name, rate, quantum, cfg.Record) })
	if err == nil {
		err = cl.wait()
	}
	if err != nil {
		_ = cl.Close()
		return nil, err
	}
	return cl, nil
}

func (cl *Client) logf(format string, args ...any) {
	if cl.log != nil {
		cl.log(format, args...)
	}
}

// connect makes the streams and connects them. It runs with the loop
// locked.
func (cl *Client) connect(name string, rate, quantum int, record bool) error {
	a, st := cl.a, cl.st
	pod := formatPod(rate)
	params := [1]unsafe.Pointer{unsafe.Pointer(&pod[0])}
	defer runtime.KeepAlive(pod)
	open := func(k int, category, media string, state, process uintptr, dir uint32) (uintptr, error) {
		ev := &cl.events[k]
		ev[2], ev[8] = state, process
		props := a.propsNewString(fmt.Sprintf("media.type=Audio media.category=%s media.name=%s "+
			"node.name=%s application.name=%s node.latency=\"%d/%d\"",
			category, media, quote(name), quote(name), quantum, rate))
		if props == 0 {
			return 0, errors.New("pipewire: making the stream's properties failed")
		}
		// The stream takes the properties, made or not.
		s := a.streamNew(a.loopGetLoop(cl.loop), name, props, ev, unsafe.Pointer(st))
		if s == 0 {
			return 0, fmt.Errorf("pipewire: making the %s stream failed", media)
		}
		if r := a.streamConnect(s, dir, anyID, flagAutoconnect|flagMapBuffers|flagRTProcess,
			&params[0], 1); r < 0 {
			return s, fmt.Errorf("pipewire: connecting the %s stream failed: %w", media, unix.Errno(-r))
		}
		return s, nil
	}
	var err error
	st.outStream, err = open(0, "Playback", "playback", procs.outState, procs.process, directionOutput)
	if err != nil || !record {
		return err
	}
	st.inStream, err = open(1, "Capture", "recording", procs.inState, procs.capture, directionInput)
	return err
}

// wait waits for the server to take the streams: each paused or
// streaming. It returns the error a stream met.
func (cl *Client) wait() error {
	st := cl.st
	ready := func(v *uint64, s uintptr) (bool, error) {
		if s == 0 {
			return true, nil
		}
		switch State(int64(atomic.LoadUint64(v))) {
		case StatePaused, StateStreaming:
			return true, nil
		case StateError:
			return false, cl.Err()
		case StateUnconnected, StateConnecting:
		}
		return false, nil
	}
	deadline := time.Now().Add(openWait)
	for {
		outOK, err := ready(&st.outState, st.outStream)
		if err != nil {
			return err
		}
		inOK, err := ready(&st.inState, st.inStream)
		if err != nil {
			return err
		}
		if outOK && inOK {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pipewire: the server took no stream in %v", openWait)
		}
		time.Sleep(time.Millisecond)
	}
}

// locked runs f with the thread loop locked. The lock is a mutex of
// the thread that takes it, and PipeWire checks that the thread
// calling it holds it: f runs on the one thread that locks and
// unlocks, which a goroutine otherwise may leave between calls.
func (cl *Client) locked(f func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cl.a.loopLock(cl.loop)
	defer cl.a.loopUnlock(cl.loop)
	f()
}

// Info says how the client plays.
func (cl *Client) Info() Info { return cl.info }

// Write adds frames, of Channels interleaved samples, to the ring the
// output plays from, as many as it has room for, and returns how many.
func (cl *Client) Write(frames []float32) int {
	st := cl.st
	w := atomic.LoadUint64(&st.outW)
	n := min(uint64(len(frames)/Channels), cl.size-(w-atomic.LoadUint64(&st.outR)))
	at := w & st.outMask
	first := min(n, cl.size-at)
	copy(cl.out[at*Channels:], frames[:first*Channels])
	copy(cl.out, frames[first*Channels:n*Channels])
	atomic.StoreUint64(&st.outW, w+n)
	return int(n)
}

// Queued returns how many frames written the output has not taken.
func (cl *Client) Queued() int64 {
	r := atomic.LoadUint64(&cl.st.outR)
	return int64(atomic.LoadUint64(&cl.st.outW) - r)
}

// Room returns how many frames Write has room for.
func (cl *Client) Room() int64 { return int64(cl.size) - cl.Queued() }

// Taken returns how many frames written the output has taken, ever.
func (cl *Client) Taken() int64 { return int64(atomic.LoadUint64(&cl.st.outR)) }

// snapshot is the output's last cycle, as its process function left
// it: next, the ring's count once it took its frames; took, the frames
// the cycle played; and the cycle's time report.
type snapshot struct {
	next, took uint64
	t          pwTime
}

// snap reads the output's last cycle. ok is false before its first.
func (cl *Client) snap() (s snapshot, ok bool) {
	st := cl.st
	for try := 0; ; try++ {
		seq := atomic.LoadUint64(&st.seq)
		if seq == 0 {
			return s, false
		}
		if seq%2 == 0 {
			s.next = atomic.LoadUint64(&st.outR)
			s.took = atomic.LoadUint64(&st.took)
			s.t = loadTime(&st.outTime)
			if atomic.LoadUint64(&st.seq) == seq {
				return s, true
			}
		}
		if try%64 == 63 {
			runtime.Gosched()
		}
	}
}

// loadTime reads a time report a process function writes.
func loadTime(p *pwTime) pwTime {
	return pwTime{
		now:        atomic.LoadInt64(&p.now),
		rateNum:    atomic.LoadUint32(&p.rateNum),
		rateDenom:  atomic.LoadUint32(&p.rateDenom),
		ticks:      atomic.LoadUint64(&p.ticks),
		delay:      atomic.LoadInt64(&p.delay),
		queued:     atomic.LoadUint64(&p.queued),
		buffered:   atomic.LoadUint64(&p.buffered),
		queuedBufs: atomic.LoadUint32(&p.queuedBufs),
		availBufs:  atomic.LoadUint32(&p.availBufs),
		size:       atomic.LoadUint64(&p.size),
	}
}

// delayNs returns t's delay in nanoseconds.
func delayNs(t pwTime) int64 {
	if t.rateDenom == 0 {
		return 0
	}
	return t.delay * int64(t.rateNum) * int64(time.Second) / int64(t.rateDenom)
}

// framesIn returns how many frames at rate pass in ns nanoseconds,
// rounded to the nearest.
func framesIn(ns int64, rate int) int64 {
	f := ns * int64(rate)
	if f < 0 {
		return -((-f + int64(time.Second)/2) / int64(time.Second))
	}
	return (f + int64(time.Second)/2) / int64(time.Second)
}

// outDelay returns the frames from the output's ring to their being
// heard, as of the cycle s: the delay to the device, the frames
// queued in the stream and those its resampler holds.
func (cl *Client) outDelay(s snapshot) int64 {
	return framesIn(delayNs(s.t), cl.info.Rate) + int64(s.t.queued) + int64(s.t.buffered)
}

// monotonic is CLOCK_MONOTONIC, in nanoseconds: PipeWire's clock. A
// test puts a clock of its own here.
var monotonic = func() int64 {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		return 0
	}
	return ts.Nano()
}

// Held returns how many frames written are still to be heard: those in
// the ring, and those past it, less the frames played since the last
// cycle began, as far as a cycle's. Before the first cycle it is the
// frames in the ring.
func (cl *Client) Held() int64 {
	q := cl.Queued()
	s, ok := cl.snap()
	if !ok {
		return q
	}
	since := min(max(framesIn(monotonic()-s.t.now, cl.info.Rate), 0), int64(s.took))
	return max(int64(atomic.LoadUint64(&cl.st.outW)-s.next)+cl.outDelay(s)-since, 0)
}

// HeardAt returns the frame written, as Taken counts them, that is
// heard at ns on CLOCK_MONOTONIC, before or after now, as the output's
// last cycle places it. Past that cycle's start it moves on as far as
// the cycle's frames, as Held does: while the output's cycles stall the
// frame heard waits, and never passes the frames taken. ok is false
// before the first cycle.
func (cl *Client) HeardAt(ns int64) (frame int64, ok bool) {
	s, ok := cl.snap()
	if !ok {
		return 0, false
	}
	since := min(framesIn(ns-s.t.now, cl.info.Rate), int64(s.took))
	return int64(s.next) - cl.outDelay(s) + since, true
}

// Read takes what the input recorded in the oldest cycle not yet read,
// as many frames as dst holds, of Channels interleaved samples. It
// returns how many, and at, the frame written that was heard as the
// first of them reached the input, as HeardAt counts it. ok is false
// where nothing waits. Where Read falls behind by logSize cycles, those
// cycles are lost. What the input records before the output's first
// cycle is dropped: nothing places it on the output's time line.
func (cl *Client) Read(dst []float32) (n int, at int64, ok bool) {
	st := cl.st
	if cl.in == nil || len(dst) < Channels {
		return 0, 0, false
	}
	for {
		cycles := atomic.LoadUint64(&st.inCycles)
		if cl.seen == cycles {
			return 0, 0, false
		}
		if cycles-cl.seen >= logSize {
			// The log has moved past the cycles not read.
			cl.seen = cycles - logSize/2
			continue
		}
		e := &st.log[cl.seen&(logSize-1)]
		in0, k, got := atomic.LoadUint64(&e.in), atomic.LoadUint64(&e.n), atomic.LoadUint64(&e.got)
		t := loadTime(&e.time)
		if atomic.LoadUint64(&st.inCycles)-cl.seen >= logSize {
			// Written over as it was read.
			continue
		}
		r := max(atomic.LoadUint64(&st.inR), in0)
		heard, placed := cl.HeardAt(t.now - delayNs(t))
		if !placed {
			r = in0 + k
		}
		if r >= in0+k {
			cl.seen++
			atomic.StoreUint64(&st.inR, r)
			continue
		}
		m := min(in0+k-r, uint64(len(dst)/Channels))
		at := r & st.inMask
		first := min(m, cl.size-at)
		copy(dst, cl.in[at*Channels:(at+first)*Channels])
		copy(dst[first*Channels:], cl.in[:(m-first)*Channels])
		atomic.StoreUint64(&st.inR, r+m)
		if r+m == in0+k {
			cl.seen++
		}
		// The last frame the cycle handed reached the stream the delay
		// and what the resampler holds before the cycle began; frame r
		// came the frames between them before that.
		return int(m), heard - int64(t.buffered+got-(r-in0)), true
	}
}

// Poll returns what happened since it was last called.
func (cl *Client) Poll() Events {
	st := cl.st
	delta := func(v *uint64, last *uint64) int64 {
		now := atomic.LoadUint64(v)
		d := int64(now - *last)
		*last = now
		return d
	}
	e := Events{
		Short:  delta(&st.short, &cl.polled.short),
		Empty:  delta(&st.empty, &cl.polled.empty),
		Lost:   delta(&st.lost, &cl.polled.lost),
		Cycles: int64(atomic.LoadUint64(&st.cycles)),
		Out:    State(int64(atomic.LoadUint64(&st.outState))),
		In:     State(int64(atomic.LoadUint64(&st.inState))),
	}
	e.Moved = delta(&st.outStates, &cl.polled.outStates)+delta(&st.inStates, &cl.polled.inStates) > 0
	if e.Out == StateError || e.In == StateError {
		if cl.failed == "" {
			cl.failed = cl.errorText()
			cl.logf("pipewire: a stream failed: %s", cl.failed)
		}
		e.Error = cl.failed
	}
	if s, ok := cl.snap(); ok {
		e.Quantum = int(s.took)
		if s.t.rateNum > 0 {
			e.DeviceRate = int(s.t.rateDenom / s.t.rateNum)
		}
		e.Delay = int(cl.outDelay(s) - int64(s.t.queued))
	}
	return e
}

// errorText returns what PipeWire said as a stream failed.
func (cl *Client) errorText() string {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.closed {
		return ""
	}
	text := ""
	cl.locked(func() {
		for _, s := range []uintptr{cl.st.outStream, cl.st.inStream} {
			if s == 0 {
				continue
			}
			var msg unsafe.Pointer
			if State(cl.a.streamState(s, &msg)) == StateError {
				if text = cString(msg); text == "" {
					text = "unknown error"
				}
				return
			}
		}
	})
	return text
}

// Pause stops the output taking from the ring, and plays silence, or
// starts it again.
func (cl *Client) Pause(paused bool) {
	on := uint64(1)
	if paused {
		on = 0
	}
	atomic.StoreUint64(&cl.st.on, on)
}

// Err returns ErrStopped, with what PipeWire said, once a stream has
// failed.
func (cl *Client) Err() error {
	st := cl.st
	if State(int64(atomic.LoadUint64(&st.outState))) != StateError &&
		State(int64(atomic.LoadUint64(&st.inState))) != StateError {
		return nil
	}
	if text := cl.errorText(); text != "" {
		return fmt.Errorf("%w: %s", ErrStopped, text)
	}
	return ErrStopped
}

// Close stops the streams and lets the client go. Calls after the
// first return nil.
func (cl *Client) Close() error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.closed {
		return nil
	}
	cl.closed = true
	a, st := cl.a, cl.st
	// Destroying a stream takes it off the real-time thread first: no
	// process function runs after.
	cl.locked(func() {
		for _, s := range []uintptr{st.outStream, st.inStream} {
			if s != 0 {
				a.streamDestroy(s)
			}
		}
	})
	a.loopStop(cl.loop)
	a.loopDestroy(cl.loop)
	// The server's threads read these until the streams went.
	runtime.KeepAlive(st)
	runtime.KeepAlive(&cl.events)
	runtime.KeepAlive(cl.out)
	runtime.KeepAlive(cl.in)
	return nil
}
