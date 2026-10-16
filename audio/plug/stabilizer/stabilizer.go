// Package stabilizer is an EQ that keeps setting itself as the track
// plays, as a mastering engineer's stabilizer does. It works in one of
// two ways. In Shape it listens to the track's balance over the last
// few seconds, from low to high, and draws it gently toward a target,
// pink noise or the slope recorded music commonly takes, with broad and
// smooth lifts and cuts. In Cut it
// listens a few milliseconds at a time for narrow resonances, pitches
// that stand out over those round them, and cuts those alone, as soon
// as they ring, leaving the rest as it was.
//
// It hears the sound through a short-time Fourier transform: the sound
// is cut into frames that overlap, each weighed by a window, turned
// into its pitches, each a narrow band called a bin, each bin turned up
// or down, turned back, and the frames added together again. That takes a frame's length to do, so
// the sound comes out a frame late, as Latency reports.
package stabilizer

import (
	"errors"
	"fmt"
	"math"
	"math/cmplx"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/tonal"
)

// The parameters' IDs: the mode and the target, how far it goes and
// how fast, the most it lifts or cuts, how broad its changes are, the
// span it works in, the output, and the knee of a target that has one.
const (
	ModeID   uint32 = 1
	TargetID uint32 = 2
	AmountID uint32 = 3
	SpeedID  uint32 = 4
	RangeID  uint32 = 5
	SmoothID uint32 = 6
	LowID    uint32 = 7
	HighID   uint32 = 8
	OutputID uint32 = 9
	KneeID   uint32 = 10
)

// gainTrace is where the IDs of the gain's traces start: the gain at
// traceFreqs[i], in decibels, as the views read it with
// [plug.Host.Traced], is traced as gainTrace+i. They are IDs of no
// parameter, as the EQ's band traces are.
const gainTrace = 1000

// traced is how many frequencies the gain is traced at; traceLow and
// traceHigh the lowest and the highest of them.
const (
	traced              = 48
	traceLow, traceHigh = 20.0, 20000.0
)

// traceFreqs are the frequencies the gain is traced at, evenly apart by
// their ratios.
var traceFreqs = func() [traced]float64 {
	var out [traced]float64
	for i := range out {
		out[i] = traceLow * math.Pow(traceHigh/traceLow, float64(i)/(traced-1))
	}
	return out
}()

// The modes.
const (
	Shape = iota // the balance, drawn slowly toward a target
	Cut          // narrow resonances, caught and cut quickly
	modes
)

var (
	modeNames = []string{"Shape", "Cut"}
	modeTips  = []string{
		"Shape: listens over seconds and draws the track's balance gently toward the target, with broad lifts and cuts",
		"Cut: listens over milliseconds for narrow resonances, pitches ringing out over those round them, and cuts those alone",
	}
)

// targetNames and targetTips name the targets, and say what each is,
// in the order of tonal.Targets.
var targetNames, targetTips = func() ([]string, []string) {
	names, tips := make([]string, 0, len(tonal.Targets)), make([]string, 0, len(tonal.Targets))
	for _, t := range tonal.Targets {
		names, tips = append(names, t.Short), append(tips, t.Tip)
	}
	return names, tips
}()

func pct(v float64) string { return fmt.Sprintf("%.0f%%", v) }

func hz(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.3g kHz", v/1000)
	}
	return fmt.Sprintf("%.0f Hz", v)
}

func db(v float64) string { return fmt.Sprintf("%+.1f dB", v) }

// octaves writes a width in octaves as a fraction where it is near
// one, as "1/3 oct".
func octaves(v float64) string {
	if n := 1 / v; math.Abs(n-math.Round(n)) < 0.05 {
		if math.Round(n) == 1 {
			return "1 oct"
		}
		return fmt.Sprintf("1/%.0f oct", n)
	}
	return fmt.Sprintf("%.2f oct", v)
}

// seconds writes a time in seconds, or in milliseconds where it is
// under one.
func seconds(s float64) string {
	if s < 1 {
		return fmt.Sprintf("%.0f ms", s*1000)
	}
	return fmt.Sprintf("%.1f s", s)
}

