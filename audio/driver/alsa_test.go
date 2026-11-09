//go:build linux && !android && (amd64 || arm64)

package driver

import (
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio/internal/alsa"
)

func TestSamplesKeepTheirValueThroughEachFormat(t *testing.T) {
	for _, f := range []alsaFormat{
		{alsa.FormatS16, 2, 1}, {alsa.FormatS24, 4, 1}, {alsa.FormatS243, 3, 1}, {alsa.FormatS32, 4, 1}, {alsa.FormatFloat, 4, 1},
	} {
		for _, v := range []float32{0, 0.5, -0.5, 0.25, -1} {
			b := make([]byte, 4)
			putSample(b, f, v)
			if got := getSample(b, f); math.Abs(float64(got-v)) > 1.0/(1<<14) {
				t.Errorf("format %d: %v came back as %v", f.format, v, got)
			}
		}
	}
}

func TestSamplesPastFullScaleStopThere(t *testing.T) {
	for _, f := range []alsaFormat{{alsa.FormatS16, 2, 1}, {alsa.FormatS24, 4, 1}, {alsa.FormatS243, 3, 1}, {alsa.FormatS32, 4, 1}} {
		b := make([]byte, 4)
		putSample(b, f, 8)
		if got := getSample(b, f); got < 0.99 {
			t.Errorf("format %d: 8 came back as %v", f.format, got)
		}
		putSample(b, f, -8)
		if got := getSample(b, f); got > -0.99 {
			t.Errorf("format %d: -8 came back as %v", f.format, got)
		}
		putSample(b, f, float32(math.NaN()))
		if got := getSample(b, f); got != 0 {
			t.Errorf("format %d: NaN came back as %v", f.format, got)
		}
	}
}

func s32At(l *alsaLoop, frame, ch int) float32 {
	return getSample(l.outBytes[(frame*l.out.channels+ch)*l.out.bytes:], l.out)
}

var fourOut = alsaFormat{alsa.FormatS32, 4, 4}

func TestAPairPlaysOnTheFirstTwoOutputs(t *testing.T) {
	c := Config{Fill: func(f []float32) {
		for i := 0; i+1 < len(f); i += 2 {
			f[i], f[i+1] = 0.25, -0.25
		}
	}}
	l := newALSALoop(c, 8, fourOut, alsaFormat{}, layout(nil, 4))
	l.fill()
	for i := range 8 {
		if s32At(&l, i, 0) != 0.25 || s32At(&l, i, 1) != -0.25 || s32At(&l, i, 2) != 0 || s32At(&l, i, 3) != 0 {
			t.Fatalf("frame %d: %v %v %v %v", i, s32At(&l, i, 0), s32At(&l, i, 1), s32At(&l, i, 2), s32At(&l, i, 3))
		}
	}
}

func TestAPairPlaysOnTheOutputsNamed(t *testing.T) {
	c := Config{Outputs: []int{3, 2}, Fill: func(f []float32) {
		for i := 0; i+1 < len(f); i += 2 {
			f[i], f[i+1] = 0.25, -0.25
		}
	}}
	l := newALSALoop(c, 4, fourOut, alsaFormat{}, layout(c.Outputs, 4))
	l.fill()
	if s32At(&l, 1, 3) != 0.25 || s32At(&l, 1, 2) != -0.25 || s32At(&l, 1, 0) != 0 {
		t.Errorf("got %v %v %v %v", s32At(&l, 1, 0), s32At(&l, 1, 1), s32At(&l, 1, 2), s32At(&l, 1, 3))
	}
}

func TestEveryOutputPlaysItsOwnChannel(t *testing.T) {
	c := Config{Outputs: []int{0, 1, 2, 3}, FillN: func(f []float32, ch int) {
		for i := 0; i < len(f); i += ch {
			for k := range ch {
				f[i+k] = float32(k+1) / 8
			}
		}
	}}
	l := newALSALoop(c, 4, fourOut, alsaFormat{}, layout(c.Outputs, 4))
	l.fill()
	for k := range 4 {
		if got, want := s32At(&l, 2, k), float32(k+1)/8; got != want {
			t.Errorf("output %d: %v, want %v", k, got, want)
		}
	}
}

func TestOneOutputHearsThePairAveraged(t *testing.T) {
	c := Config{Fill: func(f []float32) {
		for i := 0; i+1 < len(f); i += 2 {
			f[i], f[i+1] = 0.5, 0.25
		}
	}}
	l := newALSALoop(c, 4, alsaFormat{alsa.FormatS32, 4, 1}, alsaFormat{}, layout(nil, 1))
	l.fill()
	if got := s32At(&l, 2, 0); got != 0.375 {
		t.Errorf("got %v", got)
	}
}

func TestInputsCarryTheirOwnChannelsAndOnlyWhenWanted(t *testing.T) {
	want := false
	calls := 0
	var seen []float32
	c := Config{Fill: func([]float32) {}, Recording: func() bool { return want }, Record: func(in []float32, ch int) {
		calls++
		if ch != 4 {
			t.Errorf("%d channels", ch)
		}
		seen = in
	}}
	in := alsaFormat{alsa.FormatS32, 4, 4}
	l := newALSALoop(c, 2, fourOut, in, layout(nil, 4))
	for i := range 8 {
		putSample(l.inBytes[i*4:], in, float32(i)/8)
	}
	l.record()
	if calls != 0 {
		t.Error("recorded while unwanted")
	}
	want = true
	l.record()
	if calls != 1 || seen[5] != 5.0/8 {
		t.Errorf("%d calls, %v", calls, seen)
	}
}

