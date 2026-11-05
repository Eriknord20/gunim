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
	more atomic.Int64
	// startAfter is how long each stream opened from when it is set
	// takes to start its device after Start, as one at a Bluetooth
	// headset may.
	startAfter atomic.Int64
	// inTakes is how long an input's open takes, as AAudio's take as
	// long as 200 ms; inOpens holds when each began, and whether it
	// asked for the device to itself.
	inTakes time.Duration
	inOpens []fakeOpen
	// opening counts the inputs opening now. dryWhileOpening has every
	// output's callback find its ring short meanwhile, and starve at
	// any time, as a system too busy to run the feed.
	opening         atomic.Int64
	dryWhileOpening bool
	starve          atomic.Bool
	// failStartIn fails every input's Start. dryAfterOpen has every
	// output's callback find its ring short for as long after an input
	// opens, until starveTill, in Unix nanoseconds.
	failStartIn  atomic.Bool
	dryAfterOpen time.Duration
	starveTill   atomic.Int64
	mu           sync.Mutex
	outs, ins    []*fakeAAStream
	// played holds each frame played, the first sample of its pair, as
	// devices play them, one after another.
	played []float32
	// route is what Android tells of the outputs' device, nil for
	// nothing, as where the Android driver is missing.
	route atomic.Pointer[aaudio.Route]
}

type fakeOpen struct {
	at        time.Time
	exclusive bool
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
	// late is how long it takes to start its device after Start.
	late   int64
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
		f.mu.Lock()
		f.inOpens = append(f.inOpens, fakeOpen{time.Now(), c.exclusive})
		f.mu.Unlock()
		f.opening.Add(1)
		time.Sleep(f.inTakes)
		f.opening.Add(-1)
	}
	if fails.Add(-1) >= 0 {
		return nil, errors.New("fake: refused")
	}
	if c.input && f.dryAfterOpen > 0 {
		f.starveTill.Store(time.Now().Add(f.dryAfterOpen).UnixNano())
	}
	fails.Store(0)
	s := &fakeAAStream{f: f, c: c, sh: aaudio.NewShared(c.data, c.blocks), closed: make(chan struct{}),
		done: make(chan struct{}), late: f.startAfter.Load()}
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
	if s.c.input && s.f.failStartIn.Load() {
		return errors.New("fake: would not start")
	}
	if s.paused.Swap(false) {
		// Paused, the device stood still.
		s.start.Add(aaClock() - s.pausedAt)
	}
	if s.started.Swap(true) {
		return nil
	}
	s.start.Store(aaClock() + s.late)
	go s.run()
	return nil
}

// run calls back a burst at a time, until the stream closes.
func (s *fakeAAStream) run() {
	defer close(s.done)
	period := time.Duration(int64(s.f.burst) * 1e9 / int64(s.f.rate))
	buf := make([]float32, s.f.burst*s.c.chans)
	select {
	case <-s.closed:
		return
	case <-time.After(time.Duration(s.late)):
	}
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
			got := 0
			if !s.f.starve.Load() && (!s.f.dryWhileOpening || s.f.opening.Load() == 0) &&
				time.Now().UnixNano() >= s.f.starveTill.Load() {
				got = s.c.data.Pop(buf)
			}
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

func (s *fakeAAStream) Route() aaudio.Route {
	if r := s.f.route.Load(); r != nil {
		return *r
	}
	return aaudio.Route{}
}

func (s *fakeAAStream) Probe() (aaudio.Probe, bool) { return aaudio.Probe{}, false }

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
	t.Run("speaker", func(t *testing.T) { testAAudioStamps(t, 4800, nil) })
	// On a Bluetooth headset whose timestamps, the mixer's, leave out
	// what Android's mixer reports, the stamps count it too.
	t.Run("Bluetooth", func(t *testing.T) {
		testAAudioStamps(t, 600, &aaudio.Route{Known: true, Type: 8, Latency: 220 * time.Millisecond})
	})
}

