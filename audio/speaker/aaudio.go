package speaker

import (
	"errors"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/driver"
	"github.com/marrasen/gunim/audio/internal/aaudio"
	"github.com/marrasen/gunim/audio/internal/duplex"
	"github.com/marrasen/gunim/audio/rt"
)

// aaudioFirst plays through AAudio where Options names no driver, in
// place of oto, on the Androids it plays on: from Android 8.1.
var aaudioFirst = runtime.GOOS == "android"

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
	Position() int64
	Route() aaudio.Route
	Probe() (aaudio.Probe, bool)
}

// aaConfig says how to open a stream, as aaudio.Config does.
type aaConfig struct {
	input       bool
	rate, chans int
	exclusive   bool
	quick       bool
	// device is the device to ask for, by its ID; at 0 Android picks.
	device int
	data   *aaudio.Ring[float32]
	blocks *aaudio.Ring[int64]
}

// aaClock is the clock an output reads; a test puts its own here.
var aaClock = nanotime

// openAAStream opens an AAudio stream, where the system has AAudio;
// nil elsewhere.
var openAAStream func(c aaConfig) (aaStream, error)

// aaWatchDevices and aaDeviceChanges are aaudio.WatchDevices and
// aaudio.DeviceChanges where AAudio does not itself tell of a stream
// whose device went away, before Android 11; nil where it does. There
// the output reopens a stream as Android tells of devices coming and
// going, and one whose device stands still.
var (
	aaWatchDevices  func() bool
	aaDeviceChanges func() (out, in int64)
)

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
	// fitted says one has, and gap is how far the last that did placed
	// the device from the frames carried.
	odd    int
	fitted bool
	gap    int64
	// most is the most frames a callback has carried, as the goroutine
	// last read it from sh, for setTarget to read from any goroutine:
	// sh is let go as the stream closes.
	most atomic.Int64
	// opened is when the stream opened, on the output's clock.
	opened int64
	moved  atomic.Int64
	// waitPast is the frames carried the stream must pass for its
	// device to have started, as it opens or resumes, or -1 once it
	// has: the output settles from then.
	waitPast atomic.Int64
	// dead says the feed found the stream failed, for the opener to
	// open another; moving says it is to open again as Android told of
	// devices coming or going, a move rather than a failure.
	dead, moving atomic.Bool
	// bluetooth says the stream plays on a Bluetooth device, and mixer
	// is the latency Android's mixer reports for its output, in frames,
	// or 0 where it reports none, as the opener last asked.
	bluetooth atomic.Bool
	mixer     atomic.Int64
	// asked counts the opener's questions of the route, and ask is when
	// it asks next, on the output's clock.
	asked int
	ask   atomic.Int64
	// mic is the device an input was asked to record on, as wantMic
	// said when it opened.
	mic int32
	// pos is the frames the device had moved as the feed last saw them
	// move, at posAt; resumed is when the stream last started again
	// after a pause. Where the output watches for itself, a device that
	// stands still for aaStill reopens the stream.
	pos, posAt int64
	resumed    atomic.Int64
}

