package speaker

import (
	"fmt"
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/driver"
	"github.com/marrasen/gunim/audio/internal/audiounit"
	"github.com/marrasen/gunim/audio/internal/duplex"
	"github.com/marrasen/gunim/audio/rt"
)

// An auDevice is Core Audio's default output, and its default input, as
// package audio/internal/audiounit opens them: the device takes each
// buffer from a ring on its own thread, and the speaker fills the ring.
type auDevice interface {
	Info() audiounit.Info
	Rate() int
	Channels() int
	Buffer() int
	Delay() int
	Write(frames []float32) int
	Written() int64
	Queued() int64
	Room() int64
	Heard() (int64, bool)
	Read(dst []float32) (n int, at int64, ok bool)
	Poll() audiounit.Events
	Pause(paused bool)
	Suspend() error
	Resume() error
	Close() error
}

// The pace of opening again as the devices change.
const (
	// auSteady is how long an output plays before a change no longer
	// counts as one more in a row.
	auSteady = 10 * time.Second
	// auWait is how long a change waits before the output opens again,
	// from the third in a row, doubling with each after, as far as
	// auWaitMost.
	auWait     = time.Second
	auWaitMost = 16 * time.Second
	// auChurned is how many in a row State.Problem tells of.
	auChurned = 3
)

