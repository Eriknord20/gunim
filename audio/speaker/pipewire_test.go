package speaker

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/driver"
	"github.com/marrasen/gunim/audio/internal/duplex"
	"github.com/marrasen/gunim/audio/internal/pipewire"
)

// fakePipeWire stands in for a PipeWire server and the client the
// speaker opens on it: the test runs its cycles, as PipeWire's thread
// would.
type fakePipeWire struct {
	mu    sync.Mutex
	cfg   pipewire.Config
	opens int
	// q holds the frames written, interleaved pairs, not yet taken.
	q      []float32
	taken  int64
	paused bool
	ev     pipewire.Events
	// delay is the frames past the ring that the client counts held, as
	// PipeWire's time report places them, and nextDelay the delay the
	// next client opened reports, where set.
	delay, nextDelay int
	// ins are what the input recorded, cycle by cycle, not yet read.
	ins    []fakeIn
	closed bool
	failed error
	closes int
}

func usePipeWire(t *testing.T) *fakePipeWire {
	t.Helper()
	f := &fakePipeWire{delay: 480}
	was := openPipeWire
	openPipeWire = f.open
	t.Cleanup(func() { openPipeWire = was })
	return f
}

func (f *fakePipeWire) open(c pipewire.Config) (pwDevice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failed != nil {
		return nil, f.failed
	}
	if f.nextDelay != 0 {
		f.delay, f.nextDelay = f.nextDelay, 0
	}
	f.cfg, f.opens, f.closed = c, f.opens+1, false
	f.q, f.taken, f.ins = nil, 0, nil
	in := pipewire.StateUnconnected
	if c.Record {
		in = pipewire.StateStreaming
	}
	f.ev = pipewire.Events{Out: pipewire.StateStreaming, In: in, Moved: true}
	return fakePWDevice{f}, nil
}

// cycle runs a cycle of n frames, as PipeWire does, and returns what it
// played: the ring's frames, silence past them. The input records a
// cycle too, stamped with the frame taken first less the delay.
func (f *fakePipeWire) cycle(n int) []float32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]float32, 2*n)
	at := f.taken - int64(f.delay)
	if !f.paused {
		k := min(n, len(f.q)/2)
		copy(out, f.q[:2*k])
		f.q = f.q[2*k:]
		f.taken += int64(k)
		if k < n {
			f.ev.Short++
		}
	}
	if f.cfg.Record {
		f.ins = append(f.ins, fakeIn{make([]float32, 2*n), at})
	}
	f.ev.Cycles++
	f.ev.Quantum, f.ev.Delay, f.ev.DeviceRate = n, f.delay, 48000
	return out
}

// tell has the server tell e, as its callbacks would.
func (f *fakePipeWire) tell(e func(*pipewire.Events)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e(&f.ev)
}

type fakePWDevice struct{ f *fakePipeWire }

func (d fakePWDevice) Info() pipewire.Info {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	c := d.f.cfg
	return pipewire.Info{Library: "fake", Rate: c.Rate, Quantum: c.Quantum, Recording: c.Record,
		OutChannels: max(len(c.OutPositions), 2), InChannels: max(len(c.InPositions), 2)}
}

func (d fakePWDevice) Write(frames []float32) int {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	n := min(len(frames)/2, 1<<16-len(d.f.q)/2)
	d.f.q = append(d.f.q, frames[:2*n]...)
	return n
}

func (d fakePWDevice) Queued() int64 {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return int64(len(d.f.q) / 2)
}

func (d fakePWDevice) Room() int64 { return 1<<16 - d.Queued() }

func (d fakePWDevice) Held() int64 {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return int64(len(d.f.q)/2 + d.f.delay)
}

func (d fakePWDevice) Read(dst []float32) (n int, at int64, ok bool) {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	if len(d.f.ins) == 0 {
		return 0, 0, false
	}
	in := d.f.ins[0]
	d.f.ins = d.f.ins[1:]
	return copy(dst, in.frames) / 2, in.at, true
}

