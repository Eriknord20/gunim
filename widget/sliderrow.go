package widget

import (
	"image/color"
	"math"
	"strconv"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// The slider row's tokens.
var (
	// SliderRowLabel is the width of a slider row's label, and
	// SliderRowValue of its readout.
	SliderRowLabel = theme.Length("sliderrow.label", 96)
	SliderRowValue = theme.Length("sliderrow.value", 56)
	// SliderRowSize is the size of a slider row's text.
	SliderRowSize = theme.Length("sliderrow.size", 13)
	// SliderRowInk is the colour of a slider row's readout.
	SliderRowInk = theme.Color("sliderrow.ink", color.NRGBA{R: 0xa4, G: 0xab, B: 0xbb, A: 0xff})
	// SliderRowActive fills the row the keys act on, and its bar marks it.
	SliderRowActive = theme.Color("sliderrow.active", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x22})
)

// SliderRow is a labelled slider among others in a panel, as a photo
// editor lists its adjustments: the label, the slider, its value, and a
// mark that resets it. The readout counts along with the knob as it
// glides, and the mark fades in while the value is off the slider's Rest
// and turns a little as the pointer comes over it.
//
// Its slider is its own, to set up as any other: Min and Max, Rest,
// Gradient, OnChange and OnCommit.
type SliderRow struct {
	anim.Group
	// Label names the row's slider.
	Label  string
	Slider *Slider
	// Format writes the value for the readout. Nil writes it with as many
	// decimals as the slider's Snap has, or two.
	Format func(v float32) string

	labelText, value *Label
	// off shows the reset mark, and hot turns it under the pointer.
	off, hot *anim.Float
	reset    geom.Rect
	click    Clicker
	// active is how far the row shows as the one the keys act on.
	active *anim.Float
	on     bool
	// laid is set by the first layout, which shows the reset mark at
	// once for a slider already off rest.
	laid bool
}

// NewSliderRow returns a row of s, labelled label.
func NewSliderRow(label string, s *Slider) *SliderRow {
	r := &SliderRow{Label: label, Slider: s, labelText: NewLabel(label), value: NewLabel(""), off: anim.NewFloat(0), hot: anim.NewFloat(0), active: anim.NewFloat(0)}
	r.labelText.Size, r.labelText.NoWrap, r.labelText.MaxLines = SliderRowSize, true, 1
	r.value.Size, r.value.Color, r.value.NoWrap, r.value.Align = SliderRowSize, SliderRowInk, true, text.AlignEnd
	r.Add(r.off, r.hot, r.active)
	return r
}

// SetActive marks the row as the one the keys act on, or not, as a photo
// editor marks the adjustment its + and - step: the row's fill and a bar
// at its start grow in, and fade out again.
func (r *SliderRow) SetActive(on bool, u *gunim.UI) {
	if on == r.on {
		return
	}
	r.on = on
	m := Quick.Get(u.Theme())
	if !on {
		m = Settle.Get(u.Theme())
	}
	r.active.Animate(map[bool]float32{false: 0, true: 1}[on], m)
	u.Invalidate()
}

// Active reports whether the row is marked as the one the keys act on.
func (r *SliderRow) Active() bool { return r.on }

// PaintActive draws a row of box marked k of the way as the one the keys
// act on, as a slider row marks itself, for a row of an application's own
// beside slider rows.
func PaintActive(p *paint.Painter, th *theme.Live, box geom.Size, k float32) {
	if k < 0.01 {
		return
	}
	r := geom.Rect{Min: geom.Pt(-8, 0), Max: geom.Pt(box.W+4, box.H)}
	fill := SliderRowActive.Get(th)
	fill.A = uint8(float32(fill.A) * min(k, 1))
	p.RRect(r, 6, paint.Solid(fill))
	bar := Accent.Get(th)
	bar.A = uint8(float32(bar.A) * min(k, 1))
	h := box.H * 0.6 * k
	p.RRect(geom.Rc(r.Min.X+2, (box.H-h)/2, 3, h), 1.5, paint.Solid(bar))
}

// format writes v for the readout.
func (r *SliderRow) format(v float32) string {
	if r.Format != nil {
		return r.Format(v)
	}
	decimals := 2
	if s := r.Slider.Snap; s > 0 {
		decimals = max(0, int(math.Ceil(-math.Log10(float64(s))-1e-6)))
	}
	return strconv.FormatFloat(float64(v), 'f', decimals, 32)
}

