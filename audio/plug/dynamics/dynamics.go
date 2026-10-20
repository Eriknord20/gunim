// Package dynamics shapes the sound's loudness over time: a gate, which
// turns the sound down where it falls quiet; a compressor, which turns
// it down where it rises over a threshold; and a limiter, which keeps
// its peaks under a ceiling, looking ahead so it turns them down
// smoothly before they come. The sound goes in through Input, through
// the three in that order, and out through Output.
//
// Its aux input is a sidechain: where the host connects one, the gate
// and the compressor listen to it in place of the sound they shape, as
// a bass ducks under the kick drum. The limiter always listens to the
// sound itself, to keep it under its ceiling.
//
// Its panel draws the sound in and out as it plays, with how far each
// part turns it down, over knobs for every control, beside two classic
// VU meters.
package dynamics

import (
	"errors"
	"fmt"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/decibel"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/internal/least"
)

// The parameters' IDs, by part.
const (
	InputID  uint32 = 1
	OutputID uint32 = 2

	GateOnID        uint32 = 10
	GateThresholdID uint32 = 11
	GateRangeID     uint32 = 12
	GateAttackID    uint32 = 13
	GateReleaseID   uint32 = 14

	CompOnID        uint32 = 20
	CompThresholdID uint32 = 21
	CompRatioID     uint32 = 22
	CompKneeID      uint32 = 23
	CompAttackID    uint32 = 24
	CompReleaseID   uint32 = 25
	CompMakeupID    uint32 = 26
	// The compressor's sidechain: what it listens to, the whole sound or
	// a band of it, by SideModeID, about SideFreqID; SideListenID plays
	// what it listens to, to find the band by ear. The sound is the
	// sidechain input's, where one is connected, and the plugin's own
	// otherwise.
	SideModeID   uint32 = 27
	SideFreqID   uint32 = 28
	SideListenID uint32 = 29

	LimitOnID      uint32 = 30
	LimitCeilingID uint32 = 31
	LimitReleaseID uint32 = 32

	// MeterID is the level that reads 0 VU on the output's meter.
	MeterID uint32 = 40

	// How far each part turns the sound down now, in decibels, as the
	// plugin reports it, and keeps it by frame.
	GateGRID  uint32 = 50
	CompGRID  uint32 = 51
	LimitGRID uint32 = 52
)

// db writes decibels, to a tenth under 10 and whole over.
func db(v float64) string {
	if math.Abs(v) >= 10 {
		return fmt.Sprintf("%.0f dB", v)
	}
	return fmt.Sprintf("%.1f dB", v)
}

func signed(v float64) string { return fmt.Sprintf("%+.1f dB", v) }

func ms(v float64) string {
	if v < 10 {
		return fmt.Sprintf("%.1f ms", v)
	}
	return fmt.Sprintf("%.0f ms", v)
}

func hz(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.3g kHz", v/1000)
	}
	return fmt.Sprintf("%.0f Hz", v)
}

// SidechainPort is the sidechain's place in the plugin's aux inputs.
const SidechainPort = 0

// The sidechain's modes: the whole sound, or what is under, about or
// over its frequency.
const (
	SideFull = iota
	SideLow
	SideBand
	SideHigh
	sideModes
)

var (
	sideNames = []string{"Full", "Low", "Band", "High"}
	sideTips  = []string{
		"The compressor listens to the whole sound",
		"The compressor listens only under the frequency: the kick and the bass pump it",
		"The compressor listens only about the frequency, an octave wide: a harsh band, or the voice",
		"The compressor listens only over the frequency: sibilance and cymbals",
	}
)

// sideQ is how wide the sidechain's band is: an octave, about.
const sideQ = 1.41

func onOff(v float64) string {
	if v > 0.5 {
		return "On"
	}
	return "Off"
}

