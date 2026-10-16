// Package impact shapes a sound's micro-dynamics, a band of pitches at a
// time, as a mastering engineer's transient shaper does: it makes each
// hit stand out more against the sound that follows it, which is called
// adding punch, or less, which smooths it, without the pumping a
// compressor brings. It splits the sound into four bands at three
// crossovers, and follows each band's loudness twice: closely, as each
// hit rises, and loosely, over about the length of a hit. Where the
// close one stands over the loose one, a hit is starting, and the band
// is turned up, for punch, or down, to smooth it, by as many decibels
// as the one stands over the other, times the punch. A steady sound,
// the two alike, passes as it was, and so does a sound dying away: the
// loose one falls with the close one, so the next hit starts from a
// band left as it was.
package impact

import (
	"errors"
	"fmt"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/xover"
)

// bands is how many bands the plugin splits the sound into.
const bands = xover.Bands

// The parameters' IDs: the crossovers, each band's punch, the length of
// a hit, the mix and the output, and how far each band is turned as it
// plays.
const (
	CrossoverID uint32 = 1  // to 3
	PunchID     uint32 = 10 // to 13
	LengthID    uint32 = 20
	MixID       uint32 = 30
	OutputID    uint32 = 31
	ChangeID    uint32 = 40 // to 43
)

// bandNames name the bands, low to high.
var bandNames = [bands]string{"Low", "Low mid", "High mid", "High"}

func pct(v float64) string { return fmt.Sprintf("%.0f%%", v) }

// punch writes a band's punch, its sign always shown, as it adds punch
// over zero and smooths under.
func punch(v float64) string {
	if math.Round(v) == 0 {
		return "0%"
	}
	return fmt.Sprintf("%+.0f%%", v)
}

func hz(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.3g kHz", v/1000)
	}
	return fmt.Sprintf("%.0f Hz", v)
}

func ms(v float64) string { return fmt.Sprintf("%.0f ms", v) }

func db(v float64) string { return fmt.Sprintf("%+.1f dB", v) }

// Descriptor is the plugin, as it is registered.
var Descriptor = func() plug.Descriptor {
	d := plug.Descriptor{
		ID:       "marrasen.impact",
		Name:     "Impact",
		Vendor:   "Marras",
		Version:  "0.1.0",
		Kind:     "Fx|Dynamics|Mastering",
		Tagline:  "Transient shaper",
		Keywords: []string{"transient", "punch", "attack", "sustain", "micro-dynamics", "smooth", "multiband", "drums"},
		New:      func(h plug.Host) plug.Plugin { return &plugin{} },
	}
	homes := [bands - 1][3]float64{{30, 1000, 120}, {200, 5000, 1200}, {1000, 16000, 6000}}
	for i, x := range homes {
		d.Params = append(d.Params, plug.Param{ID: CrossoverID + uint32(i), Name: fmt.Sprintf("Split %d", i+1),
			Unit: "Hz", Min: x[0], Max: x[1], Default: x[2], Log: true, Flags: plug.Automatable, Format: hz})
	}
	for b := range bands {
		d.Params = append(d.Params, plug.Param{ID: PunchID + uint32(b), Name: "Punch", Unit: "%", Min: -100,
			Max: 100, Flags: plug.Automatable, Format: punch})
	}
	d.Params = append(d.Params,
		plug.Param{ID: LengthID, Name: "Length", Unit: "ms", Min: 10, Max: 300, Default: 60, Log: true,
			Flags: plug.Automatable, Format: ms},
		plug.Param{ID: MixID, Name: "Mix", Unit: "%", Max: 100, Default: 100, Flags: plug.Automatable, Format: pct},
		plug.Param{ID: OutputID, Name: "Output", Unit: "dB", Min: -12, Max: 12, Flags: plug.Automatable, Format: db})
	for b := range bands {
		d.Params = append(d.Params, plug.Param{ID: ChangeID + uint32(b), Name: bandNames[b] + " change", Unit: "dB",
			Max: gainLimit, Flags: plug.ReadOnly, Format: db})
	}
	d.Patches = []plug.Patch{
		{Name: "Default"},
		{Name: "Punchy drums", Params: map[uint32]float64{PunchID: 40, PunchID + 1: 50, PunchID + 2: 35, PunchID + 3: 20,
			LengthID: 50}},
		{Name: "Tight low end", Params: map[uint32]float64{PunchID: 45, PunchID + 1: 20, LengthID: 90}},
		{Name: "Smooth top", Params: map[uint32]float64{PunchID + 2: -20, PunchID + 3: -40, LengthID: 30}},
		{Name: "Glue", Params: map[uint32]float64{PunchID: -25, PunchID + 1: -25, PunchID + 2: -25, PunchID + 3: -25,
			LengthID: 120}},
	}
	return d
}()

func init() { plug.Register(Descriptor) }

// param returns the parameter id; every ID above is one.
func param(id uint32) plug.Param {
	p, _ := Descriptor.Param(id)
	return p
}

// plugin is one Impact.
type plugin struct{ proc processor }

