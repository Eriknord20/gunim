package speaker

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/driver"
	"github.com/marrasen/gunim/audio/internal/audiounit"
	"github.com/marrasen/gunim/audio/internal/duplex"
)

// fakeAU stands in for Core Audio's default devices and the client the
// speaker opens on them: the test takes their buffers, as the device's
// thread would, and places the frame heard, as its time stamps would.
type fakeAU struct {
	mu   sync.Mutex
	info audiounit.Info
	cfg  audiounit.Config
	// next is the info the next open takes, where set.
	next  *audiounit.Info
	opens int
	// q holds the frames written, of chans channels, not yet taken;
	// written counts them all.
	q       []float32
	chans   int
	size    int
	written int64
	// heard is the frame heard, as Heard tells it, where stamped.
	heard   int64
	stamped bool
	ev      audiounit.Events
	ins     []fakeIn
	paused  bool
	// suspends and resumes count the calls; closes counts Close's.
	suspends, resumes, closes int
	failed                    error
}

func useAU(t *testing.T, info audiounit.Info) *fakeAU {
	t.Helper()
	f := &fakeAU{info: info}
	was := openAU
	openAU = f.open
	t.Cleanup(func() { openAU = was })
	return f
}

func (f *fakeAU) open(c audiounit.Config) (auDevice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failed != nil {
		return nil, f.failed
	}
	if f.next != nil {
		f.info, f.next = *f.next, nil
	}
	f.cfg, f.opens = c, f.opens+1
	f.info.Played = jackLayout(c.Outputs, len(f.info.Outputs))
	if c.Record {
		f.info.Recorded = len(f.info.Inputs)
	} else {
		f.info.Recorded = 0
	}
	f.chans, f.size = len(f.info.Played), int(audio.FramesAt(c.Hold, f.info.Rate))+8192
	f.q, f.written, f.ins, f.ev, f.stamped = nil, 0, nil, audiounit.Events{}, false
	return fakeAUDevice{f}, nil
}

// take takes a buffer, as the device does, and returns what it played:
// the ring's frames, silence past them. The input records a buffer,
// stamped with the frame taken as it came in.
func (f *fakeAU) take() []float32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ch := f.info.Buffer, f.chans
	out := make([]float32, n*ch)
	at := f.written - int64(len(f.q)/ch)
	if !f.paused {
		k := min(n, len(f.q)/ch)
		copy(out, f.q[:k*ch])
		f.q = f.q[k*ch:]
		if k < n {
			f.ev.Short++
		}
	}
	if f.info.Recorded > 0 {
		f.ins = append(f.ins, fakeIn{make([]float32, n*f.info.Recorded), at})
	}
	f.ev.Cycles++
	return out
}

func (f *fakeAU) tell(e func(*audiounit.Events)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e(&f.ev)
}

type fakeAUDevice struct{ f *fakeAU }

func (d fakeAUDevice) Info() audiounit.Info {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return d.f.info
}

func (d fakeAUDevice) Rate() int { return d.Info().Rate }

func (d fakeAUDevice) Channels() int {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return d.f.chans
}

func (d fakeAUDevice) Buffer() int { return d.Info().Buffer }

func (d fakeAUDevice) Delay() int {
	i := d.Info()
	return i.Buffer + i.Safety + i.Latency
}

func (d fakeAUDevice) Write(frames []float32) int {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	ch := d.f.chans
	n := min(len(frames)/ch, d.f.size-len(d.f.q)/ch)
	d.f.q = append(d.f.q, frames[:n*ch]...)
	d.f.written += int64(n)
	return n
}

func (d fakeAUDevice) Written() int64 {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return d.f.written
}

func (d fakeAUDevice) Queued() int64 {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return int64(len(d.f.q) / d.f.chans)
}

func (d fakeAUDevice) Room() int64 {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return int64(d.f.size - len(d.f.q)/d.f.chans)
}

func (d fakeAUDevice) Heard() (int64, bool) {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return d.f.heard, d.f.stamped
}

func (d fakeAUDevice) Read(dst []float32) (n int, at int64, ok bool) {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	if len(d.f.ins) == 0 {
		return 0, 0, false
	}
	in := d.f.ins[0]
	d.f.ins = d.f.ins[1:]
	return copy(dst, in.frames) / d.f.info.Recorded, in.at, true
}

