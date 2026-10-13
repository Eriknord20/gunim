// Package eq is a parametric EQ in the [plug] format: up to eight
// bands, each a bell, a shelf, a cut or a notch, kept as the plugin's
// state, and a graph to drag them about on. A bell or a shelf can be
// dynamic, turning itself down as its band of the sound grows loud;
// Amount scales every gain; and Auto shapes the bands so the whole
// track, as it reaches the EQ, sounds as even as pink noise, or leans as
// recorded music commonly does.
package eq

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/tonal"
)

// BypassID is the ID of the bypass parameter: over 0.5 passes the
// sound by the EQ. AmountID is the amount's, in percent: how far the
// bells and shelves lift and lower, and the dynamic bands turn down,
// from none at 0 to twice as set at 200. QID scales every bell's,
// shelf's and notch's Q, in percent; ThresholdID moves every dynamic
// band's threshold, in decibels; GainID is the gain the EQ puts on
// the sound it passes, in decibels; TargetID is the balance Auto brings
// the track to, as its place in [tonal.Targets], and KneeID the knee of
// a target that has one, in hertz.
const (
	BypassID    uint32 = 1
	AmountID    uint32 = 2
	QID         uint32 = 3
	ThresholdID uint32 = 4
	GainID      uint32 = 5
	TargetID    uint32 = 6
	KneeID      uint32 = 7
)

// bandTrace is where the IDs of the bands' traces start: band n's
// reduction, as the views read it with [plug.Host.Traced], is traced
// as bandTrace+n. heardTrace is where the levels the dynamic bands
// hear start, each traced as the negative of its decibels, as a trace
// keeps the least and the views want the loudest.
const (
	bandTrace  = 1000
	heardTrace = 2000
)

// Descriptor is the plugin, as it is registered.
var Descriptor = plug.Descriptor{
	ID:      "marrasen.eq",
	Name:    "EQ",
	Vendor:  "Marras",
	Version: "0.1.0",
	Kind:    "Fx|EQ",
	Params: []plug.Param{{
		ID: BypassID, Name: "Bypass",
		Min: 0, Max: 1, Default: 0, Steps: 1,
		Flags: plug.Bypass | plug.Hidden,
	}, {
		ID: AmountID, Name: "Amount", Unit: "%",
		Min: 0, Max: 200, Default: 100,
		Flags:  plug.Automatable,
		Format: func(v float64) string { return fmt.Sprintf("%.0f%%", v) },
	}, {
		ID: QID, Name: "Q", Unit: "%",
		Min: 25, Max: 400, Default: 100, Log: true,
		Flags:  plug.Automatable,
		Format: func(v float64) string { return fmt.Sprintf("%.0f%%", v) },
	}, {
		ID: ThresholdID, Name: "Threshold", Unit: "dB",
		Min: -24, Max: 24, Default: 0,
		Flags:  plug.Automatable,
		Format: func(v float64) string { return fmt.Sprintf("%+.1f dB", v) },
	}, {
		ID: GainID, Name: "Gain", Unit: "dB",
		Min: -12, Max: 12, Default: 0,
		Flags:  plug.Automatable,
		Format: func(v float64) string { return fmt.Sprintf("%+.1f dB", v) },
	}, {
		ID: TargetID, Name: "Match to",
		Min: 0, Max: float64(len(tonal.Targets) - 1), Default: 0, Steps: len(tonal.Targets) - 1,
		Flags:  plug.Hidden,
		Format: func(v float64) string { return tonal.TargetAt(v).Short },
	}, {
		ID: KneeID, Name: "Knee", Unit: "Hz",
		Min: tonal.KneeLow, Max: tonal.KneeHigh, Default: tonal.Knee, Log: true,
		Flags:  plug.Hidden,
		Format: func(v float64) string { return fmt.Sprintf("%.0f Hz", v) },
	}},
	Patches: patches,
	New:     func(h plug.Host) plug.Plugin { return newPlugin(h) },
}

func init() { plug.Register(Descriptor) }

// The bands' bounds: frequencies from eqLow to eqHigh, gains of
// eqRange decibels each way, and Q from minQ to maxQ.
const (
	eqLow, eqHigh = 20.0, 20000.0
	eqRange       = 18
	maxBands      = 8
	minQ, maxQ    = 0.1, 18
)

