package stabilizer

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugtest"
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
	return param(id).Default
}
func (h *host) Begin(uint32, *gunim.UI) { h.log = append(h.log, "begin") }
func (h *host) Set(id uint32, v float64, _ *gunim.UI) {
	h.values[id] = v
	h.log = append(h.log, "set")
}
func (h *host) End(uint32, *gunim.UI)       { h.log = append(h.log, "end") }
func (h *host) BeginEdit(string, *gunim.UI) {}
func (h *host) Changed(*gunim.UI)           {}
func (h *host) EndEdit(*gunim.UI)           {}
func (h *host) Sound() (in, out plug.Sound) { return h.in, h.out }
func (h *host) Traced(id uint32, f int64) (float32, bool) {
	if h.proc == nil {
		return 0, false
	}
	if t := h.proc.Trace(id); t != nil {
		return t.Get(f)
	}
	return 0, false
}

func newStabilizer(t *testing.T) (*plugin, *host) {
	t.Helper()
	d, ok := plug.Lookup("marrasen.stabilizer")
	if !ok {
		t.Fatal("the stabilizer did not register itself")
	}
	h := &host{values: map[uint32]float64{}}
	p, ok := d.New(h).(*plugin)
	if !ok {
		t.Fatal("the stabilizer made something else")
	}
	h.proc = &p.proc
	return p, h
}

// set returns changes at frame 0 of the parameters and values in kv,
// in pairs: an ID, then its value, as a number of any kind.
func set(kv ...any) []plug.Change {
	num := func(v any) float64 {
		switch v := v.(type) {
		case uint32:
			return float64(v)
		case int:
			return float64(v)
		}
		return v.(float64)
	}
	var out []plug.Change
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, plug.Change{ID: uint32(num(kv[i])), Value: num(kv[i+1])})
	}
	return out
}

// rate is the rate the tests run at, and block the frames of a block.
const (
	rate  = 48000
	block = 512
)

// shaped returns n frames, a power of two, of noise at an RMS of a,
// whose level by sixth-octave bands rises tilt decibels an octave over
// pink noise's: at 0 pink, at 3 white. It is made by its spectrum, so
// it holds its tilt exactly, and seed makes each one its own.
func shaped(n int, tilt, a float64, seed uint64) []float32 {
	r := seed*2654435761 + 1
	gauss := func() float64 {
		r = r*6364136223846793005 + 1442695040888963407
		u1 := (float64(r>>11) + 0.5) / (1 << 53)
		r = r*6364136223846793005 + 1442695040888963407
		u2 := float64(r>>11) / (1 << 53)
		return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	}
	x := make([]complex128, n)
	for k := 1; k < n/2; k++ {
		f := float64(k) * rate / float64(n)
		// Pink's power falls as the frequency; tilt lifts it more.
		amp := math.Sqrt(1/f) * math.Pow(10, tilt*math.Log2(f/1000)/20)
		v := complex(gauss()*amp, gauss()*amp)
		x[k], x[n-k] = v, complex(real(v), -imag(v))
	}
	audio.NewFFT(n).Transform(x)
	var sum float64
	for _, v := range x {
		sum += real(v) * real(v)
	}
	g := a / math.Sqrt(sum/float64(n))
	out := make([]float32, n)
	for i, v := range x {
		out[i] = float32(real(v) * g)
	}
	return out
}

// stereo returns x in both channels.
func stereo(x []float32) [2][]float32 { return [2][]float32{x, slices.Clone(x)} }

// addTone adds a sine at hz and level a to x.
func addTone(x []float32, hz, a float64) {
	for i := range x {
		x[i] += float32(a * math.Sin(2*math.Pi*hz*float64(i)/rate))
	}
}

// through plays in through pr, prepared, set by changes in the first
// block, and returns all that comes out.
func through(pr plug.Processor, in [2][]float32, changes []plug.Change) [2][]float32 {
	pr.Prepare(plug.Setup{Rate: rate, MaxBlock: block, Channels: 2})
	var out [2][]float32
	for at := 0; at < len(in[0]); at += block {
		n := min(block, len(in[0])-at)
		ch := [][]float32{slices.Clone(in[0][at : at+n]), slices.Clone(in[1][at : at+n])}
		pr.Process(&plug.Block{In: ch, Out: ch, Frames: n, Changes: changes,
			Position: plug.Position{Frame: int64(at)}})
		changes = nil
		out[0], out[1] = append(out[0], ch[0]...), append(out[1], ch[1]...)
	}
	return out
}

