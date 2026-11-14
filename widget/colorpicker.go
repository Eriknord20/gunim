package widget

import (
	"fmt"
	"image/color"
	"math"
	"strconv"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// The colour picker's tokens.
var (
	// ColorPickerSize is the width of a colour picker, and the side of
	// its square.
	ColorPickerSize = theme.Length("colorpicker.size", 200)
	// ColorPickerStrip is the height of the hue and opacity strips.
	ColorPickerStrip = theme.Length("colorpicker.strip", 14)
	// ColorPickerGap is the room between the picker's parts.
	ColorPickerGap = theme.Length("colorpicker.gap", 10)
	// ColorPickerRadius rounds the square, the strips and the swatches.
	ColorPickerRadius = theme.Length("colorpicker.radius", 6)
	// ColorPickerKnob is the size of the knobs on the square and the strips.
	ColorPickerKnob = theme.Length("colorpicker.knob", 14)
	// ColorPickerChecker is the size of the squares of the checks shown
	// through a colour that is not opaque, and ColorPickerCheckLight and
	// ColorPickerCheckDark their colours.
	ColorPickerChecker    = theme.Length("colorpicker.checker", 6)
	ColorPickerCheckLight = theme.Color("colorpicker.check.light", color.NRGBA{R: 0x8c, G: 0x91, B: 0x9c, A: 0xff})
	ColorPickerCheckDark  = theme.Color("colorpicker.check.dark", color.NRGBA{R: 0x4c, G: 0x51, B: 0x5c, A: 0xff})
	// ColorChipWidth and ColorChipHeight are the size of the chip a
	// [ColorButton] with Hex shows its colour in, ColorChipRadius rounds
	// it, and ColorHexSize is the size of the hex beside it.
	ColorChipWidth  = theme.Length("colorbutton.chip.width", 28)
	ColorChipHeight = theme.Length("colorbutton.chip.height", 20)
	ColorChipRadius = theme.Length("colorbutton.chip.radius", 5)
	ColorHexSize    = theme.FontSize("colorbutton.hex.size", 12)
)

// ColorPicker picks a colour: a square of saturation across and
// brightness up, a strip of hues under it, a strip of opacity, and a
// field for the colour written in hex. A swatch beside the field shows
// the colour the picker started with beside the one picked.
//
// A press on the square or a strip moves its knob there, and a drag
// carries it along; the knobs glide to a colour set from outside or by
// a key. With the keyboard on the square, the arrow keys move its knob,
// a hundredth of the way, a tenth with Shift. On a strip, Left and
// Right do the same, and Home and End go to its ends. The hex field
// takes "#rgb", "#rrggbb" and "#rrggbbaa", and Enter in it commits. The
// swatch of the old colour, clicked or pressed with Space or Enter,
// brings it back.
type ColorPicker struct {
	anim.Group
	// Label names the picker for a screen reader, as the label beside
	// it does on screen.
	Label string
	// Opaque leaves out the strip of opacity, for a colour that is
	// always opaque. The picked colour's alpha is then always full.
	Opaque bool
	// OnChange runs on the UI goroutine as the user changes the colour,
	// on every step of a drag and each key typed in the hex field. It
	// may act in the window through u, such as a preview that follows;
	// a non-nil result is sent to the application as the picker's
	// intent.
	OnChange func(c color.NRGBA, u *gunim.UI) gunim.Intent
	// OnCommit runs, in the same way, with the colour a gesture ends on:
	// as a drag is let go, a key moves a knob, Enter is pressed in the
	// hex field, or the old colour is brought back. It runs once per
	// change made, for an application that saves, or keeps undo history.
	OnCommit func(c color.NRGBA, u *gunim.UI) gunim.Intent

	// h, s, v and a are the colour picked: hue, saturation, brightness
	// and opacity, each from 0 to 1. The hue stays where it was while
	// the colour is grey, where it shows nowhere in the colour.
	h, s, v, a float32
	// was is the colour the picker started with.
	was color.NRGBA
	// hue and alpha are where the strips' knobs show, and sv the
	// square's, from 0 to 1.
	hue, alpha *anim.Float
	sv         *anim.Point

	plane  *pickPlane
	hueBar *pickStrip
	alpBar *pickStrip
	swatch *pickSwatch
	hex    *TextField
	laid   bool
	// done, set by a [ColorButton], closes the popup the picker is in.
	done func(u *gunim.UI)
}

// NewColorPicker returns a picker holding c, which its swatch shows as
// the old colour.
func NewColorPicker(c color.NRGBA) *ColorPicker {
	p := &ColorPicker{was: c, hue: anim.NewFloat(0), alpha: anim.NewFloat(1), sv: anim.NewPoint(geom.Point{})}
	p.Add(p.hue, p.alpha, p.sv)
	p.plane = &pickPlane{Control: newControl(), p: p}
	p.hueBar = &pickStrip{Control: newControl(), p: p}
	p.alpBar = &pickStrip{Control: newControl(), p: p, opacity: true}
	p.swatch = &pickSwatch{Control: newControl(), p: p}
	p.hex = NewTextField()
	p.hex.Placeholder, p.hex.Face = "Hex colour", MonoFont
	p.hex.OnChange = func(s string, u *gunim.UI) gunim.Intent {
		if typed, err := theme.ParseHex(s); err == nil {
			p.setColor(typed, Quick.Get(u.Theme()), false, u)
		}
		return nil
	}
	p.hex.OnCommit = func(s string, u *gunim.UI) gunim.Intent {
		typed, err := theme.ParseHex(s)
		if err != nil {
			u.Cue(gunim.CueError, p.hex)
			p.hex.Flash()
			p.hex.SetText(theme.Hex(p.Value()), u)
			return nil
		}
		p.setColor(typed, Quick.Get(u.Theme()), true, u)
		p.commit(u)
		if p.done != nil {
			u.Cue(gunim.CueClose, p)
			p.done(u)
		}
		return nil
	}
	// Escape in the field closes the popup the picker is in, as it does
	// anywhere else in the picker.
	p.hex.Keys = func(e input.KeyPress, u *gunim.UI) bool {
		if e.Key != input.KeyEscape || e.Mods != 0 || p.done == nil {
			return false
		}
		u.Cue(gunim.CueClose, p)
		p.done(u)
		return true
	}
	p.SetValue(c, nil)
	return p
}

// Value returns the colour picked.
func (p *ColorPicker) Value() color.NRGBA {
	a := p.a
	if p.Opaque {
		a = 1
	}
	return hsvColor(p.h, p.s, p.v, a)
}

// SetValue sets the colour and sends no intent. Once the picker is laid
// out the knobs glide to it; before that, or with a nil u, they jump.
func (p *ColorPicker) SetValue(c color.NRGBA, u *gunim.UI) {
	var m anim.Motion
	if p.laid && u != nil {
		m = Quick.Get(u.Theme())
	}
	p.setColor(c, m, true, u)
}

// Original returns the colour the swatch shows as the old one.
func (p *ColorPicker) Original() color.NRGBA { return p.was }

// SetOriginal makes c the colour the swatch shows as the old one, and
// the one it brings back.
func (p *ColorPicker) SetOriginal(c color.NRGBA, u *gunim.UI) {
	p.was = c
	u.Invalidate()
}

// setColor takes c as the colour picked, the knobs moving with m, or
// jumping for a nil m. It rewrites the hex field when text is set, and
// tells the application when the colour changed, with a u.
func (p *ColorPicker) setColor(c color.NRGBA, m anim.Motion, text bool, u *gunim.UI) {
	h, s, v := colorHSV(c)
	// A grey keeps the hue it had, and black the saturation too, so the
	// knobs stay where the user left them.
	if s == 0 || v == 0 {
		h = p.h
	}
	if v == 0 {
		s = p.s
	}
	p.pick(h, s, v, float32(c.A)/255, m, u)
	if text {
		p.hex.SetText(theme.Hex(p.Value()), u)
	}
}

// pick sets the colour from its hue, saturation, brightness and
// opacity, the knobs moving with m, or jumping for a nil m, and tells
// the application, through u, when the colour changed.
func (p *ColorPicker) pick(h, s, v, a float32, m anim.Motion, u *gunim.UI) {
	was := p.Value()
	p.h, p.s, p.v, p.a = clamp01(h), clamp01(s), clamp01(v), clamp01(a)
	if m == nil {
		p.hue.Jump(p.h)
		p.sv.Jump(geom.Pt(p.s, p.v))
		p.alpha.Jump(p.a)
	} else {
		p.hue.Animate(p.h, m)
		p.sv.Animate(geom.Pt(p.s, p.v), m)
		p.alpha.Animate(p.a, m)
	}
	if u == nil || p.Value() == was {
		return
	}
	u.Invalidate()
	if p.OnChange != nil {
		send(u, p, p.OnChange(p.Value(), u))
	}
}

// moved is pick for a knob the user moved, which rewrites the hex
// field.
func (p *ColorPicker) moved(h, s, v, a float32, m anim.Motion, u *gunim.UI) {
	p.pick(h, s, v, a, m, u)
	p.hex.SetText(theme.Hex(p.Value()), u)
}

// commit tells the application the colour a gesture ended on.
func (p *ColorPicker) commit(u *gunim.UI) {
	if p.OnCommit != nil {
		send(u, p, p.OnCommit(p.Value(), u))
	}
}

// Children implements [gunim.Composite]: the square, the strips, the
// swatch and the hex field, in the order Tab goes through them.
func (p *ColorPicker) Children() []gunim.Node {
	return []gunim.Node{p.plane, p.hueBar, p.alpBar, p.swatch, p.hex}
}

// Layout implements [gunim.Node]. The picker is [ColorPickerSize] wide,
// or as wide as it is given where that is less.
func (p *ColorPicker) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	th := f.Theme
	p.laid = true
	w := ColorPickerSize.Get(th)
	if c.Max.W > 0 {
		w = min(w, c.Max.W)
	}
	gap, strip, fh := ColorPickerGap.Get(th), ColorPickerStrip.Get(th), FieldHeight.Get(th)
	y := float32(0)
	put := func(i int, r geom.Rect) {
		kids.At(i).Layout(gunim.Tight(r.Size()))
		kids.At(i).Place(r.Min)
	}
	put(0, geom.Rc(0, 0, w, w))
	y += w + gap
	put(1, geom.Rc(0, y, w, strip))
	y += strip + gap
	if p.Opaque {
		put(2, geom.Rc(0, y, 0, 0))
	} else {
		put(2, geom.Rc(0, y, w, strip))
		y += strip + gap
	}
	sw := min(2*fh, w/2)
	put(3, geom.Rc(0, y, sw, fh))
	put(4, geom.Rc(sw+gap, y, max(0, w-sw-gap), fh))
	// The field shows the colour written out in full once the keyboard
	// has left it, whatever was typed there.
	if u := f.UI(); u != nil && !u.HasFocus(p.hex) {
		if want := theme.Hex(p.Value()); p.hex.Text() != want {
			p.hex.SetText(want, u)
		}
	}
	return c.Constrain(geom.Sz(w, y+fh))
}

