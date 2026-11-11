package speaker

import (
	"bytes"
	"errors"
	"log"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/driver"
	"github.com/marrasen/gunim/audio/internal/duplex"
	"github.com/marrasen/gunim/audio/internal/wasapi"
)

// fakeWas is WASAPI as a test plays it. Each device it opens has an
// engine that takes a period of frames from the device's buffer at
// each period's end, from the moment it starts, as Windows' engine
// does, and plays frame c(t) - latency at t, c(t) being the frames its
// time has moved: (t - start) × rate. Its clock tells that frame. Its
// input hands on a packet of a period at each period's end, of frames
// that came in over that period; each packet's first sample holds the
// ramp value the speakers played as its first frame came in, -1 where
// they played none, so a test reads the truth from it.
type fakeWas struct {
	rate, period, buffer, latency, chans, inputs int
	// startAfter is how long each device takes, once started, before
	// its time moves; frozen, where set, opens each device frozen.
	startAfter time.Duration
	frozen     atomic.Bool
	// stampEnd stamps each input packet as its last frame came in, as
	// the MOTU M Series does on a fresh open on Windows, read under mu;
	// lateStamp, where lateBy is set, stamps lateCount packets from the
	// one of that number that much later. Where
	// set, stamps the packet of that number that much later.
	stampEnd                     bool
	lateStamp, lateCount, lateBy int64
	// failOpen fails as many opens; nextRate, nextInputs and
	// nextLatency, where set, are the next device's.
	failOpen, nextRate, nextInputs, nextLatency atomic.Int64
	mu                                          sync.Mutex
	devs                                        []*fakeWasDev
	// misuse counts calls into a device after its Close.
	misuse atomic.Int64
}

type fakeWasDev struct {
	f    *fakeWas
	info wasapi.Info
	// The rest is guarded by f.mu. t0 is when the device's time began,
	// moved on by the time it stood stopped; stoppedAt when it stopped.
	t0, stoppedAt int64
	running, ever bool
	// q holds the first sample of each frame written and not yet taken;
	// played those taken, in order, silence as -1.
	q, played []float32
	// periods counts the periods the engine took, and packets the
	// input's packets read.
	periods, packets int64
	// lost fails each call, as a device unplugged; changed is what
	// Changed reports once; frozen stops its time, as a device gone
	// untold.
	lost, changed, frozen atomic.Bool
	closes                atomic.Int64
	starts, stops         int
}

func useFakeWas(t *testing.T, f *fakeWas) {
	t.Helper()
	was, clock := openWasDevice, wasClock
	openWasDevice = f.open
	wasClock = func() int64 { return time.Now().UnixNano() }
	t.Cleanup(func() { openWasDevice, wasClock = was, clock })
}

func (f *fakeWas) open(c wasapi.Config) (wasDevice, error) {
	if f.failOpen.Add(-1) >= 0 {
		return nil, errors.New("fake: no device")
	}
	f.failOpen.Store(0)
	rate, inputs, latency := f.rate, f.inputs, f.latency
	if l := f.nextLatency.Swap(0); l > 0 {
		f.latency, latency = int(l), int(l)
	}
	if r := f.nextRate.Swap(0); r > 0 {
		f.rate, rate = int(r), int(r)
	}
	if n := f.nextInputs.Swap(0); n > 0 {
		f.inputs, inputs = int(n), int(n)
	}
	info := wasapi.Info{Name: "Speakers", ID: "out", Rate: rate, Channels: f.chans, Period: f.period, Buffer: f.buffer,
		Latency: latency, LowLatency: true}
	if c.Record {
		info.InputName, info.InputID, info.Inputs, info.InputBuffer = "Microphone", "in", inputs, f.period
		info.InputLatency, info.InputPeriod = f.period, f.period
	}
	d := &fakeWasDev{f: f, info: info}
	d.frozen.Store(f.frozen.Load())
	f.mu.Lock()
	defer f.mu.Unlock()
	f.devs = append(f.devs, d)
	return d, nil
}

// dev returns the ith device opened, and how many opened.
func (f *fakeWas) dev(i int) (d *fakeWasDev, opened int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i < 0 {
		i += len(f.devs)
	}
	return f.devs[i], len(f.devs)
}

// periodNs is how long a period plays.
func (d *fakeWasDev) periodNs() int64 { return int64(d.info.Period) * 1e9 / int64(d.info.Rate) }