// run plays in through a new stabilizer set by changes, and returns it
// and all that comes out, failing where any of it is not a number or
// is infinite.
func run(t *testing.T, in [2][]float32, changes []plug.Change) (*plugin, [2][]float32) {
	t.Helper()
	p, _ := newStabilizer(t)
	out := through(p.Processor(), in, changes)
	for c := range out {
		for i, v := range out[c] {
			if f := float64(v); math.IsNaN(f) || math.IsInf(f, 0) {
				t.Fatalf("channel %d comes out %v at frame %d", c, v, i)
			}
		}
	}
	return p, out
}

// welch returns x's power by frequency: the mean power of each bin of
// windowed spans of specN frames, half overlapping, over its last
// frames; and how many hertz a bin is.
func welch(x []float32, frames int) (power []float64, bin float64) {
	const specN = 8192
	x = x[len(x)-frames:]
	fft := audio.NewFFT(specN)
	buf := make([]complex128, specN)
	power = make([]float64, specN/2+1)
	spans := 0
	for at := 0; at+specN <= len(x); at += specN / 2 {
		for i := range buf {
			w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/specN)
			buf[i] = complex(float64(x[at+i])*w, 0)
		}
		fft.Transform(buf)
		for k := range power {
			power[k] += real(buf[k])*real(buf[k]) + imag(buf[k])*imag(buf[k])
		}
		spans++
	}
	for k := range power {
		power[k] /= float64(spans)
	}
	return power, rate / float64(specN)
}

// bandLevel is the level of power, by welch, from lo to hi hertz, in
// decibels.
func bandLevel(power []float64, bin, lo, hi float64) float64 {
	sum := 0.0
	for k := int(math.Ceil(lo / bin)); k <= int(hi/bin) && k < len(power); k++ {
		sum += power[k]
	}
	return 10 * math.Log10(sum+1e-30)
}

// tilt returns how many decibels an octave x rises over its last
// frames, as sixth-octave bands from 125 Hz to 8 kHz read it, fitted by
// least squares: pink noise 0, white noise 3.
func tilt(x []float32, frames int) float64 { return tiltOver(x, frames, 125, 8000) }

// tiltOver is tilt, from lo to hi alone.
func tiltOver(x []float32, frames int, lo, hi float64) float64 {
	power, bin := welch(x, frames)
	var sx, sy, sxx, sxy, n float64
	for f := lo; f <= hi*1.0001; f *= math.Pow(2, 1.0/6) {
		o := math.Log2(f / 1000)
		l := bandLevel(power, bin, f/math.Pow(2, 1.0/12), f*math.Pow(2, 1.0/12))
		sx, sy, sxx, sxy, n = sx+o, sy+l, sxx+o*o, sxy+o*l, n+1
	}
	return (n*sxy - sx*sy) / (n*sxx - sx*sx)
}

// level returns x's level at hz, in decibels, a sine of 1 at 0.
func level(x []float32, hz float64) float64 {
	var re, im float64
	for i, v := range x {
		ph := 2 * math.Pi * hz * float64(i) / rate
		re, im = re+float64(v)*math.Cos(ph), im+float64(v)*math.Sin(ph)
	}
	return 20 * math.Log10(2*math.Hypot(re, im)/float64(len(x))+1e-12)
}

// late returns in, late by n frames, as long as it was.
func late(in []float32, n int) []float32 {
	out := make([]float32, len(in))
	copy(out[n:], in)
	return out
}

func TestTheSoundComesOutAFrameLate(t *testing.T) {
	for _, c := range []struct {
		rate float64
		late int
	}{{44100, 2047}, {48000, 2047}, {96000, 4095}, {192000, 8191}} {
		p, _ := newStabilizer(t)
		p.proc.Prepare(plug.Setup{Rate: c.rate, MaxBlock: block, Channels: 2})
		if got := p.proc.Latency(); got != c.late {
			t.Errorf("at %v Hz, the stabilizer reports %d frames late, want %d", c.rate, got, c.late)
		}
	}
}