// Paint implements [gunim.Node].
func (p *ColorPicker) Paint(pt *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for i := range kids.Len() {
		if i == 2 && p.Opaque {
			continue
		}
		kids.At(i).Paint(pt)
	}
}

// Access implements [gunim.Accessible]: a group named by its label,
// with the colour in hex as its value.
func (p *ColorPicker) Access() access.Info {
	return access.Info{Role: access.RoleGroup, Name: p.Label, Value: theme.Hex(p.Value())}
}

// Handle implements [gunim.Handler]: in a popup a [ColorButton] opened,
// Escape and Enter close it.
func (p *ColorPicker) Handle(e input.Event, u *gunim.UI) bool {
	k, ok := e.(input.KeyPress)
	if !ok || p.done == nil || k.Mods != 0 {
		return false
	}
	if k.Key != input.KeyEscape && k.Key != input.KeyEnter && k.Key != input.KeyKPEnter {
		return false
	}
	u.Cue(gunim.CueClose, p)
	p.done(u)
	return true
}

// pickPlane is a colour picker's square: saturation across, brightness
// up, in the picker's hue.
type pickPlane struct {
	Control
	p    *ColorPicker
	size geom.Size
}

// at returns the saturation and brightness at pos.
func (q *pickPlane) at(pos geom.Point) (s, v float32) {
	return clamp01(pos.X / max(q.size.W, 1)), clamp01(1 - pos.Y/max(q.size.H, 1))
}

