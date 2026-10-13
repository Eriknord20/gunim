package impact

import (
	"math"
	"math/cmplx"
	"slices"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugtest"
	"github.com/marrasen/gunim/audio/plug/xover"
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

func newImpact(t *testing.T) (*plugin, *host) {
	t.Helper()
	d, ok := plug.Lookup("marrasen.impact")
	if !ok {
		t.Fatal("Impact did not register itself")
	}
	h := &host{values: map[uint32]float64{}}
	p, ok := d.New(h).(*plugin)
	if !ok {
		t.Fatal("Impact made something else")
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

// every sets each band's punch to v.
func every(v float64) []plug.Change {
	return set(float64(PunchID), v, float64(PunchID+1), v, float64(PunchID+2), v, float64(PunchID+3), v)
}

// tone returns n frames of a sine at hz and level a, alike in both
// channels.
func tone(hz, a float64, n int) [2][]float32 {
	var out [2][]float32
	for c := range out {
		out[c] = make([]float32, n)
	}
	for i := range out[0] {
		v := float32(a * math.Sin(2*math.Pi*hz*float64(i)/48000))
		out[0][i], out[1][i] = v, v
	}
	return out
}

// noise returns n frames of noise at a, alike in both channels, as
// often under zero as over.
func noise(a float64, n int) [2][]float32 {
	var out [2][]float32
	for c := range out {
		out[c] = make([]float32, n)
	}
	r := uint64(1)
	for i := range out[0] {
		r = r*6364136223846793005 + 1442695040888963407
		v := float32(float64(int32(r>>32)) / (1 << 31) * a)
		out[0][i], out[1][i] = v, v
	}
	return out
}

// The hits the tests play: one every hitEvery frames, for two seconds,
// each a tone that starts at once and dies away over hitDecay seconds,
// as a drum does.
const (
	hitEvery = 14400
	hitDecay = 0.06
	hitHz    = 2500
)

// hits returns two seconds of hits at hz and level a, alike in both
// channels.
func hits(hz, a float64) [2][]float32 {
	var out [2][]float32
	for c := range out {
		out[c] = make([]float32, 96000)
	}
	for i := range out[0] {
		t := float64(i%hitEvery) / 48000
		v := float32(a * math.Exp(-t/hitDecay) * math.Sin(2*math.Pi*hz*t))
		out[0][i], out[1][i] = v, v
	}
	return out
}

// play plays in through pr, set by changes, and returns all that comes
// out.
func play(pr plug.Processor, in [2][]float32, changes []plug.Change) [2][]float32 {
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
	return out
}

// through is play, returning the last half second of what comes out.
func through(pr plug.Processor, in [2][]float32, changes []plug.Change) [2][]float32 {
	out := play(pr, in, changes)
	for c := range out {
		out[c] = out[c][len(out[c])-24000:]
	}
	return out
}

// finite fails the test where any frame of out is not a number, or is
// infinite.
func finite(t *testing.T, out [2][]float32) {
	t.Helper()
	for c := range out {
		for i, v := range out[c] {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("channel %d, frame %d, comes out %v", c, i, v)
			}
		}
	}
}

// run plays in through a new Impact, set by changes, and returns the
// last half second of what comes out, which must all be numbers.
func run(t *testing.T, in [2][]float32, changes []plug.Change) [2][]float32 {
	t.Helper()
	p, _ := newImpact(t)
	out := through(p.Processor(), in, changes)
	finite(t, out)
	return out
}

// runAll is run, returning all that comes out.
func runAll(t *testing.T, in [2][]float32, changes []plug.Change) [2][]float32 {
	t.Helper()
	p, _ := newImpact(t)
	out := play(p.Processor(), in, changes)
	finite(t, out)
	return out
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

// rms returns the level of x from frame from to frame to, in decibels.
func rms(x []float32, from, to int) float64 {
	var sum float64
	for _, v := range x[from:to] {
		sum += float64(v) * float64(v)
	}
	return 10 * math.Log10(sum/float64(to-from)+1e-30)
}

// fft returns x's spectrum; x's length is a power of two.
func fft(x []complex128) []complex128 {
	n := len(x)
	if n == 1 {
		return []complex128{x[0]}
	}
	even, odd := make([]complex128, n/2), make([]complex128, n/2)
	for i := range n / 2 {
		even[i], odd[i] = x[2*i], x[2*i+1]
	}
	e, o := fft(even), fft(odd)
	out := make([]complex128, n)
	for k := range n / 2 {
		w := cmplx.Exp(complex(0, -2*math.Pi*float64(k)/float64(n))) * o[k]
		out[k], out[k+n/2] = e[k]+w, e[k]-w
	}
	return out
}

// octaves returns x's level in each octave from 31.25 Hz up, in
// decibels: the power of its spectrum, windowed, summed over each.
func octaves(x []float32) []float64 {
	const n = 1 << 15
	in := make([]complex128, n)
	for i := range n {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/n)
		in[i] = complex(float64(x[len(x)-n+i])*w, 0)
	}
	s := fft(in)
	var out []float64
	for lo := 31.25; lo < 20000; lo *= 2 {
		var sum float64
		for k := int(lo * n / 48000); k < int(2*lo*n/48000) && k < n/2; k++ {
			sum += real(s[k])*real(s[k]) + imag(s[k])*imag(s[k])
		}
		out = append(out, 10*math.Log10(sum+1e-30))
	}
	return out
}

func TestAtNoPunchTheSoundComesOutFlat(t *testing.T) {
	in := noise(0.5, 96000)
	out := runAll(t, in, set(float64(CrossoverID), 300, float64(LengthID), 20))
	want, got := octaves(in[0]), octaves(out[0])
	for i := range want {
		if d := got[i] - want[i]; !(math.Abs(d) <= 0.1) {
			t.Errorf("at no punch, the octave from %.0f Hz comes out %+.2f dB", 31.25*math.Pow(2, float64(i)), d)
		}
	}
	for _, hz := range []float64{60, 300, 1000, 1200, 6000, 12000} {
		out := run(t, tone(hz, 0.5, 48000), nil)
		if d := level(out[1], hz) - 20*math.Log10(0.5); !(math.Abs(d) <= 0.1) {
			t.Errorf("at no punch, a tone at %v Hz comes out %+.2f dB", hz, d)
		}
	}
}

func TestASteadyToneKeepsItsLevelAndGainsNoHarmonics(t *testing.T) {
	// Two seconds, for the low band's loose follower to settle.
	for _, hz := range []float64{60, 1000} {
		flat := level(run(t, tone(hz, 0.5, 96000), nil)[0], hz)
		for _, p := range []float64{100, -100} {
			out := run(t, tone(hz, 0.5, 96000), every(p))
			lv := level(out[0], hz)
			if d := lv - flat; !(math.Abs(d) <= 0.1) {
				t.Errorf("at %+.0f%% punch, a steady tone at %v Hz comes out %+.2f dB", p, hz, d)
			}
			for k := 2; k <= 5; k++ {
				if h := level(out[0], float64(k)*hz) - lv; !(h <= -50) {
					t.Errorf("at %+.0f%% punch, a tone at %v Hz gains its harmonic %d at %.1f dB", p, hz, k, h)
				}
			}
		}
	}
}

// attack returns, over the hits from one second into x to two, the level of
// each hit's first 10 ms over that of 50 to 100 ms after it starts, in
// decibels: how far its attack stands over its tail.
func attack(x []float32) float64 {
	var sum float64
	n := 0
	for at := 48000 + hitEvery - 48000%hitEvery; at+4800 <= len(x); at += hitEvery {
		sum += rms(x, at, at+480) - rms(x, at+2400, at+4800)
		n++
	}
	return sum / float64(n)
}

func TestPunchRaisesEachHitOverItsTail(t *testing.T) {
	in := hits(hitHz, 0.5)
	flat := attack(runAll(t, in, nil)[0])
	up := attack(runAll(t, in, set(float64(PunchID+2), 100))[0])
	down := attack(runAll(t, in, set(float64(PunchID+2), -100))[0])
	if !(up >= flat+6) {
		t.Errorf("at +100%% punch, a hit's attack stands %.1f dB over its tail, at none %.1f dB: want 6 dB more",
			up, flat)
	}
	if !(down <= flat-6) {
		t.Errorf("at -100%% punch, a hit's attack stands %.1f dB over its tail, at none %.1f dB: want 6 dB less",
			down, flat)
	}
	// Another band's punch hardly touches it, with the crossovers
	// either side of it over two octaves off. What the band beside it
	// holds of it, under their slopes, rises and falls as it does, and is
	// punched as much, however quiet: only the slopes keep it small.
	apart := set(float64(CrossoverID+1), 500, float64(CrossoverID+2), 12000)
	alone := attack(runAll(t, in, apart)[0])
	for _, p := range []float64{100, -100} {
		for _, b := range []int{0, 1, 3} {
			other := attack(runAll(t, in, append(slices.Clone(apart), set(float64(PunchID+uint32(b)), p)...))[0])
			if !(math.Abs(other-alone) <= 0.5) {
				t.Errorf("at %+.0f%% punch in the %s band, a high mid hit's attack stands %.2f dB over its tail, "+
					"at none %.2f dB", p, bandNames[b], other, alone)
			}
		}
	}
}

func TestTheLengthIsHowLongAHitIsPunched(t *testing.T) {
	// 60 ms into a hit, a short length has let it go, and a long one
	// still turns it up.
	turned := func(length float64) float32 {
		p, _ := newImpact(t)
		finite(t, play(p.Processor(), hits(hitHz, 0.5), set(float64(PunchID+2), 100, float64(LengthID), length)))
		v, ok := p.proc.Trace(ChangeID + 2).Get(4*hitEvery + 60*48)
		if !ok {
			t.Fatalf("at a length of %v ms, the high mid traces nothing", length)
		}
		return -v
	}
	if short, long := turned(20), turned(200); !(short <= 1 && long >= 6) {
		t.Errorf("60 ms into a hit, it is turned %.1f dB at a length of 20 ms and %.1f dB at 200 ms", short, long)
	}
}

func TestTheChannelsAreTurnedAlike(t *testing.T) {
	// Hits in the left channel alone, and a steady tone, quieter, in
	// the right, both in the high mid: the right is turned as the left
	// is, by the left's hits.
	in := hits(hitHz, 0.5)
	in[1] = tone(hitHz*1.1, 0.05, 96000)[1]
	flat := runAll(t, in, nil)
	out := runAll(t, in, set(float64(PunchID+2), 100))
	swing := [2][2]float64{{99, -99}, {99, -99}}
	for at := 48000; at+240 <= 96000; at += 240 {
		var g [2]float64
		for c := range g {
			g[c] = rms(out[c], at, at+240) - rms(flat[c], at, at+240)
			swing[c] = [2]float64{min(swing[c][0], g[c]), max(swing[c][1], g[c])}
		}
		if rms(flat[0], at, at+240) < -60 {
			continue
		}
		if d := g[1] - g[0]; !(math.Abs(d) <= 0.5) {
			t.Fatalf("at frame %d, the left is turned %+.2f dB and the right %+.2f dB", at, g[0], g[1])
		}
	}
	for c, s := range swing {
		if !(s[1]-s[0] >= 6) {
			t.Errorf("channel %d is turned from %+.1f to %+.1f dB: want the hits to turn it", c, s[0], s[1])
		}
	}
}

func TestTheMixAndTheOutput(t *testing.T) {
	in := hits(hitHz, 0.5)
	flat := runAll(t, in, nil)
	out := runAll(t, in, set(float64(PunchID+2), 100, float64(MixID), 0))
	if !slices.Equal(out[0], flat[0]) {
		t.Error("at a mix of none, the sound comes out unlike it does at no punch")
	}
	half := attack(runAll(t, in, set(float64(PunchID+2), 100, float64(MixID), 50))[0])
	full := attack(runAll(t, in, set(float64(PunchID+2), 100))[0])
	if none := attack(flat[0]); !(half > none+1 && half < full-1) {
		t.Errorf("a hit's attack stands %.1f dB over its tail at half the mix, %.1f at none and %.1f at all",
			half, none, full)
	}
	out = runAll(t, in, set(float64(OutputID), 6))
	for i, v := range out[0] {
		if want := flat[0][i] * float32(math.Pow(10, 6.0/20)); !(math.Abs(float64(v-want)) <= 1e-6) {
			t.Fatalf("at an output of +6 dB, frame %d comes out %v, want %v", i, v, want)
		}
	}
}

func TestEachBandTracesHowFarItIsTurned(t *testing.T) {
	p, _ := newImpact(t)
	play(p.Processor(), hits(hitHz, 0.5), set(float64(PunchID+2), 100))
	most := float32(0)
	for f := int64(48000); f < 96000; f += plug.TraceFrames {
		v, ok := p.proc.Trace(ChangeID + 2).Get(f)
		if !ok {
			t.Fatalf("the high mid traces nothing at frame %d", f)
		}
		most = max(most, -v)
	}
	if !(most >= 6 && most <= gainLimit) {
		t.Errorf("the high mid at +100%% traces at most %v dB turned, want from 6 to %v", most, gainLimit)
	}
	if v, ok := p.proc.Trace(ChangeID).Get(90000); !ok || v != 0 {
		t.Errorf("the low band, at no punch, traces %v dB turned, %v", -v, ok)
	}
	if p.proc.Trace(MixID) != nil {
		t.Error("the mix has a trace")
	}
}

// lagOf returns how many frames late out is most like in, from -20
// to 20, over the last half second of each.
func lagOf(in, out []float32) int {
	best, most := 99, 0.0
	for lag := -20; lag <= 20; lag++ {
		var sum float64
		for i := len(out) - 23000; i < len(out)-1000; i++ {
			sum += float64(out[i]) * float64(in[i-lag])
		}
		if math.Abs(sum) > most {
			best, most = lag, math.Abs(sum)
		}
	}
	return best
}

func TestTheSoundIsNeverLate(t *testing.T) {
	p, _ := newImpact(t)
	pr := p.Processor()
	if pr.Latency() != 0 || pr.Tail() != 0 {
		t.Errorf("the latency is %d frames and the tail %d", pr.Latency(), pr.Tail())
	}
	// The bands add back to the sound shifted in phase, which leaves it
	// most like the sound in where the crossovers' own sum is most like
	// it, a frame or so on; and punched, it comes out where it does
	// unpunched, no later.
	var split xover.Splitter
	split.Set(xover.Order([bands - 1]float64{param(CrossoverID).Default, param(CrossoverID + 1).Default,
		param(CrossoverID + 2).Default}), 48000)
	peak, most := 0, 0.0
	for i := range 64 {
		sum := 0.0
		for _, v := range split.Split(float64(onOff(i == 0))) {
			sum += v
		}
		if math.Abs(sum) > most {
			peak, most = i, math.Abs(sum)
		}
	}
	in := noise(0.5, 48000)
	flat := runAll(t, in, nil)
	if lag := lagOf(in[0], flat[0]); lag != peak || peak > 2 {
		t.Errorf("the sound out is most like the sound in %d frames late, the crossovers' own sum %d", lag, peak)
	}
	out := runAll(t, in, append(every(60), set(float64(LengthID), 20)...))
	if lag := lagOf(flat[0], out[0]); lag != 0 {
		t.Errorf("punched, the sound comes out %d frames later than unpunched", lag)
	}
}

func TestProcessingAllocatesNothing(t *testing.T) {
	p, _ := newImpact(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	in := hits(hitHz, 0.5)
	ch := [][]float32{in[0][:512], in[1][:512]}
	blocks := make([]*plug.Block, 64)
	for i := range blocks {
		blocks[i] = &plug.Block{In: ch, Out: ch, Frames: 512, Position: plug.Position{Frame: int64(i * 512)},
			Changes: set(float64(CrossoverID+uint32(i%3)), float64(100+i*40), float64(PunchID+uint32(i%4)),
				float64(i*3-90), float64(LengthID), float64(10+i*4), float64(MixID), float64(i))}
	}
	made := plugtest.AllocsUnder(t, "impact.(*processor).Process", func() {
		for _, b := range blocks {
			pr.Process(b)
		}
	})
	if made != 0 {
		t.Errorf("processing made %d things over 64 blocks", made)
	}
}

func TestTheDisplayDragsABandsPunchAndACrossover(t *testing.T) {
	p, h := newImpact(t)
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	d := pn.disp
	d.Layout(gunim.Constraints{Max: geom.Sz(1200, 600)}, gunim.Frame{}, gunim.Children{})
	d.line, d.top = 400, 100
	// The high band, dragged up by half its reach: +50%.
	at := geom.Pt(d.xOf(10000), d.middle())
	d.Handle(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1}, nil)
	d.Handle(input.PointerMove{Pos: at.Add(geom.Pt(0, -d.reach()/2))}, nil)
	d.Handle(input.PointerUp{Pos: at, Button: input.ButtonPrimary}, nil)
	if got := h.values[PunchID+3]; !(math.Abs(got-50) <= 0.5) || !slices.Equal(h.log, []string{"begin", "set", "end"}) {
		t.Errorf("dragged up half its reach, the high band's punch is %v, by %v", got, h.log)
	}
	// Down, past all its reach, finer with Shift on the way: to -100%,
	// and no further.
	h.log = nil
	d.Handle(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1}, nil)
	d.Handle(input.PointerMove{Pos: at.Add(geom.Pt(0, d.reach())), Mods: input.ModShift}, nil)
	if got := h.values[PunchID+3]; !(math.Abs(got-40) <= 0.5) {
		t.Errorf("dragged down all its reach with Shift, the high band's punch is %v, want 40", got)
	}
	d.Handle(input.PointerMove{Pos: at.Add(geom.Pt(0, 2*d.reach()))}, nil)
	d.Handle(input.PointerUp{Pos: at, Button: input.ButtonPrimary}, nil)
	if got := h.values[PunchID+3]; got != -100 || !slices.Equal(h.log, []string{"begin", "set", "set", "end"}) {
		t.Errorf("dragged down twice its reach, the high band's punch is %v, by %v", got, h.log)
	}
	// A double-click sets it back.
	h.log = nil
	d.Handle(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 2}, nil)
	if h.values[PunchID+3] != 0 || !slices.Equal(h.log, []string{"begin", "set", "end"}) {
		t.Errorf("double-clicked, the high band's punch is %v, by %v", h.values[PunchID+3], h.log)
	}
	// The second crossover, dragged to 2 kHz.
	h.log = nil
	x := geom.Pt(d.xOf(1200), d.middle())
	d.Handle(input.PointerDown{Pos: x, Button: input.ButtonPrimary, Clicks: 1}, nil)
	d.Handle(input.PointerMove{Pos: geom.Pt(d.xOf(2000), d.middle())}, nil)
	d.Handle(input.PointerUp{Pos: x, Button: input.ButtonPrimary}, nil)
	if got := h.values[CrossoverID+1]; !(math.Abs(got-2000) <= 5) || !slices.Equal(h.log, []string{"begin", "set", "end"}) {
		t.Errorf("dragged to 2 kHz, the second crossover is at %v, by %v", got, h.log)
	}
	// Under the bars, where the controls stand, nothing is dragged.
	h.log = nil
	if d.Handle(input.PointerDown{Pos: geom.Pt(d.xOf(10000), d.line+30), Button: input.ButtonPrimary, Clicks: 1}, nil) ||
		len(h.log) > 0 {
		t.Errorf("pressed under the bars, the display took it, by %v", h.log)
	}
}