func TestAtNoAmountTheSoundComesOutLateButAsItWas(t *testing.T) {
	in := [2][]float32{shaped(1<<16, 0, 0.2, 1), shaped(1<<16, 3, 0.2, 2)}
	// A click, and a stretch of silence, where Shape holds.
	in[0][20000], in[1][20001] = 0.9, -0.9
	for i := 40000; i < 50000; i++ {
		in[0][i], in[1][i] = 0, 0
	}
	for m := range modes {
		p, out := run(t, in, set(ModeID, float64(m), AmountID, 0, SpeedID, 100))
		lat := p.proc.Latency()
		for c := range out {
			want := late(in[c], lat)
			for i, v := range out[c] {
				if d := math.Abs(float64(v - want[i])); !(d <= 1e-5) {
					t.Fatalf("in %s at no amount, channel %d frame %d comes out %v, want %v, as it went in %d frames before",
						modeNames[m], c, i, v, want[i], lat)
				}
			}
		}
	}
}

func TestTheOutputTurnsTheSoundUpOrDown(t *testing.T) {
	in := stereo(shaped(1<<15, 0, 0.2, 3))
	p, out := run(t, in, set(AmountID, 0, OutputID, 6))
	want := late(in[0], p.proc.Latency())
	for i, v := range out[0] {
		if d := math.Abs(float64(v) - float64(want[i])*math.Pow(10, 6.0/20)); !(d <= 1e-5) {
			t.Fatalf("at an output of +6 dB, frame %d comes out %v, want %v doubled", i, v, want[i])
		}
	}
}

// gainsIn returns the least and the most of the processor's gain, in
// decibels, over the bins from lo to hi hertz.
func gainsIn(pr *processor, lo, hi float64) (least, most float64) {
	least, most = math.Inf(1), math.Inf(-1)
	for k, f := range pr.freq {
		if f >= lo && f <= hi {
			least, most = min(least, pr.gain[k]), max(most, pr.gain[k])
		}
	}
	return least, most
}

func TestShapeLeavesPinkNoiseAsItIs(t *testing.T) {
	in := [2][]float32{shaped(1<<19, 0, 0.1, 4), shaped(1<<19, 0, 0.1, 5)}
	p, _ := run(t, in, set(TargetID, 0))
	least, most := gainsIn(&p.proc, 40, 16000)
	if !(least >= -0.5 && most <= 0.5) {
		t.Errorf("on pink noise toward pink, Shape's gain runs from %.2f to %.2f dB, want within 0.5", least, most)
	}
	for i, f := range traceFreqs {
		if f < 40 || f > 16000 {
			continue
		}
		if v, ok := p.proc.Trace(gainTrace + uint32(i)).Get(int64(len(in[0]) - 1000)); !ok || !(math.Abs(float64(v)) <= 0.5) {
			t.Errorf("on pink noise, the gain traced at %.0f Hz is %v dB, %v", f, v, ok)
		}
	}
}

func TestShapeDrawsTheBalanceTowardTheTarget(t *testing.T) {
	const frames = 1 << 19
	measure := 3 * rate
	white := [2][]float32{shaped(frames, 3, 0.1, 6), shaped(frames, 3, 0.1, 7)}
	pink := [2][]float32{shaped(frames, 0, 0.1, 8), shaped(frames, 0, 0.1, 9)}
	// The music slope, against pink, rises 3 dB an octave under its knee
	// and falls 3 over it: each side measured clear of the knee, which
	// the smoothing rounds off.
	for _, c := range []struct {
		name         string
		in           [2][]float32
		target, knee float64
		amount       float64
		lo, hi       float64
		// The tilt the target asks for, and how much of the way there the
		// tilt coming out may be pulled.
		want, least, most float64
	}{
		{"white noise toward pink", white, 0, 550, 100, 125, 8000, 0, 0.75, 1.1},
		{"white noise toward pink, half way", white, 0, 550, 50, 125, 8000, 0, 0.4, 0.6},
		{"pink noise toward the music slope, under its knee", pink, 1, 550, 100, 80, 350, 3, 0.6, 1.1},
		{"pink noise toward the music slope, over its knee", pink, 1, 550, 100, 900, 8000, -3, 0.6, 1.1},
		{"pink noise toward the music slope, over its knee, half way", pink, 1, 550, 50, 900, 8000, -3, 0.3, 0.6},
		{"pink noise toward the music slope, its knee at 1.5 kHz, under it", pink, 1, 1500, 100, 600, 1200, 3, 0.6, 1.1},
	} {
		_, out := run(t, c.in, set(TargetID, c.target, KneeID, c.knee, AmountID, c.amount, RangeID, 12, SpeedID, 100))
		was, is := tiltOver(c.in[0], measure, c.lo, c.hi), tiltOver(out[0], measure, c.lo, c.hi)
		pulled := (was - is) / (was - c.want)
		if !(pulled >= c.least && pulled <= c.most) {
			t.Errorf("%s at %v%%: the tilt goes from %.2f to %.2f dB an octave, %.0f%% of the way to %.0f; want %.0f%% to %.0f%%",
				c.name, c.amount, was, is, 100*pulled, c.want, 100*c.least, 100*c.most)
		}
	}
}

