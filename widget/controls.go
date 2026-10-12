package widget

import (
	"image/color"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// toggle is the behaviour a checkbox and a switch share: hover, press,
// focus, and flipping on a click or Space.
type toggle struct {
	Control
	Label string
	// OnChange runs on the UI goroutine when the user flips the control,
	// with the new state. It may act in the window through u, such as a
	// box that shows a password field's text; a non-nil result is sent
	// to the application as the control's intent.
	OnChange func(on bool, u *gunim.UI) gunim.Intent

	// lit runs from 0 to 1 as the control turns on.
	lit   *anim.Float
	click Clicker
	size  geom.Size
	text  shapedText
	ell   shapedText
	// checked is the state: ticked, or on.
	checked bool
}

func newToggle(label string) toggle {
	t := toggle{
		Control: newControl(),
		Label:   label,
		lit:     anim.NewFloat(0),
		// Every click flips it, the fast second of a double click too.
		click: Clicker{Repeats: true},
	}
	t.Add(t.lit)
	return t
}

// Checked reports whether the control is ticked, or on.
func (t *toggle) Checked() bool { return t.checked }

// SetChecked sets the state and sends no intent. Once the control is laid out it animates there; before that, or
// with a nil u, it jumps.
func (t *toggle) SetChecked(on bool, u *gunim.UI) {
	if on == t.checked {
		return
	}
	t.checked = on
	if !t.laid || u == nil {
		t.lit.Jump(value(on))
		return
	}
	t.lit.Animate(value(on), Bounce.Get(u.Theme()))
}

func (t *toggle) flip(n gunim.Node, u *gunim.UI) {
	t.checked = !t.checked
	if t.checked {
		u.Cue(gunim.CueToggleOn, n)
	} else {
		u.Cue(gunim.CueToggleOff, n)
	}
	t.lit.Animate(value(t.checked), Bounce.Get(u.Theme()))
	if t.OnChange != nil {
		send(u, n, t.OnChange(t.checked, u))
	}
}

// handle is the Handle both controls share; n is the control itself.
func (t *toggle) handle(n gunim.Node, e input.Event, u *gunim.UI) bool {
	t.showTip(e, u, n)
	th := u.Theme()
	if t.Disabled {
		return t.handleDisabled(e, u, nil)
	}
	switch e := e.(type) {
	case input.PointerEnter:
		t.hover.Animate(1, Quick.Get(th))
		if t.held {
			t.squash.Animate(1, Quick.Get(th))
		}
	case input.PointerLeave:
		t.hover.Animate(0, Settle.Get(th))
		t.squash.Animate(0, Bounce.Get(th))
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		t.held = true
		t.click.Press(e, 0)
		t.squash.Animate(1, Quick.Get(th))
	case input.PointerUp:
		if !t.held {
			return false
		}
		t.held = false
		t.squash.Animate(0, Bounce.Get(th))
		if t.click.Release(e, over(e.Pos, t.size)) {
			t.flip(n, u)
		}
	case input.KeyPress:
		if e.Key != input.KeySpace {
			return false
		}
		t.squash.Retarget(1, Quick.Get(th))
		t.squash.Animate(0, Bounce.Get(th))
		t.flip(n, u)
	default:
		return t.ringFollows(e, th)
	}
	return true
}

// layout sizes the control: its mark, a gap, and its label.
func (t *toggle) layout(c gunim.Constraints, f gunim.Frame, mark geom.Size) geom.Size {
	if !t.laid {
		t.lit.Jump(value(t.checked))
	}
	t.follow(f.Theme)
	w, h := mark.W, mark.H
	if t.Label != "" {
		run := t.text.shape(faceIn(Font, f.Theme), t.Label, TextSize.Get(f.Theme))
		w += ControlGap.Get(f.Theme) + run.Advance
		h = max(h, run.Height())
	}
	t.size = c.Constrain(geom.Sz(w, max(h, ControlHeight.Get(f.Theme))))
	return t.size
}

// paintLabel draws the label after the control's mark, which is mark
// wide, cut with an ellipsis where the box is too narrow for it.
func (t *toggle) paintLabel(p *paint.Painter, f gunim.Frame, box geom.Size, mark float32) {
	x := mark + ControlGap.Get(f.Theme)
	if t.Label == "" || x >= box.W {
		return
	}
	run := fitRun(t.text.shape(faceIn(Font, f.Theme), t.Label, TextSize.Get(f.Theme)), &t.ell, box.W-x)
	run.Paint(p, geom.Pt(x, (box.H-run.Height())/2), Ink.Get(f.Theme))
}

// Checkbox is a box that is ticked or not, with a label.
//
// The tick draws itself in, short stroke then long, as the box fills
// with the accent colour; hover warms the border, a press squashes the
// box, and focus fades a ring in around it. A click anywhere on the
// checkbox or its label flips it, and so does Space.
type Checkbox struct{ toggle }

// NewCheckbox returns an unticked checkbox.
func NewCheckbox(label string) *Checkbox { return &Checkbox{newToggle(label)} }

// Handle implements [gunim.Handler].
func (c *Checkbox) Handle(e input.Event, u *gunim.UI) bool { return c.handle(c, e, u) }

// Layout implements [gunim.Node].
func (c *Checkbox) Layout(cs gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	s := CheckSize.Get(f.Theme)
	return c.layout(cs, f, geom.Sz(s, s))
}

// Paint implements [gunim.Node].
func (c *Checkbox) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	defer c.faint(p, box)()
	th := f.Theme
	s := CheckSize.Get(th)
	r := geom.Rc(0, (box.H-s)/2, s, s)
	radius := CheckRadius.Get(th)
	on := c.lit.Value()

	func() {
		defer p.Push(paint.Scale(1-0.1*c.squash.Value(), r.Center()))()
		c.paintRing(p, r, radius, th)
		border := anim.Mix(anim.ColorCodec, FieldBorder.Get(th), Ink.Get(th), 0.35*c.hover.Value())
		p.RRectStroke(r, radius, paint.Solid(FieldFill.Get(th)), paint.Stroke{Width: 1.5, Color: border})
		// The fill grows from the middle as the box turns on.
		if on > 0.01 {
			grow := min(on, 1)
			fill := Accent.Get(th)
			inner := geom.Rect{
				Min: r.Center().Sub(geom.Pt(s/2*grow, s/2*grow)),
				Max: r.Center().Add(geom.Pt(s/2*grow, s/2*grow)),
			}
			p.RRect(inner, radius*grow, paint.Solid(fill))
		}
		// The tick: a short stroke down, then a long one up.
		k := s / 18
		a := r.Min.Add(geom.Pt(4.5*k, 9.5*k))
		b := r.Min.Add(geom.Pt(7.5*k, 12.5*k))
		d := r.Min.Add(geom.Pt(13.5*k, 5.5*k))
		mark := CheckMark.Get(th)
		t := min(max(on, 0), 1)
		if short := min(t/0.35, 1); short > 0 {
			bar(p, a, lerpPt(a, b, short), 2*k, mark)
		}
		if long := (t - 0.35) / 0.65; long > 0 {
			bar(p, b, lerpPt(b, d, long), 2*k, mark)
		}
	}()
	c.paintLabel(p, f, box, s)
}

