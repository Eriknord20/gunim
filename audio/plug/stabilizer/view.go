package stabilizer

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugui"
	"github.com/marrasen/gunim/audio/plug/tonal"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// The display's frequencies, from viewLow to viewHigh across, as the
// gain is traced; its spectrum's points, how many frames it measures,
// and the levels at its top and its foot.
const (
	viewLow, viewHigh = traceLow, traceHigh
	specPoints        = 160
	specFrames        = 4096
	specTop           = 0
	specBottom        = -84
)

// gainFull is the gain at the top of the display's gain, and its
// negative the gain at the foot; chipFull the gain that fills a chip.
const (
	gainFull = 12
	chipFull = 12
)

// values are the parameters as the views show them, by ID.
type values map[uint32]float64

func valuesOf(h plug.Host) values {
	v := values{}
	for _, p := range Descriptor.Params {
		v[p.ID] = h.Param(p.ID)
	}
	return v
}

// mode is the mode set.
func (v values) mode() int { return int(math.Round(v[ModeID])) }

// target is the target set.
func (v values) target() tonal.Target { return tonal.TargetAt(v[TargetID]).At(v[KneeID]) }

// span returns Low and High in order, as the processor takes them.
func (v values) span() (lo, hi float64) {
	return min(v[LowID], v[HighID]), max(v[LowID], v[HighID])
}

// speedParam is the speed's parameter as a view shows it in mode m: in
// seconds in Shape, how long the balance is heard over, and in
// milliseconds in Cut, how quickly a cut comes.
func speedParam(m int) plug.Param {
	p := param(SpeedID)
	p.Format = func(v float64) string { return seconds(speedTime(m, v)) }
	return p
}

// The knobs' tooltips, by mode, where they differ.
var (
	amountTips = [modes]string{
		"How far the balance is drawn toward the target: at 100% all the way, within the range",
		"How much of a resonance's rise over the pitches round it is cut: at 100% all of it, within the range",
	}
	speedTips = [modes]string{
		"How long the balance is heard over: slow follows the whole song, fast each passage",
		"How quickly a cut comes as a resonance rings out, and lets go after it",
	}
	rangeTips = [modes]string{
		"The most any pitch is lifted or cut",
		"The most any pitch is cut",
	}
	smoothTips = [modes]string{
		"How broad the lifts and cuts are, in octaves: broad is gentle, narrow follows the balance closely",
		"How wide the pitches round a resonance are, in octaves, that it is heard against",
	}
)

// gainsOf reads the gain the stabilizer applies as the sound plays,
// from the processor's traces, at the last frame heard: none while the
// sound is still.
type gainsOf struct {
	h  plug.Host
	l  plug.Listener
	db [traced]float32
}

func newGainsOf(h plug.Host) *gainsOf {
	_, out := h.Sound()
	return &gainsOf{h: h, l: plug.Listener{Sound: out}}
}

// read reads it, a frame of the window, dt, on, and reports whether the
// sound plays.
func (g *gainsOf) read(dt time.Duration) bool {
	frames, _, playing := g.l.Hear(4096, dt)
	switch {
	case len(frames) > 0:
		at := g.l.At() - 1
		for i := range g.db {
			if v, ok := g.h.Traced(gainTrace+uint32(i), at); ok {
				g.db[i] = v
			}
		}
	case !playing:
		g.db = [traced]float32{}
	}
	return playing
}

// display is the sound by pitch, as it goes out, filled, and as it
// came in, faintly; in Shape, the target, a line through the sound
// coming in at its mean from Low to High; the gain the stabilizer
// applies, filled from a line of none; and Low and High, handles to
// drag across, the span outside them shaded, where it does nothing.
type display struct {
	h       plug.Host
	v       values
	in, out plug.Sound
	// frames and ins are the frames heard, out and in, and meter what
	// measures them; freqs are the spectrum's points.
	frames, ins []float32
	meter       *audioui.Spectrometer
	freqs       []float32
	// is and was are the levels measured, of the sound going out and of
	// the sound coming in, and isSm and wasSm as drawn, easing to them;
	// at is the frame heard last, still how long nothing new came.
	is, was, isSm, wasSm []float32
	at                   int64
	still                time.Duration
	// gains reads the gain, and gainSm is it as drawn, easing to it.
	gains  *gainsOf
	gainSm [traced]float32
	// hot is the handle the pointer is on, Low or High's ID, or 0; lit
	// is how lit each is, gliding; held is the one dragged, or 0.
	hot, held uint32
	lit       [2]*anim.Float
	group     anim.Group
	// line is the spectrum's foot, over the sections, as the panel lays
	// them out.
	line float32
	size geom.Size
}

