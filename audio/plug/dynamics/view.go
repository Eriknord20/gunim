package dynamics

import (
	"image/color"
	"math"
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

// reduction reads how far the compressor and limiter turn the sound
// heard down, together, in decibels, below zero: from what the plugin
// kept for the very frames heard, so it moves with the sound. Between
// the chunks the sound comes in it holds, and once the sound stops it
// reads nothing.
type reduction struct {
	h    plug.Host
	l    plug.Listener
	last float32
}

func newReduction(h plug.Host) *reduction {
	_, out := h.Sound()
	return &reduction{h: h, l: plug.Listener{Sound: out}}
}

// read reads it, a frame of the window, dt, on, and whether the sound
// plays.
func (r *reduction) read(dt time.Duration) (db float32, live bool) {
	frames, _, playing := r.l.Hear(4096, dt)
	switch {
	case len(frames) > 0:
		end := r.l.At()
		r.last = 0
		for f := end - int64(len(frames)/2); f < end; f += plug.TraceFrames {
			c, _ := r.h.Traced(CompGRID, f)
			l, _ := r.h.Traced(LimitGRID, f)
			r.last = min(r.last, c+l)
		}
	case !playing:
		r.last = 0
	}
	return r.last, playing
}

// vuOf reads the level of a sound in VU, 0 VU at ref() dBFS: the
// average of the frames heard since the last chunk, as a VU meter
// rectifies, held between chunks, and whether the sound plays.
func vuOf(s plug.Sound, ref func() float64) func(time.Duration) (float32, bool) {
	l := plug.Listener{Sound: s}
	last := float32(vuRest)
	return func(dt time.Duration) (float32, bool) {
		frames, _, playing := l.Hear(8192, dt)
		switch {
		case len(frames) > 0:
			var sum float64
			for _, v := range frames {
				sum += math.Abs(float64(v))
			}
			// A sine's average is 0.9 of its RMS, by which a VU meter
			// reads it.
			avg := sum / float64(len(frames)) * math.Pi / (2 * math.Sqrt2)
			last = float32(dbOf(avg) - ref())
		case !playing:
			last = vuRest
		}
		return last, playing
	}
}

// chip shows, in a plugin's chip in a chain, the compressor's ratio,
// or the limiter's ceiling where the compressor is off, and a bar of
// how far the two turn the sound down, growing from the right.
type chip struct {
	label  *plugui.ChipText
	values map[uint32]float64
	r      *reduction
	shown  *anim.Float
	group  anim.Group
}

func newChip(h plug.Host) *chip {
	c := &chip{label: plugui.NewChipText(""), values: map[uint32]float64{}, r: newReduction(h), shown: anim.NewFloat(0)}
	for _, id := range []uint32{CompOnID, CompRatioID, LimitOnID, LimitCeilingID} {
		c.values[id] = h.Param(id)
	}
	c.label.Text = c.text()
	c.group.Add(c.shown)
	return c
}

// text is what the chip says.
func (c *chip) text() string {
	switch {
	case c.values[CompOnID] > 0.5:
		return param(CompRatioID).Text(c.values[CompRatioID])
	case c.values[LimitOnID] > 0.5:
		return param(LimitCeilingID).Text(c.values[LimitCeilingID])
	}
	return "Off"
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
		p.RRect(geom.Rc(x+chipBar-w, y, w, h), h/2, paint.Solid(HistoryCut.Get(th)))
	}
}

// Step implements [gunim.Animator]: the bar follows the reduction.
func (c *chip) Step(dt time.Duration) bool {
	db, live := c.r.read(dt)
	c.shown.Animate(db, anim.Snappy)
	return c.group.Step(dt) || live
}

// Access implements [gunim.Accessible]: the chip reads as its text.
func (c *chip) Access() access.Info { return c.label.Access() }

// Follow implements [plug.Follower].
func (c *chip) Follow(id uint32, v float64, u *gunim.UI) {
	if _, ok := c.values[id]; ok {
		c.values[id] = v
		c.label.Text = c.text()
		u.Invalidate()
	}
}

