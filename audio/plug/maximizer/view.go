package maximizer

import (
	"fmt"
	"image/color"
	"math"
	"strings"
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
	"github.com/marrasen/gunim/widget"
)

// The maximizer's colours: how far it turns the sound down, and a
// loudness on its target, or off it.
var (
	MaxCut    = theme.Color("maximizer.cut", color.NRGBA{R: 0xff, G: 0x5a, B: 0x4f, A: 0xff})
	MaxOnTarg = theme.Color("maximizer.target.on", color.NRGBA{R: 0x5a, G: 0xd8, B: 0x9a, A: 0xff})
	MaxOffTar = theme.Color("maximizer.target.off", color.NRGBA{R: 0xff, G: 0xc8, B: 0x5a, A: 0xff})
)

// modeTips say what each mode does, for its lamp's tooltip.
var modeTips = []string{
	"comes back fast after a lone peak, and slowly through a loud passage, for the least you can hear",
	"lets the start of each transient through before it turns it down, for drums that hit",
	"rounds the peaks off softly first, so the limiter has less to do, for the loudest masters",
}

// reductionOf reads how far the maximizer turns the sound heard down:
// from what it kept for the frames heard, held between the chunks the
// sound comes in, nothing once the sound stops.
type reductionOf struct {
	h    plug.Host
	l    plug.Listener
	last float32
}

func newReductionOf(h plug.Host) *reductionOf {
	_, out := h.Sound()
	return &reductionOf{h: h, l: plug.Listener{Sound: out}}
}

// read reads it, a frame of the window, dt, on, and whether the sound
// plays.
func (r *reductionOf) read(dt time.Duration) (db float32, live bool) {
	frames, _, playing := r.l.Hear(4096, dt)
	switch {
	case len(frames) > 0:
		end := r.l.At()
		r.last = 0
		for f := end - int64(len(frames)/2); f < end; f += plug.TraceFrames {
			if v, ok := r.h.Traced(ReductionID, f); ok {
				r.last = min(r.last, v)
			}
		}
	case !playing:
		r.last = 0
	}
	return r.last, playing
}

// chip shows, in a plugin's chip in a chain, the gain, and a bar of how
// far the maximizer turns the sound down, growing from the right.
type chip struct {
	label *plugui.ChipText
	r     *reductionOf
	shown *anim.Float
	group anim.Group
}

func newChip(h plug.Host) *chip {
	c := &chip{label: plugui.NewChipText(param(GainID).Text(h.Param(GainID))), r: newReductionOf(h), shown: anim.NewFloat(0)}
	c.group.Add(c.shown)
	return c
}

// chipBar is how wide the chip's bar is, and how many decibels fill it,
// drawn by the square root, so a decibel shows.
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
		p.RRect(geom.Rc(x+chipBar-w, y, w, h), h/2, paint.Solid(MaxCut.Get(th)))
	}
}

// Step implements [gunim.Animator]: the bar follows the reduction.
func (c *chip) Step(dt time.Duration) bool {
	db, live := c.r.read(dt)
	c.shown.Animate(db, anim.Snappy)
	return c.group.Step(dt) || live
}

// Access implements [gunim.Accessible]: the chip reads as the gain.
func (c *chip) Access() access.Info { return c.label.Access() }

// Follow implements [plug.Follower].
func (c *chip) Follow(id uint32, v float64, u *gunim.UI) {
	if id == GainID {
		c.label.Text = param(GainID).Text(v)
		u.Invalidate()
	}
}

// panel is the sound's history, filling the panel, with how far the
// maximizer turns it down and how loud it comes out; over it, on glass,
// the readouts of its loudness, its true peak and its reduction, and
// under them the sections of its controls: its mode; its gain, ceiling,
// character and stereo link; and its target, with Learn.
type panel struct {
	p        *plugin
	h        plug.Host
	hist     *plugui.History
	readouts *readouts
	mode     *plugui.Selector
	sections []*plugui.Section
	learn    *plugui.ActionDial
	tuner    *plugui.Tuner
	// values are the parameters the panel shows, as last set.
	values map[uint32]float64
	// learning says Learn reads the track, and stopPoll stops it looking
	// how far.
	learning bool
	stopPoll func()
	paneAt   geom.Rect
}