// Descriptor is the plugin, as it is registered.
var Descriptor = plug.Descriptor{
	ID:       "marrasen.dynamics",
	Name:     "Dynamics",
	Vendor:   "Marras",
	Version:  "0.1.0",
	Kind:     "Fx|Dynamics",
	Tagline:  "Gate, compressor & limiter",
	Keywords: []string{"compressor", "compression", "limiter", "gate", "noise gate", "sidechain", "ducking", "threshold", "ratio", "loudness"},
	AuxIn:    []plug.AudioPort{{Name: "Sidechain"}},
	Params: []plug.Param{
		{ID: InputID, Name: "Input", Unit: "dB", Min: -24, Max: 24, Flags: plug.Automatable, Format: signed},
		{ID: OutputID, Name: "Output", Unit: "dB", Min: -24, Max: 24, Flags: plug.Automatable, Format: signed},

		{ID: GateOnID, Name: "Gate", Max: 1, Steps: 1, Flags: plug.Automatable, Format: onOff},
		{ID: GateThresholdID, Name: "Threshold", Unit: "dB", Min: -90, Max: 0, Default: -60, Flags: plug.Automatable, Format: db},
		{ID: GateRangeID, Name: "Range", Unit: "dB", Min: 0, Max: 90, Default: 40, Flags: plug.Automatable, Format: db},
		{ID: GateAttackID, Name: "Attack", Unit: "ms", Min: 0.1, Max: 50, Default: 1, Log: true, Flags: plug.Automatable, Format: ms},
		{ID: GateReleaseID, Name: "Release", Unit: "ms", Min: 5, Max: 2000, Default: 150, Log: true, Flags: plug.Automatable, Format: ms},

		{ID: CompOnID, Name: "Compressor", Max: 1, Default: 1, Steps: 1, Flags: plug.Automatable, Format: onOff},
		{ID: CompThresholdID, Name: "Threshold", Unit: "dB", Min: -60, Max: 0, Default: -18, Flags: plug.Automatable, Format: db},
		{ID: CompRatioID, Name: "Ratio", Min: 1, Max: 20, Default: 3, Log: true, Flags: plug.Automatable,
			Format: func(v float64) string { return fmt.Sprintf("%.1f:1", v) }},
		{ID: CompKneeID, Name: "Knee", Unit: "dB", Min: 0, Max: 24, Default: 6, Flags: plug.Automatable, Format: db},
		{ID: CompAttackID, Name: "Attack", Unit: "ms", Min: 0.1, Max: 400, Default: 10, Log: true, Flags: plug.Automatable, Format: ms},
		{ID: CompReleaseID, Name: "Release", Unit: "ms", Min: 5, Max: 2000, Default: 120, Log: true, Flags: plug.Automatable, Format: ms},
		{ID: CompMakeupID, Name: "Makeup", Unit: "dB", Min: 0, Max: 24, Flags: plug.Automatable, Format: signed},
		{ID: SideModeID, Name: "Sidechain", Max: sideModes - 1, Steps: sideModes - 1, Flags: plug.Automatable,
			Format: func(v float64) string { return sideNames[int(math.Round(v))] }},
		{ID: SideFreqID, Name: "Frequency", Unit: "Hz", Min: 20, Max: 20000, Default: 1000, Log: true,
			Flags: plug.Automatable, Format: hz},
		{ID: SideListenID, Name: "Listen", Max: 1, Steps: 1, Format: onOff},

		{ID: LimitOnID, Name: "Limiter", Max: 1, Default: 1, Steps: 1, Flags: plug.Automatable, Format: onOff},
		{ID: LimitCeilingID, Name: "Ceiling", Unit: "dB", Min: -12, Max: 0, Default: -1, Flags: plug.Automatable, Format: db},
		{ID: LimitReleaseID, Name: "Release", Unit: "ms", Min: 10, Max: 1000, Default: 120, Log: true, Flags: plug.Automatable, Format: ms},

		{ID: MeterID, Name: "0 VU", Unit: "dBFS", Min: -24, Max: -6, Default: -14, Steps: 18,
			Format: func(v float64) string { return fmt.Sprintf("%.0f dBFS", v) }},

		{ID: GateGRID, Name: "Gate reduction", Unit: "dB", Min: -90, Max: 0, Flags: plug.ReadOnly, Format: db},
		{ID: CompGRID, Name: "Compression", Unit: "dB", Min: -60, Max: 0, Flags: plug.ReadOnly, Format: db},
		{ID: LimitGRID, Name: "Limiting", Unit: "dB", Min: -24, Max: 0, Flags: plug.ReadOnly, Format: db},
	},
	Patches: []plug.Patch{
		{Name: "Default"},
		{Name: "Gentle glue", Params: map[uint32]float64{CompThresholdID: -20, CompRatioID: 2, CompKneeID: 12,
			CompAttackID: 30, CompReleaseID: 250, CompMakeupID: 1.5}},
		{Name: "Vocal leveller", Params: map[uint32]float64{CompThresholdID: -26, CompRatioID: 4, CompKneeID: 6,
			CompAttackID: 5, CompReleaseID: 80, CompMakeupID: 5}},
		{Name: "Brickwall", Params: map[uint32]float64{CompOnID: 0, InputID: 4, LimitCeilingID: -1, LimitReleaseID: 60}},
		{Name: "Noise gate", Params: map[uint32]float64{GateOnID: 1, GateThresholdID: -50, GateRangeID: 30,
			GateAttackID: 0.5, GateReleaseID: 200, CompOnID: 0, LimitOnID: 0}},
	},
	New: func(h plug.Host) plug.Plugin { return &plugin{} },
}