// elapsed returns the frames the device's time has moved at now. It
// runs with f.mu held.
func (d *fakeWasDev) elapsed(now int64) int64 {
	if !d.ever {
		return 0
	}
	if !d.running {
		now = d.stoppedAt
	}
	return max(0, now-d.t0) * int64(d.info.Rate) / 1e9
}

// advance has the engine take the periods that ended by now. It runs
// with f.mu held.
func (d *fakeWasDev) advance(now int64) {
	if !d.running || d.frozen.Load() {
		return
	}
	for d.periods < max(0, now-d.t0)/d.periodNs() {
		k := min(d.info.Period, len(d.q))
		d.played = append(d.played, d.q[:k]...)
		d.q = d.q[k:]
		d.periods++
	}
}

// heard returns the frame the speakers play at now, -1 for none yet.
// It runs with f.mu held.
func (d *fakeWasDev) heard(now int64) int64 {
	return min(d.elapsed(now)-int64(d.info.Latency), int64(len(d.played))-1)
}

// truthAt returns the ramp's frame the speakers play at now, or -1, and
// the device's frame they play.
func (d *fakeWasDev) truthAt(now int64) (truth, frame int64) {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return d.truthLocked(now), d.heard(now)
}

func (d *fakeWasDev) truthLocked(now int64) int64 {
	h := d.heard(now)
	if h < 0 || d.played[h] < 0 {
		return -1
	}
	return int64(math.Round(float64(d.played[h]) * 1e6))
}

// check counts a call after Close, and returns ErrLost for a device
// lost.
func (d *fakeWasDev) check() error {
	if d.closes.Load() > 0 {
		d.f.misuse.Add(1)
	}
	if d.lost.Load() {
		return wasapi.ErrLost
	}
	return nil
}

func (d *fakeWasDev) Info() wasapi.Info { return d.info }

func (d *fakeWasDev) Start() error {
	if err := d.check(); err != nil {
		return err
	}
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	now := wasClock()
	if d.running {
		return errors.New("fake: started already")
	}
	if !d.ever {
		d.t0, d.ever = now+int64(d.f.startAfter), true
	} else {
		d.t0 += now - d.stoppedAt
	}
	d.running = true
	d.starts++
	return nil
}

func (d *fakeWasDev) Stop() error {
	if err := d.check(); err != nil {
		return err
	}
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	now := wasClock()
	d.advance(now)
	if d.running {
		d.running, d.stoppedAt = false, now
	}
	d.stops++
	return nil
}

func (d *fakeWasDev) Wait(most time.Duration) {
	_ = d.check()
	d.f.mu.Lock()
	now := wasClock()
	d.advance(now)
	wait := int64(most)
	if d.running && !d.frozen.Load() {
		wait = min(wait, d.t0+(d.periods+1)*d.periodNs()-now)
	}
	d.f.mu.Unlock()
	time.Sleep(time.Duration(max(wait, 1)))
}

func (d *fakeWasDev) Room() (int, error) {
	if err := d.check(); err != nil {
		return 0, err
	}
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	d.advance(wasClock())
	return d.info.Buffer - len(d.q), nil
}

func (d *fakeWasDev) Write(frames []float32) error {
	if err := d.check(); err != nil {
		return err
	}
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	if len(d.q)+len(frames)/2 > d.info.Buffer {
		return errors.New("fake: written past the buffer")
	}
	for i := 0; i < len(frames); i += 2 {
		d.q = append(d.q, frames[i])
	}
	return nil
}

func (d *fakeWasDev) Silence(n int) error {
	if err := d.check(); err != nil {
		return err
	}
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	if len(d.q)+n > d.info.Buffer {
		return errors.New("fake: written past the buffer")
	}
	for range n {
		d.q = append(d.q, -1)
	}
	return nil
}

func (d *fakeWasDev) Clock() (pos, at int64, err error) {
	if err := d.check(); err != nil {
		return 0, 0, err
	}
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	now := wasClock()
	d.advance(now)
	if d.frozen.Load() {
		return int64(len(d.played)), now, nil
	}
	return max(0, d.heard(now)), now, nil
}