// DragsTouch implements [gunim.TouchDragger]: a finger on the square
// drags the knob, where it would scroll the page round it.
func (q *pickPlane) DragsTouch() bool { return q.held }

// Handle implements [gunim.Handler].
func (q *pickPlane) Handle(e input.Event, u *gunim.UI) bool {
	q.showTip(e, u, q)
	th := u.Theme()
	p := q.p
	switch e := e.(type) {
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		q.held = true
		s, v := q.at(e.Pos)
		p.moved(p.h, s, v, p.a, Quick.Get(th), u)
	case input.PointerMove:
		if !q.held {
			return false
		}
		s, v := q.at(e.Pos)
		p.moved(p.h, s, v, p.a, Caret.Get(th), u)
	case input.PointerUp:
		if !q.held {
			return false
		}
		q.held = false
		p.commit(u)
	case input.KeyPress:
		if e.Mods.Has(input.ModControl) || e.Mods.Has(input.ModAlt) {
			return false
		}
		step := pickStep(e.Mods)
		s, v := p.s, p.v
		switch e.Key {
		case input.KeyLeft:
			s -= step
		case input.KeyRight:
			s += step
		case input.KeyUp:
			v += step
		case input.KeyDown:
			v -= step
		default:
			return false
		}
		was := p.Value()
		p.moved(p.h, s, v, p.a, Quick.Get(th), u)
		if p.Value() != was {
			p.commit(u)
		}
	default:
		if !q.ringFollows(e, th) {
			return false
		}
	}
	u.Invalidate()
	return true
}

// pickStep is how far an arrow key moves a knob: a hundredth of the
// way, a tenth with Shift.
func pickStep(m input.Mods) float32 {
	if m.Has(input.ModShift) {
		return 0.1
	}
	return 0.01
}

// Layout implements [gunim.Node].
func (q *pickPlane) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	q.follow(f.Theme)
	side := ColorPickerSize.Get(f.Theme)
	q.size = c.Constrain(geom.Sz(side, side))
	return q.size
}