// Switch is a knob in a track that slides between off and on, with a
// label.
//
// The knob springs across and the track fills with the accent colour;
// pressing stretches the knob, the way a finger flattens it. A click or
// Space flips it.
type Switch struct{ toggle }

// NewSwitch returns a switch that is off.
func NewSwitch(label string) *Switch { return &Switch{newToggle(label)} }

// Handle implements [gunim.Handler].
func (s *Switch) Handle(e input.Event, u *gunim.UI) bool { return s.handle(s, e, u) }

// Layout implements [gunim.Node].
func (s *Switch) Layout(cs gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	return s.layout(cs, f, geom.Sz(SwitchWidth.Get(f.Theme), SwitchHeight.Get(f.Theme)))
}

// Paint implements [gunim.Node].
func (s *Switch) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	defer s.faint(p, box)()
	th := f.Theme
	w, h := SwitchWidth.Get(th), SwitchHeight.Get(th)
	track := geom.Rc(0, (box.H-h)/2, w, h)
	on := s.lit.Value()
	s.paintRing(p, track, h/2, th)
	off := anim.Mix(anim.ColorCodec, SwitchOff.Get(th), Ink.Get(th), 0.15*s.hover.Value())
	p.RRect(track, h/2, paint.Solid(anim.Mix(anim.ColorCodec, off, Accent.Get(th), min(max(on, 0), 1))))

	// The knob, inset from the track, widens under a press toward the
	// side it is heading for.
	inset := float32(3)
	d := h - 2*inset
	stretch := d * 0.3 * s.squash.Value()
	x := track.Min.X + inset + (w-2*inset-d-stretch)*on
	knob := geom.Rc(x, track.Min.Y+inset, d+stretch, d)
	p.ShadowRRect(knob, d/2, paint.Solid(Knob.Get(th)), paint.Shadow{Offset: geom.Pt(0, 1), Blur: 2, Color: KnobShadow.Get(th)})
	s.paintLabel(p, f, box, w)
}

// Slider picks a number between Min and Max by dragging a knob along a
// track, or with the arrow keys, Page Up and Page Down, Home and End.
//
// A click on the track sends the knob gliding there; a drag carries it
// along with the pointer. The knob grows while the pointer is over it
// or it is held.
//
// A [Vertical] slider is a fader: Max is at the top, and it fills the
// height it is given rather than the width.
type Slider struct {
	Control
	// Axis lays the track along the width, which is the zero value, or
	// up the height as a fader.
	Axis     Axis
	Min, Max float32
	// Label names the slider for a screen reader, as the label beside it
	// does on screen. A [SliderRow] names its slider by its own label.
	Label string
	// Snap rounds the value to multiples of itself, counted from Min;
	// zero leaves it free.
	Snap float32
	// OnChange runs on the UI goroutine as the user moves the value, on
	// every step of a drag. It may act in the window through u, such as a
	// number beside the slider that follows it; a non-nil result is sent
	// to the application as the slider's intent.
	OnChange func(v float32, u *gunim.UI) gunim.Intent
	// OnCommit runs, in the same way, with the value a gesture ends on: as
	// a drag is let go, a double click returns to Rest, or a key steps. It
	// runs once per change made, for an application that saves, or keeps
	// undo history.
	OnCommit func(v float32, u *gunim.UI) gunim.Intent
	// Rest, with HasRest set, is the value the slider rests at, such as
	// nought on a scale from -100 to 100: the fill runs from it to the
	// knob, a small mark shows it on the track, and a double click glides
	// back to it.
	Rest    float32
	HasRest bool
	// Gradient colours the track along its length, from Min to Max, as a
	// colour temperature runs from blue to amber; it takes the fill's
	// place. Two colours or more.
	Gradient []color.NRGBA

	value float32
	// drag is the value a drag has carried the knob to, unclamped, and
	// last where the pointer was, so a drag moves the knob by as much as
	// the pointer moves, a tenth as much with Shift. pressed is the value
	// as the drag began.
	drag, pressed float32
	last          geom.Point
	// at is the knob's place, 0 to 1 along the track.
	at   *anim.Float
	size geom.Size
	// row is the label of the row the slider is in, which names it where Label is empty.
	row string
}

