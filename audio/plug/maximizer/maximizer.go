// Package maximizer makes a master as loud as asked without its peaks
// passing a ceiling: the last plugin of a chain. It turns the sound up
// by Gain and limits it, looking ahead, so its true peaks, between the
// samples too, stay under Ceiling. Its modes limit as a mastering
// engineer would for each kind of music: Transparent lets the gain come
// back fast after a lone peak and slowly through a loud passage; Punchy
// lets transients through before it turns them down; Loud softly clips
// the sound's peaks first, so the limiter has less to do. Character
// runs from fast to slow coming back, Stereo link from each channel
// limited apart to both as one, and Learn reads the whole track and sets
// the gain so it comes out as loud as Target.
package maximizer

import (
	"errors"
	"fmt"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/decibel"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/internal/least"
)

// The parameters' IDs.
const (
	GainID      uint32 = 1
	CeilingID   uint32 = 2
	ModeID      uint32 = 3
	CharacterID uint32 = 4
	LinkID      uint32 = 5
	TargetID    uint32 = 6
	// How far the maximizer turns the sound down now, in decibels, and
	// the true peak of what comes out: of the last block, and the most
	// since playing last jumped.
	ReductionID uint32 = 10
	PeakID      uint32 = 11
	PeakHoldID  uint32 = 12
)

// The modes.
const (
	Transparent = iota
	Punchy
	Loud
)

// modeNames name the modes, in their order.
var modeNames = []string{"Transparent", "Punchy", "Loud"}

func dbText(v float64) string { return fmt.Sprintf("%.1f dB", v) }

// Descriptor is the plugin, as it is registered.
var Descriptor = plug.Descriptor{
	ID:       "marrasen.maximizer",
	Name:     "Maximizer",
	Vendor:   "Marras",
	Version:  "0.1.0",
	Kind:     "Fx|Dynamics|Mastering",
	Tagline:  "Loudness & ceiling for the master",
	Keywords: []string{"limiter", "loudness", "lufs", "ceiling", "true peak", "master", "mastering", "clipper"},
	Params: []plug.Param{
		{ID: GainID, Name: "Gain", Unit: "dB", Max: 24, Flags: plug.Automatable,
			Format: func(v float64) string { return fmt.Sprintf("%+.1f dB", v) }},
		{ID: CeilingID, Name: "Ceiling", Unit: "dBTP", Min: -6, Max: 0, Default: -1, Flags: plug.Automatable,
			Format: func(v float64) string { return fmt.Sprintf("%.1f dB", v) }},
		{ID: ModeID, Name: "Mode", Max: 2, Steps: 2, Flags: plug.Automatable,
			Format: func(v float64) string { return modeNames[int(math.Round(v))] }},
		{ID: CharacterID, Name: "Character", Max: 10, Default: 4, Flags: plug.Automatable,
			Format: func(v float64) string { return fmt.Sprintf("%.1f", v) }},
		{ID: LinkID, Name: "Stereo link", Unit: "%", Max: 100, Default: 100, Flags: plug.Automatable,
			Format: func(v float64) string { return fmt.Sprintf("%.0f%%", v) }},
		{ID: TargetID, Name: "Target", Unit: "LUFS", Min: -24, Max: -6, Default: -14,
			Format: func(v float64) string { return fmt.Sprintf("%.1f LUFS", v) }},
		{ID: ReductionID, Name: "Reduction", Unit: "dB", Min: -24, Max: 0, Flags: plug.ReadOnly, Format: dbText},
		{ID: PeakID, Name: "True peak", Unit: "dBTP", Min: -60, Max: 6, Default: -60, Flags: plug.ReadOnly,
			Format: dbText},
		{ID: PeakHoldID, Name: "Most true peak", Unit: "dBTP", Min: -60, Max: 6, Default: -60, Flags: plug.ReadOnly,
			Format: dbText},
	},
	Patches: []plug.Patch{
		{Name: "Default"},
		{Name: "Gentle master", Params: map[uint32]float64{GainID: 3, CharacterID: 6, CeilingID: -1}},
		{Name: "Streaming, -14 LUFS", Params: map[uint32]float64{CeilingID: -1, TargetID: -14, CharacterID: 4}},
		{Name: "Loud and punchy", Params: map[uint32]float64{GainID: 8, ModeID: Punchy, CharacterID: 2, CeilingID: -0.3}},
		{Name: "Club", Params: map[uint32]float64{GainID: 10, ModeID: Loud, CharacterID: 3, CeilingID: -0.3, TargetID: -8}},
	},
	New: func(h plug.Host) plug.Plugin { return &plugin{h: h} },
}

