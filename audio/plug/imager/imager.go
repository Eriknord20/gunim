// Package imager widens and narrows the stereo image, a band of pitches
// at a time, as a mastering engineer does: the bass kept narrow, so it
// holds the middle and plays on a mono speaker, the top widened, so it
// opens out. It splits the sound into four bands at three crossovers and
// sets how wide each is, from mono through as it was to twice as wide;
// and Stereoize adds width to a band too narrow to widen, as mono, from
// the band's middle a moment late, which leaves the sound in mono as it
// was.
package imager

import (
	"errors"
	"fmt"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/xover"
)

// bands is how many bands the imager splits the sound into.
const bands = xover.Bands

// The parameters' IDs: the crossovers, then each band's width, then its
// stereoize.
const (
	CrossoverID uint32 = 1  // to 3
	WidthID     uint32 = 10 // to 13
	StereoizeID uint32 = 20 // to 23
)

// bandNames name the bands, low to high.
var bandNames = [bands]string{"Low", "Low mid", "High mid", "High"}

func percent(v float64) string { return fmt.Sprintf("%+.0f%%", v) }

func hz(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.3g kHz", v/1000)
	}
	return fmt.Sprintf("%.0f Hz", v)
}

// Descriptor is the plugin, as it is registered.
var Descriptor = func() plug.Descriptor {
	d := plug.Descriptor{
		ID:      "marrasen.imager",
		Name:    "Imager",
		Vendor:  "Marras",
		Version: "0.1.0",
		Kind:    "Fx|Spatial|Mastering",
		New:     func(h plug.Host) plug.Plugin { return &plugin{} },
	}
	homes := [bands - 1][3]float64{{30, 1000, 120}, {200, 5000, 1200}, {1000, 16000, 6000}}
	for i, x := range homes {
		d.Params = append(d.Params, plug.Param{ID: CrossoverID + uint32(i), Name: fmt.Sprintf("Split %d", i+1),
			Unit: "Hz", Min: x[0], Max: x[1], Default: x[2], Log: true, Flags: plug.Automatable, Format: hz})
	}
	for b := range bands {
		d.Params = append(d.Params, plug.Param{ID: WidthID + uint32(b), Name: "Width", Unit: "%", Min: -100, Max: 100,
			Flags: plug.Automatable, Format: percent})
	}
	for b := range bands {
		d.Params = append(d.Params, plug.Param{ID: StereoizeID + uint32(b), Name: "Stereoize", Unit: "%", Max: 100,
			Flags: plug.Automatable, Format: func(v float64) string { return fmt.Sprintf("%.0f%%", v) }})
	}
	d.Patches = []plug.Patch{
		{Name: "Default"},
		{Name: "Tight bass, open top", Params: map[uint32]float64{WidthID: -60, WidthID + 2: 15, WidthID + 3: 30}},
		{Name: "Mono bass", Params: map[uint32]float64{WidthID: -100, CrossoverID: 150}},
		{Name: "Wide master", Params: map[uint32]float64{WidthID: -40, WidthID + 1: 10, WidthID + 2: 25, WidthID + 3: 40}},
		{Name: "Widen a mono mix", Params: map[uint32]float64{StereoizeID + 1: 30, StereoizeID + 2: 45, StereoizeID + 3: 50}},
	}
	return d
}()

func init() { plug.Register(Descriptor) }

// param returns the parameter id; every ID above is one.
func param(id uint32) plug.Param {
	p, _ := Descriptor.Param(id)
	return p
}

// plugin is one imager.
type plugin struct{ proc processor }

// Processor implements [plug.Plugin].
func (p *plugin) Processor() plug.Processor { return &p.proc }

// State implements [plug.Plugin]: it keeps nothing but its parameters.
func (p *plugin) State() []byte { return nil }

// SetState implements [plug.Plugin]: it takes only an empty state.
func (p *plugin) SetState(b []byte) error {
	if len(b) > 0 {
		return errors.New("imager: the plugin keeps no state")
	}
	return nil
}

// Close implements [plug.Plugin].
func (p *plugin) Close() {}

// View implements [plug.Viewer]: the bands' widths in a chip, and the
// panel, which serves in full too.
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

// stereoizeMS is how late each band's middle comes back to stereoize
// it, in milliseconds, a little apart band to band so they do not comb
// alike; stereoizeMost is how much of it comes back at 100%.
var stereoizeMS = [bands]float64{11, 8, 6, 4}

const stereoizeMost = 0.7

