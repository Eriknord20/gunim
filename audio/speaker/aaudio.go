package speaker

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/asio"
	"github.com/marrasen/gunim/audio/internal/aaudio"
	"github.com/marrasen/gunim/audio/internal/duplex"
	"github.com/marrasen/gunim/audio/rt"
)

// AAudio is the name [Options].Driver takes to play through AAudio,
// Android's own sound API, from Android 11, in place of oto: in
// low-latency mode, with the device to itself where it allows. With
// [Options].Record set it records the phone's microphone too, or the
// headset's, for package audio/capture, stamped with the mixer frame
// heard as each block came in, as an ASIO driver's inputs are; the
// application needs the microphone permission first, which gunim's
// App.Ask asks for. On another system, or an older Android, it plays
// through the system's sound as a driver that fails to open does.
const AAudio = "AAudio"

// aaudioFirst plays through AAudio where Options names no driver, in
// place of oto, on the Androids it plays on.
const aaudioFirst = false

// micName names the AAudio input to package audio/capture.
const micName = "Microphone"

// An aaStream is an AAudio stream, as package audio/internal/aaudio
// opens it; a test opens one of its own.
type aaStream interface {
	Start() error
	Pause() error
	Close() error
	Timestamp() (pos, at, now int64, ok bool)
	Shared() *aaudio.Shared
	Info() aaudio.Info
	XRuns() int64
}

// aaConfig says how to open a stream, as aaudio.Config does.
type aaConfig struct {
	input       bool
	rate, chans int
	exclusive   bool
	data        *aaudio.Ring[float32]
	blocks      *aaudio.Ring[int64]
}

// aaClock is the clock an output reads; a test puts its own here.
var aaClock = nanotime

// openAAStream opens an AAudio stream, where the system has AAudio;
// nil elsewhere.
var openAAStream func(c aaConfig) (aaStream, error)

// aaRun is a stream open, and what the output's goroutine has seen of
// it.
type aaRun struct {
	st   aaStream
	sh   *aaudio.Shared
	info aaudio.Info
	// start is the output ring's read count as the stream started: the
	// stream's frame 0 is ring frame start.
	start int64
	// underruns and xruns are the goroutine's count of those seen, and
	// frames the frames counted at moved, on its clock, to tell a stream
	// that stopped; resume sets moved too.
	underruns, xruns int64
	frames           int64
	// odd counts the timestamps in a row that did not fit the frames,
	// and fitted says one has.
	odd    int
	fitted bool
	// opened is when the stream opened, on the output's clock.
	opened int64
	moved  atomic.Int64
}

// aaOutput plays through AAudio. AAudio's thread takes each buffer from
// a ring in C memory, and a goroutine keeps the ring filled from the
// mixer, a chunk at a time, as ringOutput's feed does. Where Record is
// set, AAudio's thread puts each block recorded in another ring, which
// the goroutine stamps and hands to package audio/capture. Each stream
// tells by its timestamps which frame the device plays or records when,
// on one clock, which places the frames heard, and the frame heard as
// each block recorded came in. The goroutine opens a stream again where
// its device goes away, as a headset unplugged.
type aaOutput struct {
	s      *Speaker
	hz     int
	byName bool
	// clock is the clock the goroutine and held read, in nanoseconds,
	// as aaClock was as it opened.
	clock func() int64
	// q holds the frames to play, in pairs; in and blocks the frames
	// recorded, mono, and each block's first frame and length.
	q      *aaudio.Ring[float32]
	in     *aaudio.Ring[float32]
	blocks *aaudio.Ring[int64]
	tap    *duplex.Tap
	// base is the mixer frame of the ring's frame 0.
	base int64
	// started says start has run: streams open from then on are of
	// the speaker opened, at its rate.
	started bool
	// run and irun are the streams playing and recording, nil while
	// they are closed. mu guards opening and closing them, and paused.
	mu        sync.Mutex
	run, irun atomic.Pointer[aaRun]
	paused    atomic.Bool
	closed    atomic.Bool
	readers   atomic.Int64
	ahead     atomic.Int64
	target    atomic.Int64
	primed    atomic.Bool
	// settle is when the output stream last started or resumed.
	settle     atomic.Int64
	delay      atomic.Int64
	inDelay    atomic.Int64
	org, iorg  atomic.Uint64
	problem    atomic.Pointer[error]
	iproblem   atomic.Pointer[error]
	wake, quit chan struct{}
	done       chan struct{}
	// Opening again: the goroutine's own count of attempts that failed in
	// a row, and when it may try next.
	fails, ifails int
	next, inext   int64
}

