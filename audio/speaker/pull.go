package speaker

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/asio"
	"github.com/marrasen/gunim/audio/internal/duplex"
	"github.com/marrasen/gunim/audio/rt"
)

// A pullDevice is a device that asks for its sound, as an ASIO driver
// does: it calls Fill each time it takes a buffer.
type pullDevice interface {
	Info() asio.Info
	ControlPanel() error
	Close() error
}

// openPull opens an ASIO driver; a test puts a device of its own here.
var openPull = func(c asio.Config) (pullDevice, error) {
	d, err := asio.Open(c)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// ringOutput plays through a device that asks for its sound. The device
// asks on a thread of its own and must have its answer at once, so a
// goroutine reads the mixer ahead into a ring, and the device takes
// from the ring; or, where Options.Direct says, the device's thread
// mixes each buffer itself as it takes it, and there is no ring.
type ringOutput struct {
	s    *Speaker
	dev  pullDevice
	info asio.Info
	q    atomic.Pointer[ring]
	// base is the mixer frame the output's first frame is: the feed, or
	// the device's thread where it mixes, alone reads the mixer, so
	// frame i the device takes is the mixer's base+i.
	base atomic.Int64
	// tap hands the driver's inputs to package audio/capture, once the
	// output has started.
	tap atomic.Pointer[duplex.Tap]
	// records says the driver was opened with its inputs.
	records bool
	// chans is how many channels a frame of the ring holds: the
	// mixer's two, then those Options.Aux fills.
	chans int
	// target is how many frames the ring is kept at.
	target atomic.Int64
	// primed says the ring has reached its target once, so a buffer the
	// device finds short is the sound running dry.
	primed atomic.Bool
	paused atomic.Bool
	// taken is when the device last took a buffer, and silent says the
	// log has told it stopped.
	taken  atomic.Int64
	silent bool
	wake   chan struct{}
	quit   chan struct{}
	done   chan struct{}
	// direct mixes on the device's thread, as Options.Direct says: live
	// says it may, from start until close, and mixing that it mixes now,
	// so close can wait the buffer out; drawn counts the frames it mixed,
	// as the ring's reads count them; period is how long a buffer plays,
	// in nanoseconds; and main and aux are its room for the mixer's pair
	// and Options.Aux's channels, where it fills more.
	direct       bool
	live, mixing atomic.Bool
	drawn        atomic.Int64
	period       int64
	main, aux    []float32
}

func openRing(s *Speaker, o Options) (*ringOutput, error) {
	r := &ringOutput{s: s, chans: 2, wake: make(chan struct{}, 1), quit: make(chan struct{}), done: make(chan struct{}),
		direct: o.Direct}
	c := asio.Config{Name: o.Driver, Rate: o.rate(), Buffer: o.Buffer, Outputs: o.Outputs, Reset: s.askReset, Log: s.logf}
	if len(o.Outputs) > 2 {
		r.chans = len(o.Outputs)
		c.FillN = r.takeN
	} else {
		c.Fill = r.take
	}
	if o.Record {
		c.Record, c.Recording = r.record, r.recording
		r.records = true
	}
	dev, err := openPull(c)
	if err != nil {
		return nil, err
	}
	r.dev, r.info = dev, dev.Info()
	s.logf("%s takes %d frames at a time (asked %d; it allows %d to %d, prefers %d), and reports %d frames of output latency and %d of input",
		r.info.Driver, r.info.Buffer, o.Buffer, r.info.MinBuffer, r.info.MaxBuffer, r.info.PreferredBuffer, r.info.Latency, r.info.InputLatency)
	if r.direct {
		r.period = int64(r.info.Buffer) * int64(time.Second) / int64(max(r.info.Rate, 1))
		r.main = make([]float32, 2*r.info.Buffer)
		r.aux = make([]float32, (r.chans-2)*r.info.Buffer)
		return r, nil
	}
	r.q.Store(newRing(int(audio.FramesAt(MaxLatency, r.info.Rate))+2*r.info.Buffer+4096, r.chans))
	return r, nil
}

// start starts reading the mixer, working ahead as far as ahead.
func (r *ringOutput) start(ahead time.Duration) {
	r.taken.Store(time.Now().UnixNano())
	r.setAhead(ahead)
	r.base.Store(r.s.m.Mixed())
	if r.records && r.info.Recorded > 0 {
		tap := duplex.NewTap(r.info.Driver, r.info.Rate, r.info.Buffer, r.info.InputLatency, r.info.InputNames)
		r.tap.Store(tap)
		duplex.Publish(tap)
	}
	if r.direct {
		// The device's thread reads the mixer from here on.
		r.primed.Store(true)
		r.live.Store(true)
		close(r.done)
		return
	}
	go r.feed()
}

// take fills frames from the ring, as the device asks, and silence
// where the ring runs short.
func (r *ringOutput) take(frames []float32) {
	r.taken.Store(time.Now().UnixNano())
	if r.direct {
		r.mix(frames)
		return
	}
	n := 0
	q := r.q.Load()
	if q != nil && !r.paused.Load() {
		n = q.pop(frames)
	}
	clear(frames[r.chans*n:])
	if q != nil && n < len(frames)/r.chans && r.primed.Load() && !r.paused.Load() {
		r.s.ranDry()
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// mix fills frames, of all the channels the device plays, from the
// mixer, on the device's thread, as Options.Direct has it: silence
// before the output starts, after it closes, and while it is
// suspended. A buffer that takes longer to mix than it plays counts as
// a dropout, as the driver plays on without it; so does a buffer
// larger than the driver said it takes, played silent, as the driver
// is opened again to say anew.
func (r *ringOutput) mix(frames []float32) {
	// Marked before live is read, as close clears live before it reads
	// the mark: one of the two sees the other.
	r.mixing.Store(true)
	defer r.mixing.Store(false)
	n := len(frames) / r.chans
	if !r.live.Load() || r.paused.Load() {
		clear(frames)
		return
	}
	if n > r.info.Buffer {
		clear(frames)
		r.s.dropouts.Add(1)
		r.s.askReset()
		return
	}
	t0 := nanotime()
	if r.chans == 2 {
		r.s.pull(frames)
	} else {
		m, a := r.main[:2*n], r.aux[:(r.chans-2)*n]
		r.s.pull(m)
		r.s.pullAux(a, r.chans-2, r.base.Load()+r.drawn.Load())
		interleave(frames, m, a, r.chans)
	}
	r.drawn.Add(int64(n))
	if nanotime()-t0 > r.period {
		// Not a dry read: the ring that grows on those is not there.
		r.s.dropouts.Add(1)
	}
}

// takeN fills frames of all the channels the device plays, as take
// does.
func (r *ringOutput) takeN(frames []float32, channels int) {
	if channels != r.chans {
		clear(frames)
		return
	}
	r.take(frames)
}

// record hands the driver's inputs on, as the device takes a buffer,
// with the mixer frame at the speakers as they came in: the first frame
// the device takes now, the ring's next or the next mixed, is heard
// Latency frames from now, and the inputs reached the driver
// InputLatency frames ago.
func (r *ringOutput) record(in []float32, channels int) {
	tap := r.tap.Load()
	if tap == nil {
		return
	}
	// The frame the device takes next: the next the device's thread
	// mixes, or the ring's next.
	var next int64
	if r.direct {
		next = r.drawn.Load()
	} else if q := r.q.Load(); q != nil {
		next = q.r.Load()
	} else {
		return
	}
	heard := r.base.Load() + next - int64(r.info.Latency+r.info.InputLatency)
	tap.Deliver(in, channels, heard)
}

// recording says a stream hears the driver's inputs.
func (r *ringOutput) recording() bool {
	tap := r.tap.Load()
	return tap != nil && tap.Attached()
}

// feed keeps the ring filled to its target, reading the mixer a chunk
// at a time, until the output closes. The device waits on it, so it
// runs on a thread of its own at the system's audio priority, as
// package audio/rt gives it.
func (r *ringOutput) feed() {
	defer close(r.done)
	leave := rt.Enter()
	defer leave()
	t := time.NewTicker(2 * time.Millisecond)
	defer t.Stop()
	most := max(1, int(audio.FramesAt(chunk, r.info.Rate)))
	buf := make([]float32, 2*most)
	q := r.q.Load()
	// Past the mixer's pair, aux holds what Options.Aux fills, and wide
	// the two together, a frame at a time, as the ring takes them.
	var aux, wide []float32
	if r.chans > 2 {
		aux = make([]float32, (r.chans-2)*most)
		wide = make([]float32, r.chans*most)
	}
	for {
		tick := t.C
		if r.paused.Load() {
			// Suspended, it waits for the resume to wake it.
			tick = nil
		}
		select {
		case <-r.quit:
			return
		case <-r.wake:
		case <-tick:
		}
		if r.paused.Load() {
			continue
		}
		for {
			want := r.target.Load() - q.len()
			if want <= 0 {
				r.primed.Store(true)
				break
			}
			n := min(want, int64(len(buf)/2), q.room())
			if n <= 0 {
				break
			}
			// The mixer frame of the first: the feed alone reads it.
			at := r.base.Load() + q.w.Load()
			frames := buf[:2*n]
			r.s.pull(frames)
			if r.chans == 2 {
				q.push(frames)
				continue
			}
			a := aux[:int64(r.chans-2)*n]
			r.s.pullAux(a, r.chans-2, at)
			w := wide[:int64(r.chans)*n]
			interleave(w, frames, a, r.chans)
			q.push(w)
		}
	}
}

// interleave writes frames of chans channels into dst: main's pair
// first, then aux's chans-2.
func interleave(dst, main, aux []float32, chans int) {
	k := chans - 2
	for i := range len(main) / 2 {
		f := dst[i*chans : (i+1)*chans]
		f[0], f[1] = main[2*i], main[2*i+1]
		copy(f[2:], aux[i*k:(i+1)*k])
	}
}

func (r *ringOutput) rate() int { return r.info.Rate }

func (r *ringOutput) held() int64 {
	if q := r.q.Load(); q != nil {
		return q.len() + int64(r.info.Latency)
	}
	return int64(r.info.Latency)
}

// setAhead keeps the ring at what d leaves after the driver's own
// latency, and a buffer of the driver's at least. Where the device's
// thread mixes, nothing reads it.
func (r *ringOutput) setAhead(d time.Duration) {
	r.target.Store(max(audio.FramesAt(d, r.info.Rate)-int64(r.info.Latency), int64(r.info.Buffer)))
}

func (r *ringOutput) latency() time.Duration {
	if r.direct {
		return audio.DurationAt(int64(r.info.Latency), r.info.Rate)
	}
	return audio.DurationAt(r.target.Load()+int64(r.info.Latency), r.info.Rate)
}

func (r *ringOutput) fill(st *State) {
	st.Driver = r.info.Driver
	st.Aux = r.chans - 2
	info := r.info
	st.ASIO = &info
}

func (r *ringOutput) err() error { return nil }

func (r *ringOutput) suspend() error {
	r.paused.Store(true)
	return nil
}

func (r *ringOutput) resume() error {
	r.paused.Store(false)
	select {
	case r.wake <- struct{}{}:
	default:
	}
	return nil
}

func (r *ringOutput) close() error {
	if tap := r.tap.Load(); tap != nil {
		duplex.Withdraw(tap)
	}
	close(r.quit)
	<-r.done
	if r.direct {
		// The device's thread mixes no more once the buffer it mixes now,
		// if any, is done: the next output reads the mixer alone. One that
		// never finishes is let go, as a driver that stopped answering.
		r.live.Store(false)
		deadline := time.Now().Add(mixWait)
		for r.mixing.Load() {
			if time.Now().After(deadline) {
				return errors.Join(asio.ErrNotAnswering, r.dev.Close())
			}
			time.Sleep(100 * time.Microsecond)
		}
	}
	return r.dev.Close()
}

// mixWait is how long close waits for the device's thread to finish
// the buffer it mixes.
const mixWait = 3 * time.Second

// stoppedTaking says the device has taken no buffer for a while, as it
// would while it plays, the first time it is asked after it stopped.
func (r *ringOutput) stoppedTaking(after time.Duration) (time.Duration, bool) {
	since := time.Since(time.Unix(0, r.taken.Load()))
	if since < after || !r.primed.Load() || r.paused.Load() {
		r.silent = false
		return since, false
	}
	if r.silent {
		return since, false
	}
	r.silent = true
	return since, true
}