func TestTheViewsFollowTheirParameters(t *testing.T) {
	p, h := newImpact(t)
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	pn.Follow(PunchID+1, -30, nil)
	if pn.disp.v.punch(1) != -30 || pn.sections[2].Knobs[0].Value() != -30 {
		t.Errorf("the low mid's punch set to -30%%, the display shows %v and its knob %v", pn.disp.v.punch(1),
			pn.sections[2].Knobs[0].Value())
	}
	c, ok := p.View(plug.Chip, h).(*chip)
	if !ok {
		t.Fatal("the chip is of another type")
	}
	c.Follow(PunchID+3, 25, nil)
	if got := c.Access().Name; got != "Low 0% Low mid 0% High mid 0% High +25% " {
		t.Errorf("the chip reads %q", got)
	}
}

// BenchmarkProcess processes a second of stereo sound at 48 kHz, every
// band punched or smoothed.
func BenchmarkProcess(b *testing.B) {
	pr := &processor{}
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	in := hits(hitHz, 0.5)
	ch := [][]float32{slices.Clone(in[0][:512]), slices.Clone(in[1][:512])}
	pr.Process(&plug.Block{In: ch, Out: ch, Frames: 512,
		Changes: set(float64(PunchID), 40, float64(PunchID+1), -30, float64(PunchID+2), 80, float64(PunchID+3), -60)})
	b.ResetTimer()
	for b.Loop() {
		for at := 0; at < 48000; at += 512 {
			n := min(512, 48000-at)
			copy(ch[0], in[0][at:at+n])
			copy(ch[1], in[1][at:at+n])
			pr.Process(&plug.Block{In: ch, Out: ch, Frames: n, Position: plug.Position{Frame: int64(at)}})
		}
	}
}
