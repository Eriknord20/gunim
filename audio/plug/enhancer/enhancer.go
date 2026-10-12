// Package enhancer brings out a sound by adding harmonics to it, a band
// of pitches at a time, as a mastering engineer's exciter does: warmth
// added low, presence in the middle, air at the top. It splits the sound
// into four bands at three crossovers, drives each through a curve of
// its own style, and adds what the curve adds, the band's harmonics, to
// the sound as it was, as much as the band's amount, so a band at none
// is left exactly as it was.
package enhancer

import (
	"errors"
	"fmt"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/xover"
)

// bands is how many bands the enhancer splits the sound into.
const bands = xover.Bands

// The parameters' IDs: the crossovers, each band's amount and its style,
// the mix and the output, and how much each band adds as it plays.
const (
	CrossoverID uint32 = 1  // to 3
	AmountID    uint32 = 10 // to 13
	StyleID     uint32 = 20 // to 23
	MixID       uint32 = 30
	OutputID    uint32 = 31
	AddedID     uint32 = 40 // to 43
)

// The styles, each a curve the band is driven through.
const (
	Tape  = iota // soft and even, odd harmonics
	Tube         // leaning, even harmonics first
	Warm         // tape and tube, half each
	Crisp        // sharper, the high harmonics further up
	styles
)

var (
	styleNames = []string{"Tape", "Tube", "Warm", "Crisp"}
	styleTips  = []string{
		"Tape: rounds the peaks off softly, adding odd harmonics: glue and weight",
		"Tube: leans to one side, adding even harmonics first: warm and full, an octave over the note",
		"Warm: tape and tube, half each",
		"Crisp: a sharper bend, its harmonics reaching higher: bite and air",
	}
)

// bandNames name the bands, low to high.
var bandNames = [bands]string{"Low", "Low mid", "High mid", "High"}

func pct(v float64) string { return fmt.Sprintf("%.0f%%", v) }

func hz(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.3g kHz", v/1000)
	}
	return fmt.Sprintf("%.0f Hz", v)
}

func db(v float64) string { return fmt.Sprintf("%+.1f dB", v) }

// Descriptor is the plugin, as it is registered.
var Descriptor = func() plug.Descriptor {
	d := plug.Descriptor{
		ID:      "marrasen.enhancer",
		Name:    "Enhancer",
		Vendor:  "Marras",
		Version: "0.1.0",
		Kind:    "Fx|Distortion|Mastering",
		New:     func(h plug.Host) plug.Plugin { return &plugin{} },
	}
	homes := [bands - 1][3]float64{{30, 1000, 120}, {200, 5000, 1200}, {1000, 16000, 6000}}
	for i, x := range homes {
		d.Params = append(d.Params, plug.Param{ID: CrossoverID + uint32(i), Name: fmt.Sprintf("Split %d", i+1),
			Unit: "Hz", Min: x[0], Max: x[1], Default: x[2], Log: true, Flags: plug.Automatable, Format: hz})
	}
	for b := range bands {
		d.Params = append(d.Params, plug.Param{ID: AmountID + uint32(b), Name: "Amount", Unit: "%", Max: 100,
			Flags: plug.Automatable, Format: pct})
	}
	for b := range bands {
		d.Params = append(d.Params, plug.Param{ID: StyleID + uint32(b), Name: "Style", Max: styles - 1,
			Steps: styles - 1, Flags: plug.Automatable,
			Format: func(v float64) string { return styleNames[int(math.Round(v))] }})
	}
	d.Params = append(d.Params,
		plug.Param{ID: MixID, Name: "Mix", Unit: "%", Max: 100, Default: 100, Flags: plug.Automatable, Format: pct},
		plug.Param{ID: OutputID, Name: "Output", Unit: "dB", Min: -12, Max: 12, Flags: plug.Automatable, Format: db})
	for b := range bands {
		d.Params = append(d.Params, plug.Param{ID: AddedID + uint32(b), Name: bandNames[b] + " added", Unit: "dB",
			Min: addedFloor, Max: 0, Default: addedFloor, Flags: plug.ReadOnly, Format: db})
	}
	d.Patches = []plug.Patch{
		{Name: "Default"},
		{Name: "Air", Params: map[uint32]float64{AmountID + 3: 35, StyleID + 3: Tape}},
		{Name: "Presence", Params: map[uint32]float64{AmountID + 2: 30, StyleID + 2: Tube}},
		{Name: "Warm low end", Params: map[uint32]float64{AmountID: 30, StyleID: Tube, AmountID + 1: 15, StyleID + 1: Warm}},
		{Name: "Tape glue", Params: map[uint32]float64{AmountID: 20, AmountID + 1: 20, AmountID + 2: 20, AmountID + 3: 20}},
		{Name: "Crisp top", Params: map[uint32]float64{AmountID + 2: 15, StyleID + 2: Crisp, AmountID + 3: 30,
			StyleID + 3: Crisp}},
	}
	return d
}()

