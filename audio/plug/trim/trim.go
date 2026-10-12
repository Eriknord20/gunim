// Package trim is a plugin that turns the sound up or down: the first
// plugin in the [plug] format, as small as one can be while it has a
// parameter, a gesture to undo, and a view in a chip and a panel.
package trim

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugui"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// GainID is the ID of the gain parameter, in decibels.
const GainID uint32 = 1

// Descriptor is the plugin, as it is registered.
var Descriptor = plug.Descriptor{
	ID:      "marrasen.trim",
	Name:    "Trim",
	Vendor:  "Marras",
	Version: "0.1.0",
	Kind:    "Fx|Tools",
	Params: []plug.Param{{
		ID: GainID, Name: "Gain", Unit: "dB",
		Min: -24, Max: 24, Default: 0,
		Flags:  plug.Automatable,
		Format: func(v float64) string { return fmt.Sprintf("%+.1f dB", v) },
	}},
	New: func(h plug.Host) plug.Plugin { return &plugin{host: h} },
}

func init() { plug.Register(Descriptor) }

// glide is how long the gain takes to get most of the way to a new
// setting, so a move makes no click.
const glide = 0.005

// plugin is one trim.
type plugin struct {
	host plug.Host
	proc processor
}

// Processor implements [plug.Plugin].
func (p *plugin) Processor() plug.Processor { return &p.proc }

// State implements [plug.Plugin]: a trim keeps nothing but its gain.
func (p *plugin) State() []byte { return nil }

// SetState implements [plug.Plugin]: it takes only an empty state.
func (p *plugin) SetState(b []byte) error {
	if len(b) > 0 {
		return errors.New("trim: a trim keeps no state")
	}
	return nil
}

// Close implements [plug.Plugin].
func (p *plugin) Close() {}

// View implements [plug.Viewer]: the gain in a chip, and a slider in a
// panel. In full, the host's own view does as well.
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

// gainText writes v, in decibels, to show.
func gainText(v float64) string { return Descriptor.Params[0].Text(v) }

// processor turns the sound up or down, gliding to each new gain.
type processor struct {
	// gain is the gain now, as a factor, and target the one it glides
	// to; step is how much of the way it goes a frame.
	gain, target, step float64
	// fresh says no gain has come since Prepare: the first jumps.
	fresh bool
}

// Prepare implements [plug.Processor].
func (p *processor) Prepare(s plug.Setup) {
	p.step = 1 - math.Exp(-1/(s.Rate*glide))
	p.gain, p.target, p.fresh = 1, 1, true
}

// Process implements [plug.Processor]: each change takes effect from
// its frame on.
func (p *processor) Process(b *plug.Block) {
	next := 0
	for i := range b.Frames {
		for next < len(b.Changes) && b.Changes[next].Frame <= i {
			p.take(b.Changes[next])
			next++
		}
		p.gain += (p.target - p.gain) * p.step
		if math.Abs(p.target-p.gain) < 1e-7 {
			p.gain = p.target
		}
		g := float32(p.gain)
		for ch := range b.Out {
			b.Out[ch][i] = b.In[ch][i] * g
		}
	}
	for ; next < len(b.Changes); next++ {
		p.take(b.Changes[next])
	}
}

// take takes a change to the gain.
func (p *processor) take(c plug.Change) {
	if c.ID != GainID {
		return
	}
	p.target = math.Pow(10, Descriptor.Params[0].Clamp(c.Value)/20)
	if p.fresh {
		p.gain, p.fresh = p.target, false
	}
}

// Latency implements [plug.Processor]: the trim is never late.
func (p *processor) Latency() int { return 0 }

// Tail implements [plug.Processor]: the trim never rings.
func (p *processor) Tail() int { return 0 }

// Reset implements [plug.Processor]: the gain stops gliding, there.
func (p *processor) Reset() { p.gain = p.target }

// meter is a pair of levels, left and right, of a sound as it is
// heard, taken a frame of the window at a time.
type meter struct {
	l      plug.Listener
	levels *audioui.Levels
	// since is the time since the last chunk of sound, which the levels
	// take it over.
	since time.Duration
}

func newMeter(s plug.Sound) *meter {
	return &meter{l: plug.Listener{Sound: s}, levels: audioui.NewLevels()}
}

// step takes what was heard over dt, and says the levels still move.
// Between the chunks the sound comes in, the levels hold.
func (m *meter) step(dt time.Duration) bool {
	m.since += dt
	frames, rate, playing := m.l.Hear(8192, dt)
	if len(frames) > 0 {
		m.levels.Take(frames, rate, m.since)
		m.since = 0
		return true
	}
	if playing {
		return true
	}
	m.since = 0
	m.levels.Quiet(dt)
	return m.levels.Peak[0] > -89 || m.levels.Peak[1] > -89 || m.levels.RMS[0] > -89 || m.levels.RMS[1] > -89
}

// chip shows the gain, in a plugin's chip in a chain, and, beside it,
// the level that comes out, left over right.
type chip struct {
	label *plugui.ChipText
	out   *meter
	size  geom.Size
}

func newChip(h plug.Host) *chip {
	l := plugui.NewChipText(gainText(h.Param(GainID)))
	_, out := h.Sound()
	return &chip{label: l, out: newMeter(out)}
}

// Children implements [gunim.Composite].
func (c *chip) Children() []gunim.Node { return []gunim.Node{c.label} }