// aaOutput plays through AAudio. AAudio's thread takes each buffer from
// a ring in C memory, and a goroutine, the feed, keeps the ring filled
// from the mixer, a chunk at a time, as ringOutput's feed does. Where
// Record is set, AAudio's thread puts each block recorded in another
// ring, which the feed stamps and hands to package audio/capture. Each
// stream tells by its timestamps which frame the device plays or
// records when, on one clock, which places the frames heard, and the
// frame heard as each block recorded came in. Another goroutine, the
// opener, opens a stream again where its device goes away, as a headset
// unplugged, and the microphone until it opens. Opening a stream takes
// AAudio as long as 200 ms, and the feed must never wait that long.
type aaOutput struct {
	s      *Speaker
	hz     int
	byName bool
	quick  bool
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
	// settle is when the output stream last opened or resumed, and
	// then when it first carried a buffer after: a stream may start
	// its device late, as one at a Bluetooth headset does.
	settle atomic.Int64
	// delay is how far behind the frames carried the output's
	// timestamps place the device, in frames, and unstamped what plays
	// on past that where they leave out part of the route's delay.
	delay     atomic.Int64
	unstamped atomic.Pointer[glide]
	// on is where the output plays, a Route, as Android last told. It
	// outlives a stream, so a route stays told while a stream reopens.
	on atomic.Int32
	// phones says on is headphones, as State.Headphones tells.
	phones atomic.Bool
	// wantMic is the device the input should record on: the phone's own
	// microphone while Bluetooth plays, and 0, for Android's pick,
	// otherwise. On the earbuds' own microphone Android would move them
	// to their call link, mono at 16 kHz, both ways.
	wantMic    atomic.Int32
	inDelay    atomic.Int64
	org, iorg  atomic.Uint64
	problem    atomic.Pointer[error]
	iproblem   atomic.Pointer[error]
	wake, quit chan struct{}
	done       chan struct{}
	// reopen wakes the opener, and kept closes as it ends.
	reopen chan struct{}
	kept   chan struct{}
	// opening counts the streams being opened or started now, and calm
	// is when the last of those ended, plus aaSettle, on the output's
	// clock: dry spells until then leave the latency as it is.
	opening atomic.Int64
	calm    atomic.Int64
	// told is when the feed last logged the streams' raw timestamps,
	// where the speaker logs.
	told int64
	// Opening again: the opener's own count of attempts that failed in a
	// row, and when it may try next.
	fails, ifails int
	next, inext   int64
	// watching says Android tells the output of devices coming and
	// going, as aaWatchDevices began, and outs and ins are the counts of
	// those aaDeviceChanges last gave. outAt and inAt are when the feed
	// last found an output, or an input, came or went, and quiet when
	// the opener reopens what that moved, once Android has told of all
	// of them, or 0 for none.
	watching    atomic.Bool
	outs, ins   atomic.Int64
	outAt, inAt atomic.Int64
	quiet       atomic.Int64
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
	// aaSettle is how long a stream starting or resuming runs before a
	// buffer it finds short, or an xrun AAudio counts, counts as the
	// sound running dry.
	aaSettle = 250 * time.Millisecond
	// aaShift is how far a stream's delay may move from one timestamp
	// to the next: further, it counts its frames anew.
	aaShift = 100 * time.Millisecond
	// aaWaitStamp is how long what was recorded waits for the streams'
	// timestamps, to be stamped from them.
	aaWaitStamp = 500 * time.Millisecond
	// aaProven is how long a stream runs before the attempts that
	// failed before it are forgotten: one that fails to start, or dies
	// sooner, counts as one more that failed.
	aaProven = 2 * time.Second
	// aaMicGiveUp is how many attempts in a row may fail to open the
	// microphone before it is tried only every aaMicSlow, as each try
	// may hold the output up a moment.
	aaMicGiveUp = 10
	aaMicSlow   = 10 * time.Second
	// aaRouteEvery is how often the opener asks Android again of the
	// output's device, as a headset's delay may change while it plays.
	// It asks sooner too, aaRouteSoon and aaRouteLater after a stream
	// opens: as earbuds connect, Android may tell the delay of the device
	// before them at first.
	aaRouteEvery = 2 * time.Second
	aaRouteSoon  = 250 * time.Millisecond
	aaRouteLater = time.Second
	// aaSlew is how many frames play for each frame the route's delay
	// moves by, once a stream plays: a delay grown at once would stop
	// the frame heard, which never goes back, for as long.
	aaSlew = 2
	// aaMostCallback is the most frames a callback is counted to ask
	// for at once, as a Bluetooth headset's large bursts do.
	aaMostCallback = 16384
	// aaStill is how long a stream's device may stand still, its
	// position unmoved, before the stream is opened again, before
	// Android 11. It is longer than aaStalled, as a Bluetooth headset
	// may take a second to start.
	aaStill = 2 * time.Second
	// aaDeviceQuiet is how long the opener waits after Android last
	// told of a device coming or going before it reopens the streams
	// that moved: a headset connecting brings several at once.
	aaDeviceQuiet = 300 * time.Millisecond
)

// none marks an origin unknown.
var none = math.Float64bits(math.NaN())

// openAAudio opens AAudio's output, and its input where o.Record says,
// stopped; start starts them. byName says Options.Driver named it.
func openAAudio(s *Speaker, o Options, byName bool) (*aaOutput, error) {
	if openAAStream == nil {
		return nil, aaudio.ErrUnsupported
	}
	a := &aaOutput{s: s, hz: o.rate(), byName: byName, quick: o.Quick, clock: aaClock,
		wake: make(chan struct{}, 1), quit: make(chan struct{}), done: make(chan struct{}),
		reopen: make(chan struct{}, 1), kept: make(chan struct{})}
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
			a.inext = a.clock() + int64(micRetry(1))
			s.logf("%v; trying again in %v", err, micRetry(1))
		}
		a.mu.Unlock()
	}
	return a, nil
}

// openStream opens a stream as c says, as the device's own where it
// lets it, and shared where it does not.
func (a *aaOutput) openStream(c aaConfig) (aaStream, error) {
	c.exclusive = true
	st, err := openAAStream(c)
	if err != nil {
		c.exclusive = false
		st, err = openAAStream(c)
	}
	return st, err
}

// busy marks a stream being opened or started, until the func it
// returns runs: dry spells meanwhile, and for aaSettle after, leave the
// latency as it is.
func (a *aaOutput) busy() func() {
	a.opening.Add(1)
	return func() {
		a.calm.Store(a.clock() + int64(aaSettle))
		a.opening.Add(-1)
	}
}