func init() { plug.Register(Descriptor) }

// param returns the parameter id; every ID above is one.
func param(id uint32) plug.Param {
	p, _ := Descriptor.Param(id)
	return p
}

// lookahead is how far ahead the limiter looks, in seconds: as late as
// its sound comes, whether the limiter is on or not, so switching it
// moves nothing in time.
const lookahead = 0.005

// gateHysteresis is how far under its threshold the sound must fall,
// in decibels, for an open gate to close, so a sound hovering at the
// threshold does not chatter.
const gateHysteresis = 4

// plugin is one dynamics plugin.
type plugin struct{ proc processor }

// Processor implements [plug.Plugin].
func (p *plugin) Processor() plug.Processor { return &p.proc }

// State implements [plug.Plugin]: it keeps nothing but its parameters.
func (p *plugin) State() []byte { return nil }

// SetState implements [plug.Plugin]: it takes only an empty state.
func (p *plugin) SetState(b []byte) error {
	if len(b) > 0 {
		return errors.New("dynamics: the plugin keeps no state")
	}
	return nil
}

// Close implements [plug.Plugin].
func (p *plugin) Close() {}

// View implements [plug.Viewer]: a bar of the reduction in a chip, and
// the panel, which serves in full too.
func (p *plugin) View(s plug.Size, h plug.Host) gunim.Node {
	switch s {
	case plug.Chip:
		return newChip(h)
	case plug.Panel:
		return newPanel(h)
	default:
		return nil
	}
}

// coef is how far a one-pole filter moves toward its target each frame
// to get most of the way there in ms milliseconds.
func coef(ms, rate float64) float64 { return 1 - math.Exp(-1/(ms/1000*rate)) }

func dbOf(v float64) float64 { return 20 * math.Log10(max(v, 1e-9)) }

func gainOf(db float64) float64 { return math.Pow(10, db/20) }

// fastDB is dbOf for every frame: exact where v is 1, no reduction, the
// commonest, and otherwise to a ten-millionth of a decibel.
func fastDB(v float64) float64 {
	if v == 1 {
		return 0
	}
	return decibel.Of(v)
}

// processor shapes the sound. Every part works on both channels as one,
// by the louder, so the stereo image holds.
type processor struct {
	rate float64
	// fresh is set from a reset to the first block, whose gains are
	// taken at once rather than glided to.
	fresh bool
	glide float64
	// Input and output gains, as ratios, gliding to where they are set.
	in, inTo, out, outTo float64

	gate gate
	comp comp
	lim  limiter
	side side

	// The least gain of each part over the last block, in decibels, as
	// the plugin reports it, and by frame, for the views.
	gateGR, compGR, limGR            plug.Meter
	gateTrace, compTrace, limitTrace plug.Trace
	// pending holds the gate's and compressor's reductions until the
	// frames they shape come out, lookahead later.
	pending []float32
	// run is what a run of frames holds between the stages, and the
	// least gains are each part's so far this block.
	run                            runs
	gateLeast, compLeast, limLeast float64
}

// gate turns the sound down by Range where it falls under Threshold,
// opening over Attack and closing over Release.
type gate struct {
	on                 bool
	threshold, rangeDB float64
	attack, release    float64
	env, fall          float64
	open               bool
	gain               float64 // in decibels, 0 open
	// openAt and shutAt are the threshold, and the threshold less the
	// hysteresis, as ratios, which the envelope is held against: as
	// ratios, not decibels, so no sample needs its logarithm.
	openAt, shutAt float64
}

// comp turns the sound down where it rises over Threshold, by Ratio,
// softly over Knee, taking Attack to turn down and Release to let go.
type comp struct {
	on                      bool
	threshold, ratio, knee  float64
	attack, release, makeup float64
	makeupTo                float64
	gr                      float64 // in decibels, 0 or less
	// kneeAt is where the knee starts, as a ratio: a peak no louder is
	// not turned down.
	kneeAt float64
}

