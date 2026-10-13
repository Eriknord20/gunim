package impact

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
	"github.com/marrasen/gunim/audio/plug/xover"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// ImpactSmooth is the colour of a band that smooths; one that adds
// punch takes the theme's accent.
var ImpactSmooth = theme.Color("impact.smooth", color.NRGBA{R: 0x8a, G: 0xd4, B: 0xff, A: 0xff})

// punchColor is the colour of a band at punch v: the accent where it
// adds punch, or is at none, and the smoothing colour where it smooths.
func punchColor(th *theme.Live, v float64) color.NRGBA {
	if v < 0 {
		return ImpactSmooth.Get(th)
	}
	return widget.Accent.Get(th)
}

// The display's frequencies, from viewLow to viewHigh across; and its
// spectrum's points, and how many frames it measures, and the levels at
// its top and its foot.
const (
	viewLow, viewHigh = 20.0, 20000.0
	specPoints        = 160
	specFrames        = 4096
	specTop           = 0
	specBottom        = -84
)

// liveLow and liveHigh are how far a band is turned, in decibels, that
// shows as nothing, and as all, as it plays: a mastering engineer turns
// a hit little, so a little shows.
const (
	liveLow  = 0.25
	liveHigh = 6
)

// live is how far a turn of db decibels shows, from 0 to 1.
func live(db float32) float32 { return min(max((db-liveLow)/(liveHigh-liveLow), 0), 1) }

// values are the parameters as the views show them, by ID.
type values map[uint32]float64

func valuesOf(h plug.Host) values {
	v := values{}
	for _, p := range Descriptor.Params {
		v[p.ID] = h.Param(p.ID)
	}
	return v
}

// crossovers returns the crossovers in order, as the processor keeps
// them.
func (v values) crossovers() [bands - 1]float64 {
	var x [bands - 1]float64
	for i := range x {
		x[i] = v[CrossoverID+uint32(i)]
	}
	return xover.Order(x)
}

// punch is band b's punch, from -100 to 100.
func (v values) punch(b int) float64 { return v[PunchID+uint32(b)] }

// changeOf reads how far each band is turned as the sound plays, from
// the processor's traces, over the frames heard since it last read.
type changeOf struct {
	h  plug.Host
	l  plug.Listener
	db [bands]float32
}

func newChangeOf(h plug.Host) *changeOf {
	_, out := h.Sound()
	return &changeOf{h: h, l: plug.Listener{Sound: out}}
}

// read reads it, a frame of the window, dt, on, and reports whether the
// sound plays.
func (a *changeOf) read(dt time.Duration) bool {
	frames, _, playing := a.l.Hear(4096, dt)
	switch {
	case len(frames) > 0:
		end := a.l.At()
		for k := range a.db {
			a.db[k] = 0
			for f := end - int64(len(frames)/2); f < end; f += plug.TraceFrames {
				if v, ok := a.h.Traced(ChangeID+uint32(k), f); ok {
					a.db[k] = max(a.db[k], -v)
				}
			}
		}
	case !playing:
		a.db = [bands]float32{}
	}
	return playing
}

// display is the sound by pitch as it goes out, and the bands over it:
// the crossovers, to drag across, and each band's punch, a bar from the
// line across its middle, up in the accent as it adds punch and down
// in the smoothing colour as it smooths, glowing as the band is turned,
// to drag up and down, double-clicked back to none.
type display struct {
	h      plug.Host
	v      values
	out    plug.Sound
	frames []float32
	meter  *audioui.Spectrometer
	freqs  []float32
	// is is the level measured of the sound going out, and isSm as
	// drawn, easing to it; at is the frame heard last, still how long
	// nothing new came.
	is, isSm []float32
	at       int64
	still    time.Duration
	changes  *changeOf
	glow     [bands]*anim.Float
	group    anim.Group
	// hot is what the pointer is on: a crossover, hotX, from 0, or a
	// band's bar, hotBand, or neither; xLit and bandLit are how lit each
	// is, gliding. held is what is dragged.
	hotX, hotBand int
	xLit          [bands - 1]*anim.Float
	bandLit       [bands]*anim.Float
	held          int
	heldFrom      geom.Point
	heldWas       float64
	// line is the foot of the bars' room, and top its top, as the panel
	// lays them out; the bars stand on the middle between them.
	line, top float32
	size      geom.Size
}

// What a drag holds.
const (
	holdNone = iota
	holdCrossover
	holdBand
)