// openOut opens the output stream, stopped. It runs with mu held.
func (a *aaOutput) openOut() error {
	a.watchDevices()
	st, err := a.openStream(aaConfig{rate: a.hz, chans: 2, quick: a.quick, data: a.q})
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
	run.posAt = run.opened
	a.settle.Store(run.opened)
	a.org.Store(none)
	a.delay.Store(int64(info.Buffer))
	a.primed.Store(false)
	a.route(run, true)
	run.ask.Store(run.opened + int64(aaRouteSoon))
	a.setUnstamped(run, true)
	a.run.Store(run)
	a.setTarget()
	a.s.logf("AAudio plays at %d Hz, %d frames a burst, %d in the device (of %d), device %d, exclusive %v, low latency %v, quick %v",
		info.Rate, info.Burst, info.Buffer, info.Capacity, info.Device, info.Exclusive, info.LowLatency, a.quick)
	if a.s.debug {
		go a.probe(st)
	}
	return nil
}

// watchDevices asks Android to tell of devices coming and going, where
// the output watches for itself and Android does not tell it yet. It
// runs with mu held, as it calls into Java.
func (a *aaOutput) watchDevices() {
	if aaWatchDevices == nil || a.watching.Load() || !aaWatchDevices() {
		return
	}
	out, in := aaDeviceChanges()
	a.outs.Store(out)
	a.ins.Store(in)
	a.watching.Store(true)
	a.s.logf("AAudio may not tell of a device gone on this Android: the speaker watches the devices itself")
}

// devicesChanged notes the devices Android told of coming or going
// since it last looked, and has the opener reopen what that moved, once
// aaDeviceQuiet passes with no more. It runs on the feed.
func (a *aaOutput) devicesChanged(now int64) {
	if !a.watching.Load() {
		return
	}
	out, in := aaDeviceChanges()
	moved := false
	if out != a.outs.Load() {
		a.outs.Store(out)
		a.outAt.Store(now)
		moved = true
	}
	if in != a.ins.Load() {
		a.ins.Store(in)
		a.inAt.Store(now)
		moved = true
	}
	if moved {
		a.s.logf("Android told of devices coming or going: %d outputs and %d inputs so far", out, in)
		a.quiet.Store(now + int64(aaDeviceQuiet))
		a.wakeOpener()
	}
}

// moveFor says a stream opened at opened should open again, as Android
// told of a device on its side coming or going at changed, 0 for never:
// one on Android's pick of device, picked, follows any such change, as
// Android may pick another now; one asked for a device follows only
// where that device is gone, no longer listed.
func moveFor(opened, changed int64, picked, listed bool) bool {
	return changed > 0 && opened <= changed && (picked || !listed)
}

// moveStreams marks dead the streams the devices Android told of moved,
// as moveFor says, once Android has been quiet aaDeviceQuiet, for
// keepOpen to open them again. It runs on the opener, with mu held:
// it may ask Java of a stream's device.
func (a *aaOutput) moveStreams() {
	q := a.quiet.Load()
	if q == 0 || a.paused.Load() || a.clock() < q || !a.quiet.CompareAndSwap(q, 0) {
		return
	}
	if run := a.run.Load(); run != nil && !run.dead.Load() && moveFor(run.opened, a.outAt.Load(), true, true) {
		a.s.logf("Android told of an output device coming or going; opening the output again")
		run.moving.Store(true)
		run.dead.Store(true)
	}
	if run := a.irun.Load(); run != nil && !run.dead.Load() {
		picked := run.mic == 0
		listed := true
		if !picked {
			r := run.st.Route()
			listed = !r.Known || r.Type != 0
		}
		if moveFor(run.opened, a.inAt.Load(), picked, listed) {
			a.s.logf("Android told of an input device coming or going; opening the microphone again")
			run.moving.Store(true)
			run.dead.Store(true)
		}
	}
}

// stood says run's device has stood still for aaStill, its position
// unmoved while the stream plays or records, and logs it. It runs on
// the feed, where the output watches for itself.
func (a *aaOutput) stood(run *aaRun, now int64) bool {
	if p := run.st.Position(); p != run.pos {
		run.pos, run.posAt = p, now
		return false
	}
	since := now - max(run.posAt, run.resumed.Load())
	if since <= int64(aaStill) {
		return false
	}
	a.s.logf("AAudio's device has stood still for %v; opening the stream again", time.Duration(since))
	return true
}

// route asks Android of the device run plays on, as it opens, which
// first says, and as run.ask says after, on the opener: the call goes
// through Java. It logs what it finds where that changed. Where Android
// tells no latency, or a running stream tells nothing, it keeps what it
// told before. A new stream that tells nothing, or a device Android no
// longer lists, makes the route RouteUnknown, as an app lets the voice
// back into the headphones only on a route it knows.
func (a *aaOutput) route(run *aaRun, first bool) {
	r := run.st.Route()
	if !r.Known {
		if first {
			a.on.Store(int32(RouteUnknown))
			a.phones.Store(false)
			a.wantMic.Store(0)
		}
		return
	}
	was, wasMixer := Route(a.on.Load()), run.mixer.Load()
	mixer := wasMixer
	if r.Latency >= 0 {
		mixer = audio.FramesAt(r.Latency, a.hz)
	}
	on := RouteUnknown
	// A device Android no longer lists is one going away, and the stream
	// reopens on the next: until then the delay and the microphone stay
	// as they were told.
	if first || r.Type != 0 {
		on = routeOf(r.Type)
		run.bluetooth.Store(on == RouteBluetooth)
		a.wantMic.Store(0)
		if on == RouteBluetooth {
			a.wantMic.Store(int32(r.Mic))
		}
	}
	a.on.Store(int32(on))
	a.phones.Store((first || r.Type != 0) && headphonesOf(r.Type))
	run.mixer.Store(mixer)
	if first || on != was || mixer != wasMixer {
		a.s.logf("AAudio's device is of type %d, route %v; Android's mixer reports %v for media's output",
			r.Type, on, r.Latency)
	}
}

