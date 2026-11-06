package speaker

import (
	"errors"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/driver"
	"github.com/marrasen/gunim/audio/internal/duplex"
	"github.com/marrasen/gunim/audio/internal/pipewire"
	"github.com/marrasen/gunim/audio/rt"
)

// A pwDevice is a client of a PipeWire server, as package
// audio/internal/pipewire opens it: PipeWire takes each cycle from a
// ring on its own thread, and the speaker fills the ring.
type pwDevice interface {
	Info() pipewire.Info
	Write(frames []float32) int
	Queued() int64
	Room() int64
	Held() int64
	Read(dst []float32) (n int, at int64, ok bool)
	Poll() pipewire.Events
	Pause(paused bool)
	Err() error
	Close() error
}

// openPipeWire opens a PipeWire client; a test puts a device of its
// own here.
var openPipeWire = func(c pipewire.Config) (pwDevice, error) {
	d, err := pipewire.Open(c)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// pipeWireFirst plays through PipeWire where Options names no driver, on
// Linux; a test turns it off.
var pipeWireFirst = runtime.GOOS == "linux"

// feedBreathe is how long a thread that keeps a ring filled goes without a
// sleep, at the most, though the ring never fills.
const feedBreathe = 20 * time.Millisecond

// errPWPanel is what ControlPanel returns while PipeWire plays.
var errPWPanel = errors.New("speaker: PipeWire has no control panel: choose the device in the system's " +
	"sound settings, or in pavucontrol")

// pwChannels names the channels PipeWire's streams play and record.
var pwChannels = []string{"left", "right"}

// pwOutput plays through a PipeWire server. PipeWire takes each cycle
// from a ring on its own real-time thread, with no Go; a goroutine, the
// pump, reads the mixer ahead into the ring, and hands what the input
// recorded to package audio/capture.
type pwOutput struct {
	s *Speaker
	// byName says Options.Driver named PipeWire: State tells it as the
	// driver. Where it plays as the system's own sound, State tells none.
	byName bool
	dev    pwDevice
	hz     int
	// base is the mixer frame the ring's first frame is: the pump alone
	// reads the mixer, so frame i of the ring is the mixer's base+i.
	base atomic.Int64
	// ahead is how far ahead the mixer works, as setAhead last set it,
	// in nanoseconds.
	ahead atomic.Int64
	// quantum is the frames of PipeWire's last cycle, and delay the
	// frames from a cycle's first frame to its being heard, as the pump
	// last polled them; the quantum asked for until the first cycle.
	quantum, delay, deviceRate atomic.Int64
	// primed says the ring has reached its target once, so a cycle
	// PipeWire finds short is the sound running dry.
	primed, paused atomic.Bool
	tap            atomic.Pointer[duplex.Tap]
	quit           chan struct{}
	done           chan struct{}
	closing        sync.Once
	// What the pump alone keeps: the stream failed; the cycles as the
	// pump last saw them change; and the streams' states.
	shut     bool
	cycles   int64
	cycledAt time.Time
	stalled  bool
	out, in  pipewire.State
	// settle tells the short cycles of the stream starting, resuming or
	// moving to another device from those of the sound running dry.
	settle settling
}

// openPWOutput opens a client of the PipeWire server, playing to the
// default output at the rate Options asks, and recording the default
// input where Options.Record says. It asks for cycles of Options.Buffer
// frames, or a quarter of the latency, as Core Audio does.
func openPWOutput(s *Speaker, o Options, byName bool) (*pwOutput, error) {
	ahead := time.Duration(s.ahead.Load() + s.margin.Load())
	quantum := auBuffer(o, ahead, o.rate())
	// As the system's own sound it plays alone: package audio/capture
	// records the system's inputs.
	dev, err := openPipeWire(pipewire.Config{Name: o.Name, Rate: o.rate(), Quantum: quantum, Record: o.Record && byName,
		Hold: MaxLatency, Log: s.logf})
	if err != nil {
		return nil, err
	}
	info := dev.Info()
	p := &pwOutput{s: s, dev: dev, byName: byName, hz: info.Rate, quit: make(chan struct{}), done: make(chan struct{})}
	p.quantum.Store(int64(info.Quantum))
	s.logf("PipeWire: libpipewire %s at %d Hz, asking %d frames a cycle, recording %v",
		info.Library, info.Rate, info.Quantum, info.Recording)
	return p, nil
}

// start starts the pump, working ahead as far as ahead.
func (p *pwOutput) start(ahead time.Duration) {
	p.setAhead(ahead)
	p.base.Store(p.s.m.Mixed())
	p.cycledAt = time.Now()
	if info := p.dev.Info(); info.Recording {
		tap := duplex.NewTap(driver.PipeWire, p.hz, info.Quantum, info.Quantum, pwChannels)
		p.tap.Store(tap)
		duplex.Publish(tap)
	}
	go p.pump()
}

// device returns the frames from a cycle PipeWire starts to the last of
// it heard: the cycle, and the delay PipeWire reports past it.
func (p *pwOutput) device() int64 { return p.quantum.Load() + p.delay.Load() }

// target returns how many frames the ring is kept at: what Latency
// leaves after the device's own, and a cycle and two of the pump's
// ticks at least; and past that, the margin dry spells grew.
func (p *pwOutput) target() int64 {
	least := p.quantum.Load() + audio.FramesAt(2*pumpTick, p.hz)
	return max(audio.FramesAt(time.Duration(p.ahead.Load()), p.hz)-p.device(), least) +
		audio.FramesAt(time.Duration(p.s.margin.Load()), p.hz)
}

// remargin takes the margin the speaker grew: target reads it as it
// goes. The delay comes from PipeWire's time reports, and changes as
// the stream moves, as to a Bluetooth headset.
func (p *pwOutput) remargin() {}

// pump keeps the ring filled to its target, hands the input on, and
// hears what PipeWire tells, until the output closes. PipeWire waits on
// it, so it runs on a thread of its own at the system's audio priority,
// as package audio/rt gives it.
func (p *pwOutput) pump() {
	defer close(p.done)
	leave := rt.Enter()
	defer leave()
	t := time.NewTicker(pumpTick)
	defer t.Stop()
	most := max(1, int(audio.FramesAt(chunk, p.hz)))
	buf := make([]float32, pipewire.Channels*most)
	var in []float32
	if p.dev.Info().Recording {
		in = make([]float32, pipewire.Channels*8192)
	}
	for {
		select {
		case <-p.quit:
			return
		case <-t.C:
		}
		p.hear()
		if !p.paused.Load() {
			p.feed(buf, most)
		}
		if in != nil {
			p.record(in)
		}
	}
}

// feed reads the mixer into the ring, as far as its target.
func (p *pwOutput) feed(buf []float32, most int) {
	target := p.target()
	began := time.Now()
	for {
		// A song too heavy for the ring never reaches its target, and the
		// system limits how long a real-time thread may run without a sleep.
		if now := time.Now(); now.Sub(began) > feedBreathe {
			began = now
			rt.Breathe()
		}
		want := target - p.dev.Queued()
		if want <= 0 {
			p.primed.Store(true)
			return
		}
		n := min(want, int64(most), p.dev.Room())
		if n <= 0 {
			return
		}
		frames := buf[:pipewire.Channels*n]
		p.s.pull(frames)
		p.dev.Write(frames)
	}
}

// record hands on what the input recorded, with the mixer frame heard
// as it reached the input, as the client places it on the output's
// time line.
func (p *pwOutput) record(in []float32) {
	tap := p.tap.Load()
	for {
		n, at, ok := p.dev.Read(in)
		if !ok {
			return
		}
		if tap == nil || !tap.Attached() {
			continue
		}
		tap.Deliver(in[:n*pipewire.Channels], pipewire.Channels, p.base.Load()+at)
	}
}

// hear acts on what PipeWire told since the pump last asked.
func (p *pwOutput) hear() {
	e := p.dev.Poll()
	s := p.s
	if e.Moved && e.Out == pipewire.StateStreaming && p.out != pipewire.StateStreaming {
		// Streaming again, as after a move to another device: the first
		// cycles may find the ring short as the new device starts.
		p.settle.restart()
	}
	// The settle window starts with the first cycle, primed or not; a
	// short cycle counts once the ring has filled, while it plays.
	short := e.Short
	if !p.primed.Load() || p.paused.Load() {
		short = 0
	}
	p.settle.count(s, short, e.Cycles, time.Now())
	if e.Lost > 0 {
		s.logf("PipeWire: the input found its ring full: %d frames lost", e.Lost)
	}
	if e.Moved {
		s.logf("PipeWire: the output is %v, the input %v", e.Out, e.In)
	}
	p.out, p.in = e.Out, e.In
	if e.Cycles > 0 {
		q, d := int64(e.Quantum), int64(e.Delay)
		if q != p.quantum.Load() || d != p.delay.Load() || int64(e.DeviceRate) != p.deviceRate.Load() {
			p.quantum.Store(q)
			p.delay.Store(d)
			p.deviceRate.Store(int64(e.DeviceRate))
			s.logf("PipeWire: %d frames a cycle, %d frames of delay, the device at %d Hz; working %v ahead",
				q, d, e.DeviceRate, p.latency())
		}
	}
	if !p.shut && (e.Out == pipewire.StateError || e.In == pipewire.StateError || e.Out == pipewire.StateUnconnected) {
		p.shut = true
		s.logf("PipeWire: the stream stopped: %s; opening again", e.Error)
		s.askReset()
		return
	}
	if e.Cycles != p.cycles {
		p.cycles, p.cycledAt, p.stalled = e.Cycles, time.Now(), false
	} else if !p.stalled && !p.paused.Load() && time.Since(p.cycledAt) > 2*time.Second {
		p.stalled = true
		s.logf("PipeWire: the output has asked for no sound in %v; it is %v", time.Since(p.cycledAt).Round(time.Millisecond), e.Out)
	}
}

func (p *pwOutput) rate() int { return p.hz }

// held counts the frames in the ring, and those past it as PipeWire's
// last time report places them, less the frames played since its cycle
// began: so the frame heard moves on through a cycle, not a cycle at a
// time.
func (p *pwOutput) held() int64 { return p.dev.Held() }

func (p *pwOutput) setAhead(d time.Duration) { p.ahead.Store(int64(d)) }

func (p *pwOutput) latency() time.Duration {
	return audio.DurationAt(p.target()+p.device(), p.hz)
}

func (p *pwOutput) fill(st *State) {
	q, d := int(p.quantum.Load()), int(p.delay.Load())
	if p.byName {
		st.Driver = driver.PipeWire
	}
	if r := int(p.deviceRate.Load()); r > 0 && r != p.hz {
		st.DeviceRate = r
	}
	info := &driver.Info{Driver: driver.PipeWire, Rate: p.hz, Buffer: q, MinBuffer: q, MaxBuffer: q,
		PreferredBuffer: q, Latency: q + d, Bits: 32, Float: true,
		Outputs: len(pwChannels), OutputNames: slices.Clone(pwChannels), Played: []int{0, 1}}
	if p.dev.Info().Recording {
		info.Inputs, info.Recorded, info.InputNames, info.InputLatency = len(pwChannels), len(pwChannels), slices.Clone(pwChannels), q
	}
	st.Info = info
}

func (p *pwOutput) err() error { return p.dev.Err() }

func (p *pwOutput) suspend() error {
	p.paused.Store(true)
	p.dev.Pause(true)
	return nil
}

func (p *pwOutput) resume() error {
	p.settle.restart()
	p.dev.Pause(false)
	p.paused.Store(false)
	return nil
}

// close stops the pump and lets the client go, once: later calls do
// nothing. A client that fails to close is gone all the same, so the
// failure is logged, not returned: the speaker would keep the output.
func (p *pwOutput) close() error {
	p.closing.Do(func() {
		if tap := p.tap.Load(); tap != nil {
			duplex.Withdraw(tap)
		}
		close(p.quit)
		<-p.done
		if err := p.dev.Close(); err != nil {
			p.s.logf("PipeWire: %v", err)
		}
	})
	return nil
}