func testAAudioStamps(t *testing.T, delay int64, route *aaudio.Route) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 240, delay: delay, ahead: 96}
		f.route.Store(route)
		extra := int64(0)
		if route != nil {
			extra = unstamped(f.delay, 2*240, 240, audio.FramesAt(route.Latency, 48000))
		}
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
			if want := b.pos - f.ahead - f.delay - extra; abs64(b.heard-want) > 2 {
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

// TestAAudioPlaysOnAsTheMicrophoneWillNotOpen opens the speaker as on
// a Pixel 8 Pro whose user has yet to allow recording: the output's
// device takes a burst each 2 ms, and each open of the microphone takes
// 200 ms and fails, 15 in a row. The output never runs short and its
// latency stays as it was, the opens are spaced half a second apart and
// then further, to 2 s, and once the microphone opens it records every
// block.
func TestAAudioPlaysOnAsTheMicrophoneWillNotOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 96, delay: 192, inTakes: 200 * time.Millisecond}
		f.failIn.Store(15)
		useFakeAA(t, f)
		m := audio.NewMixer()
		v := m.Play(&ramp{}, audio.Options{})
		s := openAA(t, m, Options{Record: true}, v)
		time.Sleep(300 * time.Millisecond)
		synctest.Wait()
		lat := s.Latency()
		var blocks atomic.Int64
		var last atomic.Int64
		var gaps atomic.Int64
		last.Store(-1)
		detach := duplex.Current().Attach(func(in []float32, _ int, _ int64) {
			pos := int64(math.Round(float64(in[0]) * 1e6))
			if l := last.Swap(pos); l >= 0 && pos != l+int64(len(in)) {
				gaps.Add(1)
			}
			blocks.Add(1)
		})
		defer detach()
		for range 200 {
			time.Sleep(100 * time.Millisecond)
			synctest.Wait()
			if d := s.State().Dropouts; d != 0 {
				t.Fatalf("the output ran dry %d times as the microphone opened", d)
			}
			if l := s.Latency(); l > lat+time.Millisecond {
				t.Fatalf("the latency grew from %v to %v as the microphone opened", lat, l)
			}
		}
		f.mu.Lock()
		opens := append([]fakeOpen(nil), f.inOpens...)
		f.mu.Unlock()
		if len(opens) != 16 {
			t.Fatalf("the microphone was opened %d times, want 16: 15 that failed, and one", len(opens))
		}
		// Each try asks for the device to itself, then shares it: 400 ms
		// for two that fail, then the wait.
		want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second,
			2 * time.Second, 2 * time.Second}
		for i, w := range want {
			a, b := opens[2*i], opens[2*i+2]
			if !a.exclusive || !b.exclusive || opens[2*i+1].exclusive {
				t.Fatalf("try %d asked for the device to itself %v, then %v", i, a.exclusive, opens[2*i+1].exclusive)
			}
			if gap := b.at.Sub(a.at) - 400*time.Millisecond; gap < w || gap > w+10*time.Millisecond {
				t.Errorf("try %d came %v after the one before failed, want %v", i+1, gap, w)
			}
		}
		if blocks.Load() < 100 {
			t.Errorf("the tap heard %d blocks once the microphone opened", blocks.Load())
		}
		if gaps.Load() != 0 {
			t.Errorf("%d blocks did not follow the one before", gaps.Load())
		}
		if p := s.State().Problem; p != nil {
			t.Errorf("once recording, the state tells %v", p)
		}
	})
}

// TestAAudioGrowsNoLatencyAsAStreamOpens has the output's callback find
// its ring short while each open of the microphone takes 200 ms, as a
// system held up by the open would: the dropouts count, the latency
// stays as it was. Dry spells once all is open grow it, as before.
func TestAAudioGrowsNoLatencyAsAStreamOpens(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 96, delay: 192, inTakes: 200 * time.Millisecond, dryWhileOpening: true}
		f.failIn.Store(6)
		useFakeAA(t, f)
		m := audio.NewMixer()
		s := openAA(t, m, Options{Record: true})
		time.Sleep(300 * time.Millisecond)
		synctest.Wait()
		lat, ahead := s.Latency(), s.ahead.Load()
		time.Sleep(6 * time.Second)
		synctest.Wait()
		if _, ins := f.streams(); len(ins) != 1 {
			t.Fatalf("%d microphones opened, want 1", len(ins))
		}
		if s.State().Dropouts == 0 {
			t.Error("the dry spells as the microphone opened counted no dropouts")
		}
		if l := s.Latency(); l > lat+time.Millisecond || s.ahead.Load() != ahead {
			t.Errorf("the latency grew from %v to %v as the microphone opened", lat, l)
		}
		f.starve.Store(true)
		time.Sleep(time.Second)
		f.starve.Store(false)
		synctest.Wait()
		if l := s.Latency(); l <= lat {
			t.Errorf("the latency stayed %v as the sound ran dry with all open", l)
		}
	})
}