func newPanel(pl *plugin, h plug.Host) *panel {
	p := &panel{p: pl, h: h, values: map[uint32]float64{}, tuner: plugui.NewTuner("Tune how the sound shows behind the controls")}
	for _, prm := range Descriptor.Params {
		p.values[prm.ID] = h.Param(prm.ID)
	}
	p.hist = plugui.NewHistory(h, []plugui.Reduction{{IDs: []uint32{ReductionID}, Color: MaxCut, Line: true}},
		[]plugui.Mark{
			{Name: "CEILING", Color: MaxCut, At: func() (float64, bool) { return p.values[CeilingID], true }},
			{Name: "TARGET", Color: MaxOnTarg, At: func() (float64, bool) { return p.values[TargetID], true }},
		}, true)
	p.readouts = &readouts{p: p}
	p.mode = plugui.NewSelector("Mode", modeNames, modeTips)
	p.mode.SetSelected(int(math.Round(p.values[ModeID])), nil)
	p.mode.OnPick = func(i int, u *gunim.UI) {
		h.Begin(ModeID, u)
		h.Set(ModeID, float64(i), u)
		h.End(ModeID, u)
	}
	p.learn = plugui.NewActionDial("Learn", icon.Target)
	// While it learns, the dial stops it.
	p.learn.OnClick = func(u *gunim.UI) {
		if p.learning {
			p.halt(u)
		} else {
			p.startLearn(u)
		}
	}
	p.learnable()
	modeSection := &plugui.Section{Name: "MODE"}
	modeSection.Add(p.mode, 96)
	learn := plugui.NewSection(h, &Descriptor, "LOUDNESS", 0, TargetID)
	learn.Add(p.learn, plugui.KnobW)
	p.sections = []*plugui.Section{
		modeSection,
		plugui.NewSection(h, &Descriptor, "LIMITER", 0, GainID, CeilingID, CharacterID, LinkID),
		learn,
	}
	return p
}

// Learn's tooltip, and its word at rest before it has learned.
const (
	learnTip = "Read the whole track as it reaches the maximizer, and set the gain so it comes out as loud as the " +
		"target. Undo puts the gain back."
	learnReady = "Ready"
)

// learnable shows Learn at rest: greyed where there is no track to
// read; else what it last learned, greyed where the target has moved
// since, or ready to.
func (p *panel) learnable() {
	if p.learning {
		return
	}
	l := p.p.learnt
	p.learn.Disabled = !p.p.canLearn()
	p.learn.Stale = l.ok && l.target != p.values[TargetID]
	p.learn.Value, p.learn.Tooltip = learnReady, learnTip
	switch {
	case p.learn.Disabled:
		p.learn.Value, p.learn.Tooltip = "No track", "The host gives the maximizer no track to read"
	case l.ok:
		p.learn.Value = fmt.Sprintf("%+.1f dB", l.gain)
		switch {
		case p.learn.Stale:
			p.learn.Tooltip = fmt.Sprintf("Learned for %.1f LUFS; the target moved since. ", l.target) + learnTip
		case !l.reached:
			p.learn.Tooltip = "The target is out of reach: even at the most gain, the track comes out quieter. " + learnTip
		}
	}
}

// startLearn starts learning the gain, and follows it.
func (p *panel) startLearn(u *gunim.UI) {
	if p.learning || !p.p.canLearn() {
		return
	}
	s := settingsOf(func(id uint32) (float64, bool) { v, ok := p.values[id]; return v, ok })
	p.learning = true
	p.learn.Start(u)
	p.learn.Stale, p.learn.Value, p.learn.Tooltip = false, "0%", "Stop learning"
	p.p.startLearning(s, p.values[TargetID])
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
// gain it learned, as one step to undo.
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
		p.h.Begin(GainID, u)
		p.h.Set(GainID, got.gain, u)
		p.h.End(GainID, u)
		p.values[GainID] = got.gain
		p.sections[1].Follow(GainID, got.gain, u)
		target := p.values[TargetID]
		p.p.learnt = learnt{gain: got.gain, target: target, reached: math.Abs(got.loud-target) <= 0.5, ok: true}
		p.finish(true, u)
	default:
		p.finish(false, u)
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
	kids := make([]gunim.Node, 0, 4+len(p.sections))
	kids = append(kids, p.hist, p.readouts)
	for _, s := range p.sections {
		kids = append(kids, s)
	}
	return append(kids, p.tuner.Button, p.tuner.Card)
}

// The panel's sizes: how far in from its edges the glass stands, and
// the readouts' pane.
const (
	panelInset = 16
	readoutW   = 150
	readoutH   = 64
)

// Layout implements [gunim.Node]: the history fills the panel; over it,
// on glass, the sections along the foot, and the readouts centred above
// them.
func (p *panel) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	kids.At(0).Layout(gunim.Tight(size))
	kids.At(0).Place(geom.Point{})
	top := plugui.PlaceSections(size, panelInset, p.sections, func(i int, r geom.Rect) {
		kids.At(2 + i).Layout(gunim.Tight(r.Size()))
		kids.At(2 + i).Place(r.Min)
	})
	w := float32(len(readoutNames)) * readoutW
	p.paneAt = geom.Rc((size.W-w)/2, top-plugui.SectionGap-readoutH, w, readoutH)
	kids.At(1).Layout(gunim.Tight(p.paneAt.Size()))
	kids.At(1).Place(p.paneAt.Min)
	n := 2 + len(p.sections)
	kids.At(n).Layout(gunim.Tight(geom.Sz(32, 32)))
	kids.At(n).Place(geom.Pt(size.W-32-panelInset/2, panelInset/2))
	cs := kids.At(n + 1).Layout(gunim.Constraints{Max: geom.Sz(plugui.TunerW, plugui.TunerH)})
	kids.At(n + 1).Place(geom.Pt(size.W-panelInset/2-max(cs.W, plugui.TunerW), panelInset/2+36))
	return size
}