func (d *fakeWasDev) Read(dst []float32) (n int, at int64, ok bool, err error) {
	if err := d.check(); err != nil {
		return 0, 0, false, err
	}
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	if d.info.Inputs == 0 || !d.running {
		return 0, 0, false, nil
	}
	d.advance(wasClock())
	if d.packets >= d.periods {
		return 0, 0, false, nil
	}
	at = d.t0 + d.packets*d.periodNs()
	first := at
	if d.f.stampEnd {
		at += d.periodNs()
	}
	late := d.f.lateBy != 0 && d.packets >= d.f.lateStamp && d.packets < d.f.lateStamp+max(1, d.f.lateCount)
	if late {
		at += d.f.lateBy
	}
	d.packets++
	n = d.info.Period
	clear(dst[:n*d.info.Inputs])
	dst[0] = -1
	if late && d.info.Inputs > 1 {
		// Its second channel tells the test which it is.
		dst[1] = 1
	}
	if v := d.truthLocked(first); v >= 0 {
		dst[0] = float32(v) * 1e-6
	}
	return n, at, true, nil
}

func (d *fakeWasDev) Position() int64 {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return max(0, d.packets-1) * int64(d.info.Period)
}

func (d *fakeWasDev) Changed() bool {
	_ = d.check()
	return d.changed.Swap(false)
}

func (d *fakeWasDev) Close() { d.closes.Add(1) }

// openWas opens the speaker through WASAPI, in a bubble, and closes it
// as the test ends, its voices stopped.
func openWas(t *testing.T, m *audio.Mixer, o Options, voices ...*audio.Voice) *Speaker {
	t.Helper()
	o.Driver = driver.WASAPI
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
		m.Mix(make([]float32, 2*4800))
		time.Sleep(2 * time.Second)
	})
	if _, ok := s.out.Load().output.(*wasOutput); !ok {
		t.Fatalf("the speaker plays through %T", s.out.Load().output)
	}
	return s
}

// hearsAsPlayed checks, every 10 ms for d, that the frame the speaker
// says is heard is the one the device plays then.
func hearsAsPlayed(t *testing.T, m *audio.Mixer, dev *fakeWasDev, d time.Duration) {
	t.Helper()
	checked := 0
	for range d / (10 * time.Millisecond) {
		time.Sleep(10 * time.Millisecond)
		synctest.Wait()
		// Until its clock has told a frame heard, at the end of the period
		// its first frame plays in, the speaker guesses: let that be.
		want, at := dev.truthAt(wasClock())
		if want < 0 || at < int64(2*dev.info.Period) {
			continue
		}
		checked++
		if got := m.Heard(); abs64(got-want) > 2 {
			t.Fatalf("the speaker hears frame %d; the device plays %d", got, want)
		}
	}
	if checked < int(d/(20*time.Millisecond)) {
		t.Fatalf("the device played the ramp at %d of %v's checks", checked, d)
	}
}

// TestWASAPIPlaysTheMixerAsHeard plays a ramp through WASAPI for a
// while: the device plays at its own rate, whatever was asked, takes
// every frame in order, never short once started, and the frame the
// speaker says is heard is the one the device plays then, its buffer
// and latency counted, every 10 ms.
func TestWASAPIPlaysTheMixerAsHeard(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeWas{rate: 44100, period: 441, buffer: 882, latency: 441, chans: 2}
		useFakeWas(t, f)
		m := audio.NewMixer()
		v := m.Play(&ramp{}, audio.Options{})
		s := openWas(t, m, Options{Rate: 48000, Latency: 30 * time.Millisecond, Fixed: true}, v)
		st := s.State()
		if st.Driver != driver.WASAPI || st.Rate != 44100 || m.Rate() != 44100 || st.Info == nil || st.Info.Buffer != 441 ||
			len(st.Info.Played) != 2 || st.Problem != nil {
			t.Fatalf("the state says %+v, %+v", st, st.Info)
		}
		dev, _ := f.dev(0)
		hearsAsPlayed(t, m, dev, 3*time.Second)
		if d := s.State().Dropouts; d != 0 {
			t.Errorf("the sound ran dry %d times", d)
		}
		f.mu.Lock()
		last := int64(-1)
		for i, v := range dev.played {
			if v < 0 {
				if last >= 0 {
					f.mu.Unlock()
					t.Fatalf("the device played silence at %d, after the ramp began", i)
				}
				continue
			}
			if r := int64(math.Round(float64(v) * 1e6)); last >= 0 && r != last+1 {
				f.mu.Unlock()
				t.Fatalf("the device played frame %d after %d", r, last)
			} else {
				last = r
			}
		}
		f.mu.Unlock()
		// The device holds its buffer, and plays it its latency later.
		st = s.State()
		if d := st.Info.Latency; d < 441 || d > 882+441 {
			t.Errorf("the device's delay is %d frames", d)
		}
		// As asked, or the device's own delay and the ring's least.
		least := int64(441) + audio.FramesAt(2*wasTick, 44100)
		if l, most := s.Latency(), audio.DurationAt(int64(st.Info.Latency)+least, 44100); l < 30*time.Millisecond || l > most+time.Millisecond {
			t.Errorf("the latency is %v, asked 30 ms; the device delays %d frames", l, st.Info.Latency)
		}
		if !errors.Is(s.ControlPanel(), errWasPanel) {
			t.Errorf("ControlPanel = %v", s.ControlPanel())
		}
	})
}