// TestAAudioBacksOffAMicrophoneThatWillNotStart has every microphone
// open and then fail to start: each try waits as long as one that
// failed to open, half a second, then longer, to every 2 s, and every
// 10 s after ten. A dry spell between tries still grows the latency.
func TestAAudioBacksOffAMicrophoneThatWillNotStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 96, delay: 192}
		f.failStartIn.Store(true)
		useFakeAA(t, f)
		m := audio.NewMixer()
		s := openAA(t, m, Options{Record: true})
		time.Sleep(26 * time.Second)
		lat := s.Latency()
		// Tries at 25.5 s and 35.5 s: a dry spell between them.
		f.starve.Store(true)
		time.Sleep(time.Second)
		f.starve.Store(false)
		synctest.Wait()
		if l := s.Latency(); l <= lat {
			t.Errorf("the latency stayed %v as the sound ran dry between tries", l)
		}
		time.Sleep(20 * time.Second)
		synctest.Wait()
		f.mu.Lock()
		opens := append([]fakeOpen(nil), f.inOpens...)
		f.mu.Unlock()
		// The first open is as the speaker opens; each that would not
		// start counts one failure more.
		if len(opens) != 13 {
			t.Errorf("in 47 s the microphone was opened %d times, want 13", len(opens))
		}
		for i := 1; i < len(opens); i++ {
			want := micRetry(i)
			if gap := opens[i].at.Sub(opens[i-1].at); gap < want || gap > want+10*time.Millisecond {
				t.Errorf("try %d came %v after the one before, want %v", i, gap, want)
			}
		}
	})
}

