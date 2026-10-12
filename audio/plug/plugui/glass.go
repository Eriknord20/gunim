package plugui

import (
	"fmt"
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// Glass: a plugin's controls over its display, as the EQ's bands over
// its graph and the dynamics' knobs over the sound: the display darkened
// behind them, as far as Dark says; the controls on cards of glass that
// blur it, as far as Blur says, tinted as far as Card says; and the
// display's lines, the curve or the reduction, as bright over the dark as
// Curve says.

// GlassLook is how a display shows behind the controls over it.
type GlassLook struct {
	Dark, Blur, Card, Curve float64
}

// DefaultLook is where the look starts, and where a [Tuner]'s Reset puts
// it back.
var DefaultLook = GlassLook{Dark: 0.15, Blur: 2, Card: 0, Curve: 0.84}

// Look is the look now, for every plugin's views. It is the window's,
// read and set on its goroutine alone.
var Look = DefaultLook

// Darken darkens r as far as the look says, times t, for a display
// behind controls: t runs from 0, as it was, to 1, all the way behind.
func Darken(p *paint.Painter, r geom.Rect, t float32) {
	if a := min(max(Look.Dark*float64(t), 0), 1); a > 0.001 {
		p.RRect(r, 0, paint.Solid(color.NRGBA{A: uint8(255 * a)}))
	}
}

// Lines returns how bright a display's lines show over the dark, times
// t, from 1, as they were: for the opacity of a layer they draw in.
func Lines(t float32) float32 { return 1 - t*float32(1-Look.Curve) }

// PaintGlass draws a card of glass over r, its corners radius round: the
// display already drawn behind it blurred, tinted with the theme's
// ground, its edge catching the light. Whatever draws before it in a
// layer of its own, faded, is what it blurs; draw it in place.
func PaintGlass(p *paint.Painter, th *theme.Live, r geom.Rect, radius float32) {
	glass := p.Layer(paint.LayerOpts{Bounds: r, Opacity: 1, Backdrop: float32(Look.Blur), Clip: true, Radius: radius})
	if Look.Card > 0.001 {
		p.RRect(r, radius, paint.Solid(audioui.Faded(audioui.Ground.Get(th), float32(Look.Card))))
	}
	glass()
	p.RRectStroke(r, radius, paint.Fill{}, paint.Stroke{Width: 1, Color: audioui.Faded(widget.Ink.Get(th), 0.12)})
}

// Tuner turns the look: its Button opens its Card, a knob for each of the
// look's values, as they show at once, and a Reset. A view lays the card
// out last, over the rest, so a click on it reaches nothing under it.
type Tuner struct {
	Button *widget.IconButton
	Card   *TunerCard
}

// TunerCard is a [Tuner]'s card, dropping in as it opens and away as it
// closes, taking no room while closed.
type TunerCard struct {
	anim.Group
	Knobs []*Knob
	Reset *widget.Button
	in    *anim.Float
}

// The tuner's card: how wide and tall it is.
const (
	TunerW = 4*CompactW + 3*8 + 2*12
	TunerH = CompactH + 44
)

// The tuner's knobs: what each turns of the look, and how far.
var tunerParams = []plug.Param{
	{Name: "Darkness", Max: 100, Default: DefaultLook.Dark * 100, Format: percent},
	{Name: "Blur", Max: 30, Default: DefaultLook.Blur, Format: func(v float64) string { return fmt.Sprintf("%.0f px", v) }},
	{Name: "Card", Max: 100, Default: DefaultLook.Card * 100, Format: percent},
	{Name: "Curve", Max: 100, Default: DefaultLook.Curve * 100, Format: percent},
}

func percent(v float64) string { return fmt.Sprintf("%.0f%%", v) }

// lookOf returns what knob k turns of the look, and how much of the
// knob's value is one of it.
func lookOf(k int) (at *float64, scale float64) {
	if k == 1 {
		return &Look.Blur, 1
	}
	return []*float64{&Look.Dark, nil, &Look.Card, &Look.Curve}[k], 100
}

// NewTuner returns a tuner, its button's tooltip tip.
func NewTuner(tip string) *Tuner {
	c := &TunerCard{in: anim.NewFloat(0)}
	c.Add(c.in)
	t := &Tuner{Button: widget.NewIconButton(icon.SlidersHorizontal, tip), Card: c}
	t.Button.OnClick = func(u *gunim.UI) gunim.Intent {
		t.SetOpen(c.in.Target() == 0, u)
		return nil
	}
	tips := []string{"How dark the display goes behind the controls", "How far each card blurs the display behind it",
		"How much of each card's own colour shows", "How bright the display's lines stay over the dark"}
	for k, prm := range tunerParams {
		at, scale := lookOf(k)
		kn := NewKnobFunc(prm, *at*scale, func(*gunim.UI) {}, func(x float64, u *gunim.UI) {
			*at = x / scale
			u.Invalidate()
		}, func(*gunim.UI) {})
		kn.Compact, kn.Tooltip = true, tips[k]
		c.Knobs = append(c.Knobs, kn)
	}
	c.Reset = widget.NewButton("Reset")
	c.Reset.Tooltip = "Put the look back as it starts"
	c.Reset.OnClick = func(u *gunim.UI) gunim.Intent {
		Look = DefaultLook
		t.Follow(u)
		u.Invalidate()
		return nil
	}
	return t
}

// SetOpen opens the card, or closes it.
func (t *Tuner) SetOpen(open bool, u *gunim.UI) {
	t.Button.Active = open
	t.Card.in.Animate(onOff(open), widget.Settle.Get(u.Theme()))
	if open {
		// Another view may have turned the look meanwhile.
		t.Follow(u)
	}
	u.Invalidate()
}

// Open says the card is open, or opening.
func (t *Tuner) Open() bool { return t.Card.in.Target() == 1 }

// Follow turns the knobs to the look as it is.
func (t *Tuner) Follow(u *gunim.UI) {
	for k, kn := range t.Card.Knobs {
		at, scale := lookOf(k)
		kn.Follow(*at*scale, u)
	}
}

// shut says the card is closed, and takes no room.
func (c *TunerCard) shut() bool { return c.in.Value() < 0.01 && c.in.Target() == 0 }

// Children implements [gunim.Composite].
func (c *TunerCard) Children() []gunim.Node {
	kids := make([]gunim.Node, 0, len(c.Knobs)+1)
	kids = append(kids, c.Reset)
	for _, k := range c.Knobs {
		kids = append(kids, k)
	}
	return kids
}

// Layout implements [gunim.Node]: the knobs in a row, Reset under them;
// nothing while closed.
func (c *TunerCard) Layout(_ gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	if c.shut() {
		for k := range kids.All {
			k.Layout(gunim.Tight(geom.Size{}))
		}
		return geom.Size{}
	}
	in := geom.Pt(12, 10)
	for k := range c.Knobs {
		kids.At(1 + k).Layout(gunim.Tight(geom.Sz(CompactW, CompactH)))
		kids.At(1 + k).Place(in.Add(geom.Pt(float32(k)*(CompactW+8), 0)))
	}
	kids.At(0).Layout(gunim.Constraints{Max: geom.Sz(100, 28)})
	kids.At(0).Place(in.Add(geom.Pt(0, CompactH+4)))
	return geom.Sz(TunerW, TunerH)
}

// Paint implements [gunim.Node]: a raised card, dropping in as it opens.
func (c *TunerCard) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	in := min(max(c.in.Value(), 0), 1)
	if in < 0.01 {
		return
	}
	th := f.Theme
	r := geom.Rect{Max: box.Point()}
	defer p.Push(paint.Translate(geom.Pt(0, -(1-in)*8)))()
	end := p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-20)), Opacity: in})
	defer end()
	p.ShadowRRect(r, 12, paint.Solid(audioui.Mix(audioui.Ground.Get(th), widget.Ink.Get(th), 0.08)),
		paint.Shadow{Blur: 18, Offset: geom.Pt(0, 6), Color: audioui.Faded(audioui.Ground.Get(th), 0.8)})
	p.RRectStroke(r, 12, paint.Fill{}, paint.Stroke{Width: 1, Color: audioui.Faded(widget.Ink.Get(th), 0.12)})
	for k := range kids.All {
		k.Paint(p)
	}
}
