package eq

import (
	"fmt"
	"strconv"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugui"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// kindTips say what each kind of band does, for its lamp's tooltip.
var kindTips = []string{
	"lifts or lowers the pitches about its frequency",
	"lifts or lowers every pitch below its frequency",
	"lifts or lowers every pitch above its frequency",
	"takes away the pitches below its frequency",
	"takes away the pitches above its frequency",
	"takes away a narrow stretch about its frequency",
}

// What the band's knobs turn.
var (
	freqParam = plug.Param{Name: "Freq", Min: eqLow, Max: eqHigh, Default: 1000, Log: true,
		Format: func(v float64) string { return hzText(float32(v)) }}
	gainParam = plug.Param{Name: "Gain", Min: -eqRange, Max: eqRange,
		Format: func(v float64) string { return fmt.Sprintf("%+.1f dB", v) }}
	qParam = plug.Param{Name: "Q", Min: minQ, Max: maxQ, Default: 1, Log: true,
		Format: func(v float64) string { return fmt.Sprintf("%.2f", v) }}
	slopeParam = plug.Param{Name: "Slope", Min: 12, Max: 48, Default: 24, Steps: 3,
		Format: func(v float64) string { return fmt.Sprintf("%.0f dB/oct", v) }}
	thresholdParam = plug.Param{Name: "Threshold", Min: dynLowThreshold, Max: dynHighThreshold,
		Default: float64(dynDefaults.Threshold), Format: func(v float64) string { return fmt.Sprintf("%.1f dB", v) }}
	ratioParam = plug.Param{Name: "Ratio", Min: 1, Max: dynMaxRatio, Default: float64(dynDefaults.Ratio), Log: true,
		Format: func(v float64) string { return fmt.Sprintf("%.1f:1", v) }}
	attackParam = plug.Param{Name: "Attack", Min: dynLowAttack, Max: dynHighAttack, Default: float64(dynDefaults.Attack),
		Log: true, Format: func(v float64) string { return msText(float32(v)) }}
	releaseParam = plug.Param{Name: "Release", Min: dynLowRelease, Max: dynHighRelease,
		Default: float64(dynDefaults.Release), Log: true, Format: func(v float64) string { return msText(float32(v)) }}
	rangeParam = plug.Param{Name: "Range", Min: 0, Max: dynMaxRange, Default: float64(dynDefaults.Range),
		Format: func(v float64) string { return fmt.Sprintf("%.1f dB", v) }}
)

// msText writes a time in milliseconds, to a tenth under 10.
func msText(ms float32) string {
	if ms < 10 {
		return fmt.Sprintf("%.1f ms", ms)
	}
	return fmt.Sprintf("%.0f ms", ms)
}

// bandRow is one band's controls in a row: its kind, as lamps; its
// frequency, gain and Q, or slope, as knobs; buttons to turn it off and
// on, to make it dynamic, and to remove it; and, sliding in where it is
// dynamic, its dynamics' knobs: threshold, ratio, attack, release and
// range. Under the graph it is the band picked; in the list, each band
// has one, its number at its start. Every change is a gesture of the
// graph's, one step to undo. It glides in as it takes a band, and out
// as it lets one go.
type bandRow struct {
	anim.Group
	g *graph
	// id is the band's, 0 for none, and place its place among them, for
	// its number and colour; numbered shows the number.
	id, place int
	numbered  bool
	kind      *plugui.Selector
	knobs     []*plugui.Knob // freq, gain, q, then the dynamics'
	on, dyn   *widget.IconButton
	del       *widget.IconButton
	// glass puts the row on a card of glass, as the list does over the
	// graph, the band lit there while the pointer is on its row.
	glass bool
	// in is how far the row is in, and dynIn how far its dynamics are;
	// dynX is where its dynamics start.
	in, dynIn *anim.Float
	dynX      float32
	size      geom.Size
}

// The row's sizes: its number, the kind's lamps, and its buttons.
const (
	rowNumber  = 30
	rowKinds   = 156
	rowButton  = 32
	rowGap     = 6
	rowSection = 14
)

func newBandRow(g *graph, numbered bool) *bandRow {
	r := &bandRow{g: g, numbered: numbered, in: anim.NewFloat(0), dynIn: anim.NewFloat(0)}
	r.Add(r.in, r.dynIn)
	r.kind = plugui.NewSelector("Kind", kindNames, kindTips)
	r.kind.OnPick = func(i int, u *gunim.UI) { g.setKind(r.id, audio.FilterKind(i), u) }
	params := []plug.Param{freqParam, gainParam, qParam, thresholdParam, ratioParam, attackParam, releaseParam, rangeParam}
	for k, p := range params {
		r.knobs = append(r.knobs, plugui.NewKnobFunc(p, p.Default, r.begin(k), r.set(k), r.end))
	}
	r.knobs[0].Tooltip, r.knobs[1].Tooltip = "The band's frequency", "How far the band lifts or lowers"
	r.knobs[3].Tooltip = "The level of the band's sound where it starts to turn down"
	r.knobs[4].Tooltip = "How far it turns down over the threshold: at 4:1, 8 dB over turns down 6"
	r.knobs[5].Tooltip, r.knobs[6].Tooltip = "How fast it turns down", "How fast it comes back up"
	r.knobs[7].Tooltip = "The most it turns down"
	r.on = widget.NewIconButton(icon.Power, "Turn the band off")
	r.on.OnClick = func(u *gunim.UI) gunim.Intent { g.toggle(r.id, u); return nil }
	r.dyn = widget.NewIconButton(icon.Gauge, "Make the band dynamic: it turns itself down as its sound grows loud")
	r.dyn.OnClick = func(u *gunim.UI) gunim.Intent { g.toggleDynamic(r.id, u); return nil }
	r.del = widget.NewIconButton(icon.Trash2, "Remove the band")
	r.del.OnClick = func(u *gunim.UI) gunim.Intent { g.remove(r.id, u); return nil }
	return r
}

// begin returns the start of a gesture on knob k, named for what it
// moves.
func (r *bandRow) begin(k int) func(*gunim.UI) {
	return func(u *gunim.UI) {
		b, i := r.g.band(r.id)
		if b == nil {
			return
		}
		r.g.held = r.id
		name := fmt.Sprintf("Move band %d", i+1)
		switch {
		case k == 2:
			name = widthName(b, i)
		case k >= 3:
			name = fmt.Sprintf("Band %d dynamics", i+1)
		}
		r.g.host.BeginEdit(name, u)
	}
}

// set returns how knob k sets its value of the band, as a move of the
// gesture.
func (r *bandRow) set(k int) func(float64, *gunim.UI) {
	return func(v float64, u *gunim.UI) {
		b, _ := r.g.band(r.id)
		if b == nil {
			return
		}
		was := *b
		f := float32(v)
		switch k {
		case 0:
			b.Freq = clampHz(v)
		case 1:
			if hasGain(b.Kind) {
				b.Gain = clampDB(f)
			}
		case 2:
			if isCut(b.Kind) {
				b.Slope = int(v)
			} else {
				b.Q = clampQ(v)
			}
		case 3:
			b.Dynamic.Threshold = f
		case 4:
			b.Dynamic.Ratio = f
		case 5:
			b.Dynamic.Attack = f
		case 6:
			b.Dynamic.Release = f
		case 7:
			b.Dynamic.Range = f
		}
		if *b != was {
			r.g.commit(false, u)
		}
	}
}

// end ends a knob's gesture.
func (r *bandRow) end(u *gunim.UI) {
	r.g.held = 0
	r.g.host.EndEdit(u)
}

// show shows band b, at place, or none where b is nil: the controls
// glide to it.
func (r *bandRow) show(b *audio.Band, place int, u *gunim.UI) {
	sp := widget.Settle.Get(u.Theme())
	if b == nil {
		r.in.Animate(0, sp)
		r.id = 0
		u.Invalidate()
		return
	}
	r.id, r.place = b.ID, place
	r.in.Animate(1, sp)
	r.dynIn.Animate(onOff(dynamic(b)), sp)
	r.kind.SetSelected(int(b.Kind), u)
	q := r.knobs[2]
	switch {
	case isCut(b.Kind) && q.Param().Name != slopeParam.Name:
		q.SetParam(slopeParam, float64(max(b.Slope, 12)), u)
	case !isCut(b.Kind) && q.Param().Name != qParam.Name:
		q.SetParam(qParam, float64(b.Q), u)
	}
	d := b.Dynamic
	if d == (audio.Dynamics{}) {
		d = dynDefaults
	}
	values := []float64{float64(b.Freq), float64(b.Gain), float64(b.Q),
		float64(d.Threshold), float64(d.Ratio), float64(d.Attack), float64(d.Release), float64(d.Range)}
	if isCut(b.Kind) {
		values[2] = float64(max(b.Slope, 12))
	}
	for k, kn := range r.knobs {
		kn.Follow(values[k], u)
	}
	r.knobs[1].Locked = !hasGain(b.Kind)
	r.dyn.Disabled = !hasGain(b.Kind)
	r.on.Active, r.dyn.Active = b.On, b.Dynamic.On && hasGain(b.Kind)
	r.on.Tooltip = "Turn the band off"
	if !b.On {
		r.on.Tooltip = "Turn the band on"
	}
	switch {
	case !hasGain(b.Kind):
		r.dyn.Tooltip = "A cut or a notch has no gain to turn down: only a bell or a shelf can be dynamic"
	case b.Dynamic.On:
		r.dyn.Tooltip = "Make the band static again: its dynamics are kept for next time"
	default:
		r.dyn.Tooltip = "Make the band dynamic: it turns itself down as its sound grows loud"
	}
	u.Invalidate()
}

// Overhear implements [gunim.Overhearer]: a row of the list lights its
// band on the graph while the pointer is on it, unless a control of a
// row is held, whose band stays lit.
func (r *bandRow) Overhear(e input.Event, u *gunim.UI) {
	if !r.glass || r.g.held != 0 {
		return
	}
	m, ok := e.(input.PointerMove)
	if !ok {
		return
	}
	b, _ := u.Bounds(r)
	switch {
	case r.id != 0 && b.Contains(m.Pos):
		if r.g.lit != r.id {
			r.g.lit = r.id
			u.Invalidate()
		}
	case r.g.lit == r.id:
		r.g.lit = 0
		u.Invalidate()
	}
}

// dynWidth is how much wider a row is with its dynamics in.
const dynWidth = rowSection + 5*plugui.KnobW

// shownWidth is how wide the row is as it shows now: its dynamics as
// far in as they are.
func (r *bandRow) shownWidth() float32 {
	return r.width() - dynWidth*(1-min(max(r.dynIn.Value(), 0), 1))
}

// width is how wide the row is with its dynamics in.
func (r *bandRow) width() float32 {
	w := float32(rowKinds + 3*plugui.KnobW + 3*(rowButton+rowGap) + 2*rowSection + 5*plugui.KnobW)
	if r.numbered {
		w += rowNumber
	}
	return w
}

// Children implements [gunim.Composite].
func (r *bandRow) Children() []gunim.Node {
	kids := make([]gunim.Node, 0, 4+len(r.knobs))
	kids = append(kids, r.kind, r.on, r.dyn, r.del)
	for _, k := range r.knobs {
		kids = append(kids, k)
	}
	return kids
}

// Layout implements [gunim.Node]: the number, the lamps, the band's
// knobs and its buttons, then its dynamics' knobs, as far in as they
// are; a row out of the way takes no room, and nothing it has the
// pointer.
func (r *bandRow) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	gone := r.in.Value() < 0.01 && r.in.Target() == 0
	r.size = geom.Sz(c.Max.W, plugui.KnobH)
	if gone {
		// Out of the way, it takes no room, or it would stand over the
		// rows under it and take the pointer from them.
		r.size = geom.Size{}
	}
	place := func(i int, rc geom.Rect) {
		if gone {
			rc = geom.Rect{}
		}
		kids.At(i).Layout(gunim.Tight(rc.Size()))
		kids.At(i).Place(rc.Min)
	}
	x := float32(0)
	if r.numbered {
		x += rowNumber
	}
	place(0, geom.Rc(x, 0, rowKinds, plugui.SelectorH))
	x += rowKinds + rowSection
	for k := range 3 {
		place(4+k, geom.Rc(x, 0, plugui.KnobW, plugui.KnobH))
		x += plugui.KnobW
	}
	x += rowGap
	for i := range 3 {
		place(1+i, geom.Rc(x, (plugui.KnobH-rowButton)/2, rowButton, rowButton))
		x += rowButton + rowGap
	}
	x += rowSection
	r.dynX = x
	dynGone := r.dynIn.Value() < 0.01 && r.dynIn.Target() == 0
	for k := 3; k < len(r.knobs); k++ {
		rc := geom.Rc(x, 0, plugui.KnobW, plugui.KnobH)
		if dynGone {
			rc = geom.Rect{}
		}
		place(4+k, rc)
		x += plugui.KnobW
	}
	return r.size
}

