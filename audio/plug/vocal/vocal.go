// Package vocal is a vocal chain in one plugin: the chain a producer
// builds of a de-esser, two EQs and two compressors in series, packed
// into one, with a row of macro knobs over it, as a rack of devices
// under a combinator's knobs. The voice goes through, in order:
//
//   - a de-esser, which turns down the hiss of an S or a T before
//     anything after it hears it, so the compressors do not pump on it;
//   - Input, the gain that brings the voice to the level the rest is
//     set for;
//   - a clean EQ, which takes out the rumble and mud under the voice
//     with a low shelf, and its boxy or nasal ring with a narrow cut;
//   - a tone EQ, a high-pass under the voice, a broad lift where it
//     cuts through a mix, and a high shelf of air;
//   - a peak compressor, fast, which catches the transients, and a glue
//     compressor, slower and smoother, as an optical one, which levels
//     the performance: each takes a couple of decibels, at 2:1;
//   - a parallel compressor, squeezed hard and mixed under the voice for
//     body; tape saturation; and a distorted octave below, for a thick
//     voice;
//   - width, which spreads the voice's highs to the sides while the
//     centre stays as it was: the left and the right summed to mono are
//     the voice as it came;
//   - and Output.
//
// The space round a voice, its delays and reverbs, stays out: it
// belongs on a send, where every voice shares it.
//
// Learn reads the voice, as it reaches the plugin, and sets the chain
// for it: Input to bring it to the target loudness; the clean EQ's cut
// to its own boxy ring, the low shelf and the lifts to its balance, and
// the de-esser to its own hiss; the compressors' thresholds so each
// takes its couple of decibels; and Output to bring it back to the
// target. Where the host loops, as a [plug.Looper] tells, it reads the
// loop alone, so the chorus can be learned apart from the verse.
package vocal

import (
	"errors"
	"fmt"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
)

// The parameters' IDs: the macros first, then each part of the chain.
const (
	// The macros: each works a part of the chain, as a combinator's
	// knob: De-Ess; Rumble, the rumble and the mud; Presence, the
	// presence and the air; Comp, the two compressors' ratios; Parallel,
	// the parallel compressor; Tape, tape and distortion; Octave, the
	// octave down; and Width. Those that scale a part run from 0 to 200 percent, 100 as
	// the part is set; those that blend one in run from 0 to 100.
	DeEssID    uint32 = 1
	RumbleID   uint32 = 2
	PresenceID uint32 = 3
	SquashID   uint32 = 4
	ParallelID uint32 = 5
	TapeID     uint32 = 6
	OctaveID   uint32 = 7
	WidthID    uint32 = 8

	InputID  uint32 = 10
	OutputID uint32 = 11
	// TargetID is the loudness Learn brings the voice to, at Input and
	// again at Output.
	TargetID uint32 = 12

	DeEssOnID        uint32 = 20
	DeEssFreqID      uint32 = 21
	DeEssThresholdID uint32 = 22
	DeEssRangeID     uint32 = 23

	CleanOnID    uint32 = 30
	LowShelfFqID uint32 = 31
	LowShelfID   uint32 = 32
	MudFreqID    uint32 = 33
	MudGainID    uint32 = 34
	MudQID       uint32 = 35

	ToneOnID       uint32 = 40
	HighPassID     uint32 = 41
	PresenceFreqID uint32 = 42
	PresenceGainID uint32 = 43
	AirFreqID      uint32 = 44
	AirGainID      uint32 = 45

	PeakOnID        uint32 = 50
	PeakThresholdID uint32 = 51
	PeakRatioID     uint32 = 52
	PeakAttackID    uint32 = 53
	PeakReleaseID   uint32 = 54

	GlueOnID        uint32 = 60
	GlueThresholdID uint32 = 61
	GlueRatioID     uint32 = 62
	GlueAttackID    uint32 = 63
	GlueReleaseID   uint32 = 64

	WidthFreqID uint32 = 71

	// How far the de-esser and each compressor turn the voice down now,
	// in decibels, as the plugin reports it, and keeps it by frame.
	DeEssGRID uint32 = 90
	PeakGRID  uint32 = 91
	GlueGRID  uint32 = 92
)

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

func percent(v float64) string { return fmt.Sprintf("%.0f%%", v) }

func ratio(v float64) string { return fmt.Sprintf("%.1f:1", v) }

func lufs(v float64) string { return fmt.Sprintf("%.0f LUFS", v) }

func onOff(v float64) string {
	if v > 0.5 {
		return "On"
	}
	return "Off"
}

// The parameters' flags: each but Target is automatable.
const auto = plug.Automatable