func newDisplay(h plug.Host, v values) *display {
	_, out := h.Sound()
	d := &display{h: h, v: v, out: out, frames: make([]float32, 2*specFrames),
		meter: audioui.NewSpectrometer(specFrames), changes: newChangeOf(h), hotX: -1, hotBand: -1,
		still: plug.StillAfter}
	d.freqs = make([]float32, specPoints)
	for i := range d.freqs {
		d.freqs[i] = float32(viewLow * math.Pow(viewHigh/viewLow, float64(i)/(specPoints-1)))
	}
	for _, p := range []*[]float32{&d.is, &d.isSm} {
		*p = make([]float32, specPoints)
		for i := range *p {
			(*p)[i] = specBottom - 20
		}
	}
	for k := range d.xLit {
		d.xLit[k] = anim.NewFloat(0)
		d.group.Add(d.xLit[k])
	}
	for k := range d.bandLit {
		d.bandLit[k] = anim.NewFloat(0)
		d.group.Add(d.bandLit[k])
	}
	for k := range d.glow {
		d.glow[k] = anim.NewFloat(0)
		d.group.Add(d.glow[k])
	}
	return d
}

// measure measures what the sound last brought, and reports whether it
// brought anything new.
func (d *display) measure() bool {
	if d.out == nil {
		return false
	}
	n, at, rate := d.out.Heard(d.frames)
	if at == 0 || n != specFrames || at == d.at {
		return false
	}
	d.at = at
	d.meter.Measure(d.frames, rate, d.freqs, d.is)
	return true
}

// Step implements [gunim.Animator]: the spectrum measured anew as the
// sound brings more, held between its chunks, falling once it stops;
// and each band glowing as it is turned.
func (d *display) Step(dt time.Duration) bool {
	playing := false
	switch {
	case d.measure():
		d.still, playing = 0, true
	case d.at != 0:
		d.still = min(d.still+dt, plug.StillAfter)
		playing = d.still < plug.StillAfter
	}
	if !playing {
		for i := range d.is {
			d.is[i] = specBottom - 20
		}
	}
	turning := d.changes.read(dt)
	for k, g := range d.glow {
		g.Animate(live(d.changes.db[k]), anim.Snappy)
	}
	sec := float32(dt.Seconds())
	settled := true
	for i := range d.is {
		rate := float32(30)
		if d.is[i] < d.isSm[i] {
			rate = 6
		}
		d.isSm[i] += (d.is[i] - d.isSm[i]) * min(1, rate*sec)
		if math.Abs(float64(d.is[i]-d.isSm[i])) > 0.3 {
			settled = false
		}
	}
	return d.group.Step(dt) || playing || turning || !settled
}

// xOf is where across the display a frequency is, and hzAt the
// frequency at x.
func (d *display) xOf(hz float64) float32 {
	return d.size.W * float32(math.Log(hz/viewLow)/math.Log(viewHigh/viewLow))
}

func (d *display) hzAt(x float32) float64 {
	return viewLow * math.Pow(viewHigh/viewLow, float64(x/max(d.size.W, 1)))
}

// specY is where on the display a level of the spectrum is: its foot
// is the bars' foot, so none of it stands behind the sections.
func (d *display) specY(db float32) float32 {
	t := (db - specBottom) / (specTop - specBottom)
	return d.line * (1 - max(0, min(t, 1)))
}

// bandsAt returns where each band runs across.
func (d *display) bandsAt() [bands][2]float32 {
	x := d.v.crossovers()
	var out [bands][2]float32
	from := float32(0)
	for b := range bands {
		to := d.size.W
		if b < bands-1 {
			to = d.xOf(x[b])
		}
		out[b] = [2]float32{from, to}
		from = to
	}
	return out
}

// middle is the line the bars stand on, and reach how far a bar runs
// from it, up or down, at a punch of all.
func (d *display) middle() float32 { return (d.line + d.top) / 2 }

func (d *display) reach() float32 { return (d.line - d.top) / 2 }

// Layout implements [gunim.Node].
func (d *display) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	d.size = c.Max
	if d.line == 0 {
		d.line, d.top = d.size.H*0.8, d.size.H*0.2
	}
	return c.Max
}