// glideMS is how long a width, or a crossover, takes to glide where it
// is set; xoverEvery how many frames apart a gliding crossover's filters
// are worked out anew.
const (
	glideMS    = 20
	xoverEvery = 32
)

// processor splits the sound's middle and its sides each into four bands
// alike, so they keep their phase with one another, and puts them back
// with each band's sides as wide as it is set, and the band's middle,
// late, added to them as far as it is stereoized.
type processor struct {
	rate float64
	// The crossovers as set, and as they glide, in hertz.
	xTo, x [bands - 1]float64
	// Each band's width as set and as it glides, as how far its sides
	// are scaled, and its stereoize, as how much of its late middle is
	// added to its sides.
	wTo, w   [bands]float64
	stTo, st [bands]float64
	glide    float64
	fresh    bool
	mid, sid xover.Splitter
	// late holds each band's middle, a ring of its own, at at.
	late [bands][]float32
	at   [bands]int
	tick int
}

// Prepare implements [plug.Processor]: everything the imager keeps is
// made here, so that processing makes nothing.
func (p *processor) Prepare(s plug.Setup) {
	p.rate = s.Rate
	p.glide = 1 - math.Exp(-1/(glideMS/1000*s.Rate))
	for b := range bands {
		p.late[b] = make([]float32, max(1, int(math.Round(stereoizeMS[b]/1000*s.Rate))))
	}
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
	switch {
	case c.ID >= StereoizeID:
		p.stTo[c.ID-StereoizeID] = v / 100 * stereoizeMost
	case c.ID >= WidthID:
		p.wTo[c.ID-WidthID] = 1 + v/100
	default:
		p.xTo[c.ID-CrossoverID] = v
	}
	if p.fresh {
		p.w, p.st = p.wTo, p.stTo
		p.setCrossovers(p.xTo)
	}
}

// setCrossovers sets the crossovers at x, kept in order, each at least
// half an octave over the one under it, and works their filters out.
func (p *processor) setCrossovers(x [bands - 1]float64) {
	p.x = xover.Order(x)
	p.mid.Set(p.x, p.rate)
	p.sid.Set(p.x, p.rate)
}

// Process implements [plug.Processor].
func (p *processor) Process(b *plug.Block) {
	if len(b.In) == 0 || len(b.Out) == 0 || len(p.late[0]) == 0 {
		return
	}
	inL, outL := b.In[0], b.Out[0]
	inR, outR := inL, outL
	stereo := len(b.In) > 1 && len(b.Out) > 1
	if stereo {
		inR, outR = b.In[1], b.Out[1]
	}
	next := 0
	for i := range b.Frames {
		for next < len(b.Changes) && b.Changes[next].Frame <= i {
			p.take(b.Changes[next])
			next++
		}
		p.step()
		l, r := float64(inL[i]), float64(inR[i])
		ms, ss := p.mid.Split((l+r)/2), p.sid.Split((l-r)/2)
		var m, s float64
		for k := range bands {
			m += ms[k]
			// The band's middle, late, added to its sides.
			ring := p.late[k]
			late := float64(ring[p.at[k]])
			ring[p.at[k]] = float32(ms[k])
			p.at[k] = (p.at[k] + 1) % len(ring)
			s += ss[k]*p.w[k] + late*p.st[k]
		}
		outL[i] = float32(m + s)
		if stereo {
			outR[i] = float32(m - s)
		}
	}
	for ; next < len(b.Changes); next++ {
		p.take(b.Changes[next])
	}
	p.fresh = false
}

// step glides the widths, the stereoizes and the crossovers a frame's
// way to where they are set, the crossovers' filters worked out anew
// every xoverEvery frames as they move.
func (p *processor) step() {
	for k := range bands {
		p.w[k] += (p.wTo[k] - p.w[k]) * p.glide
		p.st[k] += (p.stTo[k] - p.st[k]) * p.glide
	}
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

// Latency implements [plug.Processor]: the imager is never late.
func (p *processor) Latency() int { return 0 }

// Tail implements [plug.Processor]: a stereoized band comes back the
// longest stereoize late.
func (p *processor) Tail() int { return len(p.late[0]) }

// Reset implements [plug.Processor]: what it held, let go.
func (p *processor) Reset() {
	p.mid.Reset()
	p.sid.Reset()
	for k := range bands {
		clear(p.late[k])
		p.at[k] = 0
	}
	p.fresh = true
	p.w, p.st = p.wTo, p.stTo
	p.setCrossovers(p.xTo)
}