// Descriptor is the plugin, as it is registered.
var Descriptor = plug.Descriptor{
	ID:       "marrasen.vocal",
	Name:     "Vocal Chain",
	Vendor:   "Marras",
	Version:  "0.1.0",
	Kind:     "Fx|EQ|Dynamics",
	Tagline:  "Voice in one plugin",
	Keywords: []string{"vocal", "voice", "de-esser", "sibilance", "compressor", "eq", "singer", "podcast", "dialog"},
	Params: []plug.Param{
		{ID: DeEssID, Name: "De-Ess", Unit: "%", Max: 200, Default: 100, Flags: auto, Format: percent},
		{ID: RumbleID, Name: "Rumble", Unit: "%", Max: 200, Default: 100, Flags: auto, Format: percent},
		{ID: PresenceID, Name: "Presence", Unit: "%", Max: 200, Default: 100, Flags: auto, Format: percent},
		{ID: SquashID, Name: "Comp", Unit: "%", Max: 200, Default: 100, Flags: auto, Format: percent},
		{ID: ParallelID, Name: "Parallel", Unit: "%", Max: 100, Flags: auto, Format: percent},
		{ID: TapeID, Name: "Tape", Unit: "%", Max: 100, Flags: auto, Format: percent},
		{ID: OctaveID, Name: "Octave", Unit: "%", Max: 100, Flags: auto, Format: percent},
		{ID: WidthID, Name: "Width", Unit: "%", Max: 100, Default: 25, Flags: auto, Format: percent},

		{ID: InputID, Name: "Input", Unit: "dB", Min: -24, Max: 24, Flags: auto, Format: signed},
		{ID: OutputID, Name: "Output", Unit: "dB", Min: -24, Max: 24, Flags: auto, Format: signed},
		{ID: TargetID, Name: "Target", Unit: "LUFS", Min: -30, Max: -10, Default: -18, Steps: 20, Format: lufs},

		{ID: DeEssOnID, Name: "De-esser", Max: 1, Default: 1, Steps: 1, Flags: auto, Format: onOff},
		{ID: DeEssFreqID, Name: "Frequency", Unit: "Hz", Min: 3000, Max: 12000, Default: 6500, Log: true, Flags: auto, Format: hz},
		{ID: DeEssThresholdID, Name: "Threshold", Unit: "dB", Min: -60, Max: 0, Default: -30, Flags: auto, Format: db},
		{ID: DeEssRangeID, Name: "Range", Unit: "dB", Min: 0, Max: 18, Default: 6, Flags: auto, Format: db},

		{ID: CleanOnID, Name: "Clean EQ", Max: 1, Default: 1, Steps: 1, Flags: auto, Format: onOff},
		{ID: LowShelfFqID, Name: "Shelf", Unit: "Hz", Min: 60, Max: 400, Default: 150, Log: true, Flags: auto, Format: hz},
		{ID: LowShelfID, Name: "Rumble", Unit: "dB", Min: -18, Max: 0, Default: -6, Flags: auto, Format: signed},
		{ID: MudFreqID, Name: "Mud", Unit: "Hz", Min: 150, Max: 1000, Default: 400, Log: true, Flags: auto, Format: hz},
		{ID: MudGainID, Name: "Mud cut", Unit: "dB", Min: -12, Max: 6, Default: -3, Flags: auto, Format: signed},
		{ID: MudQID, Name: "Mud Q", Min: 0.5, Max: 10, Default: 4, Log: true, Flags: auto,
			Format: func(v float64) string { return fmt.Sprintf("%.1f", v) }},

		{ID: ToneOnID, Name: "Tone EQ", Max: 1, Default: 1, Steps: 1, Flags: auto, Format: onOff},
		{ID: HighPassID, Name: "High-pass", Unit: "Hz", Min: 20, Max: 300, Default: 60, Log: true, Flags: auto, Format: hz},
		{ID: PresenceFreqID, Name: "Presence", Unit: "Hz", Min: 1500, Max: 8000, Default: 3500, Log: true, Flags: auto, Format: hz},
		{ID: PresenceGainID, Name: "Lift", Unit: "dB", Min: -6, Max: 12, Default: 2, Flags: auto, Format: signed},
		{ID: AirFreqID, Name: "Air", Unit: "Hz", Min: 4000, Max: 16000, Default: 8000, Log: true, Flags: auto, Format: hz},
		{ID: AirGainID, Name: "Air lift", Unit: "dB", Min: -6, Max: 12, Default: 3, Flags: auto, Format: signed},

		{ID: PeakOnID, Name: "Peak comp", Max: 1, Default: 1, Steps: 1, Flags: auto, Format: onOff},
		{ID: PeakThresholdID, Name: "Threshold", Unit: "dB", Min: -60, Max: 0, Default: -14, Flags: auto, Format: db},
		{ID: PeakRatioID, Name: "Ratio", Min: 1, Max: 10, Default: 2, Log: true, Flags: auto, Format: ratio},
		{ID: PeakAttackID, Name: "Attack", Unit: "ms", Min: 0.05, Max: 20, Default: 0.5, Log: true, Flags: auto, Format: ms},
		{ID: PeakReleaseID, Name: "Release", Unit: "ms", Min: 5, Max: 500, Default: 40, Log: true, Flags: auto, Format: ms},

		{ID: GlueOnID, Name: "Glue comp", Max: 1, Default: 1, Steps: 1, Flags: auto, Format: onOff},
		{ID: GlueThresholdID, Name: "Threshold", Unit: "dB", Min: -60, Max: 0, Default: -20, Flags: auto, Format: db},
		{ID: GlueRatioID, Name: "Ratio", Min: 1, Max: 10, Default: 2, Log: true, Flags: auto, Format: ratio},
		{ID: GlueAttackID, Name: "Attack", Unit: "ms", Min: 1, Max: 100, Default: 15, Log: true, Flags: auto, Format: ms},
		{ID: GlueReleaseID, Name: "Release", Unit: "ms", Min: 20, Max: 2000, Default: 250, Log: true, Flags: auto, Format: ms},

		{ID: WidthFreqID, Name: "Width from", Unit: "Hz", Min: 500, Max: 8000, Default: 2000, Log: true, Flags: auto, Format: hz},

		{ID: DeEssGRID, Name: "De-essing", Unit: "dB", Min: -24, Max: 0, Flags: plug.ReadOnly, Format: db},
		{ID: PeakGRID, Name: "Peak compression", Unit: "dB", Min: -40, Max: 0, Flags: plug.ReadOnly, Format: db},
		{ID: GlueGRID, Name: "Glue compression", Unit: "dB", Min: -40, Max: 0, Flags: plug.ReadOnly, Format: db},
	},
	Patches: []plug.Patch{
		{Name: "Default", Category: "Vocal"},
		{Name: "Clean pop", Category: "Vocal", Params: map[uint32]float64{PresenceGainID: 3, AirGainID: 4, WidthID: 20}},
		{Name: "Slap", Category: "Vocal", Tags: []string{"rap"}, Params: map[uint32]float64{
			ParallelID: 40, TapeID: 30, WidthID: 35, PresenceGainID: 3.5, SquashID: 130}},
		{Name: "Trap octave", Category: "Vocal", Tags: []string{"rap"}, Params: map[uint32]float64{
			OctaveID: 40, TapeID: 45, ParallelID: 30, WidthID: 30}},
		{Name: "Intimate", Category: "Vocal", Params: map[uint32]float64{
			AirGainID: 1.5, PresenceGainID: 1, GlueThresholdID: -24, GlueReleaseID: 400, WidthID: 10}},
		{Name: "Dark and warm", Category: "Vocal", Params: map[uint32]float64{
			AirGainID: 0, PresenceGainID: 0.5, LowShelfID: -3, TapeID: 35}},
	},
	New: func(h plug.Host) plug.Plugin { return &plugin{} },
}