// Paint implements [gunim.Node].
func (d *display) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink, accent := widget.Ink.Get(th), widget.Accent.Get(th)
	radius := widget.CardRadius.Get(th)
	whole := geom.Rect{Max: box.Point()}
	p.RRect(whole, radius, paint.Solid(audioui.Ground.Get(th)))
	end := p.Layer(paint.LayerOpts{Bounds: whole, Opacity: 1, Clip: true, Radius: radius})
	defer end()
	for _, hz := range []float64{50, 100, 200, 500, 1000, 2000, 5000, 10000} {
		x := d.xOf(hz)
		p.RRect(geom.Rc(x, 0, 1, box.H), 0, paint.Solid(audioui.Faded(ink, 0.06)))
		label := audioui.Shaped(hz2(hz), 9, false, true)
		label.Paint(p, geom.Pt(x+4, 8), audioui.Faded(ink, 0.35))
	}
	// The sound going out, filled.
	n := len(d.freqs)
	step := math.Pow(viewHigh/viewLow, 0.5/float64(n-1))
	for i, fz := range d.freqs {
		x0, x1 := d.xOf(float64(fz)/step), d.xOf(float64(fz)*step)
		if y := d.specY(d.isSm[i]); y < d.line {
			p.RRect(geom.Rc(x0, y, x1-x0+0.5, d.line-y), 0, paint.Solid(audioui.Faded(accent, 0.14)))
		}
	}
	plugui.Darken(p, whole, 1)
	lines := p.Layer(paint.LayerOpts{Bounds: whole, Opacity: plugui.Lines(1)})
	defer lines()
	var prev geom.Point
	for i, fz := range d.freqs {
		o := geom.Pt(d.xOf(float64(fz)), d.specY(d.isSm[i]))
		if i > 0 {
			audioui.Segment(p, prev, o, 1.5, audioui.Faded(accent, 0.75))
		}
		prev = o
	}
	// The bands: each band's bar, from the middle, glowing as the band
	// is turned; then the crossovers.
	mid := d.middle()
	p.RRect(geom.Rc(0, mid, box.W, 1), 0, paint.Solid(audioui.Faded(ink, 0.18)))
	for b, span := range d.bandsAt() {
		v := d.v.punch(b)
		c := punchColor(th, v)
		tip := mid - float32(v/100)*d.reach()
		r := geom.Rc(span[0]+3, min(tip, mid), span[1]-span[0]-6, float32(math.Abs(float64(tip-mid))))
		glow, lit := d.glow[b].Value(), d.bandLit[b].Value()
		fill := 0.14 + 0.3*glow + 0.12*lit
		if r.Size().H > 0.5 {
			if glow > 0.02 {
				p.ShadowRRect(r, 4, paint.Solid(audioui.Faded(c, 0)), paint.Shadow{Blur: 18 * glow, Color: audioui.Faded(c, 0.5*glow)})
			}
			p.RRect(r, 4, paint.Solid(audioui.Faded(c, fill)))
		}
		p.RRect(geom.Rc(r.Min.X, tip-1-lit, r.Size().W, 2+2*lit), 1+lit, paint.Solid(audioui.Faded(c, 0.5+0.5*max(glow, lit))))
		label := audioui.Shaped(punch(v), 11, true, false)
		if label.Advance < span[1]-span[0]-8 {
			y := tip - 18
			if v < 0 {
				y = tip + 6
			}
			label.Paint(p, geom.Pt((span[0]+span[1]-label.Advance)/2, y), c)
		}
	}
	for i, hz := range d.v.crossovers() {
		x := d.xOf(hz)
		lit := d.xLit[i].Value()
		a := 0.35 + 0.55*lit
		w, r := 1.5+lit, 5+2*lit
		p.RRect(geom.Rc(x-w/2, 0, w, box.H), 0, paint.Solid(audioui.Faded(ink, a)))
		if lit > 0.01 {
			p.ShadowRRect(geom.Rc(x-r, mid-r, 2*r, 2*r), r, paint.Solid(audioui.Faded(ink, 0)),
				paint.Shadow{Blur: 10 * lit, Color: audioui.Faded(ink, 0.4*lit)})
		}
		p.RRect(geom.Rc(x-r, mid-r, 2*r, 2*r), r, paint.Solid(audioui.Faded(ink, a)))
	}
}

// light lights what the pointer is on, gliding.
func (d *display) light(x, b int) {
	d.hotX, d.hotBand = x, b
	for i, l := range d.xLit {
		l.Animate(float32(onOff(i == x)), anim.Snappy)
	}
	for i, l := range d.bandLit {
		l.Animate(float32(onOff(i == b)), anim.Snappy)
	}
}

func onOff(b bool) int {
	if b {
		return 1
	}
	return 0
}

// hz2 writes a frequency on the grid, short.
func hz2(hz float64) string {
	if hz >= 1000 {
		return fmt.Sprintf("%.0fk", hz/1000)
	}
	return fmt.Sprintf("%.0f", hz)
}

