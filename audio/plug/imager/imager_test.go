package imager

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugtest"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// host is a host for tests, which notes the gestures it is sent.
type host struct {
	values map[uint32]float64
	log    []string
	out    plug.Sound
}

func (h *host) Param(id uint32) float64 {
	if v, ok := h.values[id]; ok {
		return v
	}
	return param(id).Default
}
func (h *host) Begin(uint32, *gunim.UI) { h.log = append(h.log, "begin") }
func (h *host) Set(id uint32, v float64, _ *gunim.UI) {
	h.values[id] = v
	h.log = append(h.log, "set")
}
func (h *host) End(uint32, *gunim.UI)                { h.log = append(h.log, "end") }
func (h *host) BeginEdit(string, *gunim.UI)          {}
func (h *host) Changed(*gunim.UI)                    {}
func (h *host) EndEdit(*gunim.UI)                    {}
func (h *host) Sound() (in, out plug.Sound)          { return nil, h.out }
func (h *host) Traced(uint32, int64) (float32, bool) { return 0, false }

func newImager(t *testing.T) (*plugin, *host) {
	t.Helper()
	d, ok := plug.Lookup("marrasen.imager")
	if !ok {
		t.Fatal("the imager did not register itself")
	}
	h := &host{values: map[uint32]float64{}}
	p, ok := d.New(h).(*plugin)
	if !ok {
		t.Fatal("the imager made something else")
	}
	return p, h
}

func set(kv ...float64) []plug.Change {
	var out []plug.Change
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, plug.Change{ID: uint32(kv[i]), Value: kv[i+1]})
	}
	return out
}

// tone returns a second of a stereo sine at hz, its left at l and its
// right at r, the right a shift of phase behind.
func tone(hz, l, r, shift float64) [2][]float32 {
	var out [2][]float32
	for c := range out {
		out[c] = make([]float32, 48000)
	}
	for i := range out[0] {
		ph := 2 * math.Pi * hz * float64(i) / 48000
		out[0][i] = float32(l * math.Sin(ph))
		out[1][i] = float32(r * math.Sin(ph-shift))
	}
	return out
}