// headphonesOf says whether an output device of a type, as
// android.media.AudioDeviceInfo numbers them, plays into ears alone.
func headphonesOf(deviceType int) bool {
	switch deviceType {
	case 3, 4, 22: // TYPE_WIRED_HEADSET, TYPE_WIRED_HEADPHONES, TYPE_USB_HEADSET
		return true
	}
	return false
}

// routeOf returns the route of an output device of a type, as
// android.media.AudioDeviceInfo numbers them.
func routeOf(deviceType int) Route {
	switch deviceType {
	case 1, 2, 24: // TYPE_BUILTIN_EARPIECE, TYPE_BUILTIN_SPEAKER, TYPE_BUILTIN_SPEAKER_SAFE
		return RouteSpeaker
	case 3, 4, 5, 6, 19: // TYPE_WIRED_HEADSET, TYPE_WIRED_HEADPHONES, TYPE_LINE_ANALOG, TYPE_LINE_DIGITAL, TYPE_AUX_LINE
		return RouteWired
	case 7, 8, 23, 26, 27, 30: // TYPE_BLUETOOTH_SCO, TYPE_BLUETOOTH_A2DP, TYPE_HEARING_AID, TYPE_BLE_HEADSET, TYPE_BLE_SPEAKER, TYPE_BLE_BROADCAST
		return RouteBluetooth
	case 11, 12, 22: // TYPE_USB_DEVICE, TYPE_USB_ACCESSORY, TYPE_USB_HEADSET
		return RouteUSB
	}
	return RouteUnknown
}

// probe logs what a Java AudioTrack playing beside the stream reports,
// to compare with what the output tells.
func (a *aaOutput) probe(st aaStream) {
	p, ok := st.Probe()
	if !ok {
		return
	}
	a.s.logf("AudioTrack beside it: its timestamp places the device %v behind its frames, buffer %d; "+
		"getLatency %v; the output tells %v", p.Behind.Round(100*time.Microsecond), p.Buffer, p.Latency,
		audio.DurationAt(a.delay.Load()+a.extra(), a.hz).Round(100*time.Microsecond))
}

// askRoute asks Android of the route again where run.ask says it is
// time, and sets when to ask next. It runs on the opener.
func (a *aaOutput) askRoute() {
	a.mu.Lock()
	defer a.mu.Unlock()
	run := a.run.Load()
	if run == nil || a.paused.Load() || a.clock() < run.ask.Load() {
		// keep waits for none while paused, and Resume wakes it.
		return
	}
	a.route(run, false)
	run.asked++
	next := a.clock() + int64(aaRouteEvery)
	if run.asked == 1 {
		next = run.opened + int64(aaRouteLater)
	}
	run.ask.Store(next)
}

// A glide is a delay moving from one value to another, a frame for each
// aaSlew played, from a time on the output's clock.
type glide struct{ from, to, at int64 }

// now returns where s stands at time now, at rate hz.
func (s *glide) now(now int64, hz int) int64 {
	moved := audio.FramesAt(time.Duration(max(0, now-s.at)), hz) / aaSlew
	if s.to >= s.from {
		return min(s.to, s.from+moved)
	}
	return max(s.to, s.from-moved)
}

// extra returns what plays on past the output's timestamps, in frames,
// as it stands now.
func (a *aaOutput) extra() int64 {
	if s := a.unstamped.Load(); s != nil {
		return s.now(a.clock(), a.hz)
	}
	return 0
}

// setUnstamped sets what of run's route its timestamps leave out: at
// once where jump says, as a stream opens, and otherwise moving there
// from where it stands, a frame for each aaSlew played, either way.
func (a *aaOutput) setUnstamped(run *aaRun, jump bool) {
	want := int64(0)
	if run.bluetooth.Load() {
		want = unstamped(a.delay.Load(), int64(run.info.Buffer), int64(run.info.Burst), run.mixer.Load())
	}
	now := a.clock()
	was := a.unstamped.Load()
	switch {
	case jump || was == nil:
		a.unstamped.Store(&glide{want, want, now})
	case want != was.to:
		a.unstamped.Store(&glide{was.now(now, a.hz), want, now})
	}
}