// The times the speed runs between, in seconds, from 0% to 100%: in
// Shape, how long the balance is heard over; in Cut, how quickly each
// pitch's cut follows it.
const (
	shapeSlow, shapeFast = 20.0, 0.5
	cutSlow, cutFast     = 0.2, 0.005
)

// speedTime returns the time a speed of v, from 0 to 100, stands for in
// mode m, in seconds: by ratios, so each step of the knob is as long a
// step to the ear.
func speedTime(m int, v float64) float64 {
	slow, fast := shapeSlow, shapeFast
	if m == Cut {
		slow, fast = cutSlow, cutFast
	}
	return slow * math.Pow(fast/slow, v/100)
}

// Descriptor is the plugin, as it is registered.
var Descriptor = plug.Descriptor{
	ID:       "marrasen.stabilizer",
	Name:     "Stabilizer",
	Vendor:   "Marras",
	Version:  "0.1.0",
	Kind:     "Fx|EQ|Mastering",
	Tagline:  "Self-setting EQ",
	Keywords: []string{"resonance", "resonant", "tonal balance", "auto eq", "dynamic eq", "soothe", "harsh", "mastering"},
	New:      func(h plug.Host) plug.Plugin { return &plugin{} },
	Params: []plug.Param{
		{ID: ModeID, Name: "Mode", Max: modes - 1, Steps: modes - 1, Flags: plug.Automatable,
			Format: func(v float64) string { return modeNames[int(math.Round(v))] }},
		{ID: TargetID, Name: "Target", Max: float64(len(tonal.Targets) - 1), Steps: len(tonal.Targets) - 1,
			Flags: plug.Automatable, Format: func(v float64) string { return tonal.TargetAt(v).Short }},
		{ID: AmountID, Name: "Amount", Unit: "%", Max: 100, Default: 50, Flags: plug.Automatable, Format: pct},
		{ID: SpeedID, Name: "Speed", Unit: "%", Max: 100, Default: 50, Flags: plug.Automatable, Format: pct},
		{ID: RangeID, Name: "Range", Unit: "dB", Min: 1, Max: 12, Default: 6, Flags: plug.Automatable,
			Format: func(v float64) string { return fmt.Sprintf("%.1f dB", v) }},
		{ID: SmoothID, Name: "Smooth", Unit: "oct", Min: 1.0 / 12, Max: 1, Default: 1.0 / 3, Log: true,
			Flags: plug.Automatable, Format: octaves},
		{ID: LowID, Name: "Low", Unit: "Hz", Min: 20, Max: 20000, Default: 40, Log: true, Flags: plug.Automatable,
			Format: hz},
		{ID: HighID, Name: "High", Unit: "Hz", Min: 20, Max: 20000, Default: 16000, Log: true,
			Flags: plug.Automatable, Format: hz},
		{ID: OutputID, Name: "Output", Unit: "dB", Min: -12, Max: 12, Flags: plug.Automatable, Format: db},
		{ID: KneeID, Name: "Knee", Unit: "Hz", Min: tonal.KneeLow, Max: tonal.KneeHigh, Default: tonal.Knee, Log: true,
			Flags: plug.Automatable, Format: hz},
	},
	Patches: []plug.Patch{
		{Name: "Default"},
		{Name: "Gentle balance", Params: map[uint32]float64{AmountID: 30, SpeedID: 25, RangeID: 4}},
		{Name: "Music slope", Params: map[uint32]float64{TargetID: 1, AmountID: 40, RangeID: 6}},
		{Name: "Tame resonances", Params: map[uint32]float64{ModeID: Cut, AmountID: 50, SpeedID: 60, RangeID: 6,
			SmoothID: 1.0 / 3, LowID: 150, HighID: 12000}},
		{Name: "Harsh top", Params: map[uint32]float64{ModeID: Cut, AmountID: 60, SpeedID: 70, RangeID: 8,
			SmoothID: 0.5, LowID: 2000, HighID: 16000}},
	},
}

