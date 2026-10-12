package plugui

import (
	"image/color"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audioui"
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
		p.ShadowRRect(geom.Rc(c.X-5, c.Y-5, 10, 10), 5, paint.Solid(audioui.Faded(accent, 0)),
			paint.Shadow{Blur: 9 * on, Color: audioui.Faded(accent, 0.55*on)})
	}
	ring := audioui.Mix(audioui.Faded(ink, 0.28+0.2*hover), audioui.Faded(accent, 0.6+0.2*hover), on)
	p.RRect(geom.Rc(c.X-7, c.Y-7, 14, 14), 7, paint.Solid(ring))
	p.RRect(geom.Rc(c.X-5.5, c.Y-5.5, 11, 11), 5.5, paint.Solid(audioui.Mix(audioui.Faded(ink, 0.08), audioui.Faded(accent, 0.25), on)))
	core := audioui.Mix(audioui.Faded(ink, 0.3), audioui.Mix(accent, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, 0.3), on)
	r := 2.5 + 1.5*on
	p.RRect(geom.Rc(c.X-r, c.Y-r, 2*r, 2*r), r, paint.Solid(core))
}

// Power switches something on and off, as a power lamp: lit while on,
// gliding as it changes, lighting a little under the pointer, its
// tooltip saying what a click does.
type Power struct {
	anim.Group
	checked bool
	on      *anim.Float
	hover   *anim.Float
	// OnChange is called as a click changes it, with what it is now.
	OnChange func(on bool, u *gunim.UI) gunim.Intent
	// Name is what it switches, for its tooltip and for access.
	Name string
	tip  widget.PartTip
}

// NewPower returns a power lamp, off, switching name.
func NewPower(name string) *Power {
	p := &Power{Name: name, on: anim.NewFloat(0), hover: anim.NewFloat(0)}
	p.Add(p.on, p.hover)
	return p
}

// Checked says it is on.
func (p *Power) Checked() bool { return p.checked }

// SetChecked sets it on or off, as set elsewhere, gliding there.
func (p *Power) SetChecked(on bool, u *gunim.UI) {
	p.checked = on
	if u == nil {
		p.on.Jump(onOff(on))
		return
	}
	p.on.Animate(onOff(on), widget.Quick.Get(u.Theme()))
	u.Invalidate()
}

// Step implements [gunim.Animator].
func (p *Power) Step(dt time.Duration) bool { return p.Group.Step(dt) }

// Layout implements [gunim.Node].
func (p *Power) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Constrain(geom.Sz(PowerSize, PowerSize))
}

// Paint implements [gunim.Node].
func (p *Power) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	PaintPower(pt, geom.Pt(box.W/2, box.H/2), p.on.Value(), p.hover.Value(), widget.Accent.Get(th), widget.Ink.Get(th))
}

// tooltip says what a click does.
func (p *Power) tooltip() string {
	if p.checked {
		return "Switch the " + p.Name + " off"
	}
	return "Switch the " + p.Name + " on"
}

// Focusable implements [gunim.Focusable].
func (p *Power) Focusable() bool { return true }

// Handle implements [gunim.Handler]: a click, or Space or Enter,
// switches it.
func (p *Power) Handle(e input.Event, u *gunim.UI) bool {
	p.tip.Handle(e, u, p, p.tooltip())
	switch e := e.(type) {
	case input.PointerEnter:
		p.hover.Animate(1, widget.Quick.Get(u.Theme()))
		return false
	case input.PointerLeave:
		p.hover.Animate(0, widget.Quick.Get(u.Theme()))
		return false
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		p.tip.Hide(u)
		p.toggle(u)
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