// unstamped returns how much of a Bluetooth route's delay a stream's
// timestamps leave out, in frames: delay is how far behind its frames
// they place the device, buffer and burst the stream's, and mixer the
// latency Android's mixer reports for the output, 0 for none.
//
// On a Pixel 8 Pro with WF-1000XM4 earbuds, a shared stream's timestamp
// places the device about 50 ms behind, its own 40 ms buffer and little
// more, and Java's AudioTrack.getTimestamp about 40 ms; the earbuds play
// about 270 ms behind. Whether Android takes the position at its mixer
// or the Bluetooth HAL reports one without the headset, the headset's
// delay is left out. AudioManager's hidden getOutputLatency reports 200
// ms there: the headset's delay with the HAL's buffer. A timestamp that
// stops within a burst of the buffer counts none of that, and all of it
// is added. One that places the device further back counts what it
// places past the buffer and a burst as part of it, and only the rest
// is added. Where the two agree, that rest is a burst at most.
//
// Sources: ExtendedTimestamp's getBestTimestamp in AOSP's frameworks/av
// (media/libaudioclient), AudioStreamTrack::getTimestamp in
// media/libaaudio/src/legacy, PlaybackThread::latency_l in
// services/audioflinger, and out_get_latency_ms in the Bluetooth
// module's system/audio_bluetooth_hw.
func unstamped(delay, buffer, burst, mixer int64) int64 {
	if mixer <= 0 {
		return 0
	}
	counted := max(0, delay-buffer-burst)
	return max(0, mixer-counted)
}

// openIn opens the input stream, stopped. It runs with mu held.
func (a *aaOutput) openIn() error {
	mic := a.wantMic.Load()
	c := aaConfig{input: true, rate: a.hz, chans: 1, device: int(mic), data: a.in, blocks: a.blocks}
	st, err := a.openStream(c)
	if err != nil && c.device != 0 {
		// A take on any microphone beats no take.
		a.s.logf("the phone's own microphone, device %d, did not open: %v; letting Android pick", c.device, err)
		c.device = 0
		st, err = a.openStream(c)
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
	// Blocks of a stream gone count frames of their own. The feed reads
	// the rings only while an input is open, so it is still now.
	a.blocks.Drop()
	a.in.Drop()
	info := st.Info()
	a.iorg.Store(none)
	a.inDelay.Store(int64(info.Burst))
	run := &aaRun{st: st, sh: st.Shared(), info: info, opened: a.clock(), mic: mic}
	run.moved.Store(run.opened)
	run.posAt = run.opened
	a.irun.Store(run)
	if a.s.debug {
		a.s.logf("AAudio records at %d Hz, %d frames a burst, device %d of type %d, asked for device %d, "+
			"exclusive %v, low latency %v", info.Rate, info.Burst, info.Device, st.Route().Type, c.device,
			info.Exclusive, info.LowLatency)
	}
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

// start fills the ring and starts the streams, and the goroutine that
// keeps them going.
func (a *aaOutput) start(ahead time.Duration) {
	a.base = a.s.m.Mixed()
	a.setAhead(ahead)
	buf := make([]float32, 2*a.chunk())
	a.topUp(buf)
	if a.in != nil {
		a.tap = duplex.NewTap(driver.AAudio, a.hz, a.burst(), int(a.inDelay.Load()), []string{micName})
		duplex.Publish(a.tap)
	}
	a.mu.Lock()
	a.started = true
	if !a.startOut() {
		a.fails++
		a.next = a.clock() + int64(retryDelay(a.fails))
	}
	if !a.startIn() {
		a.ifails++
		a.inext = a.clock() + int64(micRetry(a.ifails))
	}
	a.mu.Unlock()
	go a.loop(buf)
	go a.keep()
}

// startOut starts the output stream, where it is open, and closes it
// where it fails to start. It runs with mu held.
// It returns false where it failed to start.
func (a *aaOutput) startOut() bool {
	if run := a.run.Load(); run != nil {
		if err := run.st.Start(); err != nil {
			a.s.logf("%v", err)
			a.dropOut()
			return false
		}
	}
	return true
}

// startIn starts the input stream as startOut does the output.
func (a *aaOutput) startIn() bool {
	if run := a.irun.Load(); run != nil {
		if err := run.st.Start(); err != nil {
			a.s.logf("%v", err)
			a.dropIn()
			return false
		}
	}
	return true
}

// chunk is the most frames the goroutine reads the mixer for at once.
func (a *aaOutput) chunk() int { return max(1, int(audio.FramesAt(chunk, a.hz))) }

func (a *aaOutput) burst() int {
	if run := a.run.Load(); run != nil {
		return run.info.Burst
	}
	return 0
}

// loop is the feed. It keeps the streams going until the output closes:
// it tops the ring up, reads the timestamps, hands on what was
// recorded, and tells the opener of a stream that failed. It runs at
// the system's audio priority, where there is one, as ringOutput's
// feed does, and never opens or closes a stream: that waits too long.
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
		// The opener, Suspend and Resume may close a stream meanwhile,
		// once no reader reads it.
		if !a.enter() {
			return
		}
		a.watch(&a.run, now)
		a.watch(&a.irun, now)
		if now-polled >= int64(aaPoll) {
			polled = now
			a.poll()
			if a.watching.Load() {
				a.devicesChanged(now)
				a.still(&a.run, now)
				a.still(&a.irun, now)
			}
		}
		a.topUp(buf)
		a.count(now)
		a.drain(ibuf, now)
		a.leave()
	}
}