// The output's rings and timings.
const (
	// aaPoll is how often the goroutine reads the streams' timestamps.
	aaPoll = 50 * time.Millisecond
	// aaStalled is how long a stream started may take no buffer before
	// it is opened again, as one whose device went away untold.
	aaStalled = time.Second
	// aaTick is how often the goroutine tops up the ring.
	aaTick = 2 * time.Millisecond
	// aaJump is how far a timestamp may place a device from where the
	// last ones did before it is taken as it is, unsmoothed.
	aaJump = 5 * time.Millisecond
	// aaSettle is how long a stream starting runs before a buffer it
	// finds short counts as the sound running dry.
	aaSettle = 250 * time.Millisecond
	// aaShift is how far a stream's delay may move from one timestamp
	// to the next: further, it counts its frames anew.
	aaShift = 100 * time.Millisecond
	// aaWaitStamp is how long what was recorded waits for the streams'
	// timestamps, to be stamped from them.
	aaWaitStamp = 500 * time.Millisecond
	// aaMostCallback is the most frames a callback is counted to ask
	// for at once, as a Bluetooth headset's large bursts do.
	aaMostCallback = 16384
)

// none marks an origin unknown.
var none = math.Float64bits(math.NaN())

// openAAudio opens AAudio's output, and its input where o.Record says,
// stopped; start starts them. byName says Options.Driver named it.
func openAAudio(s *Speaker, o Options, byName bool) (*aaOutput, error) {
	if openAAStream == nil {
		return nil, aaudio.ErrUnsupported
	}
	a := &aaOutput{s: s, hz: o.rate(), byName: byName, clock: aaClock,
		wake: make(chan struct{}, 1), quit: make(chan struct{}), done: make(chan struct{})}
	a.org.Store(none)
	a.iorg.Store(none)
	a.q = aaudio.NewRing[float32](int(audio.FramesAt(MaxLatency, a.hz))+2*aaMostCallback, 2)
	a.mu.Lock()
	err := a.openOut()
	a.mu.Unlock()
	if err != nil {
		a.q.Free()
		return nil, err
	}
	a.hz = a.run.Load().info.Rate
	if o.Record {
		a.in = aaudio.NewRing[float32](int(audio.FramesAt(time.Second, a.hz))+aaMostCallback, 1)
		a.blocks = aaudio.NewRing[int64](4096, 2)
		a.mu.Lock()
		if err := a.openIn(); err != nil {
			a.ifails = 1
			s.logf("%v; trying again as it plays", err)
		}
		a.mu.Unlock()
	}
	return a, nil
}