func TestShapeLeavesAllOutsideLowAndHigh(t *testing.T) {
	const frames = 1 << 18
	in := [2][]float32{shaped(frames, 3, 0.1, 10), shaped(frames, 3, 0.1, 11)}
	p, out := run(t, in, set(AmountID, 100, RangeID, 12, SpeedID, 100, LowID, 200, HighID, 4000))
	lo, hi := 200/math.Sqrt2, 4000*math.Sqrt2
	least, most := gainsIn(&p.proc, 0, lo)
	l2, m2 := gainsIn(&p.proc, hi, rate/2)
	if least != 0 || most != 0 || l2 != 0 || m2 != 0 {
		t.Errorf("outside Low and High, past the fade, the gain runs from %v to %v dB under and %v to %v over",
			least, most, l2, m2)
	}
	if least, most := gainsIn(&p.proc, 200, 4000); !(most-least >= 6) {
		t.Fatalf("within Low and High, white noise toward pink is turned from %v to %v dB only", least, most)
	}
	inP, bin := welch(late(in[0], p.proc.Latency()), 2*rate)
	outP, _ := welch(out[0], 2*rate)
	for _, b := range [][2]float64{{40, 80}, {80, 120}, {6000, 9000}, {9000, 14000}, {14000, 20000}} {
		d := bandLevel(outP, bin, b[0], b[1]) - bandLevel(inP, bin, b[0], b[1])
		if !(math.Abs(d) <= 0.1) {
			t.Errorf("from %v to %v Hz, outside Low and High, the sound comes out %+.2f dB", b[0], b[1], d)
		}
	}
}

// resonant returns frames of noise at an RMS of 0.05, white, each
// channel its own, with a sine at resonanceHz over it in each of
// channels, its level standing over the noise's in a bin of the
// transform by over decibels.
func resonant(frames int, over float64, channels ...int) [2][]float32 {
	const sigma = 0.05
	in := [2][]float32{shaped(frames, 3, sigma, 12), shaped(frames, 3, sigma, 13)}
	// A bin of a window of n frames hears a sine of a at a²n²/16, and
	// white noise of power σ² at σ²·3n/8.
	n := 2048.0
	a := math.Sqrt(math.Pow(10, over/10) * sigma * sigma * 6 / n)
	for _, c := range channels {
		addTone(in[c], resonanceHz, a)
	}
	return in
}

// resonanceHz is the resonance's pitch, on a bin of the transform.
const resonanceHz = 43 * rate / 2048.0

func TestCutCutsAResonanceAndLeavesTheRest(t *testing.T) {
	const frames = 1 << 18
	in := resonant(frames, 16, 0, 1)
	for _, c := range []struct {
		amount, rng float64
		least, most float64
	}{
		// Half of 16 dB, within a range of 12.
		{50, 12, 5.5, 10.5},
		// All of it, but no further than a range of 6.
		{100, 6, 4.5, 6.5},
	} {
		p, out := run(t, in, set(ModeID, Cut, AmountID, c.amount, RangeID, c.rng))
		was := late(in[0], p.proc.Latency())
		tail := 3 * rate
		cut := level(was[frames-tail:], resonanceHz) - level(out[0][frames-tail:], resonanceHz)
		if !(cut >= c.least && cut <= c.most) {
			t.Errorf("at %v%% and a range of %v dB, a resonance 16 dB over the noise is cut %.2f dB, want %v to %v",
				c.amount, c.rng, cut, c.least, c.most)
		}
		inP, bin := welch(was, tail)
		outP, _ := welch(out[0], tail)
		for lo := 62.5; lo < 16000; lo *= 2 {
			if lo*2 > resonanceHz/2 && lo < resonanceHz*2 {
				continue
			}
			d := bandLevel(outP, bin, lo, 2*lo) - bandLevel(inP, bin, lo, 2*lo)
			if !(math.Abs(d) < 0.5) {
				t.Errorf("at %v%%, the noise from %v to %v Hz, an octave off the resonance, comes out %+.2f dB",
					c.amount, lo, 2*lo, d)
			}
		}
	}
}