// NewSlider returns a slider from lo to hi, at lo.
func NewSlider(lo, hi float32) *Slider {
	s := &Slider{Control: newControl(), Min: lo, Max: hi, value: lo, at: anim.NewFloat(0)}
	s.Add(s.at)
	return s
}

// NewVerticalSlider returns a vertical slider from lo to hi, at lo, with hi at
// the top.
func NewVerticalSlider(lo, hi float32) *Slider {
	s := NewSlider(lo, hi)
	s.Axis = Vertical
	return s
}

// Value returns the slider's value.
func (s *Slider) Value() float32 { return s.value }

// Held reports whether the pointer holds the knob, for a view that leaves
// a slider being dragged alone as its state comes in.
func (s *Slider) Held() bool { return s.held }

// Shown returns the value where the knob shows now, on its way to Value,
// for a readout that counts along with it.
func (s *Slider) Shown() float32 { return s.Min + s.at.Value()*(s.Max-s.Min) }

// fracOf returns v as a fraction of the range.
func (s *Slider) fracOf(v float32) float32 {
	if s.Max <= s.Min {
		return 0
	}
	return max(0, min((v-s.Min)/(s.Max-s.Min), 1))
}

// commit tells the application the value a gesture ended on.
func (s *Slider) commit(u *gunim.UI) {
	if s.OnCommit != nil {
		send(u, s, s.OnCommit(s.value, u))
	}
}

// SetValue sets the value and sends no intent. Once the slider is laid
// out the knob glides to it; before that, or with a nil u, it jumps. It
// keeps the value as it is, within the range, off Snap's steps too: only
// the slider's own moves round to them, so a value stored with more
// precision shows as it is.
func (s *Slider) SetValue(v float32, u *gunim.UI) {
	s.value = max(s.Min, min(v, s.Max))
	if !s.laid || u == nil {
		s.at.Jump(s.frac())
		return
	}
	s.at.Animate(s.frac(), Quick.Get(u.Theme()))
}

func (s *Slider) clamp(v float32) float32 {
	if s.Snap > 0 {
		v = s.Min + float32(math.Round(float64((v-s.Min)/s.Snap)))*s.Snap
	}
	return max(s.Min, min(v, s.Max))
}

// frac returns the value as a fraction of the range.
func (s *Slider) frac() float32 {
	if s.Max <= s.Min {
		return 0
	}
	return (s.value - s.Min) / (s.Max - s.Min)
}

// set moves to v, the knob following with m, and tells the
// application when the value changed.
func (s *Slider) set(v float32, m anim.Motion, u *gunim.UI) {
	v = s.clamp(v)
	if v == s.value {
		return
	}
	if s.passesStep(s.value, v) {
		u.Cue(gunim.CueTick, s)
	}
	s.value = v
	s.at.Animate(s.frac(), m)
	if s.OnChange != nil {
		send(u, s, s.OnChange(v, u))
	}
}

// passesStep reports whether a move from a to b passes a step: a snap,
// on a slider that snaps, or a tenth of the track on one that does not.
func (s *Slider) passesStep(a, b float32) bool {
	step := s.Snap
	if step <= 0 {
		step = (s.Max - s.Min) / 10
	}
	if step <= 0 {
		return false
	}
	at := func(v float32) float64 { return math.Floor(float64((v - s.Min) / step)) }
	return at(a) != at(b) || s.Snap > 0
}

// trackLength returns how far the knob travels from Min to Max.
func (s *Slider) trackLength(th *theme.Live) float32 {
	k := KnobSize.Get(th)
	if s.Axis == Vertical {
		return max(s.size.H-k, 1)
	}
	return max(s.size.W-k, 1)
}

// onKnob reports whether pos is on the knob, with a little room round it.
func (s *Slider) onKnob(pos geom.Point, th *theme.Live) bool {
	k := KnobSize.Get(th) / 2
	at := k + s.at.Value()*s.trackLength(th)
	d := pos.X - at
	if s.Axis == Vertical {
		d = pos.Y - (s.size.H - at)
	}
	return d >= -k-3 && d <= k+3
}

// valueAt returns the value at a point in the slider's space.
func (s *Slider) valueAt(pos geom.Point, th *theme.Live) float32 {
	k := KnobSize.Get(th) / 2
	if s.Axis == Vertical {
		// The track runs up the slider, so the top of it is Max.
		h := max(s.size.H-2*k, 1)
		return s.Min + (1-max(0, min((pos.Y-k)/h, 1)))*(s.Max-s.Min)
	}
	w := max(s.size.W-2*k, 1)
	return s.Min + max(0, min((pos.X-k)/w, 1))*(s.Max-s.Min)
}