// openOut opens the output stream, stopped, as the device's own where
// it lets it. It runs with mu held.
func (a *aaOutput) openOut() error {
	c := aaConfig{rate: a.hz, chans: 2, exclusive: true, data: a.q}
	st, err := openAAStream(c)
	if err != nil {
		c.exclusive = false
		st, err = openAAStream(c)
	}
	if err != nil {
		a.problem.Store(&err)
		return err
	}
	info := st.Info()
	if a.started && info.Rate != a.hz {
		// A device at another rate: the speaker opens again at it, and
		// the mixer follows.
		_ = st.Close()
		a.s.askReset()
		return errors.New("speaker: the device plays at another rate")
	}
	a.problem.Store(nil)
	// What waited was mixed for the device gone; the new one starts
	// from the next frame mixed.
	a.q.Drop()
	run := &aaRun{st: st, sh: st.Shared(), info: info, start: a.q.Read(), opened: a.clock()}
	run.moved.Store(run.opened)
	a.settle.Store(run.opened)
	a.org.Store(none)
	a.delay.Store(int64(info.Buffer))
	a.primed.Store(false)
	a.run.Store(run)
	a.setTarget()
	a.s.logf("AAudio plays at %d Hz, %d frames a burst, %d in the device (of %d), device %d, exclusive %v, low latency %v",
		info.Rate, info.Burst, info.Buffer, info.Capacity, info.Device, info.Exclusive, info.LowLatency)
	return nil
}

// openIn opens the input stream, stopped. It runs with mu held.
func (a *aaOutput) openIn() error {
	c := aaConfig{input: true, rate: a.hz, chans: 1, exclusive: true, data: a.in, blocks: a.blocks}
	st, err := openAAStream(c)
	if err != nil {
		c.exclusive = false
		st, err = openAAStream(c)
	}
	if err == nil && st.Info().Rate != a.hz {
		_ = st.Close()
		err = errors.New("speaker: the microphone records at another rate than the output plays")
	}
	if err != nil {
		err = errors.Join(errors.New("speaker: the microphone did not open; the application may need the "+
			"permission to record"), err)
		a.iproblem.Store(&err)
		return err
	}
	a.iproblem.Store(nil)
	// Blocks of a stream gone count frames of their own.
	a.blocks.Drop()
	a.in.Drop()
	info := st.Info()
	a.iorg.Store(none)
	a.inDelay.Store(int64(info.Burst))
	run := &aaRun{st: st, sh: st.Shared(), info: info, opened: a.clock()}
	run.moved.Store(run.opened)
	a.irun.Store(run)
	a.s.logf("AAudio records at %d Hz, %d frames a burst, device %d, exclusive %v, low latency %v",
		info.Rate, info.Burst, info.Device, info.Exclusive, info.LowLatency)
	return nil
}

// dropOut closes the output stream, once nothing reads it. It runs
// with mu held.
func (a *aaOutput) dropOut() {
	run := a.run.Swap(nil)
	if run == nil {
		return
	}
	a.waitReaders()
	if err := run.st.Close(); err != nil {
		a.s.logf("%v", err)
	}
}

// dropIn closes the input stream. It runs with mu held.
func (a *aaOutput) dropIn() {
	if run := a.irun.Swap(nil); run != nil {
		a.waitReaders()
		if err := run.st.Close(); err != nil {
			a.s.logf("%v", err)
		}
	}
}

// waitReaders waits out held and the goroutine's stamps reading a
// stream just dropped.
func (a *aaOutput) waitReaders() {
	for a.readers.Load() > 0 {
		time.Sleep(10 * time.Microsecond)
	}
}

// enter marks a reader of the streams, and false once the output has
// closed; leave ends it.
func (a *aaOutput) enter() bool {
	a.readers.Add(1)
	if a.closed.Load() {
		a.readers.Add(-1)
		return false
	}
	return true
}

func (a *aaOutput) leave() { a.readers.Add(-1) }

// check says the stream p holds failed, as failed tells, reading it as
// a reader of the streams.
func (a *aaOutput) check(p *atomic.Pointer[aaRun], now int64) bool {
	if !a.enter() {
		return false
	}
	defer a.leave()
	run := p.Load()
	return run != nil && a.failed(run, now)
}