func init() { plug.Register(Descriptor) }

// param returns the parameter id; every ID above is one.
func param(id uint32) plug.Param {
	p, _ := Descriptor.Param(id)
	return p
}

// lookahead and lateBy are how late the sound comes, in seconds and then
// frames more, the same in every mode; the limiter looks ahead that long
// less the true peaks' filter's delay.
const (
	lookahead = 0.005
	lateBy    = 6
)

// plugin is one maximizer.
type plugin struct {
	h    plug.Host
	proc processor
	// learner reads the whole track, for Learn, and learnt is what it
	// last learned, kept as the views come and go.
	learner learner
	learnt  learnt
}

// learnt is what Learn last learned: the gain, for the target, and
// whether it reached it; ok says it has learned at all.
type learnt struct {
	gain, target float64
	reached, ok  bool
}

// Processor implements [plug.Plugin].
func (p *plugin) Processor() plug.Processor { return &p.proc }

// State implements [plug.Plugin]: it keeps nothing but its parameters.
func (p *plugin) State() []byte { return nil }

// SetState implements [plug.Plugin]: it takes only an empty state.
func (p *plugin) SetState(b []byte) error {
	if len(b) > 0 {
		return errors.New("maximizer: the plugin keeps no state")
	}
	return nil
}

// Close implements [plug.Plugin]: a learning under way stops.
func (p *plugin) Close() { p.stopLearning() }

// View implements [plug.Viewer]: the reduction and the loudness in a
// chip, and the panel, which serves in full too.
func (p *plugin) View(s plug.Size, h plug.Host) gunim.Node {
	switch s {
	case plug.Chip:
		return newChip(h)
	case plug.Panel:
		return newPanel(p, h)
	default:
		return nil
	}
}

func coef(ms, rate float64) float64 { return 1 - math.Exp(-1/(ms/1000*rate)) }

func dbOf(v float64) float64 { return 20 * math.Log10(max(v, 1e-9)) }

func gainOf(db float64) float64 { return math.Pow(10, db/20) }

// settings are what the processor limits by, each as its parameter
// has it: the ceiling in dBTP, the link in a ratio.
type settings struct {
	gain, ceiling, character, link float64
	mode                           int
}

// settingsOf returns the settings the parameters' values in v say,
// their defaults where v has none.
func settingsOf(v func(id uint32) (float64, bool)) settings {
	get := func(id uint32) float64 {
		p := param(id)
		if x, ok := v(id); ok {
			return p.Clamp(x)
		}
		return p.Default
	}
	return settings{gain: get(GainID), ceiling: get(CeilingID), character: get(CharacterID), link: get(LinkID) / 100,
		mode: int(math.Round(get(ModeID)))}
}

// processor maximizes the sound: gain, then, in Loud, a soft clip, then
// the limiter, a core a channel, each fed what its channel's true peaks
// need, blended with the other's as far as the stereo link says.
type processor struct {
	rate float64
	// fresh is set from a reset to the first block, whose gain is taken
	// at once rather than glided to.
	fresh        bool
	glide        float64
	gain, gainTo float64
	ceiling      float64
	link         float64
	character    float64
	mode         int
	// ahead is the limiter's lookahead in frames; the sound is held
	// back ahead and the true peaks' filter's delay, in delay, a ring a
	// channel, at at.
	ahead int
	delay [2][]float32
	at    int
	peaks [2]truePeak
	outs  [2]truePeak
	cores [2]core
	// What the plugin reports: the least gain of the last block, the
	// true peak out of the last block and the most since a reset, in
	// decibels; and the reduction by frame, for the views.
	reduction, peak, peakHold plug.Meter
	held                      float64
	trace                     plug.Trace
	// charMS is the release the character sets, in milliseconds, and
	// fixed the release of the modes whose release holds still, worked
	// out by releases.
	charMS, fixed float64
	// run is what a run of frames holds between the stages; least and
	// blockPeak are the least gain and the most true peak out so far
	// this block; slow is how fast the slow reduction follows.
	run              runs
	least, blockPeak float64
	slow             float64
}

