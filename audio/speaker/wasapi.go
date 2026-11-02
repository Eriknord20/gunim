package speaker

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/asio"
	"github.com/marrasen/gunim/audio/internal/duplex"
	"github.com/marrasen/gunim/audio/internal/wasapi"
	"github.com/marrasen/gunim/audio/rt"
)

// WASAPI is the name [Options].Driver takes to play through WASAPI,
// Windows' own sound API, through gunim's own code in place of oto: the
// default output, in shared mode, at the smallest period the engine
// offers where Windows 10 or newer lets it. It plays at the rate the
// device is set to in Windows' Sound settings, whatever Options.Rate
// asks, so Windows converts nothing, and the mixer follows it. With
// [Options].Record set it records the default input too, converted to
// that rate, for package audio/capture, stamped with the mixer frame
// heard as each packet came in. As the default device changes, or the
// device goes, it plays on the new default. On another system it plays
// through the system's sound, as a driver that fails to open does.
const WASAPI = "WASAPI"

// wasapiFirst plays through WASAPI where Options names no driver, in
// place of oto, on Windows.
const wasapiFirst = false

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
	// wasSettle is how long a device starting or resuming plays before a
	// buffer the ring leaves short counts as the sound running dry.
	wasSettle = 250 * time.Millisecond
	// wasStalled is how long a started device's clock may stand still
	// before it is opened again, as one gone untold.
	wasStalled = time.Second
	// wasJump is how far a clock reading may place the device from
	// where the last ones did before it is taken as it is, unsmoothed.
	wasJump = 5 * time.Millisecond
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
	fails   int
	next    int64
	buf, in []float32
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
			s.logf("WASAPI records %s, %d channels, %d frames of latency", info.InputName, info.Inputs, info.InputLatency)
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
	a.settle, a.movedAt = now, now
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
	a.problem.Store(nil)
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
	a.s.logf("WASAPI: %v; opening the default device again", err)
	a.drop()
	a.next = a.clock()
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
	a.settle, a.movedAt = now, now
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
		a.pos, a.movedAt = pos, now
	} else if now-a.movedAt > int64(wasStalled) {
		return errStalled
	}
	if pos > 0 {
		a.place(pos, at, now)
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
		if a.primed.Load() && !a.paused.Load() && now-a.settle > int64(wasSettle) {
			a.s.ranDry()
		}
		if err := a.dev.Silence(short); err != nil {
			return err
		}
		a.sil.add(a.written, int64(short))
		a.written += int64(short)
	}
	return nil
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
		if a.tap == nil || !a.tap.Attached() || n == 0 {
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
		a.tap = duplex.NewTap(WASAPI, a.hz, info.Period, info.InputLatency, channelNames(info.InputName, info.Inputs))
		duplex.Publish(a.tap)
	}
	go a.feed(buf)
	close(a.begin)
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
// the least.
func (a *wasOutput) setTarget() {
	least := int64(a.info.Load().Period) + audio.FramesAt(2*wasTick, a.hz)
	t := max(audio.FramesAt(time.Duration(a.ahead.Load()), a.hz)-a.delay.Load(), least)
	a.target.Store(min(t, a.q.size()-int64(a.chunk())))
}

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
		st.Driver = WASAPI
	}
	info := a.info.Load()
	played := []int{0, 1}
	if info.Channels == 1 {
		played = []int{0}
	}
	ai := asio.Info{Driver: WASAPI, Rate: a.hz, Buffer: info.Period, MinBuffer: info.Period, MaxBuffer: info.Buffer,
		PreferredBuffer: info.Period, Latency: int(a.delay.Load()), Bits: 32, Float: true, Outputs: info.Channels,
		OutputNames: channelNames(info.Name, info.Channels), Played: played}
	if a.tap != nil {
		ai.Inputs, ai.Recorded, ai.InputNames, ai.InputLatency = len(a.tap.Names), len(a.tap.Names), a.tap.Names, a.tap.Latency
	}
	st.ASIO = &ai
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