// A dynamic band's bounds: its threshold, ratio, attack and release,
// and its range.
const (
	dynLowThreshold, dynHighThreshold = -60, 0
	dynMaxRatio                       = 20
	dynLowAttack, dynHighAttack       = 0.1, 200
	dynLowRelease, dynHighRelease     = 5, 2000
	dynMaxRange                       = 24
)

// dynDefaults are a band's dynamics as they first turn on.
var dynDefaults = audio.Dynamics{On: true, Threshold: -24, Ratio: 3, Attack: 10, Release: 120, Range: 6}

// version is the version of the state's format that State writes.
const version = 1

// plugin is one EQ.
type plugin struct {
	host plug.Host
	eq   *audio.EQ
	proc processor
	// mu guards bands, which State and SetState reach from the app's
	// goroutine and the views from the window's; ids are their IDs, for
	// the processor, which takes no lock.
	mu    sync.Mutex
	bands []audio.Band
	ids   atomic.Pointer[[]int]
	// reader reads the whole track, for Auto.
	reader reader
}

func newPlugin(h plug.Host) *plugin {
	p := &plugin{host: h, eq: audio.NewEQ()}
	p.proc.eq, p.proc.ids, p.proc.gainTo = p.eq, &p.ids, 1
	p.ids.Store(&[]int{})
	return p
}

// Processor implements [plug.Plugin].
func (p *plugin) Processor() plug.Processor { return &p.proc }

// State implements [plug.Plugin]: the bands, as JSON.
func (p *plugin) State() []byte {
	b, err := json.Marshal(stateOf(p.Bands()))
	if err != nil {
		// Bands of numbers and names always marshal.
		panic("eq: " + err.Error())
	}
	return b
}

// SetState implements [plug.Plugin]: it takes the bands State wrote,
// and none for an empty state. A state it cannot read changes nothing.
func (p *plugin) SetState(b []byte) error {
	var bands []audio.Band
	if len(b) > 0 {
		var err error
		if bands, err = parseState(b); err != nil {
			return err
		}
	}
	p.setBands(bands)
	return nil
}

// Close implements [plug.Plugin]: a reading under way stops.
func (p *plugin) Close() { p.stopReading() }

// Bands returns a copy of the EQ's bands.
func (p *plugin) Bands() []audio.Band {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.bands)
}

// setBands makes bands the EQ's bands, for the sound too.
func (p *plugin) setBands(bands []audio.Band) {
	p.mu.Lock()
	p.bands = slices.Clone(bands)
	p.mu.Unlock()
	ids := make([]int, len(bands))
	for i, b := range bands {
		ids[i] = b.ID
	}
	p.ids.Store(&ids)
	p.eq.Set(bands)
}

// View implements [plug.Viewer]: the curve in a chip, the graph in a
// panel, and the graph over the band picked in full.
func (p *plugin) View(s plug.Size, h plug.Host) gunim.Node {
	switch s {
	case plug.Chip:
		return newChip(p, h)
	case plug.Panel:
		return newPanel(p, h)
	case plug.Full:
		return newFull(p, h)
	default:
		return nil
	}
}

// state is the plugin's state as it is saved: its format's version and
// the bands.
type state struct {
	Version int         `json:"version"`
	Bands   []savedBand `json:"bands"`
}

// savedBand is a band as it is saved, its kind by name, so the format
// is the plugin's own and not gunim's.
type savedBand struct {
	ID    int     `json:"id"`
	Kind  string  `json:"kind"`
	Freq  float32 `json:"freq"`
	Gain  float32 `json:"gain"`
	Q     float32 `json:"q"`
	Slope int     `json:"slope,omitempty"`
	On    bool    `json:"on"`
	// Dynamic is the band's dynamics, where it has them.
	Dynamic *savedDynamics `json:"dynamic,omitempty"`
}

// savedDynamics are a band's dynamics as they are saved.
type savedDynamics struct {
	On        bool    `json:"on"`
	Threshold float32 `json:"threshold"`
	Ratio     float32 `json:"ratio"`
	Attack    float32 `json:"attack"`
	Release   float32 `json:"release"`
	Range     float32 `json:"range"`
}

// kindKeys name the kinds of band in the saved state, in their order.
var kindKeys = []string{"bell", "lowShelf", "highShelf", "lowCut", "highCut", "notch"}