// DragsTouch implements [gunim.TouchDragger]: a finger on the thumb
// drags it, where it would scroll the page round the slider.
func (s *Slider) DragsTouch() bool { return s.held }

// Handle implements [gunim.Handler].
func (s *Slider) Handle(e input.Event, u *gunim.UI) bool {
	s.showTip(e, u, s)
	th := u.Theme()
	if s.Disabled {
		return s.handleDisabled(e, u, nil)
	}
	step := s.Snap
	if step <= 0 {
		step = (s.Max - s.Min) / 100
	}
	switch e := e.(type) {
	case input.PointerEnter:
		s.hover.Animate(1, Quick.Get(th))
	case input.PointerLeave:
		if !s.held {
			s.hover.Animate(0, Settle.Get(th))
		}
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if e.Clicks == 2 && s.HasRest {
			// Back to rest, gliding there.
			s.held = false
			s.set(s.Rest, Settle.Get(th), u)
			s.commit(u)
			break
		}
		s.held, s.pressed, s.last = true, s.value, e.Pos
		// A press on the knob takes hold of it where it is; one on the
		// track sends it gliding there.
		if !s.onKnob(e.Pos, th) {
			s.set(s.valueAt(e.Pos, th), Quick.Get(th), u)
		}
		s.drag = s.value
	case input.PointerMove:
		if !s.held {
			return false
		}
		// The knob moves as far as the pointer does, a tenth as far with
		// Shift held, for a fine adjustment; it keeps up with the pointer
		// while it glides in from a click, then follows it exactly.
		d := e.Pos.X - s.last.X
		if s.Axis == Vertical {
			d = s.last.Y - e.Pos.Y
		}
		if e.Mods.Has(input.ModShift) {
			d /= 10
		}
		s.last = e.Pos
		s.drag += d / s.trackLength(th) * (s.Max - s.Min)
		s.set(s.drag, Caret.Get(th), u)
	case input.PointerUp:
		if !s.held {
			return false
		}
		s.held = false
		if !(geom.Rect{Max: s.size.Point()}).Contains(e.Pos) {
			s.hover.Animate(0, Settle.Get(th))
		}
		if s.value != s.pressed {
			s.commit(u)
		}
	case input.KeyPress:
		// Shift steps ten times as far.
		if e.Mods.Has(input.ModShift) {
			step *= 10
		}
		was := s.value
		switch e.Key {
		case input.KeyLeft, input.KeyDown:
			s.set(s.value-step, Quick.Get(th), u)
		case input.KeyRight, input.KeyUp:
			s.set(s.value+step, Quick.Get(th), u)
		case input.KeyPageDown:
			s.set(s.value-(s.Max-s.Min)/10, Quick.Get(th), u)
		case input.KeyPageUp:
			s.set(s.value+(s.Max-s.Min)/10, Quick.Get(th), u)
		case input.KeyHome:
			s.set(s.Min, Quick.Get(th), u)
		case input.KeyEnd:
			s.set(s.Max, Quick.Get(th), u)
		default:
			return false
		}
		if s.value != was {
			s.commit(u)
		}
	default:
		if !s.ringFollows(e, th) {
			return false
		}
	}
	u.Invalidate()
	return true
}

// Layout implements [gunim.Node].
//
// A horizontal slider fills the width it is given, or the theme's
// [FieldWidth] where the width is unbounded. A vertical one fills the
// height, or [FieldWidth] where that is unbounded, and is as wide as
// the knob.
func (s *Slider) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	s.follow(f.Theme)
	if s.Axis == Vertical {
		h := c.Max.H
		if h <= 0 {
			h = FieldWidth.Get(f.Theme)
		}
		s.size = c.Constrain(geom.Sz(ControlHeight.Get(f.Theme), h))
		return s.size
	}
	w := c.Max.W
	if w <= 0 {
		w = FieldWidth.Get(f.Theme)
	}
	s.size = c.Constrain(geom.Sz(w, ControlHeight.Get(f.Theme)))
	return s.size
}