// retryDelay is how long to wait before opening the output again after
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

// micRetry is how long to wait before opening the microphone again
// after fails attempts in a row failed: half a second at first, as the
// user may be answering the prompt for the permission to record, and
// twice as long each time after, to 2 s; after aaMicGiveUp, every
// aaMicSlow. Any error counts so: before the permission is given,
// Android 16 fails the open with AAUDIO_ERROR_INTERNAL, and earlier
// ones with others.
func micRetry(fails int) time.Duration {
	if fails >= aaMicGiveUp {
		return aaMicSlow
	}
	return min(500*time.Millisecond<<min(max(fails-1, 0), 2), 2*time.Second)
}

// lived says how a stream that is closing ran: one that ran aaProven
// clears the count of attempts that failed, and is opened again at
// once; one that died sooner counts as an attempt that failed. One
// moving is opened again at once, the count kept. It returns when to
// try next.
func (a *aaOutput) lived(run *aaRun, fails *int, delay func(int) time.Duration) int64 {
	now := a.clock()
	if run.moving.Load() {
		return now
	}
	if now-run.opened >= int64(aaProven) {
		*fails = 0
		return now
	}
	*fails++
	return now + int64(delay(*fails))
}

// watch marks the stream p holds dead where it failed, as failed
// tells, and wakes the opener to open another. It runs on the feed, as
// a reader of the streams.
func (a *aaOutput) watch(p *atomic.Pointer[aaRun], now int64) {
	if run := p.Load(); run != nil && !run.dead.Load() && a.failed(run, now) {
		run.dead.Store(true)
		a.wakeOpener()
	}
}

// still marks the stream p holds dead where its device stood still, as
// stood says, and wakes the opener. It runs on the feed, as watch does.
func (a *aaOutput) still(p *atomic.Pointer[aaRun], now int64) {
	if run := p.Load(); run != nil && !run.dead.Load() && a.stood(run, now) {
		run.dead.Store(true)
		a.wakeOpener()
	}
}

func (a *aaOutput) wakeOpener() {
	select {
	case a.reopen <- struct{}{}:
	default:
	}
}

func (a *aaOutput) wakeFeed() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// keep is the opener: it opens a stream again whose device went away,
// or that stopped taking buffers, and one that failed to open, when it
// may try next, until the output closes. The feed plays on meanwhile.
func (a *aaOutput) keep() {
	defer close(a.kept)
	for {
		var next, ask <-chan time.Time
		if wait, ok := a.keepOpen(); ok {
			next = time.After(wait)
		}
		if run := a.run.Load(); run != nil && !a.paused.Load() {
			// Resume wakes the opener, to ask again.
			ask = time.After(time.Duration(max(0, run.ask.Load()-a.clock())))
		}
		var quiet <-chan time.Time
		if q := a.quiet.Load(); q != 0 && !a.paused.Load() {
			quiet = time.After(time.Duration(max(0, q-a.clock())))
		}
		select {
		case <-a.quit:
			return
		case <-a.reopen:
		case <-next:
		case <-ask:
			a.askRoute()
		case <-quiet:
		}
	}
}

// keepOpen closes the streams the feed found dead, and opens those
// closed whose time has come. It returns how long until the next it
// must try, and false where it waits for none.
func (a *aaOutput) keepOpen() (wait time.Duration, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.paused.Load() {
		return 0, false
	}
	a.moveStreams()
	// The count of attempts that failed is cleared only by a stream
	// that ran aaProven, so one that opens and fails at once waits as
	// one that fails to open.
	if run := a.run.Load(); run != nil && run.dead.Load() {
		a.next = a.lived(run, &a.fails, retryDelay)
		a.dropOut()
	}
	if a.run.Load() == nil && a.clock() >= a.next {
		done := a.busy()
		err := a.openOut()
		if err == nil {
			// The feed fills the ring as the stream starts.
			a.wakeFeed()
			if !a.startOut() {
				err = errors.New("speaker: AAudio's output did not start")
			}
		}
		if err != nil {
			a.fails++
			a.next = a.clock() + int64(retryDelay(a.fails))
			a.s.logf("%v; trying again in %v", err, retryDelay(a.fails))
		}
		done()
	}
	if a.in != nil {
		if run := a.irun.Load(); run != nil && run.dead.Load() {
			a.inext = a.lived(run, &a.ifails, micRetry)
			a.dropIn()
		} else if run != nil && run.mic != a.wantMic.Load() {
			// The sound moved to or from Bluetooth: the microphone moves
			// as it would have opened there, its count of failures kept.
			a.s.logf("the input should record on device %d now; opening it again", a.wantMic.Load())
			a.inext = a.clock()
			a.dropIn()
		}
		if a.irun.Load() == nil && a.clock() >= a.inext {
			done := a.busy()
			err := a.openIn()
			if err == nil && !a.startIn() {
				err = errors.New("speaker: the microphone did not start")
			}
			if err != nil {
				// The permission to record may come later: try again.
				a.ifails++
				a.inext = a.clock() + int64(micRetry(a.ifails))
				a.s.logf("%v; trying again in %v", err, micRetry(a.ifails))
			}
			done()
		}
	}
	now := a.clock()
	wait = time.Duration(math.MaxInt64)
	if a.run.Load() == nil {
		wait, ok = time.Duration(a.next-now), true
	}
	if a.in != nil && a.irun.Load() == nil {
		wait, ok = min(wait, time.Duration(a.inext-now)), true
	}
	return max(wait, 0), ok
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
	if now := a.clock(); a.s.debug && now-a.told >= int64(time.Second) {
		a.told = now
		a.tell("plays", a.run.Load(), false, a.delay.Load()+a.extra())
		a.tell("records", a.irun.Load(), true, a.inDelay.Load())
	}
	if run := a.run.Load(); run != nil {
		run.most.Store(run.sh.Most.Load())
		if behind, first, ok := a.fit(run, &a.org, false); first {
			a.delay.Store(behind)
		} else if ok {
			a.delay.Store(smooth(a.delay.Load(), behind))
		}
		a.setUnstamped(run, false)
		if x := run.st.XRuns(); x > run.xruns {
			if now := a.clock(); a.settled(run, now) {
				a.ranDry(min(x-run.xruns, 100), now)
			}
			run.xruns = x
		}
		a.setTarget()
	}
	if run := a.irun.Load(); run != nil {
		if ahead, first, ok := a.fit(run, &a.iorg, true); first {
			a.inDelay.Store(ahead)
		} else if ok {
			a.inDelay.Store(smooth(a.inDelay.Load(), ahead))
		}
	}
}

