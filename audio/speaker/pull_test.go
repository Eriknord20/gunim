package speaker

import (
	"errors"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/asio"
	"github.com/marrasen/gunim/audio/internal/duplex"
)

// fakeDriver stands in for an ASIO driver: the test takes its buffers,
// as the driver's thread would.
type fakeDriver struct {
	mu     sync.Mutex
	info   asio.Info
	cfg    asio.Config
	opens  int
	closed bool
	// stuck has the device not stop as it closes, as a driver that
	// stopped answering.
	stuck bool
}

func (f *fakeDriver) open(c asio.Config) (pullDevice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfg, f.closed = c, false
	f.opens++
	info := f.info
	if c.Rate > 0 {
		info.Rate = c.Rate
	}
	return &fakeDevice{f: f, info: info}, nil
}

// take takes one buffer from the speaker, as the driver does.
func (f *fakeDriver) take() []float32 {
	f.mu.Lock()
	fill, record := f.cfg.Fill, f.cfg.Record
	n, ins := f.info.Buffer, f.info.Recorded
	f.mu.Unlock()
	if record != nil && ins > 0 {
		record(make([]float32, n*ins), ins)
	}
	frames := make([]float32, 2*n)
	fill(frames)
	return frames
}

type fakeDevice struct {
	f    *fakeDriver
	info asio.Info
}

func (d *fakeDevice) Info() asio.Info     { return d.info }
func (d *fakeDevice) ControlPanel() error { return nil }
func (d *fakeDevice) Close() error {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	if d.f.stuck {
		return asio.ErrNotAnswering
	}
	d.f.closed = true
	return nil
}

// useFake puts a fake driver in place of ASIO for the test.
func useFake(t *testing.T, info asio.Info) *fakeDriver {
	t.Helper()
	f := &fakeDriver{info: info}
	was := openPull
	openPull = f.open
	t.Cleanup(func() { openPull = was })
	return f
}

// ringOf returns the ring output s plays through.
func ringOf(t *testing.T, s *Speaker) *ringOutput {
	t.Helper()
	r, ok := s.out.Load().output.(*ringOutput)
	if !ok {
		t.Fatalf("the speaker plays through %T", s.out.Load().output)
	}
	return r
}

// waitFor waits for cond, or fails the test after a while.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waited in vain for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// steady is a source of one value, without end.
type steady float32

func (s steady) Read(dst []float32) (int, error) {
	for i := range dst {
		dst[i] = float32(s)
	}
	return len(dst) / 2, nil
}