func (d fakePWDevice) Poll() pipewire.Events {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	e := d.f.ev
	d.f.ev.Short, d.f.ev.Lost, d.f.ev.Empty, d.f.ev.Moved = 0, 0, 0, false
	return e
}

func (d fakePWDevice) Pause(paused bool) {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	d.f.paused = paused
}

func (d fakePWDevice) Err() error {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	if d.f.ev.Out == pipewire.StateError {
		return pipewire.ErrStopped
	}
	return nil
}

func (d fakePWDevice) Close() error {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	d.f.closed = true
	d.f.closes++
	return nil
}

// pwOf returns the PipeWire output s plays through.
func pwOf(t *testing.T, s *Speaker) *pwOutput {
	t.Helper()
	p, ok := s.out.Load().output.(*pwOutput)
	if !ok {
		t.Fatalf("the speaker plays through %T", s.out.Load().output)
	}
	return p
}

// PipeWire plays the mixer at the rate asked, and asks for a quarter of
// the latency a cycle; the state tells the cycle and the delay.
func TestPipeWirePlaysTheMixer(t *testing.T) {
	f := usePipeWire(t)
	m := audio.NewMixer()
	m.Play(steady(0.25), audio.Options{})
	s, err := Open(m, Options{Name: "My app", Driver: driver.PipeWire, Rate: 44100, Latency: 50 * time.Millisecond, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if m.Rate() != 44100 {
		t.Errorf("the mixer plays at %d Hz, want 44100", m.Rate())
	}
	if f.cfg.Name != "My app" || f.cfg.Hold != MaxLatency || f.cfg.Rate != 44100 || f.cfg.Quantum != 512 || f.cfg.Record {
		t.Errorf("opened with %+v", f.cfg)
	}
	p := pwOf(t, s)
	waitFor(t, "the ring to fill", p.primed.Load)
	// The voice fades in over its first frames: read past them.
	f.cycle(512)
	got := f.cycle(512)
	for i, v := range got {
		if v != 0.25 {
			t.Fatalf("sample %d is %v, want 0.25", i, v)
		}
	}
	waitFor(t, "the cycle polled", func() bool { return s.State().Info.Latency == 512+480 })
	st := s.State()
	if st.Driver != driver.PipeWire || st.Info.Buffer != 512 || st.DeviceRate != 48000 ||
		!slices.Equal(st.Info.Played, []int{0, 1}) || st.Info.Recorded != 0 {
		t.Errorf("state %+v, info %+v", st, st.Info)
	}
	if want := 50 * time.Millisecond; st.Latency < want-time.Millisecond || st.Latency > want+time.Millisecond {
		t.Errorf("working %v ahead, want %v", st.Latency, want)
	}
	if !errors.Is(s.ControlPanel(), errPWPanel) {
		t.Errorf("ControlPanel = %v", s.ControlPanel())
	}
	// Buffer names the cycle.
	if err := s.Set(Options{Driver: driver.PipeWire, Buffer: 128}); err != nil {
		t.Fatal(err)
	}
	if f.cfg.Quantum != 128 || f.opens != 2 {
		t.Errorf("Buffer 128 opened %d times, with a quantum of %d", f.opens, f.cfg.Quantum)
	}
}

// held is what the client counts, and the latency the ring's target, a
// cycle and the delay; a longer delay, as a Bluetooth headset's, shows
// in both at once and leaves the ring as short as it may be.
func TestPipeWireHeldAndLatencyFollowTheDelay(t *testing.T) {
	f := usePipeWire(t)
	s, err := Open(audio.NewMixer(), Options{Driver: driver.PipeWire, Latency: 40 * time.Millisecond, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	p := pwOf(t, s)
	waitFor(t, "the ring to fill", p.primed.Load)
	f.cycle(256)
	waitFor(t, "the cycle polled", func() bool { return p.delay.Load() == 480 })
	target := p.target()
	waitFor(t, "the ring at its target", func() bool {
		if p.dev.Queued() > target {
			// Filled before the delay was known: cycles take the extra.
			f.cycle(256)
		}
		return p.dev.Queued() == target
	})
	if got := s.held(); got != target+480 {
		t.Errorf("held %d, want %d", got, target+480)
	}
	if got, want := s.Latency(), audio.DurationAt(target+256+480, 48000); got != want {
		t.Errorf("latency %v, want %v", got, want)
	}
	f.mu.Lock()
	f.delay = 9600
	f.mu.Unlock()
	f.cycle(256)
	waitFor(t, "the new delay", func() bool { return s.State().Info.Latency == 256+9600 })
	if got, want := p.target(), int64(2*256); got != want {
		t.Errorf("target %d on Bluetooth, want the floor %d", got, want)
	}
	if got, want := s.Latency(), audio.DurationAt(p.target()+256+9600, 48000); got != want {
		t.Errorf("latency %v on Bluetooth, want %v", got, want)
	}
}

// The input reaches package audio/capture stamped with the mixer frame
// heard as it came in, as the client places it.
func TestPipeWireRecordsStamped(t *testing.T) {
	f := usePipeWire(t)
	s, err := Open(audio.NewMixer(), Options{Driver: driver.PipeWire, Record: true, Latency: 30 * time.Millisecond, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	tap := duplex.Current()
	if tap == nil || tap.Driver != driver.PipeWire || len(tap.Names) != 2 || tap.Rate != 48000 {
		t.Fatalf("tap %+v", tap)
	}
	if st := s.State(); st.Info.Recorded != 2 || len(st.Info.InputNames) != 2 {
		t.Errorf("info %+v", st.Info)
	}
	p := pwOf(t, s)
	waitFor(t, "the ring to fill", p.primed.Load)
	var mu sync.Mutex
	var heard []int64
	detach := tap.Attach(func(in []float32, channels int, h int64) {
		mu.Lock()
		defer mu.Unlock()
		if channels == 2 && len(in) == 2*256 {
			heard = append(heard, h)
		}
	})
	defer detach()
	for range 3 {
		f.cycle(256)
	}
	waitFor(t, "three cycles recorded", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(heard) == 3
	})
	base := p.base.Load()
	for i, h := range heard {
		if want := base + int64(256*i) - 480; h != want {
			t.Errorf("cycle %d stamped %d, want %d", i, h, want)
		}
	}
	_ = s.Close()
	select {
	case <-tap.Gone():
	default:
		t.Error("the tap stayed open after the speaker closed")
	}
}

// A stream that fails, as the server going away, or one left
// unconnected, opens the output again.
func TestPipeWireStreamFailingOpensAgain(t *testing.T) {
	f := usePipeWire(t)
	s, err := Open(audio.NewMixer(), Options{Driver: driver.PipeWire, Record: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for i, state := range []func(*pipewire.Events){
		func(e *pipewire.Events) { e.Out, e.Error, e.Moved = pipewire.StateError, "connection closed", true },
		func(e *pipewire.Events) {
			e.In, e.Error, e.Moved = pipewire.StateError, "no target node available", true
		},
		func(e *pipewire.Events) { e.Out, e.Moved = pipewire.StateUnconnected, true },
	} {
		f.tell(state)
		select {
		case <-s.Changed():
		case <-time.After(3 * time.Second):
			t.Fatalf("change %d: the speaker never opened again", i)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.opens != 4 || f.closes != 3 {
		t.Errorf("opened %d times, closed %d, want 4 and 3", f.opens, f.closes)
	}
}

// A server that fails to open plays through the system's sound, and the
// state says why.
func TestPipeWireFailingFallsBack(t *testing.T) {
	noBackend(t)
	f := usePipeWire(t)
	f.failed = errors.New("pipewire: no PipeWire server is running")
	was := openSystemSound
	openSystemSound = func(_ *Speaker, o Options) (startedOutput, error) { return &stillOut{hz: o.rate()}, nil }
	t.Cleanup(func() { openSystemSound = was })
	s, err := Open(audio.NewMixer(), Options{Driver: driver.PipeWire})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if st := s.State(); st.Driver != "" || !errors.Is(st.Problem, f.failed) {
		t.Errorf("state %+v", st)
	}
}

// Suspended, PipeWire plays silence and takes nothing from the ring.
func TestPipeWireSuspendPauses(t *testing.T) {
	f := usePipeWire(t)
	s, err := Open(audio.NewMixer(), Options{Driver: driver.PipeWire})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	waitFor(t, "the ring to fill", pwOf(t, s).primed.Load)
	if err := s.Suspend(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	paused := f.paused
	f.mu.Unlock()
	if !paused {
		t.Fatal("suspended, PipeWire plays on")
	}
	if err := s.Resume(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	paused = f.paused
	f.mu.Unlock()
	if paused {
		t.Fatal("resumed, PipeWire stays paused")
	}
}

// held, Heard and Anchor read the output from other goroutines, as the
// UI does each frame, while the output opens again and closes: nothing
// races.
func TestPipeWireHeldWhileReopening(t *testing.T) {
	usePipeWire(t)
	m := audio.NewMixer()
	s, err := Open(m, Options{Driver: driver.PipeWire})
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
	o := Options{Driver: driver.PipeWire}
	for i := range 30 {
		o.Latency = time.Duration(20+i) * time.Millisecond
		o.Record = i%2 == 0
		if err := s.Set(o); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()
}

// TestPipeWireGrowsNoLeadFromAnOpening plays through PipeWire as the
// session manager moves the stream to a Bluetooth headset, 9600 frames
// of delay, and the stream pauses and streams again: cycles short as it
// starts there count as dropouts, and leave the lead as it was. The
// server then goes away and comes back, the stream opened again on the
// headset: the same. Then cycles run short while it plays steadily:
// the lead grows by 10 ms, not by the headset's delay. Moved back to
// the wired output, the speaker works as far ahead as before, and the
// 10 ms more.
func TestPipeWireGrowsNoLeadFromAnOpening(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := usePipeWire(t)
		s, err := Open(audio.NewMixer(), Options{Driver: driver.PipeWire})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		waitFor(t, "the ring to fill", pwOf(t, s).primed.Load)
		f.cycle(256)
		time.Sleep(100 * time.Millisecond)
		before := s.Latency()
		short := func(n int64) {
			f.cycle(256)
			f.tell(func(e *pipewire.Events) { e.Short += n })
			time.Sleep(time.Second)
		}

		// The move: paused, then streaming on the headset, short as it
		// starts.
		f.tell(func(e *pipewire.Events) { e.Out, e.Moved = pipewire.StatePaused, true })
		time.Sleep(10 * time.Millisecond)
		f.mu.Lock()
		f.delay = 9600
		f.mu.Unlock()
		f.tell(func(e *pipewire.Events) { e.Out, e.Moved = pipewire.StateStreaming, true })
		time.Sleep(10 * time.Millisecond)
		drops := s.State().Dropouts
		short(3)
		if d := s.State().Dropouts - drops; d != 3 {
			t.Errorf("the move ran dry %d times, want 3", d)
		}
		if g := s.margin.Load(); g != 0 || s.ahead.Load() != int64(30*time.Millisecond) {
			t.Fatalf("the move grew the lead by %v, and set it to %v", time.Duration(g), time.Duration(s.ahead.Load()))
		}

		// The server goes and comes back, on the headset still.
		f.mu.Lock()
		f.nextDelay = 9600
		f.mu.Unlock()
		f.tell(func(e *pipewire.Events) { e.Out, e.Error, e.Moved = pipewire.StateError, "connection closed", true })
		select {
		case <-s.Changed():
		case <-time.After(3 * time.Second):
			t.Fatal("the speaker never opened again after the stream failed")
		}
		waitFor(t, "the ring to fill", pwOf(t, s).primed.Load)
		drops = s.State().Dropouts
		short(3)
		if d := s.State().Dropouts - drops; d != 3 {
			t.Errorf("the opening ran dry %d times, want 3", d)
		}
		if g := s.margin.Load(); g != 0 || s.ahead.Load() != int64(30*time.Millisecond) {
			t.Fatalf("the opening grew the lead by %v, and set it to %v", time.Duration(g), time.Duration(s.ahead.Load()))
		}

		// Short while playing steadily: 10 ms more.
		short(3)
		if g := time.Duration(s.margin.Load()); g != 10*time.Millisecond {
			t.Fatalf("the dry spell grew the lead by %v, want 10 ms", g)
		}

		// Back to the wired output.
		f.mu.Lock()
		f.delay = 480
		f.mu.Unlock()
		f.cycle(256)
		time.Sleep(100 * time.Millisecond)
		if l, want := s.Latency(), before+10*time.Millisecond; l < want-time.Millisecond || l > want+time.Millisecond {
			t.Errorf("back on the wired output the speaker works %v ahead, want %v: %v as before, and 10 ms grown", l, want, before)
		}
	})
}

// The tests of the other outputs name none, and must not reach the
// computer's own PipeWire.
func init() { pipeWireFirst = false }

// The tests run in testing/synctest bubbles, and the pump's real sleep isn't
// one a bubble can wait out.
func init() { pwSleep = time.Sleep }

// withPipeWireFirst has PipeWire play where no driver is named, as on
// Linux.
func withPipeWireFirst(t *testing.T) {
	t.Helper()
	wasAA, wasCA, wasWA := aaudioFirst, coreAudioFirst, wasapiFirst
	aaudioFirst, coreAudioFirst, wasapiFirst, pipeWireFirst = false, false, false, true
	t.Cleanup(func() { aaudioFirst, coreAudioFirst, wasapiFirst, pipeWireFirst = wasAA, wasCA, wasWA, false })
}

func TestPipeWirePlaysWhereNoDriverIsNamed(t *testing.T) {
	withPipeWireFirst(t)
	usePipeWire(t)
	was := openSystemSound
	t.Cleanup(func() { openSystemSound = was })
	openSystemSound = func(*Speaker, Options) (startedOutput, error) {
		t.Error("the system's other sound opened")
		return nil, errors.New("no")
	}
	s, err := Open(audio.NewMixer(), Options{Rate: 48000})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	st := s.State()
	if st.Driver != "" {
		t.Errorf("as the system's own sound it tells the driver %q", st.Driver)
	}
	if st.Info == nil || st.Info.Driver != driver.PipeWire {
		t.Errorf("it plays through %+v", st.Info)
	}
}

func TestPipeWireNamedTellsItsName(t *testing.T) {
	usePipeWire(t)
	s, err := Open(audio.NewMixer(), Options{Rate: 48000, Driver: driver.PipeWire})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if d := s.State().Driver; d != driver.PipeWire {
		t.Errorf("it tells %q", d)
	}
}

func TestANamedDriverThatFailsFallsBackOnPipeWire(t *testing.T) {
	withPipeWireFirst(t)
	usePipeWire(t)
	was := openSystemSound
	t.Cleanup(func() { openSystemSound = was })
	openSystemSound = func(*Speaker, Options) (startedOutput, error) {
		t.Error("what takes the card from everything else opened, though PipeWire plays")
		return nil, errors.New("no")
	}
	wasPull := openPull
	t.Cleanup(func() { openPull = wasPull })
	openPull = func(driver.Config) (pullDevice, error) { return nil, errors.New("unplugged") }
	s, err := Open(audio.NewMixer(), Options{Rate: 48000, Driver: driver.ALSAPrefix + "M4"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	st := s.State()
	if st.Problem == nil {
		t.Error("it told of no problem")
	}
	if st.Info == nil || st.Info.Driver != driver.PipeWire {
		t.Errorf("it plays through %+v", st.Info)
	}
}

func TestChangingTheBufferReachesPipeWireAsTheSystemsSound(t *testing.T) {
	withPipeWireFirst(t)
	f := usePipeWire(t)
	s, err := Open(audio.NewMixer(), Options{Rate: 48000, Buffer: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.Set(Options{Rate: 48000, Buffer: 64}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cfg.Quantum != 64 {
		t.Errorf("the quantum is %d", f.cfg.Quantum)
	}
}

func TestWherePipeWireDoesNotPlayTheSystemsOtherSoundDoes(t *testing.T) {
	withPipeWireFirst(t)
	f := usePipeWire(t)
	f.failed = errors.New("no server")
	was := openSystemSound
	t.Cleanup(func() { openSystemSound = was })
	opened := false
	openSystemSound = func(_ *Speaker, o Options) (startedOutput, error) {
		opened = true
		return &stillOut{hz: o.rate()}, nil
	}
	s, err := Open(audio.NewMixer(), Options{Rate: 48000})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if !opened {
		t.Error("nothing else opened")
	}
}

func TestArmingOnTheSystemsSoundLeavesPipeWirePlaying(t *testing.T) {
	withPipeWireFirst(t)
	f := usePipeWire(t)
	s, err := Open(audio.NewMixer(), Options{Rate: 48000})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.Set(Options{Rate: 48000, Record: true}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.opens != 1 {
		t.Errorf("opened %d times", f.opens)
	}
	if f.cfg.Record {
		t.Error("it records what no one reads")
	}
}

// useM4 lists a device of four outputs and inputs, and returns its name.
func useM4(t *testing.T) string {
	t.Helper()
	was := pwDevices
	t.Cleanup(func() { pwDevices = was })
	four := []uint32{3, 4, 12, 13}
	pwDevices = func() ([]pipewire.Device, error) {
		return []pipewire.Device{
			{Name: "Built-in", Output: "builtin", OutPositions: []uint32{3, 4}},
			{Name: "M4", Output: "m4.out", Input: "m4.in", OutPositions: four, InPositions: four},
		}, nil
	}
	return driver.PipeWirePrefix + "M4"
}

func TestADeviceOfPipeWireOpensAllItsChannels(t *testing.T) {
	f := usePipeWire(t)
	name := useM4(t)
	s, err := Open(audio.NewMixer(), Options{Rate: 48000, Driver: name, Record: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f.mu.Lock()
	c := f.cfg
	f.mu.Unlock()
	if c.Output != "m4.out" || c.Input != "m4.in" || len(c.OutPositions) != 4 || len(c.InPositions) != 4 || !c.Record {
		t.Errorf("it opened %+v", c)
	}
	st := s.State()
	if st.Driver != name || st.Info == nil || st.Info.Driver != name {
		t.Fatalf("state %+v", st)
	}
	if st.Info.Outputs != 4 || st.Info.Inputs != 4 || st.Info.Recorded != 4 || st.Info.OutputNames[3] != "Output 4" || st.Info.InputNames[3] != "Input 4" {
		t.Errorf("info %+v", st.Info)
	}
}

func TestADeviceGoneFallsBackAndSaysSo(t *testing.T) {
	noBackend(t)
	usePipeWire(t)
	useM4(t)
	was := openSystemSound
	t.Cleanup(func() { openSystemSound = was })
	openSystemSound = func(_ *Speaker, o Options) (startedOutput, error) { return &stillOut{hz: o.rate()}, nil }
	s, err := Open(audio.NewMixer(), Options{Rate: 48000, Driver: driver.PipeWirePrefix + "Gone"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if st := s.State(); st.Problem == nil {
		t.Errorf("state %+v", st)
	}
}

func TestADeviceGoneFallsBackOnPipeWiresDefaultNotACard(t *testing.T) {
	withPipeWireFirst(t)
	f := usePipeWire(t)
	useM4(t)
	was := openSystemSound
	t.Cleanup(func() { openSystemSound = was })
	openSystemSound = func(*Speaker, Options) (startedOutput, error) {
		t.Error("what takes a card from the desktop opened, though PipeWire plays")
		return nil, errors.New("no")
	}
	s, err := Open(audio.NewMixer(), Options{Rate: 48000, Driver: driver.PipeWirePrefix + "Unplugged"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	st := s.State()
	if st.Problem == nil || st.Info == nil || st.Info.Driver != driver.PipeWire {
		t.Errorf("state %+v", st)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cfg.Output != "" {
		t.Errorf("it opened on %q", f.cfg.Output)
	}
}

func TestTheRingKeepsACycleInHandOrASpareOfALongOne(t *testing.T) {
	p := &pwOutput{s: &Speaker{}, hz: 48000}
	for _, c := range []struct{ q, want int64 }{{64, 128}, {256, 512}, {1024, 1024 + 288}} {
		p.quantum.Store(c.q)
		if got := p.target(); got != c.want {
			t.Errorf("a cycle of %d keeps %d, want %d", c.q, got, c.want)
		}
	}
}

func TestThePumpNeverSpins(t *testing.T) {
	p := &pwOutput{hz: 48000}
	if d := p.tick(); d < pwTickLeast {
		t.Errorf("with no cycle yet it waits %v", d)
	}
	p.quantum.Store(64)
	if d := p.tick(); d != audio.DurationAt(64, 48000)/4 {
		t.Errorf("at 64 it waits %v", d)
	}
	p.quantum.Store(8192)
	if d := p.tick(); d != pumpTick {
		t.Errorf("at 8192 it waits %v", d)
	}
}

func TestSpreadPutsChannelsWhereTheyPlay(t *testing.T) {
	mix := []float32{1, 2, 3, 4} // two frames of a pair
	out := make([]float32, 8)
	spread(out, mix, 2, 4, pwLayout(nil, 4))
	if want := []float32{1, 2, 0, 0, 3, 4, 0, 0}; !slices.Equal(out, want) {
		t.Errorf("the first two: %v", out)
	}
	out = make([]float32, 8)
	spread(out, mix, 2, 4, pwLayout([]int{3, 2}, 4))
	if want := []float32{0, 0, 2, 1, 0, 0, 4, 3}; !slices.Equal(out, want) {
		t.Errorf("those named: %v", out)
	}
	out = make([]float32, 8)
	spread(out, mix, 2, 4, pwLayout([]int{1, 9}, 4))
	if want := []float32{0, 1, 0, 0, 0, 3, 0, 0}; !slices.Equal(out, want) {
		t.Errorf("one the device lacks: %v", out)
	}
	out = make([]float32, 8)
	spread(out, mix, 2, 4, pwLayout([]int{2}, 4))
	if want := []float32{0, 0, 1.5, 0, 0, 0, 3.5, 0}; !slices.Equal(out, want) {
		t.Errorf("one output named: %v", out)
	}
	mono := make([]float32, 2)
	spread(mono, mix, 2, 1, pwLayout(nil, 1))
	if want := []float32{1.5, 3.5}; !slices.Equal(mono, want) {
		t.Errorf("one output: %v", mono)
	}
}

func TestSpreadAllocatesNothing(t *testing.T) {
	mix, out := make([]float32, 128), make([]float32, 256)
	played := pwLayout(nil, 4)
	if n := testing.AllocsPerRun(100, func() { spread(out, mix, 2, 4, played) }); n != 0 {
		t.Errorf("it allocates %v times", n)
	}
}

func TestReopenTakesADriverBackOnceItIsThere(t *testing.T) {
	withPipeWireFirst(t)
	f := usePipeWire(t)
	name := useM4(t)
	there := false
	was := pwDevices
	four := []uint32{3, 4, 12, 13}
	pwDevices = func() ([]pipewire.Device, error) {
		if !there {
			return nil, nil
		}
		return []pipewire.Device{{Name: "M4", Output: "m4.out", OutPositions: four}}, nil
	}
	t.Cleanup(func() { pwDevices = was })
	s, err := Open(audio.NewMixer(), Options{Rate: 48000, Driver: name})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if st := s.State(); st.Driver != "" || st.Problem == nil {
		t.Fatalf("with the device away: %+v", st)
	}
	there = true
	s.Reopen()
	waitFor(t, "the device", func() bool { return s.State().Driver == name })
	if st := s.State(); st.Problem != nil {
		t.Errorf("the problem stands: %v", st.Problem)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cfg.Output != "m4.out" {
		t.Errorf("it opened on %q", f.cfg.Output)
	}
}
