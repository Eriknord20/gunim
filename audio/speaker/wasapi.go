package speaker

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/driver"
	"github.com/marrasen/gunim/audio/internal/duplex"
	"github.com/marrasen/gunim/audio/internal/wasapi"
	"github.com/marrasen/gunim/audio/rt"
)

// wasapiFirst plays through WASAPI where Options names no driver, on
// Windows; a test sets it elsewhere.
var wasapiFirst = runtime.GOOS == "windows"

// A wasDevice is a WASAPI device, as package audio/internal/wasapi opens
// it: the output and its input. The render goroutine alone calls it,
// from the thread it opened it on, Close too; a test opens one of its
// own.
type wasDevice interface {
	Info() wasapi.Info
	Start() error
	Stop() error
	Wait(d time.Duration)
	Room() (int, error)
	Write(frames []float32) error
	Silence(n int) error
	Clock() (pos, at int64, err error)
	Read(dst []float32) (n int, at int64, ok bool, err error)
	Position() int64
	Changed() bool
	Close()
}

// openWasDevice opens WASAPI's default output, on the calling thread,
// where the system has WASAPI; nil elsewhere.
var openWasDevice func(c wasapi.Config) (wasDevice, error)

// wasClock is the clock the device's stamps are on, in nanoseconds; a
// test puts its own here.
var wasClock = nanotime

// The output's timings.
const (
	// wasWait is the longest the render goroutine waits on the device
	// before it looks at what the speaker asks.
	wasWait = 20 * time.Millisecond
	// wasTick is how often the feed tops the ring up.
	wasTick = 2 * time.Millisecond
	// wasSettle is how long a device plays, once its clock first moves
	// after it starts or resumes, before a buffer the ring leaves short
	// counts as the sound running dry for good, and grows the lead: until
	// then it counts as a dropout of the opening. A device that just
	// changed its rate may take a while to start its clock.
	wasSettle = 250 * time.Millisecond
	// wasStalled is how long a playing device's clock may stand still
	// before it is opened again, as one gone untold. wasStartup is how
	// long a device starting or resuming may take before its clock first
	// moves, as a Bluetooth headset or an HDMI display waking up does.
	wasStalled = time.Second
	wasStartup = 5 * time.Second
	// wasStallWait is how long the render goroutine waits before it
	// opens a device again after its second stall in a row, doubling with
	// each stall after, as far as wasStallMost.
	wasStallWait = time.Second
	wasStallMost = 16 * time.Second
	// wasJump is how far a clock reading may place the device from
	// where the last ones did before it is taken as it is, unsmoothed.
	wasJump = 5 * time.Millisecond
	// wasNoteEvery is how often the debug log tells of the stamps.
	wasNoteEvery = 5 * time.Second
	// wasStampOff is how far an input stamp may lie off the line the
	// stamps before it lie on and still count as on it. One further off
	// is left out; two in a row start the line again, as after a gap.
	// A line teaches how late the stamps run only once wasStampAgree
	// stamps in a row have lain on it.
	wasStampOff   = time.Millisecond
	wasStampAgree = 4
)

// errWasPanel is what ControlPanel returns while WASAPI plays.
var errWasPanel = errors.New("speaker: WASAPI has no control panel of its own: set the device's format in " +
	"Windows' Sound settings, and the speaker plays at its rate")

// errDefaultMoved and errStalled tell why a device opens again.
var (
	errDefaultMoved = errors.New("the default device changed, or the device went or changed its format")
	errStalled      = errors.New("the device's clock stood still")
)