// hit returns the crossover near pos, or -1, and else the band whose
// bar pos is over, above the foot of the bars' room, or -1: under it,
// where the controls stand and the gaps between them, no band.
func (d *display) hit(pos geom.Point) (crossover, band int) {
	for i, hz := range d.v.crossovers() {
		if math.Abs(float64(pos.X-d.xOf(hz))) < 6 {
			return i, -1
		}
	}
	if pos.Y > d.line+6 {
		return -1, -1
	}
	for b, span := range d.bandsAt() {
		if pos.X >= span[0] && pos.X < span[1] {
			return -1, b
		}
	}
	return -1, -1
}

// Handle implements [gunim.Handler]: a crossover drags across, a band's
// bar up and down, each drag one gesture; a double-click on a band sets
// its punch back to none.
func (d *display) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		if d.held != holdNone {
			d.drag(e.Pos, e.Mods.Has(input.ModShift), u)
			break
		}
		d.light(d.hit(e.Pos))
	case input.PointerLeave:
		if d.held == holdNone {
			d.light(-1, -1)
		}
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		x, b := d.hit(e.Pos)
		switch {
		case x >= 0:
			d.held, d.hotX, d.heldFrom = holdCrossover, x, e.Pos
			d.h.Begin(CrossoverID+uint32(x), u)
		case b >= 0 && e.Clicks == 2:
			id := PunchID + uint32(b)
			d.h.Begin(id, u)
			d.h.Set(id, 0, u)
			d.h.End(id, u)
			d.v[id] = 0
		case b >= 0:
			d.held, d.hotBand, d.heldFrom, d.heldWas = holdBand, b, e.Pos, d.v.punch(b)
			d.h.Begin(PunchID+uint32(b), u)
		default:
			return false
		}
	case input.PointerUp:
		switch d.held {
		case holdCrossover:
			d.h.End(CrossoverID+uint32(d.hotX), u)
		case holdBand:
			d.h.End(PunchID+uint32(d.hotBand), u)
		default:
			return false
		}
		d.held = holdNone
		d.light(d.hit(e.Pos))
	default:
		return false
	}
	u.Invalidate()
	return true
}

// drag moves what is held to the pointer at pos: a crossover to the
// frequency there, a band's punch by how far up, or down, the pointer
// has gone, finer with Shift.
func (d *display) drag(pos geom.Point, fine bool, u *gunim.UI) {
	switch d.held {
	case holdCrossover:
		id := CrossoverID + uint32(d.hotX)
		v := param(id).Clamp(d.hzAt(pos.X))
		d.v[id] = v
		d.h.Set(id, v, u)
	case holdBand:
		id := PunchID + uint32(d.hotBand)
		per := 100 / max(d.reach(), 1)
		if fine {
			per /= 10
		}
		v := param(id).Clamp(d.heldWas + float64((d.heldFrom.Y-pos.Y)*per))
		d.v[id] = v
		d.h.Set(id, v, u)
	}
}

// Access implements [gunim.Accessible].
func (d *display) Access() access.Info {
	var v strings.Builder
	for b := range bands {
		fmt.Fprintf(&v, "%s %s. ", bandNames[b], punch(d.v.punch(b)))
	}
	return access.Info{Role: access.RoleGroup, Name: "Bands", Value: v.String()}
}

// panel is the display filling the panel, and along its foot the
// sections: the crossovers, each band's punch, and the length, the mix
// and the output.
type panel struct {
	v        values
	disp     *display
	sections []*plugui.Section
	tuner    *plugui.Tuner
}

// The knobs' tooltips.
const (
	punchTip  = "Above zero, each hit in the band stands out more against what follows it; below, it is smoothed into it"
	lengthTip = "About how long a hit lasts: the punch works on changes over about this long"
	mixTip    = "How much of the change is heard: none leaves the sound as the bands split it"
	outputTip = "The level out, after the bands"
	splitTip  = "Where the bands part: drag the lines on the display too"
)

func newPanel(h plug.Host) *panel {
	v := valuesOf(h)
	p := &panel{v: v, disp: newDisplay(h, v), tuner: plugui.NewTuner("Tune how the sound shows behind the controls")}
	xs := plugui.NewSection(h, &Descriptor, "CROSSOVERS", 0, CrossoverID, CrossoverID+1, CrossoverID+2)
	for _, k := range xs.Knobs {
		k.Tooltip = splitTip
	}
	p.sections = append(p.sections, xs)
	for b := range bands {
		s := plugui.NewSection(h, &Descriptor, bandNames[b], 0, PunchID+uint32(b))
		s.Knobs[0].Tooltip = punchTip
		p.sections = append(p.sections, s)
	}
	out := plugui.NewSection(h, &Descriptor, "OUTPUT", 0, LengthID, MixID, OutputID)
	out.Knobs[0].Tooltip, out.Knobs[1].Tooltip, out.Knobs[2].Tooltip = lengthTip, mixTip, outputTip
	p.sections = append(p.sections, out)
	return p
}

