package widget

import (
	"image/color"
	"math"
	"time"

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
// A finger's press shows on the frame it lands: the control lights as under the pointer, squashes, and a soft glow
// spreads out from the finger. Inside something that scrolls, the press waits up to 80 ms for the finger to show
// whether it taps or scrolls, so a scroll lights none of the controls it starts on. A tap quicker than that still
// glows on the control, briefly.
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
	// touch is a finger's press on the control.
	touch touchPress
}

// touchPress is what a control keeps of a finger's press: spread runs from 0 to 1 as the glow grows out from at,
// and shine from 1 to 0 as it fades once the finger lifts. waiting says the press waits to show, stop ends that
// wait, and down says a finger is on the control.
type touchPress struct {
	spread, shine *anim.Float
	at            geom.Point
	waiting, down bool
	stop          func()
}

// pressWait is how long a finger's press inside something that scrolls waits to show, for the finger to show
// whether it scrolls, and flash how long a tap quicker than that shows.
const (
	pressWait = 80 * time.Millisecond
	flash     = 120 * time.Millisecond
)

// newControl returns a control with its springs at rest, registered with its group.
func newControl() Control {
	c := Control{dim: anim.NewFloat(0), ring: anim.NewFloat(0), hover: anim.NewFloat(0), squash: anim.NewFloat(0)}
	c.touch.spread, c.touch.shine = anim.NewFloat(0), anim.NewFloat(0)
	c.Add(c.dim, c.ring, c.hover, c.squash, c.touch.spread, c.touch.shine)
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
	c.endWait()
	c.touch.down = false
	c.touch.shine.Animate(0, Settle.Get(th))
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
// disabled control too, since saying why it cannot act is what a tooltip is for. A theme with [Tooltips] off shows
// none. It also follows a finger's press, for the light and glow that show it.
func (c *Control) showTip(e input.Event, u *gunim.UI, n gunim.Node) {
	c.track(e)
	c.feel(e, u, n)
	if c.Tooltip == "" && c.tip.popup == nil && c.tip.stop == nil {
		return
	}
	c.tip.handle(e, u, n, c.Tooltip, tipDelay)
}

// feel follows a finger pressing the control n and lifting, to light it and spread a glow from the finger. A press
// inside something that scrolls waits pressWait to show.
func (c *Control) feel(e input.Event, u *gunim.UI, n gunim.Node) {
	t := &c.touch
	switch e := e.(type) {
	case input.PointerDown:
		if !e.Touch || e.Button != input.ButtonPrimary || c.Disabled {
			return
		}
		c.endWait()
		t.at, t.down = e.Pos, true
		if !scrollsAround(u, n) {
			c.lightUp(u.Theme())
			return
		}
		t.waiting = true
		t.stop = u.After(pressWait, func(u *gunim.UI) {
			t.waiting, t.stop = false, nil
			c.lightUp(u.Theme())
		})
	case input.PointerUp:
		if !e.Touch || e.Button != input.ButtonPrimary || !t.down {
			return
		}
		t.down = false
		if t.waiting {
			c.endWait()
			if away(e.Pos) {
				return
			}
			// A tap quicker than the wait glows on the control for a moment, so it shows it was hit.
			c.lightUp(u.Theme())
			t.stop = u.After(flash, func(u *gunim.UI) {
				t.stop = nil
				c.letGo(u.Theme())
			})
			return
		}
		c.letGo(u.Theme())
	case input.FocusLost:
		if t.down || t.stop != nil {
			c.endWait()
			t.down = false
			c.letGo(u.Theme())
		}
	}
}

// endWait stops a press waiting to show, or a tap's light waiting to go.
func (c *Control) endWait() {
	if c.touch.stop != nil {
		c.touch.stop()
		c.touch.stop = nil
	}
	c.touch.waiting = false
}

// lightUp shows a finger's press: the control lights, squashes while it is still held, and the glow starts to
// spread from the finger.
func (c *Control) lightUp(th *theme.Live) {
	t := &c.touch
	c.hover.Animate(1, Quick.Get(th))
	if c.held {
		c.squash.Animate(1, Quick.Get(th))
	}
	t.spread.Jump(0)
	t.spread.Animate(1, Settle.Get(th))
	t.shine.Animate(1, Quick.Get(th))
}

// letGo eases a finger's press off: the light and the glow fade, and the glow goes on spreading as it fades.
func (c *Control) letGo(th *theme.Live) {
	if !c.over {
		c.hover.Animate(0, Settle.Get(th))
	}
	c.touch.shine.Animate(0, Settle.Get(th))
}

// squashIn squashes the control for a press, at once, or once a finger's press inside something that scrolls has
// waited to show.
func (c *Control) squashIn(th *theme.Live) {
	if !c.touch.waiting {
		c.squash.Animate(1, Quick.Get(th))
	}
}

// paintGlow draws the glow of a finger's press over r, a shape with corners radius round: brightest where the
// finger landed, and fading out as it spreads.
func (c *Control) paintGlow(p *paint.Painter, r geom.Rect, radius float32, th *theme.Live) {
	c.paintGlowIn(p, r, radius, PressGlow.Get(th))
}

// paintGlowIn is paintGlow in glow, for a control whose fill the press glow does not suit.
func (c *Control) paintGlowIn(p *paint.Painter, r geom.Rect, radius float32, glow color.NRGBA) {
	t := &c.touch
	shine, spread := min(max(t.shine.Value(), 0), 1), max(t.spread.Value(), 0)
	if shine < 0.01 || spread < 0.01 || r.Empty() {
		return
	}
	far := float32(0)
	for _, corner := range []geom.Point{r.Min, r.Max, geom.Pt(r.Min.X, r.Max.Y), geom.Pt(r.Max.X, r.Min.Y)} {
		d := corner.Sub(t.at)
		far = max(far, float32(math.Hypot(float64(d.X), float64(d.Y))))
	}
	glow.A = uint8(float32(glow.A)*shine + 0.5)
	gone := glow
	gone.A = 0
	reach := max(far*spread, 1)
	p.RRect(r, radius, paint.Fill{Gradient: &paint.Gradient{
		From: t.at, To: t.at.Add(geom.Pt(reach, 0)), Start: glow, End: gone, Radial: true,
	}})
}

// scrollsAround reports whether something round n scrolls, either way along either axis.
func scrollsAround(u *gunim.UI, n gunim.Node) bool {
	for _, d := range []geom.Point{{X: 0, Y: 1}, {X: 0, Y: -1}, {X: 1, Y: 0}, {X: -1, Y: 0}} {
		if u.ScrollsAround(n, d) {
			return true
		}
	}
	return false
}

// away reports whether p, in a node's space, is [input.Away] moved there: a press let go as its finger scrolled.
func away(p geom.Point) bool { return p.X < input.Away.X/2 || p.Y < input.Away.Y/2 }

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