// wasOutput plays through WASAPI. A goroutine, the feed, reads the
// mixer ahead into a ring; another, the render goroutine, waits on the
// device's event, at the system's audio priority, and moves what the
// ring holds into the device's buffer each period, silence where it
// runs short. The render goroutine alone opens, calls and closes the
// device, so nothing else can call into a device it closed: it tells
// the rest what it read of the device through atomics. It reads the
// device's clock each period, which places the frame heard on the
// clock; and where Record is set, it stamps each packet the input
// recorded with the mixer frame heard as it came in, and hands it to
// package audio/capture. Where the device goes, or the default device
// changes, it opens the default device again; one at another rate, or
// with other inputs, has the speaker open the output again.
type wasOutput struct {
	s      *Speaker
	byName bool
	record bool
	hz     int
	clock  func() int64
	// q holds the frames to play, in pairs. base is the mixer frame of
	// its frame 0: the feed alone reads the mixer.
	q    *ring
	base atomic.Int64
	// ahead is how far ahead the mixer works, in nanoseconds, and
	// target how full the feed keeps the ring.
	ahead, target atomic.Int64
	// primed says the ring has reached its target once; paused that the
	// speaker suspended the output.
	primed, paused atomic.Bool
	// What the render goroutine tells of the device: org is the ring
	// frame heard as the clock read 0, as float64 bits, or none; from
	// is the ring frame the device's first frame was; delay how many
	// frames the device took that are still to be heard, as its clock
	// last told.
	org   atomic.Uint64
	from  atomic.Int64
	delay atomic.Int64
	info  atomic.Pointer[wasapi.Info]
	// problem is why no device plays, while none does.
	problem atomic.Pointer[error]
	tap     *duplex.Tap
	started bool
	// notes is what the debug log tells of the stamps, nil where the
	// speaker logs nothing.
	notes *wasNotes
	// ctl carries Suspend's and Resume's asks to the render goroutine.
	ctl     chan bool
	begin   chan struct{}
	wake    chan struct{}
	quit    chan struct{}
	done    chan struct{}
	fed     chan struct{}
	closing sync.Once

	// What the render goroutine alone keeps: the device, the first
	// device's info, the frames written to the device, silence among
	// them, when it settled, its clock's last position and when that
	// moved, the opens that failed in a row and when to try next, and
	// whether it waits for the speaker to open the output again.
	dev     wasDevice
	first   wasapi.Info
	written int64
	sil     silences
	stopped bool
	waiting bool
	settle  int64
	pos     int64
	movedAt int64
	// moved says the clock has moved since the device started or
	// resumed, and resumed which of the two it was; stalls counts the
	// devices in a row whose clock stood still.
	moved   bool
	resumed bool
	stalls  int
	fails   int
	next    int64
	buf, in []float32
	// What the render goroutine learns of the input's stamps over an
	// open: inFrames counts the frames read; inLine is where the line
	// the stamps lie on puts frame 0, in nanoseconds, NaN for none, and
	// inOff counts the stamps in a row off it, and inOn those on it
	// since it was drawn; inBack is how much later than its first frame
	// a packet's stamp marks, in nanoseconds.
	inFrames    int64
	inLine      float64
	inOff, inOn int
	inBack      int64
}

// openWasapi opens WASAPI's default output, and its default input
// where o.Record says, stopped; start starts them. byName says
// Options.Driver named it.
func openWasapi(s *Speaker, o Options, byName bool) (*wasOutput, error) {
	if openWasDevice == nil {
		return nil, wasapi.ErrUnsupported
	}
	if o.Outputs != nil && len(o.Outputs) == 0 {
		return nil, errors.New("speaker: Options.Outputs names no outputs to play on")
	}
	a := &wasOutput{s: s, byName: byName, record: o.Record, clock: wasClock, ctl: make(chan bool),
		begin: make(chan struct{}), wake: make(chan struct{}, 1), quit: make(chan struct{}),
		done: make(chan struct{}), fed: make(chan struct{})}
	a.org.Store(none)
	ready := make(chan error, 1)
	go a.run(ready)
	if err := <-ready; err != nil {
		<-a.done
		return nil, err
	}
	info := a.first
	a.hz = info.Rate
	a.q = newRing(int(audio.FramesAt(MaxLatency, a.hz))+2*info.Buffer+4096, 2)
	s.logf("WASAPI plays %s at %d Hz, %d channels, %d frames a period, %d in its buffer, %d of stream latency, low latency %v",
		info.Name, info.Rate, info.Channels, info.Period, info.Buffer, info.Latency, info.LowLatency)
	if o.Record {
		if info.InputErr != nil {
			s.logf("WASAPI records nothing: %v", info.InputErr)
		} else {
			s.logf("WASAPI records %s, %d channels, %d frames of latency: %d of stream latency and a period of %d",
				info.InputName, info.Inputs, info.InputLatency, info.InputLatency-info.InputPeriod, info.InputPeriod)
		}
	}
	return a, nil
}