func init() { plug.Register(Descriptor) }

// param returns the parameter id; every ID above is one.
func param(id uint32) plug.Param {
	p, _ := Descriptor.Param(id)
	return p
}

// plugin is one enhancer.
type plugin struct{ proc processor }

// Processor implements [plug.Plugin].
func (p *plugin) Processor() plug.Processor { return &p.proc }

// State implements [plug.Plugin]: it keeps nothing but its parameters.
func (p *plugin) State() []byte { return nil }

// SetState implements [plug.Plugin]: it takes only an empty state.
func (p *plugin) SetState(b []byte) error {
	if len(b) > 0 {
		return errors.New("enhancer: the plugin keeps no state")
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

// driveDB is how far a band at its full amount is driven into its
// curve; glideMS how long an amount, a style, the mix or the output, or
// a crossover, takes to glide where it is set; xoverEvery how many frames
// apart a gliding crossover's filters are worked out anew; dcHz where
// what a band adds is cut under, so a leaning curve adds no offset; and
// addedMS how quickly a band's added level follows it, addedFloor the
// least it shows.
const (
	driveDB    = 30
	glideMS    = 20
	xoverEvery = 32
	dcHz       = 10
	addedMS    = 30
	addedFloor = -90
)

// processor splits each channel into four bands, drives each band
// through its curve, and adds to the channel what the curves add, each
// band's as far as its amount, all as far as the mix.
type processor struct {
	rate float64
	// The crossovers as set, and as they glide, in hertz.
	xTo, x [bands - 1]float64
	// Each band's amount as set and as it glides, from 0 to 1, and its
	// style, as how much of each curve it takes, as set and as it glides.
	aTo, a   [bands]float64
	stTo, st [bands][styles]float64
	// The mix, from 0 to 1, and the output, as a gain, as set and as
	// they glide.
	mixTo, mix   float64
	gainTo, gain float64
	glide        float64
	fresh        bool
	split        [2]xover.Splitter
	// Each channel's band's last frame, and its cut of offset.
	last [2][bands]float64
	dc   [2][bands]dcCut
	dcR  float64
	// Each band's added level, following it, and its trace.
	env    [bands]float64
	envK   float64
	traces [bands]plug.Trace
	tick   int
}

// dcCut cuts the offset out of what a band adds: a first-order high
// pass at dcHz.
type dcCut struct{ x1, y1 float64 }

func (c *dcCut) run(x, r float64) float64 {
	y := x - c.x1 + r*c.y1
	c.x1, c.y1 = x, y
	return y
}

// Prepare implements [plug.Processor].
func (p *processor) Prepare(s plug.Setup) {
	p.rate = s.Rate
	p.glide = 1 - math.Exp(-1/(glideMS/1000*s.Rate))
	p.dcR = 1 - 2*math.Pi*dcHz/s.Rate
	p.envK = 1 - math.Exp(-1/(addedMS/1000*s.Rate))
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
	case c.ID >= StyleID:
		b, s := c.ID-StyleID, int(math.Round(v))
		for k := range styles {
			p.stTo[b][k] = float64(onOff(k == s))
		}
	case c.ID >= AmountID:
		p.aTo[c.ID-AmountID] = v / 100
	default:
		p.xTo[c.ID-CrossoverID] = v
	}
	if p.fresh {
		p.a, p.st, p.mix, p.gain = p.aTo, p.stTo, p.mixTo, p.gainTo
		p.setCrossovers(p.xTo)
	}
}

func onOff(b bool) int {
	if b {
		return 1
	}
	return 0
}

// setCrossovers sets the crossovers at x, kept in order, and works their
// filters out.
func (p *processor) setCrossovers(x [bands - 1]float64) {
	p.x = xover.Order(x)
	for c := range p.split {
		p.split[c].Set(p.x, p.rate)
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
		var drive [bands]float64
		for k := range bands {
			drive[k] = math.Exp(p.a[k] * driveDB / 20 * math.Ln10)
		}
		var peak [bands]float64
		for c := range chans {
			x := float64(b.In[c][i])
			parts := p.split[c].Split(x)
			added := 0.0
			for k, v := range parts {
				if p.a[k] < 1e-6 {
					p.last[c][k] = v
					continue
				}
				h := p.dc[c][k].run(p.harmonics(k, p.last[c][k], v, drive[k]), p.dcR) * p.a[k]
				p.last[c][k] = v
				added += h
				peak[k] = max(peak[k], math.Abs(h))
			}
			b.Out[c][i] = float32((x + added*p.mix) * p.gain)
		}
		// Each band's added level, a quick envelope of its peaks.
		f := b.Position.Frame + int64(i)
		for k := range bands {
			p.env[k] += (peak[k]*p.mix - p.env[k]) * p.envK
			p.traces[k].Put(f, -float32(max(20*math.Log10(p.env[k]+1e-12), addedFloor)))
		}
	}
	for ; next < len(b.Changes); next++ {
		p.take(b.Changes[next])
	}
	p.fresh = false
}

// harmonics returns what band k's curve adds to it, from its last frame
// was to its frame now, driven by drive: the curve's mean between them
// less the line's, so it neither aliases as a curve read a frame at a
// time would, nor lags the band, both lagging alike.
func (p *processor) harmonics(k int, was, now, drive float64) float64 {
	u0, u1 := was*drive, now*drive
	h := 0.0
	for s, w := range p.st[k] {
		if w < 1e-4 {
			continue
		}
		h += w * (mean(s, u0, u1) - (u0+u1)/2)
	}
	return h / drive
}

// step glides the amounts, the styles, the mix, the output and the
// crossovers a frame's way to where they are set, the crossovers' filters
// worked out anew every xoverEvery frames as they move.
func (p *processor) step() {
	for k := range bands {
		p.a[k] += (p.aTo[k] - p.a[k]) * p.glide
		for s := range styles {
			p.st[k][s] += (p.stTo[k][s] - p.st[k][s]) * p.glide
		}
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
	}
}

// Latency implements [plug.Processor]: the enhancer is never late.
func (p *processor) Latency() int { return 0 }

// Tail implements [plug.Processor]: nothing rings on.
func (p *processor) Tail() int { return 0 }

// Reset implements [plug.Processor]: what it held, let go.
func (p *processor) Reset() {
	for c := range p.split {
		p.split[c].Reset()
		p.last[c] = [bands]float64{}
		p.dc[c] = [bands]dcCut{}
	}
	p.env = [bands]float64{}
	p.fresh = true
	p.a, p.st, p.mix, p.gain = p.aTo, p.stTo, p.mixTo, p.gainTo
	p.setCrossovers(p.xTo)
}

var (
	_ plug.Tracer   = (*processor)(nil)
	_ plug.Follower = (*panel)(nil)
	_ plug.Follower = (*chip)(nil)
)

// Trace implements [plug.Tracer]: each band's added level, by frame, as
// the negative of its decibels, as a trace keeps the least and the views
// want the most.
func (p *processor) Trace(id uint32) *plug.Trace {
	if id >= AddedID && id < AddedID+bands {
		return &p.traces[id-AddedID]
	}
	return nil
}