// Paint implements [gunim.Node].
func (s *Slider) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	defer s.faint(p, box)()
	th := f.Theme
	k := KnobSize.Get(th)
	track := SliderTrack.Get(th)
	at := s.at.Value()

	var whole, filled geom.Rect
	var centre geom.Point
	if s.Axis == Vertical {
		x := box.W / 2
		y0, y1 := box.H-k/2, k/2 // the bottom of the track is Min
		y := y0 + (y1-y0)*at
		whole = geom.Rect{Min: geom.Pt(x-track/2, y1), Max: geom.Pt(x+track/2, y0)}
		filled = geom.Rect{Min: geom.Pt(x-track/2, y), Max: geom.Pt(x+track/2, y0)}
		centre = geom.Pt(x, y)
	} else {
		y := box.H / 2
		x0, x1 := k/2, box.W-k/2
		x := x0 + (x1-x0)*at
		whole = geom.Rect{Min: geom.Pt(x0, y-track/2), Max: geom.Pt(x1, y+track/2)}
		filled = geom.Rect{Min: geom.Pt(x0, y-track/2), Max: geom.Pt(x, y+track/2)}
		centre = geom.Pt(x, y)
	}
	switch {
	case len(s.Gradient) >= 2:
		// The track in its colours, from Min to Max, and no fill.
		g := &paint.Gradient{Start: s.Gradient[0], End: s.Gradient[len(s.Gradient)-1]}
		if s.Axis == Vertical {
			g.From, g.To = geom.Pt(0, whole.Max.Y), geom.Pt(0, whole.Min.Y)
		} else {
			g.From, g.To = geom.Pt(whole.Min.X, 0), geom.Pt(whole.Max.X, 0)
		}
		for i := 1; i < len(s.Gradient)-1; i++ {
			g.Stops = append(g.Stops, paint.Stop{At: float32(i) / float32(len(s.Gradient)-1), Color: s.Gradient[i]})
		}
		p.RRect(whole.Inset(geom.Uniform(-0.5)), track/2+0.5, paint.Fill{Gradient: g})
	case s.HasRest:
		// The fill runs from rest to the knob, and a mark shows rest.
		p.RRect(whole, track/2, paint.Solid(SwitchOff.Get(th)))
		r := s.fracOf(s.Rest)
		if s.Axis == Vertical {
			y := whole.Max.Y + (whole.Min.Y-whole.Max.Y)*r
			p.RRect(geom.Rect{Min: geom.Pt(whole.Min.X, min(y, centre.Y)), Max: geom.Pt(whole.Max.X, max(y, centre.Y))}, track/2, paint.Solid(Accent.Get(th)))
			if r > 0 && r < 1 {
				p.RRect(geom.Rc(whole.Min.X-2, y-0.75, track+4, 1.5), 0.75, paint.Solid(SliderRestMark.Get(th)))
			}
		} else {
			x := whole.Min.X + (whole.Max.X-whole.Min.X)*r
			p.RRect(geom.Rect{Min: geom.Pt(min(x, centre.X), whole.Min.Y), Max: geom.Pt(max(x, centre.X), whole.Max.Y)}, track/2, paint.Solid(Accent.Get(th)))
			if r > 0 && r < 1 {
				p.RRect(geom.Rc(x-0.75, whole.Min.Y-2, 1.5, track+4), 0.75, paint.Solid(SliderRestMark.Get(th)))
			}
		}
	default:
		p.RRect(whole, track/2, paint.Solid(SwitchOff.Get(th)))
		p.RRect(filled, track/2, paint.Solid(Accent.Get(th)))
	}

	grow := 1 + 0.2*max(s.hover.Value(), 0)
	d := k * grow
	knob := geom.Rc(centre.X-d/2, centre.Y-d/2, d, d)
	s.paintRing(p, knob, d/2, th)
	p.ShadowRRect(knob, d/2, paint.Solid(Knob.Get(th)), paint.Shadow{Offset: geom.Pt(0, 1), Blur: 3, Color: KnobShadow.Get(th)})
}

// Tabs shows one of several pages under a row of titles. The line under
// the chosen title glides to the next one, and the new page slides in
// from the side it lies on while the old one fades.
//
// The row of titles takes focus, and then the arrow keys, Home and End
// choose a tab. A click on a title chooses it.
//
// Titles wider than the row scroll sideways, by the wheel or a finger,
// and the row brings a newly chosen title into view.
type Tabs struct {
	anim.Group
	Titles []string
	// Icons shows an icon before each title, in the title's colour, and may be shorter than Titles.
	Icons []*icon.Icon
	// Disabled lists the tabs that cannot be chosen now, in order, and may
	// be shorter than Titles. They are drawn faint.
	Disabled []bool
	// OnChange runs on the UI goroutine when the user chooses a tab; a
	// non-nil result is sent to the application as the tabs' intent.
	OnChange func(i int, u *gunim.UI) gunim.Intent
	// Inset, when set, keeps the titles and the line under them that
	// far in from the left and the right: the first title's words start
	// at it, and the line ends there, so they line up with a page whose
	// content is inset as far. The pages keep the whole width.
	Inset theme.Token[float32]

	bar   *tabBar
	pages []gunim.Node
	// count is how many pages there are: those given, and those inserted
	// since, as the views mounted under the tabs' view are. laidPages
	// holds them all, as at the last layout.
	count     int
	laidPages []gunim.Node
	selected  int
	// prev is the page leaving, or -1, and from is the side the new
	// page comes from: 1 from the right, -1 from the left.
	prev int
	from float32
	// line is the indicator's place, left and right edges; slide runs
	// from 0 to 1 as the new page arrives.
	line  *anim.Point
	slide *anim.Float
	ring  *anim.Float
	laid  bool

	shaped []shapedText
	// spans holds each title's left and right edge.
	spans [][2]float32
	head  float32

	// off is how far the titles are scrolled left, and offTo where they are going. room is the row's width and
	// wide the titles', from the last layout. shown is the tab last brought into view, or -1.
	off        *anim.Float
	offTo      float32
	room, wide float32
	shown      int
}

// NewTabs returns tabs showing pages under titles, the first chosen.
// The two lists pair up in order.
func NewTabs(titles []string, pages ...gunim.Node) *Tabs {
	t := &Tabs{Titles: titles, pages: pages, count: len(pages), prev: -1, line: anim.NewPoint(geom.Point{}), slide: anim.NewFloat(1), ring: anim.NewFloat(0),
		off: anim.NewFloat(0), shown: -1}
	t.bar = &tabBar{t: t}
	t.Add(t.line, t.slide, t.ring, t.off)
	return t
}