// TestWASAPIOpensAgainWhereTheDeviceGoes loses the device, then has the
// default device change, then stops a device's clock untold, then has
// no device for a while: each time the output opens the default device
// itself, with no Changed, and the speaker hears what the new device
// plays. A device at another rate has the speaker open again.
func TestWASAPIOpensAgainWhereTheDeviceGoes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeWas{rate: 48000, period: 480, buffer: 1440, latency: 960, chans: 2}
		useFakeWas(t, f)
		m := audio.NewMixer()
		v := m.Play(&ramp{}, audio.Options{})
		s := openWas(t, m, Options{}, v)
		time.Sleep(500 * time.Millisecond)

		lose := []func(d *fakeWasDev){
			func(d *fakeWasDev) { d.lost.Store(true) },
			func(d *fakeWasDev) { d.changed.Store(true) },
			func(d *fakeWasDev) { d.frozen.Store(true) },
		}
		for i, how := range lose {
			dev, n := f.dev(-1)
			how(dev)
			time.Sleep(wasStalled + 200*time.Millisecond)
			synctest.Wait()
			if _, now := f.dev(-1); now != n+1 {
				t.Fatalf("case %d: %d devices opened, want %d", i, now, n+1)
			}
			if dev.closes.Load() != 1 {
				t.Fatalf("case %d: the device gone closed %d times", i, dev.closes.Load())
			}
			dev, _ = f.dev(-1)
			hearsAsPlayed(t, m, dev, time.Second)
		}
		// No device for a while: the state tells why, and it plays once
		// one is back.
		f.failOpen.Store(5)
		dev, n := f.dev(-1)
		dev.lost.Store(true)
		time.Sleep(30 * time.Millisecond)
		synctest.Wait()
		if s.State().Problem == nil {
			t.Error("with no device the state tells no problem")
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if _, now := f.dev(-1); now != n+1 {
			t.Fatalf("after five failed opens %d devices opened, want %d", now, n+1)
		}
		if p := s.State().Problem; p != nil {
			t.Errorf("playing again, the state tells %v", p)
		}
		dev, _ = f.dev(-1)
		hearsAsPlayed(t, m, dev, time.Second)
		select {
		case <-s.Changed():
			t.Fatal("the speaker told of a change: the output opened the device again itself")
		default:
		}
		// A device at another rate: the speaker opens again, and the
		// mixer follows.
		f.nextRate.Store(44100)
		dev, _ = f.dev(-1)
		dev.lost.Store(true)
		select {
		case <-s.Changed():
		case <-time.After(3 * time.Second):
			t.Fatal("the speaker never opened again for a device at another rate")
		}
		if m.Rate() != 44100 || s.State().Rate != 44100 {
			t.Errorf("the mixer plays at %d Hz, want 44100", m.Rate())
		}
		dev, _ = f.dev(-1)
		hearsAsPlayed(t, m, dev, time.Second)
		if f.misuse.Load() != 0 {
			t.Errorf("devices were called %d times after they closed", f.misuse.Load())
		}
	})
}

// TestWASAPIStampsWhatItRecords records the input as it plays: each
// packet reaches the tap stamped with the mixer frame the speakers
// played as its first frame came in, and the packets follow one
// another. That holds where WASAPI stamps a packet as its first frame
// came in, as Windows says, and where it stamps it as its last did, as
// the MOTU M Series does on a fresh open, the packet read microseconds
// after its stamp. One stamp 3 ms off misleads neither, nor two in a
// row.
func TestWASAPIStampsWhatItRecords(t *testing.T) {
	for _, c := range []struct {
		name string
		end  bool
		late int64
	}{
		{"first frame", false, 1}, {"last frame", true, 1},
		{"first frame, two late", false, 2}, {"last frame, two late", true, 2},
	} {
		t.Run(c.name, func(t *testing.T) { stampsWhatItRecords(t, c.end, c.late) })
	}
}