func newDisplay(h plug.Host, v values) *display {
	in, out := h.Sound()
	d := &display{h: h, v: v, in: in, out: out, frames: make([]float32, 2*specFrames),
		ins: make([]float32, 2*specFrames), meter: audioui.NewSpectrometer(specFrames), gains: newGainsOf(h),
		still: plug.StillAfter}
	d.freqs = make([]float32, specPoints)
	for i := range d.freqs {
		d.freqs[i] = float32(viewLow * math.Pow(viewHigh/viewLow, float64(i)/(specPoints-1)))
	}
	for _, p := range []*[]float32{&d.is, &d.was, &d.isSm, &d.wasSm} {
		*p = make([]float32, specPoints)
		for i := range *p {
			(*p)[i] = specBottom - 20
		}
	}
	for k := range d.lit {
		d.lit[k] = anim.NewFloat(0)
		d.group.Add(d.lit[k])
	}
	return d
}

// measure measures what the sound last brought, going out and coming
// in, and reports whether it brought anything new.
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
	if d.in != nil {
		if n, _, rate := d.in.Heard(d.ins); n == specFrames {
			d.meter.Measure(d.ins, rate, d.freqs, d.was)
		}
	}
	return true
}

// Step implements [gunim.Animator]: the spectra measured anew as the
// sound brings more, held between its chunks, falling once it stops;
// and the gain following the stabilizer's.
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
			d.is[i], d.was[i] = specBottom-20, specBottom-20
		}
	}
	gaining := d.gains.read(dt)
	sec := float32(dt.Seconds())
	settled := true
	ease := func(v *float32, to, up, down float32, near float64) {
		rate := up
		if to < *v {
			rate = down
		}
		*v += (to - *v) * min(1, rate*sec)
		if math.Abs(float64(to-*v)) > near {
			settled = false
		}
	}
	for i := range d.is {
		ease(&d.isSm[i], d.is[i], 30, 6, 0.3)
		ease(&d.wasSm[i], d.was[i], 30, 6, 0.3)
	}
	for i := range d.gainSm {
		ease(&d.gainSm[i], d.gains.db[i], 12, 12, 0.02)
	}
	return d.group.Step(dt) || playing || gaining || !settled
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
// is the line, so none of it stands behind the sections.
func (d *display) specY(db float32) float32 {
	t := (db - specBottom) / (specTop - specBottom)
	return d.line * (1 - max(0, min(t, 1)))
}

// zeroY is where the line of no gain runs, and gainY where a gain is,
// gainFull a third of the way from it to the top.
func (d *display) zeroY() float32 { return d.line * 0.5 }

func (d *display) gainY(db float32) float32 {
	return d.zeroY() - max(-1, min(db/gainFull, 1))*d.line/3
}

// gainAt is the gain drawn at hz, between the traced frequencies.
func (d *display) gainAt(hz float64) float32 {
	x := math.Log(hz/viewLow) / math.Log(viewHigh/viewLow) * (traced - 1)
	k := min(max(int(x), 0), traced-2)
	t := float32(min(max(x-float64(k), 0), 1))
	return d.gainSm[k]*(1-t) + d.gainSm[k+1]*t
}

// Layout implements [gunim.Node].
func (d *display) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	d.size = c.Max
	if d.line == 0 {
		d.line = d.size.H * 0.8
	}
	return c.Max
}

