package enhancer

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
func (h *host) Sound() (in, out plug.Sound)          { return nil, nil }
func (h *host) Traced(uint32, int64) (float32, bool) { return 0, false }

func newEnhancer(t *testing.T) (*plugin, *host) {
	t.Helper()
	d, ok := plug.Lookup("marrasen.enhancer")
	if !ok {
		t.Fatal("the enhancer did not register itself")
	}
	h := &host{values: map[uint32]float64{}}
	p, ok := d.New(h).(*plugin)
	if !ok {
		t.Fatal("the enhancer made something else")
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

// tone returns a second of a sine at hz and level a, alike in both
// channels.
func tone(hz, a float64) [2][]float32 {
	var out [2][]float32
	for c := range out {
		out[c] = make([]float32, 48000)
	}
	for i := range out[0] {
		v := float32(a * math.Sin(2*math.Pi*hz*float64(i)/48000))
		out[0][i], out[1][i] = v, v
	}
	return out
}

// noise returns a second of noise at a, alike in both channels.
func noise(a float64) [2][]float32 {
	var out [2][]float32
	for c := range out {
		out[c] = make([]float32, 48000)
	}
	r := uint64(1)
	for i := range out[0] {
		r = r*6364136223846793005 + 1442695040888963407
		v := float32(float64(int32(r>>33)) / (1 << 31) * a)
		out[0][i], out[1][i] = v, v
	}
	return out
}

// through plays in through pr, set by changes, and returns the last
// half second of what comes out.
func through(pr plug.Processor, in [2][]float32, changes []plug.Change) [2][]float32 {
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	var out [2][]float32
	for at := 0; at < len(in[0]); at += 512 {
		n := min(512, len(in[0])-at)
		ch := [][]float32{slices.Clone(in[0][at : at+n]), slices.Clone(in[1][at : at+n])}
		pr.Process(&plug.Block{In: ch, Out: ch, Frames: n, Changes: changes,
			Position: plug.Position{Frame: int64(at)}})
		changes = nil
		out[0], out[1] = append(out[0], ch[0]...), append(out[1], ch[1]...)
	}
	for c := range out {
		out[c] = out[c][24000:]
	}
	return out
}

func run(t *testing.T, in [2][]float32, changes []plug.Change) [2][]float32 {
	t.Helper()
	p, _ := newEnhancer(t)
	return through(p.Processor(), in, changes)
}

// level returns x's level at hz, in decibels, a sine of 1 at 0.
func level(x []float32, hz float64) float64 {
	var re, im float64
	for i, v := range x {
		ph := 2 * math.Pi * hz * float64(i) / 48000
		re, im = re+float64(v)*math.Cos(ph), im+float64(v)*math.Sin(ph)
	}
	return 20 * math.Log10(2*math.Hypot(re, im)/float64(len(x))+1e-12)
}

func TestAtNoAmountTheSoundPassesExactly(t *testing.T) {
	in := noise(0.5)
	out := run(t, in, set(float64(StyleID+1), Tube, float64(CrossoverID), 300))
	for c := range out {
		if !slices.Equal(out[c], in[c][24000:]) {
			t.Fatalf("with no band's amount up, channel %d comes out changed", c)
		}
	}
}

func TestEachIntegralIsItsCurves(t *testing.T) {
	for s := range styles {
		if d := (curve(s, 1e-6) - curve(s, -1e-6)) / 2e-6; math.Abs(d-1) > 1e-6 || curve(s, 0) != 0 {
			t.Errorf("%s's curve is %v at 0 and rises %v through it, want 0 and 1", styleNames[s], curve(s, 0), d)
		}
		for u := -6.0; u <= 6; u += 0.25 {
			const e = 1e-5
			d := (integral(s, u+e) - integral(s, u-e)) / (2 * e)
			if math.Abs(d-curve(s, u)) > 1e-5 {
				t.Errorf("%s's integral rises %v at %v, but its curve is %v there", styleNames[s], d, u, curve(s, u))
			}
		}
	}
}

func TestABandAddsHarmonicsToItsOwnPitches(t *testing.T) {
	// A tone at 300 Hz, in the low mid: its third harmonic, at 900 Hz,
	// comes with the low mid's amount, and hardly with the low's.
	own := run(t, tone(300, 0.5), set(float64(AmountID+1), 50))
	other := run(t, tone(300, 0.5), set(float64(AmountID), 50))
	h3own, h3other := level(own[0], 900), level(other[0], 900)
	if h3own < -45 {
		t.Errorf("the low mid at 50%% adds a third harmonic at %.1f dB, want over -45", h3own)
	}
	if h3other > h3own-30 {
		t.Errorf("the low band at 50%% adds a third harmonic at %.1f dB to a low mid tone, against %.1f dB from its own",
			h3other, h3own)
	}
}

func TestEachStyleAddsItsOwnHarmonics(t *testing.T) {
	h := map[int][8]float64{}
	for _, s := range []int{Tape, Tube, Crisp} {
		out := run(t, tone(300, 0.5), set(float64(AmountID+1), 30, float64(StyleID+1), float64(s)))
		var lv [8]float64
		for k := 2; k < 8; k++ {
			lv[k] = level(out[0], float64(k)*300)
		}
		h[s] = lv
	}
	if tape := h[Tape]; tape[3] < tape[2]+30 {
		t.Errorf("tape adds the second harmonic at %.1f dB and the third at %.1f dB: want odd ones", tape[2], tape[3])
	}
	if tube := h[Tube]; tube[2] < tube[3]+3 {
		t.Errorf("tube adds the second harmonic at %.1f dB and the third at %.1f dB: want the second first",
			tube[2], tube[3])
	}
	if tape, crisp := h[Tape], h[Crisp]; crisp[7]-crisp[3] < tape[7]-tape[3]+6 {
		t.Errorf("crisp's seventh harmonic is %.1f dB under its third, tape's %.1f dB: want crisp's reaching higher",
			crisp[3]-crisp[7], tape[3]-tape[7])
	}
}

func TestTheCurvesMeanKeepsAliasesDown(t *testing.T) {
	// A tone at 15 kHz, driven hard: its third harmonic, at 45 kHz, is
	// over what 48 kHz can hold, and folds back to 3 kHz.
	out := run(t, tone(15000, 0.5), set(float64(AmountID+3), 100, float64(StyleID+3), Crisp))
	if alias := level(out[0], 3000) - level(out[0], 15000); alias > -40 {
		t.Errorf("the third harmonic of a tone at 15 kHz folds back to 3 kHz at %.1f dB under it, want 40", -alias)
	}
}

func TestTheMixAndTheOutput(t *testing.T) {
	in := noise(0.3)
	out := run(t, in, set(float64(AmountID+2), 80, float64(MixID), 0))
	if !slices.Equal(out[0], in[0][24000:]) {
		t.Error("at a mix of none, the sound comes out changed")
	}
	out = run(t, in, set(float64(OutputID), 6))
	for i, v := range out[0] {
		if want := in[0][24000+i] * float32(math.Pow(10, 6.0/20)); math.Abs(float64(v-want)) > 1e-6 {
			t.Fatalf("at an output of +6 dB, frame %d comes out %v, want %v", i, v, want)
		}
	}
}

func TestALeaningCurveAddsNoOffset(t *testing.T) {
	out := run(t, tone(50, 0.7), set(float64(AmountID), 100, float64(StyleID), Tube))
	var sum float64
	for _, v := range out[0] {
		sum += float64(v)
	}
	if mean := sum / float64(len(out[0])); math.Abs(mean) > 1e-3 {
		t.Errorf("tube on a low tone puts it off centre by %v", mean)
	}
}

func TestEachBandTracesWhatItAdds(t *testing.T) {
	p, _ := newEnhancer(t)
	through(p.Processor(), tone(300, 0.5), set(float64(AmountID+1), 50))
	tr := p.proc.Trace(AddedID + 1)
	v, ok := tr.Get(47000)
	if !ok || -v < -40 || -v > -6 {
		t.Errorf("the low mid at 50%% traces %v dB added, %v; want between -40 and -6", -v, ok)
	}
	if v, ok := p.proc.Trace(AddedID).Get(47000); !ok || -v > addedFloor+1 {
		t.Errorf("the low band, at none, traces %v dB added, %v", -v, ok)
	}
	if p.proc.Trace(MixID) != nil {
		t.Error("the mix has a trace")
	}
}

func TestProcessingAllocatesNothing(t *testing.T) {
	p, _ := newEnhancer(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	in := noise(0.5)
	ch := [][]float32{in[0][:512], in[1][:512]}
	blocks := make([]*plug.Block, 64)
	for i := range blocks {
		blocks[i] = &plug.Block{In: ch, Out: ch, Frames: 512, Position: plug.Position{Frame: int64(i * 512)},
			Changes: set(float64(CrossoverID+uint32(i%3)), float64(100+i*40), float64(AmountID+uint32(i%4)), float64(i),
				float64(StyleID+uint32(i%4)), float64(i%styles))}
	}
	made := plugtest.AllocsUnder(t, "enhancer.(*processor).Process", func() {
		for _, b := range blocks {
			pr.Process(b)
		}
	})
	if made != 0 {
		t.Errorf("processing made %d things over 64 blocks", made)
	}
}

func TestTheHarmonicsShownAreTheOnesAdded(t *testing.T) {
	// A tone at harmonicsTone in the low mid, where the band passes it
	// whole: the harmonics the processor adds to it are the ones shown.
	for _, s := range []int{Tape, Tube, Crisp} {
		for _, a := range []float64{20, 60} {
			out := run(t, tone(300, harmonicsTone), set(float64(AmountID+1), a, float64(StyleID+1), float64(s)))
			shown := harmonicsOf(s, a/100)
			for k := 2; k <= 3; k++ {
				got := level(out[0], float64(k)*300) - 20*math.Log10(harmonicsTone)
				if want := shown[k-2]; want > -70 && math.Abs(got-want) > 1.5 {
					t.Errorf("%s at %v%% adds harmonic %d at %.1f dB, but shows it at %.1f dB", styleNames[s], a, k, got, want)
				}
			}
		}
	}
	if none := harmonicsOf(Crisp, 0); none[1] > harmonicsFloor+1 {
		t.Errorf("at none, crisp shows a third harmonic at %.1f dB", none[1])
	}
}

func TestTheDisplayDragsABandsAmountAndACrossover(t *testing.T) {
	p, h := newEnhancer(t)
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	d := pn.disp
	d.Layout(gunim.Constraints{Max: geom.Sz(1200, 600)}, gunim.Frame{}, gunim.Children{})
	d.line = 400
	// The high band, dragged up by half its reach: 50%.
	at := geom.Pt(d.xOf(10000), d.line)
	d.Handle(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1}, nil)
	d.Handle(input.PointerMove{Pos: at.Add(geom.Pt(0, -d.reach()/2))}, nil)
	d.Handle(input.PointerUp{Pos: at, Button: input.ButtonPrimary}, nil)
	if got := h.values[AmountID+3]; math.Abs(got-50) > 0.5 || !slices.Equal(h.log, []string{"begin", "set", "end"}) {
		t.Errorf("dragged up half its reach, the high band's amount is %v, by %v", got, h.log)
	}
	// A double-click sets it back.
	h.log = nil
	d.Handle(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 2}, nil)
	if h.values[AmountID+3] != 0 {
		t.Errorf("double-clicked, the high band's amount is %v", h.values[AmountID+3])
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

func TestTheCurveShowsTheBandTouched(t *testing.T) {
	p, h := newEnhancer(t)
	h.values[AmountID+2] = 40
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	if pn.touched != 2 {
		t.Errorf("opened, the curve shows band %d, not the one set most, 2", pn.touched)
	}
	pn.Follow(StyleID, Crisp, nil)
	if pn.touched != 0 || pn.styles[0].Selected() != Crisp {
		t.Errorf("the low band's style set to crisp, the curve shows band %d and its selector %d", pn.touched,
			pn.styles[0].Selected())
	}
}

func TestTheCurveFollowsTheBarsAndGlidesBetweenThem(t *testing.T) {
	p, h := newEnhancer(t)
	h.values[AmountID] = 60
	h.values[AmountID+3], h.values[StyleID+3] = 20, Crisp
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	d, c := pn.disp, pn.curve
	d.Layout(gunim.Constraints{Max: geom.Sz(1200, 600)}, gunim.Frame{}, gunim.Children{})
	d.line, d.top = 400, 100
	step := func(n int) {
		for range n {
			c.Step(time.Second / 60)
			d.group.Step(time.Second / 60)
		}
	}
	if c.band != 0 {
		t.Fatalf("opened, the curve shows band %d, not the one set most, 0", c.band)
	}
	// On a crossover: lit, and the curve stays.
	d.Handle(input.PointerMove{Pos: geom.Pt(d.xOf(1200), 300)}, nil)
	step(30)
	if d.xLit[1].Value() < 0.99 || c.band != 0 {
		t.Errorf("on the second crossover, it is lit %v, and the curve shows band %d", d.xLit[1].Value(), c.band)
	}
	// Under the line, where the controls stand, no band.
	d.Handle(input.PointerMove{Pos: geom.Pt(d.xOf(10000), d.line+30)}, nil)
	if d.hotBand != -1 || c.band != 0 {
		t.Errorf("under the bars, band %d is lit, and the curve shows band %d", d.hotBand, c.band)
	}
	// On the high band's bar: the curve turns to it, gliding, and keeps
	// it as the pointer goes on under the line.
	d.Handle(input.PointerMove{Pos: geom.Pt(d.xOf(10000), 350)}, nil)
	step(3)
	if a := c.amount.Value(); c.band != 3 || a <= 0.21 || a >= 0.59 || c.swap.Value() > 0.9 || c.was == "" {
		t.Errorf("partway to the high band, the curve shows band %d at %v, its title swapped %v from %q",
			c.band, a, c.swap.Value(), c.was)
	}
	d.Handle(input.PointerMove{Pos: geom.Pt(d.xOf(10000), d.line+30)}, nil)
	step(60)
	if c.band != 3 || math.Abs(float64(c.amount.Value())-0.2) > 0.01 || c.weight[Crisp].Value() < 0.99 {
		t.Errorf("after, the curve shows band %d at %v, crisp at %v", c.band, c.amount.Value(), c.weight[Crisp].Value())
	}
	if d.bandLit[3].Value() > 0.01 || d.xLit[1].Value() > 0.01 {
		t.Errorf("off them, the high band is lit %v and the crossover %v", d.bandLit[3].Value(), d.xLit[1].Value())
	}
}