func init() { plug.Register(Descriptor) }

// param returns the parameter id; every ID above is one.
func param(id uint32) plug.Param {
	p, _ := Descriptor.Param(id)
	return p
}

// plugin is one stabilizer.
type plugin struct{ proc processor }

// Processor implements [plug.Plugin].
func (p *plugin) Processor() plug.Processor { return &p.proc }

// State implements [plug.Plugin]: it keeps nothing but its parameters.
func (p *plugin) State() []byte { return nil }

// SetState implements [plug.Plugin]: it takes only an empty state.
func (p *plugin) SetState(b []byte) error {
	if len(b) > 0 {
		return errors.New("stabilizer: the plugin keeps no state")
	}
	return nil
}

// Close implements [plug.Plugin].
func (p *plugin) Close() {}

// View implements [plug.Viewer]: the gain in a chip, and the panel,
// which serves in full too.
func (p *plugin) View(s plug.Size, h plug.Host) gunim.Node {
	switch s {
	case plug.Chip:
		return newChip(h)
	case plug.Panel, plug.Full:
		return newPanel(h)
	default:
		return nil
	}
}

// frameFor returns how many frames long the transform's frames are at
// rate: about 43 milliseconds at 48 kHz, as long at the higher rates,
// so a bin is as narrow at each, about 23 Hz.
func frameFor(rate float64) int {
	switch {
	case rate <= 50000:
		return 2048
	case rate <= 100000:
		return 4096
	}
	return 8192
}

// overlap is how many hops a frame is long: a frame starts each
// quarter of a frame.
const overlap = 4

// glideMS is how long the output takes to glide where it is set;
// shapeGlideMS how long Shape's gain takes to glide to the one it
// wants, on top of the long hearing, so a change of a knob lands
// smoothly; quietDB the level, of a frame's mean power against full
// scale, under which the stabilizer takes the sound for silence: Shape
// holds what it heard, so a pause does not drag the balance, and Cut
// lets its cuts go.
const (
	glideMS      = 20
	shapeGlideMS = 200
	quietDB      = -70
)

// How Cut hears a resonance. cutHearMS is how long each bin's power is
// heard over, a little, so noise reads steadier. cutNotch is how many
// bins either side of a bin are left out of the level round it, so a
// peak, whose window spreads it over that many bins, does not raise its
// own reference; cutLeast is how many bins the level round it takes at
// least either side, beyond those. A bin counts as standing out from
// cutFrom decibels over the level round it, wholly from cutFull, so the
// ripple of noise is let be. cutRelease is how many times longer a cut
// takes to let go than to come.
const (
	cutHearMS  = 20
	cutNotch   = 2
	cutLeast   = 6
	cutFrom    = 3.0
	cutFull    = 9.0
	cutRelease = 3
)

// fadeOctaves is how far outside Low and High the gain fades to none.
const fadeOctaves = 0.5

// processor changes the sound through a short-time Fourier transform:
// both channels as one complex sound, the left real and the right
// imaginary, so one transform each way does both and one gain serves
// both, which keeps the stereo image as it was.
type processor struct {
	rate float64
	// n is the frames' length and hop how far apart they start; m is
	// the bin at half the rate, the last of the bins that count.
	n, hop, m int
	fft       *audio.FFT
	// window is the window each frame is weighed by, going in and again
	// coming out; norm what the overlapped windows add up to, inverted,
	// with the inverse transform's 1/n.
	window []float64
	norm   float64
	// in holds each channel's last n frames, a ring, and out what is
	// added up for it to come out, a ring as long; pos is where the next
	// frame goes in both, and count how many came since the last hop.
	in, out    [2][]float64
	pos, count int
	spec       []complex128
	// Each bin's frequency and its power this hop; the power heard over
	// the speed, for Shape, and over cutHearMS, for Cut; a running sum,
	// and two scratches; the gain wanted, and the gain, in decibels, as
	// it glides there.
	freq, power, long, short []float64
	sums, level, spread      []float64
	want, gain               []float64
	// heardLong and heardShort say each of long and short has heard
	// sound.
	heardLong, heardShort bool
	// The parameters as set.
	mode                                  int
	targetAt, knee                        float64
	target                                tonal.Target
	amount, speed, rng, smooth, low, high float64
	// The output as a gain, as set and as it glides.
	outTo, outGain, glide float64
	fresh                 bool
	// traceAt is where each traced frequency falls among the bins, and
	// traceBins the bins nearer it than any other traced frequency.
	traceAt   [traced]float64
	traceBins [traced][2]int
	traces    [traced]plug.Trace
	latency   int
}