func init() { plug.Register(Descriptor) }

// param returns the parameter id; every ID above is one.
func param(id uint32) plug.Param {
	p, _ := Descriptor.Param(id)
	return p
}

// plugin is one vocal chain.
type plugin struct {
	proc processor
	// learner reads the voice, for Learn, and learnt is what it last
	// learned, kept as the views come and go.
	learner learner
	learnt  learnt
}

// Processor implements [plug.Plugin].
func (p *plugin) Processor() plug.Processor { return &p.proc }

// State implements [plug.Plugin]: it keeps nothing but its parameters.
func (p *plugin) State() []byte { return nil }

// SetState implements [plug.Plugin]: it takes only an empty state.
func (p *plugin) SetState(b []byte) error {
	if len(b) > 0 {
		return errors.New("vocal: the plugin keeps no state")
	}
	return nil
}

// Close implements [plug.Plugin]: a learning under way stops.
func (p *plugin) Close() { p.stopLearning() }

// View implements [plug.Viewer]: the compression in a chip; the macros
// and Learn in a panel; and every control in full.
func (p *plugin) View(s plug.Size, h plug.Host) gunim.Node {
	switch s {
	case plug.Chip:
		return newChip(h)
	case plug.Panel:
		return newPanel(p, h, false)
	default:
		return newPanel(p, h, true)
	}
}