// core is one channel's limiter: for each frame the gain that keeps it
// under the ceiling; the gain applied, ahead frames later, is the least
// of those over the next avgLen frames and the frame itself, averaged
// over avgLen, so it falls smoothly to what the loudest needs as that
// arrives, starting avgLen frames before; and it rises again as the
// release says. The needs wait ahead less avgLen frames first, in lag,
// so a shorter attack starts falling later.
type core struct {
	lag   []float64
	lagAt int
	// need is the needs of the last avgLen+1 frames, for their least.
	need   *least.Window
	avg    []float64
	avgLen int
	avgAt  int
	avgSum float64
	gain   float64
	// slow is how far the gain has been down over the last second and a
	// half, in decibels, for Transparent's release; down how far it is
	// down now.
	slow, down float64
}

// Prepare implements [plug.Processor]: everything the maximizer keeps is
// made here, so that processing makes nothing.
func (p *processor) Prepare(s plug.Setup) {
	p.rate = s.Rate
	p.glide = coef(5, s.Rate)
	p.ahead = max(2, int(math.Round(s.Rate*lookahead))+lateBy-tpDelay)
	p.slow = coef(1500, s.Rate)
	p.run.make(s.MaxBlock)
	for c := range p.delay {
		p.delay[c] = make([]float32, p.ahead+tpDelay)
		p.cores[c] = core{lag: make([]float64, p.ahead), need: least.New(p.ahead + 1), avg: make([]float64, p.ahead)}
	}
	for _, prm := range Descriptor.Params {
		if prm.Flags&plug.ReadOnly == 0 {
			p.take(plug.Change{ID: prm.ID, Value: prm.Default})
		}
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
	case GainID:
		p.gainTo = gainOf(v)
		if p.fresh {
			p.gain = p.gainTo
		}
	case CeilingID:
		p.ceiling = gainOf(v)
	case ModeID:
		p.mode = int(math.Round(v))
		p.setAttack()
	case CharacterID:
		p.character = v
	case LinkID:
		p.link = v / 100
	}
}

// setAttack sets how many frames each core's gain falls over before a
// peak, by the mode: over the whole lookahead, smoothest; a third of it,
// Punchy, so a transient's start comes through; half of it, Loud. The
// sound is as late in every mode.
func (p *processor) setAttack() {
	n := p.ahead
	switch p.mode {
	case Punchy:
		n = max(1, p.ahead/3)
	case Loud:
		n = max(1, p.ahead/2)
	}
	for c := range p.cores {
		k := &p.cores[c]
		if k.avgLen == n || len(k.avg) == 0 {
			k.avgLen = n
			continue
		}
		// The average starts again over its new length, at the gain now,
		// and the needs waiting, and the least of them, again too.
		k.avgLen, k.avgAt = n, 0
		for i := range n {
			k.avg[i] = k.gain
		}
		k.avgSum = k.gain * float64(n)
		for i := range k.lag {
			k.lag[i] = 1
		}
		k.lagAt = 0
		k.need.Resize(n + 1)
	}
}

// release returns how much of the way back up a core's gain goes each
// frame: by the character, from 10 ms to about a second; faster for
// Punchy and Loud; and for Transparent, fast after a lone peak and slow
// through a loud passage, as far down as the gain has been a while.
//
// Only Transparent's changes from frame to frame; the others' are worked
// out once a block, in releases.
func (p *processor) release(k *core) float64 {
	if p.mode != Transparent {
		return p.fixed
	}
	sustained := 0.0
	if k.down > 0.01 {
		sustained = min(max(k.slow/k.down, 0), 1)
	}
	return fastCoef(p.charMS*(0.4+1.1*sustained)*p.rate/1000, p.rate)
}

// releases works out the release of the modes whose release holds still,
// for the block: fixed.
func (p *processor) releases() {
	p.charMS = 10 * math.Exp2(p.character*0.65)
	ms := p.charMS
	switch p.mode {
	case Punchy:
		ms *= 0.7
	case Loud:
		ms *= 0.8
	}
	p.fixed = coef(ms, p.rate)
}

