package eq

import (
	"math"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
)

// host is a host for tests, which notes the gestures it is sent.
type host struct {
	values map[uint32]float64
	log    []string
	// in and out are the sound the views hear, or nil; proc, where set,
	// is the processor whose traces the views read.
	in, out plug.Sound
	proc    *processor
}

func (h *host) Param(id uint32) float64 {
	if v, ok := h.values[id]; ok {
		return v
	}
	// As a host does, a parameter not set is at its default.
	p, _ := Descriptor.Param(id)
	return p.Default
}
func (h *host) Begin(uint32, *gunim.UI) { h.log = append(h.log, "begin") }
func (h *host) Set(id uint32, v float64, _ *gunim.UI) {
	h.values[id] = v
	h.log = append(h.log, "set")
}
func (h *host) End(uint32, *gunim.UI)              { h.log = append(h.log, "end") }
func (h *host) BeginEdit(name string, _ *gunim.UI) { h.log = append(h.log, "edit "+name) }
func (h *host) Changed(*gunim.UI)                  { h.log = append(h.log, "changed") }
func (h *host) EndEdit(*gunim.UI)                  { h.log = append(h.log, "end edit") }
func (h *host) Sound() (in, out plug.Sound)        { return h.in, h.out }
func (h *host) Traced(id uint32, f int64) (float32, bool) {
	if h.proc == nil {
		return 0, false
	}
	if t := h.proc.Trace(id); t != nil {
		return t.Get(f)
	}
	return 0, false
}

func newEQ(t *testing.T) (*plugin, *host) {
	t.Helper()
	d, ok := plug.Lookup("marrasen.eq")
	if !ok {
		t.Fatal("the EQ did not register itself")
	}
	h := &host{values: map[uint32]float64{BypassID: 0}}
	p, ok := d.New(h).(*plugin)
	if !ok {
		t.Fatal("the EQ made something else")
	}
	return p, h
}

// bell is a bell of +6 dB at 1 kHz, Q 1.
var bell = []audio.Band{{ID: 1, Kind: audio.Bell, Freq: 1000, Gain: 6, Q: 1, On: true}}

// setBands sets p's bands through its state, as a host does.
func setBands(t *testing.T, p plug.Plugin, bands []audio.Band) {
	t.Helper()
	q, _ := newEQ(t)
	q.setBands(bands)
	if err := p.SetState(q.State()); err != nil {
		t.Fatal(err)
	}
}

// sine returns n frames of a sine at hz, at rate, at half scale, from
// frame at.
func sine(n, at int, hz, rate float64) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = float32(0.5 * math.Sin(2*math.Pi*hz*float64(at+i)/rate))
	}
	return s
}

// rms returns the level of s, in decibels.
func rms(s []float32) float64 {
	var sum float64
	for _, v := range s {
		sum += float64(v) * float64(v)
	}
	return 10 * math.Log10(sum/float64(len(s)))
}

// gainOf plays a second of a 1 kHz sine at 44.1 kHz through p, in
// blocks of block frames on chans channels with the processor's
// MaxBlock at most, and returns how much it lifts the last quarter
// second, in decibels, on each channel.
func gainOf(t *testing.T, p plug.Plugin, bypass float64, chans, block, most int) []float64 {
	t.Helper()
	const rate = 44100
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: rate, MaxBlock: most, Channels: chans})
	in, out := make([][]float32, chans), make([][]float32, chans)
	changes := []plug.Change{{ID: BypassID, Value: bypass}}
	for at := 0; at < rate; at += block {
		n := min(block, rate-at)
		ins, outs := make([][]float32, chans), make([][]float32, chans)
		for ch := range chans {
			ins[ch], outs[ch] = sine(n, at, 1000, rate), make([]float32, n)
		}
		pr.Process(&plug.Block{In: ins, Out: outs, Frames: n, Changes: changes})
		changes = nil
		for ch := range chans {
			in[ch], out[ch] = append(in[ch], ins[ch]...), append(out[ch], outs[ch]...)
		}
	}
	gains := make([]float64, chans)
	for ch := range chans {
		gains[ch] = rms(out[ch][rate*3/4:]) - rms(in[ch][rate*3/4:])
	}
	return gains
}