func TestCutNeverLifts(t *testing.T) {
	// Noise, tones that come and go, a burst, and silence.
	const frames = 1 << 18
	in := [2][]float32{shaped(frames, 0, 0.1, 14), shaped(frames, 3, 0.05, 15)}
	for i := range frames {
		s := float64(i) / rate
		v := 0.2 * math.Sin(2*math.Pi*440*s) * float64(onOff(int(s*3)%2 == 0))
		v += 0.1 * math.Sin(2*math.Pi*3150*s) * float64(onOff(int(s*5)%3 != 0))
		if i > 2*rate && i < 2*rate+4000 {
			v *= 8
		}
		if i > 3*rate && i < 3*rate+20000 {
			in[0][i], in[1][i], v = 0, 0, 0
		}
		in[0][i] += float32(v)
		in[1][i] += float32(v / 2)
	}
	p, _ := newStabilizer(t)
	pr := &p.proc
	pr.Prepare(plug.Setup{Rate: rate, MaxBlock: block, Channels: 2})
	for at := 0; at < frames; at += block {
		var changes []plug.Change
		if at == 0 {
			changes = set(ModeID, Shape, AmountID, 100, RangeID, 12)
		}
		// Shape lifts first; Cut must let it go and lift nothing after.
		if at == rate/2/block*block {
			changes = set(ModeID, Cut, SpeedID, float64(at%100))
		}
		if at == 3*rate/2/block*block {
			changes = set(SpeedID, 100, SmoothID, 1.0/12)
		}
		ch := [][]float32{slices.Clone(in[0][at : at+block]), slices.Clone(in[1][at : at+block])}
		pr.Process(&plug.Block{In: ch, Out: ch, Frames: block, Changes: changes, Position: plug.Position{Frame: int64(at)}})
		if at < rate {
			continue
		}
		for k, g := range pr.want {
			if !(g <= 0) {
				t.Fatalf("at frame %d, Cut wants %.3g dB at %.0f Hz", at, g, pr.freq[k])
			}
		}
		if _, most := gainsIn(pr, 0, rate/2); !(most <= 0) {
			t.Fatalf("at frame %d, a second after Shape, Cut lifts by %.3g dB", at, most)
		}
	}
}

func TestOneGainServesBothChannels(t *testing.T) {
	const frames = 1 << 18
	tail := 3 * rate
	for c := range 2 {
		in := resonant(frames, 16, c)
		// The other channel hears the very noise this one does, without
		// the resonance, so its gain shows in it alone.
		in[1-c] = slices.Clone(in[c])
		addTone(in[1-c], resonanceHz, -math.Sqrt(math.Pow(10, 1.6)*0.05*0.05*6/2048))
		p, out := run(t, in, set(ModeID, Cut, AmountID, 50, RangeID, 12))
		lat := p.proc.Latency()
		cut := level(late(in[c], lat)[frames-tail:], resonanceHz) - level(out[c][frames-tail:], resonanceHz)
		// The other channel: its noise round the resonance, from a bin
		// of welch either side, before and after.
		inP, bin := welch(late(in[1-c], lat), tail)
		outP, _ := welch(out[1-c], tail)
		lo, hi := resonanceHz-bin, resonanceHz+bin
		other := bandLevel(inP, bin, lo, hi) - bandLevel(outP, bin, lo, hi)
		if !(cut >= 4) || !(math.Abs(cut-other) <= 2) {
			t.Errorf("with a resonance in channel %d alone, it is cut %.2f dB, and channel %d there %.2f dB: want both alike",
				c, cut, 1-c, other)
		}
	}
}

func TestProcessingAllocatesNothing(t *testing.T) {
	p, _ := newStabilizer(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: rate, MaxBlock: block, Channels: 2})
	in := resonant(64*block, 16, 0, 1)
	blocks := make([]*plug.Block, 64)
	for i := range blocks {
		ch := [][]float32{in[0][i*block : (i+1)*block], in[1][i*block : (i+1)*block]}
		blocks[i] = &plug.Block{In: ch, Out: ch, Frames: block, Position: plug.Position{Frame: int64(i * block)},
			Changes: set(ModeID, float64(i/4%2), TargetID, float64(i/8%2), AmountID, float64(i), SpeedID, float64(i),
				SmoothID, 0.1+float64(i)/80, LowID, float64(20+i*10), HighID, float64(16000-i*100), OutputID, -1)}
		blocks[i].Changes[3].Frame = 200
	}
	made := plugtest.AllocsUnder(t, "stabilizer.(*processor).Process", func() {
		for _, b := range blocks {
			pr.Process(b)
		}
	})
	if made != 0 {
		t.Errorf("processing made %d things over 64 blocks", made)
	}
}