// TestAAudioGrowsNoLatencyJustAfterAStreamOpens has the output's
// callback find its ring short for 200 ms right after the microphone
// opens: inside aaSettle of the open, those dropouts count, and leave
// the latency as it was.
func TestAAudioGrowsNoLatencyJustAfterAStreamOpens(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 96, delay: 192, dryAfterOpen: 200 * time.Millisecond}
		// The first try, as the speaker opens, fails; the next, half a
		// second later, opens.
		f.failIn.Store(2)
		useFakeAA(t, f)
		m := audio.NewMixer()
		s := openAA(t, m, Options{Record: true})
		time.Sleep(300 * time.Millisecond)
		synctest.Wait()
		lat, ahead := s.Latency(), s.ahead.Load()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if _, ins := f.streams(); len(ins) != 1 {
			t.Fatalf("%d microphones opened, want 1", len(ins))
		}
		if s.State().Dropouts == 0 {
			t.Error("the dry spell after the microphone opened counted no dropouts")
		}
		if l := s.Latency(); l > lat+time.Millisecond || s.ahead.Load() != ahead {
			t.Errorf("the latency grew from %v to %v just after the microphone opened", lat, l)
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

// TestUnstamped checks the part of a Bluetooth route's delay counted on
// top of what a stream's timestamps place, with a buffer of 1920 frames
// and bursts of 960, as on the WF-1000XM4.
func TestUnstamped(t *testing.T) {
	const buffer, burst = 1920, 960
	for _, c := range []struct {
		name               string
		delay, mixer, want int64
	}{
		{"no latency reported", 2400, 0, 0},
		{"the mixer's stamp, inside the buffer", 1500, 10560, 10560},
		{"the mixer's stamp, past the buffer", 2400, 10560, 10560},
		{"the mixer's stamp, a burst past the buffer", buffer + burst, 10560, 10560},
		{"the device's stamp, counting part", buffer + burst + 4000, 10560, 6560},
		{"the device's stamp, counting all", buffer + 10560, 10560, burst},
		{"the device's stamp, counting more", buffer + 20000, 10560, 0},
	} {
		if got := unstamped(c.delay, buffer, burst, c.mixer); got != c.want {
			t.Errorf("%s: delay %d, mixer %d: got %d, want %d", c.name, c.delay, c.mixer, got, c.want)
		}
	}
}

// TestAAudioCountsWhatBluetoothStampsLeaveOut plays on a fake Bluetooth
// headset whose timestamps leave out the 220 ms Android's mixer reports,
// as the WF-1000XM4's do on a Pixel 8 Pro: the frame heard and the
// latency count it, every 10 ms. Then the route turns to the phone's
// speaker, with timestamps that leave nothing out: once the opener asks
// again, nothing more is counted.
func TestAAudioCountsWhatBluetoothStampsLeaveOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// 50 ms behind the frames carried: the stream's 40 ms buffer and
		// a little, as the mixer stamps them.
		f := &fakeAA{rate: 48000, burst: 960, delay: 2400}
		mixer := 220 * time.Millisecond
		f.route.Store(&aaudio.Route{Known: true, Type: 8, Latency: mixer})
		useFakeAA(t, f)
		m := audio.NewMixer()
		v := m.Play(&ramp{}, audio.Options{})
		s := openAA(t, m, Options{Latency: 20 * time.Millisecond}, v)
		outs, _ := f.streams()
		extra := audio.FramesAt(mixer, 48000)
		check := func(what string, behind int64) {
			t.Helper()
			for i := range 100 {
				time.Sleep(10 * time.Millisecond)
				synctest.Wait()
				c := outs[0].carried(aaClock())
				if c <= behind {
					continue
				}
				if got, want := m.Heard(), c-behind; abs64(got-want) > 2 {
					t.Fatalf("%s, at %v: the speaker hears frame %d; the device plays %d", what,
						time.Duration(i)*10*time.Millisecond, got, want)
				}
			}
			st := s.State()
			want := audio.DurationAt(behind, 48000)
			if st.Latency < want || st.Latency > want+40*time.Millisecond {
				t.Errorf("%s: the latency is %v; the device alone delays %v", what, st.Latency, want)
			}
			// The driver's latency is smoothed, as the delay a timestamp
			// tells jumps by a burst as the callback runs.
			if got := int64(st.Info.Latency); abs64(got-behind) > 960 {
				t.Errorf("%s: the driver's latency is %d frames; want %d", what, got, behind)
			}
		}
		time.Sleep(200 * time.Millisecond)
		check("on the headset", f.delay+extra)
		if got := s.State().Route; got != RouteBluetooth {
			t.Errorf("on the headset the route is %v", got)
		}
		// Android tells nothing a while, as where its audio service
		// failed: the output keeps what it told before.
		f.route.Store(&aaudio.Route{})
		time.Sleep(aaRouteEvery + 200*time.Millisecond)
		check("with the route untold", f.delay+extra)
		if got := s.State().Route; got != RouteBluetooth {
			t.Errorf("with the route untold the route is %v", got)
		}
		f.route.Store(&aaudio.Route{Known: true, Type: 2, Latency: 40 * time.Millisecond})
		// The opener asks within aaRouteEvery, and the delay moves to the
		// speaker's over twice its own length.
		time.Sleep(aaRouteEvery + aaSlew*mixer + 200*time.Millisecond)
		check("on the speaker", f.delay)
		if got := s.State().Route; got != RouteSpeaker {
			t.Errorf("on the speaker the route is %v", got)
		}
	})
}

// TestAAudioQuickMarksTheStream checks that Quick reaches the stream,
// and that changing it opens the output again.
func TestAAudioQuickMarksTheStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 192, delay: 960}
		useFakeAA(t, f)
		m := audio.NewMixer()
		s := openAA(t, m, Options{})
		time.Sleep(100 * time.Millisecond)
		if err := s.Set(Options{Driver: driver.AAudio, Quick: true}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		outs, _ := f.streams()
		if len(outs) != 2 || outs[0].c.quick || !outs[1].c.quick {
			t.Fatalf("opened %d outputs; want two, the second alone quick", len(outs))
		}
	})
}

// TestAAudioMovesTheBluetoothDelaySmoothly connects a fake headset whose
// delay Android tells late, as the Pixel 8 Pro told 20 ms at first and
// 200 ms a moment later: the opener asks again soon after the stream
// opens, and the frame heard moves on every 10 ms at half speed or
// more, never stopping, until it counts the delay in full.
func TestAAudioMovesTheBluetoothDelaySmoothly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 960, delay: 2400}
		f.route.Store(&aaudio.Route{Known: true, Type: 8, Latency: 20 * time.Millisecond})
		useFakeAA(t, f)
		m := audio.NewMixer()
		v := m.Play(&ramp{}, audio.Options{})
		openAA(t, m, Options{Latency: 20 * time.Millisecond}, v)
		outs, _ := f.streams()
		time.Sleep(100 * time.Millisecond)
		f.route.Store(&aaudio.Route{Known: true, Type: 8, Latency: 200 * time.Millisecond})
		step := audio.FramesAt(10*time.Millisecond, 48000)
		h := m.Heard()
		for i := range 100 {
			time.Sleep(10 * time.Millisecond)
			synctest.Wait()
			got := m.Heard()
			if d := got - h; h > 0 && (d < step/2-2 || d > step*3/2+2) {
				t.Fatalf("at %v the frame heard moved %d frames in 10 ms; want %d to %d", time.Duration(i)*10*time.Millisecond,
					d, step/2, step*3/2)
			}
			h = got
		}
		want := outs[0].carried(aaClock()) - f.delay - audio.FramesAt(200*time.Millisecond, 48000)
		if got := m.Heard(); abs64(got-want) > 2 {
			t.Errorf("after a second the speaker hears frame %d; the device plays %d", got, want)
		}
	})
}