func TestABellLiftsASineByItsResponse(t *testing.T) {
	p, _ := newEQ(t)
	setBands(t, p, bell)
	want := audio.Response(bell, 1000)
	if math.Abs(want-6) > 0.1 {
		t.Fatalf("the bell's response at 1 kHz is %.2f dB, want about 6", want)
	}
	for ch, g := range gainOf(t, p, 0, 2, 512, 512) {
		if math.Abs(g-want) > 0.2 {
			t.Errorf("channel %d is lifted %.2f dB, want %.2f", ch, g, want)
		}
	}
}

func TestASinePassesWithNoBandsAndBypassed(t *testing.T) {
	p, _ := newEQ(t)
	for ch, g := range gainOf(t, p, 0, 2, 512, 512) {
		if math.Abs(g) > 0.01 {
			t.Errorf("with no bands, channel %d is lifted %.3f dB", ch, g)
		}
	}
	setBands(t, p, bell)
	for ch, g := range gainOf(t, p, 1, 2, 512, 512) {
		if math.Abs(g) > 0.01 {
			t.Errorf("bypassed, channel %d is lifted %.3f dB", ch, g)
		}
	}
}

func TestMonoAndLongBlocksAreLiftedToo(t *testing.T) {
	p, _ := newEQ(t)
	setBands(t, p, bell)
	if g := gainOf(t, p, 0, 1, 512, 512); math.Abs(g[0]-6) > 0.2 {
		t.Errorf("a mono block is lifted %.2f dB, want 6", g[0])
	}
	// Blocks of 1000 frames, with a MaxBlock of 256, go in chunks.
	g := gainOf(t, p, 0, 3, 1000, 256)
	for ch := range 2 {
		if math.Abs(g[ch]-6) > 0.2 {
			t.Errorf("in long blocks, channel %d is lifted %.2f dB, want 6", ch, g[ch])
		}
	}
	if g[2] != 0 {
		t.Errorf("the third channel is lifted %.3f dB, want it passed through", g[2])
	}
}

func TestProcessingAllocatesNothingAsTheBandsChange(t *testing.T) {
	p, _ := newEQ(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 256, Channels: 2})
	ch := [][]float32{sine(512, 0, 1000, 48000), sine(512, 0, 1000, 48000)}
	// Bands coming, changing kind and slope, going, and the bypass, a
	// change each few blocks; every allocation in Process counted, as
	// the mean a block would round the few a change makes down to none.
	steps := [][]audio.Band{
		bell,
		{{ID: 1, Kind: audio.LowCut, Freq: 80, Q: 0.7, Slope: 12, On: true}},
		{{ID: 1, Kind: audio.LowCut, Freq: 80, Q: 0.7, Slope: 48, On: true}, {ID: 2, Kind: audio.Notch, Freq: 3000, Q: 4, On: true}},
		{{ID: 2, Kind: audio.HighShelf, Freq: 6000, Gain: 3, Q: 0.7, On: true}},
		nil,
	}
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	// On one thread, as testing.AllocsPerRun counts, so other
	// goroutines allocate as little as may be meanwhile.
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	var made uint64
	for i := range 4 * len(steps) {
		if i%4 == 0 {
			setBands(t, p, steps[i/4])
		}
		b := &plug.Block{In: ch, Out: ch, Frames: 512, Changes: []plug.Change{{ID: BypassID, Value: float64(i % 2)}}}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		pr.Process(b)
		runtime.ReadMemStats(&after)
		made += after.Mallocs - before.Mallocs
	}
	if made != 0 {
		t.Errorf("processing made %d things over %d blocks", made, 4*len(steps))
	}
}