// Paint implements [gunim.Node].
func (q *pickPlane) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	r := geom.Rect{Max: box.Point()}
	radius := ColorPickerRadius.Get(th)
	q.paintRing(pt, r, radius, th)
	// The hue at full strength, whitened toward the left and darkened
	// toward the bottom.
	pt.RRect(r, radius, paint.Solid(hsvColor(q.p.hue.Value(), 1, 1, 1)))
	white := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	none := white
	none.A = 0
	pt.RRect(r, radius, paint.Fill{Gradient: &paint.Gradient{From: r.Min, To: geom.Pt(r.Max.X, r.Min.Y), Start: white, End: none}})
	pt.RRect(r, radius, paint.Fill{Gradient: &paint.Gradient{From: r.Min, To: geom.Pt(r.Min.X, r.Max.Y), Start: color.NRGBA{}, End: color.NRGBA{A: 0xff}}})
	at := q.p.sv.Value()
	paintPickKnob(pt, th, geom.Pt(at.X*box.W, (1-at.Y)*box.H), hsvColor(q.p.hue.Value(), at.X, at.Y, 1), q.hover.Value())
}

// Access implements [gunim.Accessible].
func (q *pickPlane) Access() access.Info {
	p := q.p
	return access.Info{Role: access.RoleSlider, Name: "Saturation and brightness",
		Value: fmt.Sprintf("saturation %d%%, brightness %d%%", percent(p.s), percent(p.v))}
}

// pickStrip is a colour picker's strip of hues, or of opacity.
type pickStrip struct {
	Control
	p *ColorPicker
	// opacity makes it the strip of opacity.
	opacity bool
	size    geom.Size
}

// Focusable implements [gunim.Focusable]: the strip of opacity takes no
// keyboard while the picker leaves it out.
func (q *pickStrip) Focusable() bool { return !q.opacity || !q.p.Opaque }

// DragsTouch implements [gunim.TouchDragger].
func (q *pickStrip) DragsTouch() bool { return q.held }

// value returns where the strip's knob is heading, from 0 to 1.
func (q *pickStrip) value() float32 {
	if q.opacity {
		return q.p.a
	}
	return q.p.h
}

// set moves the strip's knob to t, from 0 to 1, with m.
func (q *pickStrip) set(t float32, m anim.Motion, u *gunim.UI) {
	p := q.p
	if q.opacity {
		p.moved(p.h, p.s, p.v, t, m, u)
	} else {
		p.moved(t, p.s, p.v, p.a, m, u)
	}
}

// at returns the value at x.
func (q *pickStrip) at(x float32) float32 { return clamp01(x / max(q.size.W, 1)) }

// Handle implements [gunim.Handler].
func (q *pickStrip) Handle(e input.Event, u *gunim.UI) bool {
	q.showTip(e, u, q)
	th := u.Theme()
	switch e := e.(type) {
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		q.held = true
		q.set(q.at(e.Pos.X), Quick.Get(th), u)
	case input.PointerMove:
		if !q.held {
			return false
		}
		q.set(q.at(e.Pos.X), Caret.Get(th), u)
	case input.PointerUp:
		if !q.held {
			return false
		}
		q.held = false
		q.p.commit(u)
	case input.KeyPress:
		if e.Mods.Has(input.ModControl) || e.Mods.Has(input.ModAlt) {
			return false
		}
		t := q.value()
		switch e.Key {
		case input.KeyLeft, input.KeyDown:
			t -= pickStep(e.Mods)
		case input.KeyRight, input.KeyUp:
			t += pickStep(e.Mods)
		case input.KeyHome:
			t = 0
		case input.KeyEnd:
			t = 1
		default:
			return false
		}
		was := q.p.Value()
		q.set(t, Quick.Get(th), u)
		if q.p.Value() != was {
			q.p.commit(u)
		}
	default:
		if !q.ringFollows(e, th) {
			return false
		}
	}
	u.Invalidate()
	return true
}

// Layout implements [gunim.Node].
func (q *pickStrip) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	q.follow(f.Theme)
	q.size = c.Constrain(geom.Sz(ColorPickerSize.Get(f.Theme), ColorPickerStrip.Get(f.Theme)))
	return q.size
}

// hueStops are the hues round the colour wheel, at sixths.
var hueStops = func() []paint.Stop {
	out := make([]paint.Stop, 0, 5)
	for i := 1; i < 6; i++ {
		out = append(out, paint.Stop{At: float32(i) / 6, Color: hsvColor(float32(i)/6, 1, 1, 1)})
	}
	return out
}()