// run opens the device and tells ready, then, once start lets it,
// plays until the output closes. It runs on a thread of its own, at
// the system's audio priority, and the device is that thread's.
func (a *wasOutput) run(ready chan<- error) {
	defer close(a.done)
	leave := rt.Enter()
	defer leave()
	dev, err := openWasDevice(wasapi.Config{Record: a.record})
	if err != nil {
		ready <- err
		return
	}
	a.first = dev.Info()
	a.use(dev)
	ready <- nil
	select {
	case <-a.begin:
	case <-a.quit:
		a.drop()
		return
	}
	a.startDevice()
	a.loop()
	a.drop()
}

// use makes dev the device playing, from the ring's next frame.
func (a *wasOutput) use(dev wasDevice) {
	info := dev.Info()
	a.dev = dev
	a.info.Store(&info)
	a.written = 0
	a.sil = silences{}
	a.pos = 0
	a.forgetStamps()
	if a.q != nil {
		a.from.Store(a.q.r.Load())
	}
	a.org.Store(none)
	a.delay.Store(int64(info.Buffer + info.Latency))
	if len(a.buf) < 2*info.Buffer {
		a.buf = make([]float32, 2*info.Buffer)
	}
	if n := info.Inputs * max(info.InputBuffer, info.Period); len(a.in) < n {
		a.in = make([]float32, n)
	}
}

// drop closes the device.
func (a *wasOutput) drop() {
	if a.dev != nil {
		a.dev.Close()
		a.dev = nil
	}
	a.org.Store(none)
	a.delay.Store(0)
}

// startDevice fills the device's buffer and starts it.
func (a *wasOutput) startDevice() {
	now := a.clock()
	a.settle, a.movedAt, a.moved, a.resumed = now, now, false, false
	if err := a.write(now); err != nil {
		a.lose(err)
		return
	}
	if err := a.dev.Start(); err != nil {
		a.lose(err)
	}
}

// loop plays until the output closes: it waits on the device, fills
// it, reads its clock and its input, and acts on what the speaker asks.
func (a *wasOutput) loop() {
	for {
		select {
		case <-a.quit:
			return
		case p := <-a.ctl:
			a.pause(p)
		default:
		}
		if a.stopped || a.waiting {
			select {
			case <-a.quit:
				return
			case p := <-a.ctl:
				a.pause(p)
			}
			continue
		}
		if a.dev == nil && !a.reopen() {
			continue
		}
		a.dev.Wait(wasWait)
		if a.dev.Changed() {
			a.lose(errDefaultMoved)
			continue
		}
		if err := a.cycle(); err != nil {
			a.lose(err)
		}
	}
}

// reopen opens the default device again, once it may try, and reports
// whether one plays. Where none opens it waits, for the next try, or
// for the speaker to ask something.
func (a *wasOutput) reopen() bool {
	now := a.clock()
	if now < a.next {
		t := time.NewTimer(time.Duration(a.next - now))
		defer t.Stop()
		select {
		case <-a.quit:
		case p := <-a.ctl:
			a.pause(p)
		case <-t.C:
		}
		return false
	}
	dev, err := openWasDevice(wasapi.Config{Record: a.record})
	if err != nil {
		a.fails++
		a.next = now + int64(retryDelay(a.fails))
		err = fmt.Errorf("speaker: WASAPI: %w", err)
		a.problem.Store(&err)
		a.s.logf("%v; trying again in %v", err, retryDelay(a.fails))
		return false
	}
	info := dev.Info()
	if info.Rate != a.first.Rate || info.Inputs != a.first.Inputs {
		// The mixer and the inputs' readers follow the first device: the
		// speaker opens the output again for this one.
		dev.Close()
		a.waiting = true
		a.s.logf("WASAPI: %s plays at %d Hz and records %d channels, where the last played at %d Hz and recorded %d; "+
			"opening the output again", info.Name, info.Rate, info.Inputs, a.first.Rate, a.first.Inputs)
		a.s.askReset()
		return false
	}
	a.fails = 0
	if a.stalls == 0 {
		// A stall's problem stands until a device's clock moves.
		a.problem.Store(nil)
	}
	// What waited was mixed for the device gone; the new one starts from
	// the next frame mixed.
	a.q.r.Store(a.q.w.Load())
	a.use(dev)
	a.s.logf("WASAPI plays %s again", info.Name)
	a.startDevice()
	return a.dev != nil
}

