// Package braid is a plugin that sets a sound's gain and its stereo: it
// turns the sound up or down, inverts either side, pans each channel
// on its own, and folds the two into mono. Its panel draws the two
// channels as ribbons that twist, bend, braid and squeeze through the
// controls, so what each setting does to the sound shows as it is set.
//
// The sound goes through the settings in this order: invert, then each
// channel's pan, then mono, then the gain. Left only and Right only
// take one channel as it comes in and play it on both sides, and drop
// the other: for a mono source recorded on one side of a stereo track,
// with noise on the other.
package braid

import (
	"errors"
	"fmt"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
)

// The parameters' IDs.
const (
	GainID    uint32 = 1
	InvertLID uint32 = 2
	InvertRID uint32 = 3
	MonoID    uint32 = 4
	PanLID    uint32 = 5
	PanRID    uint32 = 6
)

// The Mono parameter's values.
const (
	MonoOff = iota
	MonoSum
	MonoLeft
	MonoRight
)

// monoNames name the Mono parameter's values.
var monoNames = [...]string{"Off", "Sum", "Left only", "Right only"}

// GainOff is the lowest gain, in decibels, which is no sound at all:
// minus infinity.
const GainOff = -80

// Descriptor is the plugin, as it is registered.
var Descriptor = plug.Descriptor{
	ID:       "marrasen.braid",
	Name:     "Braid",
	Vendor:   "Marras",
	Version:  "0.2.0",
	Kind:     "Fx|Tools",
	Tagline:  "Gain & stereo",
	Keywords: []string{"gain", "trim", "volume", "stereo", "mono", "pan", "invert", "phase", "polarity", "utility", "channel"},
	Params: []plug.Param{
		{ID: GainID, Name: "Gain", Unit: "dB", Min: GainOff, Max: 48, Default: 0, Flags: plug.Automatable, Format: gainText},
		{ID: InvertLID, Name: "Invert L", Max: 1, Steps: 1, Flags: plug.Automatable, Format: onOff},
		{ID: InvertRID, Name: "Invert R", Max: 1, Steps: 1, Flags: plug.Automatable, Format: onOff},
		{ID: MonoID, Name: "Mono", Max: MonoRight, Steps: MonoRight, Flags: plug.Automatable, Format: monoText},
		{ID: PanLID, Name: "Pan L", Min: -100, Max: 100, Default: -100, Flags: plug.Automatable, Format: panText},
		{ID: PanRID, Name: "Pan R", Min: -100, Max: 100, Default: 100, Flags: plug.Automatable, Format: panText},
	},
	New: func(h plug.Host) plug.Plugin { return &plugin{host: h} },
}

func init() { plug.Register(Descriptor) }

// param returns the parameter id describes.
func param(id uint32) plug.Param {
	for _, p := range Descriptor.Params {
		if p.ID == id {
			return p
		}
	}
	panic(fmt.Sprintf("braid: no parameter %d", id))
}

// gainText writes a gain, in decibels, to show; the lowest is no sound.
func gainText(v float64) string {
	if v <= GainOff {
		return "-∞ dB"
	}
	return fmt.Sprintf("%+.1f dB", v)
}

func onOff(v float64) string {
	if v > 0.5 {
		return "On"
	}
	return "Off"
}

func monoText(v float64) string { return monoNames[monoOf(v)] }

// monoOf returns the Mono value v is nearest; Off for a NaN.
func monoOf(v float64) int {
	if math.IsNaN(v) {
		return MonoOff
	}
	return int(min(max(math.Round(v), MonoOff), MonoRight))
}

// panText writes a pan, from -100, left, to 100, right, as "L40", "C"
// or "R100".
func panText(v float64) string {
	switch r := math.Round(v); {
	case r < 0:
		return fmt.Sprintf("L%.0f", -r)
	case r > 0:
		return fmt.Sprintf("R%.0f", r)
	default:
		return "C"
	}
}

// PanGains returns how much of a channel panned to v, from -100 to 100,
// goes left and right: a constant power law, sine and cosine, which
// takes each side 3 dB down in the middle and keeps the channel as loud
// wherever it is. At either end the far side is exactly zero.
func PanGains(v float64) (left, right float64) {
	switch {
	case v <= -100:
		return 1, 0
	case v >= 100:
		return 0, 1
	}
	a := (v + 100) / 200 * math.Pi / 2
	return math.Cos(a), math.Sin(a)
}

// Settings are the plugin's parameters' values, as the sound and the
// panel take them.
type Settings struct {
	// Gain is in decibels; GainOff or lower is no sound.
	Gain             float64
	InvertL, InvertR bool
	Mono             int
	PanL, PanR       float64
}

// DefaultSettings are the settings a new plugin starts at: the sound
// goes through as it came.
func DefaultSettings() Settings { return Settings{PanL: -100, PanR: 100} }

// with returns s with parameter id at v.
func (s Settings) with(id uint32, v float64) Settings {
	switch id {
	case GainID:
		s.Gain = param(GainID).Clamp(v)
	case InvertLID:
		s.InvertL = v > 0.5
	case InvertRID:
		s.InvertR = v > 0.5
	case MonoID:
		s.Mono = monoOf(v)
	case PanLID:
		s.PanL = param(PanLID).Clamp(v)
	case PanRID:
		s.PanR = param(PanRID).Clamp(v)
	}
	return s
}

// SettingsOf returns the settings h holds.
func SettingsOf(h plug.Host) Settings {
	var s Settings
	for _, p := range Descriptor.Params {
		s = s.with(p.ID, h.Param(p.ID))
	}
	return s
}

