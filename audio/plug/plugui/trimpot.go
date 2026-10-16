package plugui

import (
	"image/color"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The trim pot's colours: its head, from its top to its bottom, and the
// slot cut across it.
var (
	TrimHeadTop    = theme.Color("plug.trim.head.top", color.NRGBA{R: 0xe4, G: 0xe4, B: 0xe8, A: 0xff})
	TrimHeadBottom = theme.Color("plug.trim.head.bottom", color.NRGBA{R: 0x86, G: 0x88, B: 0x90, A: 0xff})
	TrimSlot       = theme.Color("plug.trim.slot", color.NRGBA{R: 0x26, G: 0x26, B: 0x2a, A: 0xff})
)

// TrimPotSize is how big a trim pot is, across.
const TrimPotSize = 17

// TrimPot is a small setting turned now and then, as a meter's
// calibration is set with a screwdriver: a screw head, its slot turned
// as far as the value. It turns as a [Knob] does: a drag up turns it
// up, the wheel and the arrow keys step it, a double click sets it back.
// While the pointer is on it, or it is held, its value shows beside it,
// to its left, fading in.
type TrimPot struct {
	*Knob
}

// NewTrimPot returns a trim pot for p, a parameter of h's.
func NewTrimPot(h plug.Host, p plug.Param) *TrimPot { return &TrimPot{Knob: NewKnob(h, p)} }

// Layout implements [gunim.Node].
func (t *TrimPot) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Constrain(geom.Sz(TrimPotSize, TrimPotSize))
}

// slotAngle is how far round the slot is turned, in radians from
// upright: from 135° left to 135° right.
func (t *TrimPot) slotAngle() float64 { return (t.turnOf(t.v)*2 - 1) * 0.75 * math.Pi }

// Paint implements [gunim.Node].
func (t *TrimPot) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink, accent := widget.Ink.Get(th), widget.Accent.Get(th)
	hover := max(t.hover.Value(), onOff(t.held))
	c := geom.Pt(box.W/2, box.H/2)
	r := min(box.W, box.H)/2 - 1.5
	if hover > 0.01 {
		p.ShadowRRect(geom.Rc(c.X-r, c.Y-r, 2*r, 2*r), r, paint.Solid(audioui.Faded(accent, 0)),
			paint.Shadow{Blur: 10 * hover, Color: audioui.Faded(accent, 0.5*hover)})
	}
	// The hole it sits in, then the head, lit from above.
	p.RRect(geom.Rc(c.X-r-1.5, c.Y-r-1.5, 2*r+3, 2*r+3), r+1.5, paint.Solid(color.NRGBA{A: 0x70}))
	p.ShadowRRect(geom.Rc(c.X-r, c.Y-r, 2*r, 2*r), r, paint.Fill{Gradient: &paint.Gradient{
		From: geom.Pt(c.X, c.Y-r), To: geom.Pt(c.X, c.Y+r), Start: TrimHeadTop.Get(th), End: TrimHeadBottom.Get(th)}},
		paint.Shadow{Offset: geom.Pt(0, 1), Blur: 2, Color: color.NRGBA{A: 0x80}})
	// The slot, across the head, and the light on its lower edge.
	a := t.slotAngle()
	dx, dy := float32(math.Sin(a))*(r-1.8), -float32(math.Cos(a))*(r-1.8)
	audioui.Segment(p, geom.Pt(c.X-dx, c.Y-dy+0.6), geom.Pt(c.X+dx, c.Y+dy+0.6), 1.6, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x50})
	slot := TrimSlot.Get(th)
	if t.held {
		slot = audioui.Mix(slot, accent, 0.5)
	}
	audioui.Segment(p, geom.Pt(c.X-dx, c.Y-dy), geom.Pt(c.X+dx, c.Y+dy), 1.6, slot)
	// Its value, beside it to the left, as it is turned or looked at.
	if hover > 0.01 {
		words := audioui.Shaped(t.p.Name+" "+t.p.Text(t.v), 10, false, true)
		pad := float32(5)
		bub := geom.Rc(-words.Advance-2*pad-4, c.Y-9, words.Advance+2*pad, 18)
		p.RRect(bub, 9, paint.Solid(audioui.Faded(audioui.Ground.Get(th), 0.92*hover)))
		words.Paint(p, geom.Pt(bub.Min.X+pad, bub.Min.Y+(18-words.Height())/2), audioui.Faded(ink, 0.95*hover))
	}
}