// lose closes a device that went away, failed or stalled; the loop
// opens the default one again.
func (a *wasOutput) lose(err error) {
	a.drop()
	a.next = a.clock()
	if !errors.Is(err, errStalled) {
		a.s.logf("WASAPI: %v; opening the default device again", err)
		return
	}
	// A device whose clock never moves plays nothing, opened again or
	// not: once more at once, then ever more slowly, and say so.
	a.stalls++
	if a.stalls == 1 {
		a.s.logf("WASAPI: %v; opening the default device again", err)
		return
	}
	wait := min(wasStallWait<<min(a.stalls-2, 8), wasStallMost)
	a.next += int64(wait)
	p := fmt.Errorf("speaker: WASAPI: the device's clock stood still %d times in a row: it plays nothing", a.stalls)
	a.problem.Store(&p)
	a.s.logf("WASAPI: %v; opening the default device again in %v", p, wait)
}

// pause stops the device, keeping what its buffer holds, or starts it
// again, as the speaker suspends and resumes.
func (a *wasOutput) pause(p bool) {
	if p == a.stopped {
		return
	}
	a.stopped = p
	if a.dev == nil {
		return
	}
	if p {
		if err := a.dev.Stop(); err != nil {
			a.lose(err)
			return
		}
		// The frame heard stands still while it is stopped.
		a.org.Store(none)
		return
	}
	now := a.clock()
	a.org.Store(none)
	// A device started again may stamp another frame of its packets.
	a.forgetStamps()
	a.settle, a.movedAt, a.moved, a.resumed = now, now, false, true
	if err := a.dev.Start(); err != nil {
		a.lose(err)
	}
}

// cycle fills the device's buffer, reads its clock, and hands on what
// the input recorded.
func (a *wasOutput) cycle() error {
	now := a.clock()
	if err := a.write(now); err != nil {
		return err
	}
	pos, at, err := a.dev.Clock()
	if err != nil {
		return err
	}
	if pos != a.pos {
		if !a.moved {
			// The device plays from now: it settles from here.
			if a.notes != nil {
				a.notes.firstMove(now-a.settle, pos, a.written, a.sil.total, a.padding(), a.resumed)
			}
			a.settle = now
		}
		a.pos, a.movedAt, a.moved = pos, now, true
		if a.stalls > 0 {
			a.stalls = 0
			a.problem.Store(nil)
		}
	} else {
		limit := wasStalled
		if !a.moved {
			limit = wasStartup
		}
		if now-a.movedAt > int64(limit) {
			return errStalled
		}
	}
	if pos > 0 {
		a.place(pos, at, now)
		if n := a.notes; n != nil {
			n.output(a.clock()-at, a.written-pos, a.padding(), a.sil.total)
		}
	}
	if err := a.drain(now); err != nil {
		return err
	}
	select {
	case a.wake <- struct{}{}:
	default:
	}
	return nil
}

// write moves as much of the ring into the device's buffer as it has
// room for, and silence past what the ring holds where the device would
// otherwise run dry before the next period.
func (a *wasOutput) write(now int64) error {
	room, err := a.dev.Room()
	if err != nil || room <= 0 {
		return err
	}
	info := a.info.Load()
	n := 0
	if !a.paused.Load() {
		n = a.q.pop(a.buf[:2*room])
	}
	if n > 0 {
		if err := a.dev.Write(a.buf[:2*n]); err != nil {
			return err
		}
		a.written += int64(n)
	}
	// Silence only where the device would run dry before the next
	// period: what it holds then falls short of a period.
	held := info.Buffer - room + n
	if short := min(info.Period-held, room-n); short > 0 {
		a.countDry(now)
		if err := a.dev.Silence(short); err != nil {
			return err
		}
		a.sil.add(a.written, int64(short))
		a.written += int64(short)
	}
	return nil
}

// countDry counts a buffer the ring left short: none before the
// ring first filled, or while the device's clock has yet to move, as
// nothing plays then; a dropout of the opening for wasSettle after it
// first moves; and the sound running dry after that.
func (a *wasOutput) countDry(now int64) {
	switch {
	case !a.primed.Load() || a.paused.Load() || !a.moved:
	case now-a.settle <= int64(wasSettle):
		a.s.ranDryOnce()
	default:
		a.s.ranDry()
	}
}