// stateOf returns bands as they are saved.
func stateOf(bands []audio.Band) state {
	s := state{Version: version, Bands: make([]savedBand, len(bands))}
	for i, b := range bands {
		s.Bands[i] = savedBand{ID: b.ID, Kind: kindKeys[b.Kind], Freq: b.Freq, Gain: b.Gain, Q: b.Q, Slope: b.Slope, On: b.On}
		if d := b.Dynamic; d != (audio.Dynamics{}) {
			s.Bands[i].Dynamic = &savedDynamics{On: d.On, Threshold: d.Threshold, Ratio: d.Ratio, Attack: d.Attack,
				Release: d.Release, Range: d.Range}
		}
	}
	return s
}

// parseState returns the bands in b, or what is wrong with it.
func parseState(b []byte) ([]audio.Band, error) {
	var s state
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("eq: reading the state: %w", err)
	}
	if s.Version != version {
		return nil, fmt.Errorf("eq: the state is of version %d, and this EQ reads version %d", s.Version, version)
	}
	if len(s.Bands) > maxBands {
		return nil, fmt.Errorf("eq: the state has %d bands, and an EQ has at most %d", len(s.Bands), maxBands)
	}
	bands := make([]audio.Band, len(s.Bands))
	seen := map[int]bool{}
	for i, sb := range s.Bands {
		kind := slices.Index(kindKeys, sb.Kind)
		if kind < 0 {
			return nil, fmt.Errorf("eq: band %d is of an unknown kind, %q", i+1, sb.Kind)
		}
		b := audio.Band{ID: sb.ID, Kind: audio.FilterKind(kind), Freq: sb.Freq, Gain: sb.Gain, Q: sb.Q, Slope: sb.Slope, On: sb.On}
		if d := sb.Dynamic; d != nil {
			b.Dynamic = audio.Dynamics{On: d.On, Threshold: d.Threshold, Ratio: d.Ratio, Attack: d.Attack, Release: d.Release,
				Range: d.Range}
		}
		if err := checkBand(b); err != nil {
			return nil, fmt.Errorf("eq: band %d %w", i+1, err)
		}
		if seen[b.ID] {
			return nil, fmt.Errorf("eq: two bands have the ID %d", b.ID)
		}
		seen[b.ID] = true
		bands[i] = b
	}
	return bands, nil
}

// checkBand returns what is wrong with b, if anything, as the end of a
// sentence about it.
func checkBand(b audio.Band) error {
	inside := func(v, lo, hi float32) bool { return v >= lo && v <= hi }
	switch {
	case b.ID <= 0:
		return errors.New("has no ID")
	case !inside(b.Freq, eqLow, eqHigh):
		return fmt.Errorf("is at %g Hz, outside %g Hz to %g Hz", b.Freq, eqLow, eqHigh)
	case !inside(b.Gain, -eqRange, eqRange):
		return fmt.Errorf("has a gain of %g dB, outside ±%d dB", b.Gain, eqRange)
	case !inside(b.Q, minQ, maxQ):
		return fmt.Errorf("has a Q of %g, outside %g to %g", b.Q, minQ, float32(maxQ))
	case b.Slope != 0 && (b.Slope%12 != 0 || !inside(float32(b.Slope), 12, 48)):
		return fmt.Errorf("has a slope of %d dB an octave, not 12, 24, 36 or 48", b.Slope)
	}
	if d := b.Dynamic; d != (audio.Dynamics{}) {
		switch {
		case !inside(d.Threshold, dynLowThreshold, dynHighThreshold):
			return fmt.Errorf("turns down from %g dB, outside %d dB to %d dB", d.Threshold, dynLowThreshold, dynHighThreshold)
		case !inside(d.Ratio, 1, dynMaxRatio):
			return fmt.Errorf("turns down at %g:1, outside 1:1 to %d:1", d.Ratio, dynMaxRatio)
		case !inside(d.Attack, dynLowAttack, dynHighAttack) || !inside(d.Release, dynLowRelease, dynHighRelease):
			return fmt.Errorf("turns down over %g ms and back over %g ms, outside %g to %d ms and %d to %d ms",
				d.Attack, d.Release, dynLowAttack, dynHighAttack, dynLowRelease, dynHighRelease)
		case !inside(d.Range, 0, dynMaxRange):
			return fmt.Errorf("turns down as far as %g dB, outside 0 to %d dB", d.Range, dynMaxRange)
		}
	}
	return nil
}