// start fills the ring and starts the streams, and the goroutine that
// keeps them going.
func (a *aaOutput) start(ahead time.Duration) {
	a.base = a.s.m.Mixed()
	a.setAhead(ahead)
	buf := make([]float32, 2*a.chunk())
	a.topUp(buf)
	if a.in != nil {
		a.tap = duplex.NewTap(AAudio, a.hz, a.burst(), int(a.inDelay.Load()), []string{micName})
		duplex.Publish(a.tap)
	}
	a.mu.Lock()
	a.started = true
	a.startOut()
	a.startIn()
	a.mu.Unlock()
	go a.loop(buf)
}

// startOut starts the output stream, where it is open, and closes it
// where it fails to start. It runs with mu held.
func (a *aaOutput) startOut() {
	if run := a.run.Load(); run != nil {
		if err := run.st.Start(); err != nil {
			a.s.logf("%v", err)
			a.dropOut()
		}
	}
}

// startIn starts the input stream as startOut does the output.
func (a *aaOutput) startIn() {
	if run := a.irun.Load(); run != nil {
		if err := run.st.Start(); err != nil {
			a.s.logf("%v", err)
			a.dropIn()
		}
	}
}

// chunk is the most frames the goroutine reads the mixer for at once.
func (a *aaOutput) chunk() int { return max(1, int(audio.FramesAt(chunk, a.hz))) }

func (a *aaOutput) burst() int {
	if run := a.run.Load(); run != nil {
		return run.info.Burst
	}
	return 0
}

// loop keeps the streams going until the output closes: it tops the
// ring up, reads the timestamps, hands on what was recorded, and opens
// a stream again whose device went away. It runs at the system's audio
// priority, where there is one, as ringOutput's feed does.
func (a *aaOutput) loop(buf []float32) {
	defer close(a.done)
	leave := rt.Enter()
	defer leave()
	t := time.NewTicker(aaTick)
	defer t.Stop()
	var ibuf []float32
	if a.in != nil {
		ibuf = make([]float32, a.in.Size())
	}
	var polled int64
	for {
		tick := t.C
		if a.paused.Load() {
			tick = nil
		}
		select {
		case <-a.quit:
			return
		case <-a.wake:
		case <-tick:
		}
		if a.paused.Load() {
			continue
		}
		now := a.clock()
		a.keepOpen(now, buf)
		// Suspend and Resume may close a stream meanwhile, once no
		// reader reads it.
		if !a.enter() {
			return
		}
		if now-polled >= int64(aaPoll) {
			polled = now
			a.poll()
		}
		a.topUp(buf)
		a.count(now)
		a.drain(ibuf, now)
		a.leave()
	}
}

// retryDelay is how long to wait before opening a stream again after
// fails attempts in a row failed: soon at first, for a device about to
// be ready, then once a second, for one gone a while.
func retryDelay(fails int) time.Duration {
	switch {
	case fails <= 1:
		return 10 * time.Millisecond
	case fails == 2:
		return 50 * time.Millisecond
	case fails < 10:
		return 100 * time.Millisecond
	}
	return time.Second
}

// keepOpen opens a stream again whose device went away, or that
// stopped taking buffers, and one that failed to open, when it may try
// next.
func (a *aaOutput) keepOpen(now int64, buf []float32) {
	if a.check(&a.run, now) {
		a.mu.Lock()
		if !a.paused.Load() {
			a.dropOut()
			a.next = now
		}
		a.mu.Unlock()
	}
	if a.run.Load() == nil && now >= a.next {
		a.mu.Lock()
		if a.run.Load() == nil && !a.paused.Load() {
			if err := a.openOut(); err != nil {
				a.fails++
				a.next = now + int64(retryDelay(a.fails))
				a.s.logf("%v; trying again in %v", err, retryDelay(a.fails))
			} else {
				a.fails = 0
				a.topUp(buf)
				a.startOut()
			}
		}
		a.mu.Unlock()
	}
	if a.in == nil {
		return
	}
	if a.check(&a.irun, now) {
		a.mu.Lock()
		if !a.paused.Load() {
			a.dropIn()
			a.inext = now
		}
		a.mu.Unlock()
	}
	if a.irun.Load() == nil && now >= a.inext {
		a.mu.Lock()
		if a.irun.Load() == nil && !a.paused.Load() {
			if err := a.openIn(); err != nil {
				// The permission to record may come later: try again,
				// every two seconds at most.
				a.ifails++
				a.inext = now + int64(min(retryDelay(a.ifails), 2*time.Second))
			} else {
				a.ifails = 0
				a.startIn()
			}
		}
		a.mu.Unlock()
	}
}