// through plays in through a fresh imager set by changes, and returns
// the last quarter second of what comes out, and of what went in.
func through(t *testing.T, in [2][]float32, changes []plug.Change) (out, was [2][]float32) {
	t.Helper()
	p, _ := newImager(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	for at := 0; at < len(in[0]); at += 512 {
		n := min(512, len(in[0])-at)
		ch := [][]float32{slices.Clone(in[0][at : at+n]), slices.Clone(in[1][at : at+n])}
		pr.Process(&plug.Block{In: ch, Out: ch, Frames: n, Changes: changes})
		changes = nil
		out[0], out[1] = append(out[0], ch[0]...), append(out[1], ch[1]...)
	}
	for c := range out {
		out[c], was[c] = out[c][36000:], in[c][36000:]
	}
	return out, was
}

// midSide returns the levels of x's middle and sides, in decibels.
func midSide(x [2][]float32) (mid, side float64) {
	var m, s float64
	for i := range x[0] {
		a, b := float64(x[0][i]+x[1][i])/2, float64(x[0][i]-x[1][i])/2
		m, s = m+a*a, s+b*b
	}
	n := float64(len(x[0]))
	return 10 * math.Log10(m/n+1e-20), 10 * math.Log10(s/n+1e-20)
}

func TestAsSetTheImagerLeavesTheImageAsItWas(t *testing.T) {
	for _, hz := range []float64{60, 300, 1200, 4000, 12000} {
		out, was := through(t, tone(hz, 0.5, 0.3, 0.4), nil)
		mo, so := midSide(out)
		mw, sw := midSide(was)
		if math.Abs(mo-mw) > 0.1 || math.Abs(so-sw) > 0.1 {
			t.Errorf("at %v Hz, as set, the middle changes %+.2f dB and the sides %+.2f dB", hz, mo-mw, so-sw)
		}
	}
}

func TestEachBandsWidthScalesItsSidesAlone(t *testing.T) {
	// Band 3, high mid, from 1.2 kHz to 6 kHz: a tone in its middle,
	// made mono, twice as wide, half as wide. The crossovers' slopes let
	// a little of it through the bands either side, left as they were.
	for _, c := range []struct {
		width, want float64
	}{{-100, -120}, {100, 6.02}, {-50, -6.02}} {
		out, was := through(t, tone(2683, 0.5, 0.3, 0.4), set(float64(WidthID+2), c.width))
		mo, so := midSide(out)
		mw, sw := midSide(was)
		if math.Abs(mo-mw) > 0.1 {
			t.Errorf("at a width of %v%% the middle changes %+.2f dB", c.width, mo-mw)
		}
		switch {
		case c.want < -100 && so-sw > -20:
			t.Errorf("at a width of -100%% the sides fall %.1f dB, want at least 20", so-sw)
		case c.want > -100 && math.Abs(so-sw-c.want) > 0.6:
			t.Errorf("at a width of %v%% the sides change %+.2f dB, want %+.2f", c.width, so-sw, c.want)
		}
		// Another band's tone is left as it was.
		out, was = through(t, tone(100, 0.5, 0.3, 0.4), set(float64(WidthID+2), c.width))
		_, so = midSide(out)
		_, sw = midSide(was)
		if math.Abs(so-sw) > 0.3 {
			t.Errorf("a tone at 100 Hz, in the low band, has its sides changed %+.2f dB by the high mid's width", so-sw)
		}
	}
}

func TestStereoizeWidensMonoAndLeavesItsMonoMixAlone(t *testing.T) {
	out, was := through(t, tone(3000, 0.4, 0.4, 0), set(float64(StereoizeID+2), 100))
	mo, so := midSide(out)
	mw, _ := midSide(was)
	if so < mw-10 {
		t.Errorf("stereoized, a mono tone's sides are at %.1f dB, its middle %.1f dB: hardly wider", so, mw)
	}
	if math.Abs(mo-mw) > 0.1 {
		t.Errorf("stereoized, the mono mix changes %+.2f dB", mo-mw)
	}
}

func TestStereoizeAddsTheMiddleLateNotPanned(t *testing.T) {
	// Mono noise, its high band stereoized: the sides are that band's
	// middle, as late as the band's stereoize, alike to the middle then
	// and not at once, as a middle added in time, which only pans it,
	// would be.
	r := uint64(1)
	var in [2][]float32
	for c := range in {
		in[c] = make([]float32, 48000)
	}
	for i := range in[0] {
		r = r*6364136223846793005 + 1442695040888963407
		v := float32(int32(r>>33)) / float32(1<<31) * 0.3
		in[0][i], in[1][i] = v, v
	}
	out, _ := through(t, in, set(float64(StereoizeID+3), 100))
	n := len(out[0])
	mid, side := make([]float64, n), make([]float64, n)
	for i := range n {
		mid[i], side[i] = float64(out[0][i]+out[1][i])/2, float64(out[0][i]-out[1][i])/2
	}
	alike := func(lag int) float64 {
		var ms, mm, ss float64
		for i := lag; i < n; i++ {
			ms, mm, ss = ms+side[i]*mid[i-lag], mm+mid[i-lag]*mid[i-lag], ss+side[i]*side[i]
		}
		return ms / math.Sqrt(mm*ss)
	}
	late := int(math.Round(stereoizeMS[3] / 1000 * 48000))
	if now, then := alike(0), alike(late); then < now+0.2 {
		t.Errorf("the sides are alike to the middle %.2f at once and %.2f %d frames late: not added late", now, then, late)
	}
}

func TestTheCrossoversKeepInOrder(t *testing.T) {
	p, _ := newImager(t)
	pr := &p.proc
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 64, Channels: 2})
	ch := [][]float32{make([]float32, 64), make([]float32, 64)}
	pr.Process(&plug.Block{In: ch, Out: ch, Frames: 64, Changes: set(float64(CrossoverID), 900, float64(CrossoverID+1), 300)})
	if pr.x[1] < pr.x[0]*math.Sqrt2 {
		t.Errorf("crossovers set at 900 Hz and 300 Hz work at %.0f Hz and %.0f Hz: out of order", pr.x[0], pr.x[1])
	}
}

