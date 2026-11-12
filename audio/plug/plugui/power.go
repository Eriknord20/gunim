package plugui

import (
	"image/color"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// PowerSize is how big a power lamp is, across.
const PowerSize = 18

// PaintPower draws a power lamp about c: on as far as on, from 0 to 1,
// a lit lamp in accent with a glow about it; off, a dim ring; hover
// lights its ring a little.
func PaintPower(p *paint.Painter, c geom.Point, on, hover float32, accent, ink color.NRGBA) {
	if on > 0.01 {
		p.ShadowRRect(geom.Rc(c.X-5, c.Y-5, 10, 10), 5, paint.Solid(paint.Faded(accent, 0)),
			paint.Shadow{Blur: 9 * on, Color: paint.Faded(accent, 0.55*on)})
	}
	ring := anim.MixColor(paint.Faded(ink, 0.28+0.2*hover), paint.Faded(accent, 0.6+0.2*hover), on)
	p.RRect(geom.Rc(c.X-7, c.Y-7, 14, 14), 7, paint.Solid(ring))
	p.RRect(geom.Rc(c.X-5.5, c.Y-5.5, 11, 11), 5.5, paint.Solid(anim.MixColor(paint.Faded(ink, 0.08), paint.Faded(accent, 0.25), on)))
	core := anim.MixColor(paint.Faded(ink, 0.3), anim.MixColor(accent, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, 0.3), on)
	r := 2.5 + 1.5*on
	p.RRect(geom.Rc(c.X-r, c.Y-r, 2*r, 2*r), r, paint.Solid(core))
}

// Power switches something on and off, as a power lamp: lit while on,
// gliding as it changes, lighting a little under the pointer, its
// tooltip saying what a click does. Tab brings the keyboard to it, a
// focus ring fades in round it, and Space or Enter switches it.
type Power struct {
	anim.Group
	checked bool
	on      *anim.Float
	hover   *anim.Float
	// ring is how far the focus ring shows.
	ring *anim.Float
	// squash is how far it is pressed in; held says a press is on it, and size is how big it was laid out.
	squash *anim.Float
	held   bool
	click  widget.Clicker
	size   geom.Size
	// OnChange is called as a click changes it, with what it is now.
	OnChange func(on bool, u *gunim.UI) gunim.Intent
	// Name is what it switches, for its tooltip and for access.
	Name string
	// Tooltip, where set, replaces the default tooltip, which says whether Name is on and what a click does.
	Tooltip string
	tip     widget.PartTip
}

// NewPower returns a power lamp, off, switching name.
func NewPower(name string) *Power {
	p := &Power{Name: name, on: anim.NewFloat(0), hover: anim.NewFloat(0), ring: anim.NewFloat(0), squash: anim.NewFloat(0),
		click: widget.Clicker{Repeats: true}}
	p.Add(p.on, p.hover, p.ring, p.squash)
	return p
}

// Checked says it is on.
func (p *Power) Checked() bool { return p.checked }

// SetChecked sets it on or off, as set elsewhere, gliding there.
func (p *Power) SetChecked(on bool, u *gunim.UI) {
	p.checked = on
	if u == nil {
		p.on.Jump(anim.OnOff(on))
		return
	}
	p.on.Animate(anim.OnOff(on), widget.Quick.Get(u.Theme()))
	u.Invalidate()
}

// Step implements [gunim.Animator].
func (p *Power) Step(dt time.Duration) bool { return p.Group.Step(dt) }

// Layout implements [gunim.Node].
func (p *Power) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	p.size = c.Constrain(geom.Sz(PowerSize, PowerSize))
	return p.size
}

// Paint implements [gunim.Node].
func (p *Power) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	mid := geom.Pt(box.W/2, box.H/2)
	widget.FocusRing(pt, geom.Rc(mid.X-7, mid.Y-7, 14, 14), 7, p.ring.Value(), th)
	defer pt.Push(paint.Scale(1-0.12*p.squash.Value(), mid))()
	PaintPower(pt, mid, p.on.Value(), p.hover.Value(), widget.Accent.Get(th), widget.Ink.Get(th))
}

// tooltip says whether it is on and what a click does, as "Follow song is on. Click to turn it off.".
func (p *Power) tooltip() string {
	if p.Tooltip != "" {
		return p.Tooltip
	}
	now, to := "off", "on"
	if p.checked {
		now, to = "on", "off"
	}
	click := "Click to turn it " + to + "."
	if p.Name == "" {
		return click
	}
	r, n := utf8.DecodeRuneInString(p.Name)
	return string(unicode.ToUpper(r)) + p.Name[n:] + " is " + now + ". " + click
}

// Focusable implements [gunim.Focusable].
func (p *Power) Focusable() bool { return true }

// Handle implements [gunim.Handler]: a click, or Space or Enter,
// switches it.
func (p *Power) Handle(e input.Event, u *gunim.UI) bool {
	p.tip.Handle(e, u, p, p.tooltip())
	if followRing(p.ring, e, u) {
		return true
	}
	switch e := e.(type) {
	case input.PointerEnter:
		p.hover.Animate(1, widget.Quick.Get(u.Theme()))
		if p.held {
			p.squash.Animate(1, widget.Quick.Get(u.Theme()))
		}
		return false
	case input.PointerLeave:
		p.hover.Animate(0, widget.Quick.Get(u.Theme()))
		p.squash.Animate(0, widget.Bounce.Get(u.Theme()))
		return false
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		p.tip.Hide(u)
		p.held = true
		p.click.Press(e, 0)
		p.squash.Animate(1, widget.Quick.Get(u.Theme()))
	case input.PointerUp:
		if !p.held {
			return false
		}
		p.held = false
		p.squash.Animate(0, widget.Bounce.Get(u.Theme()))
		// It acts on the release over it, so a press dragged away does nothing.
		if p.click.Release(e, over(e.Pos, p.size)) {
			p.toggle(u)
		}
	case input.KeyPress:
		if e.Key != input.KeySpace && e.Key != input.KeyEnter {
			return false
		}
		p.toggle(u)
	default:
		return false
	}
	return true
}

// toggle switches it, as the user did.
func (p *Power) toggle(u *gunim.UI) {
	p.SetChecked(!p.checked, u)
	u.Cue(gunim.CueTick, p)
	if p.OnChange != nil {
		if in := p.OnChange(p.checked, u); in != nil {
			u.Send(p, in)
		}
	}
}

// Access implements [gunim.Accessible]: a switch, named as what it
// switches.
func (p *Power) Access() access.Info {
	st := access.State(0)
	if p.checked {
		st = access.StateChecked
	}
	return access.Info{Role: access.RoleSwitch, Name: p.Name, State: st, Actions: []string{access.ActionPress}}
}

// AccessAct implements [gunim.AccessActor]: pressing switches it.
func (p *Power) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress {
		return false
	}
	p.toggle(u)
	return true
}

// over is 0 for pos inside a part of size, and -1 elsewhere: the target a [widget.Clicker] takes.
func over(pos geom.Point, size geom.Size) int {
	if (geom.Rect{Max: size.Point()}).Contains(pos) {
		return 0
	}
	return -1
}