// openAU opens Core Audio's devices; a test puts a device of its own
// here.
var openAU = func(c audiounit.Config) (auDevice, error) {
	d, err := audiounit.Open(c)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// auOutput plays through Core Audio's default output device, by a HAL
// output unit. Core Audio's thread takes each buffer from a ring, with
// no Go; a goroutine, the pump, reads the mixer ahead into the ring,
// and hands what the default input recorded to package audio/capture.
// It works as jackOutput does, but that the device's time stamps place
// the frame heard, and the device follows the system: the speaker opens
// again as the default device changes, goes away or changes its rate.
type auOutput struct {
	s   *Speaker
	dev auDevice
	// byName says Options.Driver named Core Audio: State tells it as
	// the driver. Otherwise it plays as the system's own sound.
	byName bool
	hz     int
	// chans is how many channels a frame of the ring holds: the
	// mixer's pair, or the one it is averaged onto, then those
	// Options.Aux fills.
	chans int
	// base is the mixer frame the ring's first frame is: the pump alone
	// reads the mixer, so frame i of the ring is the mixer's base+i.
	base atomic.Int64
	// ahead is how far ahead the mixer works, as setAhead last set it,
	// in nanoseconds.
	ahead atomic.Int64
	// primed says the ring has reached its target once, so a buffer the
	// device finds short is the sound running dry.
	primed, paused atomic.Bool
	tap            atomic.Pointer[duplex.Tap]
	// mu guards info.
	mu      sync.Mutex
	info    audiounit.Info
	quit    chan struct{}
	done    chan struct{}
	closing sync.Once
	// opened is when the output opened, churn how many opened in a row
	// before it, as Speaker.auChurn counts them, and problem why it
	// opened in a row so often, until it has played auSteady.
	opened  time.Time
	churn   int64
	problem atomic.Pointer[error]
	// What the pump alone keeps: the frames it wrote, ever; why it is to
	// open again, and when; whether it asked; and the cycles as the pump
	// last saw them change.
	written  int64
	reopen   string
	reopenAt time.Time
	asked    bool
	cycles   int64
	cycledAt time.Time
	stalled  bool
	// settle tells the short buffers of the device starting or resuming
	// from those of the sound running dry.
	settle settling
}

// auBuffer returns the buffer to ask the device for: Options.Buffer, or
// a quarter of the latency the speaker works at, rounded down to a power
// of two, from 64 to 1024 frames. The device brings it into its range.
func auBuffer(o Options, ahead time.Duration, rate int) int {
	if o.Buffer > 0 {
		return o.Buffer
	}
	quarter := max(audio.FramesAt(ahead, rate)/4, 1)
	return min(max(1<<(bits.Len64(uint64(quarter))-1), 64), 1024)
}

// coreAudioFirst plays through Core Audio's HAL output unit where
// Options names no driver, on macOS; a test sets it elsewhere.
var coreAudioFirst = runtime.GOOS == "darwin"

// driverFirst says a driver plays where Options names none.
func driverFirst() bool { return aaudioFirst || wasapiFirst || coreAudioFirst }

// openAUOutput opens Core Audio's default output, playing on the
// channels Options.Outputs names, and the default input where
// Options.Record says. byName says Options.Driver named it.
func openAUOutput(s *Speaker, o Options, byName bool) (*auOutput, error) {
	buffer := auBuffer(o, time.Duration(s.ahead.Load()+s.margin.Load()), o.rate())
	// As the system's own sound, it sets the device to the rate asked,
	// as the older output did; named as a driver, it follows the
	// device's.
	rate := o.Rate
	if byName {
		rate = 0
	}
	dev, err := openAU(audiounit.Config{Outputs: o.Outputs, Record: o.Record, Rate: rate, Buffer: buffer,
		Hold: MaxLatency, Log: s.logf})
	if err != nil {
		return nil, err
	}
	info := dev.Info()
	now := time.Now()
	churn := int64(0)
	if last := s.auOpened.Load(); last != 0 && now.Sub(time.Unix(0, last)) < auSteady {
		churn = s.auChurn.Add(1)
	} else {
		s.auChurn.Store(0)
	}
	s.auOpened.Store(now.UnixNano())
	u := &auOutput{s: s, dev: dev, byName: byName, hz: dev.Rate(), chans: dev.Channels(), info: info, opened: now, churn: churn,
		quit: make(chan struct{}), done: make(chan struct{})}
	if churn >= auChurned {
		err := fmt.Errorf("speaker: Core Audio: the devices changed %d times in a row as the output opened, "+
			"as AirPods do as their microphone opens: it opens again more slowly, from %v apart", churn, u.wait())
		u.problem.Store(&err)
		s.logf("%v", err)
	}
	s.logf("Core Audio: %s at %d Hz, %d frames a buffer, plays on %v of %d; %d frames from a buffer to the ear",
		info.Device, info.Rate, info.Buffer, info.Played, len(info.Outputs), dev.Delay())
	if info.InputProblem != nil {
		s.logf("Core Audio: %v", info.InputProblem)
	}
	return u, nil
}

// start starts the pump, working ahead as far as ahead.
func (u *auOutput) start(ahead time.Duration) {
	u.setAhead(ahead)
	u.base.Store(u.s.m.Mixed())
	u.cycledAt = time.Now()
	if u.info.Recorded > 0 {
		tap := duplex.NewTap(driver.CoreAudio, u.hz, u.info.InputBuffer, u.info.InputLatency,
			u.info.Inputs[:u.info.Recorded])
		u.tap.Store(tap)
		duplex.Publish(tap)
	}
	go u.pump()
}

// device returns the frames from a frame written as the device starts a
// buffer to its being heard.
func (u *auOutput) device() int64 { return int64(u.dev.Delay()) }

// target returns how many frames the ring is kept at: what Latency
// leaves after the device's own, and a buffer and two of the pump's
// ticks at least; and past that, the margin dry spells grew.
func (u *auOutput) target() int64 {
	least := int64(u.dev.Buffer()) + audio.FramesAt(2*pumpTick, u.hz)
	return max(audio.FramesAt(time.Duration(u.ahead.Load()), u.hz)-u.device(), least) +
		audio.FramesAt(time.Duration(u.s.margin.Load()), u.hz)
}

// remargin takes the margin the speaker grew: target reads it as it
// goes. The device's delay is what it reports, and Bluetooth
// headphones report a long one.
func (u *auOutput) remargin() {}

// pump keeps the ring filled to its target, hands the input on, and
// hears what the device tells, until the output closes. The device waits
// on it, so it runs on a thread of its own at the system's audio
// priority, as package audio/rt gives it.
func (u *auOutput) pump() {
	defer close(u.done)
	leave := rt.Enter()
	defer leave()
	t := time.NewTicker(pumpTick)
	defer t.Stop()
	most := max(1, int(audio.FramesAt(chunk, u.hz)))
	buf := make([]float32, 2*most)
	var aux, wide []float32
	if u.chans > 2 {
		aux = make([]float32, (u.chans-2)*most)
	}
	if u.chans != 2 {
		wide = make([]float32, u.chans*most)
	}
	var in []float32
	if u.info.Recorded > 0 {
		in = make([]float32, u.info.Recorded*8192)
	}
	for {
		select {
		case <-u.quit:
			return
		case <-t.C:
		}
		u.hear()
		if !u.paused.Load() {
			u.feed(buf, aux, wide, most)
		}
		if in != nil {
			u.record(in)
		}
	}
}

// feed reads the mixer into the ring, as far as its target.
func (u *auOutput) feed(buf, aux, wide []float32, most int) {
	target := u.target()
	for {
		want := target - u.dev.Queued()
		if want <= 0 {
			u.primed.Store(true)
			return
		}
		n := min(want, int64(most), u.dev.Room())
		if n <= 0 {
			return
		}
		frames := buf[:2*n]
		u.s.pull(frames)
		switch u.chans {
		case 2:
			u.dev.Write(frames)
		case 1:
			w := wide[:n]
			for i := range w {
				w[i] = (frames[2*i] + frames[2*i+1]) / 2
			}
			u.dev.Write(w)
		default:
			a := aux[:int64(u.chans-2)*n]
			u.s.pullAux(a, u.chans-2, u.base.Load()+u.written)
			w := wide[:int64(u.chans)*n]
			interleave(w, frames, a, u.chans)
			u.dev.Write(w)
		}
		u.written += n
	}
}

// record hands on what the input recorded, each block with the mixer
// frame heard as it reached the input, as the device's time stamps
// place it.
func (u *auOutput) record(in []float32) {
	ch := u.info.Recorded
	tap := u.tap.Load()
	for {
		n, at, ok := u.dev.Read(in)
		if !ok {
			return
		}
		if tap == nil || !tap.Attached() {
			continue
		}
		tap.Deliver(in[:n*ch], ch, u.base.Load()+at)
	}
}

// hear acts on what the device told since the pump last asked.
func (u *auOutput) hear() {
	e := u.dev.Poll()
	s := u.s
	if u.primed.Load() && !u.paused.Load() {
		u.settle.count(s, e.Short, e.Cycles, time.Now())
	}
	if e.Lost > 0 {
		s.logf("Core Audio: the input found its ring full: %d frames lost", e.Lost)
	}
	if e.Failed > 0 {
		s.logf("Core Audio: the input failed to render %d times", e.Failed)
	}
	if e.Changed {
		info := u.dev.Info()
		u.mu.Lock()
		u.info = info
		u.mu.Unlock()
	}
	now := time.Now()
	if e.Reopen != "" && u.reopen == "" {
		u.reopen = e.Reopen
		u.reopenAt = u.opened.Add(u.wait())
		if now.Sub(u.opened) >= auSteady {
			// Played steady: a change now is the first of a row.
			u.reopenAt = now
		}
		if wait := u.reopenAt.Sub(now); wait > 0 {
			s.logf("Core Audio: %s; opening again in %v, as the devices changed %d times in a row",
				e.Reopen, wait.Round(time.Millisecond), u.churn+1)
		}
	}
	if u.reopen != "" && !u.asked && !now.Before(u.reopenAt) {
		u.asked = true
		s.logf("Core Audio: %s; opening again", u.reopen)
		s.askReset()
	}
	if u.reopen == "" && u.problem.Load() != nil && now.Sub(u.opened) >= auSteady {
		u.problem.Store(nil)
	}
	if e.Cycles != u.cycles {
		u.cycles, u.cycledAt, u.stalled = e.Cycles, time.Now(), false
	} else if !u.stalled && !u.paused.Load() && time.Since(u.cycledAt) > 2*time.Second {
		u.stalled = true
		s.logf("Core Audio: the device has asked for no sound in %v", time.Since(u.cycledAt).Round(time.Millisecond))
	}
}

// wait returns how long after it opened the output waits to open again
// as the devices change: not at all for the first two in a row, then
// auWait, doubling, as far as auWaitMost.
func (u *auOutput) wait() time.Duration {
	if u.churn < 2 {
		return 0
	}
	return min(auWait<<min(u.churn-2, 8), auWaitMost)
}

func (u *auOutput) rate() int { return u.hz }

// held counts the frames mixed that are still to be heard, as the
// device's time stamps place the frame heard: so it moves on through a
// buffer, not a buffer at a time, and silence played while the ring ran
// dry counts for nothing. Before the device has stamped a buffer, it is
// the frames mixed and not taken, and the device's delay. It counts from
// the mixer's own count of the frames it mixed, not the ring's: the pump
// mixes a chunk before it adds it to the ring, and the mixer's Heard,
// which never goes back, would keep a frame heard read in between, a
// chunk too far on.
func (u *auOutput) held() int64 {
	mixed := u.s.m.Mixed() - u.base.Load()
	if at, ok := u.dev.Heard(); ok {
		return max(0, mixed-at)
	}
	taken := u.dev.Written() - u.dev.Queued()
	return max(0, mixed-taken) + u.device()
}

func (u *auOutput) setAhead(d time.Duration) { u.ahead.Store(int64(d)) }

func (u *auOutput) latency() time.Duration {
	return audio.DurationAt(u.target()+u.device(), u.hz)
}

func (u *auOutput) fill(st *State) {
	u.mu.Lock()
	i := u.info
	u.mu.Unlock()
	if u.byName {
		st.Driver = driver.CoreAudio
	}
	st.Aux = max(u.chans-2, 0)
	st.Info = &driver.Info{Driver: driver.CoreAudio, Rate: i.Rate, Buffer: i.Buffer, MinBuffer: i.MinBuffer,
		MaxBuffer: i.MaxBuffer, PreferredBuffer: i.Buffer, Latency: i.Buffer + i.Safety + i.Latency, Bits: 32,
		Float: true, Outputs: len(i.Outputs), OutputNames: i.Outputs, Played: i.Played,
		Inputs: len(i.Inputs), Recorded: i.Recorded, InputNames: i.Inputs[:i.Recorded], InputLatency: i.InputLatency}
	if p := u.problem.Load(); p != nil && st.Problem == nil {
		st.Problem = *p
	}
	if st.Problem == nil {
		st.Problem = i.InputProblem
	}
}

func (u *auOutput) err() error { return nil }

// suspend stops the units, so the device sleeps and the microphone is
// let go; the ring keeps what it holds.
func (u *auOutput) suspend() error {
	u.paused.Store(true)
	return u.dev.Suspend()
}

func (u *auOutput) resume() error {
	u.settle.restart()
	err := u.dev.Resume()
	u.paused.Store(false)
	return err
}

// close stops the pump and lets the device go, once: later calls do
// nothing. A device that fails to close is gone all the same, so the
// failure is logged, not returned: the speaker would keep the output.
func (u *auOutput) close() error {
	u.closing.Do(func() {
		if tap := u.tap.Load(); tap != nil {
			duplex.Withdraw(tap)
		}
		close(u.quit)
		<-u.done
		if err := u.dev.Close(); err != nil {
			u.s.logf("Core Audio: %v", err)
		}
	})
	return nil
}

// auControlPanel opens Audio MIDI Setup; a test puts a function of its
// own here.
var auControlPanel = audiounit.ControlPanel
