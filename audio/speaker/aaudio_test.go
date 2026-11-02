package speaker

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/driver"
	"github.com/marrasen/gunim/audio/internal/aaudio"
	"github.com/marrasen/gunim/audio/internal/duplex"
)

// fakeAA is AAudio as a test plays it: each stream it opens runs a
// device of its own on a goroutine, which takes, or records, a burst
// at a time, as AAudio's thread does. Its frames move on the clock
// without steps, as a device's do: frame c(t) = (t - start) × rate,
// and its timestamps tell it. An output's device plays its frame
// c(t) - delay at t; an input's has frame c(t) + ahead at its
// microphone, which the callback hands on later.
type fakeAA struct {
	rate, burst  int
	delay, ahead int64
	// failIn fails as many input streams' opens; failOut as many
	// outputs'; failPause fails each pause, as of a stream whose device
	// went away.
	failIn, failOut atomic.Int64
	failPause       atomic.Bool
	// more adds to every output's delay from when it is set, as
	// earbuds connecting to a device already playing.
	more      atomic.Int64
	mu        sync.Mutex
	outs, ins []*fakeAAStream
	// played holds each frame played, the first sample of its pair, as
	// devices play them, one after another.
	played []float32
}

type fakeAAStream struct {
	f     *fakeAA
	c     aaConfig
	sh    *aaudio.Shared
	start atomic.Int64
	// pausedAt is when it paused.
	pausedAt int64
	started  atomic.Bool
	paused   atomic.Bool
	// frozen stops the device taking buffers, as one gone untold;
	// anew moves its timestamps back, as a stream that counts anew.
	frozen atomic.Bool
	anew   atomic.Int64
	closed chan struct{}
	done   chan struct{}
	pauses atomic.Int64
}

func useFakeAA(t *testing.T, f *fakeAA) {
	t.Helper()
	was, clock := openAAStream, aaClock
	openAAStream = f.open
	aaClock = func() int64 { return time.Now().UnixNano() }
	t.Cleanup(func() { openAAStream, aaClock = was, clock })
}

func (f *fakeAA) open(c aaConfig) (aaStream, error) {
	fails := &f.failOut
	if c.input {
		fails = &f.failIn
	}
	if fails.Add(-1) >= 0 {
		return nil, errors.New("fake: refused")
	}
	fails.Store(0)
	s := &fakeAAStream{f: f, c: c, sh: aaudio.NewShared(c.data, c.blocks), closed: make(chan struct{}),
		done: make(chan struct{})}
	f.mu.Lock()
	defer f.mu.Unlock()
	if c.input {
		f.ins = append(f.ins, s)
	} else {
		f.outs = append(f.outs, s)
	}
	return s, nil
}

// carried returns the frames the device has moved at t: c(t).
func (s *fakeAAStream) carried(t int64) int64 {
	return (t - s.start.Load()) * int64(s.f.rate) / 1e9
}

func (s *fakeAAStream) Start() error {
	if s.paused.Swap(false) {
		// Paused, the device stood still.
		s.start.Add(aaClock() - s.pausedAt)
	}
	if s.started.Swap(true) {
		return nil
	}
	s.start.Store(aaClock())
	go s.run()
	return nil
}

// run calls back a burst at a time, until the stream closes.
func (s *fakeAAStream) run() {
	defer close(s.done)
	period := time.Duration(int64(s.f.burst) * 1e9 / int64(s.f.rate))
	buf := make([]float32, s.f.burst*s.c.chans)
	for {
		select {
		case <-s.closed:
			return
		case <-time.After(period):
		}
		if s.paused.Load() || s.frozen.Load() {
			continue
		}
		n := s.f.burst
		if s.c.input {
			pos := s.sh.Frames.Load()
			if s.c.data.Room() >= int64(n) && s.c.blocks.Room() >= 1 {
				for i := range buf {
					buf[i] = float32(pos+int64(i)) * 1e-6
				}
				s.c.data.Push(buf)
				s.c.blocks.Push([]int64{pos, int64(n)})
			} else {
				s.sh.Dropped.Add(int64(n))
			}
		} else {
			got := s.c.data.Pop(buf)
			clear(buf[got*2:])
			if got < n {
				s.sh.Silence.Add(int64(n - got))
				s.sh.Underruns.Add(1)
			}
			s.f.mu.Lock()
			for i := range n {
				s.f.played = append(s.f.played, buf[2*i])
			}
			s.f.mu.Unlock()
		}
		s.sh.Most.Store(int64(n))
		s.sh.Frames.Add(int64(n))
	}
}