func TestTheDisplayDragsLowAndHigh(t *testing.T) {
	p, h := newStabilizer(t)
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	d := pn.disp
	d.Layout(gunim.Constraints{Max: geom.Sz(1200, 600)}, gunim.Frame{}, gunim.Children{})
	d.line = 400
	for _, c := range []struct {
		id       uint32
		from, to float64
	}{{LowID, 40, 100}, {HighID, 16000, 8000}} {
		h.log = nil
		at := geom.Pt(d.xOf(c.from), 200)
		d.Handle(input.PointerMove{Pos: at}, nil)
		d.Handle(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1}, nil)
		d.Handle(input.PointerMove{Pos: geom.Pt(d.xOf((c.from+c.to)/2), 210)}, nil)
		d.Handle(input.PointerMove{Pos: geom.Pt(d.xOf(c.to), 220)}, nil)
		d.Handle(input.PointerUp{Pos: geom.Pt(d.xOf(c.to), 220), Button: input.ButtonPrimary}, nil)
		if got := h.values[c.id]; math.Abs(got/c.to-1) > 0.01 ||
			!slices.Equal(h.log, []string{"begin", "set", "set", "end"}) {
			t.Errorf("%s dragged to %v Hz is at %v, by %v", param(c.id).Name, c.to, got, h.log)
		}
	}
	// Under the line, where the sections stand, nothing drags.
	h.log = nil
	if d.Handle(input.PointerDown{Pos: geom.Pt(d.xOf(100), d.line+30), Button: input.ButtonPrimary, Clicks: 1}, nil) ||
		len(h.log) > 0 {
		t.Errorf("a press under the display's line took a handle, by %v", h.log)
	}
}

func TestTheSelectorsSetTheModeAndTheTarget(t *testing.T) {
	p, h := newStabilizer(t)
	pn, ok := p.View(plug.Full, h).(*panel)
	if !ok {
		t.Fatal("the full view is of another type")
	}
	speed := pn.knobs[SpeedID]
	if s := speed.Param().Text(50); !strings.HasSuffix(s, " s") || pn.target.Locked {
		t.Errorf("in Shape, the speed at 50%% reads %q, and the target is locked %v", s, pn.target.Locked)
	}
	pn.target.Layout(gunim.Constraints{Max: geom.Sz(selectorW, 100)}, gunim.Frame{}, gunim.Children{})
	pn.target.Handle(input.PointerDown{Pos: pn.target.Cell(1).Center(), Button: input.ButtonPrimary, Clicks: 1}, nil)
	if h.values[TargetID] != 1 || !slices.Equal(h.log, []string{"begin", "set", "end"}) {
		t.Errorf("the music slope picked, the target is %v, by %v", h.values[TargetID], h.log)
	}
	// The knee works for the music slope in Shape alone.
	if pn.knobs[KneeID].Disabled {
		t.Error("the music slope picked in Shape, its knee is greyed")
	}
	pn.Follow(ModeID, Cut, nil)
	if !pn.knobs[KneeID].Disabled {
		t.Error("in Cut, the knee is not greyed")
	}
	pn.Follow(ModeID, Shape, nil)
	pn.Follow(TargetID, 0, nil)
	if !pn.knobs[KneeID].Disabled {
		t.Error("pink picked, the knee is not greyed")
	}
	pn.Follow(TargetID, 1, nil)
	h.log = nil
	pn.mode.Layout(gunim.Constraints{Max: geom.Sz(selectorW, 100)}, gunim.Frame{}, gunim.Children{})
	pn.mode.Handle(input.PointerDown{Pos: pn.mode.Cell(Cut).Center(), Button: input.ButtonPrimary, Clicks: 1}, nil)
	if h.values[ModeID] != Cut || !slices.Equal(h.log, []string{"begin", "set", "end"}) {
		t.Errorf("Cut picked, the mode is %v, by %v", h.values[ModeID], h.log)
	}
	if s := speed.Param().Text(50); !strings.HasSuffix(s, " ms") || !pn.target.Locked {
		t.Errorf("in Cut, the speed at 50%% reads %q, and the target is locked %v", s, pn.target.Locked)
	}
	// Set elsewhere, as by an undo, the panel follows.
	pn.Follow(ModeID, Shape, nil)
	pn.Follow(TargetID, 0, nil)
	if pn.mode.Selected() != Shape || pn.target.Selected() != 0 || pn.target.Locked {
		t.Errorf("followed back, the mode shows %d and the target %d, locked %v", pn.mode.Selected(),
			pn.target.Selected(), pn.target.Locked)
	}
}