// piece is how many frames the insert takes at a time: an EQ glides
// toward its settings a step each call, and the steps are made for
// the 128 frames a mixer gives it.
const piece = 128

// processor runs the EQ on a block's first two channels.
type processor struct {
	eq     *audio.EQ
	insert audio.Insert
	// reducer is the insert, telling how far each band turns itself
	// down; ids the bands' IDs, as the plugin sets them.
	reducer reducer
	ids     *atomic.Pointer[[]int]
	rate    float64
	// scratch holds a chunk of the block as interleaved stereo frames.
	scratch []float32
	// bypass is the bypass the EQ was last told.
	bypass bool
	// gain is the gain put on the sound, as set and as it glides there,
	// a frame at a time at glide.
	gainTo, gain, glide float32
	// traces keep how far each band turned itself down, by frame, for
	// the views: a trace a band, the band's ID with it.
	traces [maxBands]bandTraced
}

// reducer is an EQ's insert, as it tells how far a band turns itself
// down, and the level it hears.
type reducer interface {
	Reduction(id int) (float64, bool)
	Heard(id int) (float64, bool)
}

// bandTraced is a band's traces: the band's ID, 0 for none, how far it
// turned itself down, and the level it heard.
type bandTraced struct {
	id    atomic.Int64
	trace plug.Trace
	heard plug.Trace
}

// Prepare implements [plug.Processor].
func (p *processor) Prepare(s plug.Setup) {
	p.rate = s.Rate
	p.glide = float32(1 - math.Exp(-1/(0.02*s.Rate)))
	p.gain = p.target()
	p.setInsert(p.eq.InsertAt(int(s.Rate)))
	p.scratch = make([]float32, 2*max(s.MaxBlock, 0))
}

// Process implements [plug.Processor]: it works through the block in
// chunks that fit the scratch buffer, and each change takes effect from
// the start of the chunk its frame falls in. Channels past the second
// pass through.
func (p *processor) Process(b *plug.Block) {
	for ch := 2; ch < len(b.Out) && ch < len(b.In); ch++ {
		copy(b.Out[ch][:b.Frames], b.In[ch][:b.Frames])
	}
	chunk := len(p.scratch) / 2
	if p.insert == nil || chunk == 0 || len(b.In) == 0 || len(b.Out) == 0 {
		p.passBy(b)
		return
	}
	next := 0
	p.claimTraces()
	for at := 0; at < b.Frames; at += chunk {
		n := min(chunk, b.Frames-at)
		for next < len(b.Changes) && b.Changes[next].Frame < at+n {
			p.take(b.Changes[next])
			next++
		}
		p.run(b, at, n)
	}
	for ; next < len(b.Changes); next++ {
		p.take(b.Changes[next])
	}
}

// run runs the EQ on n frames of b from frame at.
func (p *processor) run(b *plug.Block, at, n int) {
	left, right := b.In[0][at:at+n], b.In[0][at:at+n]
	if len(b.In) > 1 {
		right = b.In[1][at : at+n]
	}
	buf := p.scratch[:2*n]
	for i := range n {
		buf[2*i], buf[2*i+1] = left[i], right[i]
	}
	for i := 0; i < n; i += piece {
		end := min(i+piece, n)
		p.insert.Process(buf[2*i : 2*end])
		p.keepDown(b.Position.Frame+int64(at+i), b.Position.Frame+int64(at+end-1))
	}
	// The gain, gliding a frame at a time.
	outL := b.Out[0][at : at+n]
	var outR []float32
	if len(b.Out) > 1 {
		outR = b.Out[1][at : at+n]
	}
	to := p.target()
	for i := range n {
		p.gain += (to - p.gain) * p.glide
		outL[i] = buf[2*i] * p.gain
		if outR != nil {
			outR[i] = buf[2*i+1] * p.gain
		}
	}
}

// target is the gain the sound glides to: as set, or none while the EQ
// is bypassed.
func (p *processor) target() float32 {
	if p.bypass {
		return 1
	}
	return p.gainTo
}