// failed says run's device went away, its timestamps no longer fit
// its frames, or it has taken no buffer for aaStalled.
func (a *aaOutput) failed(run *aaRun, now int64) bool {
	if e := run.sh.Err.Load(); e != 0 {
		a.s.logf("AAudio's stream failed, error %d; opening it again", e)
		return true
	}
	if run.odd >= 3 {
		a.s.logf("AAudio's stream counts its frames anew; opening it again")
		return true
	}
	if f := run.sh.Frames.Load(); f != run.frames {
		run.frames = f
		run.moved.Store(now)
		return false
	}
	if since := now - run.moved.Load(); since > int64(aaStalled) {
		a.s.logf("AAudio's stream has taken no buffer in %v; opening it again", time.Duration(since))
		return true
	}
	return false
}

// poll reads the streams' timestamps: where the device is in the frames
// it took, and how far behind the frames the callback carried.
func (a *aaOutput) poll() {
	if run := a.run.Load(); run != nil {
		if behind, ok := a.fit(run, &a.org, a.delay.Load(), false); ok {
			a.delay.Store(smooth(a.delay.Load(), behind))
		}
		if x := run.st.XRuns(); x > run.xruns {
			for range min(x-run.xruns, 100) {
				a.s.ranDry()
			}
			run.xruns = x
		}
		a.setTarget()
	}
	if run := a.irun.Load(); run != nil {
		if ahead, ok := a.fit(run, &a.iorg, a.inDelay.Load(), true); ok {
			a.inDelay.Store(smooth(a.inDelay.Load(), ahead))
		}
	}
}

// fit reads run's timestamp into org, the origin of its frames, where
// it fits the frames its callback carried: an output's device plays
// behind them, an input's records ahead of them, by as much as
// MaxLatency, and by two callbacks at most the other way; and, once a
// timestamp of run's has fitted, within aaShift of was, the delay they
// told. It returns how far. One that does not fit is let be, and
// counted in run.odd: a stream may count its frames anew under AAudio,
// as where the system's sound server restarted and the stream with it.
func (a *aaOutput) fit(run *aaRun, org *atomic.Uint64, was int64, input bool) (int64, bool) {
	pos, at, cnow, ok := run.st.Timestamp()
	if !ok || pos <= 0 {
		return 0, false
	}
	now := a.clock()
	// The timestamp on a.clock, as the two clocks stood just now.
	at -= cnow - now
	k := float64(a.hz) / 1e9
	raw := float64(pos) - float64(at)*k
	gap := func(org float64) int64 {
		g := run.sh.Frames.Load() - int64(org+float64(now)*k)
		if input {
			return -g
		}
		return g
	}
	slack := 2 * max(run.sh.Most.Load(), int64(run.info.Burst))
	g := gap(raw)
	if g < -slack || g > audio.FramesAt(MaxLatency, a.hz) || (run.fitted && abs64(g-was) > audio.FramesAt(aaShift, a.hz)) {
		run.odd++
		return 0, false
	}
	run.odd, run.fitted = 0, true
	return max(0, gap(a.steady(org, raw))), true
}