// Prepare implements [plug.Processor]: everything Process needs is made
// here, so it makes nothing.
func (p *processor) Prepare(s plug.Setup) {
	p.rate = s.Rate
	p.n = frameFor(s.Rate)
	p.hop = p.n / overlap
	p.m = p.n / 2
	p.latency = p.n - 1
	p.fft = audio.NewFFT(p.n)
	p.window = make([]float64, p.n)
	for i := range p.window {
		// Periodic, so the windows of the frames, overlapped, add up to
		// the same at every frame.
		p.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(p.n))
	}
	// The windows over any one frame of the sound, each squared, as it
	// is weighed going in and coming out, add up alike: those over the
	// second frame are taken.
	sum := 0.0
	for k := range overlap {
		w := p.window[k*p.hop+1]
		sum += w * w
	}
	p.norm = 1 / (sum * float64(p.n))
	for c := range p.in {
		p.in[c] = make([]float64, p.n)
		p.out[c] = make([]float64, p.n)
	}
	p.spec = make([]complex128, p.n)
	bins := p.m + 1
	for _, a := range []*[]float64{&p.freq, &p.power, &p.long, &p.short, &p.level, &p.spread, &p.want, &p.gain} {
		*a = make([]float64, bins)
	}
	p.sums = make([]float64, bins+1)
	for k := range p.freq {
		p.freq[k] = float64(k) * s.Rate / float64(p.n)
	}
	half := math.Sqrt(math.Pow(traceHigh/traceLow, 1.0/(traced-1)))
	for i, f := range traceFreqs {
		p.traceAt[i] = min(f*float64(p.n)/s.Rate, float64(p.m))
		lo := int(math.Ceil(f / half * float64(p.n) / s.Rate))
		hi := min(int(math.Ceil(f*half*float64(p.n)/s.Rate))-1, p.m)
		p.traceBins[i] = [2]int{lo, hi}
	}
	p.glide = 1 - math.Exp(-1/(glideMS/1000*s.Rate))
	for _, prm := range Descriptor.Params {
		p.take(plug.Change{ID: prm.ID, Value: prm.Default})
	}
	p.Reset()
}

// take takes a change to a parameter.
func (p *processor) take(c plug.Change) {
	prm, ok := Descriptor.Param(c.ID)
	if !ok {
		return
	}
	v := prm.Clamp(c.Value)
	switch c.ID {
	case ModeID:
		p.mode = int(math.Round(v))
	case TargetID:
		p.targetAt = v
		p.target = tonal.TargetAt(v).At(p.knee)
	case KneeID:
		p.knee = v
		p.target = tonal.TargetAt(p.targetAt).At(v)
	case AmountID:
		p.amount = v / 100
	case SpeedID:
		p.speed = v
	case RangeID:
		p.rng = v
	case SmoothID:
		p.smooth = v
	case LowID:
		p.low = v
	case HighID:
		p.high = v
	case OutputID:
		p.outTo = math.Pow(10, v/20)
	}
	if p.fresh {
		p.outGain = p.outTo
	}
}