// side is the compressor's sidechain: a filter on each channel, of the
// sound the compressor listens to, where its mode is a band.
type side struct {
	mode   int
	freq   float64
	listen bool
	b      [3]float64 // b0, b1, b2
	a      [2]float64 // a1, a2
	z      [2][2]float64
}

// set works the filter out for its mode and frequency, as Robert
// Bristow-Johnson's cookbook has it: a low or high pass, Butterworth,
// or a band pass whose peak passes at its level.
func (s *side) set(rate float64) {
	w := 2 * math.Pi * min(s.freq, rate*0.45) / rate
	q := math.Sqrt2 / 2
	if s.mode == SideBand {
		q = sideQ
	}
	cos, alpha := math.Cos(w), math.Sin(w)/(2*q)
	a0 := 1 + alpha
	switch s.mode {
	case SideLow:
		s.b = [3]float64{(1 - cos) / 2 / a0, (1 - cos) / a0, (1 - cos) / 2 / a0}
	case SideHigh:
		s.b = [3]float64{(1 + cos) / 2 / a0, -(1 + cos) / a0, (1 + cos) / 2 / a0}
	default:
		s.b = [3]float64{alpha / a0, 0, -alpha / a0}
	}
	s.a = [2]float64{-2 * cos / a0, (1 - alpha) / a0}
}

// run filters x, of channel c.
func (s *side) run(c int, x float64) float64 {
	z := &s.z[c]
	y := s.b[0]*x + z[0]
	z[0] = s.b[1]*x - s.a[0]*y + z[1]
	z[1] = s.b[2]*x - s.a[1]*y
	return y
}

// limiter keeps the sound under Ceiling. For each frame it works out
// the gain that keeps the frame under the ceiling; the gain applied,
// lookahead later, is the least of those over the frames to come,
// averaged over as many, so it falls smoothly to what the loudest needs
// as that arrives, and rises again as slow as Release says.
type limiter struct {
	on bool
	// ahead is the lookahead in frames, delay the frames held back over
	// it, and at where in delay the next goes.
	ahead int
	delay []float32
	at    int
	// need is the gains the last ahead+1 frames need, for their least.
	need *least.Window
	// The average of the least gains over the last ahead frames: their
	// ring, where the next goes, and their sum.
	avg    []float64
	avgAt  int
	avgSum float64
	gain   float64
	// ceiling is the most a sample may be, and release how far the gain
	// rises toward what the sound allows each frame.
	ceiling, release float64
}

// Prepare implements [plug.Processor]: everything the plugin keeps is
// made here, so that processing makes nothing.
func (p *processor) Prepare(s plug.Setup) {
	p.rate = s.Rate
	p.glide = coef(5, s.Rate)
	l := &p.lim
	l.ahead = max(1, int(math.Round(s.Rate*lookahead)))
	l.delay = make([]float32, 2*l.ahead)
	l.need = least.New(l.ahead + 1)
	l.avg = make([]float64, l.ahead)
	p.pending = make([]float32, 2*l.ahead)
	p.run.make(s.MaxBlock)
	p.gate.fall = coef(10, s.Rate)
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
	v, rate := prm.Clamp(c.Value), p.rate
	g, k, l := &p.gate, &p.comp, &p.lim
	switch c.ID {
	case InputID:
		p.inTo = gainOf(v)
	case OutputID:
		p.outTo = gainOf(v)
	case GateOnID:
		g.on = v > 0.5
	case GateThresholdID:
		g.threshold = v
		g.openAt, g.shutAt = gainOf(v), gainOf(v-gateHysteresis)
	case GateRangeID:
		g.rangeDB = v
	case GateAttackID:
		g.attack = coef(v, rate)
	case GateReleaseID:
		g.release = coef(v, rate)
	case CompOnID:
		k.on = v > 0.5
	case CompThresholdID:
		k.threshold = v
		k.kneeAt = gainOf(k.threshold - k.knee/2)
	case CompRatioID:
		k.ratio = v
	case CompKneeID:
		k.knee = v
		k.kneeAt = gainOf(k.threshold - k.knee/2)
	case CompAttackID:
		k.attack = coef(v, rate)
	case CompReleaseID:
		k.release = coef(v, rate)
	case CompMakeupID:
		k.makeupTo = gainOf(v)
	case SideModeID:
		p.side.mode = int(math.Round(v))
		p.side.set(rate)
	case SideFreqID:
		p.side.freq = v
		p.side.set(rate)
	case SideListenID:
		p.side.listen = v > 0.5
	case LimitOnID:
		l.on = v > 0.5
	case LimitCeilingID:
		l.ceiling = gainOf(v)
	case LimitReleaseID:
		l.release = coef(v, rate)
	}
	if p.fresh {
		p.in, p.out, k.makeup = p.inTo, p.outTo, k.makeupTo
	}
}