// Paint implements [gunim.Node].
func (d *display) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink, accent := widget.Ink.Get(th), widget.Accent.Get(th)
	cutColor := audioui.Over.Get(th)
	radius := widget.CardRadius.Get(th)
	whole := geom.Rect{Max: box.Point()}
	p.RRect(whole, radius, paint.Solid(audioui.Ground.Get(th)))
	end := p.Layer(paint.LayerOpts{Bounds: whole, Opacity: 1, Clip: true, Radius: radius})
	defer end()
	for _, hz := range []float64{50, 100, 200, 500, 1000, 2000, 5000, 10000} {
		x := d.xOf(hz)
		p.RRect(geom.Rc(x, 0, 1, box.H), 0, paint.Solid(audioui.Faded(ink, 0.06)))
		label := audioui.Shaped(gridHz(hz), 9, false, true)
		label.Paint(p, geom.Pt(x+4, 8), audioui.Faded(ink, 0.35))
	}
	for _, g := range []float32{-gainFull, -gainFull / 2, gainFull / 2, gainFull} {
		y := d.gainY(g)
		p.RRect(geom.Rc(0, y, box.W, 1), 0, paint.Solid(audioui.Faded(ink, 0.04)))
		label := audioui.Shaped(fmt.Sprintf("%+.0f", g), 9, false, true)
		label.Paint(p, geom.Pt(box.W-label.Advance-6, y-11), audioui.Faded(ink, 0.3))
	}
	// The sound going out, filled; and coming in, a faint line.
	n := len(d.freqs)
	step := math.Pow(viewHigh/viewLow, 0.5/float64(n-1))
	for i, fz := range d.freqs {
		x0, x1 := d.xOf(float64(fz)/step), d.xOf(float64(fz)*step)
		if y := d.specY(d.isSm[i]); y < d.line {
			p.RRect(geom.Rc(x0, y, x1-x0+0.5, d.line-y), 0, paint.Solid(audioui.Faded(accent, 0.12)))
		}
	}
	plugui.Darken(p, whole, 1)
	lines := p.Layer(paint.LayerOpts{Bounds: whole, Opacity: plugui.Lines(1)})
	defer lines()
	var prevO, prevI geom.Point
	for i, fz := range d.freqs {
		x := d.xOf(float64(fz))
		o, in := geom.Pt(x, d.specY(d.isSm[i])), geom.Pt(x, d.specY(d.wasSm[i]))
		if i > 0 {
			if in.Y < d.line || prevI.Y < d.line {
				audioui.Segment(p, prevI, in, 1, audioui.Faded(ink, 0.3))
			}
			audioui.Segment(p, prevO, o, 1.5, audioui.Faded(accent, 0.7))
		}
		prevO, prevI = o, in
	}
	if d.v.mode() == Shape {
		d.paintTarget(p, ink)
	}
	// Outside Low and High, where the stabilizer does nothing, shaded.
	lo, hi := d.v.span()
	shade := paint.Solid(color.NRGBA{A: 70})
	if x := d.xOf(lo); x > 0 {
		p.RRect(geom.Rc(0, 0, x, box.H), 0, shade)
	}
	if x := d.xOf(hi); x < box.W {
		p.RRect(geom.Rc(x, 0, box.W-x, box.H), 0, shade)
	}
	// The gain: lifts filled in the accent up from the line of none,
	// cuts in red down from it, and the curve over them.
	zero := d.zeroY()
	p.RRect(geom.Rc(0, zero, box.W, 1), 0, paint.Solid(audioui.Faded(ink, 0.25)))
	const strip = 3
	for x := float32(0); x < box.W; x += strip {
		g := d.gainAt(d.hzAt(x + strip/2))
		y := d.gainY(g)
		switch {
		case y < zero-0.25:
			p.RRect(geom.Rc(x, y, strip, zero-y), 0, paint.Solid(audioui.Faded(accent, 0.25)))
		case y > zero+0.25:
			p.RRect(geom.Rc(x, zero, strip, y-zero), 0, paint.Solid(audioui.Faded(cutColor, 0.25)))
		}
	}
	var prev geom.Point
	for i, g := range d.gainSm {
		pt := geom.Pt(d.xOf(traceFreqs[i]), d.gainY(g))
		if i > 0 {
			audioui.Segment(p, prev, pt, 2, accent)
		}
		prev = pt
	}
	// Low and High, each a line across the display with a knob on the
	// line of none.
	for k, id := range []uint32{LowID, HighID} {
		x := d.xOf(d.v[id])
		lit := d.lit[k].Value()
		a := 0.45 + 0.5*lit
		w, r := 1.5+lit, 5+2*lit
		p.RRect(geom.Rc(x-w/2, 0, w, d.line), 0, paint.Solid(audioui.Faded(ink, a)))
		if lit > 0.01 {
			p.ShadowRRect(geom.Rc(x-r, zero-r, 2*r, 2*r), r, paint.Solid(audioui.Faded(ink, 0)),
				paint.Shadow{Blur: 10 * lit, Color: audioui.Faded(ink, 0.4*lit)})
		}
		p.RRect(geom.Rc(x-r, zero-r, 2*r, 2*r), r, paint.Solid(audioui.Faded(ink, a)))
		label := audioui.Shaped(param(id).Name+"  "+hz(d.v[id]), 10, true, false)
		lx := x + 8
		if id == HighID {
			lx = x - 8 - label.Advance
		}
		label.Paint(p, geom.Pt(lx, 24), audioui.Faded(ink, 0.5+0.4*lit))
	}
}