func (s *fakeAAStream) Pause() error {
	s.pauses.Add(1)
	if s.f.failPause.Load() {
		return errors.New("fake: disconnected")
	}
	s.pausedAt = aaClock()
	s.paused.Store(true)
	return nil
}

func (s *fakeAAStream) Close() error {
	close(s.closed)
	if s.started.Load() {
		<-s.done
	}
	return nil
}

func (s *fakeAAStream) Timestamp() (pos, at, now int64, ok bool) {
	if !s.started.Load() {
		return 0, 0, 0, false
	}
	now = aaClock()
	c := s.carried(now) - s.anew.Load()
	if s.c.input {
		return c + s.f.ahead, now, now, true
	}
	d := s.f.delay + s.f.more.Load()
	return c - d, now, now, c > d
}

func (s *fakeAAStream) Shared() *aaudio.Shared { return s.sh }

func (s *fakeAAStream) Info() aaudio.Info {
	return aaudio.Info{Rate: s.f.rate, Channels: s.c.chans, Burst: s.f.burst, Buffer: 2 * s.f.burst,
		Capacity: 8 * s.f.burst, LowLatency: true, Exclusive: s.c.exclusive}
}

func (s *fakeAAStream) XRuns() int64 { return 0 }

// outs and ins return the streams opened.
func (f *fakeAA) streams() (outs, ins []*fakeAAStream) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*fakeAAStream(nil), f.outs...), append([]*fakeAAStream(nil), f.ins...)
}

// playedFrames returns the ramp's frames as the devices played them.
func (f *fakeAA) playedFrames() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int64, len(f.played))
	for i, v := range f.played {
		out[i] = int64(math.Round(float64(v) * 1e6))
	}
	return out
}

// openAA opens the speaker through AAudio, in a bubble, and closes it
// as the test ends, its voices stopped, and the mixer's teller let go.
func openAA(t *testing.T, m *audio.Mixer, o Options, voices ...*audio.Voice) *Speaker {
	t.Helper()
	o.Driver = driver.AAudio
	s, err := Open(m, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
		for _, v := range voices {
			v.Stop(0)
		}
		// A few milliseconds, as the voices fade out.
		m.Mix(make([]float32, 2*4800))
		time.Sleep(2 * time.Second)
	})
	if _, ok := s.out.Load().output.(*aaOutput); !ok {
		t.Fatalf("the speaker plays through %T", s.out.Load().output)
	}
	return s
}