func stampsWhatItRecords(t *testing.T, end bool, late int64) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeWas{rate: 48000, period: 480, buffer: 960, latency: 2400, chans: 2, inputs: 2, stampEnd: end,
			lateStamp: 100, lateCount: late, lateBy: int64(3 * time.Millisecond)}
		useFakeWas(t, f)
		m := audio.NewMixer()
		v := m.Play(&ramp{}, audio.Options{})
		s := openWas(t, m, Options{Record: true}, v)
		tap := duplex.Current()
		if tap == nil || tap.Driver != driver.WASAPI || len(tap.Names) != 2 || tap.Rate != 48000 || tap.Latency != 480 {
			t.Fatalf("the tap is %+v", tap)
		}
		if st := s.State(); st.Info.Recorded != 2 || st.Info.InputNames[0] != "Microphone 1" {
			t.Errorf("the state says %+v", st.Info)
		}
		type packet struct {
			truth, heard int64
			late         bool
		}
		var (
			mu      sync.Mutex
			packets []packet
		)
		detach := tap.Attach(func(in []float32, channels int, heard int64) {
			mu.Lock()
			defer mu.Unlock()
			if channels != 2 || len(in) != 2*480 {
				t.Errorf("a packet of %d samples, %d channels", len(in), channels)
			}
			truth := int64(-1)
			if in[0] >= 0 {
				truth = int64(math.Round(float64(in[0]) * 1e6))
			}
			packets = append(packets, packet{truth, heard, in[1] == 1})
		})
		time.Sleep(2 * time.Second)
		synctest.Wait()
		detach()
		mu.Lock()
		defer mu.Unlock()
		checked := 0
		for i, p := range packets {
			if p.truth < 0 || p.late {
				continue
			}
			checked++
			if abs64(p.heard-p.truth) > 2 {
				t.Fatalf("packet %d is stamped %d; the speakers played %d as it came in", i, p.heard, p.truth)
			}
		}
		if checked < 150 {
			t.Fatalf("%d packets of %d came in as the ramp played, want 150", checked, len(packets))
		}
		late := false
		for _, p := range packets {
			late = late || p.late
		}
		if !late {
			t.Fatal("the packet stamped late never came in")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if duplex.Current() != nil {
			t.Error("the tap stays after the speaker closed")
		}
	})
}

// TestWASAPILearnsStampsAgainOnResume records from a device that
// stamps each packet as its last frame came in, then suspends and
// resumes it, after which it stamps the first frame: the packets
// recorded after it resumes are stamped as their first frames came in,
// not a period early.
func TestWASAPILearnsStampsAgainOnResume(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeWas{rate: 48000, period: 480, buffer: 960, latency: 480, chans: 2, inputs: 1, stampEnd: true}
		useFakeWas(t, f)
		m := audio.NewMixer()
		v := m.Play(&ramp{}, audio.Options{})
		s := openWas(t, m, Options{Record: true}, v)
		var (
			mu              sync.Mutex
			checked, missed int
			worst           int64
		)
		detach := duplex.Current().Attach(func(in []float32, _ int, heard int64) {
			mu.Lock()
			defer mu.Unlock()
			// Silence, or the stopped ramp's zeros: nothing to tell.
			if in[0] <= 0 {
				return
			}
			checked++
			if d := abs64(heard - int64(math.Round(float64(in[0])*1e6))); d > 2 {
				missed++
				worst = max(worst, d)
			}
		})
		defer detach()
		time.Sleep(time.Second)
		v.Stop(0)
		time.Sleep(100 * time.Millisecond)
		if err := s.Suspend(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(SuspendFade + time.Second)
		synctest.Wait()
		f.mu.Lock()
		f.stampEnd = false
		f.mu.Unlock()
		mu.Lock()
		checked, missed = 0, 0
		mu.Unlock()
		v = m.Play(&ramp{at: m.Mixed()}, audio.Options{})
		defer v.Stop(0)
		if err := s.Resume(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		mu.Lock()
		defer mu.Unlock()
		if checked < 50 {
			t.Fatalf("%d packets came in as the ramp played after it resumed, want 50", checked)
		}
		if missed > 0 {
			t.Errorf("after it resumed %d packets of %d were stamped off, by as much as %d frames", missed, checked, worst)
		}
	})
}