// TestADriverPlaysTheMixerRounded opens a driver at 44.1 kHz with
// 16-bit samples, and reads the mixer's sound from its buffers, at the
// driver's rate and rounded to 16 bits.
func TestADriverPlaysTheMixerRounded(t *testing.T) {
	f := useFake(t, asio.Info{Driver: "Fake", Rate: 48000, Buffer: 256, Latency: 300, Bits: 32})
	m := audio.NewMixer()
	m.Play(steady(0.3), audio.Options{})
	s, err := Open(m, Options{Driver: "Fake", Rate: 44100, Bits: 16, Latency: 50 * time.Millisecond, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if m.Rate() != 44100 {
		t.Errorf("the mixer plays at %d Hz, want 44100", m.Rate())
	}
	st := s.State()
	if st.Driver != "Fake" || st.Rate != 44100 || st.ASIO == nil {
		t.Errorf("state %+v", st)
	}
	if want := 50 * time.Millisecond; st.Latency < want-time.Millisecond || st.Latency > want+time.Millisecond {
		t.Errorf("working %v ahead, want %v", st.Latency, want)
	}
	r := ringOf(t, s)
	waitFor(t, "the ring to fill", r.primed.Load)
	want := float32(math.Round(0.3*32768) / 32768)
	// The voice fades in over its first frames: read past them.
	f.take()
	got := f.take()
	for i, v := range got {
		if v != want {
			t.Fatalf("sample %d is %v, want %v", i, v, want)
		}
	}
}

// TestRunningDryWorksFurtherAhead takes buffers two at a time, faster
// than the ring fills, so it runs dry, and finds the speaker working
// further ahead; with Fixed set it stays where it is.
func TestRunningDryWorksFurtherAhead(t *testing.T) {
	for _, fixed := range []bool{false, true} {
		f := useFake(t, asio.Info{Driver: "Fake", Rate: 48000, Buffer: 4800, Latency: 0, Bits: 32})
		m := audio.NewMixer()
		s, err := Open(m, Options{Driver: "Fake", Latency: 20 * time.Millisecond, Fixed: fixed})
		if err != nil {
			t.Fatal(err)
		}
		r := ringOf(t, s)
		waitFor(t, "the ring to fill", r.primed.Load)
		start := s.Latency()
		for range 300 {
			f.take()
			f.take()
			time.Sleep(2 * time.Millisecond)
		}
		grew := s.Latency() > start
		if grew == fixed {
			t.Errorf("fixed %v: working %v ahead after running dry, from %v", fixed, s.Latency(), start)
		}
		if s.State().Dropouts == 0 {
			t.Errorf("fixed %v: no dropouts counted", fixed)
		}
		_ = s.Close()
	}
}

// TestADriverAskingForAResetOpensAgain has the driver ask to be opened
// again, and hears of it on Changed.
func TestADriverAskingForAResetOpensAgain(t *testing.T) {
	f := useFake(t, asio.Info{Driver: "Fake", Rate: 48000, Buffer: 256, Bits: 32})
	m := audio.NewMixer()
	s, err := Open(m, Options{Driver: "Fake"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f.mu.Lock()
	reset := f.cfg.Reset
	f.mu.Unlock()
	reset()
	select {
	case <-s.Changed():
	case <-time.After(3 * time.Second):
		t.Fatal("the speaker never opened again")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.opens != 2 {
		t.Errorf("the driver opened %d times, want 2", f.opens)
	}
}

// TestSetChangesTheRate sets another rate, and finds the driver opened
// again at it, and the mixer playing at it.
func TestSetChangesTheRate(t *testing.T) {
	f := useFake(t, asio.Info{Driver: "Fake", Rate: 48000, Buffer: 256, Bits: 32})
	m := audio.NewMixer()
	o := Options{Driver: "Fake", Rate: 48000}
	s, err := Open(m, o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	o.Rate = 96000
	if err := s.Set(o); err != nil {
		t.Fatal(err)
	}
	if m.Rate() != 96000 || s.State().Rate != 96000 {
		t.Errorf("the mixer plays at %d Hz and the speaker at %d, want 96000", m.Rate(), s.State().Rate)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.opens != 2 {
		t.Errorf("the driver opened %d times, want 2", f.opens)
	}
}

// TestADriverThatFailedOpensItsSettings has a driver fail to open, opens
// its settings, and finds the speaker trying it again after.
func TestADriverThatFailedOpensItsSettings(t *testing.T) {
	f := useFake(t, asio.Info{Driver: "Fake", Rate: 48000, Buffer: 256, Bits: 32})
	fail := true
	was := openPull
	openPull = func(c asio.Config) (pullDevice, error) {
		if fail {
			return nil, errors.New("the driver refused to start")
		}
		return f.open(c)
	}
	defer func() { openPull = was }()
	var panels []string
	wasPanel := driverPanel
	driverPanel = func(name string) error {
		panels = append(panels, name)
		fail = false
		return nil
	}
	defer func() { driverPanel = wasPanel }()
	if os.Getenv("GUNIM_SPEAKER") != "1" {
		t.Skip("the speaker falls back to the system's sound: set GUNIM_SPEAKER=1 to let it")
	}
	m := audio.NewMixer()
	s, err := Open(m, Options{Driver: "Fake"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if s.State().Problem == nil {
		t.Fatal("a driver that refused to start tells no problem")
	}
	if err := s.ControlPanel(); err != nil || len(panels) != 1 || panels[0] != "Fake" {
		t.Fatalf("the settings opened for %v: %v", panels, err)
	}
	select {
	case <-s.Changed():
	case <-time.After(3 * time.Second):
		t.Fatal("the speaker never tried the driver again")
	}
	if st := s.State(); st.Driver != "Fake" || st.Problem != nil {
		t.Errorf("after the settings, the speaker plays through %q, problem %v", st.Driver, st.Problem)
	}
}

// TestADriverThatStopsAnsweringIsLetGo has a driver that stops
// answering ask to be opened again: the speaker lets it go and opens
// the driver anew, where it would keep the one that does not close.
func TestADriverThatStopsAnsweringIsLetGo(t *testing.T) {
	f := useFake(t, asio.Info{Driver: "Fake", Rate: 48000, Buffer: 256, Latency: 300, Bits: 32})
	m := audio.NewMixer()
	s, err := Open(m, Options{Driver: "Fake", Latency: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	was := ringOf(t, s)
	f.mu.Lock()
	f.stuck = true
	reset := f.cfg.Reset
	f.mu.Unlock()
	reset()
	waitFor(t, "the driver opened anew", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.opens == 2
	})
	waitFor(t, "a new output", func() bool {
		out := s.out.Load()
		return out != nil && out.output != was
	})
}

// ramp is a source whose every frame is its own index, a millionth of
// it, as the mixer's frames are while it is the only one playing.
type ramp struct{ at int64 }

func (r *ramp) Read(dst []float32) (int, error) {
	for i := range len(dst) / 2 {
		v := float32(r.at) * 1e-6
		dst[2*i], dst[2*i+1] = v, v
		r.at++
	}
	return len(dst) / 2, nil
}

// TestADriverHandsItsInputsOnAsHeard records a driver's inputs, as
// package audio/capture does: before each buffer the driver plays, its
// inputs come stamped with the mixer frame heard as they came in, the
// buffer's first frame less the driver's output and input latencies.
// The tap goes as the speaker closes.
func TestADriverHandsItsInputsOnAsHeard(t *testing.T) {
	f := useFake(t, asio.Info{Driver: "Fake", Rate: 48000, Buffer: 256, Latency: 300, Bits: 32,
		Inputs: 4, Recorded: 2, InputNames: []string{"Mic", "Guitar"}, InputLatency: 200})
	m := audio.NewMixer()
	m.Play(&ramp{}, audio.Options{})
	s, err := Open(m, Options{Driver: "Fake", Latency: 50 * time.Millisecond, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	tap := duplex.Current()
	if tap == nil || tap.Driver != "Fake" || len(tap.Names) != 2 || tap.Latency != 200 {
		t.Fatalf("the tap is %+v", tap)
	}
	var heard []int64
	tap.Attach(func(in []float32, channels int, h int64) {
		if channels != 2 || len(in) != 2*256 {
			t.Errorf("the inputs came as %d samples of %d channels", len(in), channels)
		}
		heard = append(heard, h)
	})
	waitFor(t, "the ring to fill", ringOf(t, s).primed.Load)
	// The voice fades in over its first frames: read past them.
	f.take()
	for range 4 {
		out := f.take()
		first := int64(math.Round(float64(out[0]) * 1e6))
		if h := heard[len(heard)-1]; h != first-500 {
			t.Fatalf("the inputs were stamped %d, before a buffer starting at frame %d: want %d", h, first, first-500)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-tap.Gone():
	default:
		t.Fatal("the tap stays open after the speaker closed")
	}
	if duplex.Current() != nil {
		t.Fatal("the closed driver's tap is still current")
	}
}