// passBy copies the first two channels through unchanged, as a
// processor not yet prepared does.
func (p *processor) passBy(b *plug.Block) {
	for ch := range min(len(b.In), len(b.Out), 2) {
		copy(b.Out[ch][:b.Frames], b.In[ch][:b.Frames])
	}
	for _, c := range b.Changes {
		p.take(c)
	}
}

// take takes a change to the bypass, the amount, the Q, the threshold
// or the gain.
func (p *processor) take(c plug.Change) {
	prm, ok := Descriptor.Param(c.ID)
	if !ok {
		return
	}
	v := prm.Clamp(c.Value)
	switch c.ID {
	case AmountID:
		p.eq.SetAmount(v / 100)
	case QID:
		p.eq.SetQScale(v / 100)
	case ThresholdID:
		p.eq.SetThresholdShift(v)
	case GainID:
		p.gainTo = float32(math.Pow(10, v/20))
	case BypassID:
		if on := c.Value > 0.5; on != p.bypass {
			p.bypass = on
			p.eq.SetBypass(on)
		}
	}
}

// setInsert makes in the insert played.
func (p *processor) setInsert(in audio.Insert) {
	p.insert = in
	p.reducer, _ = in.(reducer)
}

// claimTraces gives each band a trace: its own, where it has one, or
// one a band gone had.
func (p *processor) claimTraces() {
	ids := *p.ids.Load()
	for _, id := range ids {
		if p.traceOf(int64(id)) != nil {
			continue
		}
		for k := range p.traces {
			t := &p.traces[k]
			if old := t.id.Load(); old == 0 || !slices.Contains(ids, int(old)) {
				t.id.Store(int64(id))
				break
			}
		}
	}
}

// traceOf returns band id's traces, or nil.
func (p *processor) traceOf(id int64) *bandTraced {
	for k := range p.traces {
		if p.traces[k].id.Load() == id {
			return &p.traces[k]
		}
	}
	return nil
}

// keepDown keeps how far each band turns itself down for frames from to
// to, as the insert last played them.
func (p *processor) keepDown(from, to int64) {
	if p.reducer == nil {
		return
	}
	for k := range p.traces {
		t := &p.traces[k]
		id := t.id.Load()
		if id == 0 {
			continue
		}
		r, ok := p.reducer.Reduction(int(id))
		if !ok {
			continue
		}
		t.trace.Put(from, float32(r))
		t.trace.Put(to, float32(r))
		if h, ok := p.reducer.Heard(int(id)); ok {
			t.heard.Put(from, -float32(h))
			t.heard.Put(to, -float32(h))
		}
	}
}

// Trace implements [plug.Tracer]: band n's reduction as bandTrace+n,
// and the level it hears as heardTrace+n.
func (p *processor) Trace(id uint32) *plug.Trace {
	switch {
	case id > heardTrace:
		if t := p.traceOf(int64(id - heardTrace)); t != nil {
			return &t.heard
		}
	case id > bandTrace:
		if t := p.traceOf(int64(id - bandTrace)); t != nil {
			return &t.trace
		}
	}
	return nil
}

// Latency implements [plug.Processor]: the EQ is never late.
func (p *processor) Latency() int { return 0 }

// Tail implements [plug.Processor]: the EQ's ringing is too short to
// count.
func (p *processor) Tail() int { return 0 }

// Reset implements [plug.Processor]: a fresh insert, with nothing held
// from before.
func (p *processor) Reset() {
	if p.rate > 0 {
		p.setInsert(p.eq.InsertAt(int(p.rate)))
	}
}

// isCut says whether a band of kind k cuts, and so has a slope and
// no gain.
func isCut(k audio.FilterKind) bool { return k == audio.LowCut || k == audio.HighCut }

// hasGain says whether a band of kind k lifts and lowers.
func hasGain(k audio.FilterKind) bool {
	return k == audio.Bell || k == audio.LowShelf || k == audio.HighShelf
}

// clampQ returns q within the bounds a band's Q keeps to.
func clampQ(q float64) float32 { return float32(max(minQ, min(q, maxQ))) }

// clampHz returns hz within the graph's frequencies.
func clampHz(hz float64) float32 { return float32(max(eqLow, min(hz, eqHigh))) }

// clampDB returns db within the graph's gains.
func clampDB(db float32) float32 { return max(-eqRange, min(db, eqRange)) }