func (d fakeAUDevice) Poll() audiounit.Events {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	e := d.f.ev
	d.f.ev.Short, d.f.ev.Lost, d.f.ev.Failed, d.f.ev.Changed = 0, 0, 0, false
	return e
}

func (d fakeAUDevice) Pause(paused bool) {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	d.f.paused = paused
}

func (d fakeAUDevice) Suspend() error {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	d.f.paused = true
	d.f.suspends++
	return nil
}

func (d fakeAUDevice) Resume() error {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	d.f.paused = false
	d.f.resumes++
	return nil
}

func (d fakeAUDevice) Close() error {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	d.f.closes++
	return nil
}

// auOf returns the Core Audio output s plays through.
func auOf(t *testing.T, s *Speaker) *auOutput {
	t.Helper()
	u, ok := s.out.Load().output.(*auOutput)
	if !ok {
		t.Fatalf("the speaker plays through %T", s.out.Load().output)
	}
	return u
}

func macInfo() audiounit.Info {
	return audiounit.Info{Device: "MacBook Pro Speakers", Rate: 44100, Buffer: 128, MinBuffer: 15, MaxBuffer: 4096,
		Latency: 300, Safety: 40, Outputs: []string{"Left", "Right"},
		InputDevice: "MacBook Pro Microphone", Inputs: []string{"Mic 1", "Mic 2"}, InputLatency: 200,
		InputBuffer: 128}
}