// TestAAudioWorksAheadOfTheBluetoothDelay asks for 400 ms of latency on
// a headset whose timestamps leave out 220 ms: the latency stays 400 ms,
// the ring holding the less.
func TestAAudioWorksAheadOfTheBluetoothDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 960, delay: 2400}
		f.route.Store(&aaudio.Route{Known: true, Type: 8, Latency: 220 * time.Millisecond})
		useFakeAA(t, f)
		m := audio.NewMixer()
		v := m.Play(&ramp{}, audio.Options{})
		s := openAA(t, m, Options{Latency: 400 * time.Millisecond, Fixed: true}, v)
		time.Sleep(time.Second)
		synctest.Wait()
		if l := s.Latency(); l < 390*time.Millisecond || l > 420*time.Millisecond {
			t.Errorf("the latency is %v; want 400 ms", l)
		}
	})
}

// TestAAudioGrowsNoLeadFromAnOpening plays on the phone's speaker, then
// on Bluetooth earbuds, 280 ms slower, whose stream starts its device
// 300 ms after it starts, and the mixer is late once just then. Those
// dry spells come from opening: they leave the lead as it was, though
// the stream opened 300 ms before. Then the mixer is late once while
// the earbuds play steadily: the lead grows by 10 ms, not by the
// earbuds' delay. Back on the speaker, the speaker works as far ahead
// as before, and the 10 ms more.
func TestAAudioGrowsNoLeadFromAnOpening(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 192, delay: 960}
		useFakeAA(t, f)
		m := audio.NewMixer()
		r := &stalling{}
		v := m.Play(r, audio.Options{})
		s := openAA(t, m, Options{}, v)
		time.Sleep(3 * time.Second)
		synctest.Wait()
		before := s.Latency()

		// The speaker's stream fails as the earbuds take over.
		f.startAfter.Store(int64(300 * time.Millisecond))
		f.more.Store(audio.FramesAt(280*time.Millisecond, 48000))
		outs, _ := f.streams()
		outs[len(outs)-1].sh.Err.Store(-899)
		r.stallAt(310*time.Millisecond, 80*time.Millisecond)
		time.Sleep(3 * time.Second)
		synctest.Wait()
		if outs, _ = f.streams(); len(outs) != 2 {
			t.Fatalf("%d streams opened, want 2", len(outs))
		}
		if g := s.margin.Load(); g != 0 || s.ahead.Load() != int64(30*time.Millisecond) {
			t.Fatalf("the dry spells of an opening grew the lead by %v, and set it to %v",
				time.Duration(g), time.Duration(s.ahead.Load()))
		}

		// Late once, playing steadily: 10 ms more, or 20 where the dry
		// spells fall in two of the watch's looks.
		drops := s.State().Dropouts
		r.stallAt(0, 80*time.Millisecond)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if d := s.State().Dropouts - drops; d < 2 {
			t.Fatalf("the late mixer ran the device dry %d times, want 2 or more", d)
		}
		grown := time.Duration(s.margin.Load())
		if grown != 10*time.Millisecond && grown != 20*time.Millisecond {
			t.Fatalf("the dry spell grew the lead by %v, want 10 ms or 20", grown)
		}

		// Back on the speaker.
		f.startAfter.Store(0)
		f.more.Store(0)
		outs[len(outs)-1].sh.Err.Store(-899)
		time.Sleep(3 * time.Second)
		synctest.Wait()
		// The latency holds the delay the timestamps tell, which moves
		// within a burst as the callback runs, so each reading is good to
		// a burst. Here it reads 30 to 32.125 ms.
		slack := audio.DurationAt(int64(f.burst), 48000)
		if l, want := s.Latency(), before+grown; l < want-slack || l > want+slack {
			t.Errorf("back on the speaker the speaker works %v ahead, want %v within %v: %v as before, and %v grown", l, want, slack, before, grown)
		}
	})
}