// Process implements [plug.Processor].
//
// It runs the block a stage at a time, between the frames parameters
// change at: the input's gain and what the gate and the compressor
// hear, over every frame; then the gate over every frame; then the
// compressor; then the gain they shape the sound by; then the limiter.
// Each frame's work through every stage is a long chain, each step
// waiting on the last, and a processor can only work a frame or two
// ahead; a stage at a time, its frames are many short chains side by
// side, and it works them all at once.
func (p *processor) Process(b *plug.Block) {
	if len(b.In) == 0 || len(b.Out) == 0 || len(p.pending) == 0 {
		return
	}
	inL, outL := b.In[0], b.Out[0]
	inR, outR := inL, outL
	stereo := len(b.In) > 1 && len(b.Out) > 1
	if stereo {
		inR, outR = b.In[1], b.Out[1]
	}
	// The sidechain, where one is connected, is what the gate and the
	// compressor listen to.
	var keyL, keyR []float32
	if key := b.AuxInput(SidechainPort); key != nil {
		keyL, keyR = key[0], key[min(1, len(key)-1)]
	}
	next := 0
	p.gateLeast, p.compLeast, p.limLeast = 0, 0, 1
	for i := 0; i < b.Frames; {
		for next < len(b.Changes) && b.Changes[next].Frame <= i {
			p.take(b.Changes[next])
			next++
		}
		e := min(b.Frames, i+len(p.run.xl))
		if next < len(b.Changes) {
			e = min(e, b.Changes[next].Frame)
		}
		var kl, kr []float32
		if keyL != nil {
			kl, kr = keyL[i:e], keyR[i:e]
		}
		var or []float32
		if stereo {
			or = outR[i:e]
		}
		p.stages(inL[i:e], inR[i:e], kl, kr, outL[i:e], or, b.Position.Frame+int64(i))
		i = e
	}
	for ; next < len(b.Changes); next++ {
		p.take(b.Changes[next])
	}
	p.fresh = false
	p.gateGR.Set(p.gateLeast)
	p.compGR.Set(p.compLeast)
	p.limGR.Set(dbOf(p.limLeast))
}

// runs is what a run of frames holds between the stages.
type runs struct {
	// xl and xr are the sound, gained in, then shaped; key the peaks the
	// gate hears, heard the compressor's; sl and sr what the compressor
	// listens to, for Listen; gdb and cdb the gate's and compressor's
	// gains; out the output's gain, frame by frame.
	xl, xr, key, heard, sl, sr, gdb, cdb, out []float64
}

// make makes room for runs of up to n frames.
func (r *runs) make(n int) {
	n = max(n, 1)
	for _, s := range []*[]float64{&r.xl, &r.xr, &r.key, &r.heard, &r.sl, &r.sr, &r.gdb, &r.cdb, &r.out} {
		*s = make([]float64, n)
	}
}