// place takes the device's frame pos heard at at: the ring frame heard
// then, past the silence written before it, places the ring's frames on
// the clock, and how many frames the device holds yet.
func (a *wasOutput) place(pos, at, now int64) {
	k := float64(a.hz) / 1e9
	heard := a.from.Load() + pos - a.sil.before(pos)
	org := float64(heard) - float64(at)*k
	was := math.Float64frombits(a.org.Load())
	if !math.IsNaN(was) && math.Abs(org-was) < float64(audio.FramesAt(wasJump, a.hz)) {
		// The clock jitters: move an eighth of the way.
		org = was + (org-was)/8
	}
	a.org.Store(math.Float64bits(org))
	held := max(0, a.q.r.Load()-int64(org+float64(now)*k))
	if d := a.delay.Load(); math.IsNaN(was) {
		a.delay.Store(held)
	} else {
		a.delay.Store(d + (held-d)/4)
	}
	a.setTarget()
}

// padding returns how many frames the device's buffer holds, still to
// be taken by the engine, -1 where it cannot tell. The debug log alone
// asks.
func (a *wasOutput) padding() int64 {
	room, err := a.dev.Room()
	if err != nil {
		return -1
	}
	return int64(a.info.Load().Buffer - room)
}

// drain hands each packet the input recorded to package audio/capture,
// stamped with the mixer frame heard as its first frame came in.
func (a *wasOutput) drain(now int64) error {
	info := a.info.Load()
	ch := info.Inputs
	if ch == 0 {
		return nil
	}
	for {
		n, at, ok, err := a.dev.Read(a.in)
		if err != nil || !ok {
			return err
		}
		if n == 0 {
			continue
		}
		if at != 0 {
			read, stamp := a.clock(), at
			at = a.firstAt(n, stamp, read)
			if a.notes != nil {
				a.notes.input(n, a.dev.Position(), read-stamp, a.inBack)
			}
		} else {
			a.inFrames += int64(n)
		}
		if a.tap == nil || !a.tap.Attached() {
			continue
		}
		if at == 0 {
			// WASAPI could not tell: the packet's last frame came in a
			// latency ago.
			at = now - int64(audio.DurationAt(int64(info.InputLatency+n), a.hz))
		}
		a.tap.Deliver(a.in[:n*ch], ch, a.base.Load()+a.ringAt(at, now))
	}
}

// firstAt returns when the first frame of a packet of n frames came in,
// WASAPI having stamped it at, and the packet read at read. Windows
// says the stamp marks the first frame, but on some devices, and some
// opens, it marks a later one, as late as the last: a packet is read
// microseconds after its stamp. The first frame came in no later than
// the packet was read less its length, so firstAt moves every stamp of
// the open back by as much as the soonest packet read falls short of
// that, a packet's length at most. It learns from the line the stamps
// lie on, one a frame's length after the other, once wasStampAgree
// stamps in a row lie on it, so stamps off the line mislead nothing.
// It runs on the render goroutine, and allocates nothing.
func (a *wasOutput) firstAt(n int, at, read int64) int64 {
	ns := 1e9 / float64(a.hz)
	f := float64(a.inFrames)
	a.inFrames += int64(n)
	off := float64(at) - f*ns
	switch {
	case math.IsNaN(a.inLine):
		a.inLine, a.inOff, a.inOn = off, 0, 0
	case math.Abs(off-a.inLine) <= float64(wasStampOff):
		a.inLine += (off - a.inLine) / 8
		a.inOff = 0
		a.inOn++
	default:
		a.inOff++
		if a.inOff >= 2 {
			a.inLine, a.inOff, a.inOn = off, 0, 0
		}
	}
	if a.inOn < wasStampAgree {
		return at - a.inBack
	}
	stamp := a.inLine + f*ns
	if short := min(int64(float64(n)*ns-(float64(read)-stamp)), int64(float64(n)*ns)); short > a.inBack {
		a.inBack = short
	}
	return at - a.inBack
}

// forgetStamps has firstAt learn the input's stamps anew, for a device
// opened or started again.
func (a *wasOutput) forgetStamps() {
	a.inFrames, a.inLine, a.inOff, a.inOn, a.inBack = 0, math.NaN(), 0, 0, 0
}