// TestWASAPISuspendStopsTheDevice suspends the speaker: the device
// stops, keeping its buffer, and the frame heard stands still until it
// resumes; then it plays on from where it was, as heard.
func TestWASAPISuspendStopsTheDevice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeWas{rate: 48000, period: 480, buffer: 960, latency: 480, chans: 2}
		useFakeWas(t, f)
		m := audio.NewMixer()
		v := m.Play(&ramp{}, audio.Options{})
		s := openWas(t, m, Options{}, v)
		time.Sleep(500 * time.Millisecond)
		// Nothing sounds, so it stops at once, with no fade.
		v.Stop(0)
		time.Sleep(100 * time.Millisecond)
		if err := s.Suspend(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(SuspendFade + time.Second)
		synctest.Wait()
		dev, _ := f.dev(0)
		f.mu.Lock()
		running, stops := dev.running, dev.stops
		f.mu.Unlock()
		if running || stops != 1 {
			t.Fatalf("suspended, the device runs %v, stopped %d times", running, stops)
		}
		h := m.Heard()
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		if got := m.Heard(); got != h {
			t.Errorf("suspended, the frame heard moved from %d to %d", h, got)
		}
		// The feed alone reads the mixer, and stands still: the ramp's
		// frames are the mixer's.
		v = m.Play(&ramp{at: m.Mixed()}, audio.Options{})
		defer v.Stop(0)
		if err := s.Resume(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		f.mu.Lock()
		running, starts := dev.running, dev.starts
		f.mu.Unlock()
		if !running || starts != 2 {
			t.Fatalf("resumed, the device runs %v, started %d times", running, starts)
		}
		hearsAsPlayed(t, m, dev, time.Second)
		if f.misuse.Load() != 0 {
			t.Errorf("devices were called %d times after they closed", f.misuse.Load())
		}
	})
}

// TestWASAPIHeldWhileReopening reads the output from other goroutines,
// as the UI does each frame, while the output opens again, devices go,
// and it closes: nothing races, each device closes once, and none is
// called after.
func TestWASAPIHeldWhileReopening(t *testing.T) {
	f := &fakeWas{rate: 48000, period: 480, buffer: 960, latency: 480, chans: 2, inputs: 1}
	useFakeWas(t, f)
	m := audio.NewMixer()
	s, err := Open(m, Options{Driver: driver.WASAPI})
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = s.held()
				_ = m.Heard()
				_ = s.Anchor()
				_ = s.Latency()
				_ = s.State()
			}
		}()
	}
	o := Options{Driver: driver.WASAPI}
	for i := range 20 {
		o.Latency = time.Duration(20+i) * time.Millisecond
		o.Record = i%2 == 0
		if err := s.Set(o); err != nil {
			t.Fatal(err)
		}
		if i%3 == 0 {
			dev, _ := f.dev(-1)
			dev.lost.Store(true)
		}
		if i%4 == 0 {
			_ = s.Suspend()
			_ = s.Resume()
		}
		time.Sleep(5 * time.Millisecond)
	}
	out := s.out.Load().output
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.close(); err != nil {
		t.Errorf("a second close = %v", err)
	}
	close(stop)
	wg.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, d := range f.devs {
		if c := d.closes.Load(); c != 1 {
			t.Errorf("device %d closed %d times", i, c)
		}
	}
	if f.misuse.Load() != 0 {
		t.Errorf("devices were called %d times after they closed", f.misuse.Load())
	}
}

// TestWASAPIFailsWhereItWillNotOpen opens WASAPI where it cannot:
// without WASAPI, with no device, and with Outputs naming none.
func TestWASAPIFailsWhereItWillNotOpen(t *testing.T) {
	f := &fakeWas{rate: 48000, period: 480, buffer: 960, latency: 480, chans: 2}
	f.failOpen.Store(1)
	useFakeWas(t, f)
	s := &Speaker{m: audio.NewMixer(), reset: make(chan struct{}, 1)}
	if _, err := openWasapi(s, Options{}, true); err == nil {
		t.Error("openWasapi opened a device the system refused")
	}
	if _, err := openWasapi(s, Options{Outputs: []int{}}, true); err == nil {
		t.Error("openWasapi opened with no outputs named")
	}
	openWasDevice = nil
	if _, err := openWasapi(s, Options{}, true); !errors.Is(err, wasapi.ErrUnsupported) {
		t.Errorf("without driver.WASAPI openWasapi returned %v", err)
	}
}