// Paint implements [gunim.Node].
func (q *pickStrip) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	r := geom.Rect{Max: box.Point()}
	radius := min(ColorPickerRadius.Get(th), box.H/2)
	q.paintRing(pt, r, radius, th)
	p := q.p
	var t float32
	var knob color.NRGBA
	if q.opacity {
		paintChecks(pt, th, r, radius)
		c := hsvColor(p.hue.Value(), p.sv.Value().X, p.sv.Value().Y, 1)
		none := c
		none.A = 0
		pt.RRect(r, radius, paint.Fill{Gradient: &paint.Gradient{From: r.Min, To: geom.Pt(r.Max.X, r.Min.Y), Start: none, End: c}})
		t, knob = p.alpha.Value(), c
	} else {
		red := hsvColor(0, 1, 1, 1)
		pt.RRect(r, radius, paint.Fill{Gradient: &paint.Gradient{From: r.Min, To: geom.Pt(r.Max.X, r.Min.Y), Start: red, End: red, Stops: hueStops}})
		t = p.hue.Value()
		knob = hsvColor(t, 1, 1, 1)
	}
	paintPickKnob(pt, th, geom.Pt(clamp01(t)*box.W, box.H/2), knob, q.hover.Value())
}

// Access implements [gunim.Accessible]: a slider of hue in degrees, or
// of opacity in percent.
func (q *pickStrip) Access() access.Info {
	if q.opacity {
		v := float64(percent(q.p.a))
		return access.Info{Role: access.RoleSlider, Name: "Opacity", Value: strconv.Itoa(int(v)) + "%",
			Range: &access.Range{Min: 0, Max: 100, Value: v, Step: 1}}
	}
	v := math.Round(float64(q.p.h) * 360)
	return access.Info{Role: access.RoleSlider, Name: "Hue", Value: strconv.Itoa(int(v)) + "°",
		Range: &access.Range{Min: 0, Max: 360, Value: v, Step: 1}}
}

// AccessAct implements [gunim.AccessActor]: a new value.
func (q *pickStrip) AccessAct(r access.Request, u *gunim.UI) bool {
	if !r.SetValue {
		return false
	}
	scale := 360.0
	if q.opacity {
		scale = 100
	}
	q.set(float32(r.Value/scale), Quick.Get(u.Theme()), u)
	q.p.commit(u)
	return true
}

// pickSwatch shows the colour a picker started with beside the one
// picked, and brings the old one back when pressed.
type pickSwatch struct {
	Control
	p     *ColorPicker
	click Clicker
}

// Handle implements [gunim.Handler].
func (q *pickSwatch) Handle(e input.Event, u *gunim.UI) bool {
	q.showTip(e, u, q)
	th := u.Theme()
	switch e := e.(type) {
	case input.PointerEnter:
		q.hover.Animate(1, Quick.Get(th))
	case input.PointerLeave:
		q.hover.Animate(0, Settle.Get(th))
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		q.click.Press(e, 0)
	case input.PointerUp:
		if q.click.Release(e, 0) {
			q.restore(u)
		}
	case input.KeyPress:
		if e.Mods != 0 || (e.Key != input.KeySpace && e.Key != input.KeyEnter && e.Key != input.KeyKPEnter) {
			return false
		}
		q.restore(u)
	default:
		if !q.ringFollows(e, th) {
			return false
		}
	}
	u.Invalidate()
	return true
}

// restore brings back the colour the picker started with.
func (q *pickSwatch) restore(u *gunim.UI) {
	p := q.p
	if p.Value() == p.was {
		return
	}
	u.Cue(gunim.CuePress, q)
	p.setColor(p.was, Quick.Get(u.Theme()), true, u)
	p.commit(u)
}

// Layout implements [gunim.Node].
func (q *pickSwatch) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	q.follow(f.Theme)
	h := FieldHeight.Get(f.Theme)
	return c.Constrain(geom.Sz(2*h, h))
}

// Paint implements [gunim.Node]: the old colour on the left, the new on
// the right, over checks.
func (q *pickSwatch) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	r := geom.Rect{Max: box.Point()}
	radius := ColorPickerRadius.Get(th)
	q.paintRing(pt, r, radius, th)
	paintChecks(pt, th, r, radius)
	half := box.W / 2
	now := hsvColor(q.p.hue.Value(), q.p.sv.Value().X, q.p.sv.Value().Y, q.p.alpha.Value())
	if q.p.Opaque {
		now.A = 0xff
	}
	old := q.p.was
	// The old half lightens under the pointer, as a button does.
	old = anim.Mix(anim.ColorCodec, old, anim.Mix(anim.ColorCodec, old, Ink.Get(th), 0.2), q.hover.Value())
	pt.RRect(r, radius, paint.Solid(now))
	func() {
		defer pt.Layer(paint.LayerOpts{Bounds: geom.Rc(0, 0, half, box.H), Opacity: 1, Clip: true})()
		pt.RRect(r, radius, paint.Solid(old))
	}()
	// A line between the two, and a border round both, so either shows
	// against the panel and the other whatever their colours.
	border := FieldBorder.Get(th)
	pt.RRect(geom.Rc(half-0.5, 0, 1, box.H), 0, paint.Solid(border))
	pt.RRectStroke(r, radius, paint.Fill{}, paint.Stroke{Width: 1, Color: border})
}

// Access implements [gunim.Accessible].
func (q *pickSwatch) Access() access.Info {
	return access.Info{Role: access.RoleButton, Name: "Back to " + theme.Hex(q.p.was), Actions: []string{access.ActionPress}}
}