// TestAAudioPlaysTheMixerAsHeard plays a ramp through AAudio for a
// while: the device plays every frame in order, never short once
// started, and the frame the speaker says is heard is the one the
// device plays then, its delay counted, every 10 ms.
func TestAAudioPlaysTheMixerAsHeard(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 192, delay: 2400}
		useFakeAA(t, f)
		m := audio.NewMixer()
		v := m.Play(&ramp{}, audio.Options{})
		s := openAA(t, m, Options{Latency: 20 * time.Millisecond}, v)
		st := s.State()
		if st.Driver != driver.AAudio || st.Rate != 48000 || st.Info == nil || st.Info.Buffer != 192 {
			t.Fatalf("the state says %+v", st)
		}
		outs, _ := f.streams()
		if len(outs) != 1 || !outs[0].c.exclusive {
			t.Fatalf("opened %d outputs, the first exclusive %v; want one, exclusive", len(outs), outs[0].c.exclusive)
		}
		for i := range 300 {
			time.Sleep(10 * time.Millisecond)
			synctest.Wait()
			c := outs[0].carried(aaClock())
			if i < 10 || c <= f.delay {
				continue
			}
			// The device plays frame c - delay now; the ring holds the
			// first frames pushed from frame 0 on, so it is the ramp's
			// frame too.
			want := c - f.delay
			if got := m.Heard(); abs64(got-want) > 2 {
				t.Fatalf("at %v the speaker hears frame %d; the device plays %d", time.Duration(i)*10*time.Millisecond,
					got, want)
			}
		}
		if d := s.State().Dropouts; d != 0 {
			t.Errorf("the sound ran dry %d times", d)
		}
		played := f.playedFrames()
		for i, v := range played {
			if v != int64(i) {
				t.Fatalf("the device played frame %d as its %dth", v, i)
			}
		}
		lat := s.Latency()
		if want := audio.DurationAt(f.delay, 48000); lat < want || lat > want+20*time.Millisecond {
			t.Errorf("the latency is %v; the device alone delays %v", lat, want)
		}
	})
}

// TestAAudioOpensAgainWhereTheDeviceGoes tells the output its device
// went away, then stops another's callbacks untold, then has a third
// count its frames anew, as a stream the sound server rebuilt: each
// time it opens a stream anew, on its own, with no Changed, and plays
// on from the next frame mixed, in order.
func TestAAudioOpensAgainWhereTheDeviceGoes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 480, delay: 960}
		useFakeAA(t, f)
		m := audio.NewMixer()
		v := m.Play(&ramp{}, audio.Options{})
		s := openAA(t, m, Options{}, v)
		time.Sleep(500 * time.Millisecond)
		outs, _ := f.streams()
		outs[0].sh.Err.Store(-899) // AAUDIO_ERROR_DISCONNECTED
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		outs, _ = f.streams()
		if len(outs) != 2 {
			t.Fatalf("after a disconnect %d streams opened, want 2", len(outs))
		}
		time.Sleep(500 * time.Millisecond)
		outs[1].frozen.Store(true)
		time.Sleep(aaStalled + 200*time.Millisecond)
		synctest.Wait()
		outs, _ = f.streams()
		if len(outs) != 3 {
			t.Fatalf("after a stream stopped taking buffers %d streams opened, want 3", len(outs))
		}
		time.Sleep(500 * time.Millisecond)
		// Its device a moment back, as far as a Bluetooth headset's
		// delay, and no further than MaxLatency.
		outs[2].anew.Store(audio.FramesAt(300*time.Millisecond, 48000))
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		outs, _ = f.streams()
		if len(outs) != 4 {
			t.Fatalf("after a stream counted anew %d streams opened, want 4", len(outs))
		}
		if l := s.Latency(); l > 100*time.Millisecond {
			t.Errorf("after a stream counted anew the latency is %v", l)
		}
		time.Sleep(500 * time.Millisecond)
		select {
		case <-s.Changed():
			t.Error("the speaker told of a change: the output opened its stream again itself")
		default:
		}
		played := f.playedFrames()
		for i := 1; i < len(played); i++ {
			if played[i] <= played[i-1] && played[i] != 0 {
				t.Fatalf("the device played frame %d after %d", played[i], played[i-1])
			}
		}
		// The last stream plays on: what the speaker hears goes on as
		// the device plays.
		c := outs[3].carried(aaClock())
		h0 := m.Heard()
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if got := m.Heard() - h0; abs64(got-(outs[3].carried(aaClock())-c)) > 2 {
			t.Errorf("in 100 ms the speaker heard %d frames, the device played %d", got, outs[3].carried(aaClock())-c)
		}
	})
}