// steady moves the origin org holds an eighth of the way to now, as
// timestamps jitter, or right to it from none or where it is further
// off than aaJump, as after a stream paused; and returns it. An origin
// is the frame a stream's device would be at as the clock read 0: the
// frame there at time t is the origin plus t times the rate.
func (a *aaOutput) steady(org *atomic.Uint64, now float64) float64 {
	was := math.Float64frombits(org.Load())
	if !math.IsNaN(was) && math.Abs(now-was) < float64(audio.FramesAt(aaJump, a.hz)) {
		now = was + (now-was)/8
	}
	org.Store(math.Float64bits(now))
	return now
}

// smooth moves was a quarter of the way to now, as the delay a
// timestamp tells jumps by a burst as the callback runs.
func smooth(was, now int64) int64 { return was + (now-was)/4 }

// setTarget sets how full the goroutine keeps the ring: what ahead
// leaves past the device's delay, and enough for two of the largest
// callbacks and a chunk more at the least, as a Bluetooth headset's
// callbacks are large, and come late and two at once.
func (a *aaOutput) setTarget() {
	least := int64(a.chunk())
	if run := a.run.Load(); run != nil {
		least += 2 * max(run.sh.Most.Load(), int64(run.info.Burst))
	}
	t := max(audio.FramesAt(time.Duration(a.ahead.Load()), a.hz)-a.delay.Load(), least)
	a.target.Store(min(t, a.q.Size()-int64(a.chunk())))
}

// topUp tops the ring up to its target, a chunk at a time.
func (a *aaOutput) topUp(buf []float32) {
	if a.run.Load() == nil {
		return
	}
	for {
		want := a.target.Load() - a.q.Len()
		if want <= 0 {
			a.primed.Store(true)
			return
		}
		n := min(want, int64(len(buf)/2), a.q.Room())
		if n <= 0 {
			return
		}
		frames := buf[:2*n]
		a.s.pull(frames)
		a.q.Push(frames)
	}
}

// count counts the callbacks that found the ring short as the sound
// running dry, once it has been full, and the stream has run a while
// since it started or resumed: a stream starting may take several
// buffers at once, to fill the device.
func (a *aaOutput) count(now int64) {
	run := a.run.Load()
	if run == nil {
		return
	}
	if u := run.sh.Underruns.Load(); u > run.underruns {
		if a.primed.Load() && now-a.settle.Load() > int64(aaSettle) {
			for range min(u-run.underruns, 100) {
				a.s.ranDry()
			}
		}
		run.underruns = u
	}
}

// drain hands each block recorded to package audio/capture, stamped
// with the mixer frame heard as it came in. The blocks wait in the ring
// while the streams have yet to tell their timestamps, for aaWaitStamp
// at most, and are stamped from the latencies after that.
func (a *aaOutput) drain(buf []float32, now int64) {
	if a.in == nil {
		return
	}
	if irun := a.irun.Load(); irun != nil && now-irun.opened < int64(aaWaitStamp) &&
		(math.IsNaN(math.Float64frombits(a.org.Load())) || math.IsNaN(math.Float64frombits(a.iorg.Load()))) {
		return
	}
	var b [2]int64
	for a.blocks.Pop(b[:]) == 1 {
		n := int(min(b[1], int64(len(buf))))
		a.in.Pop(buf[:n])
		if a.tap != nil && a.tap.Attached() {
			a.tap.Deliver(buf[:n], 1, a.stamp(b[0]))
		}
	}
}

// stamp returns the mixer frame heard as the input's frame pos came in.
// The two streams' timestamps place pos on the clock, and the output's
// frame at the device then.
func (a *aaOutput) stamp(pos int64) int64 {
	if !a.enter() {
		return -1
	}
	defer a.leave()
	run, irun := a.run.Load(), a.irun.Load()
	if run == nil || irun == nil {
		return a.s.m.Heard()
	}
	now := a.clock()
	k := float64(a.hz) / 1e9
	out := math.Float64frombits(a.org.Load())
	if math.IsNaN(out) {
		out = float64(run.sh.Frames.Load()-a.delay.Load()) - float64(now)*k
	}
	in := math.Float64frombits(a.iorg.Load())
	if math.IsNaN(in) {
		in = float64(irun.sh.Frames.Load()+a.inDelay.Load()) - float64(now)*k
	}
	at := int64(math.Round(out - in + float64(pos)))
	return a.base + run.start + at - run.sh.Silence.Load()
}

