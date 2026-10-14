package imager

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

// The imager's colours: the sound's sides, and a band made narrower.
// Its middle, and a band made wider, are the theme's accent.
var (
	ImagerSide   = theme.Color("imager.side", color.NRGBA{R: 0xff, G: 0xa8, B: 0x5a, A: 0xff})
	ImagerNarrow = theme.Color("imager.narrow", color.NRGBA{R: 0x8a, G: 0x9a, B: 0xff, A: 0xff})
)

// The display's frequencies, from viewLow to viewHigh across; its
// spectrum's points, and how many frames it measures; and how far up and
// down a band's bar runs at its widest and narrowest, of the room over
// the controls.
const (
	viewLow, viewHigh = 20.0, 20000.0
	specPoints        = 160
	specFrames        = 4096
	barReach          = 0.3
	specTop           = 0
	specBottom        = -84
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

// crossovers returns the crossovers in order, as the processor keeps
// them.
func (v values) crossovers() [bands - 1]float64 {
	var x [bands - 1]float64
	for i := range x {
		x[i] = v[CrossoverID+uint32(i)]
	}
	return xover.Order(x)
}

// display is the sound's middle and sides by pitch, and the bands over
// them: the crossovers, to drag across; and each band's width, a bar up
// from the middle line for wider and down for narrower, to drag up and
// down, double-clicked back to as it was.
type display struct {
	h      plug.Host
	v      values
	out    plug.Sound
	frames []float32
	scr    []float32
	meter  *audioui.Spectrometer
	freqs  []float32
	// mid and side are the levels measured, and midSm and sideSm as
	// drawn, easing to them; at is the frame heard last, still how long
	// nothing new came.
	mid, side, midSm, sideSm []float32
	at                       int64
	still                    time.Duration
	// hot is what the pointer is on: a crossover, hotX, from 0, or a
	// band, hotBand, or neither, and xLit and bandLit how lit each is,
	// gliding; held is what is dragged.
	hotX, hotBand int
	xLit          [bands - 1]*anim.Float
	bandLit       [bands]*anim.Float
	group         anim.Group
	held          int
	heldFrom      geom.Point
	heldWas       float64
	// line is where the middle line runs, as the panel lays it out.
	line float32
	size geom.Size
}

// What a drag holds.
const (
	holdNone = iota
	holdCrossover
	holdBand
)

func newDisplay(h plug.Host, v values) *display {
	_, out := h.Sound()
	d := &display{h: h, v: v, out: out, frames: make([]float32, 2*specFrames), scr: make([]float32, 2*specFrames),
		meter: audioui.NewSpectrometer(specFrames), hotX: -1, hotBand: -1, still: plug.StillAfter}
	d.freqs = make([]float32, specPoints)
	for i := range d.freqs {
		d.freqs[i] = float32(viewLow * math.Pow(viewHigh/viewLow, float64(i)/(specPoints-1)))
	}
	for k := range d.xLit {
		d.xLit[k] = anim.NewFloat(0)
		d.group.Add(d.xLit[k])
	}
	for k := range d.bandLit {
		d.bandLit[k] = anim.NewFloat(0)
		d.group.Add(d.bandLit[k])
	}
	for _, p := range []*[]float32{&d.mid, &d.side, &d.midSm, &d.sideSm} {
		*p = make([]float32, specPoints)
		for i := range *p {
			(*p)[i] = specBottom - 20
		}
	}
	return d
}

// Step implements [gunim.Animator]: the spectra measured anew as the
// sound brings more, held between its chunks, falling once it stops.
func (d *display) Step(dt time.Duration) bool {
	playing := false
	if d.out != nil {
		n, at, rate := d.out.Heard(d.frames)
		switch {
		case at != 0 && at != d.at && n == specFrames:
			d.at, d.still, playing = at, 0, true
			for _, half := range []struct {
				side bool
				out  []float32
			}{{false, d.mid}, {true, d.side}} {
				for i := range n {
					l, r := d.frames[2*i], d.frames[2*i+1]
					m := (l + r) / 2
					if half.side {
						m = (l - r) / 2
					}
					d.scr[2*i], d.scr[2*i+1] = m, m
				}
				d.meter.Measure(d.scr, rate, d.freqs, half.out)
			}
		case at != 0:
			d.still = min(d.still+dt, plug.StillAfter)
			playing = d.still < plug.StillAfter
		}
	}
	if !playing {
		for i := range d.mid {
			d.mid[i], d.side[i] = specBottom-20, specBottom-20
		}
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
	for i := range d.mid {
		ease(&d.midSm[i], d.mid[i])
		ease(&d.sideSm[i], d.side[i])
	}
	return d.group.Step(dt) || playing || !settled
}

// xOf is where across the display a frequency is, and hzAt the
// frequency at x.
func (d *display) xOf(hz float64) float32 {
	return d.size.W * float32(math.Log(hz/viewLow)/math.Log(viewHigh/viewLow))
}

func (d *display) hzAt(x float32) float64 {
	return viewLow * math.Pow(viewHigh/viewLow, float64(x/max(d.size.W, 1)))
}

// specY is where on the display a level of the spectrum is.
func (d *display) specY(db float32) float32 {
	t := (db - specBottom) / (specTop - specBottom)
	return d.size.H * (1 - max(0, min(t, 1)))
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

// reach is how far a band's bar runs at its widest or narrowest.
func (d *display) reach() float32 { return d.line * barReach * 2 }

// Layout implements [gunim.Node].
func (d *display) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	d.size = c.Max
	if d.line == 0 {
		d.line = d.size.H / 2
	}
	return c.Max
}

// Paint implements [gunim.Node].
func (d *display) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink, accent := widget.Ink.Get(th), widget.Accent.Get(th)
	side, narrow := ImagerSide.Get(th), ImagerNarrow.Get(th)
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
	// The middle, filled, and the sides over it, a line.
	n := len(d.freqs)
	for i, fz := range d.freqs {
		x0 := d.xOf(float64(fz) / math.Pow(viewHigh/viewLow, 0.5/float64(n-1)))
		x1 := d.xOf(float64(fz) * math.Pow(viewHigh/viewLow, 0.5/float64(n-1)))
		if y := d.specY(d.midSm[i]); y < box.H {
			p.RRect(geom.Rc(x0, y, x1-x0+0.5, box.H-y), 0, paint.Solid(audioui.Faded(accent, 0.16)))
		}
	}
	plugui.Darken(p, whole, 1)
	lines := p.Layer(paint.LayerOpts{Bounds: whole, Opacity: plugui.Lines(1)})
	defer lines()
	var prevM, prevS geom.Point
	for i, fz := range d.freqs {
		x := d.xOf(float64(fz))
		m, s := geom.Pt(x, d.specY(d.midSm[i])), geom.Pt(x, d.specY(d.sideSm[i]))
		if i > 0 {
			audioui.Segment(p, prevM, m, 1.5, audioui.Faded(accent, 0.7))
			audioui.Segment(p, prevS, s, 1.5, audioui.Faded(side, 0.85))
		}
		prevM, prevS = m, s
	}
	// The bands: the middle line, each band's bar, and the crossovers.
	p.RRect(geom.Rc(0, d.line, box.W, 1), 0, paint.Solid(audioui.Faded(ink, 0.18)))
	for b, span := range d.bandsAt() {
		w := float32(d.v[WidthID+uint32(b)] / 100)
		c := accent
		if w < 0 {
			c = narrow
		}
		top := d.line - w*d.reach()
		r := geom.Rc(span[0]+3, min(top, d.line), span[1]-span[0]-6, float32(math.Abs(float64(top-d.line))))
		lit := d.bandLit[b].Value()
		p.RRect(r, 4, paint.Solid(audioui.Faded(c, 0.18+0.12*lit)))
		p.RRect(geom.Rc(r.Min.X, top-1-lit, r.Size().W, 2+2*lit), 1+lit, paint.Solid(c))
		words := param(WidthID).Text(d.v[WidthID+uint32(b)])
		if st := d.v[StereoizeID+uint32(b)]; st > 0.5 {
			words += fmt.Sprintf("  ·  stereoize %.0f%%", st)
		}
		label := audioui.Shaped(words, 11, true, false)
		ly := top - 18
		if w < 0 {
			ly = top + 6
		}
		if label.Advance < span[1]-span[0]-8 {
			label.Paint(p, geom.Pt((span[0]+span[1]-label.Advance)/2, ly), c)
		}
		name := audioui.Shaped(bandNames[b], 10, false, false)
		if name.Advance < span[1]-span[0]-8 {
			name.Paint(p, geom.Pt((span[0]+span[1]-name.Advance)/2, 26), audioui.Faded(ink, 0.5))
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
// bar pos is in reach of, or -1: under the bars' reach, where the
// controls stand and the gaps between them, no band.
func (d *display) hit(pos geom.Point) (crossover, band int) {
	for i, hz := range d.v.crossovers() {
		if math.Abs(float64(pos.X-d.xOf(hz))) < 6 {
			return i, -1
		}
	}
	if pos.Y > d.line+d.reach()+6 {
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
// its width back.
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
			id := WidthID + uint32(b)
			d.h.Begin(id, u)
			d.h.Set(id, 0, u)
			d.h.End(id, u)
			d.v[id] = 0
		case b >= 0:
			d.held, d.hotBand, d.heldFrom, d.heldWas = holdBand, b, e.Pos, d.v[WidthID+uint32(b)]
			d.h.Begin(WidthID+uint32(b), u)
		default:
			return false
		}
	case input.PointerUp:
		switch d.held {
		case holdCrossover:
			d.h.End(CrossoverID+uint32(d.hotX), u)
		case holdBand:
			d.h.End(WidthID+uint32(d.hotBand), u)
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
// frequency there, a band's width by how far up the pointer has gone,
// finer with Shift.
func (d *display) drag(pos geom.Point, fine bool, u *gunim.UI) {
	switch d.held {
	case holdCrossover:
		id := CrossoverID + uint32(d.hotX)
		v := param(id).Clamp(d.hzAt(pos.X))
		d.v[id] = v
		d.h.Set(id, v, u)
	case holdBand:
		id := WidthID + uint32(d.hotBand)
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
		fmt.Fprintf(&v, "%s %s. ", bandNames[b], param(WidthID).Text(d.v[WidthID+uint32(b)]))
	}
	return access.Info{Role: access.RoleGroup, Name: "Bands", Value: v.String()}
}

// scope is the sound's stereo, on a pane of glass: a vectorscope, its
// middle up and its sides across, and under it how alike the two
// channels are, from -1, opposite, through 0, apart, to +1, the same.
type scope struct {
	out    plug.Sound
	frames []float32
	at     int64
	corr   *anim.Float
	group  anim.Group
	// points are the frames shown, of the last read.
	points int
	size   geom.Size
}

const scopeFrames = 2048

func newScope(h plug.Host) *scope {
	_, out := h.Sound()
	s := &scope{out: out, frames: make([]float32, 2*scopeFrames), corr: anim.NewFloat(1)}
	// The correlation measures, so it keeps real time at any pace.
	s.corr.RealTime()
	s.group.Add(s.corr)
	return s
}

// Step implements [gunim.Animator].
func (s *scope) Step(dt time.Duration) bool {
	if s.out != nil {
		n, at, _ := s.out.Heard(s.frames)
		if at != 0 && at != s.at {
			s.at, s.points = at, n
			s.corr.Animate(float32(correlation(s.frames[:2*n])), anim.Snappy)
		}
	}
	return s.group.Step(dt)
}

// correlation is how alike frames' channels are: their correlation, 1
// for silence.
func correlation(frames []float32) float64 {
	var lr, ll, rr float64
	for i := 0; i+1 < len(frames); i += 2 {
		l, r := float64(frames[i]), float64(frames[i+1])
		lr, ll, rr = lr+l*r, ll+l*l, rr+r*r
	}
	if ll < 1e-12 || rr < 1e-12 {
		return 1
	}
	return lr / math.Sqrt(ll*rr)
}

// Layout implements [gunim.Node].
func (s *scope) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	s.size = c.Max
	return c.Max
}

// scopePad is the room round the scope on its pane, and corrH how tall
// the correlation's bar and words are.
const (
	scopePad = 12
	corrH    = 34
)

// Paint implements [gunim.Node].
func (s *scope) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink, accent := widget.Ink.Get(th), widget.Accent.Get(th)
	plugui.PaintGlass(p, th, geom.Rect{Max: box.Point()}, widget.CardRadius.Get(th)+4)
	side := box.W - 2*scopePad
	sq := geom.Rc(scopePad, scopePad, side, min(side, box.H-2*scopePad-corrH))
	c := sq.Center()
	half := sq.Size().H / 2
	// The cross the middle and the sides run along.
	p.RRect(geom.Rc(c.X-0.5, sq.Min.Y, 1, sq.Size().H), 0, paint.Solid(audioui.Faded(ink, 0.12)))
	p.RRect(geom.Rc(sq.Min.X, c.Y-0.5, sq.Size().W, 1), 0, paint.Solid(audioui.Faded(ink, 0.12)))
	for i := range s.points {
		l, r := s.frames[2*i], s.frames[2*i+1]
		x := c.X + (l-r)*half*0.7
		y := c.Y - (l+r)*half*0.7
		if x < sq.Min.X || x > sq.Max.X || y < sq.Min.Y || y > sq.Max.Y {
			continue
		}
		p.RRect(geom.Rc(x-0.75, y-0.75, 1.5, 1.5), 0.75, paint.Solid(audioui.Faded(accent, 0.45)))
	}
	// The correlation: a bar from -1 to +1, a mark where it is.
	bar := geom.Rc(scopePad, sq.Max.Y+16, side, 4)
	p.RRect(bar, 2, paint.Solid(audioui.Faded(ink, 0.12)))
	v := min(max(s.corr.Value(), -1), 1)
	mark := bar.Min.X + bar.Size().W*(v+1)/2
	col := accent
	if v < 0 {
		col = ImagerSide.Get(th)
	}
	p.RRect(geom.Rc(mark-3, bar.Min.Y-4, 6, 12), 3, paint.Solid(col))
	words := audioui.Shaped(fmt.Sprintf("Correlation %+.2f", v), 10, true, false)
	words.Paint(p, geom.Pt((box.W-words.Advance)/2, bar.Max.Y+4), audioui.Faded(ink, 0.7))
}

// Access implements [gunim.Accessible].
func (s *scope) Access() access.Info {
	return access.Info{Role: access.RoleGroup, Name: "Stereo", Value: fmt.Sprintf("Correlation %+.2f", s.corr.Value())}
}

// panel is the display filling the panel; over it, on glass, the scope
// at the right, and along the foot the sections: the crossovers, and
// each band's width and stereoize.
type panel struct {
	v        values
	disp     *display
	scope    *scope
	sections []*plugui.Section
	tuner    *plugui.Tuner
}

func newPanel(h plug.Host) *panel {
	v := valuesOf(h)
	p := &panel{v: v, disp: newDisplay(h, v), scope: newScope(h),
		tuner: plugui.NewTuner("Tune how the sound shows behind the controls")}
	p.sections = append(p.sections, plugui.NewSection(h, &Descriptor, "CROSSOVERS", 0,
		CrossoverID, CrossoverID+1, CrossoverID+2))
	for b := range bands {
		s := plugui.NewSection(h, &Descriptor, bandNames[b], 0, WidthID+uint32(b), StereoizeID+uint32(b))
		s.Knobs[0].Tooltip = "How wide the band is: -100% is mono, 0% as it was, +100% twice as wide"
		s.Knobs[1].Tooltip = "Width for a band too narrow to widen: its middle, a moment late, in its sides, " +
			"which leaves it in mono as it was"
		p.sections = append(p.sections, s)
	}
	return p
}

// Children implements [gunim.Composite].
func (p *panel) Children() []gunim.Node {
	kids := make([]gunim.Node, 0, 4+len(p.sections))
	kids = append(kids, p.disp, p.scope)
	for _, s := range p.sections {
		kids = append(kids, s)
	}
	return append(kids, p.tuner.Button, p.tuner.Card)
}

// The panel's sizes: how far in from its edges the glass stands, and the
// scope's pane.
const (
	panelInset = 16
	scopeW     = 190
)

// Layout implements [gunim.Node]: the display fills the panel, its
// middle line halfway down the room over the sections; the sections
// along the foot; the scope at the right, over them.
func (p *panel) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	kids.At(0).Layout(gunim.Tight(size))
	kids.At(0).Place(geom.Point{})
	top := plugui.PlaceSections(size, panelInset, p.sections, func(i int, r geom.Rect) {
		kids.At(2 + i).Layout(gunim.Tight(r.Size()))
		kids.At(2 + i).Place(r.Min)
	})
	room := top - plugui.SectionGap - panelInset
	p.disp.line = panelInset + room/2
	sh := min(scopeW-2*scopePad, room-2*scopePad-corrH) + 2*scopePad + corrH
	kids.At(1).Layout(gunim.Tight(geom.Sz(scopeW, max(sh, 0))))
	kids.At(1).Place(geom.Pt(size.W-panelInset-scopeW, top-plugui.SectionGap-sh))
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

// Follow implements [plug.Follower]: the knobs glide to their
// parameters set elsewhere, unless held, and the bands move.
func (p *panel) Follow(id uint32, v float64, u *gunim.UI) {
	p.v[id] = v
	for _, s := range p.sections {
		s.Follow(id, v, u)
	}
	u.Invalidate()
}

// chip shows, in a plugin's chip in a chain, each band's width as a bar
// up for wider and down for narrower.
type chip struct {
	v    values
	size geom.Size
}

func newChip(h plug.Host) *chip { return &chip{v: valuesOf(h)} }

// Layout implements [gunim.Node].
func (c *chip) Layout(cs gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	c.size = cs.Max
	return cs.Max
}

// Paint implements [gunim.Node].
func (c *chip) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink := widget.Ink.Get(th)
	mid := box.H / 2
	w := min(box.W/bands, 12)
	p.RRect(geom.Rc(0, mid, w*bands, 1), 0, paint.Solid(audioui.Faded(ink, 0.25)))
	for b := range bands {
		v := plugui.ChipScale(c.v[WidthID+uint32(b)], 100)
		col := widget.Accent.Get(th)
		if v < 0 {
			col = ImagerNarrow.Get(th)
		}
		h := v * mid * 0.9
		p.RRect(geom.Rc(float32(b)*w+1, min(mid, mid-h), w-2, float32(math.Abs(float64(h)))), 1, paint.Solid(col))
	}
}

// Access implements [gunim.Accessible].
func (c *chip) Access() access.Info {
	var v strings.Builder
	for b := range bands {
		fmt.Fprintf(&v, "%s %s ", bandNames[b], param(WidthID).Text(c.v[WidthID+uint32(b)]))
	}
	return access.Info{Role: access.RoleLabel, Name: v.String()}
}

// Follow implements [plug.Follower].
func (c *chip) Follow(id uint32, v float64, u *gunim.UI) {
	c.v[id] = v
	u.Invalidate()
}