// sound is a sound for the views to hear: what a processor played, by
// the frame, as far as at.
type sound struct {
	frames [2][]float32
	at     int
}

func (s *sound) Heard(dst []float32) (int, int64, int) {
	n := min(len(dst)/2, s.at)
	for i := range n {
		f := s.at - n + i
		dst[2*i], dst[2*i+1] = s.frames[0][f], s.frames[1][f]
	}
	return n, int64(s.at), rate
}

func TestTheViewsShowTheGainAsTheSoundPlays(t *testing.T) {
	in := resonant(1<<17, 16, 0, 1)
	p, h := newStabilizer(t)
	h.values[ModeID] = Cut
	out := through(p.Processor(), in, set(ModeID, Cut, AmountID, 100, RangeID, 12))
	h.in, h.out = &sound{frames: in, at: 80000}, &sound{frames: out, at: 80000}
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	w := gunimtest.New(t, geom.Sz(1100, 520), nil)
	gunim.RegisterView(w, "stabilizer", func(struct{}) gunim.Node { return pn }, nil)
	if err := w.Client().Mount(gunim.Root, "stabilizer", "stabilizer", nil); err != nil {
		t.Fatal(err)
	}
	ch := p.View(plug.Chip, h).(*chip)
	for range 90 {
		h.in.(*sound).at += 400
		h.out.(*sound).at += 400
		w.Frame(time.Second / 60)
		ch.Step(time.Second / 60)
	}
	// The resonance is cut, near 1 kHz, and the display and the chip
	// show it; nothing is lifted.
	near := 0
	for i, f := range traceFreqs {
		if math.Abs(math.Log2(f/resonanceHz)) < 0.15 {
			near = i
		}
	}
	if g := pn.disp.gainSm[near]; !(g < -4) {
		t.Errorf("the display shows %v dB at %.0f Hz, by the resonance", g, traceFreqs[near])
	}
	if g := ch.shown[near].Value(); !(g < -4) {
		t.Errorf("the chip shows %v dB at %.0f Hz, by the resonance", g, traceFreqs[near])
	}
	for i, g := range pn.disp.gainSm {
		if !(g <= 0.01) {
			t.Errorf("in Cut, the display shows a lift of %v dB at %.0f Hz", g, traceFreqs[i])
		}
	}
	if a := pn.disp.Access(); !strings.Contains(a.Value, "Most cut") {
		t.Errorf("the display reads as %q", a.Value)
	}
	if a := ch.Access(); !strings.Contains(a.Name, "Cut") {
		t.Errorf("the chip reads as %q", a.Name)
	}
}

// benchmarkMode times a second of stereo sound at 48 kHz through the
// stabilizer in mode m.
func benchmarkMode(b *testing.B, m int) {
	d, _ := plug.Lookup("marrasen.stabilizer")
	pr := d.New(&host{values: map[uint32]float64{}}).Processor()
	pr.Prepare(plug.Setup{Rate: rate, MaxBlock: block, Channels: 2})
	in := resonant(1<<16, 16, 0, 1)
	buf := [][]float32{make([]float32, block), make([]float32, block)}
	changes := set(ModeID, m)
	b.ResetTimer()
	for range b.N {
		for at := 0; at < rate; at += block {
			n := min(block, rate-at)
			ch := [][]float32{buf[0][:n], buf[1][:n]}
			copy(ch[0], in[0][at:])
			copy(ch[1], in[1][at:])
			pr.Process(&plug.Block{In: ch, Out: ch, Frames: n, Changes: changes})
			changes = nil
		}
	}
}

func BenchmarkShapeASecond(b *testing.B) { benchmarkMode(b, Shape) }
func BenchmarkCutASecond(b *testing.B)   { benchmarkMode(b, Cut) }