// Children implements [gunim.Composite].
func (p *panel) Children() []gunim.Node {
	kids := make([]gunim.Node, 0, 3+len(p.sections))
	kids = append(kids, p.disp)
	for _, s := range p.sections {
		kids = append(kids, s)
	}
	return append(kids, p.tuner.Button, p.tuner.Card)
}

// panelInset is how far in from the panel's edges the glass stands.
const panelInset = 16

// Layout implements [gunim.Node]: the display fills the panel; the
// sections along its foot; the tuner at its top right; and the bars'
// room between the top and the sections.
func (p *panel) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	kids.At(0).Layout(gunim.Tight(size))
	kids.At(0).Place(geom.Point{})
	top := plugui.PlaceSections(size, panelInset, p.sections, func(i int, r geom.Rect) {
		kids.At(1 + i).Layout(gunim.Tight(r.Size()))
		kids.At(1 + i).Place(r.Min)
	})
	p.disp.line = top - plugui.SectionGap - 12
	p.disp.top = min(panelInset+48, p.disp.line-40)
	n := 1 + len(p.sections)
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

// Follow implements [plug.Follower]: the knobs glide to their
// parameters, unless held, and the bands move.
func (p *panel) Follow(id uint32, v float64, u *gunim.UI) {
	p.v[id] = v
	for _, s := range p.sections {
		s.Follow(id, v, u)
	}
	u.Invalidate()
}

// chip shows, in a plugin's chip in a chain, each band's punch as a bar
// from a line across its middle, up as it adds punch and down as it
// smooths, and, as the sound plays, how far the band is turned, a
// brighter bar growing in it.
type chip struct {
	v       values
	changes *changeOf
	shown   [bands]*anim.Float
	group   anim.Group
	size    geom.Size
}

func newChip(h plug.Host) *chip {
	c := &chip{v: valuesOf(h), changes: newChangeOf(h)}
	for k := range c.shown {
		c.shown[k] = anim.NewFloat(0)
		c.group.Add(c.shown[k])
	}
	return c
}

// Layout implements [gunim.Node].
func (c *chip) Layout(cs gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	c.size = cs.Max
	return cs.Max
}

// Step implements [gunim.Animator]: the bars follow how far each band
// is turned.
func (c *chip) Step(dt time.Duration) bool {
	playing := c.changes.read(dt)
	for k, s := range c.shown {
		s.Animate(live(c.changes.db[k]), anim.Snappy)
	}
	return c.group.Step(dt) || playing
}

// Paint implements [gunim.Node].
func (c *chip) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink := widget.Ink.Get(th)
	w := min(box.W/bands, 12)
	mid, half := box.H/2, box.H/2-1
	p.RRect(geom.Rc(0, mid-0.5, w*bands, 1), 0, paint.Solid(audioui.Faded(ink, 0.25)))
	// bar draws a bar from the middle, h tall, up, or down where h is
	// under 0.
	bar := func(x, h float32, col color.NRGBA) {
		top := min(mid, mid-h)
		p.RRect(geom.Rc(x, top, w-2, max(float32(math.Abs(float64(h))), 1)), 1, paint.Solid(col))
	}
	for b := range bands {
		v := c.v.punch(b)
		col := punchColor(th, v)
		x := float32(b)*w + 1
		a := plugui.ChipScale(v, 100)
		if a == 0 {
			continue
		}
		bar(x, a*half, audioui.Faded(col, 0.3))
		if s := c.shown[b].Value() * half; s > 0.5 {
			if a < 0 {
				s = -s
			}
			bar(x, s, col)
		}
	}
}

// Access implements [gunim.Accessible].
func (c *chip) Access() access.Info {
	var v strings.Builder
	for b := range bands {
		fmt.Fprintf(&v, "%s %s ", bandNames[b], punch(c.v.punch(b)))
	}
	return access.Info{Role: access.RoleLabel, Name: v.String()}
}

// Follow implements [plug.Follower].
func (c *chip) Follow(id uint32, v float64, u *gunim.UI) {
	c.v[id] = v
	u.Invalidate()
}