// paintTarget draws the target, in the spectrum's terms, as the
// spectrum measures each bin: pink's fall of 3 dB an octave and the
// target's own bend with it, through the sound coming in at its mean
// from Low to High. Nothing shows until the sound has been heard.
func (d *display) paintTarget(p *paint.Painter, ink color.NRGBA) {
	lo, hi := d.v.span()
	t := d.v.target()
	shape := func(f float64) float64 { return t.Level(f) + pinkSlope*math.Log2(f) }
	var sum, shaped float64
	count := 0
	for i, fz := range d.freqs {
		if f := float64(fz); f >= lo && f <= hi && d.wasSm[i] > specBottom {
			sum += float64(d.wasSm[i])
			shaped += shape(f)
			count++
		}
	}
	if count < 2 {
		return
	}
	mean, mid := sum/float64(count), shaped/float64(count)
	at := func(f float64) geom.Point {
		return geom.Pt(d.xOf(f), d.specY(float32(mean+shape(f)-mid)))
	}
	c := audioui.Faded(ink, 0.55)
	// Dashed, so it reads as a guide, not a sound.
	const dashes = 40
	for i := 0; i < dashes; i += 2 {
		f0 := lo * math.Pow(hi/lo, float64(i)/dashes)
		f1 := lo * math.Pow(hi/lo, float64(i+1)/dashes)
		audioui.Segment(p, at(f0), at(f1), 1.5, c)
	}
	label := audioui.Shaped(t.Short, 10, true, false)
	end := at(hi)
	label.Paint(p, geom.Pt(end.X-label.Advance-4, end.Y-16), c)
}

// pinkSlope is how many decibels pink noise falls each octave up, as a
// spectrum measuring each bin reads it.
const pinkSlope = -3

// gridHz writes a frequency on the grid, short.
func gridHz(hz float64) string {
	if hz >= 1000 {
		return fmt.Sprintf("%.0fk", hz/1000)
	}
	return fmt.Sprintf("%.0f", hz)
}

// hit returns the handle near pos, Low's ID or High's, or 0: on the
// display, above the sections.
func (d *display) hit(pos geom.Point) uint32 {
	if pos.Y > d.line+6 {
		return 0
	}
	best, near := uint32(0), float32(8)
	for _, id := range []uint32{LowID, HighID} {
		if dx := float32(math.Abs(float64(pos.X - d.xOf(d.v[id])))); dx < near {
			best, near = id, dx
		}
	}
	return best
}

// light lights the handle id, or none, gliding.
func (d *display) light(id uint32) {
	d.hot = id
	for k, l := range d.lit {
		l.Animate(float32(onOff(id != 0 && id == LowID+uint32(k))), anim.Snappy)
	}
}

func onOff(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Handle implements [gunim.Handler]: Low and High drag across, each
// drag one gesture; a double-click sets one back to its default.
func (d *display) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		if d.held != 0 {
			v := param(d.held).Clamp(d.hzAt(e.Pos.X))
			d.v[d.held] = v
			d.h.Set(d.held, v, u)
			break
		}
		d.light(d.hit(e.Pos))
	case input.PointerLeave:
		if d.held == 0 {
			d.light(0)
		}
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		id := d.hit(e.Pos)
		switch {
		case id == 0:
			return false
		case e.Clicks == 2:
			v := param(id).Default
			d.h.Begin(id, u)
			d.h.Set(id, v, u)
			d.h.End(id, u)
			d.v[id] = v
		default:
			d.held = id
			d.light(id)
			d.h.Begin(id, u)
		}
	case input.PointerUp:
		if d.held == 0 {
			return false
		}
		d.h.End(d.held, u)
		d.held = 0
		d.light(d.hit(e.Pos))
	default:
		return false
	}
	u.Invalidate()
	return true
}