// stages runs a run of frames, inL and inR, through each part in turn
// into outL and outR, outR nil where the plugin is mono; keyL and keyR
// are the sidechain's, or nil; f is the run's first frame.
func (p *processor) stages(inL, inR, keyL, keyR, outL, outR []float32, f int64) {
	n := len(inL)
	r := &p.run
	xl, xr, key, heard := r.xl[:n], r.xr[:n], r.key[:n], r.heard[:n]
	sl, sr, gdb, cdb, out := r.sl[:n], r.sr[:n], r.gdb[:n], r.cdb[:n], r.out[:n]
	inR = inR[:n]
	g, k, l := &p.gate, &p.comp, &p.lim
	for i := range n {
		p.in += (p.inTo - p.in) * p.glide
		p.out += (p.outTo - p.out) * p.glide
		out[i] = p.out
		xl[i], xr[i] = float64(inL[i])*p.in, float64(inR[i])*p.in
	}
	// The gate listens to the sound, or the sidechain where one is
	// connected; the compressor to that, whole or a band of it.
	if keyL != nil {
		keyL, keyR = keyL[:n], keyR[:n]
		for i := range key {
			sl[i], sr[i] = float64(keyL[i]), float64(keyR[i])
			key[i] = max(math.Abs(sl[i]), math.Abs(sr[i]))
		}
	} else {
		copy(sl, xl)
		copy(sr, xr)
		for i := range key {
			key[i] = max(math.Abs(xl[i]), math.Abs(xr[i]))
		}
	}
	if p.side.mode != SideFull {
		for i := range heard {
			sl[i], sr[i] = p.side.run(0, sl[i]), p.side.run(1, sr[i])
			heard[i] = max(math.Abs(sl[i]), math.Abs(sr[i]))
		}
	} else {
		copy(heard, key)
	}
	for i, v := range key {
		gdb[i] = g.step(v)
	}
	for i, v := range heard {
		cdb[i] = k.step(v, gdb[i])
	}
	for i := range xl {
		k.makeup += (k.makeupTo - k.makeup) * p.glide
		// No reduction, the commonest, needs no gain worked out.
		shape := k.makeup
		if d := gdb[i] + cdb[i]; d != 0 {
			shape *= decibel.Gain(d)
		}
		xl[i], xr[i] = xl[i]*shape, xr[i]*shape
	}
	if p.side.listen {
		// What the compressor listens to, in place of the sound.
		copy(xl, sl)
		copy(xr, sr)
	}
	gateLeast, compLeast, limLeast := p.gateLeast, p.compLeast, p.limLeast
	for i := range xl {
		// The gate's and compressor's reductions of this frame come out
		// with it, lookahead later; those of the frame coming out now go
		// on the traces.
		at := 2 * l.at
		gOut, cOut := p.pending[at], p.pending[at+1]
		p.pending[at], p.pending[at+1] = float32(gdb[i]), float32(cdb[i])

		dl, dr, lg := l.step(xl[i], xr[i])
		p.gateTrace.Put(f+int64(i), gOut)
		p.compTrace.Put(f+int64(i), cOut)
		p.limitTrace.Put(f+int64(i), float32(fastDB(lg)))
		outL[i] = float32(dl * out[i])
		if outR != nil {
			outR[i] = float32(dr * out[i])
		}
		gateLeast, compLeast, limLeast = min(gateLeast, gdb[i]), min(compLeast, cdb[i]), min(limLeast, lg)
	}
	p.gateLeast, p.compLeast, p.limLeast = gateLeast, compLeast, limLeast
	// The sidechain's filter, fed silence, falls away for good too.
	for c := range p.side.z {
		for j, v := range p.side.z[c] {
			if math.Abs(v) < 1e-30 {
				p.side.z[c][j] = 0
			}
		}
	}
}

// step takes the next frame's peak and returns the gate's gain for it,
// in decibels: 0 open, -Range shut.
func (g *gate) step(peak float64) float64 {
	// Up to the peak at once, or down toward it by the fall: the more of
	// the two.
	g.env = max(peak, g.env+(peak-g.env)*g.fall)
	// Numbers falling away toward 0 for good sink, in time, into ones so
	// small the processor works them out slowly: after seconds of
	// silence, the envelope, and after a second open, the gain. Each is
	// let go to 0 at -600 dB and -1e-9 dB, far under hearing.
	if g.env < 1e-30 {
		g.env = 0
	}
	switch {
	case !g.on:
		g.open = true
	case g.open && g.env < g.shutAt:
		g.open = false
	case !g.open && g.env >= g.openAt:
		g.open = true
	}
	to, c := -g.rangeDB, g.release
	if g.open {
		to, c = 0, g.attack
	}
	g.gain += (to - g.gain) * c
	if g.gain > -1e-9 {
		g.gain = 0
	}
	return g.gain
}