func TestTheStateRoundTripsAndBadStateIsRefused(t *testing.T) {
	p, _ := newEQ(t)
	if len(p.Bands()) != 0 {
		t.Fatal("a new EQ has bands")
	}
	bands := []audio.Band{
		{ID: 3, Kind: audio.LowCut, Freq: 30, Q: 0.707, Slope: 24, On: true},
		{ID: 5, Kind: audio.Bell, Freq: 2500, Gain: -3.5, Q: 2, On: false},
		{ID: 7, Kind: audio.HighShelf, Freq: 9000, Gain: 2, Q: 0.707, On: true},
	}
	q, _ := newEQ(t)
	q.setBands(bands)
	saved := q.State()
	if err := p.SetState(saved); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Bands(), bands) {
		t.Fatalf("the bands came back as %v, want %v", p.Bands(), bands)
	}
	for name, bad := range map[string]string{
		"not JSON":     "{",
		"a version on": `{"version":2,"bands":[]}`,
		"no version":   `{"bands":[]}`,
		"a bad kind":   `{"version":1,"bands":[{"id":1,"kind":"wobble","freq":100,"q":1}]}`,
		"a high freq":  `{"version":1,"bands":[{"id":1,"kind":"bell","freq":30000,"q":1}]}`,
		"a big gain":   `{"version":1,"bands":[{"id":1,"kind":"bell","freq":100,"gain":40,"q":1}]}`,
		"no Q":         `{"version":1,"bands":[{"id":1,"kind":"bell","freq":100}]}`,
		"a bad slope":  `{"version":1,"bands":[{"id":1,"kind":"lowCut","freq":100,"q":1,"slope":20}]}`,
		"two IDs":      `{"version":1,"bands":[{"id":1,"kind":"bell","freq":100,"q":1},{"id":1,"kind":"bell","freq":200,"q":1}]}`,
		"nine bands":   `{"version":1,"bands":[` + strings.Repeat(`{"id":1,"kind":"bell","freq":100,"q":1},`, 8) + `{"id":1,"kind":"bell","freq":100,"q":1}]}`,
	} {
		if p.SetState([]byte(bad)) == nil {
			t.Errorf("the EQ took a state with %s", name)
		}
		if !slices.Equal(p.Bands(), bands) {
			t.Fatalf("a state with %s changed the bands to %v", name, p.Bands())
		}
	}
	if err := p.SetState(nil); err != nil || len(p.Bands()) != 0 {
		t.Errorf("an empty state left %v, %v", p.Bands(), err)
	}
}

// stage mounts the view v in an offscreen window of size and returns
// the window and a way to run frames.
func stage(t *testing.T, size geom.Size, v gunim.Node) (w *gunim.Window, run func(int)) {
	t.Helper()
	w = gunimtest.New(t, size, nil)
	gunim.RegisterView(w, "eq", func(struct{}) gunim.Node { return v }, nil)
	if err := w.Client().Mount(gunim.Root, "eq", "eq", nil); err != nil {
		t.Fatal(err)
	}
	run = func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(2)
	return w, run
}

func TestADragOnTheGraphIsOneGesture(t *testing.T) {
	p, h := newEQ(t)
	p.setBands([]audio.Band{{ID: 1, Kind: audio.Bell, Freq: 1000, Gain: 0, Q: 1, On: true}})
	v, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is not the graph")
	}
	w, run := stage(t, geom.Sz(800, 400), v)
	g := v.graph
	from := g.dotAt(g.dots[1])
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	for i := 1; i <= 3; i++ {
		w.Input(input.PointerMove{Pos: from.Add(geom.Pt(float32(20*i), float32(-15*i)))})
		run(1)
	}
	w.Input(input.PointerUp{Pos: from.Add(geom.Pt(60, -45)), Button: input.ButtonPrimary})
	run(1)
	want := []string{"edit Move band 1", "changed", "changed", "changed", "end edit"}
	if !slices.Equal(h.log, want) {
		t.Errorf("the host heard %v, want %v", h.log, want)
	}
	b := p.Bands()[0]
	if b.Freq <= 1000 || b.Gain <= 0 {
		t.Errorf("dragged up and right, the band went to %v Hz and %v dB", b.Freq, b.Gain)
	}
	if !slices.Equal(g.bands, p.Bands()) {
		t.Errorf("the graph shows %v, and the plugin has %v", g.bands, p.Bands())
	}
}