// Paint implements [gunim.Node].
func (p *panel) Paint(pt *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(pt)
	}
}

// Follow implements [plug.Follower]: the knobs and the lamps glide to
// their parameters set elsewhere, unless held, and the history's lines
// move.
func (p *panel) Follow(id uint32, v float64, u *gunim.UI) {
	p.values[id] = v
	for _, s := range p.sections {
		s.Follow(id, v, u)
	}
	switch id {
	case ModeID:
		p.mode.SetSelected(int(math.Round(v)), u)
	case TargetID:
		// What Learn learned greys as the target moves from it.
		p.learnable()
	}
	u.Invalidate()
}

// FollowDocument implements [plug.DocumentFollower]: Learn can read the
// track, or not.
func (p *panel) FollowDocument(u *gunim.UI) {
	p.learnable()
	u.Invalidate()
}

// readoutNames name the readouts, in their order.
var readoutNames = []string{"SHORT-TERM", "INTEGRATED", "TRUE PEAK", "REDUCTION"}

// readouts are the maximizer's numbers, on a pane of glass: how loud the
// sound out is over the last three seconds, and all heard, the
// integrated one coloured as it is on the target or off it; its true
// peak, the most since playing last jumped; and how far the maximizer
// turns it down now.
type readouts struct {
	p    *panel
	size geom.Size
}

// values returns the readouts' words, and whether the integrated
// loudness is on the target, and known.
func (r *readouts) values() (words [4]string, onTarget, known bool) {
	short, integrated, ok := r.p.hist.Loudness()
	words = [4]string{"–", "–", "–", "–"}
	if ok || short > -70 {
		words[0] = fmt.Sprintf("%.1f LUFS", short)
	}
	if ok {
		words[1] = fmt.Sprintf("%.1f LUFS", integrated)
		known, onTarget = true, math.Abs(integrated-r.p.values[TargetID]) <= 1
	}
	if hold := r.p.h.Param(PeakHoldID); hold > -59 {
		words[2] = fmt.Sprintf("%.1f dBTP", hold)
	}
	if n := r.p.hist.Columns(); n > 0 {
		_, _, down, _ := r.p.hist.Column(n - 1)
		words[3] = fmt.Sprintf("%.1f dB", min(down[0], 0))
		if down[0] > -0.05 {
			words[3] = "0.0 dB"
		}
	}
	return words, onTarget, known
}

// Layout implements [gunim.Node].
func (r *readouts) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	r.size = c.Max
	return c.Max
}

// Paint implements [gunim.Node]: the pane, and a readout a quarter of
// it, its name over its number.
func (r *readouts) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink := widget.Ink.Get(th)
	plugui.PaintGlass(p, th, geom.Rect{Max: box.Point()}, widget.CardRadius.Get(th)+4)
	words, onTarget, known := r.values()
	w := box.W / float32(len(readoutNames))
	for i, name := range readoutNames {
		x := float32(i) * w
		label := audioui.Shaped(name, 9.5, true, false)
		label.Paint(p, geom.Pt(x+(w-label.Advance)/2, 12), audioui.Faded(ink, 0.55))
		c := audioui.Faded(ink, 0.92)
		switch {
		case i == 1 && known && onTarget:
			c = MaxOnTarg.Get(th)
		case i == 1 && known:
			c = MaxOffTar.Get(th)
		case i == 3 && words[3] != "–" && words[3] != "0.0 dB":
			c = MaxCut.Get(th)
		}
		run := audioui.Shaped(words[i], 18, true, true)
		run.Paint(p, geom.Pt(x+(w-run.Advance)/2, 30), c)
		if i > 0 {
			p.RRect(geom.Rc(x, 12, 1, box.H-24), 0, paint.Solid(audioui.Faded(ink, 0.1)))
		}
	}
}

// Access implements [gunim.Accessible]: the readouts read as a group of
// their numbers.
func (r *readouts) Access() access.Info {
	words, _, _ := r.values()
	var v strings.Builder
	for i, name := range readoutNames {
		fmt.Fprintf(&v, "%s %s. ", name, words[i])
	}
	return access.Info{Role: access.RoleGroup, Name: "Loudness and peaks", Value: v.String()}
}