// TestRouteOf checks each kind of device Android tells of, and above all
// that USB headphones, what a wired headset is on a Pixel 8 Pro, read as
// USB, apart from the speaker.
func TestRouteOf(t *testing.T) {
	for want, types := range map[Route][]int{
		RouteUnknown:   {0, 9, 10, 13, 15, 18, 20, 21, 25, 29, 999},
		RouteSpeaker:   {1, 2, 24},
		RouteWired:     {3, 4, 5, 6, 19},
		RouteBluetooth: {7, 8, 23, 26, 27, 30},
		RouteUSB:       {11, 12, 22},
	} {
		for _, typ := range types {
			if got := routeOf(typ); got != want {
				t.Errorf("a device of type %d is on route %v, want %v", typ, got, want)
			}
		}
	}
}

// TestAAudioRouteFollowsTheDevice moves the sound from the speaker to USB
// headphones, to earbuds and back, as Android does: the device goes away,
// and the stream opens again on the next. The route reads each at once,
// and stays told while a stream reopens. Another moves while one plays,
// which the opener hears when it asks.
func TestAAudioRouteFollowsTheDevice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 192, delay: 960}
		f.route.Store(&aaudio.Route{Known: true, Type: 2, Latency: 20 * time.Millisecond})
		useFakeAA(t, f)
		s := openAA(t, audio.NewMixer(), Options{})
		time.Sleep(100 * time.Millisecond)
		if got := s.State().Route; got != RouteSpeaker {
			t.Fatalf("on the speaker the route is %v", got)
		}
		for _, step := range []struct {
			typ  int
			want Route
		}{{22, RouteUSB}, {8, RouteBluetooth}, {4, RouteWired}, {2, RouteSpeaker}} {
			f.route.Store(&aaudio.Route{Known: true, Type: step.typ, Latency: 20 * time.Millisecond})
			outs, _ := f.streams()
			was := s.State().Route
			outs[len(outs)-1].sh.Err.Store(-899)
			for range 40 {
				time.Sleep(5 * time.Millisecond)
				synctest.Wait()
				if now, _ := f.streams(); len(now) > len(outs) {
					break
				}
				if got := s.State().Route; got != was {
					t.Fatalf("before the stream reopened on type %d the route read %v", step.typ, got)
				}
			}
			if got := s.State().Route; got != step.want {
				t.Fatalf("on a device of type %d the route is %v, want %v", step.typ, got, step.want)
			}
		}
		// A device that left the list is one going away: where the sound
		// plays is no longer known.
		f.route.Store(&aaudio.Route{Known: true, Latency: 20 * time.Millisecond})
		time.Sleep(aaRouteEvery + 100*time.Millisecond)
		if got := s.State().Route; got != RouteUnknown {
			t.Errorf("with the device gone the route is %v", got)
		}
		f.route.Store(&aaudio.Route{Known: true, Type: 22, Latency: 20 * time.Millisecond})
		time.Sleep(aaRouteEvery + 100*time.Millisecond)
		if got := s.State().Route; got != RouteUSB {
			t.Errorf("after the opener asked the route is %v", got)
		}
		// A stream that reopens where Android tells nothing plays nowhere
		// known, whatever it played on before.
		f.route.Store(&aaudio.Route{})
		outs, _ := f.streams()
		outs[len(outs)-1].sh.Err.Store(-899)
		for range 40 {
			time.Sleep(5 * time.Millisecond)
			synctest.Wait()
			if now, _ := f.streams(); len(now) > len(outs) {
				break
			}
		}
		if now, _ := f.streams(); len(now) <= len(outs) {
			t.Fatal("the stream never reopened")
		}
		if got := s.State().Route; got != RouteUnknown {
			t.Errorf("after reopening with nothing told the route is %v", got)
		}
	})
}