// TestAAudioStampsWhatItRecords records through AAudio as it plays:
// each block reaches the tap stamped with the mixer frame the device
// played as the block's first frame was at the microphone, from the
// first block on, and the blocks follow one another without a gap.
func TestAAudioStampsWhatItRecords(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 240, delay: 4800, ahead: 96}
		useFakeAA(t, f)
		m := audio.NewMixer()
		v := m.Play(&ramp{}, audio.Options{})
		s := openAA(t, m, Options{Record: true}, v)
		tap := duplex.Current()
		if tap == nil || tap.Driver != driver.AAudio || len(tap.Names) != 1 {
			t.Fatalf("the tap is %+v", tap)
		}
		type block struct{ pos, heard int64 }
		var (
			mu     sync.Mutex
			blocks []block
		)
		detach := tap.Attach(func(in []float32, channels int, heard int64) {
			mu.Lock()
			defer mu.Unlock()
			blocks = append(blocks, block{int64(math.Round(float64(in[0]) * 1e6)), heard})
		})
		time.Sleep(2 * time.Second)
		synctest.Wait()
		detach()
		mu.Lock()
		defer mu.Unlock()
		if len(blocks) < 300 {
			t.Fatalf("the tap heard %d blocks in 2 s, want 400", len(blocks))
		}
		for i, b := range blocks {
			if i > 0 && b.pos != blocks[i-1].pos+int64(f.burst) {
				t.Fatalf("block %d starts at frame %d, after one at %d", i, b.pos, blocks[i-1].pos)
			}
			// Frame pos was at the microphone as the input's device moved
			// pos - ahead frames; the output's, the same, played that less
			// its delay.
			if want := b.pos - f.ahead - f.delay; abs64(b.heard-want) > 2 {
				t.Fatalf("block %d, from frame %d, is stamped %d, want %d", i, b.pos, b.heard, want)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if duplex.Current() != nil {
			t.Error("the tap stays after the speaker closed")
		}
	})
}

// TestAAudioOpensTheMicrophoneOnceAllowed opens the speaker before the
// application may record: it plays at once, says why it does not
// record, and records once the microphone opens, as it keeps trying.
func TestAAudioOpensTheMicrophoneOnceAllowed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 240, delay: 960}
		f.failIn.Store(10)
		useFakeAA(t, f)
		m := audio.NewMixer()
		s := openAA(t, m, Options{Record: true})
		if s.State().Problem == nil {
			t.Error("the state tells no problem while the microphone is shut")
		}
		var heard atomic.Int64
		detach := duplex.Current().Attach(func(in []float32, _ int, _ int64) { heard.Add(int64(len(in))) })
		defer detach()
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if heard.Load() == 0 {
			t.Error("the microphone recorded nothing once it opened")
		}
		if p := s.State().Problem; p != nil {
			t.Errorf("once recording, the state tells %v", p)
		}
	})
}

// TestAAudioLetsTheMicrophoneGoWhileSuspended suspends and resumes:
// the output pauses, the input closes, and both run again after.
func TestAAudioLetsTheMicrophoneGoWhileSuspended(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 240, delay: 960}
		useFakeAA(t, f)
		m := audio.NewMixer()
		s := openAA(t, m, Options{Record: true})
		time.Sleep(200 * time.Millisecond)
		if err := s.Suspend(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		outs, ins := f.streams()
		if outs[0].pauses.Load() != 1 {
			t.Errorf("the output paused %d times, want 1", outs[0].pauses.Load())
		}
		select {
		case <-ins[0].closed:
		default:
			t.Error("the microphone stays open while suspended")
		}
		h := m.Heard()
		time.Sleep(time.Second)
		synctest.Wait()
		if got := m.Heard(); got != h {
			t.Errorf("while suspended the speaker heard %d frames more", got-h)
		}
		if err := s.Resume(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		outs, ins = f.streams()
		if len(outs) != 1 || len(ins) != 2 {
			t.Errorf("after resuming %d outputs and %d inputs opened, want 1 and 2", len(outs), len(ins))
		}
		if got := m.Heard(); got <= h {
			t.Error("after resuming the speaker hears nothing more")
		}
	})
}