// Process implements [plug.Processor].
func (p *processor) Process(b *plug.Block) {
	if len(b.In) == 0 || len(b.Out) == 0 || p.rate == 0 {
		return
	}
	chans := min(len(b.In), len(b.Out), 2)
	mask := p.n - 1
	next := 0
	for i := range b.Frames {
		for next < len(b.Changes) && b.Changes[next].Frame <= i {
			p.take(b.Changes[next])
			next++
		}
		p.outGain += (p.outTo - p.outGain) * p.glide
		// One channel is heard as both.
		l := float64(b.In[0][i])
		r := l
		if chans > 1 {
			r = float64(b.In[1][i])
		}
		p.in[0][p.pos], p.in[1][p.pos] = l, r
		p.pos = (p.pos + 1) & mask
		p.count++
		if p.count == p.hop {
			p.count = 0
			p.step(b.Position.Frame + int64(i))
		}
		// What comes out is what went in latency frames ago: the oldest
		// in the ring, which every frame over it has now been added to.
		for c := range chans {
			b.Out[c][i] = float32(p.out[c][p.pos] * p.outGain)
			p.out[c][p.pos] = 0
		}
		if chans == 1 {
			p.out[1][p.pos] = 0
		}
	}
	for ; next < len(b.Changes); next++ {
		p.take(b.Changes[next])
	}
	p.fresh = false
}

// step takes the frame of the last n frames: it hears it, works out
// the gain, turns each bin by it, and adds the frame back into what
// comes out. at is the frame of the sound it ends at, from which the
// gain is traced.
func (p *processor) step(at int64) {
	n, mask := p.n, p.n-1
	power := 0.0
	for j := range n {
		k := (p.pos + j) & mask
		l, r := p.in[0][k], p.in[1][k]
		power += l*l + r*r
		w := p.window[j]
		p.spec[j] = complex(l*w, r*w)
	}
	loud := 10*math.Log10(power/float64(2*n)+1e-30) > quietDB
	p.fft.Transform(p.spec)
	// Each bin's power, the mean of the two channels': the left's
	// spectrum is the even part of the complex one, the right's its odd
	// part, and their powers add up as the powers of a bin and its
	// mirror do.
	for k := range p.m + 1 {
		a, b := p.spec[k], p.spec[(n-k)&mask]
		p.power[k] = (real(a)*real(a) + imag(a)*imag(a) + real(b)*real(b) + imag(b)*imag(b)) / 4
	}
	p.hear(loud)
	if p.mode == Cut {
		p.cut(loud)
	} else {
		p.shape()
	}
	p.glideGain()
	// Each bin turned by its gain, and its mirror alike, so each
	// channel stays real; then back, by the transform of the
	// conjugate, conjugated.
	for k := range p.m + 1 {
		g := complex(math.Exp(p.gain[k]*math.Ln10/20), 0)
		p.spec[k] *= g
		if k > 0 && k < p.m {
			p.spec[n-k] *= g
		}
	}
	for j, v := range p.spec {
		p.spec[j] = cmplx.Conj(v)
	}
	p.fft.Transform(p.spec)
	// The transform left conjugated: the right channel, imaginary,
	// comes back negated.
	for j, v := range p.spec {
		k := (p.pos + j) & mask
		w := p.window[j] * p.norm
		p.out[0][k] += real(v) * w
		p.out[1][k] -= imag(v) * w
	}
	p.record(at)
}

// hear hears the hop's power: over the speed, for Shape, held while the
// sound is quiet, so a pause does not drag the balance; and over
// cutHearMS, for Cut. Each starts from the first sound it hears, so it
// does not rise from nothing.
func (p *processor) hear(loud bool) {
	if !loud {
		return
	}
	hop := float64(p.hop) / p.rate
	long := 1 - math.Exp(-hop/speedTime(Shape, p.speed))
	short := 1 - math.Exp(-hop/(cutHearMS/1000.0))
	if !p.heardLong {
		long = 1
	}
	if !p.heardShort {
		short = 1
	}
	for k, v := range p.power {
		p.long[k] += (v - p.long[k]) * long
		p.short[k] += (v - p.short[k]) * short
	}
	p.heardLong, p.heardShort = true, true
}