// TestSilencesPlaceTheClock logs runs of silence among the frames
// written, and counts those before a frame.
func TestSilencesPlaceTheClock(t *testing.T) {
	var s silences
	s.add(100, 10) // frames 100 to 109
	s.add(110, 5)  // goes on: 100 to 114
	s.add(200, 20) // 200 to 219
	for _, c := range []struct{ pos, want int64 }{
		{0, 0}, {100, 0}, {101, 1}, {114, 14}, {115, 15}, {150, 15}, {200, 15}, {210, 25}, {220, 35}, {1000, 35},
	} {
		if got := s.before(c.pos); got != c.want {
			t.Errorf("before(%d) = %d, want %d", c.pos, got, c.want)
		}
	}
	// Past the runs it keeps, the oldest kept counts all before it.
	var l silences
	for i := range int64(100) {
		l.add(i*10, 1)
	}
	if got := l.before(995); got != 100 {
		t.Errorf("before(995) = %d, want 100", got)
	}
	if got := l.before(681); got != 69 {
		t.Errorf("before(681) = %d, want 69", got)
	}
	if got := l.before(5); got != 68 {
		t.Errorf("before a run let go = %d, want the oldest kept's 68", got)
	}
}

// TestWASAPIWaitsForASlowDevice plays through a device whose clock
// first moves 2.5 s after it starts, as a Bluetooth headset waking up:
// the output waits for it, with no opening again, and hears it play.
func TestWASAPIWaitsForASlowDevice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeWas{rate: 48000, period: 480, buffer: 960, latency: 480, chans: 2, startAfter: 2500 * time.Millisecond}
		useFakeWas(t, f)
		m := audio.NewMixer()
		v := m.Play(&ramp{}, audio.Options{})
		s := openWas(t, m, Options{}, v)
		time.Sleep(3 * time.Second)
		synctest.Wait()
		dev, n := f.dev(-1)
		if n != 1 {
			t.Fatalf("%d devices opened, want 1", n)
		}
		hearsAsPlayed(t, m, dev, 2*time.Second)
		if p := s.State().Problem; p != nil {
			t.Errorf("the state tells %v", p)
		}
	})
}

// TestWASAPIBacksOffADeviceThatNeverPlays opens devices whose clock
// never moves: the output opens them again ever more slowly, and the
// state says so; once a device plays, the problem goes.
func TestWASAPIBacksOffADeviceThatNeverPlays(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeWas{rate: 48000, period: 480, buffer: 960, latency: 480, chans: 2}
		f.frozen.Store(true)
		useFakeWas(t, f)
		m := audio.NewMixer()
		v := m.Play(&ramp{}, audio.Options{})
		s := openWas(t, m, Options{}, v)
		time.Sleep(60 * time.Second)
		synctest.Wait()
		_, n := f.dev(-1)
		// Opened again at once after the first stall, then after 1, 2, 4
		// and 8 s: six in a minute, where with no backing off there
		// would be twelve, one each wasStartup.
		if n < 4 || n > 7 {
			t.Errorf("%d devices opened in a minute, want 4 to 7", n)
		}
		if p := s.State().Problem; p == nil {
			t.Error("the state tells no problem while nothing plays")
		}
		// The next device plays. (The ramp tops out at 1.0, a million
		// frames, so the test hears it soon.)
		f.frozen.Store(false)
		for range 300 {
			if _, now := f.dev(-1); now > n {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		dev, now := f.dev(-1)
		if now != n+1 {
			t.Fatalf("no device opened after the clock moved again: %d opened", now)
		}
		hearsAsPlayed(t, m, dev, time.Second)
		if p := s.State().Problem; p != nil {
			t.Errorf("playing again, the state tells %v", p)
		}
	})
}

// stalling is a ramp whose Read stands still once, for stall, at the
// first read at or after from: a mixer late to fill the ring, as a
// busy moment makes it.
type stalling struct {
	ramp
	from, stall atomic.Int64
}

func (r *stalling) Read(dst []float32) (int, error) {
	if from := r.from.Load(); from > 0 && time.Now().UnixNano() >= from {
		r.from.Store(0)
		time.Sleep(time.Duration(r.stall.Load()))
	}
	return r.ramp.Read(dst)
}

// stallAt has r stand still for d at the first read after wait.
func (r *stalling) stallAt(wait, d time.Duration) {
	r.stall.Store(int64(d))
	r.from.Store(time.Now().Add(wait).UnixNano())
}

