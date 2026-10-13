package vocal

import (
	"fmt"
	"image/color"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugui"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// The chain's colours: how far the de-esser, and each compressor, turn
// the voice down.
var (
	DeEssColor = theme.Color("vocal.deess", color.NRGBA{R: 0x5a, G: 0xc8, B: 0xff, A: 0xff})
	PeakColor  = theme.Color("vocal.peak", color.NRGBA{R: 0xff, G: 0x5a, B: 0x4f, A: 0xff})
	GlueColor  = theme.Color("vocal.glue", color.NRGBA{R: 0xff, G: 0xb0, B: 0x4a, A: 0xff})
	TargetLine = theme.Color("vocal.target", color.NRGBA{R: 0x5a, G: 0xd8, B: 0x9a, A: 0xff})
)

// downOf reads how far the two compressors turn the voice heard down,
// together: from what the processor kept for the frames heard, held
// between the chunks the sound comes in, nothing once it stops.
type downOf struct {
	h    plug.Host
	l    plug.Listener
	last float32
}

func newDownOf(h plug.Host) *downOf {
	_, out := h.Sound()
	return &downOf{h: h, l: plug.Listener{Sound: out}}
}

// read reads it, a frame of the window, dt, on, and whether the sound
// plays.
func (r *downOf) read(dt time.Duration) (db float32, live bool) {
	frames, _, playing := r.l.Hear(4096, dt)
	switch {
	case len(frames) > 0:
		end := r.l.At()
		r.last = 0
		for f := end - int64(len(frames)/2); f < end; f += plug.TraceFrames {
			peak, _ := r.h.Traced(PeakGRID, f)
			glue, _ := r.h.Traced(GlueGRID, f)
			r.last = min(r.last, peak+glue)
		}
	case !playing:
		r.last = 0
	}
	return r.last, playing
}

// chip shows, in a plugin's chip in a chain, the plugin's name, and a
// bar of how far the compressors turn the voice down, from the right.
type chip struct {
	label *plugui.ChipText
	r     *downOf
	shown *anim.Float
	group anim.Group
}

func newChip(h plug.Host) *chip {
	c := &chip{label: plugui.NewChipText("Vocal"), r: newDownOf(h), shown: anim.NewFloat(0)}
	c.group.Add(c.shown)
	return c
}

// chipBar is how wide the chip's bar is, and how many decibels fill it.
const (
	chipBar   = 40
	chipBarDB = 12
)

// Children implements [gunim.Composite].
func (c *chip) Children() []gunim.Node { return []gunim.Node{c.label} }

// Layout implements [gunim.Node].
func (c *chip) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kids.At(0).Layout(gunim.Constraints{Max: geom.Sz(max(cs.Max.W-chipBar-6, 0), cs.Max.H)})
	kids.At(0).Place(geom.Point{})
	return cs.Max
}

// Paint implements [gunim.Node].
func (c *chip) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
	th := f.Theme
	x, h := box.W-chipBar, float32(5)
	y := (box.H - h) / 2
	p.RRect(geom.Rc(x, y, chipBar, h), h/2, paint.Solid(audioui.Faded(audioui.Ground.Get(th), 0.85)))
	if w := chipBar * -plugui.ChipScale(float64(c.shown.Value()), chipBarDB); w > 0.5 {
		p.RRect(geom.Rc(x+chipBar-w, y, w, h), h/2, paint.Solid(PeakColor.Get(th)))
	}
}

// Step implements [gunim.Animator]: the bar follows the reduction.
func (c *chip) Step(dt time.Duration) bool {
	db, live := c.r.read(dt)
	c.shown.Animate(db, anim.Snappy)
	return c.group.Step(dt) || live
}

// Access implements [gunim.Accessible].
func (c *chip) Access() access.Info { return c.label.Access() }