// playing returns the output's frame at the device now, as its
// timestamp places it, and the frames the callback has carried.
func (a *aaOutput) playing(run *aaRun) (at, carried int64) {
	carried = run.sh.Frames.Load()
	org := math.Float64frombits(a.org.Load())
	if math.IsNaN(org) {
		return carried - a.delay.Load(), carried
	}
	at = int64(org + float64(a.clock())*float64(a.hz)/1e9)
	// Not past what it was given, as while it is paused, nor further
	// behind than the speaker ever works.
	return min(max(at, carried-audio.FramesAt(MaxLatency, a.hz)), carried), carried
}

func (a *aaOutput) held() int64 {
	if !a.enter() {
		return 0
	}
	defer a.leave()
	run := a.run.Load()
	if run == nil {
		return a.q.Len()
	}
	at, _ := a.playing(run)
	return max(0, a.q.Written()-run.start+run.sh.Silence.Load()-at)
}

func (a *aaOutput) setAhead(d time.Duration) {
	a.ahead.Store(int64(d))
	a.setTarget()
}

func (a *aaOutput) latency() time.Duration {
	return audio.DurationAt(a.target.Load()+a.delay.Load(), a.hz)
}

func (a *aaOutput) rate() int { return a.hz }

func (a *aaOutput) fill(st *State) {
	if a.byName {
		st.Driver = AAudio
	}
	info := asio.Info{Driver: AAudio, Rate: a.hz, Bits: 32, Float: true, Outputs: 2, Played: []int{0, 1},
		Latency: int(a.delay.Load())}
	if run := a.run.Load(); run != nil {
		info.Buffer, info.MinBuffer, info.PreferredBuffer = run.info.Burst, run.info.Burst, run.info.Burst
		info.MaxBuffer = run.info.Capacity
	}
	if a.in != nil {
		info.Inputs, info.Recorded, info.InputNames = 1, 1, []string{micName}
		info.InputLatency = int(a.inDelay.Load())
	}
	st.ASIO = &info
	if st.Problem == nil {
		st.Problem = a.err()
	}
}

func (a *aaOutput) err() error {
	if p := a.problem.Load(); p != nil && a.run.Load() == nil {
		return *p
	}
	if p := a.iproblem.Load(); p != nil {
		return *p
	}
	return nil
}

// suspend pauses the output stream, keeping what it holds, and closes
// the input, so the microphone is let go while the application is away.
func (a *aaOutput) suspend() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.paused.Store(true)
	if run := a.run.Load(); run != nil {
		if err := run.st.Pause(); err != nil {
			// A stream whose device went away pauses no more; the resume
			// opens another.
			a.dropOut()
		}
	}
	a.dropIn()
	return nil
}

// resume starts the output stream again, or has the goroutine open
// one, and the input.
func (a *aaOutput) resume() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.paused.Store(false)
	a.org.Store(none)
	a.settle.Store(a.clock())
	if run := a.run.Load(); run != nil {
		run.moved.Store(a.clock())
		a.startOut()
	}
	select {
	case a.wake <- struct{}{}:
	default:
	}
	return nil
}

func (a *aaOutput) close() error {
	if a.tap != nil {
		duplex.Withdraw(a.tap)
	}
	close(a.quit)
	<-a.done
	a.mu.Lock()
	a.dropOut()
	a.dropIn()
	a.closed.Store(true)
	a.waitReaders()
	a.mu.Unlock()
	a.q.Free()
	if a.in != nil {
		a.in.Free()
		a.blocks.Free()
	}
	return nil
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