// fade returns how much of the gain is let through at frequency f: all
// of it from Low to High, fading to none, along half a cosine,
// fadeOctaves outside them.
func (p *processor) fade(f float64) float64 {
	lo, hi := min(p.low, p.high), max(p.low, p.high)
	var out float64
	switch {
	case f <= 0:
		return 0
	case f < lo:
		out = math.Log2(lo/f) / fadeOctaves
	case f > hi:
		out = math.Log2(f/hi) / fadeOctaves
	default:
		return 1
	}
	if out >= 1 {
		return 0
	}
	return 0.5 + 0.5*math.Cos(math.Pi*out)
}

// reach returns the bins round bin k that are heard with it, from lo to
// hi: those within half of Smooth octaves either side, and no fewer
// than least bins either side.
func (p *processor) reach(k, least int) (lo, hi int) {
	r := math.Exp2(p.smooth / 2)
	lo = min(int(float64(k)/r+0.5), k-least)
	hi = max(int(float64(k)*r+0.5), k+least)
	return max(lo, 1), min(hi, p.m)
}

// shape works out the gain Shape wants: the balance heard over the
// speed, each bin's power taken over Smooth octaves round it, as a band
// that wide hears it, against the target's; less their mean from Low to
// High, so the stabilizer tilts and bends the balance without turning
// the whole up or down; each lifted or cut by the amount of how far it
// is off, no further than the range, and faded outside Low and High.
func (p *processor) shape() {
	if !p.heardLong {
		clear(p.want)
		return
	}
	p.sums[0] = 0
	for k, v := range p.long {
		p.sums[k+1] = p.sums[k] + v
	}
	// A band's power is the power at each bin over it, which falls as
	// pink noise's does, times its width, which grows as the frequency:
	// the mean times the bin's number, where pink noise reads level.
	lo, hi := min(p.low, p.high), max(p.low, p.high)
	var sum, weights float64
	for k := 1; k <= p.m; k++ {
		a, b := p.reach(k, 0)
		band := (p.sums[b+1] - p.sums[a]) / float64(b-a+1) * float64(k)
		p.level[k] = 10*math.Log10(band+1e-30) - p.target.Level(p.freq[k])
		// Each octave weighs alike: a bin covers less of an octave the
		// higher it is.
		if f := p.freq[k]; f >= lo && f <= hi {
			w := 1 / float64(k)
			sum += w * p.level[k]
			weights += w
		}
	}
	mean := 0.0
	if weights > 0 {
		mean = sum / weights
	}
	p.want[0] = 0
	for k := 1; k <= p.m; k++ {
		g := -p.amount * (p.level[k] - mean)
		p.want[k] = max(-p.rng, min(g, p.rng)) * p.fade(p.freq[k])
	}
}

// cut works out the gain Cut wants: each bin's level, heard over
// cutHearMS, against the level round it, over Smooth octaves, the bin
// and its nearest left out; what stands out cut by the amount of how
// far it does, no further than the range; each cut spread to the bins
// beside it, so a resonance is cut whole and no bin stands alone; and
// faded outside Low and High. While the sound is quiet, it cuts
// nothing.
func (p *processor) cut(loud bool) {
	clear(p.want)
	if !loud || !p.heardShort {
		return
	}
	p.sums[0] = 0
	for k, v := range p.short {
		p.level[k] = 10 * math.Log10(v+1e-30)
		p.sums[k+1] = p.sums[k] + p.level[k]
	}
	for k := 1; k <= p.m; k++ {
		lo, hi := p.reach(k, cutNotch+cutLeast)
		a, b := max(k-cutNotch, lo), min(k+cutNotch, hi)
		round := (p.sums[hi+1] - p.sums[lo]) - (p.sums[b+1] - p.sums[a])
		count := (hi - lo + 1) - (b - a + 1)
		if count <= 0 {
			continue
		}
		over := p.level[k] - round/float64(count)
		// How far it counts as standing out, rising from cutFrom to all
		// of it at cutFull.
		heed := min(max((over-cutFrom)/(cutFull-cutFrom), 0), 1)
		p.want[k] = max(-p.amount*max(over, 0)*heed, -p.rng)
	}
	// Each bin cut as deep as the deepest beside it, then softened over
	// its neighbours.
	for k := range p.want {
		p.spread[k] = min(p.want[k], p.want[max(k-1, 0)], p.want[min(k+1, p.m)])
	}
	for k := range p.want {
		v := (p.spread[max(k-1, 0)] + 2*p.spread[k] + p.spread[min(k+1, p.m)]) / 4
		p.want[k] = v * p.fade(p.freq[k])
	}
}