// panel is the voice's history, filling the panel, with how far the
// de-esser and the compressors turn it down; over it, on glass, the
// macros and Learn, and, in full, a section for each part of the chain.
type panel struct {
	p        *plugin
	h        plug.Host
	hist     *plugui.History
	sections []*plugui.Section
	learn    *plugui.ActionDial
	// values are the parameters the panel shows, as last set.
	values map[uint32]float64
	// learning says Learn reads the voice, and stopPoll stops it looking
	// how far.
	learning bool
	stopPoll func()
}

// The macros, in their row.
var macroIDs = []uint32{DeEssID, RumbleID, PresenceID, SquashID, ParallelID, TapeID, OctaveID, WidthID}

func newPanel(pl *plugin, h plug.Host, full bool) *panel {
	p := &panel{p: pl, h: h, values: map[uint32]float64{}}
	for _, prm := range Descriptor.Params {
		p.values[prm.ID] = h.Param(prm.ID)
	}
	p.hist = plugui.NewHistory(h, []plugui.Reduction{
		{IDs: []uint32{DeEssGRID}, Color: DeEssColor},
		{IDs: []uint32{PeakGRID, GlueGRID}, Color: GlueColor},
		{IDs: []uint32{PeakGRID}, Color: PeakColor, Line: true},
	}, []plugui.Mark{
		{Name: "TARGET", Color: TargetLine, At: func() (float64, bool) { return p.values[TargetID], true }},
	}, true)
	p.learn = plugui.NewActionDial("Learn", icon.Ear)
	// While it learns, the dial stops it.
	p.learn.OnClick = func(u *gunim.UI) {
		if p.learning {
			p.halt(u)
		} else {
			p.startLearn(u)
		}
	}
	p.learnable()
	learn := plugui.NewSection(h, &Descriptor, "LEARN", 0, TargetID)
	learn.Add(p.learn, plugui.KnobW)
	p.sections = []*plugui.Section{plugui.NewSection(h, &Descriptor, "MACROS", 0, macroIDs...), learn}
	if full {
		p.sections = append(p.sections,
			plugui.NewSection(h, &Descriptor, "DE-ESSER", DeEssOnID, DeEssFreqID, DeEssThresholdID, DeEssRangeID),
			plugui.NewSection(h, &Descriptor, "LEVELS", 0, InputID, OutputID, WidthFreqID),
			plugui.NewSection(h, &Descriptor, "CLEAN EQ", CleanOnID, LowShelfFqID, LowShelfID, MudFreqID, MudGainID, MudQID),
			plugui.NewSection(h, &Descriptor, "TONE EQ", ToneOnID, HighPassID, PresenceFreqID, PresenceGainID, AirFreqID, AirGainID),
			plugui.NewSection(h, &Descriptor, "PEAK COMP", PeakOnID, PeakThresholdID, PeakRatioID, PeakAttackID, PeakReleaseID),
			plugui.NewSection(h, &Descriptor, "GLUE COMP", GlueOnID, GlueThresholdID, GlueRatioID, GlueAttackID, GlueReleaseID),
		)
	}
	return p
}

// Learn's tooltip.
const learnTip = "Read the voice as it reaches the chain, and set the chain for it: Input and Output to the target, " +
	"the EQs to its balance, the de-esser to its hiss, and each compressor to take about 2 dB. The macros stay as " +
	"they are. Undo puts the chain back."

// learnable shows Learn at rest: greyed where there is no voice to
// read; else what it would read, the loop or the whole track, greyed
// where what it last learned was for another target.
func (p *panel) learnable() {
	if p.learning {
		return
	}
	ok, loop := p.p.canLearn()
	l := p.p.learnt
	p.learn.Disabled = !ok
	p.learn.Stale = l.ok && l.target != p.values[TargetID]
	scope, what := "Track", "the whole track"
	if loop {
		scope, what = "Loop", "the loop alone"
	}
	p.learn.Value, p.learn.Tooltip = scope, learnTip+" It reads "+what+"."
	switch {
	case !ok:
		p.learn.Value, p.learn.Tooltip = "No track", "The host gives the chain no track to read"
	case p.learn.Stale:
		p.learn.Tooltip = fmt.Sprintf("Learned for %.0f LUFS; the target moved since. ", l.target) + p.learn.Tooltip
	}
}