// Paint implements [gunim.Node]: faded and slid as far as the row is
// in, its dynamics as far as they are.
func (r *bandRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	in := min(max(r.in.Value(), 0), 1)
	if in < 0.01 {
		return
	}
	th := f.Theme
	whole := geom.Rect{Max: box.Point()}
	defer p.Push(paint.Translate(geom.Pt(0, (1-in)*10)))()
	end := p.Layer(paint.LayerOpts{Bounds: whole.Inset(geom.Uniform(-12)), Opacity: in})
	defer end()
	if r.glass {
		// The card: the graph behind it blurred, under a tint of the
		// ground, its top edge catching the light.
		card := geom.Rc(-8, -4, r.shownWidth()+16, box.H+8)
		plugui.PaintGlass(p, th, card, 12)
		if r.g.lit == r.id && r.id != 0 {
			c := bandColor(th, r.place)
			p.RRectStroke(card, 12, paint.Fill{}, paint.Stroke{Width: 1.5, Color: audioui.Faded(c, 0.6)})
		}
	}
	if r.numbered {
		c := bandColor(th, r.place)
		at := geom.Pt(rowNumber/2-4, box.H/2)
		p.RRect(geom.Rc(at.X-11, at.Y-11, 22, 22), 11, paint.Solid(audioui.Faded(c, 0.85)))
		run := audioui.Shaped(strconv.Itoa(r.place+1), 11, true, false)
		run.Paint(p, geom.Pt(at.X-run.Advance/2, at.Y-7), audioui.Ground.Get(th))
	}
	for i := range 7 {
		kids.At(i).Paint(p)
	}
	dyn := min(max(r.dynIn.Value(), 0), 1)
	if dyn < 0.01 {
		return
	}
	// The dynamics, sliding in from the left.
	defer p.Push(paint.Translate(geom.Pt(-(1-dyn)*24, 0)))()
	endDyn := p.Layer(paint.LayerOpts{Bounds: whole.Inset(geom.Uniform(-12)), Opacity: dyn})
	defer endDyn()
	p.RRect(geom.Rc(r.dynX-rowSection/2, 8, 1, box.H-16), 0, paint.Solid(audioui.Faded(widget.Ink.Get(th), 0.12)))
	for i := 7; i < kids.Len(); i++ {
		kids.At(i).Paint(p)
	}
}

func onOff(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// dynamic says band b's dynamics show: it is a bell or a shelf, made
// dynamic.
func dynamic(b *audio.Band) bool { return b != nil && hasGain(b.Kind) && b.Dynamic.On }