// AccessAct implements [gunim.AccessActor].
func (q *pickSwatch) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress {
		return false
	}
	q.restore(u)
	return true
}

// paintPickKnob draws a picker's knob at c, filled with fill, grown by
// hover.
func paintPickKnob(pt *paint.Painter, th *theme.Live, c geom.Point, fill color.NRGBA, hover float32) {
	d := ColorPickerKnob.Get(th) * (1 + 0.2*max(hover, 0))
	r := geom.Rc(c.X-d/2, c.Y-d/2, d, d)
	pt.ShadowRRect(r, d/2, paint.Solid(Knob.Get(th)), paint.Shadow{Offset: geom.Pt(0, 1), Blur: 3, Color: KnobShadow.Get(th)})
	in := r.Inset(geom.Uniform(2.5))
	pt.RRect(in, in.Size().W/2, paint.Solid(fill))
}

// paintChecks draws checks in r, rounded by radius, to show through a
// colour that is not opaque.
func paintChecks(pt *paint.Painter, th *theme.Live, r geom.Rect, radius float32) {
	pt.RRect(r, radius, paint.Solid(ColorPickerCheckLight.Get(th)))
	s := max(ColorPickerChecker.Get(th), 2)
	dark := paint.Solid(ColorPickerCheckDark.Get(th))
	defer pt.Layer(paint.LayerOpts{Bounds: r, Opacity: 1, Clip: true, Radius: radius})()
	for row, y := 0, r.Min.Y; y < r.Max.Y; row, y = row+1, y+s {
		for col, x := 0, r.Min.X; x < r.Max.X; col, x = col+1, x+s {
			if (row+col)%2 == 1 {
				pt.RRect(geom.Rect{Min: geom.Pt(x, y), Max: geom.Pt(min(x+s, r.Max.X), min(y+s, r.Max.Y))}, 0, dark)
			}
		}
	}
}

// ColorButton shows a colour as a swatch, and opens a [ColorPicker] in
// a popup below it to pick another. The picker takes the keyboard while
// it is open, and gives it back as it closes.
//
// A click, Space, Enter or Down opens the picker. In the picker, Tab
// goes from part to part, and Escape or Enter closes it, as a press
// outside it does. Each change shows in the swatch at once.
type ColorButton struct {
	Control
	// Label names the button for a screen reader, as the label beside it
	// does on screen.
	Label string
	// Opaque leaves out the picker's strip of opacity.
	Opaque bool
	// Hex shows the colour as a small chip with the colour written in
	// hex before it, in the mono face, as a list of colours does. The
	// whole of it is the button, clear until the pointer comes over it.
	Hex bool
	// OnChange runs on the UI goroutine as the user changes the colour in
	// the picker, on every step of a drag; OnCommit runs with the colour
	// each gesture ends on. A non-nil result is sent to the application
	// as the button's intent.
	OnChange func(c color.NRGBA, u *gunim.UI) gunim.Intent
	OnCommit func(c color.NRGBA, u *gunim.UI) gunim.Intent

	value  color.NRGBA
	popup  *gunim.Popup
	picker *ColorPicker
	size   geom.Size
	// hexText is the colour in hex, and hexRoom the hex's room: as wide
	// as a colour with opacity takes, so the chips of a column line up.
	hexText, hexRoom shapedText
}

// NewColorButton returns a button showing c.
func NewColorButton(c color.NRGBA) *ColorButton {
	return &ColorButton{Control: newControl(), value: c}
}

// Value returns the colour the button shows.
func (b *ColorButton) Value() color.NRGBA { return b.value }

// SetValue sets the colour and sends no intent. The picker open moves to
// it too. u may be nil, as before the button is laid out.
func (b *ColorButton) SetValue(c color.NRGBA, u *gunim.UI) {
	b.value = c
	if b.picker != nil && b.IsOpen() && b.picker.Value() != c {
		b.picker.SetValue(c, u)
	}
	u.Invalidate()
}

// IsOpen reports whether the picker is open.
func (b *ColorButton) IsOpen() bool { return b.popup != nil && b.popup.Open() }

// Picker returns the picker open, or nil.
func (b *ColorButton) Picker() *ColorPicker {
	if !b.IsOpen() {
		return nil
	}
	return b.picker
}

// Open shows the picker below the button and gives it the keyboard, as
// a click does.
func (b *ColorButton) Open(u *gunim.UI) {
	if b.IsOpen() {
		return
	}
	p := NewColorPicker(b.value)
	p.Label, p.Opaque = b.Label, b.Opaque
	p.OnChange = func(c color.NRGBA, u *gunim.UI) gunim.Intent {
		b.value = c
		if b.OnChange != nil {
			send(u, b, b.OnChange(c, u))
		}
		return nil
	}
	p.OnCommit = func(c color.NRGBA, u *gunim.UI) gunim.Intent {
		b.value = c
		if b.OnCommit != nil {
			send(u, b, b.OnCommit(c, u))
		}
		return nil
	}
	p.done = b.close
	b.picker = p
	u.Cue(gunim.CueOpen, b)
	b.popup = u.OpenPopup(b, newPickPopup(p, b.Label), gunim.PopupOptions{
		Anchor:  geom.Rc(0, 0, b.size.W, b.size.H+4),
		Dismiss: b.close,
	})
	u.Focus(p.plane)
}

