package enhancer

import (
	"fmt"
	"image/color"
	"math"
	"math/cmplx"
	"strconv"
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

// The styles' colours; tape's is the theme's accent.
var (
	EnhancerTube  = theme.Color("enhancer.tube", color.NRGBA{R: 0xff, G: 0xa8, B: 0x5a, A: 0xff})
	EnhancerWarm  = theme.Color("enhancer.warm", color.NRGBA{R: 0xff, G: 0xd1, B: 0x66, A: 0xff})
	EnhancerCrisp = theme.Color("enhancer.crisp", color.NRGBA{R: 0x8a, G: 0xd4, B: 0xff, A: 0xff})
)

// styleColor is style s's colour.
func styleColor(th *theme.Live, s int) color.NRGBA {
	switch s {
	case Tube:
		return EnhancerTube.Get(th)
	case Warm:
		return EnhancerWarm.Get(th)
	case Crisp:
		return EnhancerCrisp.Get(th)
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

// liveLow and liveHigh are the added levels that show as nothing, and
// as all, as a band plays: a mastering engineer adds little, so a little
// shows.
const (
	liveLow  = -60
	liveHigh = -12
)

// live is how far an added level db shows, from 0 to 1.
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

// style is band b's style.
func (v values) style(b int) int { return int(math.Round(v[StyleID+uint32(b)])) }

// bandOf returns the band parameter id is of, or -1.
func bandOf(id uint32) int {
	for _, from := range []uint32{AmountID, StyleID} {
		if id >= from && id < from+bands {
			return int(id - from)
		}
	}
	return -1
}

// addedOf reads how much each band adds as the sound plays, from the
// processor's traces, over the frames heard since it last read.
type addedOf struct {
	h  plug.Host
	l  plug.Listener
	db [bands]float32
}

func newAddedOf(h plug.Host) *addedOf {
	_, out := h.Sound()
	a := &addedOf{h: h, l: plug.Listener{Sound: out}}
	a.quiet()
	return a
}

func (a *addedOf) quiet() {
	for k := range a.db {
		a.db[k] = addedFloor
	}
}

// read reads it, a frame of the window, dt, on, and reports whether the
// sound plays.
func (a *addedOf) read(dt time.Duration) bool {
	frames, _, playing := a.l.Hear(4096, dt)
	switch {
	case len(frames) > 0:
		end := a.l.At()
		for k := range a.db {
			a.db[k] = addedFloor
			for f := end - int64(len(frames)/2); f < end; f += plug.TraceFrames {
				if v, ok := a.h.Traced(AddedID+uint32(k), f); ok {
					a.db[k] = max(a.db[k], -v)
				}
			}
		}
	case !playing:
		a.quiet()
	}
	return playing
}

// display is the sound by pitch as it goes out, and what the enhancer
// adds to it, the sound out less the sound in, in the colour of the
// style of the band each pitch is in; and the bands over it: the
// crossovers, to drag across, and each band's amount, a bar up from the
// foot in its style's colour, glowing as the band adds, to drag up and
// down, double-clicked back to none.
type display struct {
	h           plug.Host
	v           values
	in, out     plug.Sound
	frames, ins []float32
	meter       *audioui.Spectrometer
	freqs       []float32
	// is and add are the levels measured, of the sound going out and of
	// what is added, and isSm and addSm as drawn, easing to them; at is
	// the frame heard last, still how long nothing new came.
	is, add, isSm, addSm []float32
	at                   int64
	still                time.Duration
	added                *addedOf
	glow                 [bands]*anim.Float
	group                anim.Group
	// hot is what the pointer is on: a crossover, hotX, from 0, or a
	// band's bar, hotBand, or neither; xLit and bandLit are how lit each
	// is, gliding; onBand hears of each band the pointer comes onto.
	// held is what is dragged.
	hotX, hotBand int
	xLit          [bands - 1]*anim.Float
	bandLit       [bands]*anim.Float
	onBand        func(b int)
	held          int
	heldFrom      geom.Point
	heldWas       float64
	// line is where the bars stand, and top how high they reach, as the
	// panel lays them out.
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
	in, out := h.Sound()
	d := &display{h: h, v: v, in: in, out: out, frames: make([]float32, 2*specFrames), ins: make([]float32, 2*specFrames),
		meter: audioui.NewSpectrometer(specFrames), added: newAddedOf(h), hotX: -1, hotBand: -1, still: plug.StillAfter}
	d.freqs = make([]float32, specPoints)
	for i := range d.freqs {
		d.freqs[i] = float32(viewLow * math.Pow(viewHigh/viewLow, float64(i)/(specPoints-1)))
	}
	for _, p := range []*[]float32{&d.is, &d.add, &d.isSm, &d.addSm} {
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

// measure measures what the sound last brought, and what the enhancer
// added to it, where the sound in can be heard up to the same frame, and
// reports whether it brought anything new. The sound plays on as it is
// read, so the two are read again, a few times, until they agree.
func (d *display) measure() bool {
	if d.out == nil {
		return false
	}
	var n, inN int
	var at, inAt int64
	var rate int
	for range 3 {
		n, at, rate = d.out.Heard(d.frames)
		if d.in == nil {
			break
		}
		if inN, inAt, _ = d.in.Heard(d.ins); inAt == at {
			break
		}
	}
	if at == 0 || n != specFrames || at == d.at {
		return false
	}
	d.at = at
	d.meter.Measure(d.frames, rate, d.freqs, d.is)
	if d.in != nil && inN == specFrames && inAt == at {
		for i := range d.ins {
			d.ins[i] = d.frames[i] - d.ins[i]
		}
		d.meter.Measure(d.ins, rate, d.freqs, d.add)
	}
	return true
}

// Step implements [gunim.Animator]: the spectra measured anew as the
// sound brings more, held between its chunks, falling once it stops;
// and each band glowing as it adds.
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
			d.is[i], d.add[i] = specBottom-20, specBottom-20
		}
	}
	adding := d.added.read(dt)
	for k, g := range d.glow {
		g.Animate(live(d.added.db[k]), anim.Snappy)
	}
	sec := float32(dt.Seconds())
	settled := true
	ease := func(v *float32, to float32) {
		rate := float32(30)
		if to < *v {
			rate = 6
		}
		*v += (to - *v) * min(1, rate*sec)
		if math.Abs(float64(to-*v)) > 0.3 {
			settled = false
		}
	}
	for i := range d.is {
		ease(&d.isSm[i], d.is[i])
		ease(&d.addSm[i], d.add[i])
	}
	return d.group.Step(dt) || playing || adding || !settled
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
// is the bars' line, so none of it stands behind the sections.
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

// bandAt returns the band frequency hz is in.
func (d *display) bandAt(hz float64) int {
	for b, x := range d.v.crossovers() {
		if hz < x {
			return b
		}
	}
	return bands - 1
}

// reach is how far a band's bar runs at its full amount.
func (d *display) reach() float32 { return d.line - d.top }

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
	// The sound going out, filled; and what the enhancer adds, filled
	// over it, in the colour of the style of the band each pitch is in.
	n := len(d.freqs)
	step := math.Pow(viewHigh/viewLow, 0.5/float64(n-1))
	for i, fz := range d.freqs {
		x0, x1 := d.xOf(float64(fz)/step), d.xOf(float64(fz)*step)
		if y := d.specY(d.isSm[i]); y < d.line {
			p.RRect(geom.Rc(x0, y, x1-x0+0.5, d.line-y), 0, paint.Solid(audioui.Faded(accent, 0.14)))
		}
		if y := d.specY(d.addSm[i]); y < d.line {
			c := styleColor(th, d.v.style(d.bandAt(float64(fz))))
			p.RRect(geom.Rc(x0, y, x1-x0+0.5, d.line-y), 0, paint.Solid(audioui.Faded(c, 0.3)))
		}
	}
	plugui.Darken(p, whole, 1)
	lines := p.Layer(paint.LayerOpts{Bounds: whole, Opacity: plugui.Lines(1)})
	defer lines()
	var prevO, prevA geom.Point
	for i, fz := range d.freqs {
		x := d.xOf(float64(fz))
		o, ad := geom.Pt(x, d.specY(d.isSm[i])), geom.Pt(x, d.specY(d.addSm[i]))
		if i > 0 {
			audioui.Segment(p, prevO, o, 1.5, audioui.Faded(accent, 0.75))
			if ad.Y < d.line || prevA.Y < d.line {
				c := styleColor(th, d.v.style(d.bandAt(float64(fz))))
				audioui.Segment(p, prevA, ad, 1.5, c)
			}
		}
		prevO, prevA = o, ad
	}
	// The bands: each band's bar, glowing as it adds, and the crossovers.
	p.RRect(geom.Rc(0, d.line, box.W, 1), 0, paint.Solid(audioui.Faded(ink, 0.18)))
	for b, span := range d.bandsAt() {
		c := styleColor(th, d.v.style(b))
		top := d.line - float32(d.v[AmountID+uint32(b)]/100)*d.reach()
		r := geom.Rc(span[0]+3, top, span[1]-span[0]-6, d.line-top)
		glow, lit := d.glow[b].Value(), d.bandLit[b].Value()
		fill := 0.14 + 0.3*glow + 0.12*lit
		if r.Size().H > 0.5 {
			if glow > 0.02 {
				p.ShadowRRect(r, 4, paint.Solid(audioui.Faded(c, 0)), paint.Shadow{Blur: 18 * glow, Color: audioui.Faded(c, 0.5*glow)})
			}
			p.RRect(r, 4, paint.Solid(audioui.Faded(c, fill)))
		}
		p.RRect(geom.Rc(r.Min.X, top-1-lit, r.Size().W, 2+2*lit), 1+lit, paint.Solid(audioui.Faded(c, 0.5+0.5*max(glow, lit))))
		words := fmt.Sprintf("%s  ·  %s", pct(d.v[AmountID+uint32(b)]), styleNames[d.v.style(b)])
		label := audioui.Shaped(words, 11, true, false)
		if label.Advance < span[1]-span[0]-8 {
			label.Paint(p, geom.Pt((span[0]+span[1]-label.Advance)/2, top-18), c)
		}
	}
	for i, hz := range d.v.crossovers() {
		x := d.xOf(hz)
		lit := d.xLit[i].Value()
		a := 0.35 + 0.55*lit
		w, r := 1.5+lit, 5+2*lit
		p.RRect(geom.Rc(x-w/2, 0, w, box.H), 0, paint.Solid(audioui.Faded(ink, a)))
		if lit > 0.01 {
			p.ShadowRRect(geom.Rc(x-r, d.line-r, 2*r, 2*r), r, paint.Solid(audioui.Faded(ink, 0)),
				paint.Shadow{Blur: 10 * lit, Color: audioui.Faded(ink, 0.4*lit)})
		}
		p.RRect(geom.Rc(x-r, d.line-r, 2*r, 2*r), r, paint.Solid(audioui.Faded(ink, a)))
	}
}

// light lights what the pointer is on, gliding, and tells of a band's
// bar it comes onto.
func (d *display) light(x, b int) {
	if b >= 0 && b != d.hotBand && d.onBand != nil {
		d.onBand(b)
	}
	d.hotX, d.hotBand = x, b
	for i, l := range d.xLit {
		l.Animate(float32(onOff(i == x)), anim.Snappy)
	}
	for i, l := range d.bandLit {
		l.Animate(float32(onOff(i == b)), anim.Snappy)
	}
}

// hz2 writes a frequency on the grid, short.
func hz2(hz float64) string {
	if hz >= 1000 {
		return fmt.Sprintf("%.0fk", hz/1000)
	}
	return fmt.Sprintf("%.0f", hz)
}

// hit returns the crossover near pos, or -1, and else the band whose
// bar pos is over, from the line its bars stand on up, or -1: under the
// line, where the controls stand and the gaps between them, no band.
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
// its amount back to none.
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
			id := AmountID + uint32(b)
			d.h.Begin(id, u)
			d.h.Set(id, 0, u)
			d.h.End(id, u)
			d.v[id] = 0
		case b >= 0:
			d.held, d.hotBand, d.heldFrom, d.heldWas = holdBand, b, e.Pos, d.v[AmountID+uint32(b)]
			d.h.Begin(AmountID+uint32(b), u)
		default:
			return false
		}
	case input.PointerUp:
		switch d.held {
		case holdCrossover:
			d.h.End(CrossoverID+uint32(d.hotX), u)
		case holdBand:
			d.h.End(AmountID+uint32(d.hotBand), u)
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
// frequency there, a band's amount by how far up the pointer has gone,
// finer with Shift.
func (d *display) drag(pos geom.Point, fine bool, u *gunim.UI) {
	switch d.held {
	case holdCrossover:
		id := CrossoverID + uint32(d.hotX)
		v := param(id).Clamp(d.hzAt(pos.X))
		d.v[id] = v
		d.h.Set(id, v, u)
	case holdBand:
		id := AmountID + uint32(d.hotBand)
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
		fmt.Fprintf(&v, "%s %s %s. ", bandNames[b], pct(d.v[AmountID+uint32(b)]), styleNames[d.v.style(b)])
	}
	return access.Info{Role: access.RoleGroup, Name: "Bands", Value: v.String()}
}

// harmonicsShown is how many harmonics the curve's pane shows, from the
// second; harmonicsTone is the level of the tone it shows them for, and
// harmonicsFloor the level at the foot of their bars.
const (
	harmonicsShown = 7
	harmonicsTone  = 0.5
	harmonicsFloor = -80
)

// blendOf returns, for a band at amount a whose style is the styles
// mixed by w, its whole curve at x: x and what the curves add to it. The
// curve's pane mixes them as one style gives way to another.
func blendOf(w [styles]float64, a, x float64) float64 {
	g := math.Exp(a * driveDB / 20 * math.Ln10)
	add := 0.0
	for s, ws := range w {
		if ws != 0 {
			add += ws * (curve(s, g*x)/g - x)
		}
	}
	return x + a*add
}

// harmonicsOf returns the level of each harmonic, from the second, that
// style s at amount a adds to a tone at harmonicsTone, in decibels under
// the tone.
func harmonicsOf(s int, a float64) [harmonicsShown]float64 {
	var w [styles]float64
	w[s] = 1
	return harmonicsBlend(w, a)
}

// harmonicsBlend is harmonicsOf for styles mixed by w.
func harmonicsBlend(w [styles]float64, a float64) [harmonicsShown]float64 {
	const n = 256
	var sum [harmonicsShown + 2]complex128
	for i := range n {
		ph := 2 * math.Pi * float64(i) / n
		y := blendOf(w, a, harmonicsTone*math.Sin(ph))
		for k := 2; k < len(sum); k++ {
			sum[k] += complex(y, 0) * cmplx.Exp(complex(0, -float64(k)*ph))
		}
	}
	var out [harmonicsShown]float64
	for k := range out {
		amp := 2 * cmplx.Abs(sum[k+2]) / n
		out[k] = max(20*math.Log10(amp/harmonicsTone+1e-12), harmonicsFloor)
	}
	return out
}

// curvePane shows, on glass, the curve of the band the panel looks at:
// the curve a sound through it bends along, against the straight line
// it would follow untouched; and beside it the harmonics it adds to a
// tone, a bar each. As the band, its amount or its style changes, the
// curve and the bars glide to the new ones, the colour with them, and
// the title gives way to the new one.
type curvePane struct {
	v    values
	band int
	// amount and weight are the amount and how much of each style the
	// pane draws, gliding; shownBand the band they glide to.
	amount    *anim.Float
	weight    [styles]*anim.Float
	shownBand int
	// title is the title, and was the one before, giving way as swap
	// goes from 0 to 1.
	title, was string
	swap       *anim.Float
	harm       [harmonicsShown]float64
	harmOf     [styles + 1]float32
	group      anim.Group
	size       geom.Size
}

func newCurvePane(v values, band int) *curvePane {
	c := &curvePane{v: v, band: band, shownBand: band, swap: anim.NewFloat(1),
		amount: anim.NewFloat(float32(v[AmountID+uint32(band)] / 100))}
	c.group.Add(c.amount, c.swap)
	for s := range c.weight {
		c.weight[s] = anim.NewFloat(float32(onOff(s == v.style(band))))
		c.group.Add(c.weight[s])
	}
	c.title = c.titleOf()
	return c
}

// titleOf is the title of the band the pane looks at.
func (c *curvePane) titleOf() string {
	return fmt.Sprintf("%s  ·  %s  ·  %s", bandNames[c.band], styleNames[c.v.style(c.band)],
		pct(c.v[AmountID+uint32(c.band)]))
}

// Step implements [gunim.Animator]: the curve glides to the band's.
func (c *curvePane) Step(dt time.Duration) bool {
	sp := anim.Spring{Response: 0.3, Damping: 0.9}
	if to := float32(c.v[AmountID+uint32(c.band)] / 100); c.amount.Target() != to {
		c.amount.Animate(to, sp)
	}
	for s, w := range c.weight {
		if to := float32(onOff(s == c.v.style(c.band))); w.Target() != to {
			w.Animate(to, sp)
		}
	}
	if t := c.titleOf(); t != c.title {
		// Another band's title rises in; the same band's, its amount or
		// style changed, turns over at once, as it is being set.
		if c.band != c.shownBand {
			c.was = c.title
			c.swap.Jump(0)
			c.swap.Animate(1, sp)
		}
		c.title, c.shownBand = t, c.band
	}
	return c.group.Step(dt)
}

// weights are the styles' weights as the pane draws them now.
func (c *curvePane) weights() [styles]float64 {
	var w [styles]float64
	for s, a := range c.weight {
		w[s] = float64(min(max(a.Value(), 0), 1))
	}
	return w
}

// harmonics are the harmonics the pane draws now, worked out again only
// as the curve moves.
func (c *curvePane) harmonics(w [styles]float64, a float64) [harmonicsShown]float64 {
	var key [styles + 1]float32
	for s := range w {
		key[s] = float32(w[s])
	}
	key[styles] = float32(a)
	if key != c.harmOf || c.harmOf == ([styles + 1]float32{}) {
		c.harm, c.harmOf = harmonicsBlend(w, a), key
	}
	return c.harm
}

// The pane's size, and the room round its contents.
const (
	curveW   = 300
	curveH   = 150
	curvePad = 12
)

// Layout implements [gunim.Node].
func (c *curvePane) Layout(cs gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	c.size = cs.Max
	return cs.Max
}

// colourOf is the styles' colours mixed by w.
func colourOf(th *theme.Live, w [styles]float64) color.NRGBA {
	var r, g, b, a, sum float64
	for s, ws := range w {
		cl := styleColor(th, s)
		r, g, b, a, sum = r+ws*float64(cl.R), g+ws*float64(cl.G), b+ws*float64(cl.B), a+ws*float64(cl.A), sum+ws
	}
	if sum < 1e-6 {
		return styleColor(th, Tape)
	}
	return color.NRGBA{R: uint8(r / sum), G: uint8(g / sum), B: uint8(b / sum), A: uint8(a / sum)}
}

// Paint implements [gunim.Node].
func (c *curvePane) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink := widget.Ink.Get(th)
	w, a := c.weights(), float64(max(c.amount.Value(), 0))
	col := colourOf(th, w)
	plugui.PaintGlass(p, th, geom.Rect{Max: box.Point()}, widget.CardRadius.Get(th)+4)
	swap := min(max(c.swap.Value(), 0), 1)
	if swap < 0.99 && c.was != "" {
		was := audioui.Shaped(c.was, 10, true, false)
		was.Paint(p, geom.Pt(curvePad, curvePad-2-8*swap), audioui.Faded(ink, 0.7*(1-swap)))
	}
	title := audioui.Shaped(c.title, 10, true, false)
	title.Paint(p, geom.Pt(curvePad, curvePad-2+8*(1-swap)), audioui.Faded(ink, 0.7*swap))
	side := box.H - 2*curvePad - 18
	if side < 20 {
		return
	}
	sq := geom.Rc(curvePad, curvePad+18, side, side)
	ctr := sq.Center()
	half := side / 2
	p.RRect(geom.Rc(ctr.X-0.5, sq.Min.Y, 1, side), 0, paint.Solid(audioui.Faded(ink, 0.12)))
	p.RRect(geom.Rc(sq.Min.X, ctr.Y-0.5, side, 1), 0, paint.Solid(audioui.Faded(ink, 0.12)))
	audioui.Segment(p, geom.Pt(ctr.X-half, ctr.Y+half), geom.Pt(ctr.X+half, ctr.Y-half), 1, audioui.Faded(ink, 0.25))
	const steps = 64
	var prev geom.Point
	for i := range steps + 1 {
		x := -1 + 2*float64(i)/steps
		y := blendOf(w, a, x)
		pt := geom.Pt(ctr.X+float32(x)*half, ctr.Y-float32(max(min(y, 1), -1))*half)
		if i > 0 {
			audioui.Segment(p, prev, pt, 2, col)
		}
		prev = pt
	}
	// The harmonics, the second up, as tall as they are loud.
	bars := geom.Rc(sq.Max.X+curvePad, sq.Min.Y, box.W-sq.Max.X-2*curvePad, side-14)
	bw := bars.Size().W / harmonicsShown
	for k, lv := range c.harmonics(w, a) {
		t := float32(1 - lv/harmonicsFloor)
		h := t * bars.Size().H
		x := bars.Min.X + float32(k)*bw
		p.RRect(geom.Rc(x+2, bars.Max.Y-h, bw-4, h), 2, paint.Solid(audioui.Faded(col, 0.35+0.5*t)))
		num := audioui.Shaped(strconv.Itoa(k+2), 9, false, true)
		num.Paint(p, geom.Pt(x+(bw-num.Advance)/2, bars.Max.Y+3), audioui.Faded(ink, 0.45))
	}
}

// Access implements [gunim.Accessible].
func (c *curvePane) Access() access.Info {
	s := c.v.style(c.band)
	h := harmonicsOf(s, c.v[AmountID+uint32(c.band)]/100)
	return access.Info{Role: access.RoleGroup, Name: bandNames[c.band] + " curve",
		Value: fmt.Sprintf("%s, second harmonic %.0f dB, third %.0f dB", styleNames[s], h[0], h[1])}
}

// panel is the display filling the panel; over it, on glass, the mix
// and the output at the top left, the curve of the band last touched at
// the top right, and along the foot the sections: the crossovers, and
// each band's amount and style.
type panel struct {
	v        values
	disp     *display
	curve    *curvePane
	sections []*plugui.Section
	styles   [bands]*plugui.Selector
	output   *plugui.Section
	tuner    *plugui.Tuner
	// touched is the band last touched, which the curve shows while the
	// pointer is on no band.
	touched int
}

func newPanel(h plug.Host) *panel {
	v := valuesOf(h)
	p := &panel{v: v, disp: newDisplay(h, v), tuner: plugui.NewTuner("Tune how the sound shows behind the controls")}
	for b := range bands {
		if v[AmountID+uint32(b)] > v[AmountID+uint32(p.touched)] {
			p.touched = b
		}
	}
	p.curve = newCurvePane(v, p.touched)
	// The band the pointer comes onto, the curve shows, and keeps
	// showing as the pointer goes on elsewhere.
	p.disp.onBand = func(b int) {
		p.touched = b
		p.curve.band = b
	}
	p.sections = append(p.sections, plugui.NewSection(h, &Descriptor, "CROSSOVERS", 0,
		CrossoverID, CrossoverID+1, CrossoverID+2))
	for b := range bands {
		s := plugui.NewSection(h, &Descriptor, bandNames[b], 0, AmountID+uint32(b))
		s.Knobs[0].Tooltip = "How much the band's harmonics are added: none leaves it as it was"
		sel := plugui.NewSelector("Style", styleNames, styleTips)
		sel.Columns = 2
		sel.SetSelected(v.style(b), nil)
		id := StyleID + uint32(b)
		sel.OnPick = func(i int, u *gunim.UI) {
			h.Begin(id, u)
			h.Set(id, float64(i), u)
			h.End(id, u)
		}
		p.styles[b] = sel
		s.Add(sel, styleW)
		p.sections = append(p.sections, s)
	}
	p.output = plugui.NewSection(h, &Descriptor, "OUTPUT", 0, MixID, OutputID)
	p.output.Knobs[0].Tooltip = "How much of all the bands' harmonics are added"
	return p
}

// styleW is how wide a band's style selector stands: its four lamps two
// by two.
const styleW = 56

// Children implements [gunim.Composite].
func (p *panel) Children() []gunim.Node {
	kids := make([]gunim.Node, 0, 5+len(p.sections))
	kids = append(kids, p.disp, p.curve, p.output)
	for _, s := range p.sections {
		kids = append(kids, s)
	}
	return append(kids, p.tuner.Button, p.tuner.Card)
}

// panelInset is how far in from the panel's edges the glass stands.
const panelInset = 16

// Layout implements [gunim.Node]: the display fills the panel; the
// sections along its foot; the output at its top left, and the curve
// at its top right, beside the tuner; and the bars between them and the
// sections.
func (p *panel) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	kids.At(0).Layout(gunim.Tight(size))
	kids.At(0).Place(geom.Point{})
	top := plugui.PlaceSections(size, panelInset, p.sections, func(i int, r geom.Rect) {
		kids.At(3 + i).Layout(gunim.Tight(r.Size()))
		kids.At(3 + i).Place(r.Min)
	})
	kids.At(2).Layout(gunim.Tight(geom.Sz(p.output.Width(), plugui.SectionH)))
	kids.At(2).Place(geom.Pt(panelInset, panelInset))
	cw := min(curveW, size.W-p.output.Width()-2*panelInset-48-plugui.SectionGap)
	kids.At(1).Layout(gunim.Tight(geom.Sz(max(cw, 0), curveH)))
	kids.At(1).Place(geom.Pt(size.W-panelInset-40-cw, panelInset))
	p.disp.line = top - plugui.SectionGap - 12
	p.disp.top = min(panelInset+max(curveH, plugui.SectionH)+34, p.disp.line-40)
	n := 3 + len(p.sections)
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

// Follow implements [plug.Follower]: the knobs and selectors glide to
// their parameters, unless held, the bands move, and the curve turns to
// the band touched.
func (p *panel) Follow(id uint32, v float64, u *gunim.UI) {
	p.v[id] = v
	for _, s := range p.sections {
		s.Follow(id, v, u)
	}
	p.output.Follow(id, v, u)
	if b := bandOf(id); b >= 0 {
		p.touched, p.curve.band = b, b
		if id >= StyleID {
			p.styles[b].SetSelected(int(math.Round(v)), u)
		}
	}
	u.Invalidate()
}

// chip shows, in a plugin's chip in a chain, each band's amount as a
// bar in its style's colour, and, as the sound plays, how much it adds,
// a brighter bar rising in it.
type chip struct {
	v     values
	added *addedOf
	shown [bands]*anim.Float
	group anim.Group
	size  geom.Size
}

func newChip(h plug.Host) *chip {
	c := &chip{v: valuesOf(h), added: newAddedOf(h)}
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

// Step implements [gunim.Animator]: the bars follow what each band adds.
func (c *chip) Step(dt time.Duration) bool {
	playing := c.added.read(dt)
	for k, s := range c.shown {
		s.Animate(live(c.added.db[k]), anim.Snappy)
	}
	return c.group.Step(dt) || playing
}

// Paint implements [gunim.Node].
func (c *chip) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink := widget.Ink.Get(th)
	w := min(box.W/bands, 12)
	p.RRect(geom.Rc(0, box.H-1, w*bands, 1), 0, paint.Solid(audioui.Faded(ink, 0.25)))
	for b := range bands {
		col := styleColor(th, c.v.style(b))
		x := float32(b)*w + 1
		if a := plugui.ChipScale(c.v[AmountID+uint32(b)], 100); a > 0 {
			h := max(a*(box.H-1), 1)
			p.RRect(geom.Rc(x, box.H-1-h, w-2, h), 1, paint.Solid(audioui.Faded(col, 0.3)))
		}
		if h := c.shown[b].Value() * (box.H - 1); h > 0.5 {
			p.RRect(geom.Rc(x, box.H-1-h, w-2, h), 1, paint.Solid(col))
		}
	}
}

// Access implements [gunim.Accessible].
func (c *chip) Access() access.Info {
	var v strings.Builder
	for b := range bands {
		fmt.Fprintf(&v, "%s %s %s ", bandNames[b], pct(c.v[AmountID+uint32(b)]), styleNames[c.v.style(b)])
	}
	return access.Info{Role: access.RoleLabel, Name: v.String()}
}

// Follow implements [plug.Follower].
func (c *chip) Follow(id uint32, v float64, u *gunim.UI) {
	c.v[id] = v
	u.Invalidate()
}