// Children implements [gunim.Composite]: the row of titles, then the
// pages.
func (t *Tabs) Children() []gunim.Node { return append([]gunim.Node{t.bar}, t.pages...) }

// Selected returns the chosen tab.
func (t *Tabs) Selected() int { return t.selected }

// SetSelected chooses tab i and sends no intent. Once the tabs are laid
// out, the new page slides in and the line glides under its title;
// before that, or with a nil u, the tab shows at once.
func (t *Tabs) SetSelected(i int, u *gunim.UI) {
	if i < 0 || i >= max(t.count, len(t.Titles)) || i == t.selected {
		return
	}
	if !t.laid || u == nil {
		t.selected, t.prev, t.laid = i, -1, false
		t.slide.Jump(1)
		return
	}
	t.prev, t.from = t.selected, 1
	if i < t.selected {
		t.from = -1
	}
	old := t.page(t.selected)
	t.selected = i
	t.slide.Jump(0)
	t.slide.Animate(1, Settle.Get(u.Theme()))
	// The keyboard leaves a page as it hides.
	if old != nil && u.HasFocus(old) {
		t.refocus(i, u)
	}
	u.Invalidate()
}

// refocus puts the keyboard on the first place on page i that takes it,
// or on the titles where there is none. The titles hold it until the
// next frame lays the page out, and the page takes it before that frame
// paints.
func (t *Tabs) refocus(i int, u *gunim.UI) {
	u.Focus(t.bar)
	if p := t.page(i); p != nil {
		u.FocusFirstLaidOut(p)
	}
}

// page returns page i, or nil while it has not arrived.
func (t *Tabs) page(i int) gunim.Node {
	switch {
	case i < 0:
		return nil
	case i < len(t.laidPages):
		return t.laidPages[i]
	case i < len(t.pages):
		return t.pages[i]
	}
	return nil
}

func (t *Tabs) choose(i int, u *gunim.UI) {
	if i == t.selected || i < 0 || i >= t.count || flag(t.Disabled, i) {
		return
	}
	t.SetSelected(i, u)
	act(u, t, gunim.CueSelect, t.OnChange, i)
}

// icon returns tab i's icon, or nil.
func (t *Tabs) icon(i int) *icon.Icon {
	if i < 0 || i >= len(t.Icons) {
		return nil
	}
	return t.Icons[i]
}

// iconRoom is the room tab i's icon takes before its title, with the gap.
func (t *Tabs) iconRoom(i int, th *theme.Live) float32 {
	if t.icon(i) == nil {
		return 0
	}
	return IconSize.Get(th) + IconGap.Get(th)
}

// tabBar is the row of titles.
type tabBar struct {
	t     *Tabs
	click Clicker
}

// titleAt returns the title at pos in the row's space, or -1.
func (b *tabBar) titleAt(pos geom.Point) int {
	t := b.t
	if pos.Y < 0 || pos.Y >= t.head {
		return -1
	}
	x := pos.X + t.off.Value()
	for i, s := range t.spans {
		if x >= s[0] && x < s[1] {
			return i
		}
	}
	return -1
}

// Focusable implements [gunim.Focusable].
func (b *tabBar) Focusable() bool { return true }

// Handle implements [gunim.Handler]. It takes the clicks on the titles
// and the keys. A click chooses the title it lets go on, the one it
// pressed, so a finger that lands on a title to scroll the row chooses
// nothing.
func (b *tabBar) Handle(e input.Event, u *gunim.UI) bool {
	t := b.t
	th := u.Theme()
	switch e := e.(type) {
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		b.click.Press(e, b.titleAt(e.Pos))
	case input.PointerUp:
		if i := b.titleAt(e.Pos); b.click.Release(e, i) {
			t.choose(i, u)
		}
	case input.Scroll:
		// A wheel turns either way along the row; a finger drags it.
		d := e.Delta.X
		if d == 0 {
			d = e.Delta.Y
		}
		to := t.clampOff(t.offTo - d)
		if to == t.offTo {
			return false
		}
		t.scrollTo(to, Quick.Get(th))
		u.Invalidate()
	case input.KeyPress:
		switch e.Key {
		case input.KeyLeft:
			t.choose(t.enabled(t.selected-1, -1), u)
		case input.KeyRight:
			t.choose(t.enabled(t.selected+1, 1), u)
		case input.KeyHome:
			t.choose(t.enabled(0, 1), u)
		case input.KeyEnd:
			t.choose(t.enabled(t.count-1, -1), u)
		default:
			return false
		}
	case input.FocusRing:
		t.ring.Animate(ringTo(e), RingFade.Get(th))
	case input.FocusLost:
		t.ring.Animate(0, RingFade.Get(th))
	default:
		return false
	}
	return true
}

