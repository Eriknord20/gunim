package speaker

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/asio"
	"github.com/marrasen/gunim/audio/internal/duplex"
	"github.com/marrasen/gunim/audio/internal/jack"
	"github.com/marrasen/gunim/audio/rt"
)

// A jackDevice is a client of a JACK server, as package
// audio/internal/jack opens it: the server takes each buffer from a
// ring on its own thread, and the speaker fills the ring.
type jackDevice interface {
	Info() jack.Info
	Buffer() int
	Latency() (out, in int)
	Write(frames []float32) int
	Queued() int64
	Room() int64
	Read(dst []float32) (n int, at int64, ok bool)
	SinceCycle() int
	Poll() jack.Events
	Pause(paused bool)
	Err() error
	Close() error
}

// openJack opens a JACK client; a test puts a device of its own here.
var openJack = func(c jack.Config) (jackDevice, error) {
	d, err := jack.Open(c)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// errJackPanel is what ControlPanel returns while JACK plays.
var errJackPanel = errors.New("speaker: JACK has no control panel: connect its ports in a patchbay, " +
	"as qpwgraph or QjackCtl, and set its rate and buffer there")

// pumpTick is how often the pump fills the ring and empties the
// inputs. The ring is kept at least a buffer and two ticks ahead.
const pumpTick = time.Millisecond

// jackOutput plays through a JACK server. The server takes each buffer
// from a ring on its own thread, with no Go; a goroutine, the pump,
// reads the mixer ahead into the ring, and hands what the inputs
// recorded to package audio/capture.
type jackOutput struct {
	s   *Speaker
	dev jackDevice
	hz  int
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
	// server finds short is the sound running dry.
	primed, paused atomic.Bool
	tap            atomic.Pointer[duplex.Tap]
	// mu guards info.
	mu   sync.Mutex
	info jack.Info
	quit chan struct{}
	done chan struct{}
	// What the pump alone keeps: the frames it wrote, ever; the server
	// having shut down; and the cycles as the pump last saw them change.
	written  int64
	shut     bool
	cycles   int64
	cycledAt time.Time
	stalled  bool
}

// openJackOutput opens a client of the JACK server, its ports connected
// to the system's outputs Options.Outputs names, and its inputs where
// Options.Record says.
func openJackOutput(s *Speaker, o Options) (*jackOutput, error) {
	dev, err := openJack(jack.Config{Name: o.Name, Outputs: o.Outputs, Record: o.Record, Hold: MaxLatency, Log: s.logf})
	if err != nil {
		return nil, err
	}
	info := dev.Info()
	j := &jackOutput{s: s, dev: dev, hz: info.Rate, chans: len(info.Played), info: info,
		quit: make(chan struct{}), done: make(chan struct{})}
	s.logf("JACK: %s through %s at %d Hz, %d frames a buffer, plays on %v of %v, records %d of %v; "+
		"%d frames of output latency and %d of input",
		info.Client, info.Library, info.Rate, info.Buffer, info.Played, info.Outputs, info.Recorded, info.Inputs,
		info.Latency, info.InputLatency)
	return j, nil
}

// start starts the pump, working ahead as far as ahead.
func (j *jackOutput) start(ahead time.Duration) {
	j.setAhead(ahead)
	j.base.Store(j.s.m.Mixed())
	j.cycledAt = time.Now()
	if j.info.Recorded > 0 {
		_, in := j.dev.Latency()
		tap := duplex.NewTap(asio.JACK, j.hz, j.info.Buffer, in, j.info.Inputs[:j.info.Recorded])
		j.tap.Store(tap)
		duplex.Publish(tap)
	}
	go j.pump()
}

// device returns the frames from a buffer the server starts to the
// last of it heard: the buffer, and the ports' latency.
func (j *jackOutput) device() int64 {
	out, _ := j.dev.Latency()
	return int64(j.dev.Buffer() + out)
}

// target returns how many frames the ring is kept at: what Latency
// leaves after the device's own, and a buffer and two of the pump's
// ticks at least.
func (j *jackOutput) target() int64 {
	least := int64(j.dev.Buffer()) + audio.FramesAt(2*pumpTick, j.hz)
	return max(audio.FramesAt(time.Duration(j.ahead.Load()), j.hz)-j.device(), least)
}

// pump keeps the ring filled to its target, hands the inputs on, and
// hears what the server tells, until the output closes. The server waits
// on it, so it runs on a thread of its own at the system's audio
// priority, as package audio/rt gives it.
func (j *jackOutput) pump() {
	defer close(j.done)
	leave := rt.Enter()
	defer leave()
	t := time.NewTicker(pumpTick)
	defer t.Stop()
	most := max(1, int(audio.FramesAt(chunk, j.hz)))
	buf := make([]float32, 2*most)
	// Past the mixer's pair, aux holds what Options.Aux fills, and wide
	// the ring's frames; for one channel, wide holds the pair averaged.
	var aux, wide []float32
	if j.chans > 2 {
		aux = make([]float32, (j.chans-2)*most)
	}
	if j.chans != 2 {
		wide = make([]float32, j.chans*most)
	}
	var in []float32
	if j.info.Recorded > 0 {
		in = make([]float32, j.info.Recorded*8192)
	}
	for {
		select {
		case <-j.quit:
			return
		case <-t.C:
		}
		j.hear()
		if !j.paused.Load() {
			j.feed(buf, aux, wide, most)
		}
		if in != nil {
			j.record(in)
		}
	}
}

// feed reads the mixer into the ring, as far as its target.
func (j *jackOutput) feed(buf, aux, wide []float32, most int) {
	target := j.target()
	for {
		want := target - j.dev.Queued()
		if want <= 0 {
			j.primed.Store(true)
			return
		}
		n := min(want, int64(most), j.dev.Room())
		if n <= 0 {
			return
		}
		frames := buf[:2*n]
		j.s.pull(frames)
		switch j.chans {
		case 2:
			j.dev.Write(frames)
		case 1:
			w := wide[:n]
			for i := range w {
				w[i] = (frames[2*i] + frames[2*i+1]) / 2
			}
			j.dev.Write(w)
		default:
			a := aux[:int64(j.chans-2)*n]
			j.s.pullAux(a, j.chans-2, j.base.Load()+j.written)
			w := wide[:int64(j.chans)*n]
			interleave(w, frames, a, j.chans)
			j.dev.Write(w)
		}
		j.written += n
	}
}

// record hands on what the inputs recorded, with the mixer frame heard
// as it reached them: the output frame the server took at the same
// moment is heard the outputs' latency later, and the input reached the
// system the inputs' latency before.
func (j *jackOutput) record(in []float32) {
	ch := j.info.Recorded
	tap := j.tap.Load()
	for {
		n, at, ok := j.dev.Read(in)
		if !ok {
			return
		}
		if tap == nil || !tap.Attached() {
			continue
		}
		out, inLat := j.dev.Latency()
		tap.Deliver(in[:n*ch], ch, j.base.Load()+at-int64(out+inLat))
	}
}

// hear acts on what the server told since the pump last asked.
func (j *jackOutput) hear() {
	e := j.dev.Poll()
	s := j.s
	if e.Short > 0 && j.primed.Load() && !j.paused.Load() {
		for range e.Short {
			s.ranDry()
		}
	}
	if e.Xruns > 0 {
		// A buffer late anywhere in the graph breaks the sound, but a
		// larger ring would not have saved it.
		s.dropouts.Add(e.Xruns)
		s.logf("JACK: %d xruns", e.Xruns)
	}
	if e.Lost > 0 {
		s.logf("JACK: the inputs found their ring full: %d frames lost", e.Lost)
	}
	if e.Graph {
		info := j.dev.Info()
		j.mu.Lock()
		changed := info.Buffer != j.info.Buffer || info.Latency != j.info.Latency || info.InputLatency != j.info.InputLatency
		j.info = info
		j.mu.Unlock()
		if changed {
			s.logf("JACK: %d frames a buffer, %d frames of output latency and %d of input; working %v ahead",
				info.Buffer, info.Latency, info.InputLatency, j.latency())
		}
	}
	if e.Shutdown && !j.shut {
		j.shut = true
		s.logf("JACK: the server shut down")
		s.askReset()
		return
	}
	if e.Rate > 0 && e.Rate != j.hz && !j.shut {
		j.shut = true
		s.logf("JACK: the server's rate changed from %d to %d Hz; opening again", j.hz, e.Rate)
		s.askReset()
	}
	if e.Cycles != j.cycles {
		j.cycles, j.cycledAt, j.stalled = e.Cycles, time.Now(), false
	} else if !j.stalled && !j.paused.Load() && time.Since(j.cycledAt) > 2*time.Second {
		j.stalled = true
		s.logf("JACK: the server has asked for no sound in %v", time.Since(j.cycledAt).Round(time.Millisecond))
	}
}

func (j *jackOutput) rate() int { return j.hz }

// held counts the frames in the ring, the buffer the server took last
// and the ports' latency, less the frames played since the server
// took it, as its frame time tells: so the frame heard moves on through
// a buffer, not a buffer at a time.
func (j *jackOutput) held() int64 {
	n := j.dev.Buffer()
	since := min(max(j.dev.SinceCycle(), 0), n)
	return j.dev.Queued() + j.device() - int64(since)
}

func (j *jackOutput) setAhead(d time.Duration) { j.ahead.Store(int64(d)) }

func (j *jackOutput) latency() time.Duration {
	return audio.DurationAt(j.target()+j.device(), j.hz)
}

func (j *jackOutput) fill(st *State) {
	j.mu.Lock()
	i := j.info
	j.mu.Unlock()
	st.Driver = asio.JACK
	st.Aux = max(j.chans-2, 0)
	st.ASIO = &asio.Info{Driver: asio.JACK, Rate: i.Rate, Buffer: i.Buffer, MinBuffer: i.Buffer, MaxBuffer: i.Buffer,
		PreferredBuffer: i.Buffer, Latency: i.Buffer + i.Latency, Bits: 32, Float: true,
		Outputs: len(i.Outputs), OutputNames: i.Outputs, Played: i.Played,
		Inputs: len(i.Inputs), Recorded: i.Recorded, InputNames: i.Inputs[:i.Recorded], InputLatency: i.InputLatency}
}

func (j *jackOutput) err() error { return j.dev.Err() }

func (j *jackOutput) suspend() error {
	j.paused.Store(true)
	j.dev.Pause(true)
	return nil
}

func (j *jackOutput) resume() error {
	j.dev.Pause(false)
	j.paused.Store(false)
	return nil
}

func (j *jackOutput) close() error {
	if tap := j.tap.Load(); tap != nil {
		duplex.Withdraw(tap)
	}
	close(j.quit)
	<-j.done
	return j.dev.Close()
}