// ringAt returns the ring frame heard at t, on the output's clock, now
// being now: where the device's clock placed the frames, or from the
// frames it holds where it has yet to; never past the frames the device
// took, nor before its first.
func (a *wasOutput) ringAt(t, now int64) int64 {
	r := a.q.r.Load()
	k := float64(a.hz) / 1e9
	var h int64
	if org := math.Float64frombits(a.org.Load()); !math.IsNaN(org) {
		h = int64(math.Floor(org + float64(t)*k))
	} else {
		h = r - a.delay.Load() - int64(float64(now-t)*k)
	}
	return min(max(h, a.from.Load(), r-audio.FramesAt(MaxLatency, a.hz)), r)
}

// start fills the ring and lets the render goroutine start the device,
// and starts the feed.
func (a *wasOutput) start(ahead time.Duration) {
	a.started = true
	a.setAhead(ahead)
	a.base.Store(a.s.m.Mixed())
	buf := make([]float32, 2*a.chunk())
	a.topUp(buf)
	info := a.first
	if a.record && info.Inputs > 0 {
		a.tap = duplex.NewTap(driver.WASAPI, a.hz, info.Period, info.InputLatency, channelNames(info.InputName, info.Inputs))
		duplex.Publish(a.tap)
	}
	if a.s.debug {
		a.notes = newWasNotes()
		go a.report()
	}
	go a.feed(buf)
	close(a.begin)
}

// wasNotes is what the render goroutine tells the debug log of the
// stamps: of the packet the input read soonest after its stamp, and the
// clock reading soonest after its own, since the log last told, and of
// the clock's first move. A lock keeps each note's numbers together, so
// a line of the log tells of one packet or one reading. It exists only
// while the log is on, and allocates nothing.
type wasNotes struct {
	mu sync.Mutex
	wasNoted
}

// wasNoted is what the notes hold.
type wasNoted struct {
	// inLag is how long after its stamp that packet was read, in
	// nanoseconds, math.MaxInt64 for none; inFrames its frames, and inPos
	// its device position. inBack is how far the stamps were moved back
	// to their packets' first frames.
	inLag, inFrames, inPos, inBack int64
	// outLag is how long after its stamp the clock was read,
	// math.MaxInt64 for none; outPast how many frames were written past
	// the position it told, outPad how many of them the device's buffer
	// still held, and outSil the frames of silence written since the
	// device opened.
	outLag, outPast, outPad, outSil int64
	// moved is how long the device's clock took to first move after it
	// started or resumed, 0 where the log told it already, and resumed
	// which of the two it was. movedPos is the frame it moved to,
	// movedWritten the frames written by then, movedSil those of
	// silence, and movedPad those its buffer held.
	moved, movedPos, movedWritten, movedSil, movedPad int64
	resumed                                           bool
}

func newWasNotes() *wasNotes {
	return &wasNotes{wasNoted: wasNoted{inLag: math.MaxInt64, outLag: math.MaxInt64}}
}

// input notes a packet of frames at device position pos, read lag
// after its stamp, the stamps moved back by back, where it was read
// sooner after its stamp than the one noted.
func (n *wasNotes) input(frames int, pos, lag, back int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if lag < n.inLag {
		n.inLag, n.inFrames, n.inPos, n.inBack = lag, int64(frames), pos, back
	}
}

// output notes a clock reading made lag after its stamp, past frames
// written past the position it told, pad of them in the device's
// buffer, sil frames of silence written in all, where it was read
// sooner after its stamp than the one noted.
func (n *wasNotes) output(lag, past, pad, sil int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if lag < n.outLag {
		n.outLag, n.outPast, n.outPad, n.outSil = lag, past, pad, sil
	}
}

// firstMove notes the clock's first move, seen d after the device
// started, or resumed, to frame pos, with written frames written, sil
// of them silence, pad in the buffer.
func (n *wasNotes) firstMove(d, pos, written, sil, pad int64, resumed bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.moved, n.movedPos, n.movedWritten, n.movedSil, n.movedPad = max(d, 1), pos, written, sil, pad
	n.resumed = resumed
}

