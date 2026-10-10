package speaker

import (
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/asio"
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
// from the ring.
type ringOutput struct {
	s    *Speaker
	dev  pullDevice
	info asio.Info
	q    atomic.Pointer[ring]
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
}

func openRing(s *Speaker, o Options) (*ringOutput, error) {
	r := &ringOutput{s: s, wake: make(chan struct{}, 1), quit: make(chan struct{}), done: make(chan struct{})}
	dev, err := openPull(asio.Config{Name: o.Driver, Rate: o.rate(), Fill: r.take, Reset: s.askReset, Log: s.logf})
	if err != nil {
		return nil, err
	}
	r.dev, r.info = dev, dev.Info()
	r.q.Store(newRing(int(audio.FramesAt(MaxLatency, r.info.Rate)) + 2*r.info.Buffer + 4096))
	return r, nil
}

// start starts reading the mixer, working ahead as far as ahead.
func (r *ringOutput) start(ahead time.Duration) {
	r.taken.Store(time.Now().UnixNano())
	r.setAhead(ahead)
	go r.feed()
}

// take fills frames from the ring, as the device asks, and silence
// where the ring runs short.
func (r *ringOutput) take(frames []float32) {
	r.taken.Store(time.Now().UnixNano())
	n := 0
	q := r.q.Load()
	if q != nil && !r.paused.Load() {
		n = q.pop(frames)
	}
	clear(frames[2*n:])
	if q != nil && n < len(frames)/2 && r.primed.Load() && !r.paused.Load() {
		r.s.ranDry()
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// feed keeps the ring filled to its target, reading the mixer a chunk
// at a time, until the output closes.
func (r *ringOutput) feed() {
	defer close(r.done)
	t := time.NewTicker(2 * time.Millisecond)
	defer t.Stop()
	buf := make([]float32, 2*max(1, int(audio.FramesAt(chunk, r.info.Rate))))
	q := r.q.Load()
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
			frames := buf[:2*n]
			r.s.pull(frames)
			q.push(frames)
		}
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
// latency, and a buffer of the driver's at least.
func (r *ringOutput) setAhead(d time.Duration) {
	r.target.Store(max(audio.FramesAt(d, r.info.Rate)-int64(r.info.Latency), int64(r.info.Buffer)))
}

func (r *ringOutput) latency() time.Duration {
	return audio.DurationAt(r.target.Load()+int64(r.info.Latency), r.info.Rate)
}

func (r *ringOutput) fill(st *State) {
	st.Driver = r.info.Driver
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
	close(r.quit)
	<-r.done
	return r.dev.Close()
}

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