// Access implements [gunim.Accessible]: what it does, where, and the
// most it lifts and cuts now.
func (d *display) Access() access.Info {
	lo, hi := d.v.span()
	what := "cutting resonances"
	if d.v.mode() == Shape {
		what = "shaping toward " + d.v.target().Name
	}
	most, least, mostAt, leastAt := float32(0), float32(0), 0, 0
	for i, g := range d.gainSm {
		if g > most {
			most, mostAt = g, i
		}
		if g < least {
			least, leastAt = g, i
		}
	}
	v := fmt.Sprintf("%s from %s to %s. ", what, hz(lo), hz(hi))
	if most > 0.05 {
		v += fmt.Sprintf("Most lift %s at %s. ", db(float64(most)), hz(traceFreqs[mostAt]))
	}
	if least < -0.05 {
		v += fmt.Sprintf("Most cut %s at %s. ", db(float64(least)), hz(traceFreqs[leastAt]))
	}
	return access.Info{Role: access.RoleGroup, Name: "Gain", Value: v}
}

// selectorW is how wide a selector stands: its two lamps and its words.
const selectorW = 56

// panel is the display filling the panel, and along its foot the
// sections: the mode, the target, how it stabilizes, the span it works
// in, and the output.
type panel struct {
	v        values
	disp     *display
	mode     *plugui.Selector
	target   *plugui.Selector
	sections []*plugui.Section
	knobs    map[uint32]*plugui.Knob
	tuner    *plugui.Tuner
}

func newPanel(h plug.Host) *panel {
	v := valuesOf(h)
	p := &panel{v: v, disp: newDisplay(h, v), knobs: map[uint32]*plugui.Knob{},
		tuner: plugui.NewTuner("Tune how the sound shows behind the controls")}
	pick := func(id uint32) func(i int, u *gunim.UI) {
		return func(i int, u *gunim.UI) {
			h.Begin(id, u)
			h.Set(id, float64(i), u)
			h.End(id, u)
			p.Follow(id, float64(i), u)
		}
	}
	p.mode = plugui.NewSelector("Mode", modeNames, modeTips)
	p.mode.SetSelected(v.mode(), nil)
	p.mode.OnPick = pick(ModeID)
	p.target = plugui.NewSelector("Target", targetNames, targetTips)
	p.target.SetSelected(int(math.Round(v[TargetID])), nil)
	p.target.OnPick = pick(TargetID)
	target := plugui.NewSection(h, &Descriptor, "TARGET", 0, KneeID).Add(p.target, selectorW)
	stab := plugui.NewSection(h, &Descriptor, "STABILIZE", 0, AmountID, SpeedID, RangeID, SmoothID)
	span := plugui.NewSection(h, &Descriptor, "SPAN", 0, LowID, HighID)
	out := plugui.NewSection(h, &Descriptor, "OUTPUT", 0, OutputID)
	p.sections = []*plugui.Section{
		plugui.NewSection(h, &Descriptor, "MODE", 0).Add(p.mode, selectorW),
		target, stab, span, out,
	}
	for _, s := range []*plugui.Section{target, stab, span, out} {
		for _, k := range s.Knobs {
			p.knobs[k.Param().ID] = k
		}
	}
	p.knobs[LowID].Tooltip = "Where the stabilizer starts: under it, the gain fades to none within half an octave"
	p.knobs[HighID].Tooltip = "Where the stabilizer stops: over it, the gain fades to none within half an octave"
	p.knobs[OutputID].Tooltip = "The level of the sound coming out"
	p.knobs[KneeID].Tooltip = "Where the music slope stops running level and starts to fall 6 dB an octave"
	p.toMode(v.mode(), nil)
	return p
}