// Core Audio plays the mixer at the device's rate, whatever rate was
// asked, with a buffer from the latency asked, and the state tells the
// device's buffer and latencies.
func TestCoreAudioPlaysTheMixer(t *testing.T) {
	f := useAU(t, macInfo())
	m := audio.NewMixer()
	m.Play(steady(0.25), audio.Options{})
	s, err := Open(m, Options{Driver: driver.CoreAudio, Rate: 48000, Latency: 30 * time.Millisecond, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if m.Rate() != 44100 {
		t.Errorf("the mixer plays at %d Hz, want the device's 44100", m.Rate())
	}
	// A quarter of 30 ms is 360 frames at 48 kHz: 256, a power of two.
	if f.cfg.Buffer != 256 || f.cfg.Hold != MaxLatency || f.cfg.Record {
		t.Errorf("opened with %+v", f.cfg)
	}
	st := s.State()
	if st.Driver != driver.CoreAudio || st.Info == nil || st.Info.Latency != 128+40+300 || st.Info.Buffer != 128 ||
		!slices.Equal(st.Info.Played, []int{0, 1}) || st.Info.Recorded != 0 || st.Problem != nil {
		t.Errorf("state %+v, info %+v", st, st.Info)
	}
	if want := 30 * time.Millisecond; st.Latency < want-time.Millisecond || st.Latency > want+time.Millisecond {
		t.Errorf("working %v ahead, want %v", st.Latency, want)
	}
	u := auOf(t, s)
	waitFor(t, "the ring to fill", u.primed.Load)
	f.take()
	got := f.take()
	for i, v := range got {
		if v != 0.25 {
			t.Fatalf("sample %d is %v, want 0.25", i, v)
		}
	}
	was := auControlPanel
	opened := false
	auControlPanel = func() error { opened = true; return nil }
	defer func() { auControlPanel = was }()
	if err := s.ControlPanel(); err != nil || !opened {
		t.Errorf("ControlPanel = %v, opened %v", err, opened)
	}
}

// Options.Buffer goes to the device as it is; zero asks for a quarter
// of the latency, a power of two from 64 to 1024 frames.
func TestCoreAudioBuffer(t *testing.T) {
	for _, c := range []struct {
		buffer int
		ahead  time.Duration
		want   int
	}{
		{0, 30 * time.Millisecond, 256}, {0, 5 * time.Millisecond, 64}, {0, time.Second, 1024},
		{0, 21334 * time.Microsecond, 256}, {96, 30 * time.Millisecond, 96},
	} {
		if got := auBuffer(Options{Buffer: c.buffer}, c.ahead, 48000); got != c.want {
			t.Errorf("buffer %d at %v: %d, want %d", c.buffer, c.ahead, got, c.want)
		}
	}
}

// The frames held are those written less the frame the device's time
// stamps place at the ear; before a stamp, the ring and the device's
// delay.
func TestCoreAudioHeldFollowsTheStamps(t *testing.T) {
	f := useAU(t, macInfo())
	s, err := Open(audio.NewMixer(), Options{Driver: driver.CoreAudio, Latency: 40 * time.Millisecond, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	u := auOf(t, s)
	waitFor(t, "the ring to fill", u.primed.Load)
	target := u.target()
	waitFor(t, "the ring at its target", func() bool { return u.dev.Queued() == target })
	if got, want := s.held(), target+128+40+300; got != want {
		t.Errorf("held %d before a stamp, want %d", got, want)
	}
	for _, at := range []int64{0, 100, 1000, 1_000_000} {
		f.mu.Lock()
		f.heard, f.stamped = at, true
		written := f.written
		f.mu.Unlock()
		if got, want := s.held(), max(0, written-at); got != want {
			t.Errorf("heard %d: held %d, want %d", at, got, want)
		}
	}
	if got, want := s.Latency(), audio.DurationAt(target+128+40+300, 44100); got != want {
		t.Errorf("latency %v, want %v", got, want)
	}
}

// held counts the frames the mixer mixed, not those in the ring: read
// after the pump mixed a chunk and before it wrote it to the ring, as
// Aux runs, it counts that chunk too. Counted from the ring, Heard would
// come out a chunk too far on there, and stick, as it never goes back.
func TestCoreAudioHeldCountsTheMixer(t *testing.T) {
	info := macInfo()
	info.Outputs = []string{"1", "2", "3"}
	f := useAU(t, info)
	m := audio.NewMixer()
	var mu sync.Mutex
	var bad []string
	var opened atomic.Pointer[Speaker]
	var checks int
	aux := func(dst []float32, _ int, _ int64) {
		s := opened.Load()
		if s == nil {
			return
		}
		// Between opens the speaker holds no output.
		out := s.out.Load()
		if out == nil {
			return
		}
		u, ok := out.output.(*auOutput)
		if !ok {
			return
		}
		// The fake moves nothing while Aux runs: the pump alone writes.
		mixed := m.Mixed() - u.base.Load()
		want := mixed - 50
		f.mu.Lock()
		if !f.stamped {
			want = mixed - (f.written - int64(len(f.q)/f.chans)) + 128 + 40 + 300
		}
		f.mu.Unlock()
		got := s.held()
		mu.Lock()
		defer mu.Unlock()
		checks++
		if got != want {
			bad = append(bad, fmt.Sprintf("held %d mid-feed, want the %d mixed", got, want))
		}
	}
	s, err := Open(m, Options{Driver: driver.CoreAudio, Outputs: []int{0, 1, 2}, Aux: aux, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	opened.Store(s)
	defer func() { _ = s.Close() }()
	waitFor(t, "the ring to fill", auOf(t, s).primed.Load)
	for k := range 10 {
		if k == 5 {
			f.mu.Lock()
			f.heard, f.stamped = 50, true
			f.mu.Unlock()
		}
		f.take()
		time.Sleep(3 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if checks == 0 {
		t.Fatal("Aux never ran mid-feed")
	}
	if len(bad) > 0 {
		t.Fatalf("%d of %d reads: %s", len(bad), checks, bad[0])
	}
}

// Buffers the device finds short, once the ring has filled, count as
// dropouts.
func TestCoreAudioRunningDry(t *testing.T) {
	f := useAU(t, macInfo())
	s, err := Open(audio.NewMixer(), Options{Driver: driver.CoreAudio, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	waitFor(t, "the ring to fill", auOf(t, s).primed.Load)
	f.tell(func(e *audiounit.Events) { e.Short = 2 })
	waitFor(t, "the dropouts counted", func() bool { return s.State().Dropouts == 2 })
}

// A device changed, gone, or at another rate, opens the output again,
// once for each change: at the new rate, the mixer with it.
func TestCoreAudioReopensOnChange(t *testing.T) {
	f := useAU(t, macInfo())
	m := audio.NewMixer()
	s, err := Open(m, Options{Driver: driver.CoreAudio})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f.mu.Lock()
	next := macInfo()
	next.Device, next.Rate = "AirPods", 48000
	f.next = &next
	f.mu.Unlock()
	f.tell(func(e *audiounit.Events) { e.Reopen = "the default output device changed" })
	select {
	case <-s.Changed():
	case <-time.After(3 * time.Second):
		t.Fatal("the speaker never opened again")
	}
	f.mu.Lock()
	opens := f.opens
	f.mu.Unlock()
	if opens != 2 || m.Rate() != 48000 || s.State().Rate != 48000 {
		t.Errorf("opened %d times, mixer at %d Hz, want 2 and 48000", opens, m.Rate())
	}
	// A latency read again reaches the state with no opening again.
	f.mu.Lock()
	f.info.Latency = 4000
	f.mu.Unlock()
	f.tell(func(e *audiounit.Events) { e.Changed = true })
	waitFor(t, "the new latency", func() bool { return s.State().Info.Latency == 128+40+4000 })
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.opens != 2 {
		t.Errorf("opened %d times, want 2", f.opens)
	}
}

// The input reaches package audio/capture stamped with the mixer frame
// heard as it came in: the frame the device's stamps place, from the
// mixer frame the ring started at.
func TestCoreAudioRecordsStamped(t *testing.T) {
	f := useAU(t, macInfo())
	m := audio.NewMixer()
	s, err := Open(m, Options{Driver: driver.CoreAudio, Record: true, Latency: 30 * time.Millisecond, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	tap := duplex.Current()
	if tap == nil || tap.Driver != driver.CoreAudio || len(tap.Names) != 2 || tap.Latency != 200 || tap.Rate != 44100 {
		t.Fatalf("tap %+v", tap)
	}
	u := auOf(t, s)
	waitFor(t, "the ring to fill", u.primed.Load)
	var mu sync.Mutex
	var heard []int64
	detach := tap.Attach(func(in []float32, channels int, h int64) {
		mu.Lock()
		defer mu.Unlock()
		if channels == 2 && len(in) == 2*128 {
			heard = append(heard, h)
		}
	})
	defer detach()
	for range 3 {
		f.take()
	}
	waitFor(t, "three buffers recorded", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(heard) == 3
	})
	base := u.base.Load()
	for i, h := range heard {
		if want := base + int64(128*i); h != want {
			t.Errorf("buffer %d stamped %d, want %d", i, h, want)
		}
	}
	_ = s.Close()
	select {
	case <-tap.Gone():
	default:
		t.Error("the tap stayed open after the speaker closed")
	}
}

// Where the input records nothing, the speaker plays all the same, and
// its state says why.
func TestCoreAudioInputProblem(t *testing.T) {
	info := macInfo()
	info.InputProblem = audiounit.ErrDenied
	f := useAU(t, info)
	s, err := Open(audio.NewMixer(), Options{Driver: driver.CoreAudio, Record: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f.mu.Lock()
	f.info.Recorded = 0
	f.mu.Unlock()
	if st := s.State(); st.Driver != driver.CoreAudio || !errors.Is(st.Problem, audiounit.ErrDenied) {
		t.Errorf("state %+v", st)
	}
}

// Outputs past the mixer's pair play what Aux fills; one output plays
// the pair averaged.
func TestCoreAudioAuxAndMono(t *testing.T) {
	info := macInfo()
	info.Outputs = []string{"1", "2", "3", "4"}
	f := useAU(t, info)
	m := audio.NewMixer()
	m.Play(steady(0.5), audio.Options{})
	aux := func(dst []float32, channels int, _ int64) {
		for i := range dst {
			dst[i] = float32(1 + i%channels)
		}
	}
	s, err := Open(m, Options{Driver: driver.CoreAudio, Outputs: []int{0, 1, 2, 3}, Aux: aux})
	if err != nil {
		t.Fatal(err)
	}
	if s.State().Aux != 2 || !slices.Equal(f.cfg.Outputs, []int{0, 1, 2, 3}) {
		t.Errorf("aux %d, outputs %v", s.State().Aux, f.cfg.Outputs)
	}
	waitFor(t, "the ring to fill", auOf(t, s).primed.Load)
	f.take()
	got := f.take()
	if got[0] != 0.5 || got[1] != 0.5 || got[2] != 1 || got[3] != 2 {
		t.Errorf("a frame played %v, want [0.5 0.5 1 2]", got[:4])
	}
	_ = s.Close()

	info.Outputs = []string{"mono"}
	f = useAU(t, info)
	m = audio.NewMixer()
	m.Play(steady(0.5), audio.Options{})
	s, err = Open(m, Options{Driver: driver.CoreAudio})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	waitFor(t, "the ring to fill", auOf(t, s).primed.Load)
	f.take()
	got = f.take()
	if len(got) != 128 || got[0] != 0.5 {
		t.Errorf("mono played %d samples, first %v", len(got), got[0])
	}
}

// Suspend stops the device and Resume starts it; the output closes the
// device once, however often it is closed.
func TestCoreAudioSuspendAndClose(t *testing.T) {
	f := useAU(t, macInfo())
	s, err := Open(audio.NewMixer(), Options{Driver: driver.CoreAudio})
	if err != nil {
		t.Fatal(err)
	}
	u := auOf(t, s)
	if err := s.Suspend(); err != nil {
		t.Fatal(err)
	}
	if err := s.Resume(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	suspends, resumes := f.suspends, f.resumes
	f.mu.Unlock()
	if suspends != 1 || resumes != 1 {
		t.Errorf("suspended %d times, resumed %d, want 1 each", suspends, resumes)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := u.close(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closes != 1 {
		t.Errorf("closed %d times, want 1", f.closes)
	}
}

// Where Core Audio fails to open, the speaker plays through the
// system's own sound, and says why.
func TestCoreAudioFailsOver(t *testing.T) {
	f := useAU(t, macInfo())
	f.failed = audiounit.ErrNoOutput
	_, err := openAUOutput(&Speaker{}, Options{Driver: driver.CoreAudio}, true)
	if !errors.Is(err, audiounit.ErrNoOutput) {
		t.Fatalf("openAUOutput = %v", err)
	}
}

// On macOS, with no driver named, the speaker plays through Core Audio
// as the system's own sound: State names no driver but tells Core
// Audio's Info, and a change of Record, Outputs or Buffer opens it
// again, as it does a driver.
func TestCoreAudioIsTheDefault(t *testing.T) {
	coreAudioFirstOnly(t)
	f := useAU(t, macInfo())
	s, err := Open(audio.NewMixer(), Options{Record: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f.mu.Lock()
	opens, rec := f.opens, f.cfg.Record
	f.mu.Unlock()
	if opens != 1 || !rec {
		t.Fatalf("opened Core Audio %d times, recording %v", opens, rec)
	}
	st := s.State()
	if st.Driver != "" || st.Info == nil || st.Info.Driver != driver.CoreAudio || st.Info.Inputs != 2 ||
		st.Problem != nil {
		t.Fatalf("state %+v, info %+v", st, st.Info)
	}
	for i, o := range []Options{{}, {Outputs: []int{1, 0}}, {Outputs: []int{1, 0}, Buffer: 512}} {
		if err := s.Set(o); err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		opens, cfg := f.opens, f.cfg
		f.mu.Unlock()
		if opens != 2+i || cfg.Record != o.Record || !slices.Equal(cfg.Outputs, o.Outputs) ||
			(o.Buffer != 0 && cfg.Buffer != o.Buffer) {
			t.Fatalf("after Set(%+v): opened %d times, with %+v", o, opens, cfg)
		}
	}
}

// As the system's own sound, Core Audio is asked for the rate Options
// sets, as the older output set the device to it; named as a driver, it
// follows the device. Set reopens for a rate only where the output
// plays another.
func TestCoreAudioDefaultAsksTheRate(t *testing.T) {
	coreAudioFirstOnly(t)
	f := useAU(t, macInfo()) // plays at 44100 Hz, whatever it is asked
	s, err := Open(audio.NewMixer(), Options{Rate: 96000})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	opened := func() (int, int) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.opens, f.cfg.Rate
	}
	if n, rate := opened(); n != 1 || rate != 96000 {
		t.Fatalf("opened %d times, asked %d Hz, want once at 96000", n, rate)
	}
	// The device plays at 44100 Hz: asking that changes nothing.
	if err := s.Set(Options{Rate: 44100}); err != nil {
		t.Fatal(err)
	}
	if n, _ := opened(); n != 1 {
		t.Fatalf("asking the rate playing opened it again: %d opens", n)
	}
	if err := s.Set(Options{Rate: 48000}); err != nil {
		t.Fatal(err)
	}
	if n, rate := opened(); n != 2 || rate != 48000 {
		t.Fatalf("asking another rate: %d opens, asked %d Hz", n, rate)
	}
	// Named, Core Audio follows the device.
	if err := s.Set(Options{Driver: driver.CoreAudio, Rate: 48000}); err != nil {
		t.Fatal(err)
	}
	if n, rate := opened(); n != 3 || rate != 0 {
		t.Fatalf("named as a driver: %d opens, asked %d Hz", n, rate)
	}
}

// As the system's own sound on Windows and Android, which take neither
// Buffer nor Direct, a change of either opens nothing again.
func TestSetReopensForWhatTheDriverUses(t *testing.T) {
	noBackend(t)
	called := 0
	wasOto := openSystemSound
	t.Cleanup(func() { openSystemSound = wasOto })
	f := useAU(t, macInfo())
	openSystemSound = func(s *Speaker, o Options) (startedOutput, error) {
		called++
		return openAUOutput(s, Options{Driver: driver.CoreAudio}, true)
	}
	s, err := Open(audio.NewMixer(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.Set(Options{Buffer: 256, Direct: true}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	opens := f.opens
	f.mu.Unlock()
	if called != 1 || opens != 1 {
		t.Fatalf("Buffer and Direct opened the system's sound again: %d, %d", called, opens)
	}
}

// TestCoreAudioGrowsNoLeadFromAnOpening plays on a Mac's speakers, then
// on AirPods, whose latency is 8000 frames more. The AirPods find
// buffers short as they start: those count as dropouts, and leave the
// lead as it was. Then they find buffers short while they play
// steadily: the lead grows by 10 ms, not by the AirPods' delay. Back on
// the speakers, the speaker works as far ahead as before, and the
// 10 ms more.
func TestCoreAudioGrowsNoLeadFromAnOpening(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := useAU(t, macInfo())
		s, err := Open(audio.NewMixer(), Options{Driver: driver.CoreAudio})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		reopen := func(info audiounit.Info) {
			t.Helper()
			f.mu.Lock()
			f.next = &info
			f.mu.Unlock()
			f.tell(func(e *audiounit.Events) { e.Reopen = "the default output device changed" })
			select {
			case <-s.Changed():
			case <-time.After(3 * time.Second):
				t.Fatal("the speaker never opened again")
			}
			waitFor(t, "the ring to fill", auOf(t, s).primed.Load)
		}
		waitFor(t, "the ring to fill", auOf(t, s).primed.Load)
		before := s.Latency()

		airPods := macInfo()
		airPods.Device, airPods.Rate, airPods.Latency = "AirPods", 48000, 8300
		reopen(airPods)
		drops := s.State().Dropouts
		f.take()
		f.tell(func(e *audiounit.Events) { e.Short = 3 })
		time.Sleep(time.Second)
		if d := s.State().Dropouts - drops; d != 3 {
			t.Errorf("the AirPods starting ran dry %d times, want 3", d)
		}
		if g := s.margin.Load(); g != 0 || s.ahead.Load() != int64(30*time.Millisecond) {
			t.Fatalf("the AirPods starting grew the lead by %v, and set it to %v",
				time.Duration(g), time.Duration(s.ahead.Load()))
		}

		// Short while playing steadily: 10 ms more.
		f.take()
		f.tell(func(e *audiounit.Events) { e.Short = 3 })
		time.Sleep(time.Second)
		if g := time.Duration(s.margin.Load()); g != 10*time.Millisecond {
			t.Fatalf("the dry spell grew the lead by %v, want 10 ms", g)
		}

		reopen(macInfo())
		if l, want := s.Latency(), before+10*time.Millisecond; l < want-time.Millisecond || l > want+time.Millisecond {
			t.Errorf("back on the speakers the speaker works %v ahead, want %v: %v as before, and 10 ms grown", l, want, before)
		}
	})
}

// coreAudioFirstOnly makes Core Audio the default for t, as on macOS,
// with the other systems' defaults off.
func coreAudioFirstOnly(t *testing.T) {
	wasCA, wasAA, wasWA := coreAudioFirst, aaudioFirst, wasapiFirst
	coreAudioFirst, aaudioFirst, wasapiFirst = true, false, false
	t.Cleanup(func() { coreAudioFirst, aaudioFirst, wasapiFirst = wasCA, wasAA, wasWA })
}