// close takes the picker away; the keyboard goes back to the button.
func (b *ColorButton) close(u *gunim.UI) {
	if b.popup != nil {
		b.popup.Close()
		b.popup = nil
	}
	u.Invalidate()
}

// Handle implements [gunim.Handler].
func (b *ColorButton) Handle(e input.Event, u *gunim.UI) bool {
	b.showTip(e, u, b)
	th := u.Theme()
	if b.Disabled {
		return b.handleDisabled(e, u, b.close)
	}
	switch e := e.(type) {
	case input.PointerEnter:
		b.hover.Animate(1, Quick.Get(th))
	case input.PointerLeave:
		b.hover.Animate(0, Settle.Get(th))
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if b.IsOpen() {
			b.close(u)
		} else {
			b.Open(u)
		}
	case input.PointerUp:
	case input.KeyPress:
		if e.Mods != 0 {
			return false
		}
		switch e.Key {
		case input.KeySpace, input.KeyEnter, input.KeyKPEnter, input.KeyDown:
			b.Open(u)
		default:
			return false
		}
	default:
		if !b.ringFollows(e, th) {
			return false
		}
	}
	u.Invalidate()
	return true
}

// Layout implements [gunim.Node]. The button is as tall as a field and
// half as wide again. It closes the picker as it is disabled.
func (b *ColorButton) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	b.follow(th)
	if b.Disabled && b.popup != nil {
		b.popup.Close()
		b.popup = nil
	}
	if b.Hex {
		face, size := faceIn(MonoFont, th), ColorHexSize.Get(th)
		b.hexText.shape(face, theme.Hex(b.value), size)
		room := b.hexRoom.shape(face, "#00000000", size).Advance
		pad := ControlGap.Get(th)
		b.size = c.Constrain(geom.Sz(pad+room+pad+ColorChipWidth.Get(th)+pad/2, ControlHeight.Get(th)))
		return b.size
	}
	h := FieldHeight.Get(th)
	b.size = c.Constrain(geom.Sz(h*1.5, h))
	return b.size
}

// Paint implements [gunim.Node].
func (b *ColorButton) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	defer b.faint(pt, box)()
	th := f.Theme
	r := geom.Rect{Max: box.Point()}
	if b.Hex {
		b.paintHex(pt, th, box)
		return
	}
	radius := FieldRadius.Get(th)
	b.paintRing(pt, r, radius, th)
	fill := anim.Mix(anim.ColorCodec, ButtonFill.Get(th), ButtonHover.Get(th), b.hover.Value())
	pt.RRectStroke(r, radius, paint.Solid(fill), paint.Stroke{Width: 1, Color: FieldBorder.Get(th)})
	in := r.Inset(geom.Uniform(5))
	sr := max(0, radius-3)
	paintChecks(pt, th, in, sr)
	pt.RRect(in, sr, paint.Solid(b.value))
}

// paintHex draws the button with Hex: clear but for a fill under the
// pointer, the hex at the right of its room, and the chip after it, its
// edge drawn so a colour like the ground still shows.
func (b *ColorButton) paintHex(pt *paint.Painter, th *theme.Live, box geom.Size) {
	r := geom.Rect{Max: box.Point()}
	radius := FieldRadius.Get(th)
	b.paintRing(pt, r, radius, th)
	if hv := b.hover.Value(); hv > 0.001 {
		fill := ButtonHover.Get(th)
		fill.A = uint8(float32(fill.A) * min(hv, 1))
		pt.RRect(r, radius, paint.Solid(fill))
	}
	pad := ControlGap.Get(th)
	cw, ch := ColorChipWidth.Get(th), ColorChipHeight.Get(th)
	chip := geom.Rc(box.W-pad/2-cw, (box.H-ch)/2, cw, ch)
	run := b.hexText.run
	run.Paint(pt, geom.Pt(chip.Min.X-pad/2-run.Advance, (box.H-run.Height())/2), Placeholder.Get(th))
	cr := ColorChipRadius.Get(th)
	paintChecks(pt, th, chip, cr)
	pt.RRectStroke(chip, cr, paint.Solid(b.value), paint.Stroke{Width: 1, Color: FieldBorder.Get(th)})
}

// Access implements [gunim.Accessible]: a button with a popup, the
// colour in hex as its value.
func (b *ColorButton) Access() access.Info {
	info := access.Info{Role: access.RoleButton, Name: b.accessName(b.Label), Value: theme.Hex(b.value),
		State: access.StateExpandable | access.StateHasPopup | b.accessState(), Actions: []string{access.ActionPress}}
	if b.IsOpen() {
		info.State |= access.StateExpanded
	}
	return info
}

