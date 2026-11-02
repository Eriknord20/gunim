package speaker

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/asio"
	"github.com/marrasen/gunim/audio/internal/duplex"
	"github.com/marrasen/gunim/audio/internal/jack"
)

// fakeJack stands in for a JACK server and the client the speaker opens
// on it: the test takes its buffers, as the server's thread would.
type fakeJack struct {
	mu   sync.Mutex
	info jack.Info
	cfg  jack.Config
	// next is the info the next open takes, where set.
	next  *jack.Info
	opens int
	// q holds the frames written, of chans channels, not yet taken.
	q      []float32
	chans  int
	size   int
	taken  int64
	since  int
	paused bool
	ev     jack.Events
	// ins are what the inputs recorded, buffer by buffer, not yet read.
	ins    []fakeIn
	closed bool
	failed error
	// closeErr is what Close returns; closes counts its calls.
	closeErr error
	closes   int
}

type fakeIn struct {
	frames []float32
	at     int64
}

func useJack(t *testing.T, info jack.Info) *fakeJack {
	t.Helper()
	f := &fakeJack{info: info}
	was := openJack
	openJack = f.open
	t.Cleanup(func() { openJack = was })
	return f
}

func (f *fakeJack) open(c jack.Config) (jackDevice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failed != nil {
		return nil, f.failed
	}
	if f.next != nil {
		f.info, f.next = *f.next, nil
	}
	f.cfg, f.opens, f.closed = c, f.opens+1, false
	f.info.Played = jackLayout(c.Outputs, len(f.info.Outputs))
	if len(f.info.Played) == 0 {
		return nil, errors.New("jack: the server has none of the outputs []")
	}
	if c.Record {
		f.info.Recorded = len(f.info.Inputs)
	} else {
		f.info.Recorded = 0
	}
	f.chans, f.size = len(f.info.Played), int(audio.FramesAt(c.Hold, f.info.Rate))+8192
	f.q, f.taken, f.ins, f.ev = nil, 0, nil, jack.Events{Rate: f.info.Rate, Buffer: f.info.Buffer}
	return fakeJackDevice{f}, nil
}

// jackLayout is package jack's layout, in short.
func jackLayout(want []int, outs int) []int {
	if want == nil {
		return []int{0, 1}[:min(2, max(outs, 1))]
	}
	return slices.Clone(want)
}

// take takes a buffer, as the server does, and returns what it played:
// the ring's frames, silence past them. The inputs record a buffer of
// frames counting up, with the frame taken as they came in.
func (f *fakeJack) take() []float32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ch := f.info.Buffer, f.chans
	out := make([]float32, n*ch)
	at := f.taken
	if !f.paused {
		k := min(n, len(f.q)/ch)
		copy(out, f.q[:k*ch])
		f.q = f.q[k*ch:]
		f.taken += int64(k)
		if k < n {
			f.ev.Short++
		}
	}
	if f.info.Recorded > 0 {
		in := make([]float32, n*f.info.Recorded)
		f.ins = append(f.ins, fakeIn{in, at})
	}
	f.ev.Cycles++
	return out
}

// tell has the server tell e, as its callbacks would.
func (f *fakeJack) tell(e func(*jack.Events)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e(&f.ev)
}

type fakeJackDevice struct{ f *fakeJack }

func (d fakeJackDevice) Info() jack.Info {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return d.f.info
}

func (d fakeJackDevice) Buffer() int {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return d.f.info.Buffer
}

func (d fakeJackDevice) Latency() (out, in int) {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return d.f.info.Latency, d.f.info.InputLatency
}

func (d fakeJackDevice) Write(frames []float32) int {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	ch := d.f.chans
	n := min(len(frames)/ch, d.f.size-len(d.f.q)/ch)
	d.f.q = append(d.f.q, frames[:n*ch]...)
	return n
}

func (d fakeJackDevice) Queued() int64 {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return int64(len(d.f.q) / d.f.chans)
}

func (d fakeJackDevice) Room() int64 {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return int64(d.f.size - len(d.f.q)/d.f.chans)
}

func (d fakeJackDevice) Read(dst []float32) (n int, at int64, ok bool) {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	if len(d.f.ins) == 0 {
		return 0, 0, false
	}
	in := d.f.ins[0]
	d.f.ins = d.f.ins[1:]
	return copy(dst, in.frames) / d.f.info.Recorded, in.at, true
}