// take returns the notes and clears them for the next report.
func (n *wasNotes) take() wasNoted {
	n.mu.Lock()
	defer n.mu.Unlock()
	got := n.wasNoted
	n.inLag, n.outLag, n.moved = math.MaxInt64, math.MaxInt64, 0
	return got
}

// report logs the notes every wasNoteEvery, until the output closes:
// which end of a packet the input's stamp marks, and where the output's
// clock places the frame heard. A packet read a period or more after
// its stamp was stamped as its first frame came in; one read at once,
// as its last did. Of the frames written past the frame the clock
// tells, those the buffer holds wait to be taken; the engine took the
// rest, and they are still to be heard, or it dropped them untold.
func (a *wasOutput) report() {
	t := time.NewTicker(wasNoteEvery)
	defer t.Stop()
	for {
		select {
		case <-a.quit:
			return
		case <-t.C:
		}
		n := a.notes.take()
		if n.moved > 0 {
			began := "started"
			if n.resumed {
				began = "resumed"
			}
			// The cycle that sees the move reads the time as it begins,
			// so the time told runs up to a period late.
			a.s.logf("WASAPI: the device's clock was seen to move first %v after it %s (to within a period), to "+
				"frame %d; by then %d frames were written, %d of them silence, and its buffer held %d, so the engine "+
				"had taken %d, %d past the frame the clock told", time.Duration(n.moved), began, n.movedPos,
				n.movedWritten, n.movedSil, n.movedPad, n.movedWritten-n.movedPad, n.movedWritten-n.movedPad-n.movedPos)
		}
		info := a.info.Load()
		if n.inLag != math.MaxInt64 {
			a.s.logf("WASAPI input: a packet of %d frames at device position %d, read %v after its stamp, the soonest "+
				"in %v; the input's period is %d frames (%v); stamps moved back %v to their packets' first frames",
				n.inFrames, n.inPos, time.Duration(n.inLag), wasNoteEvery, info.InputPeriod,
				audio.DurationAt(int64(info.InputPeriod), a.hz), time.Duration(n.inBack))
		}
		if n.outLag != math.MaxInt64 {
			a.s.logf("WASAPI output: the clock read %v after its stamp, the soonest in %v, with %d frames written past "+
				"the position it told: %d in its buffer, %d taken by the engine; %d frames of silence written since it "+
				"opened; device delay %v", time.Duration(n.outLag), wasNoteEvery, n.outPast, n.outPad,
				n.outPast-n.outPad, n.outSil, audio.DurationAt(a.delay.Load(), a.hz))
		}
	}
}

// chunk is the most frames the feed reads the mixer for at once.
func (a *wasOutput) chunk() int { return max(1, int(audio.FramesAt(chunk, a.hz))) }

// feed keeps the ring filled to its target until the output closes.
// The device waits on it, so it runs at the system's audio priority.
func (a *wasOutput) feed(buf []float32) {
	defer close(a.fed)
	leave := rt.Enter()
	defer leave()
	t := time.NewTicker(wasTick)
	defer t.Stop()
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
		if !a.paused.Load() {
			a.topUp(buf)
		}
	}
}

// topUp tops the ring up to its target, a chunk at a time.
func (a *wasOutput) topUp(buf []float32) {
	for {
		want := a.target.Load() - a.q.len()
		if want <= 0 {
			a.primed.Store(true)
			return
		}
		n := min(want, int64(len(buf)/2), a.q.room())
		if n <= 0 {
			return
		}
		frames := buf[:2*n]
		a.s.pull(frames)
		a.q.push(frames)
	}
}

// setTarget sets how full the feed keeps the ring: what ahead leaves
// past the device's delay, and a period and two of the feed's ticks at
// the least; and past that, the margin dry spells grew.
func (a *wasOutput) setTarget() {
	least := int64(a.info.Load().Period) + audio.FramesAt(2*wasTick, a.hz)
	t := max(audio.FramesAt(time.Duration(a.ahead.Load()), a.hz)-a.delay.Load(), least) +
		audio.FramesAt(time.Duration(a.s.margin.Load()), a.hz)
	a.target.Store(min(t, a.q.size()-int64(a.chunk())))
}

// remargin takes the margin the speaker grew.
func (a *wasOutput) remargin() { a.setTarget() }