func TestADragWithAltChangesTheWidthAndTheWheelNothing(t *testing.T) {
	p, h := newEQ(t)
	p.setBands([]audio.Band{
		{ID: 1, Kind: audio.Bell, Freq: 1000, Gain: 6, Q: 1, On: true},
		{ID: 2, Kind: audio.LowCut, Freq: 60, Slope: 12, On: true},
	})
	v, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is not the graph")
	}
	w, run := stage(t, geom.Sz(800, 400), v)
	g := v.graph
	from := g.dotAt(g.dots[1])
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.Scroll{Pos: from, Notches: geom.Pt(0, 1)})
	if p.Bands()[0].Q != 1 {
		t.Fatalf("the wheel took the Q to %v", p.Bands()[0].Q)
	}
	// Up with Alt narrows the bell, and leaves where it is alone.
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Mods: input.ModAlt, Clicks: 1})
	for i := 1; i <= 2; i++ {
		w.Input(input.PointerMove{Pos: from.Add(geom.Pt(30, -24*float32(i))), Mods: input.ModAlt})
		run(1)
	}
	w.Input(input.PointerUp{Pos: from.Add(geom.Pt(30, -48)), Button: input.ButtonPrimary})
	run(1)
	b := p.Bands()[0]
	if math.Abs(float64(b.Q)-1.15*1.15) > 0.01 || b.Freq != 1000 || b.Gain != 6 {
		t.Errorf("two notches up with Alt left the bell at Q %v, %v Hz and %v dB", b.Q, b.Freq, b.Gain)
	}
	want := []string{"edit Change the width of band 1", "changed", "changed", "end edit"}
	if !slices.Equal(h.log, want) {
		t.Errorf("the host heard %v, want %v", h.log, want)
	}
	// Up with Alt steepens the cut, a step for each notch.
	from = g.dotAt(g.dots[2])
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Mods: input.ModAlt, Clicks: 1})
	w.Input(input.PointerMove{Pos: from.Add(geom.Pt(0, -48)), Mods: input.ModAlt})
	w.Input(input.PointerUp{Pos: from.Add(geom.Pt(0, -48)), Button: input.ButtonPrimary})
	run(1)
	if b := p.Bands()[1]; b.Slope != 36 || b.Freq != 60 {
		t.Errorf("two notches up with Alt left the cut at %v dB/oct and %v Hz", b.Slope, b.Freq)
	}
}

func TestEachOtherChangeIsAGestureOfItsOwn(t *testing.T) {
	p, h := newEQ(t)
	p.setBands(bell)
	v, ok := p.View(plug.Full, h).(*full)
	if !ok {
		t.Fatal("the full view is not the graph and the strip")
	}
	w, run := stage(t, geom.Sz(1000, 500), v)
	g := v.panel.graph
	// A double-click away from the band adds one.
	at := geom.Pt(g.xOf(200), g.yOf(-6))
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 2})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(30)
	// By the keyboard, the graph keeping it from the click: Right moves
	// the band picked, the new one.
	w.Input(input.KeyPress{Key: input.KeyRight})
	run(1)
	g.setKind(1, audio.LowCut, nil)
	g.remove(2, nil)
	want := []string{
		"edit Add band", "changed", "end edit",
		"edit Move band 2", "changed", "end edit",
		"edit Band 1 to low cut", "changed", "end edit",
		"edit Remove band 2", "changed", "end edit",
	}
	if !slices.Equal(h.log, want) {
		t.Errorf("the host heard %v, want %v", h.log, want)
	}
	bands := p.Bands()
	if len(bands) != 1 || bands[0].Kind != audio.LowCut || bands[0].Slope != 24 {
		t.Errorf("the bands are %v, want one low cut of 24 dB an octave", bands)
	}
}

func TestTheGraphFollowsTheStateSetElsewhere(t *testing.T) {
	p, h := newEQ(t)
	v, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is not the graph")
	}
	var _ plug.StateFollower = v
	_, run := stage(t, geom.Sz(800, 400), v)
	setBands(t, p, bell)
	v.FollowState(nil)
	run(1)
	g := v.graph
	if !slices.Equal(g.bands, bell) {
		t.Fatalf("the graph shows %v, want %v", g.bands, bell)
	}
	// The new band's point grows in and settles where the band is.
	d := g.dots[1]
	if d.in.Value() > 0.5 {
		t.Errorf("a frame on, the new point has grown %v of the way", d.in.Value())
	}
	run(90)
	if b := d.value(); math.Abs(float64(b.Freq-1000)) > 1 || math.Abs(float64(b.Gain-6)) > 0.01 || d.in.Value() < 0.99 {
		t.Errorf("the point settles at %v Hz, %v dB, grown %v", b.Freq, b.Gain, d.in.Value())
	}
	// It glides to a band moved.
	moved := slices.Clone(bell)
	moved[0].Gain = -6
	setBands(t, p, moved)
	v.FollowState(nil)
	run(1)
	if gain := d.gain.Value(); gain < 0 {
		t.Errorf("a frame on, the point is at %v dB already", gain)
	}
	run(90)
	if gain := d.gain.Value(); math.Abs(float64(gain+6)) > 0.01 {
		t.Errorf("the point settles at %v dB, want -6", gain)
	}
	if len(h.log) != 0 {
		t.Errorf("following the state, the view told the host %v", h.log)
	}
}