// Headphones are told apart from what may drive loudspeakers, and a
// device that left Android's list is neither.
func TestAAudioToldHeadphones(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 192, delay: 960}
		f.route.Store(&aaudio.Route{Known: true, Type: 4, Latency: 20 * time.Millisecond})
		useFakeAA(t, f)
		s := openAA(t, audio.NewMixer(), Options{})
		time.Sleep(100 * time.Millisecond)
		if !s.State().Headphones {
			t.Error("wired headphones read as none")
		}
		for _, c := range []struct {
			typ  int
			want bool
		}{{5, false}, {22, true}, {11, false}, {12, false}, {19, false}, {2, false}, {8, false}, {3, true}, {0, false}} {
			f.route.Store(&aaudio.Route{Known: true, Type: c.typ, Latency: 20 * time.Millisecond})
			time.Sleep(aaRouteEvery + 100*time.Millisecond)
			if got := s.State().Headphones; got != c.want {
				t.Errorf("a device of type %d reads headphones %v", c.typ, got)
			}
		}
	})
}

// TestAAudioRecordsTheBuiltInMicOnBluetooth opens the input on each
// route and moves between them: it asks for the phone's own microphone
// while Bluetooth plays, so earbuds stay on their music link, and leaves
// the pick to Android otherwise, so a USB headset's microphone records.
func TestAAudioRecordsTheBuiltInMicOnBluetooth(t *testing.T) {
	const mic = 15
	on := func(typ int) *aaudio.Route {
		return &aaudio.Route{Known: true, Type: typ, Latency: 20 * time.Millisecond, Mic: mic}
	}
	for _, c := range []struct {
		name string
		typ  int
		want int
	}{{"speaker", 2, 0}, {"USB", 22, 0}, {"wired", 4, 0}, {"A2DP", 8, mic}, {"LE Audio", 26, mic}} {
		t.Run("opening on "+c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := &fakeAA{rate: 48000, burst: 192, delay: 960}
				f.route.Store(on(c.typ))
				useFakeAA(t, f)
				openAA(t, audio.NewMixer(), Options{Record: true})
				time.Sleep(10 * time.Millisecond)
				_, ins := f.streams()
				if len(ins) != 1 || ins[0].c.device != c.want {
					t.Fatalf("%d inputs opened, the first on device %d, want one on %d", len(ins), ins[0].c.device, c.want)
				}
			})
		})
	}
	t.Run("moving", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := &fakeAA{rate: 48000, burst: 192, delay: 960}
			f.route.Store(on(2))
			useFakeAA(t, f)
			openAA(t, audio.NewMixer(), Options{Record: true})
			// expect moves the route as the opener hears it, and reads
			// which input records after.
			expect := func(typ, inputs, want int) {
				t.Helper()
				f.route.Store(on(typ))
				time.Sleep(aaRouteEvery + 100*time.Millisecond)
				synctest.Wait()
				_, ins := f.streams()
				if len(ins) != inputs || ins[inputs-1].c.device != want {
					t.Fatalf("on type %d: %d inputs opened, the last on device %d, want %d on %d", typ, len(ins),
						ins[len(ins)-1].c.device, inputs, want)
				}
			}
			expect(22, 1, 0) // No reason to reopen: Android picks on both.
			expect(8, 2, mic)
			expect(8, 2, mic)
			expect(22, 3, 0)
			// The device gone, with the route kept, reopens nothing.
			expect(0, 3, 0)
		})
	})
	t.Run("the microphone refuses", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := &fakeAA{rate: 48000, burst: 192, delay: 960}
			f.route.Store(on(8))
			f.failIn.Store(2) // Exclusive, then shared.
			useFakeAA(t, f)
			openAA(t, audio.NewMixer(), Options{Record: true})
			// It records on Android's pick, and does not keep trying.
			time.Sleep(3 * aaRouteEvery)
			synctest.Wait()
			_, ins := f.streams()
			if len(ins) != 1 || ins[0].c.device != 0 {
				t.Fatalf("%d inputs opened, the first on device %d, want one on 0", len(ins), ins[0].c.device)
			}
		})
	})
	t.Run("a phone with no microphone listed", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := &fakeAA{rate: 48000, burst: 192, delay: 960}
			f.route.Store(&aaudio.Route{Known: true, Type: 8, Latency: 20 * time.Millisecond})
			useFakeAA(t, f)
			openAA(t, audio.NewMixer(), Options{Record: true})
			time.Sleep(10 * time.Millisecond)
			if _, ins := f.streams(); len(ins) != 1 || ins[0].c.device != 0 {
				t.Fatalf("%d inputs opened, the first on device %d, want one on 0", len(ins), ins[0].c.device)
			}
		})
	})
}