// toMode shows the controls as they work in mode m: the speed in its
// time, the tooltips its own, and the target dimmed in Cut, which has
// none.
func (p *panel) toMode(m int, u *gunim.UI) {
	m = min(max(m, 0), modes-1)
	k := p.knobs[SpeedID]
	k.SetParam(speedParam(m), k.Value(), u)
	k.Tooltip = speedTips[m]
	p.knobs[AmountID].Tooltip = amountTips[m]
	p.knobs[RangeID].Tooltip = rangeTips[m]
	p.knobs[SmoothID].Tooltip = smoothTips[m]
	p.target.Locked = m == Cut
	p.knobs[KneeID].Disabled = m == Cut || !p.v.target().Kneed
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

// Layout implements [gunim.Node]: the display fills the panel, the
// sections along its foot, and the tuner at its top right.
func (p *panel) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	kids.At(0).Layout(gunim.Tight(size))
	kids.At(0).Place(geom.Point{})
	top := plugui.PlaceSections(size, panelInset, p.sections, func(i int, r geom.Rect) {
		kids.At(1 + i).Layout(gunim.Tight(r.Size()))
		kids.At(1 + i).Place(r.Min)
	})
	p.disp.line = top - plugui.SectionGap - 12
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

// Follow implements [plug.Follower]: the knobs and selectors glide to
// their parameters, unless held; the controls turn to the mode.
func (p *panel) Follow(id uint32, v float64, u *gunim.UI) {
	p.v[id] = v
	for _, s := range p.sections {
		s.Follow(id, v, u)
	}
	switch id {
	case ModeID:
		p.mode.SetSelected(int(math.Round(v)), u)
		p.toMode(int(math.Round(v)), u)
	case TargetID:
		p.target.SetSelected(int(math.Round(v)), u)
		p.toMode(p.v.mode(), u)
	}
	u.Invalidate()
}

// chip shows, in a plugin's chip in a chain, the gain the stabilizer
// applies as the sound plays: a line of none, faint, and the gain over
// it, flat while the sound is still.
type chip struct {
	v     values
	gains *gainsOf
	shown [traced]*anim.Float
	group anim.Group
}

func newChip(h plug.Host) *chip {
	c := &chip{v: valuesOf(h), gains: newGainsOf(h)}
	for i := range c.shown {
		c.shown[i] = anim.NewFloat(0)
		c.group.Add(c.shown[i])
	}
	return c
}

// Layout implements [gunim.Node]: the chip takes the room it is given.
func (c *chip) Layout(cs gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return cs.Max
}

// Step implements [gunim.Animator]: the curve follows the gain.
func (c *chip) Step(dt time.Duration) bool {
	playing := c.gains.read(dt)
	for i, s := range c.shown {
		if to := c.gains.db[i]; s.Target() != to {
			s.Animate(to, anim.Snappy)
		}
	}
	return c.group.Step(dt) || playing
}

// Paint implements [gunim.Node]: the line of none, faint, and the gain
// over it, from -chipFull at the foot to chipFull at the top.
func (c *chip) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink, accent := widget.Ink.Get(th), widget.Accent.Get(th)
	const width = 2
	mid := box.H / 2
	half := max(mid-width, 1)
	p.RRect(geom.Rc(0, mid, box.W, 1), 0, paint.Solid(audioui.Faded(ink, 0.15)))
	var prev geom.Point
	for i, s := range c.shown {
		y := mid - plugui.ChipScale(float64(s.Value()), chipFull)*half
		// The line's round ends stay inside the box.
		pt := geom.Pt(width/2+(box.W-width)*float32(i)/(traced-1), y)
		if i > 0 {
			audioui.Segment(p, prev, pt, width, accent)
		}
		prev = pt
	}
}

// Access implements [gunim.Accessible].
func (c *chip) Access() access.Info {
	what := "Cut, " + pct(c.v[AmountID])
	if c.v.mode() == Shape {
		what = fmt.Sprintf("Shape toward %s, %s", c.v.target().Name, pct(c.v[AmountID]))
	}
	return access.Info{Role: access.RoleLabel, Name: "Stabilizer: " + what}
}

// Follow implements [plug.Follower].
func (c *chip) Follow(id uint32, v float64, u *gunim.UI) {
	c.v[id] = v
	u.Invalidate()
}