func (d fakeJackDevice) SinceCycle() int {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return d.f.since
}

func (d fakeJackDevice) Poll() jack.Events {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	e := d.f.ev
	d.f.ev.Short, d.f.ev.Xruns, d.f.ev.Lost, d.f.ev.Graph = 0, 0, 0, false
	if e.Graph || e.Buffer != d.f.info.Buffer {
		d.f.info.Buffer = e.Buffer
		e.Graph = true
	}
	return e
}

func (d fakeJackDevice) Pause(paused bool) {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	d.f.paused = paused
}

func (d fakeJackDevice) Err() error {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	if d.f.ev.Shutdown {
		return jack.ErrShutdown
	}
	return nil
}

func (d fakeJackDevice) Close() error {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	d.f.closed = true
	d.f.closes++
	return d.f.closeErr
}

// jackOf returns the JACK output s plays through.
func jackOf(t *testing.T, s *Speaker) *jackOutput {
	t.Helper()
	j, ok := s.out.Load().output.(*jackOutput)
	if !ok {
		t.Fatalf("the speaker plays through %T", s.out.Load().output)
	}
	return j
}

func stereoInfo() jack.Info {
	return jack.Info{Client: "app", Library: "fake", Rate: 44100, Buffer: 256, Latency: 512, InputLatency: 300,
		Outputs: []string{"system:playback_1", "system:playback_2"},
		Inputs:  []string{"system:capture_1", "system:capture_2"}}
}