// tell logs what run's timestamp says as it stands, fitted or not,
// beside the delay the output tells, to check one against the other.
func (a *aaOutput) tell(does string, run *aaRun, input bool, told int64) {
	if run == nil {
		return
	}
	pos, at, cnow, ok := run.st.Timestamp()
	carried := run.sh.Frames.Load()
	if !ok {
		a.s.logf("AAudio %s: no timestamp yet; %d frames carried", does, carried)
		return
	}
	// As Oboe's calculateLatencyMillis reckons it.
	gap := float64(carried-pos)/float64(a.hz) - float64(cnow-at)/1e9
	if input {
		gap = -gap
	}
	info := run.st.Info()
	a.s.logf("AAudio %s: frame %d at the device %v ago, %d carried: %v between; the output tells %v; "+
		"%d in a row did not fit; buffer %d of %d, burst %d, exclusive %v, %d xruns; "+
		"Bluetooth %v, the mixer reports %v, %v added",
		does, pos, time.Duration(cnow-at).Round(time.Microsecond), carried,
		time.Duration(gap*1e9).Round(100*time.Microsecond), audio.DurationAt(told, a.hz).Round(100*time.Microsecond),
		run.odd, info.Buffer, info.Capacity, info.Burst, info.Exclusive, run.st.XRuns(),
		run.bluetooth.Load(), audio.DurationAt(run.mixer.Load(), a.hz), audio.DurationAt(a.extra(), a.hz))
}

