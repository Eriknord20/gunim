package widget

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// Control is the part every interactive widget shares: the fields Disabled, Tooltip and KeepFocus, the springs for
// hover, press, the focus ring and fading faint, and the rules that go with them. A program sets its fields through
// the widget, as button.Disabled.
//
// A widget of this package embeds it, registers its own springs with the group it holds, calls follow from Layout, showTip and
// handleDisabled from Handle, and draws inside faint, with its ring through paintRing.
type Control struct {
	// Group steps the control's springs and the widget's own.
	anim.Group
	// Disabled fades the control faint, and it then takes no clicks, keys or focus, for a choice that does not apply
	// now. A right click still passes to a context menu round it.
	Disabled bool
	// Tooltip says what the control does, or why it cannot: a popup shows it once the pointer has rested on the
	// control, disabled too, and a screen reader reads it as the name where the control shows none.
	Tooltip string
	// KeepFocus leaves the keyboard where it is when the control is clicked, as a toolbar's buttons do; Tab still
	// reaches it.
	KeepFocus bool

	// dim runs from 0 to 1 as Disabled turns on, ring as the focus ring grows, hover as the pointer comes over the
	// control and squash as it is pressed.
	dim, ring, hover, squash *anim.Float
	tip                      tipper
	// over says the pointer is over the control, held that a press on it is still to be let go, and laid that the
	// control has been laid out once.
	over, held, laid bool
}

// newControl returns a control with its springs at rest, registered with its group.
func newControl() Control {
	c := Control{dim: anim.NewFloat(0), ring: anim.NewFloat(0), hover: anim.NewFloat(0), squash: anim.NewFloat(0)}
	c.Add(c.dim, c.ring, c.hover, c.squash)
	return c
}

// Focusable implements [gunim.Focusable]: a control takes the keyboard while it is enabled.
func (c *Control) Focusable() bool { return !c.Disabled }

// FocusOnPress implements [gunim.PressFocuser].
func (c *Control) FocusOnPress() bool { return !c.KeepFocus }

// follow keeps the faint on Disabled, from Layout. The first layout puts it there at once; after that it fades. As the
// control turns disabled it lets go of a press, and its hover fades; enabled again under the pointer, it lights.
func (c *Control) follow(th *theme.Live) {
	off := anim.OnOff(c.Disabled)
	if !c.laid {
		c.laid = true
		c.dim.Jump(off)
		return
	}
	if c.dim.Target() == off {
		return
	}
	c.dim.Animate(off, Quick.Get(th))
	c.held = false
	c.squash.Animate(0, Settle.Get(th))
	c.hover.Animate(anim.OnOff(c.over && !c.Disabled), Quick.Get(th))
}

// track follows the pointer coming over the control and leaving it, whether or not the control is enabled.
func (c *Control) track(e input.Event) {
	switch e.(type) {
	case input.PointerEnter:
		c.over = true
	case input.PointerLeave:
		c.over = false
	}
}

// showTip shows the tooltip for n, the control as the tree knows it, once the pointer rests on it. It shows on a
// disabled control too, since saying why it cannot act is what a tooltip is for.
func (c *Control) showTip(e input.Event, u *gunim.UI, n gunim.Node) {
	c.track(e)
	if c.Tooltip == "" && c.tip.popup == nil && c.tip.stop == nil {
		return
	}
	c.tip.handle(e, u, n, c.Tooltip, tipDelay)
}

// handleDisabled is what a disabled control does with e, and reports whether it took it. It takes the primary
// button's presses, so a click on it reaches nothing round it, and passes the other buttons by, so a right click
// reaches a context menu round it. Keys pass by. The keyboard leaving lets the ring go and runs shut, which closes
// anything the control has open; shut may be nil.
func (c *Control) handleDisabled(e input.Event, u *gunim.UI, shut func(*gunim.UI)) bool {
	switch e := e.(type) {
	case input.PointerEnter, input.PointerLeave:
	case input.PointerDown:
		return e.Button == input.ButtonPrimary
	case input.PointerUp:
		return e.Button == input.ButtonPrimary
	case input.FocusLost:
		c.ring.Animate(0, RingFade.Get(u.Theme()))
		c.held = false
		if shut != nil {
			shut(u)
		}
	default:
		return false
	}
	return true
}

// faint draws what follows as faint as the control is disabled, and returns what ends it.
func (c *Control) faint(p *paint.Painter, box geom.Size) func() { return Faint(p, box, c.dim.Value()) }

// paintRing draws the focus ring round r, its corners radius round. It fades with the control as it turns disabled,
// and comes back as it is enabled with the keyboard still on it.
func (c *Control) paintRing(p *paint.Painter, r geom.Rect, radius float32, th *theme.Live) {
	FocusRing(p, r, radius, c.ring.Value()*(1-min(max(c.dim.Value(), 0), 1)), th)
}

// ringFollows moves the ring for a focus event e, and reports whether e was one.
func (c *Control) ringFollows(e input.Event, th *theme.Live) bool {
	switch e := e.(type) {
	case input.FocusRing:
		c.ring.Animate(ringTo(e), RingFade.Get(th))
	case input.FocusLost:
		c.ring.Animate(0, RingFade.Get(th))
	default:
		return false
	}
	return true
}

// accessState is the state a screen reader hears for the control: disabled, or nothing.
func (c *Control) accessState() access.State {
	if c.Disabled {
		return access.StateDisabled
	}
	return 0
}

// accessName is the control's name for a screen reader: own, what it shows, or its tooltip where it shows nothing.
func (c *Control) accessName(own string) string {
	if own != "" {
		return own
	}
	return c.Tooltip
}

// faintOpacity is how much of a disabled control shows.
const faintOpacity = 0.4

// Faint draws what follows faint, as a control that does not apply now is drawn, and returns what ends it. t runs from
// 0, drawn as it is, to 1, all the way faint: animate it with a spring of the node's own, so a node fades as the
// widgets do as it is disabled and enabled again. box is the node's size; what is drawn up to 8 pixels round it, such
// as a focus ring, fades with it.
func Faint(p *paint.Painter, box geom.Size, t float32) func() {
	t = min(max(t, 0), 1)
	if t <= 0 {
		return func() {}
	}
	return p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}.Inset(geom.Uniform(-8)), Opacity: 1 - (1-faintOpacity)*t})
}