func TestProcessingAllocatesNothing(t *testing.T) {
	p, _ := newImager(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	in := tone(1000, 0.5, 0.3, 0.4)
	ch := [][]float32{in[0][:512], in[1][:512]}
	blocks := make([]*plug.Block, 64)
	for i := range blocks {
		blocks[i] = &plug.Block{In: ch, Out: ch, Frames: 512,
			Changes: set(float64(CrossoverID+uint32(i%3)), float64(100+i*40), float64(WidthID+uint32(i%4)), float64(i*3-90),
				float64(StereoizeID+uint32(i%4)), float64(i))}
	}
	made := plugtest.AllocsUnder(t, "imager.(*processor).Process", func() {
		for _, b := range blocks {
			pr.Process(b)
		}
	})
	if made != 0 {
		t.Errorf("processing made %d things over 64 blocks", made)
	}
}

func TestTheCorrelationReadsAlikeApartAndOpposite(t *testing.T) {
	for _, c := range []struct {
		shift, want float64
	}{{0, 1}, {math.Pi / 2, 0}, {math.Pi, -1}} {
		x := tone(1000, 0.5, 0.5, c.shift)
		frames := make([]float32, 0, 2*4800)
		for i := range 4800 {
			frames = append(frames, x[0][i], x[1][i])
		}
		if got := correlation(frames); math.Abs(got-c.want) > 0.02 {
			t.Errorf("channels %.2f apart in phase correlate %+.2f, want %+.0f", c.shift, got, c.want)
		}
	}
}

func TestTheDisplayDragsABandsWidthAndACrossover(t *testing.T) {
	p, h := newImager(t)
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	d := pn.disp
	d.Layout(gunim.Constraints{Max: geom.Sz(1200, 600)}, gunim.Frame{}, gunim.Children{})
	d.line = 250
	// The high band, dragged up by half its reach: +50%.
	at := geom.Pt(d.xOf(10000), d.line)
	d.Handle(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1}, nil)
	d.Handle(input.PointerMove{Pos: at.Add(geom.Pt(0, -d.reach()/2))}, nil)
	d.Handle(input.PointerUp{Pos: at, Button: input.ButtonPrimary}, nil)
	if got := h.values[WidthID+3]; math.Abs(got-50) > 0.5 || !slices.Equal(h.log, []string{"begin", "set", "end"}) {
		t.Errorf("dragged up half its reach, the high band's width is %v, by %v", got, h.log)
	}
	// A double-click sets it back.
	h.log = nil
	d.Handle(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 2}, nil)
	if h.values[WidthID+3] != 0 {
		t.Errorf("double-clicked, the high band's width is %v", h.values[WidthID+3])
	}
	// The second crossover, dragged to 2 kHz.
	h.log = nil
	x := geom.Pt(d.xOf(1200), d.line)
	d.Handle(input.PointerDown{Pos: x, Button: input.ButtonPrimary, Clicks: 1}, nil)
	d.Handle(input.PointerMove{Pos: geom.Pt(d.xOf(2000), d.line)}, nil)
	d.Handle(input.PointerUp{Pos: x, Button: input.ButtonPrimary}, nil)
	if got := h.values[CrossoverID+1]; math.Abs(got-2000) > 5 || !slices.Equal(h.log, []string{"begin", "set", "end"}) {
		t.Errorf("dragged to 2 kHz, the second crossover is at %v, by %v", got, h.log)
	}
}

func TestTheDisplayLightsWhatThePointerIsOnGliding(t *testing.T) {
	p, h := newImager(t)
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	d := pn.disp
	d.Layout(gunim.Constraints{Max: geom.Sz(1200, 600)}, gunim.Frame{}, gunim.Children{})
	d.line = 250
	d.Handle(input.PointerMove{Pos: geom.Pt(d.xOf(1200), 100)}, nil)
	d.group.Step(time.Second / 60)
	if v := d.xLit[1].Value(); v <= 0 || v >= 0.99 {
		t.Errorf("a frame after the pointer came onto the crossover, it is lit %v: not gliding", v)
	}
	for range 30 {
		d.group.Step(time.Second / 60)
	}
	if d.xLit[1].Value() < 0.99 {
		t.Errorf("on the crossover, it is lit %v", d.xLit[1].Value())
	}
	// Under the bars' reach, where the controls stand, no band.
	d.Handle(input.PointerMove{Pos: geom.Pt(d.xOf(10000), d.line+d.reach()+20)}, nil)
	if d.hotBand != -1 {
		t.Errorf("under the bars' reach, band %d is lit", d.hotBand)
	}
	d.Handle(input.PointerMove{Pos: geom.Pt(d.xOf(10000), d.line)}, nil)
	for range 30 {
		d.group.Step(time.Second / 60)
	}
	if d.bandLit[3].Value() < 0.99 || d.xLit[1].Value() > 0.01 {
		t.Errorf("on the high band, it is lit %v, the crossover %v", d.bandLit[3].Value(), d.xLit[1].Value())
	}
}