// Layout implements [gunim.Node].
func (b *tabBar) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	t := b.t
	th := f.Theme
	t.head = TabHeight.Get(th)
	pad := TabPadding.Get(th)
	if len(t.shaped) != len(t.Titles) {
		t.shaped = make([]shapedText, len(t.Titles))
	}
	t.spans = t.spans[:0]
	x := max(0, t.inset(th)-pad)
	for i, s := range t.Titles {
		w := t.shaped[i].shape(faceIn(Font, th), s, TextSize.Get(th)).Advance + 2*pad + t.iconRoom(i, th)
		t.spans = append(t.spans, [2]float32{x, x + w})
		x += w
	}
	// Titles cut short of the chosen tab leave the last of them chosen,
	// its line under it at once.
	cut := false
	if n := len(t.spans); n > 0 && t.selected >= n {
		t.selected, t.prev, t.shown, cut = n-1, -1, n-1, true
	}
	if t.selected < len(t.spans) {
		sp := t.spans[t.selected]
		to := geom.Pt(sp[0]+pad, sp[1]-pad)
		if t.laid && !cut {
			t.line.Animate(to, Bounce.Get(th))
		} else {
			t.line.Jump(to)
		}
	}
	w := c.Max.W
	if w <= 0 {
		w = x
	}
	t.room, t.wide = w, x
	t.fitOff(th)
	t.laid = true
	return geom.Sz(w, t.head)
}

// inset returns how far in the titles and their line keep, or zero.
func (t *Tabs) inset(th *theme.Live) float32 {
	if t.Inset.Key() == "" {
		return 0
	}
	return t.Inset.Get(th)
}

// tabsRoom is the room the row leaves beside a title it brings into view, so the next title's edge shows.
const tabsRoom = 24

// fitOff keeps the scroll inside the titles, and brings a newly chosen title into view. Only a newly chosen title
// glides into view: the scroll held inside titles or room that changed jumps there.
func (t *Tabs) fitOff(th *theme.Live) {
	to := t.clampOff(t.offTo)
	glide := false
	if t.selected != t.shown && t.selected < len(t.spans) {
		t.shown = t.selected
		sp := t.spans[t.selected]
		switch {
		case sp[0]-tabsRoom < to:
			to, glide = t.clampOff(sp[0]-tabsRoom), true
		case sp[1]+tabsRoom > to+t.room:
			to, glide = t.clampOff(sp[1]+tabsRoom-t.room), true
		}
	}
	if to == t.offTo {
		return
	}
	if t.laid && glide {
		t.scrollTo(to, Quick.Get(th))
	} else {
		t.offTo = to
		t.off.Jump(to)
	}
}

// clampOff returns x kept between the titles' ends.
func (t *Tabs) clampOff(x float32) float32 { return max(0, min(x, t.wide-t.room)) }

// scrollTo carries the titles to x with motion.
func (t *Tabs) scrollTo(x float32, motion anim.Motion) {
	t.offTo = x
	t.off.Retarget(x, motion)
}

// Paint implements [gunim.Node].
func (b *tabBar) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	t := b.t
	th := f.Theme
	pad := TabPadding.Get(th)
	in := t.inset(th)
	p.RRect(geom.Rc(in, t.head-1, max(0, box.W-2*in), 1), 0, paint.Solid(FieldBorder.Get(th)))
	if t.wide > t.room {
		// The titles fade where more of them lie past the edge.
		at, fade := t.off.Value(), ScrollFade.Get(th)
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: 1, Clip: true,
			Fade: geom.Insets{Left: fadeFor(at, fade), Right: fadeFor(t.wide-t.room-at, fade)}})()
	}
	defer p.Push(paint.Translate(geom.Pt(-t.off.Value(), 0)))()
	for i := range t.Titles {
		run := t.shaped[i].run
		ink := Placeholder.Get(th)
		switch {
		case flag(t.Disabled, i):
			ink.A /= 3
		case i == t.selected:
			ink = Ink.Get(th)
		}
		x := t.spans[i][0] + pad
		if ic := t.icon(i); ic != nil {
			s := IconSize.Get(th)
			drawIcon(p, th, ic, geom.Rc(x, (t.head-s)/2, s, s), ink, 1)
		}
		run.Paint(p, geom.Pt(x+t.iconRoom(i, th), (t.head-run.Height())/2), ink)
	}
	line := t.line.Value()
	p.RRect(geom.Rect{Min: geom.Pt(line.X, t.head-2.5), Max: geom.Pt(line.Y, t.head-0.5)}, 1, paint.Solid(Accent.Get(th)))
	if r := t.ring.Value(); r > 0.01 && t.selected < len(t.spans) {
		sp := t.spans[t.selected]
		FocusRing(p, geom.Rc(sp[0]+3, 5, sp[1]-sp[0]-6, t.head-13), FocusRadius.Get(th), r, th)
	}
}

// Layout implements [gunim.Node]. The titles sit in a row across the
// top; the page shown, and the one sliding out, are laid out below them
// at the size left. The other pages stay mounted, unlaid, and so keep
// their state and their scroll.
func (t *Tabs) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	t.count = kids.Len() - 1
	t.laidPages = t.laidPages[:0]
	for i := 1; i < kids.Len(); i++ {
		t.laidPages = append(t.laidPages, kids.At(i).Node())
	}
	bar := kids.At(0)
	bs := bar.Layout(gunim.Constraints{Max: geom.Sz(c.Max.W, 0)})
	bar.Place(geom.Point{})
	size := c.Max
	if size.W <= 0 {
		size.W = bs.W
	}
	// The pages start a gap below the titles' line.
	top := t.head + TabGap.Get(f.Theme)
	page := geom.Sz(size.W, max(0, size.H-top))
	var tallest float32
	// Asked for no particular height, the tabs are as tall as their tallest page, so a dialog holding them keeps its
	// size from tab to tab; every page is measured for that. Given a height, only the pages showing are laid out.
	measure := size.H <= 0
	for i := 1; i < kids.Len(); i++ {
		if !measure && i-1 != t.selected && i-1 != t.prev {
			continue
		}
		kid := kids.At(i)
		s := kid.Layout(gunim.Constraints{Min: geom.Sz(page.W, 0), Max: page})
		kid.Place(geom.Pt(0, top))
		tallest = max(tallest, s.H)
	}
	if size.H <= 0 {
		size.H = top + tallest
	}
	return c.Constrain(size)
}