// step takes the next frame's peak, as a ratio, and the gate's gain on
// it, in decibels, and returns the compressor's gain for it, in
// decibels.
func (k *comp) step(peak, gate float64) float64 {
	// A peak under the knee, as most are, is not turned down: it needs
	// no decibels worked out. The gate only takes it further under.
	want := 0.0
	if k.on && peak > k.kneeAt {
		want = k.curve(decibel.Of(peak) + gate)
	}
	// The attack where the gain is to fall, the release where it is to
	// rise: picked by the sign of the step, not asked, as on loud sound
	// it changes from frame to frame.
	d := want - k.gr
	k.gr += d * [2]float64{k.release, k.attack}[math.Float64bits(d)>>63]
	// Let go to 0 as the gate's gain is, so it never sinks into numbers
	// too small to work out fast.
	if k.gr > -1e-9 {
		k.gr = 0
	}
	return k.gr
}

// curve is how far the compressor turns down a level, in decibels: by
// the ratio over the threshold, bending into it over the knee.
func (k *comp) curve(level float64) float64 {
	over, slope := level-k.threshold, 1/k.ratio-1
	switch {
	case 2*over <= -k.knee:
		return 0
	case 2*math.Abs(over) < k.knee:
		x := over + k.knee/2
		return slope * x * x / (2 * k.knee)
	default:
		return slope * over
	}
}

// step takes the next frame and returns the frame lookahead before it,
// limited, with the gain it was limited by.
func (l *limiter) step(left, right float64) (outL, outR, gain float64) {
	need := 1.0
	if peak := max(math.Abs(left), math.Abs(right)); l.on && peak > l.ceiling {
		need = l.ceiling / peak
	}
	g := l.smooth(l.need.Push(need))
	dl, dr := float64(l.delay[2*l.at]), float64(l.delay[2*l.at+1])
	l.delay[2*l.at], l.delay[2*l.at+1] = float32(left), float32(right)
	l.at++
	if l.at == l.ahead {
		l.at = 0
	}
	dl, dr = dl*g, dr*g
	if l.on {
		// Rounding may take a sample a hair past the ceiling.
		dl, dr = max(-l.ceiling, min(dl, l.ceiling)), max(-l.ceiling, min(dr, l.ceiling))
	}
	return dl, dr, g
}

// smooth averages low, the least gain, over the last ahead frames, and lets the gain
// rise from there no faster than the release.
func (l *limiter) smooth(low float64) float64 {
	l.avgSum += low - l.avg[l.avgAt]
	l.avg[l.avgAt] = low
	l.avgAt++
	if l.avgAt == len(l.avg) {
		l.avgAt = 0
		// Summed anew once around, so rounding never builds up.
		l.avgSum = 0
		for _, v := range l.avg {
			l.avgSum += v
		}
	}
	// Down at once to a, or up toward it by the release: the lesser of
	// the two, which is which as a is under the gain or not, without
	// asking which.
	a := l.avgSum / float64(len(l.avg))
	l.gain = min(a, l.gain+(a-l.gain)*l.release)
	return l.gain
}

// Latency implements [plug.Processor]: the sound comes the lookahead
// late.
func (p *processor) Latency() int { return p.lim.ahead }

// Tail implements [plug.Processor]: the plugin never rings.
func (p *processor) Tail() int { return 0 }

// Reset implements [plug.Processor]: what it held, let go.
func (p *processor) Reset() {
	l := &p.lim
	clear(l.delay)
	clear(p.pending)
	for i := range l.avg {
		l.avg[i] = 1
	}
	l.avgSum, l.avgAt = float64(len(l.avg)), 0
	l.need.Reset()
	l.at = 0
	l.gain = 1
	p.gate.env, p.gate.open, p.gate.gain = 0, true, 0
	p.comp.gr = 0
	p.side.z = [2][2]float64{}
	p.in, p.out, p.comp.makeup = p.inTo, p.outTo, p.comp.makeupTo
	p.fresh = true
	p.gateGR.Set(0)
	p.compGR.Set(0)
	p.limGR.Set(0)
}

var (
	_ plug.Reporter = (*processor)(nil)
	_ plug.Tracer   = (*processor)(nil)
)

// Report implements [plug.Reporter]: each part's reduction.
func (p *processor) Report(id uint32) float64 {
	switch id {
	case GateGRID:
		return p.gateGR.Get()
	case CompGRID:
		return p.compGR.Get()
	case LimitGRID:
		return p.limGR.Get()
	}
	return 0
}

// Trace implements [plug.Tracer]: each part's reduction, by frame.
func (p *processor) Trace(id uint32) *plug.Trace {
	switch id {
	case GateGRID:
		return &p.gateTrace
	case CompGRID:
		return &p.compTrace
	case LimitGRID:
		return &p.limitTrace
	}
	return nil
}