// held counts from the mixer's own count of the frames it mixed, not
// the ring's: the feed mixes a chunk before it adds it to the ring, and
// the mixer's Heard, which never goes back, would keep a frame heard
// read in between, a chunk too far on.
func (a *wasOutput) held() int64 {
	now := a.clock()
	return max(0, a.s.m.Mixed()-a.base.Load()-a.ringAt(now, now))
}

func (a *wasOutput) setAhead(d time.Duration) {
	a.ahead.Store(int64(d))
	a.setTarget()
}

func (a *wasOutput) latency() time.Duration {
	return audio.DurationAt(a.target.Load()+a.delay.Load(), a.hz)
}

func (a *wasOutput) rate() int { return a.hz }

func (a *wasOutput) fill(st *State) {
	if a.byName {
		st.Driver = driver.WASAPI
	}
	info := a.info.Load()
	played := []int{0, 1}
	if info.Channels == 1 {
		played = []int{0}
	}
	ai := driver.Info{Driver: driver.WASAPI, Rate: a.hz, Buffer: info.Period, MinBuffer: info.Period, MaxBuffer: info.Buffer,
		PreferredBuffer: info.Period, Latency: int(a.delay.Load()), Bits: 32, Float: true, Outputs: info.Channels,
		OutputNames: channelNames(info.Name, info.Channels), Played: played}
	if a.tap != nil {
		ai.Inputs, ai.Recorded, ai.InputNames, ai.InputLatency = len(a.tap.Names), len(a.tap.Names), a.tap.Names, a.tap.Latency
	}
	st.Info = &ai
	if st.Problem == nil {
		st.Problem = a.err()
	}
}

func (a *wasOutput) err() error {
	if p := a.problem.Load(); p != nil {
		return *p
	}
	if info := a.info.Load(); a.record && info.InputErr != nil {
		return fmt.Errorf("speaker: WASAPI records nothing: %w", info.InputErr)
	}
	return nil
}

// ask hands the render goroutine Suspend's or Resume's ask.
func (a *wasOutput) ask(p bool) {
	select {
	case a.ctl <- p:
	case <-a.done:
	}
}

// suspend stops the device, keeping what it holds; the feed stops too.
func (a *wasOutput) suspend() error {
	a.paused.Store(true)
	a.ask(true)
	return nil
}

// resume starts the device again, or has the render goroutine open
// one, and the feed.
func (a *wasOutput) resume() error {
	a.paused.Store(false)
	a.ask(false)
	select {
	case a.wake <- struct{}{}:
	default:
	}
	return nil
}

// close stops both goroutines, the render goroutine closing the device
// as it ends, once: later calls do nothing.
func (a *wasOutput) close() error {
	a.closing.Do(func() {
		if a.tap != nil {
			duplex.Withdraw(a.tap)
		}
		close(a.quit)
		<-a.done
		if a.started {
			<-a.fed
		}
	})
	return nil
}

// channelNames names a device's n channels: by the device's name
// alone, for one.
func channelNames(name string, n int) []string {
	if n == 1 {
		return []string{name}
	}
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s %d", name, i+1)
	}
	return out
}

// silences logs the runs of silence written to a device, where the ring
// ran short, so a frame the device's clock tells is placed in the ring:
// the render goroutine's own. It keeps the last runs, which the clock
// reads: the device holds a buffer, and its clock is never further back.
type silences struct {
	at, n, prior [32]int64
	// runs counts the runs ever logged, and total their frames.
	runs, total int64
}

// add logs n frames of silence written as the device's frame at.
func (s *silences) add(at, n int64) {
	if s.runs > 0 {
		i := (s.runs - 1) % int64(len(s.at))
		if s.at[i]+s.n[i] == at {
			s.n[i] += n
			s.total += n
			return
		}
	}
	i := s.runs % int64(len(s.at))
	s.at[i], s.n[i], s.prior[i] = at, n, s.total
	s.runs++
	s.total += n
}

// before returns how many frames of silence were written before the
// device's frame pos.
func (s *silences) before(pos int64) int64 {
	size := int64(len(s.at))
	oldest := max(0, s.runs-size)
	for r := s.runs - 1; r >= oldest; r-- {
		i := r % size
		if s.at[i] < pos {
			return s.prior[i] + min(s.n[i], pos-s.at[i])
		}
	}
	if s.runs > size {
		return s.prior[oldest%size]
	}
	return 0
}