// JACK plays the mixer at the server's rate, whatever rate was asked,
// and the state tells the server's buffer and latency.
func TestJackPlaysTheMixer(t *testing.T) {
	f := useJack(t, stereoInfo())
	m := audio.NewMixer()
	m.Play(steady(0.25), audio.Options{})
	s, err := Open(m, Options{Name: "My app", Driver: asio.JACK, Rate: 48000, Latency: 50 * time.Millisecond, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if m.Rate() != 44100 {
		t.Errorf("the mixer plays at %d Hz, want the server's 44100", m.Rate())
	}
	if f.cfg.Name != "My app" || f.cfg.Hold != MaxLatency {
		t.Errorf("opened with %+v", f.cfg)
	}
	st := s.State()
	if st.Driver != asio.JACK || st.ASIO == nil || st.ASIO.Latency != 256+512 || st.ASIO.Buffer != 256 ||
		!slices.Equal(st.ASIO.Played, []int{0, 1}) || st.ASIO.Recorded != 0 {
		t.Errorf("state %+v, info %+v", st, st.ASIO)
	}
	if want := 50 * time.Millisecond; st.Latency < want-time.Millisecond || st.Latency > want+time.Millisecond {
		t.Errorf("working %v ahead, want %v", st.Latency, want)
	}
	j := jackOf(t, s)
	waitFor(t, "the ring to fill", j.primed.Load)
	// The voice fades in over its first frames: read past them.
	f.take()
	got := f.take()
	for i, v := range got {
		if v != 0.25 {
			t.Fatalf("sample %d is %v, want 0.25", i, v)
		}
	}
	if !errors.Is(s.ControlPanel(), errJackPanel) {
		t.Errorf("ControlPanel = %v", s.ControlPanel())
	}
}

// The frames held are the ring's, the buffer's and the ports' latency,
// less the frames the server's clock says played since its buffer
// began: the frame heard moves through a buffer, not a buffer at once.
func TestJackHeldFollowsTheFrameTime(t *testing.T) {
	f := useJack(t, stereoInfo())
	m := audio.NewMixer()
	s, err := Open(m, Options{Driver: asio.JACK, Latency: 40 * time.Millisecond, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	j := jackOf(t, s)
	waitFor(t, "the ring to fill", j.primed.Load)
	target := j.target()
	waitFor(t, "the ring at its target", func() bool { return j.dev.Queued() == target })
	for _, c := range []struct{ since, less int }{{0, 0}, {100, 100}, {255, 255}, {1000, 256}, {-5, 0}} {
		f.mu.Lock()
		f.since = c.since
		f.mu.Unlock()
		if got, want := s.held(), target+256+512-int64(c.less); got != want {
			t.Errorf("since %d: held %d, want %d", c.since, got, want)
		}
	}
	// Latency is the ring's target and the device's own.
	if got, want := s.Latency(), audio.DurationAt(target+256+512, 44100); got != want {
		t.Errorf("latency %v, want %v", got, want)
	}
}

// However little latency is asked for, the ring holds a buffer and two
// of the pump's ticks.
func TestJackLatencyHasAFloor(t *testing.T) {
	useJack(t, stereoInfo())
	s, err := Open(audio.NewMixer(), Options{Driver: asio.JACK, Latency: time.Millisecond, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	j := jackOf(t, s)
	if got, want := j.target(), int64(256)+audio.FramesAt(2*pumpTick, 44100); got != want {
		t.Errorf("target %d, want %d", got, want)
	}
}

// Buffers the server finds short, once the ring has filled, count as
// dropouts and grow the latency; xruns count as dropouts alone.
func TestJackRunningDryAndXruns(t *testing.T) {
	info := stereoInfo()
	info.Buffer = 4410
	f := useJack(t, info)
	s, err := Open(audio.NewMixer(), Options{Driver: asio.JACK, Latency: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	j := jackOf(t, s)
	waitFor(t, "the ring to fill", j.primed.Load)
	start := s.Latency()
	f.tell(func(e *jack.Events) { e.Xruns = 3 })
	waitFor(t, "the xruns counted", func() bool { return s.State().Dropouts >= 3 })
	if s.Latency() != start {
		t.Errorf("xruns grew the latency to %v from %v", s.Latency(), start)
	}
	for range 300 {
		f.take()
		f.take()
		time.Sleep(2 * time.Millisecond)
	}
	if s.Latency() <= start {
		t.Errorf("working %v ahead after running dry, from %v", s.Latency(), start)
	}
	if s.State().Dropouts <= 3 {
		t.Errorf("dropouts %d", s.State().Dropouts)
	}
}

// The server shutting down, or changing its rate, opens the output
// again: at the new rate, the mixer with it.
func TestJackShutdownAndRateOpenAgain(t *testing.T) {
	f := useJack(t, stereoInfo())
	m := audio.NewMixer()
	s, err := Open(m, Options{Driver: asio.JACK})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f.tell(func(e *jack.Events) { e.Shutdown = true })
	select {
	case <-s.Changed():
	case <-time.After(3 * time.Second):
		t.Fatal("the speaker never opened again after the server shut down")
	}
	f.mu.Lock()
	next := stereoInfo()
	next.Rate = 96000
	f.next = &next
	f.mu.Unlock()
	f.tell(func(e *jack.Events) { e.Rate = 96000 })
	select {
	case <-s.Changed():
	case <-time.After(3 * time.Second):
		t.Fatal("the speaker never opened again after the rate changed")
	}
	f.mu.Lock()
	opens := f.opens
	f.mu.Unlock()
	if opens != 3 || m.Rate() != 96000 || s.State().Rate != 96000 {
		t.Errorf("opened %d times, mixer at %d Hz, want 3 and 96000", opens, m.Rate())
	}
}

// A buffer size changed on the server reaches the state and the
// latency, with no opening again.
func TestJackBufferChangeFollows(t *testing.T) {
	f := useJack(t, stereoInfo())
	s, err := Open(audio.NewMixer(), Options{Driver: asio.JACK})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f.tell(func(e *jack.Events) { e.Buffer = 1024 })
	waitFor(t, "the new buffer", func() bool { return s.State().ASIO.Buffer == 1024 })
	if st := s.State(); st.ASIO.Latency != 1024+512 {
		t.Errorf("latency %d, want %d", st.ASIO.Latency, 1024+512)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.opens != 1 {
		t.Errorf("opened %d times, want 1", f.opens)
	}
}

// The inputs reach package audio/capture stamped with the mixer frame
// heard as they came in: the frame taken then, less the outputs' and
// the inputs' latency.
func TestJackRecordsStamped(t *testing.T) {
	f := useJack(t, stereoInfo())
	m := audio.NewMixer()
	s, err := Open(m, Options{Driver: asio.JACK, Record: true, Latency: 30 * time.Millisecond, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	tap := duplex.Current()
	if tap == nil || tap.Driver != asio.JACK || len(tap.Names) != 2 || tap.Latency != 300 {
		t.Fatalf("tap %+v", tap)
	}
	j := jackOf(t, s)
	waitFor(t, "the ring to fill", j.primed.Load)
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
		f.take()
	}
	waitFor(t, "three buffers recorded", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(heard) == 3
	})
	base := j.base.Load()
	for i, h := range heard {
		if want := base + int64(256*i) - 512 - 300; h != want {
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

// Outputs past the mixer's pair play what Aux fills; one output plays
// the pair averaged.
func TestJackAuxAndMono(t *testing.T) {
	info := stereoInfo()
	info.Outputs = []string{"p1", "p2", "p3", "p4"}
	f := useJack(t, info)
	m := audio.NewMixer()
	m.Play(steady(0.5), audio.Options{})
	aux := func(dst []float32, channels int, _ int64) {
		for i := range dst {
			dst[i] = float32(1 + i%channels)
		}
	}
	s, err := Open(m, Options{Driver: asio.JACK, Outputs: []int{0, 1, 2, 3}, Aux: aux})
	if err != nil {
		t.Fatal(err)
	}
	if s.State().Aux != 2 {
		t.Errorf("aux %d, want 2", s.State().Aux)
	}
	waitFor(t, "the ring to fill", jackOf(t, s).primed.Load)
	f.take()
	got := f.take()
	if got[0] != 0.5 || got[1] != 0.5 || got[2] != 1 || got[3] != 2 {
		t.Errorf("a frame played %v, want [0.5 0.5 1 2]", got[:4])
	}
	_ = s.Close()

	info.Outputs = []string{"mono"}
	f = useJack(t, info)
	m = audio.NewMixer()
	m.Play(steady(0.5), audio.Options{})
	s, err = Open(m, Options{Driver: asio.JACK})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	waitFor(t, "the ring to fill", jackOf(t, s).primed.Load)
	f.take()
	got = f.take()
	if len(got) != 256 || got[0] != 0.5 {
		t.Errorf("mono played %d samples, first %v", len(got), got[0])
	}
}

// Suspended, the server plays silence and takes nothing from the ring.
func TestJackSuspendPauses(t *testing.T) {
	f := useJack(t, stereoInfo())
	m := audio.NewMixer()
	s, err := Open(m, Options{Driver: asio.JACK})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	waitFor(t, "the ring to fill", jackOf(t, s).primed.Load)
	if err := s.Suspend(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	paused := f.paused
	f.mu.Unlock()
	if !paused {
		t.Fatal("suspended, the server plays on")
	}
	if err := s.Resume(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	paused = f.paused
	f.mu.Unlock()
	if paused {
		t.Fatal("resumed, the server stays paused")
	}
}

// held, Heard and Anchor read the output from other goroutines, as the
// UI does each frame, while the output opens again and closes: nothing
// races, and nothing reads a device past its close but SinceCycle,
// which package jack guards.
func TestJackHeldWhileReopening(t *testing.T) {
	useJack(t, stereoInfo())
	m := audio.NewMixer()
	s, err := Open(m, Options{Driver: asio.JACK})
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
	o := Options{Driver: asio.JACK}
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

// A client that fails to close is let go all the same: the speaker
// opens the next, and closes it, with no second close of the first.
func TestJackFailedCloseLetsGo(t *testing.T) {
	f := useJack(t, stereoInfo())
	s, err := Open(audio.NewMixer(), Options{Driver: asio.JACK})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.closeErr = errors.New("jack: closing the client failed")
	f.mu.Unlock()
	first := jackOf(t, s)
	if err := s.Set(Options{Driver: asio.JACK, Record: true}); err != nil {
		t.Fatalf("Set = %v, want the output opened again", err)
	}
	if jackOf(t, s) == first {
		t.Fatal("the speaker kept the output that failed to close")
	}
	if err := first.close(); err != nil {
		t.Errorf("a second close = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closes != 2 {
		t.Errorf("clients closed %d times, want 2", f.closes)
	}
}

// Outputs naming none fails to open JACK, as an ASIO driver fails, and
// the speaker does not start a pump of no channels.
func TestJackNoOutputsFails(t *testing.T) {
	useJack(t, stereoInfo())
	_, err := openJackOutput(&Speaker{debug: false}, Options{Driver: asio.JACK, Outputs: []int{}})
	if err == nil {
		t.Fatal("JACK opened with no outputs")
	}
}