// startLearn starts learning the chain, and follows it.
func (p *panel) startLearn(u *gunim.UI) {
	if ok, _ := p.p.canLearn(); p.learning || !ok {
		return
	}
	p.learning = true
	p.learn.Start(u)
	p.learn.Stale, p.learn.Value, p.learn.Tooltip = false, "0%", "Stop learning"
	p.p.startLearning(p.values)
	p.watchLearning(u)
}

// halt stops learning.
func (p *panel) halt(u *gunim.UI) {
	if !p.learning {
		return
	}
	p.p.stopLearning()
	if p.stopPoll != nil {
		p.stopPoll()
	}
	p.finish(false, u)
}

// learnPoll is how often Learn looks how far it has got.
const learnPoll = 50 * time.Millisecond

// watchLearning looks how far Learn has got: while it reads, it shows
// how far and looks again a moment later; once it is done, it sets the
// chain it learned, every parameter of it as one step to undo.
func (p *panel) watchLearning(u *gunim.UI) {
	if !p.learning {
		return
	}
	got, learning, progress, err := p.p.learnState()
	switch {
	case learning:
		p.learn.Value = fmt.Sprintf("%.0f%%", 100*progress)
		p.learn.SetProgress(float32(progress), u)
		p.stopPoll = u.After(learnPoll, p.watchLearning)
		return
	case err != nil:
		p.finish(false, u)
		p.learn.Value, p.learn.Tooltip = "Failed", "Could not learn: "+err.Error()
	case got != nil:
		p.apply(got.values, u)
		p.p.learnt = learnt{target: p.values[TargetID], loop: got.loop, ok: true}
		p.finish(true, u)
	default:
		p.finish(false, u)
	}
}

// apply sets values, by ID, as one gesture: each begun before any is
// set, and ended after all are.
func (p *panel) apply(values map[uint32]float64, u *gunim.UI) {
	for _, id := range learnedIDs {
		if _, ok := values[id]; ok {
			p.h.Begin(id, u)
		}
	}
	for _, id := range learnedIDs {
		if v, ok := values[id]; ok {
			p.h.Set(id, v, u)
			p.values[id] = v
		}
	}
	for _, id := range learnedIDs {
		if _, ok := values[id]; ok {
			p.h.End(id, u)
		}
	}
}

// finish ends learning: done, the dial glowing, or stopped.
func (p *panel) finish(done bool, u *gunim.UI) {
	p.learning = false
	p.learn.Finish(done, u)
	p.learnable()
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (p *panel) Children() []gunim.Node {
	kids := make([]gunim.Node, 0, 1+len(p.sections))
	kids = append(kids, p.hist)
	for _, s := range p.sections {
		kids = append(kids, s)
	}
	return kids
}

// panelInset is how far in from the panel's edges the glass stands.
const panelInset = 16

// Layout implements [gunim.Node]: the history fills the panel, and over
// it, on glass, the sections along the foot.
func (p *panel) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	kids.At(0).Layout(gunim.Tight(size))
	kids.At(0).Place(geom.Point{})
	plugui.PlaceSections(size, panelInset, p.sections, func(i int, r geom.Rect) {
		kids.At(1 + i).Layout(gunim.Tight(r.Size()))
		kids.At(1 + i).Place(r.Min)
	})
	return size
}

// Paint implements [gunim.Node].
func (p *panel) Paint(pt *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(pt)
	}
}

// Follow implements [plug.Follower]: the knobs and the lamps glide to
// their parameters set elsewhere, unless held.
func (p *panel) Follow(id uint32, v float64, u *gunim.UI) {
	p.values[id] = v
	for _, s := range p.sections {
		s.Follow(id, v, u)
	}
	if id == TargetID {
		// What Learn learned greys as the target moves from it.
		p.learnable()
	}
	u.Invalidate()
}

// FollowDocument implements [plug.DocumentFollower]: Learn can read the
// voice, or not, and the loop alone, or the whole track.
func (p *panel) FollowDocument(u *gunim.UI) {
	p.learnable()
	u.Invalidate()
}