// snapDB is how near, in decibels, a gliding gain lands where it goes.
const snapDB = 1e-6

// glideGain moves the gain toward the one wanted: in Shape smoothly,
// over shapeGlideMS; in Cut, each bin as fast as the speed down, and
// cutRelease times slower back up.
func (p *processor) glideGain() {
	hop := float64(p.hop) / p.rate
	smooth := 1 - math.Exp(-hop/(shapeGlideMS/1000.0))
	attack := 1 - math.Exp(-hop/speedTime(Cut, p.speed))
	release := 1 - math.Exp(-hop/(cutRelease*speedTime(Cut, p.speed)))
	for k, w := range p.want {
		g, rate := p.gain[k], smooth
		if p.mode == Cut {
			rate = release
			if w < g {
				rate = attack
			}
		}
		// Near enough, it lands, so a gain gliding to none reaches it,
		// rather than lingering a hair's breadth over.
		if math.Abs(w-g) < snapDB {
			p.gain[k] = w
			continue
		}
		p.gain[k] = g + (w-g)*rate
	}
}

// gainAt returns the gain at bin x, which may fall between two, in
// decibels.
func (p *processor) gainAt(x float64) float64 {
	k := min(int(x), p.m-1)
	t := x - float64(k)
	return p.gain[k]*(1-t) + p.gain[k+1]*t
}

// record keeps the gain at each traced frequency for the hop's frames
// from at: the strongest of the bins nearest it, so a narrow cut
// between two traced frequencies still shows; or, where no bin is
// nearest it, as low down as the bins are far apart, the gain between
// the two bins it falls between.
func (p *processor) record(at int64) {
	for i, x := range p.traceAt {
		g := p.gainAt(x)
		if span := p.traceBins[i]; span[0] <= span[1] {
			g = 0
			for _, v := range p.gain[span[0] : span[1]+1] {
				if math.Abs(v) > math.Abs(g) {
					g = v
				}
			}
		}
		v := float32(g)
		for f := at; f < at+int64(p.hop); f += plug.TraceFrames {
			p.traces[i].Put(f, v)
		}
		p.traces[i].Put(at+int64(p.hop)-1, v)
	}
}

// Latency implements [plug.Processor]: the sound comes out a frame,
// less one, late, which is as soon as each frame of it has been added
// up from every frame of the transform over it.
func (p *processor) Latency() int { return p.latency }

// Tail implements [plug.Processor]: a frame of the transform spreads
// what each bin's gain does over its length, so the sound may ring on
// as long after the sound going in stops.
func (p *processor) Tail() int { return p.n }

// Reset implements [plug.Processor]: what it heard and held, let go.
func (p *processor) Reset() {
	for c := range p.in {
		clear(p.in[c])
		clear(p.out[c])
	}
	for _, a := range [][]float64{p.long, p.short, p.want, p.gain} {
		clear(a)
	}
	p.pos, p.count = 0, 0
	p.heardLong, p.heardShort = false, false
	p.fresh = true
	p.outGain = p.outTo
}

var (
	_ plug.Tracer   = (*processor)(nil)
	_ plug.Follower = (*panel)(nil)
	_ plug.Follower = (*chip)(nil)
)

// Trace implements [plug.Tracer]: the gain at traceFreqs[i], in
// decibels, by frame, as gainTrace+i.
func (p *processor) Trace(id uint32) *plug.Trace {
	if id >= gainTrace && id < gainTrace+traced {
		return &p.traces[id-gainTrace]
	}
	return nil
}