// AccessAct implements [gunim.AccessActor]: press opens the picker, or
// closes it.
func (b *ColorButton) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || b.Disabled {
		return false
	}
	if b.IsOpen() {
		b.close(u)
	} else {
		b.Open(u)
	}
	return true
}

// pickPopup is the panel a [ColorButton]'s picker shows in, as a menu
// is drawn: a card with the menu's fill, border and shadow, and the
// button's label at its top, naming what is picked. It holds the
// keyboard while it is open, and fades in and out.
type pickPopup struct {
	anim.Group
	child gunim.Node
	in    *anim.Float
	title string
	run   shapedText
	// margin is the room round the card for its shadow, where the
	// popup's window can show one, and head the room the title takes.
	margin, head float32
	card         geom.Rect
}

func newPickPopup(child gunim.Node, title string) *pickPopup {
	p := &pickPopup{child: child, in: anim.NewFloat(0), title: title}
	p.Add(p.in)
	return p
}

// Modal implements [gunim.Modal]: the keyboard keeps to the picker.
func (p *pickPopup) Modal() bool { return true }

// PopupPadding implements [gunim.PopupPadder]: the room for the shadow.
func (p *pickPopup) PopupPadding() geom.Insets { return geom.Uniform(p.margin) }

// Children implements [gunim.Composite].
func (p *pickPopup) Children() []gunim.Node { return []gunim.Node{p.child} }

// Transition implements [gunim.Transitioner].
func (p *pickPopup) Transition(pr gunim.Presence, f gunim.Frame) bool {
	switch pr {
	case gunim.Entering:
		p.in.Animate(1, Quick.Get(f.Theme))
	case gunim.Exiting:
		p.in.Animate(0, Quick.Get(f.Theme))
	case gunim.Present:
	}
	return !p.in.Active()
}

// Layout implements [gunim.Node].
func (p *pickPopup) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	th := f.Theme
	p.margin = 0
	if f.Transparent {
		p.margin = MenuMargin.Get(th)
	}
	pad := ColorPickerGap.Get(th) + 2
	p.head = 0
	if p.title != "" {
		run := p.run.shape(faceIn(BoldFont, th), p.title, TextSize.Get(th))
		p.head = run.Height() + ColorPickerGap.Get(th)
	}
	in := geom.Insets{Top: p.margin + pad + p.head, Right: p.margin + pad, Bottom: p.margin + pad, Left: p.margin + pad}
	size := inset(c, in, kids)
	p.card = geom.Rect{Min: geom.Pt(p.margin, p.margin), Max: geom.Pt(size.W-p.margin, size.H-p.margin)}
	return size
}

// Paint implements [gunim.Node].
func (p *pickPopup) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	t := clamp01(p.in.Value())
	defer pt.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: t})()
	radius := MenuRadius.Get(th)
	if p.margin > 0 {
		pt.ShadowRRect(p.card, radius, paint.Solid(MenuFill.Get(th)), paint.Shadow{
			Offset: geom.Pt(0, 3), Blur: p.margin * 0.6, Color: MenuShadow.Get(th)})
	}
	pt.RRectStroke(p.card, radius, paint.Solid(MenuFill.Get(th)), paint.Stroke{Width: 1, Color: MenuBorder.Get(th)})
	if p.title != "" {
		pad := ColorPickerGap.Get(th) + 2
		p.run.run.Paint(pt, geom.Pt(p.card.Min.X+pad, p.card.Min.Y+pad), Ink.Get(th))
	}
	kids.At(0).Paint(pt)
}

// hsvColor returns the colour of hue h, saturation s, brightness v and
// opacity a, each from 0 to 1.
func hsvColor(h, s, v, a float32) color.NRGBA {
	h = (h - float32(math.Floor(float64(h)))) * 6
	i := int(h) % 6
	f := h - float32(int(h))
	p, q, t := v*(1-s), v*(1-s*f), v*(1-s*(1-f))
	var r, g, b float32
	switch i {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	default:
		r, g, b = v, p, q
	}
	byteOf := func(x float32) uint8 { return uint8(clamp01(x)*255 + 0.5) }
	return color.NRGBA{R: byteOf(r), G: byteOf(g), B: byteOf(b), A: byteOf(a)}
}

// colorHSV returns c's hue, saturation and brightness, each from 0 to 1.
func colorHSV(c color.NRGBA) (h, s, v float32) {
	r, g, b := float32(c.R)/255, float32(c.G)/255, float32(c.B)/255
	hi, lo := max(r, g, b), min(r, g, b)
	v = hi
	d := hi - lo
	if hi > 0 {
		s = d / hi
	}
	if d == 0 {
		return 0, s, v
	}
	switch hi {
	case r:
		h = (g - b) / d
		if h < 0 {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h / 6, s, v
}

// percent returns v, from 0 to 1, in whole percent.
func percent(v float32) int { return int(math.Round(float64(v) * 100)) }