// fastCoef is coef for a release of n frames, ms milliseconds at rate:
// as a series where the step is small, as it is for any release, to the
// sixth power, which leaves out under a part in ten billion.
func fastCoef(n, rate float64) float64 {
	x := 1 / n
	if x > 0.03 {
		return coef(n/rate*1000, rate)
	}
	// 1 - e^-x.
	return x * (1 - x*(1.0/2-x*(1.0/6-x*(1.0/24-x*(1.0/120-x*(1.0/720))))))
}

// softClip rounds x's peaks off as they near the ceiling, as tape does,
// past three quarters of it, toward no more than a quarter over it.
func softClip(x, ceiling float64) float64 {
	knee := 0.75 * ceiling
	a := math.Abs(x)
	if a <= knee {
		return x
	}
	room := 0.5 * ceiling
	return math.Copysign(knee+room*math.Tanh((a-knee)/room), x)
}

// Process implements [plug.Processor].
//
// It runs the block a stage at a time, between the frames parameters
// change at: the gain, and the true peaks in; then the limiter; then the
// true peaks out. A true peak is seven sums of 32 products each; a
// stage at a time, a processor works out many frames' side by side.
func (p *processor) Process(b *plug.Block) {
	if len(b.In) == 0 || len(b.Out) == 0 || len(p.delay[0]) == 0 {
		return
	}
	inL, outL := b.In[0], b.Out[0]
	inR, outR := inL, outL
	stereo := len(b.In) > 1 && len(b.Out) > 1
	if stereo {
		inR, outR = b.In[1], b.Out[1]
	}
	next := 0
	p.least, p.blockPeak = 1, 0
	p.releases()
	for i := 0; i < b.Frames; {
		if next < len(b.Changes) && b.Changes[next].Frame <= i {
			for next < len(b.Changes) && b.Changes[next].Frame <= i {
				p.take(b.Changes[next])
				next++
			}
			p.releases()
		}
		e := min(b.Frames, i+len(p.run.x[0]))
		if next < len(b.Changes) {
			e = min(e, b.Changes[next].Frame)
		}
		var or []float32
		if stereo {
			or = outR[i:e]
		}
		p.stages(inL[i:e], inR[i:e], outL[i:e], or, b.Position.Frame+int64(i))
		i = e
	}
	for ; next < len(b.Changes); next++ {
		p.take(b.Changes[next])
	}
	p.fresh = false
	p.reduction.Set(dbOf(p.least))
	p.peak.Set(dbOf(p.blockPeak))
	p.held = max(p.held, p.blockPeak)
	p.peakHold.Set(dbOf(p.held))
}

// runs is what a run of frames holds between the stages, a channel
// each: the sound gained, its true peaks, and the sound out.
type runs struct {
	x, tp, out [2][]float64
}

// make makes room for runs of up to n frames.
func (r *runs) make(n int) {
	n = max(n, 1)
	for c := range 2 {
		r.x[c], r.tp[c], r.out[c] = make([]float64, n), make([]float64, n), make([]float64, n)
	}
}