// Processor implements [plug.Plugin].
func (p *plugin) Processor() plug.Processor { return &p.proc }

// State implements [plug.Plugin]: it keeps nothing but its parameters.
func (p *plugin) State() []byte { return nil }

// SetState implements [plug.Plugin]: it takes only an empty state.
func (p *plugin) SetState(b []byte) error {
	if len(b) > 0 {
		return errors.New("impact: the plugin keeps no state")
	}
	return nil
}

// Close implements [plug.Plugin].
func (p *plugin) Close() {}

// View implements [plug.Viewer]: the bands in a chip, and the panel,
// which serves in full too.
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

// glideMS is how long a punch, the length, the mix or the output, or a
// crossover, takes to glide where it is set; xoverEvery how many frames
// apart a gliding crossover's filters, and the followers' speeds, are
// worked out anew. gainLimit is the most, in decibels, a band is turned
// up or down; floor is the power, a hundred decibels under full scale,
// that a band's loudness is taken never to fall under, so silence turns
// nothing. lowestHz is the lowest pitch the low band is taken to hold;
// cycles is how many of the lowest cycles of its band the close
// follower takes at the least, so it follows the band's loudness and
// not each of its waves, which would bend it; fastShare is how much of
// the length the close follower takes, and slowOverFast how many times
// as long as the close one the loose one takes at the least.
const (
	glideMS      = 20
	xoverEvery   = 32
	gainLimit    = 18
	floor        = 1e-10
	lowestHz     = 40
	cycles       = 1.5
	fastShare    = 1.0 / 8
	slowOverFast = 4
)

// follower follows a band's power: two smoothers, one after the other,
// each a first-order low pass, so that what is left of the band's waves
// in it, which would bend the sound, falls twice as steeply.
type follower struct{ a, b float64 }

func (f *follower) run(x, k float64) float64 {
	f.a += (x - f.a) * k
	f.b += (f.a - f.b) * k
	return f.b
}

// under keeps the follower, each of its smoothers, no higher than where
// the close follower fast has come, and returns where it is. A loose
// follower that fell as slowly as it rose would stand far over the
// close one as a hit died away into silence, and the band would be
// turned the full gainLimit, the wrong way, as the next hit started.
// Each smoother is kept under where fast has come, its second smoother,
// and not under fast's first: what is left of the band's waves in that
// one would hold the loose follower down by its troughs, and turn even
// a steady tone.
func (f *follower) under(fast *follower) float64 {
	f.a, f.b = min(f.a, fast.b), min(f.b, fast.b)
	return f.b
}

// processor splits each channel into four bands, follows each band's
// power, both channels' together, closely and loosely, the loose
// follower never over the close one, and turns the band up or down by
// how far the one stands over the other, times its punch; and adds the
// bands back, each turned as far as the mix.
type processor struct {
	rate float64
	// The crossovers as set, and as they glide, in hertz.
	xTo, x [bands - 1]float64
	// Each band's punch, as set and as it glides, from -1 to 1.
	pTo, p [bands]float64
	// The length, in milliseconds; the mix, from 0 to 1; and the
	// output, as a gain; as set and as they glide.
	lenTo, length float64
	mixTo, mix    float64
	gainTo, gain  float64
	glide         float64
	fresh         bool
	split         [2]xover.Splitter
	// Each band's close and loose follower, and how far each moves a
	// frame, worked out for the length speedsFor.
	fast, slow   [bands]follower
	fastK, slowK [bands]float64
	speedsFor    float64
	traces       [bands]plug.Trace
	tick         int
}

// Prepare implements [plug.Processor].
func (p *processor) Prepare(s plug.Setup) {
	p.rate = s.Rate
	p.glide = 1 - math.Exp(-1/(glideMS/1000*s.Rate))
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
	if !ok || prm.Flags&plug.ReadOnly != 0 {
		return
	}
	v := prm.Clamp(c.Value)
	switch {
	case c.ID == MixID:
		p.mixTo = v / 100
	case c.ID == OutputID:
		p.gainTo = math.Pow(10, v/20)
	case c.ID == LengthID:
		p.lenTo = v
	case c.ID >= PunchID:
		p.pTo[c.ID-PunchID] = v / 100
	default:
		p.xTo[c.ID-CrossoverID] = v
	}
	if p.fresh {
		p.p, p.length, p.mix, p.gain = p.pTo, p.lenTo, p.mixTo, p.gainTo
		p.setCrossovers(p.xTo)
	}
}

// setCrossovers sets the crossovers at x, kept in order, and works their
// filters out, and the followers' speeds with them.
func (p *processor) setCrossovers(x [bands - 1]float64) {
	p.x = xover.Order(x)
	for c := range p.split {
		p.split[c].Set(p.x, p.rate)
	}
	p.setSpeeds()
}