// fit reads run's timestamp into org, the origin of its frames, where
// it fits the frames its callback carried: an output's device plays
// behind them, an input's records ahead of them, by as much as
// MaxLatency, and by two callbacks at most the other way; and, once a
// timestamp of run's has fitted, within aaShift of the last that did.
// It returns how far, and first for the first of run's that fits, which
// is taken as it is: the delay the stream was opened with is a guess.
// One that does not fit is let be, and counted in run.odd: a stream may
// count its frames anew under AAudio, as where the system's sound
// server restarted and the stream with it.
func (a *aaOutput) fit(run *aaRun, org *atomic.Uint64, input bool) (gap int64, first, ok bool) {
	pos, at, cnow, ok := run.st.Timestamp()
	if !ok || pos <= 0 {
		return 0, false, false
	}
	now := a.clock()
	// The timestamp on a.clock, as the two clocks stood just now.
	at -= cnow - now
	k := float64(a.hz) / 1e9
	raw := float64(pos) - float64(at)*k
	gapAt := func(org float64) int64 {
		g := run.sh.Frames.Load() - int64(org+float64(now)*k)
		if input {
			return -g
		}
		return g
	}
	slack := 2 * max(run.sh.Most.Load(), int64(run.info.Burst))
	g := gapAt(raw)
	if g < -slack || g > audio.FramesAt(MaxLatency, a.hz) || (run.fitted && abs64(g-run.gap) > audio.FramesAt(aaShift, a.hz)) {
		run.odd++
		if a.s.debug {
			a.s.logf("AAudio's timestamp does not fit: %v between, after %v", audio.DurationAt(g, a.hz),
				audio.DurationAt(run.gap, a.hz))
		}
		return 0, false, false
	}
	first = !run.fitted
	run.odd, run.fitted, run.gap = 0, true, g
	return max(0, gapAt(a.steady(org, raw))), first, true
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
// callbacks are large, and come late and two at once; and past that,
// the margin dry spells grew.
func (a *aaOutput) setTarget() {
	least := int64(a.chunk())
	if run := a.run.Load(); run != nil {
		least += 2 * max(run.most.Load(), int64(run.info.Burst))
	}
	t := max(audio.FramesAt(time.Duration(a.ahead.Load()), a.hz)-a.delay.Load()-a.extra(), least) +
		audio.FramesAt(time.Duration(a.s.margin.Load()), a.hz)
	a.target.Store(min(t, a.q.Size()-int64(a.chunk())))
}

// remargin takes the margin the speaker grew. The delay is what the
// timestamps place, and Bluetooth's what Android reports past them:
// both run long on a headset, and the first timestamp may place it
// far off.
func (a *aaOutput) remargin() { a.setTarget() }

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
// since it first carried a buffer after it opened or resumed: a stream
// starting may take several buffers at once, to fill the device.
func (a *aaOutput) count(now int64) {
	run := a.run.Load()
	if run == nil {
		return
	}
	if w := run.waitPast.Load(); w >= 0 && run.sh.Frames.Load() > w && run.waitPast.CompareAndSwap(w, -1) {
		a.settle.Store(now)
	}
	if u := run.sh.Underruns.Load(); u > run.underruns {
		if a.primed.Load() && a.settled(run, now) {
			a.ranDry(min(u-run.underruns, 100), now)
		}
		run.underruns = u
	}
}

// settled says run's device has played aaSettle since it first
// carried a buffer after it opened or resumed.
func (a *aaOutput) settled(run *aaRun, now int64) bool {
	return run.waitPast.Load() < 0 && now-a.settle.Load() > int64(aaSettle)
}

// ranDry counts n dry spells. Those while a stream opens or starts,
// and for aaSettle after, count as dropouts but leave the latency as
// it is: an input opening may hold the system up a moment, once, and a
// latency grown for that would stay grown.
func (a *aaOutput) ranDry(n, now int64) {
	steady := a.opening.Load() == 0 && now >= a.calm.Load()
	for range n {
		if steady {
			a.s.ranDry()
		} else {
			a.s.ranDryOnce()
		}
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
	// The rings are the opener's while no input is open.
	irun := a.irun.Load()
	if irun == nil {
		return
	}
	if now-irun.opened < int64(aaWaitStamp) &&
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
	out -= float64(a.extra())
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
		return carried - a.delay.Load() - a.extra(), carried
	}
	at = int64(org+float64(a.clock())*float64(a.hz)/1e9) - a.extra()
	// Not past what it was given, as while it is paused, nor further
	// behind than the speaker ever works.
	return min(max(at, carried-audio.FramesAt(MaxLatency, a.hz)), carried), carried
}

// held counts from the mixer's own count of the frames it mixed, not
// the ring's: the goroutine mixes a chunk before it adds it to the ring,
// and the mixer's Heard, which never goes back, would keep a frame heard
// read in between, a chunk too far on.
func (a *aaOutput) held() int64 {
	if !a.enter() {
		return 0
	}
	defer a.leave()
	written := a.s.m.Mixed() - a.base
	run := a.run.Load()
	if run == nil {
		return max(0, written-a.q.Read())
	}
	at, _ := a.playing(run)
	return max(0, written-run.start+run.sh.Silence.Load()-at)
}

func (a *aaOutput) setAhead(d time.Duration) {
	a.ahead.Store(int64(d))
	a.setTarget()
}

func (a *aaOutput) latency() time.Duration {
	return audio.DurationAt(a.target.Load()+a.delay.Load()+a.extra(), a.hz)
}

func (a *aaOutput) rate() int { return a.hz }

func (a *aaOutput) fill(st *State) {
	if a.byName {
		st.Driver = driver.AAudio
	}
	info := driver.Info{Driver: driver.AAudio, Rate: a.hz, Bits: 32, Float: true, Outputs: 2, Played: []int{0, 1},
		Latency: int(a.delay.Load() + a.extra())}
	st.Route = Route(a.on.Load())
	st.Headphones = a.phones.Load()
	if run := a.run.Load(); run != nil {
		info.Buffer, info.MinBuffer, info.PreferredBuffer = run.info.Burst, run.info.Burst, run.info.Burst
		info.MaxBuffer = run.info.Capacity
	}
	if a.in != nil {
		info.Inputs, info.Recorded, info.InputNames = 1, 1, []string{micName}
		info.InputLatency = int(a.inDelay.Load())
	}
	st.Info = &info
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
		run.resumed.Store(a.clock())
		run.waitPast.Store(run.sh.Frames.Load())
		a.startOut()
	}
	// The user may have allowed recording meanwhile: try the
	// microphone at once, its count of failures kept.
	a.inext = a.clock()
	a.wakeFeed()
	a.wakeOpener()
	return nil
}

func (a *aaOutput) close() error {
	if a.tap != nil {
		duplex.Withdraw(a.tap)
	}
	close(a.quit)
	<-a.done
	<-a.kept
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