// TestWASAPIGrowsNoLeadFromAnOpening plays as the MOTU M Series did on
// Windows: the default format changes to 44100 Hz, so the output opens
// again, on a device whose clock starts late and whose delay is 20 ms
// longer, and the mixer is late once, just as the clock starts. Those
// dry reads come from opening: they count as dropouts, but leave the
// lead as it was. Then the mixer is late once while the device plays
// steadily: the lead grows by 10 ms, not by the device's delay. Back at
// 48000 Hz, on a device of the first delay, the speaker works as far
// ahead as before, and the 10 ms more. On Windows the lead went from 38
// to 88 ms, and stayed there.
func TestWASAPIGrowsNoLeadFromAnOpening(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeWas{rate: 48000, period: 512, buffer: 1126, latency: 914, chans: 2}
		useFakeWas(t, f)
		m := audio.NewMixer()
		r := &stalling{}
		v := m.Play(r, audio.Options{})
		s := openWas(t, m, Options{}, v)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		before := s.Latency()

		// The device opens again at 44100 Hz, its clock first moving 300
		// ms after it starts, past wasSettle, with 20 ms more delay.
		f.startAfter = 300 * time.Millisecond
		f.nextRate.Store(44100)
		f.nextLatency.Store(914 + 882)
		dev, _ := f.dev(-1)
		dev.lost.Store(true)
		select {
		case <-s.Changed():
		case <-time.After(3 * time.Second):
			t.Fatal("the speaker never opened again for a device at another rate")
		}
		drops := s.State().Dropouts
		r.stallAt(310*time.Millisecond, 80*time.Millisecond)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if d := s.State().Dropouts - drops; d < 2 {
			t.Fatalf("the late mixer ran the device dry %d times as it opened, want 2 or more", d)
		}
		if g := s.margin.Load(); g != 0 {
			t.Fatalf("the dropouts of an opening grew the lead by %v", time.Duration(g))
		}
		if l := s.Latency(); l > before+25*time.Millisecond {
			t.Fatalf("after the opening the speaker works %v ahead, where it worked %v; the device delays 20 ms more", l, before)
		}

		// Late once, playing steadily: 10 ms more, or 20 where the dry
		// reads fall in two of the watch's looks.
		drops = s.State().Dropouts
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

		// Back at 48000 Hz, on a device of the first delay.
		f.nextRate.Store(48000)
		f.nextLatency.Store(914)
		dev, _ = f.dev(-1)
		dev.lost.Store(true)
		select {
		case <-s.Changed():
		case <-time.After(3 * time.Second):
			t.Fatal("the speaker never opened again for the device back at 48000 Hz")
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if l, want := s.Latency(), before+grown; l < want-2*time.Millisecond || l > want+2*time.Millisecond {
			t.Errorf("back at 48000 Hz the speaker works %v ahead, want %v: %v as before, and %v grown", l, want, before, grown)
		}
		dev, _ = f.dev(-1)
		hearsAsPlayed(t, m, dev, time.Second)
	})
}

// TestWASAPILogsItsStamps records with the debug log on: every
// wasNoteEvery it tells of the packet read soonest after its stamp,
// and the clock reading soonest after its own. The fake stamps a
// packet as its first frame comes in, and hands it on a period later,
// so the log tells it was read a period after its stamp.
func TestWASAPILogsItsStamps(t *testing.T) {
	t.Setenv("GUNIM_DEBUG_AUDIO", "1")
	var buf syncBuffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	synctest.Test(t, func(t *testing.T) {
		f := &fakeWas{rate: 48000, period: 480, buffer: 960, latency: 480, chans: 2, inputs: 1, startAfter: 50 * time.Millisecond}
		useFakeWas(t, f)
		m := audio.NewMixer()
		v := m.Play(&ramp{}, audio.Options{})
		s := openWas(t, m, Options{Record: true}, v)
		detach := duplex.Current().Attach(func([]float32, int, int64) {})
		defer detach()
		time.Sleep(wasNoteEvery + time.Second)
		synctest.Wait()
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	out := buf.String()
	for _, want := range []string{
		// 50 ms to start, then its latency, a period, before its clock
		// tells a frame heard, read at the next period's end.
		// Then it had taken a period, the fake's latency, past what it
		// tells, and holds its buffer full.
		"the device's clock was seen to move first 70ms after it started (to within a period), to frame 480; by then 1920 frames were written, " +
			"0 of them silence, and its buffer held 960, so the engine had taken 960, 480 past the frame the clock told",
		"WASAPI input: a packet of 480 frames at device position ",
		// A packet read a period after its stamp is stamped as its first
		// frame came in: nothing moves back.
		" read 10ms after its stamp, the soonest in 5s; the input's period is 480 frames (10ms); stamps moved back 0s",
		"WASAPI output: the clock read 0s after its stamp, the soonest in 5s, with 1440 frames written past the " +
			"position it told: 960 in its buffer, 480 taken by the engine; 0 frames of silence written",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the log tells no %q:\n%s", want, out)
		}
	}
}

// syncBuffer is a bytes.Buffer safe from any goroutine.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