// TestAAudioOpensAgainAStreamThatWouldNotPause suspends while the
// output's device has gone, so it will not pause, as the speaker
// records and plays on: it closes, and the resume opens another, which
// plays.
func TestAAudioOpensAgainAStreamThatWouldNotPause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 240, delay: 960}
		useFakeAA(t, f)
		m := audio.NewMixer()
		s := openAA(t, m, Options{Record: true})
		time.Sleep(200 * time.Millisecond)
		f.failPause.Store(true)
		if err := s.Suspend(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		outs, _ := f.streams()
		select {
		case <-outs[0].closed:
		default:
			t.Fatal("the output that would not pause stays open")
		}
		if err := s.Resume(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		outs, _ = f.streams()
		if len(outs) != 2 {
			t.Fatalf("after resuming %d outputs opened, want 2", len(outs))
		}
		h := m.Heard()
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		if got := m.Heard() - h; abs64(got-24000) > 2*int64(f.burst) {
			t.Errorf("in 500 ms after resuming the speaker heard %d frames, want 24000", got)
		}
	})
}

// TestAAudioKeepsALongDelay plays and records through devices as slow
// as Bluetooth earbuds: one stream each serves throughout, and the
// latency the speaker tells counts the whole delay.
func TestAAudioKeepsALongDelay(t *testing.T) {
	for _, delay := range []time.Duration{200 * time.Millisecond, 300 * time.Millisecond} {
		t.Run(delay.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				d := audio.FramesAt(delay, 48000)
				f := &fakeAA{rate: 48000, burst: 480, delay: d, ahead: d}
				useFakeAA(t, f)
				m := audio.NewMixer()
				s := openAA(t, m, Options{Record: true})
				time.Sleep(5 * time.Second)
				synctest.Wait()
				outs, ins := f.streams()
				if len(outs) != 1 || len(ins) != 1 {
					t.Errorf("in 5 s %d outputs and %d inputs opened, want 1 each", len(outs), len(ins))
				}
				if l := s.Latency(); l < delay || l > delay+30*time.Millisecond {
					t.Errorf("the latency is %v, the device alone delays %v", l, delay)
				}
				if in := s.State().Info.InputLatency; abs64(int64(in)-d) > int64(f.burst) {
					t.Errorf("the input's latency is %d frames, the device's %d", in, d)
				}
			})
		})
	}
}

// TestAAudioFollowsADelayThatGrows has the device's delay grow from
// 100 ms to 300 ms as it plays, as where earbuds connect: the output
// opens its stream again once at most, and then keeps it, telling the
// new delay.
func TestAAudioFollowsADelayThatGrows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 480, delay: audio.FramesAt(100*time.Millisecond, 48000)}
		useFakeAA(t, f)
		m := audio.NewMixer()
		s := openAA(t, m, Options{})
		time.Sleep(2 * time.Second)
		f.more.Store(audio.FramesAt(200*time.Millisecond, 48000))
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if outs, _ := f.streams(); len(outs) > 2 {
			t.Errorf("after the delay grew %d outputs opened, want 2 at most", len(outs))
		}
		if l := s.Latency(); l < 300*time.Millisecond || l > 330*time.Millisecond {
			t.Errorf("the latency is %v, the device alone delays 300ms", l)
		}
	})
}

// TestAAudioFallsBackWhereItWillNotOpen opens AAudio where it refuses:
// openAAudio says why, and lets its ring go.
func TestAAudioFallsBackWhereItWillNotOpen(t *testing.T) {
	f := &fakeAA{rate: 48000, burst: 240}
	f.failOut.Store(2)
	useFakeAA(t, f)
	s := &Speaker{m: audio.NewMixer(), reset: make(chan struct{}, 1)}
	if _, err := openAAudio(s, Options{}, true); err == nil {
		t.Fatal("openAAudio opened a stream the system refused")
	}
	openAAStream = nil
	if _, err := openAAudio(s, Options{}, true); !errors.Is(err, aaudio.ErrUnsupported) {
		t.Errorf("without AAudio openAAudio returned %v", err)
	}
}