// Children implements [gunim.Composite].
func (r *SliderRow) Children() []gunim.Node { return []gunim.Node{r.labelText, r.Slider, r.value} }

// Layout implements [gunim.Node]: the label, the slider filling what is
// left, the readout, and room for the reset mark.
func (r *SliderRow) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	th := f.Theme
	r.labelText.Text = r.Label
	r.Slider.row = r.Label
	w := c.Max.W
	if w <= 0 {
		w = FieldWidth.Get(th) * 1.5
	}
	h := ControlHeight.Get(th)
	lw, vw, rw := SliderRowLabel.Get(th), SliderRowValue.Get(th), h*0.7
	s := r.Slider
	// The readout follows the knob, so it counts as the knob glides.
	if t := r.format(s.Shown()); t != r.value.Text {
		r.value.Text = t
	}
	off := s.HasRest && math.Abs(float64(s.Value()-s.Rest)) > 1e-6
	if to := map[bool]float32{false: 0, true: 1}[off]; r.laid {
		r.off.Animate(to, Quick.Get(th))
	} else {
		r.off.Jump(to)
		r.laid = true
	}

	label, slider, value := kids.At(0), kids.At(1), kids.At(2)
	ls := label.Layout(gunim.Loose(geom.Sz(lw, h)))
	label.Place(geom.Pt(0, (h-ls.H)/2))
	sw := max(0, w-lw-vw-rw-8)
	slider.Layout(gunim.Tight(geom.Sz(sw, h)))
	slider.Place(geom.Pt(lw, 0))
	// The readout against the slider's end, on the row's middle line.
	vs := value.Layout(gunim.Loose(geom.Sz(vw, h)))
	value.Place(geom.Pt(lw+sw+4+vw-vs.W, (h-vs.H)/2))
	r.reset = geom.Rc(w-rw, (h-rw)/2, rw, rw)
	return c.Constrain(geom.Sz(w, h))
}

// Handle implements [gunim.Handler]: a click on the reset mark sends the
// slider gliding back to rest, while the slider is enabled.
func (r *SliderRow) Handle(e input.Event, u *gunim.UI) bool {
	th := u.Theme()
	shown := r.off.Target() > 0 && !r.Slider.Disabled
	switch e := e.(type) {
	case input.PointerMove:
		hot := shown && r.reset.Inset(geom.Uniform(-3)).Contains(e.Pos)
		r.hot.Animate(map[bool]float32{false: 0, true: 1}[hot], Quick.Get(th))
		u.Invalidate()
		return false
	case input.PointerLeave:
		r.hot.Animate(0, Settle.Get(th))
		return false
	case input.PointerDown:
		if !shown || e.Button != input.ButtonPrimary || r.onReset(e.Pos) < 0 {
			return false
		}
		r.click.Press(e, 0)
		return true
	case input.PointerUp:
		if !r.click.Release(e, r.onReset(e.Pos)) || !shown {
			return true
		}
		s := r.Slider
		s.set(s.Rest, Settle.Get(th), u)
		s.commit(u)
		u.Invalidate()
		return true
	}
	return false
}

// onReset is the row's one target, the reset mark: 0 for pos on it, with a little room round it, and -1 elsewhere.
func (r *SliderRow) onReset(pos geom.Point) int {
	if r.reset.Inset(geom.Uniform(-3)).Contains(pos) {
		return 0
	}
	return -1
}

// Paint implements [gunim.Node].
func (r *SliderRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	PaintActive(p, th, box, r.active.Value())
	for k := range kids.All {
		k.Paint(p)
	}
	off := r.off.Value()
	if off < 0.01 {
		return
	}
	// The mark grows in as the value leaves rest, and turns back a
	// little under the pointer, as a reset does.
	c := r.reset.Center()
	d := r.reset.Size().W * (0.6 + 0.4*off) * (1 + 0.15*r.hot.Value())
	ink := anim.Mix(anim.ColorCodec, SliderRowInk.Get(th), Ink.Get(th), r.hot.Value())
	ink.A = uint8(float32(ink.A) * min(off, 1))
	defer p.Push(paint.Rotate(-0.6*r.hot.Value(), c))()
	paintSmallIcon(p, th, icon.RotateCcw, geom.Rc(c.X-d/2, c.Y-d/2, d, d), ink)
}