func TestTheChipDrawsTheCurveAndFlattensBypassed(t *testing.T) {
	p, h := newEQ(t)
	c, ok := p.View(plug.Chip, h).(*chip)
	if !ok {
		t.Fatal("the chip is not the curve")
	}
	for _, db := range c.curve {
		if db != 0 {
			t.Fatal("with no bands, the chip's curve is not flat")
		}
	}
	setBands(t, p, bell)
	c.FollowState(nil)
	if peak := slices.Max(c.curve[:]); math.Abs(float64(peak)-6) > 0.5 {
		t.Errorf("the chip's curve peaks at %v dB, want about 6", peak)
	}
	c.Follow(BypassID, 1, nil)
	if !c.bypass {
		t.Error("the chip did not follow the bypass")
	}
}

// tone is a 1 kHz tone at a level, played to frame at, at 48 kHz.
type tone struct {
	level float64
	at    int64
}

func (s *tone) Heard(dst []float32) (frames int, at int64, rate int) {
	n := len(dst) / 2
	for i := range n {
		v := float32(s.level * math.Sin(2*math.Pi*1000*float64(s.at-int64(n)+int64(i))/48000))
		dst[2*i], dst[2*i+1] = v, v
	}
	return n, s.at, 48000
}

func TestTheGraphShowsTheSpectrumBeforeAndAfter(t *testing.T) {
	p, h := newEQ(t)
	in, out := &tone{level: 0.25}, &tone{level: 0.5}
	h.in, h.out = in, out
	g := newGraph(p, h)
	frame := time.Second / 60
	for range 120 {
		in.at += 800
		out.at += 800
		if !g.Step(frame) {
			t.Fatal("the graph rests while the sound plays")
		}
	}
	k := 0
	for i, f := range g.spec.freqs {
		if math.Abs(float64(f)-1000) < math.Abs(float64(g.spec.freqs[k])-1000) {
			k = i
		}
	}
	if d := g.spec.heardSm[k] - g.spec.beforeSm[k]; math.Abs(float64(d)-6.02) > 0.5 {
		t.Fatalf("at 1 kHz the sound after is %.2f dB over the sound before, want 6", d)
	}
	// The sound stops: the spectrum falls, and the graph comes to rest.
	rested := false
	for range 60 * 30 {
		if !g.Step(frame) {
			rested = true
			break
		}
	}
	if !rested || g.spec.heardSm[k] > specBottom {
		t.Fatalf("the spectrum stays at %v once the sound stops", g.spec.heardSm[k])
	}
}

func TestTheSpectrumHoldsStillBetweenTheChunksTheSoundComesIn(t *testing.T) {
	p, h := newEQ(t)
	in, out := &tone{level: 0.25}, &tone{level: 0.5}
	h.in, h.out = in, out
	g := newGraph(p, h)
	k := 0
	for i, f := range g.spec.freqs {
		if math.Abs(float64(f)-1000) < math.Abs(float64(g.spec.freqs[k])-1000) {
			k = i
		}
	}
	// At 144 frames a second, a chunk of 1024 frames every third: the
	// frames between hear nothing new.
	frame := time.Second / 144
	low, high := float32(0), float32(-200)
	for i := range 144 * 2 {
		if i%3 == 0 {
			in.at += 1024
			out.at += 1024
		}
		g.Step(frame)
		if i >= 144 {
			low, high = min(low, g.spec.heardSm[k]), max(high, g.spec.heardSm[k])
		}
	}
	if high-low > 0.5 {
		t.Errorf("between chunks the spectrum at 1 kHz swings %.1f dB, from %.1f to %.1f", high-low, low, high)
	}
}