// chipBars is how wide the chip's bars are.
const chipBars = 40

// Layout implements [gunim.Node]: the gain, and the bars at the right.
func (c *chip) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	c.size = cs.Max
	kids.At(0).Layout(gunim.Constraints{Max: geom.Sz(max(c.size.W-chipBars-6, 0), c.size.H)})
	kids.At(0).Place(geom.Point{})
	return c.size
}

// Paint implements [gunim.Node]: each channel's RMS over its peak,
// fainter, coral where it reaches full scale.
func (c *chip) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
	ink, ground, over := widget.Ink.Get(f.Theme), audioui.Ground.Get(f.Theme), audioui.Over.Get(f.Theme)
	x, h := box.W-chipBars, float32(3)
	top := (box.H - 2*h - 2) / 2
	lv := c.out.levels
	for ch := range 2 {
		y := top + float32(ch)*(h+2)
		p.RRect(geom.Rc(x, y, chipBars, h), h/2, paint.Solid(audioui.Faded(ground, 0.85)))
		if pk := audioui.MeterAt(lv.Peak[ch]); lv.Peak[ch] > -59 {
			p.RRect(geom.Rc(x, y, chipBars*pk, h), h/2, paint.Solid(audioui.Faded(ink, 0.3)))
		}
		if lv.RMS[ch] > -59 {
			c := ink
			if lv.Peak[ch] > -0.1 {
				c = over
			}
			p.RRect(geom.Rc(x, y, chipBars*audioui.MeterAt(lv.RMS[ch]), h), h/2, paint.Solid(audioui.Faded(c, 0.85)))
		}
	}
}

// Step implements [gunim.Animator].
func (c *chip) Step(dt time.Duration) bool { return c.out.step(dt) }

// Access implements [gunim.Accessible]: the chip reads as the gain.
func (c *chip) Access() access.Info { return c.label.Access() }

// Follow implements [plug.Follower].
func (c *chip) Follow(id uint32, v float64, u *gunim.UI) {
	if id == GainID {
		c.label.Text = gainText(v)
		u.Invalidate()
	}
}

// panel is the gain's slider, and meters of the sound fed in and the
// sound that comes out.
type panel struct {
	row     *widget.SliderRow
	in, out *meter
	// held says a gesture on the slider has begun with the host.
	held bool
	size geom.Size
}

func newPanel(h plug.Host) *panel {
	g := Descriptor.Params[0]
	s := widget.NewSlider(float32(g.Min), float32(g.Max))
	s.Snap, s.Rest, s.HasRest = 0.1, float32(g.Default), true
	s.SetValue(float32(h.Param(GainID)), nil)
	in, out := h.Sound()
	p := &panel{row: widget.NewSliderRow(g.Name, s), in: newMeter(in), out: newMeter(out)}
	p.row.Format = func(v float32) string { return gainText(float64(v)) }
	s.OnChange = func(v float32, u *gunim.UI) gunim.Intent {
		if !p.held {
			h.Begin(GainID, u)
			p.held = true
		}
		h.Set(GainID, float64(v), u)
		return nil
	}
	s.OnCommit = func(v float32, u *gunim.UI) gunim.Intent {
		if !p.held {
			h.Begin(GainID, u)
		}
		h.Set(GainID, float64(v), u)
		h.End(GainID, u)
		p.held = false
		return nil
	}
	return p
}

// The panel's meters: their place, and the room their labels take.
const (
	meterTop  = 52
	meterGap  = 24
	metersW   = 2*audioui.MeterW + meterGap
	panelSide = 32
)

// Children implements [gunim.Composite].
func (p *panel) Children() []gunim.Node { return []gunim.Node{p.row} }

// Layout implements [gunim.Node]: the meters at the left, the slider
// beside them.
func (p *panel) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	p.size = c.Max
	x := float32(metersW + panelSide)
	sz := kids.At(0).Layout(gunim.Constraints{Max: geom.Sz(min(max(p.size.W-x, 0), 460), p.size.H)})
	kids.At(0).Place(geom.Pt(x, meterTop-36+max(0, (36-sz.H)/2)))
	return p.size
}

// Paint implements [gunim.Node]: the meters, In and Out, under their
// names.
func (p *panel) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	bottom := max(box.H-12, meterTop+40)
	for i, m := range []*meter{p.in, p.out} {
		x := float32(i) * (audioui.MeterW + meterGap)
		name := audioui.Shaped([]string{"IN", "OUT"}[i], 9, true, false)
		name.Paint(pt, geom.Pt(x+(audioui.MeterW-name.Advance)/2, bottom+2), audioui.Faded(widget.Ink.Get(f.Theme), 0.6))
		audioui.PaintMeter(pt, f.Theme, geom.Rect{Min: geom.Pt(x, meterTop), Max: geom.Pt(x+audioui.MeterW, bottom-12)}, m.levels)
	}
	kids.At(0).Paint(pt)
}

// Step implements [gunim.Animator].
func (p *panel) Step(dt time.Duration) bool {
	in, out := p.in.step(dt), p.out.step(dt)
	return in || out
}

// Follow implements [plug.Follower]: the slider glides to a gain set
// elsewhere, unless it is held.
func (p *panel) Follow(id uint32, v float64, u *gunim.UI) {
	if id == GainID && !p.row.Slider.Held() {
		p.row.Slider.SetValue(float32(v), u)
	}
}