// panel is the history of the sound, beside two VU meters, of how far
// the compressor and limiter turn the sound down and of the sound that
// comes out, over the sections' knobs.
type panel struct {
	hist *plugui.History
	// marks are where the history's lines are drawn: the thresholds and
	// the ceiling, with whether each part is on.
	marks    marks
	gr, out  *vu
	sections []*plugui.Section
	ref      float64
	// zero sets the level that reads 0 VU, a trim pot in the output
	// meter's corner; side picks what the compressor listens to, and
	// listen plays it.
	zero   *plugui.TrimPot
	side   *plugui.Selector
	freq   *plugui.Knob
	listen *widget.IconButton
	// tuner tunes how the history shows behind the controls, and
	// metersAt is where the meters' pane is.
	tuner    *plugui.Tuner
	metersAt geom.Rect
}

func newPanel(h plug.Host) *panel {
	r := newReduction(h)
	_, out := h.Sound()
	p := &panel{ref: h.Param(MeterID),
		tuner: plugui.NewTuner("Tune how the sound shows behind the controls")}
	for _, id := range []uint32{GateOnID, GateThresholdID, CompOnID, CompThresholdID, LimitOnID, LimitCeilingID} {
		p.marks.follow(id, h.Param(id))
	}
	m := &p.marks
	p.hist = plugui.NewHistory(h, []plugui.Reduction{
		{IDs: []uint32{GateGRID}, Color: HistoryGate},
		{IDs: []uint32{CompGRID, LimitGRID}, Color: HistoryCut, Line: true},
	}, []plugui.Mark{
		{Name: "GATE", Color: HistoryGate, At: func() (float64, bool) { return m.gateAt, m.gateOn }},
		{Name: "THRESHOLD", Color: widget.Ink, At: func() (float64, bool) { return m.compAt, m.compOn }},
		{Name: "CEILING", Color: HistoryCut, At: func() (float64, bool) { return m.ceiling, m.limOn }},
	}, false)
	p.gr = newVU("GAIN REDUCTION", r.read)
	p.out = newVU("OUTPUT", vuOf(out, func() float64 { return p.ref }))
	p.zero = plugui.NewTrimPot(h, param(MeterID))
	p.zero.Tooltip = "The level that reads 0 VU on the output meter, in dBFS"
	p.side = plugui.NewSelector("Sidechain", sideNames, sideTips)
	p.side.Columns = 2
	p.side.SetSelected(int(math.Round(h.Param(SideModeID))), nil)
	p.side.OnPick = func(i int, u *gunim.UI) {
		p.freq.Disabled = i == SideFull
		h.Begin(SideModeID, u)
		h.Set(SideModeID, float64(i), u)
		h.End(SideModeID, u)
	}
	p.listen = widget.NewIconButton(icon.Headphones, "Listen to what the compressor listens to")
	p.listen.Active = h.Param(SideListenID) > 0.5
	p.listen.OnClick = func(u *gunim.UI) gunim.Intent {
		on := 1.0
		if p.listen.Active {
			on = 0
		}
		h.Begin(SideListenID, u)
		h.Set(SideListenID, on, u)
		h.End(SideListenID, u)
		return nil
	}
	side := plugui.NewSection(h, &Descriptor, "SIDECHAIN", 0, SideFreqID)
	p.freq = side.Knobs[0]
	p.freq.Tooltip = "The frequency the compressor listens under, about or over"
	p.freq.Disabled = p.side.Selected() == SideFull
	side.Add(p.side, 56).Add(p.listen, 32)
	p.sections = []*plugui.Section{
		plugui.NewSection(h, &Descriptor, "INPUT", 0, InputID),
		plugui.NewSection(h, &Descriptor, "GATE", GateOnID, GateThresholdID, GateRangeID, GateAttackID, GateReleaseID),
		plugui.NewSection(h, &Descriptor, "COMPRESSOR", CompOnID, CompThresholdID, CompRatioID, CompKneeID, CompAttackID, CompReleaseID, CompMakeupID),
		side,
		plugui.NewSection(h, &Descriptor, "LIMITER", LimitOnID, LimitCeilingID, LimitReleaseID),
		plugui.NewSection(h, &Descriptor, "OUTPUT", 0, OutputID),
	}
	return p
}

// Children implements [gunim.Composite].
func (p *panel) Children() []gunim.Node {
	kids := make([]gunim.Node, 0, 6+len(p.sections))
	kids = append(kids, p.hist, p.gr, p.out)
	for _, s := range p.sections {
		kids = append(kids, s)
	}
	return append(kids, p.zero, p.tuner.Button, p.tuner.Card)
}