// stages runs a run of frames, inL and inR, through each stage in turn
// into outL and outR, outR nil where the plugin is mono; f is the run's
// first frame.
func (p *processor) stages(inL, inR, outL, outR []float32, f int64) {
	n := len(inL)
	r := &p.run
	x0, x1 := r.x[0][:n], r.x[1][:n]
	inR = inR[:n]
	for i := range n {
		p.gain += (p.gainTo - p.gain) * p.glide
		x0[i], x1[i] = float64(inL[i])*p.gain, float64(inR[i])*p.gain
	}
	if p.mode == Loud {
		for i := range x0 {
			x0[i], x1[i] = softClip(x0[i], p.ceiling), softClip(x1[i], p.ceiling)
		}
	}
	for c := range 2 {
		p.peaks[c].run(r.x[c][:n], r.tp[c][:n])
	}
	tp0, tp1 := r.tp[0][:n], r.tp[1][:n]
	o0, o1 := r.out[0][:n], r.out[1][:n]
	slow := p.slow
	for i := range n {
		need := [2]float64{1, 1}
		if tp0[i] > p.ceiling {
			need[0] = p.ceiling / tp0[i]
		}
		if tp1[i] > p.ceiling {
			need[1] = p.ceiling / tp1[i]
		}
		linked := min(need[0], need[1])
		var g [2]float64
		for c := range g {
			k := &p.cores[c]
			n := p.link*linked + (1-p.link)*need[c]
			g[c] = k.step(n, p.release(k))
			k.down = 0
			if g[c] != 1 {
				k.down = -decibel.Of(g[c])
			}
			k.slow += (k.down - k.slow) * slow
			// Let go to 0 far under anything heard, so after a long
			// silence it never sinks into numbers too small to work out
			// fast.
			if k.slow < 1e-9 {
				k.slow = 0
			}
		}
		// Out comes the frame held back as long as the true peaks'
		// filter lags and the limiter looks ahead, at the gain worked
		// out for it, never over the ceiling.
		dl, dr := float64(p.delay[0][p.at]), float64(p.delay[1][p.at])
		p.delay[0][p.at], p.delay[1][p.at] = float32(x0[i]), float32(x1[i])
		p.at++
		if p.at == len(p.delay[0]) {
			p.at = 0
		}
		l := max(-p.ceiling, min(dl*g[0], p.ceiling))
		r := max(-p.ceiling, min(dr*g[1], p.ceiling))
		o0[i], o1[i] = l, r
		outL[i] = float32(l)
		if outR != nil {
			outR[i] = float32(r)
		}
		p.least = min(p.least, g[0], g[1])
		p.trace.Put(f+int64(i), float32(-max(p.cores[0].down, p.cores[1].down)))
	}
	for c := range 2 {
		tp := r.tp[c][:n]
		p.outs[c].run(r.out[c][:n], tp)
		for _, v := range tp {
			p.blockPeak = max(p.blockPeak, v)
		}
	}
}

// step adds need, the gain a frame needs, and returns the gain for the
// frame ahead frames before: rising no faster than release.
func (k *core) step(need, release float64) float64 {
	// The need waits as long as the lookahead is longer than the attack.
	if wait := len(k.lag) - k.avgLen; wait > 0 {
		need, k.lag[k.lagAt] = k.lag[k.lagAt], need
		k.lagAt++
		if k.lagAt >= wait {
			k.lagAt = 0
		}
	}
	low := k.need.Push(need)
	k.avgSum += low - k.avg[k.avgAt]
	k.avg[k.avgAt] = low
	k.avgAt++
	if k.avgAt == k.avgLen {
		k.avgAt = 0
		// Summed anew once around, so rounding never builds up.
		k.avgSum = 0
		for _, v := range k.avg[:k.avgLen] {
			k.avgSum += v
		}
	}
	a := k.avgSum / float64(k.avgLen)
	if a < k.gain {
		k.gain = a
	} else {
		k.gain += (a - k.gain) * release
	}
	return k.gain
}

// reset lets go what the core held.
func (k *core) reset() {
	for i := range k.avg {
		k.avg[i] = 1
	}
	for i := range k.lag {
		k.lag[i] = 1
	}
	k.lagAt = 0
	k.avgSum, k.avgAt = float64(k.avgLen), 0
	k.need.Reset()
	k.gain, k.slow, k.down = 1, 0, 0
}

// Latency implements [plug.Processor]: the sound comes as late as the
// true peaks' filter lags and the limiter looks ahead.
func (p *processor) Latency() int { return p.ahead + tpDelay }

// Tail implements [plug.Processor]: the maximizer never rings.
func (p *processor) Tail() int { return 0 }

// Reset implements [plug.Processor]: what it held, let go.
func (p *processor) Reset() {
	for c := range p.delay {
		clear(p.delay[c])
		p.peaks[c].reset()
		p.outs[c].reset()
		p.cores[c].reset()
	}
	p.at = 0
	p.gain = p.gainTo
	p.fresh = true
	p.held = 0
	p.reduction.Set(0)
	p.peak.Set(-60)
	p.peakHold.Set(-60)
}

var (
	_ plug.Reporter = (*processor)(nil)
	_ plug.Tracer   = (*processor)(nil)
)

// Report implements [plug.Reporter].
func (p *processor) Report(id uint32) float64 {
	switch id {
	case ReductionID:
		return p.reduction.Get()
	case PeakID:
		return p.peak.Get()
	case PeakHoldID:
		return p.peakHold.Get()
	}
	return 0
}

// Trace implements [plug.Tracer]: the reduction, by frame.
func (p *processor) Trace(id uint32) *plug.Trace {
	if id == ReductionID {
		return &p.trace
	}
	return nil
}