// setSpeeds works out how far each band's followers move a frame: the
// close one over an eighth of the length, or a cycle and a half of the
// lowest pitch in its band, whichever is longer; the loose one over the
// length, or four times the close one's time, whichever is longer.
func (p *processor) setSpeeds() {
	if p.rate == 0 {
		return
	}
	p.speedsFor = p.length
	for k := range bands {
		low := float64(lowestHz)
		if k > 0 {
			low = p.x[k-1]
		}
		fast := max(p.length/1000*fastShare, cycles/low)
		slow := max(p.length/1000, slowOverFast*fast)
		p.fastK[k] = 1 - math.Exp(-1/(fast*p.rate))
		p.slowK[k] = 1 - math.Exp(-1/(slow*p.rate))
	}
}

// Process implements [plug.Processor].
func (p *processor) Process(b *plug.Block) {
	if len(b.In) == 0 || len(b.Out) == 0 || p.rate == 0 {
		return
	}
	chans := min(len(b.In), len(b.Out), 2)
	next := 0
	for i := range b.Frames {
		for next < len(b.Changes) && b.Changes[next].Frame <= i {
			p.take(b.Changes[next])
			next++
		}
		p.step()
		var parts [2][bands]float64
		var power [bands]float64
		for c := range chans {
			parts[c] = p.split[c].Split(float64(b.In[c][i]))
			for k, v := range parts[c] {
				power[k] += v * v
			}
		}
		// Each band's turn, from both channels' power, so the two are
		// turned alike and the sound stays where it stands between them.
		var turn [bands]float64
		f := b.Position.Frame + int64(i)
		for k := range bands {
			pw := power[k] / float64(chans)
			fast := p.fast[k].run(pw, p.fastK[k])
			p.slow[k].run(pw, p.slowK[k])
			slow := p.slow[k].under(&p.fast[k])
			g, dB := 1.0, 0.0
			if math.Abs(p.p[k]) > 1e-6 {
				dB = p.p[k] * 10 * math.Log10((fast+floor)/(slow+floor))
				dB = max(-gainLimit, min(dB, gainLimit))
				g = math.Exp(dB / 20 * math.Ln10)
			}
			turn[k] = 1 - p.mix + p.mix*g
			// The turn in decibels is dB at the full mix, and else
			// worked out anew.
			if p.mix != 1 && turn[k] != 1 {
				dB = 20 * math.Log10(turn[k])
			}
			p.traces[k].Put(f, -float32(math.Abs(dB)))
		}
		for c := range chans {
			y := 0.0
			for k, v := range parts[c] {
				y += turn[k] * v
			}
			b.Out[c][i] = float32(y * p.gain)
		}
	}
	for ; next < len(b.Changes); next++ {
		p.take(b.Changes[next])
	}
	p.fresh = false
}

// step glides the punches, the length, the mix, the output and the
// crossovers a frame's way to where they are set, the crossovers'
// filters and the followers' speeds worked out anew every xoverEvery
// frames as they move.
func (p *processor) step() {
	for k := range bands {
		p.p[k] += (p.pTo[k] - p.p[k]) * p.glide
	}
	p.length += (p.lenTo - p.length) * p.glide
	if math.Abs(p.length-p.lenTo) < 0.01 {
		p.length = p.lenTo
	}
	p.mix += (p.mixTo - p.mix) * p.glide
	p.gain += (p.gainTo - p.gain) * p.glide
	p.tick++
	if p.tick < xoverEvery {
		return
	}
	p.tick = 0
	moved := false
	x := p.x
	for i := range x {
		if math.Abs(x[i]-p.xTo[i]) > 0.01 {
			// In octaves, so a low crossover glides as a high one does.
			x[i] *= math.Pow(p.xTo[i]/x[i], 1-math.Pow(1-p.glide, xoverEvery))
			moved = true
		} else if x[i] != p.xTo[i] {
			x[i], moved = p.xTo[i], true
		}
	}
	if moved {
		p.setCrossovers(x)
	} else if p.length != p.speedsFor {
		p.setSpeeds()
	}
}

// Latency implements [plug.Processor]: the plugin looks at nothing
// ahead, so it is never late.
func (p *processor) Latency() int { return 0 }

// Tail implements [plug.Processor]: nothing rings on.
func (p *processor) Tail() int { return 0 }

// Reset implements [plug.Processor]: what it held, let go.
func (p *processor) Reset() {
	for c := range p.split {
		p.split[c].Reset()
	}
	p.fast, p.slow = [bands]follower{}, [bands]follower{}
	p.fresh = true
	p.p, p.length, p.mix, p.gain = p.pTo, p.lenTo, p.mixTo, p.gainTo
	p.setCrossovers(p.xTo)
}

var (
	_ plug.Tracer   = (*processor)(nil)
	_ plug.Follower = (*panel)(nil)
	_ plug.Follower = (*chip)(nil)
)

// Trace implements [plug.Tracer]: how far each band is turned, up or
// down, by frame, as the negative of its decibels, as a trace keeps the
// least and the views want the most.
func (p *processor) Trace(id uint32) *plug.Trace {
	if id >= ChangeID && id < ChangeID+bands {
		return &p.traces[id-ChangeID]
	}
	return nil
}