// Layout implements [gunim.Node].
func (p *panel) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	// The sound's history fills the panel; over it, on glass, the
	// sections along the foot, in rows as many as fit, each row centred,
	// and the meters side by side above them, centred.
	kids.At(0).Layout(gunim.Tight(size))
	kids.At(0).Place(geom.Point{})
	top := plugui.PlaceSections(size, panelInset, p.sections, func(i int, r geom.Rect) {
		kids.At(3 + i).Layout(gunim.Tight(r.Size()))
		kids.At(3 + i).Place(r.Min)
	})
	// The meters, as a meter's face is, wider than tall, on their pane.
	room := top - plugui.SectionGap - 2*meterPad - panelInset
	mh := max(min(room, meterH), 40)
	mw := mh / 0.62
	pw := 2*mw + plugui.SectionGap + 2*meterPad
	p.metersAt = geom.Rc((size.W-pw)/2, top-plugui.SectionGap-mh-2*meterPad, pw, mh+2*meterPad)
	for i := range 2 {
		kids.At(1 + i).Layout(gunim.Tight(geom.Sz(mw, mh)))
		kids.At(1 + i).Place(p.metersAt.Min.Add(geom.Pt(meterPad+float32(i)*(mw+plugui.SectionGap), meterPad)))
	}
	// The 0 VU trim pot, in the output meter's lower right corner.
	n := 3 + len(p.sections)
	outAt := p.metersAt.Min.Add(geom.Pt(meterPad+mw+plugui.SectionGap, meterPad))
	kids.At(n).Layout(gunim.Tight(geom.Sz(plugui.TrimPotSize, plugui.TrimPotSize)))
	kids.At(n).Place(outAt.Add(geom.Pt(mw-plugui.TrimPotSize-11, mh-plugui.TrimPotSize-11)))
	// The tuner's button at the top right, its card under it.
	n++
	kids.At(n).Layout(gunim.Tight(geom.Sz(32, 32)))
	kids.At(n).Place(geom.Pt(size.W-32-panelInset/2, panelInset/2))
	cs := kids.At(n + 1).Layout(gunim.Constraints{Max: geom.Sz(plugui.TunerW, plugui.TunerH)})
	kids.At(n + 1).Place(geom.Pt(size.W-panelInset/2-max(cs.W, plugui.TunerW), panelInset/2+36))
	return size
}

// The panel's sizes: how far in from its edges the glass stands, how
// tall the meters are at most, and the room round them on their pane.
const (
	panelInset = 16
	meterH     = 130
	meterPad   = 10
)

// Paint implements [gunim.Node]: the history, then on it the meters'
// pane, the meters, the sections and the tuner.
func (p *panel) Paint(pt *paint.Painter, f gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(pt)
	plugui.PaintGlass(pt, f.Theme, p.metersAt, widget.CardRadius.Get(f.Theme)+4)
	for i := 1; i < kids.Len(); i++ {
		kids.At(i).Paint(pt)
	}
}

// Follow implements [plug.Follower]: the knobs and switches glide to
// their parameters set elsewhere, unless held, and the history's lines
// move.
func (p *panel) Follow(id uint32, v float64, u *gunim.UI) {
	for _, s := range p.sections {
		s.Follow(id, v, u)
	}
	p.marks.follow(id, v)
	switch id {
	case MeterID:
		p.ref = v
		p.zero.Follow(v, u)
	case SideModeID:
		p.side.SetSelected(int(math.Round(v)), u)
		p.freq.Disabled = p.side.Selected() == SideFull
	case SideListenID:
		p.listen.Active = v > 0.5
	}
	u.Invalidate()
}

// The history's colours: how far the compressor and limiter turn the
// sound down, and how far the gate does.
var (
	HistoryCut  = theme.Color("dynamics.history.cut", color.NRGBA{R: 0xff, G: 0x5a, B: 0x4f, A: 0xff})
	HistoryGate = theme.Color("dynamics.history.gate", color.NRGBA{R: 0x6c, G: 0x7c, B: 0xff, A: 0xff})
)

// marks are the thresholds and the ceiling the history draws lines at,
// with whether each part is on.
type marks struct {
	gateAt, compAt, ceiling float64
	gateOn, compOn, limOn   bool
}

// follow takes a parameter the history draws a line of.
func (m *marks) follow(id uint32, v float64) {
	switch id {
	case GateOnID:
		m.gateOn = v > 0.5
	case GateThresholdID:
		m.gateAt = v
	case CompOnID:
		m.compOn = v > 0.5
	case CompThresholdID:
		m.compAt = v
	case LimitOnID:
		m.limOn = v > 0.5
	case LimitCeilingID:
		m.ceiling = v
	}
}