// Paint implements [gunim.Node].
func (t *Tabs) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)

	// The pages, clipped below the titles: the old one fading where it
	// was, the new one sliding in over it.
	body := geom.Rect{Min: geom.Pt(0, t.head), Max: box.Point()}
	defer p.Layer(paint.LayerOpts{Bounds: body, Opacity: 1, Clip: true})()
	s := min(max(t.slide.Value(), 0), 1)
	if t.prev >= 0 && t.prev+1 < kids.Len() && s < 1 {
		closeOld := p.Layer(paint.LayerOpts{Bounds: body, Opacity: 1 - s})
		kids.At(t.prev + 1).Paint(p)
		closeOld()
	}
	if s >= 1 {
		t.prev = -1
	}
	if t.selected+1 < kids.Len() {
		shift := t.from * (1 - t.slide.Value()) * box.W * 0.15
		closeNew := p.Layer(paint.LayerOpts{Bounds: body, Opacity: s})
		defer closeNew()
		defer p.Push(paint.Translate(geom.Pt(shift, 0)))()
		kids.At(t.selected + 1).Paint(p)
	}
}

// FocusRing draws the ring a control shows around r, its corners radius round, as Tab brings the keyboard to it: a
// line 3 pixels out in the accent colour, at strength t from 0, no ring, to 1. It stays in place and fades: animate t
// with the control's own value on [RingFade], as [Button] does, so an app's node rings the way the widgets do.
func FocusRing(p *paint.Painter, r geom.Rect, radius, t float32, th *theme.Live) {
	if t <= 0.01 {
		return
	}
	ring := Accent.Get(th)
	ring.A = uint8(float32(ring.A) * 0.56 * min(t, 1))
	const out = 3
	p.RRectStroke(geom.Rect{Min: geom.Pt(r.Min.X-out, r.Min.Y-out), Max: geom.Pt(r.Max.X+out, r.Max.Y+out)},
		radius+out, paint.Fill{}, paint.Stroke{Width: 2, Color: ring})
}

// ringTo is where a focus ring goes for e: 1 for an [input.FocusRing] that turns it on, and 0 otherwise.
func ringTo(e input.Event) float32 {
	if r, ok := e.(input.FocusRing); ok && r.On {
		return 1
	}
	return 0
}

// ringCue follows [input.FocusRing] for a widget with a cursor inside it: on says it shows it has the keyboard, and
// whole that it draws the ring round all of itself, as no group round it does.
type ringCue struct {
	on, whole bool
}

// follow takes e.
func (c *ringCue) follow(e input.FocusRing) { c.on, c.whole = e.On, e.On && !e.Grouped }

// GroupRing draws the ring round a whole group or list that has the keyboard: a faint accent line 1 pixel wide just
// inside r, its corners radius round, at strength t from 0 to 1. It stays quieter than the [FocusRing] of the control
// inside it. A view with a cursor of its own, such as a calendar, draws it round itself when the keyboard comes by Tab.
func GroupRing(p *paint.Painter, r geom.Rect, radius, t float32, th *theme.Live) {
	if t <= 0.01 {
		return
	}
	c := Accent.Get(th)
	c.A = uint8(float32(c.A) * 0.4 * min(t, 1))
	p.RRectStroke(r.Inset(geom.Uniform(0.5)), max(radius-0.5, 0), paint.Fill{}, paint.Stroke{Width: 1, Color: c})
}

// bar draws a straight stroke from a to b, thick wide, with round ends.
func bar(p *paint.Painter, a, b geom.Point, thick float32, c color.NRGBA) {
	d := b.Sub(a)
	length := float32(math.Hypot(float64(d.X), float64(d.Y)))
	mid := geom.Pt((a.X+b.X)/2, (a.Y+b.Y)/2)
	angle := float32(math.Atan2(float64(d.Y), float64(d.X)))
	defer p.Push(paint.Rotate(angle, mid))()
	half := (length + thick) / 2
	p.RRect(geom.Rect{Min: geom.Pt(mid.X-half, mid.Y-thick/2), Max: geom.Pt(mid.X+half, mid.Y+thick/2)}, thick/2, paint.Solid(c))
}

func lerpPt(a, b geom.Point, t float32) geom.Point {
	return geom.Pt(a.X+(b.X-a.X)*t, a.Y+(b.Y-a.Y)*t)
}

func value(on bool) float32 {
	if on {
		return 1
	}
	return 0
}

// enabled returns the first tab from i on, going by dir, that can be
// chosen, or -1.
func (t *Tabs) enabled(i, dir int) int {
	for ; i >= 0 && i < t.count; i += dir {
		if !flag(t.Disabled, i) {
			return i
		}
	}
	return -1
}