func TestTheLoopAllocatesNothing(t *testing.T) {
	c := Config{Fill: func([]float32) {}, Record: func([]float32, int) {}}
	in := alsaFormat{alsa.FormatS243, 3, 4}
	l := newALSALoop(c, 64, fourOut, in, layout(nil, 4))
	if n := testing.AllocsPerRun(100, func() {
		l.record()
		l.fill()
	}); n != 0 {
		t.Errorf("it allocates %v times", n)
	}
}

func TestCardsAreNamedAndHDMILeftOut(t *testing.T) {
	got := namedCards([]alsa.PCMInfo{
		{Card: 0, Device: 0, Name: "HDA Intel PCH", ID: "ALC3271 Analog", Plays: true, Record: true},
		{Card: 0, Device: 3, Name: "HDA Intel PCH", ID: "HDMI 0", Plays: true},
		{Card: 1, Device: 0, Name: "M4", ID: "USB Audio", Plays: true, Record: true},
		{Card: 2, Device: 0, Name: "M4", ID: "USB Audio", Plays: true},
		{Card: 3, Device: 0, Name: "Mic", ID: "USB Audio", Record: true},
		{Card: 4, Device: 0, Name: "Two", ID: "A", Plays: true},
		{Card: 4, Device: 1, Name: "Two", ID: "B", Plays: true},
	})
	want := []string{"ALSA: HDA Intel PCH", "ALSA: M4", "ALSA: M4 2", "ALSA: Two, A", "ALSA: Two, B"}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i].name != want[i] {
			t.Errorf("%d: %q, want %q", i, got[i].name, want[i])
		}
	}
}

// fakePCM is a card that plays as fast as it is asked, failing as told.
type fakePCM struct {
	starts, drops atomic.Int64
	writes        atomic.Int64
	// fail is returned once from the write it says.
	failAt int64
	fail   error
}

func (f *fakePCM) Write(_ []byte, n int) (int, error) {
	w := f.writes.Add(1)
	if f.fail != nil && w == f.failAt {
		return 0, f.fail
	}
	time.Sleep(100 * time.Microsecond)
	return n, nil
}
func (f *fakePCM) Read(_ []byte, n int) (int, error) { return n, nil }
func (f *fakePCM) Wait() error                       { return nil }
func (f *fakePCM) Prepare() error                    { return nil }
func (f *fakePCM) Start() error                      { f.starts.Add(1); return nil }
func (f *fakePCM) Drop() error                       { f.drops.Add(1); return nil }

func fakeDevice(f *fakePCM, c Config) *Device {
	if c.Fill == nil {
		c.Fill = func([]float32) {}
	}
	d := &Device{cfg: c, out: f, done: make(chan struct{}), release: func() {}, closePCM: func() {}, periods: alsaPeriods}
	d.loop = newALSALoop(c, 4, fourOut, alsaFormat{}, layout(nil, 4))
	return d
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(end) {
			t.Fatalf("no sign of %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestACardOutOfTimeIsToldOfAndStartsAgain(t *testing.T) {
	var overruns atomic.Int64
	f := &fakePCM{failAt: 5, fail: alsa.ErrXrun}
	d := fakeDevice(f, Config{Overrun: func() { overruns.Add(1) }})
	if err := d.begin(); err != nil {
		t.Fatal(err)
	}
	go d.run()
	waitFor(t, "playing on", func() bool { return f.writes.Load() > 20 })
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if overruns.Load() != 1 {
		t.Errorf("told of %d overruns", overruns.Load())
	}
	if f.starts.Load() != 2 {
		t.Errorf("started %d times", f.starts.Load())
	}
}

func TestACardGoneAsksToBeOpenedAgain(t *testing.T) {
	var resets, overruns atomic.Int64
	f := &fakePCM{failAt: 5, fail: alsa.ErrGone}
	d := fakeDevice(f, Config{Reset: func() { resets.Add(1) }, Overrun: func() { overruns.Add(1) }})
	if err := d.begin(); err != nil {
		t.Fatal(err)
	}
	go d.run()
	waitFor(t, "the ask", func() bool { return resets.Load() == 1 })
	<-d.done
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if overruns.Load() != 0 {
		t.Errorf("told of %d overruns", overruns.Load())
	}
}

func TestACardGoneAsItClosesAsksNothing(t *testing.T) {
	var resets atomic.Int64
	d := fakeDevice(&fakePCM{}, Config{Reset: func() { resets.Add(1) }})
	d.quit.Store(true)
	d.lost(alsa.ErrGone)
	time.Sleep(10 * time.Millisecond)
	if resets.Load() != 0 {
		t.Error("it asked to be opened again as it closes")
	}
}

func TestClosingTwiceIsNoFault(t *testing.T) {
	f := &fakePCM{}
	d := fakeDevice(f, Config{})
	go d.run()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpeningNoCardSaysSo(t *testing.T) {
	if _, err := Open(Config{Name: ALSAPrefix + "No such card"}); err == nil {
		t.Error("it opened")
	}
	if _, err := Open(Config{Name: "An ASIO driver"}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("got %v", err)
	}
}