// GainFactor returns the gain as a factor: 0 at GainOff and below.
func (s Settings) GainFactor() float64 {
	if s.Gain <= GainOff {
		return 0
	}
	return math.Pow(10, s.Gain/20)
}

// Matrix is the way the sound goes through, as factors: how much of
// each channel coming in goes to each channel out. LL is left in to
// left out, RL right in to left out, and so on.
type Matrix struct{ LL, RL, LR, RR float64 }

// Matrix returns the way stereo sound goes through at s, the gain
// with it.
func (s Settings) Matrix() Matrix {
	sl, sr := sign(s.InvertL), sign(s.InvertR)
	var m Matrix
	switch s.Mono {
	case MonoLeft:
		m = Matrix{LL: sl, LR: sl}
	case MonoRight:
		m = Matrix{RL: sr, RR: sr}
	default:
		ll, lr := PanGains(s.PanL)
		rl, rr := PanGains(s.PanR)
		m = Matrix{LL: sl * ll, LR: sl * lr, RL: sr * rl, RR: sr * rr}
		if s.Mono == MonoSum {
			l, r := (m.LL+m.LR)/2, (m.RL+m.RR)/2
			m = Matrix{LL: l, LR: l, RL: r, RR: r}
		}
	}
	g := s.GainFactor()
	return Matrix{LL: m.LL * g, RL: m.RL * g, LR: m.LR * g, RR: m.RR * g}
}

func sign(invert bool) float64 {
	if invert {
		return -1
	}
	return 1
}

// glide is how long a change takes to get most of the way to a new
// setting, so a move makes no click: the old and new ways through are
// crossfaded over it.
const glide = 0.005

// plugin is one braid.
type plugin struct {
	host plug.Host
	proc processor
}

// Processor implements [plug.Plugin].
func (p *plugin) Processor() plug.Processor { return &p.proc }

// State implements [plug.Plugin]: a braid keeps nothing but its
// parameters.
func (p *plugin) State() []byte { return nil }

// SetState implements [plug.Plugin]: it takes only an empty state.
func (p *plugin) SetState(b []byte) error {
	if len(b) > 0 {
		return errors.New("braid: a braid keeps no state")
	}
	return nil
}

// Close implements [plug.Plugin].
func (p *plugin) Close() {}

// View implements [plug.Viewer]: the gain in a chip, and the ribbons
// in a panel. In full, the host's own view does as well.
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

// ways is how many factors glide: the stereo matrix's four, the factor
// of the one channel of a mono sound, and the gain alone, for channels
// past the second. A mono sound is the left channel alone: it takes the
// gain and Invert L, and the pans, Mono and Invert R leave it be.
const ways = 6

// processor sends the sound through, gliding to each new setting.
type processor struct {
	set Settings
	// now are the factors now, target the ones they glide to, and step
	// how much of the way they go a frame. moving says they are not
	// there yet.
	now, target [ways]float64
	step        float64
	moving      bool
	// fresh says no frame has played since Prepare: the settings the
	// first block brings jump.
	fresh bool
}

// Prepare implements [plug.Processor].
func (p *processor) Prepare(s plug.Setup) {
	p.step = 1 - math.Exp(-1/(s.Rate*glide))
	p.set = DefaultSettings()
	p.aim()
	p.now, p.moving, p.fresh = p.target, false, true
}

// aim sets the factors to glide to from the settings.
func (p *processor) aim() {
	m := p.set.Matrix()
	g := p.set.GainFactor()
	p.target = [ways]float64{m.LL, m.RL, m.LR, m.RR, sign(p.set.InvertL) * g, g}
	p.moving = p.now != p.target
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
		if p.fresh {
			p.now, p.moving, p.fresh = p.target, false, false
		}
		if p.moving {
			p.glide()
		}
		p.frame(b, i)
	}
	for ; next < len(b.Changes); next++ {
		p.take(b.Changes[next])
	}
}

// glide moves the factors a frame of the way to their targets, and
// there once they are close.
func (p *processor) glide() {
	still := true
	for k := range p.now {
		p.now[k] += (p.target[k] - p.now[k]) * p.step
		if math.Abs(p.target[k]-p.now[k]) < 1e-7 {
			p.now[k] = p.target[k]
		} else {
			still = false
		}
	}
	p.moving = !still
}

// frame sends frame i of b through.
func (p *processor) frame(b *plug.Block, i int) {
	switch len(b.Out) {
	case 0:
	case 1:
		b.Out[0][i] = b.In[0][i] * float32(p.now[4])
	default:
		l, r := b.In[0][i], b.In[1][i]
		if p.now[0] == 1 && p.now[1] == 0 && p.now[2] == 0 && p.now[3] == 1 {
			// As it came, exactly: a -0 stays -0, and a NaN or an
			// infinity on one side stays on that side.
			b.Out[0][i], b.Out[1][i] = l, r
		} else {
			b.Out[0][i] = l*float32(p.now[0]) + r*float32(p.now[1])
			b.Out[1][i] = l*float32(p.now[2]) + r*float32(p.now[3])
		}
		g := float32(p.now[5])
		for ch := 2; ch < len(b.Out); ch++ {
			b.Out[ch][i] = b.In[ch][i] * g
		}
	}
}

// take takes a change of a setting.
func (p *processor) take(c plug.Change) {
	p.set = p.set.with(c.ID, c.Value)
	p.aim()
}

// Latency implements [plug.Processor]: a braid is never late.
func (p *processor) Latency() int { return 0 }

// Tail implements [plug.Processor]: a braid never rings.
func (p *processor) Tail() int { return 0 }

// Reset implements [plug.Processor]: the factors stop gliding, there.
func (p *processor) Reset() { p.now, p.moving = p.target, false }
