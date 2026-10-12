// Package widget holds the widgets built on gunim's core.
//
// They are as much worked examples as they are a widget set: each one
// shows how the optional interfaces fit together. A widget animates by
// owning [anim] values and embedding [anim.Group]; it takes input by
// implementing [gunim.Handler]; it animates in and out by implementing
// [gunim.Transitioner].
package widget

import (
	"image/color"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// Button is a labelled, clickable rectangle.
//
// Every visible reaction it has is a spring. Hovering warms the fill,
// pressing squashes it slightly, focusing grows a ring around it. All
// three can be part way through at once and stay independent, because
// each is its own value with its own motion. Each animates a state from
// 0 to 1, and the look comes from the theme every frame, so a theme
// switch lands in the middle of a hover without a jolt.
type Button struct {
	// Control gives the button Disabled, Tooltip and KeepFocus, and its
	// group makes it an Animator, so the engine steps its values and
	// knows to keep drawing while any of them is moving.
	Control

	Label string
	// Icon shows before the label, in the label's colour. A button with an icon and no label is square.
	Icon *icon.Icon
	// IconSize, when set, is the icon's size in place of [IconSize]; a button with an icon and no label then fits
	// the icon with [IconPadding] around it.
	IconSize theme.Token[float32]
	// Ink, when set, colours the label and the icon in place of the kind's ink.
	Ink theme.Token[color.NRGBA]
	// Active shows the ink in [Accent], as a toggle that is on does, fading between the two.
	Active bool
	// Ghost leaves the fill clear until the pointer is over the button.
	Ghost bool
	// Kind says how much the button stands out: plain, primary for the
	// action a dialog expects, or danger for one that destroys, such as
	// Delete.
	Kind ButtonKind
	// OnClick runs on the UI goroutine when the button is clicked, or
	// pressed by Space or Enter. It may act in the window through u; a
	// non-nil result is sent to the application as the button's intent.
	// [Sends] makes one that only sends an intent.
	OnClick func(u *gunim.UI) gunim.Intent

	// size is the button's size at its last layout, for telling a
	// release over it from one outside.
	size geom.Size
	// walked runs from 0 to 1 while the keyboard is on the button by a
	// group's arrow keys: it lights as under the pointer, with no ring,
	// which is for Tab.
	walked *anim.Float
	// lit runs from 0 to 1 as Active turns on.
	lit *anim.Float
	// tone fades the colours from kind was to kind is, so a primary or
	// danger button comes up from the plain button's colours.
	tone    *anim.Float
	was, is ButtonKind
	click   Clicker
	text    shapedText
	// self is the node the button sends from, when it is part of a larger one.
	self gunim.Node
	ell  shapedText
}

// NewButton returns a button showing label.
func NewButton(label string) *Button {
	b := &Button{
		Control: newControl(),
		Label:   label,
		walked:  anim.NewFloat(0),
		lit:     anim.NewFloat(0),
		tone:    anim.NewFloat(1),
		// Every click counts, the fast second of a double click too, as for a + pressed again and again.
		click: Clicker{Repeats: true},
	}
	b.Add(b.walked, b.lit, b.tone)
	return b
}

// Sends returns a callback for an OnClick that sends in and does
// nothing else, as most buttons do.
func Sends(in gunim.Intent) func(*gunim.UI) gunim.Intent {
	return func(*gunim.UI) gunim.Intent { return in }
}

// send sends in from n, when there is one.
func send(u *gunim.UI, n gunim.Node, in gunim.Intent) {
	if in != nil {
		u.Send(n, in)
	}
}

// Handle implements [gunim.Handler].
func (b *Button) Handle(e input.Event, u *gunim.UI) bool {
	b.showTip(e, u, b.node())
	th := u.Theme()
	if b.Disabled {
		if _, lost := e.(input.FocusLost); lost {
			b.walked.Animate(0, Settle.Get(th))
		}
		return b.handleDisabled(e, u, nil)
	}
	switch e := e.(type) {
	case input.PointerEnter:
		b.hover.Animate(1, Quick.Get(th))
		// Coming back while still held presses it again.
		if b.held {
			b.squash.Animate(1, Quick.Get(th))
		}
	case input.PointerLeave:
		b.hover.Animate(0, Settle.Get(th))
		// The button keeps the pointer while it is held. Leaving eases
		// the press off, and releasing out here cancels it.
		b.squash.Animate(0, Bounce.Get(th))
	case input.PointerDown:
		// The primary button presses; the others pass by, so a right
		// click reaches a context menu round the button.
		if e.Button != input.ButtonPrimary {
			return false
		}
		b.held = true
		b.click.Press(e, 0)
		b.squash.Animate(1, Quick.Get(th))
	case input.PointerUp:
		if !b.held {
			return false
		}
		b.held = false
		b.squash.Animate(0, Bounce.Get(th))
		if b.click.Release(e, over(e.Pos, b.size)) {
			b.fire(u)
		}
	case input.KeyPress:
		if e.Key != input.KeySpace && e.Key != input.KeyEnter && e.Key != input.KeyKPEnter {
			return false
		}
		// Keyboard activation runs the same squash, so the button
		// looks pressed however it was reached.
		b.squash.Retarget(1, Quick.Get(th))
		b.squash.Animate(0, Bounce.Get(th))
		b.fire(u)
	case input.FocusGained:
		// In a group, the button with the keyboard is the one the group
		// has selected, lit however the keyboard came: the arrows, or
		// the application putting it there. The group's ring, if any,
		// says where the keyboard is.
		if e.Step != 0 || e.Grouped {
			b.walked.Animate(1, Quick.Get(th))
		}
		return false
	case input.FocusRing:
		if e.On && e.Grouped {
			// The group round it rings the whole; the button lights.
			b.ring.Animate(0, RingFade.Get(th))
			b.walked.Animate(1, Quick.Get(th))
			break
		}
		b.ring.Animate(ringTo(e), RingFade.Get(th))
	case input.FocusLost:
		b.ring.Animate(0, RingFade.Get(th))
		b.walked.Animate(0, Settle.Get(th))
	default:
		return false
	}
	return true
}

// fire runs OnClick, which acts in the window first, and sends its
// intent second.
//
// That order is the point of the whole arrangement: a button that
// closes a dialog closes it on this frame, and the application hears
// about it whenever it gets round to reading. The interface stays fluid
// while the work queues up behind it.
func (b *Button) fire(u *gunim.UI) {
	act0(u, b.node(), gunim.CuePress, b.OnClick)
}

// Layout implements [gunim.Node].
func (b *Button) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	h := ButtonHeight.Get(th)
	w := h
	switch {
	case b.Label != "" || b.Icon == nil:
		w = b.content(th) + 2*ButtonPadding.Get(th)
	case b.IconSize.Key() != "":
		w = b.iconSize(th) + 2*IconPadding.Get(th)
		h = w
	}
	if on := float32(0); b.Active != (b.lit.Target() == 1) {
		if b.Active {
			on = 1
		}
		b.lit.Animate(on, Quick.Get(th))
	}
	b.follow(th)
	b.size = c.Constrain(geom.Sz(w, h))
	if b.Kind != b.is {
		b.was, b.is = b.is, b.Kind
		b.tone.Jump(0)
		b.tone.Animate(1, anim.Tween{Duration: toneTime})
	}
	return b.size
}

// node is the button as the tree knows it, which is the whole widget
// when the button is part of a larger one.
func (b *Button) node() gunim.Node {
	if b.self != nil {
		return b.self
	}
	return b
}

// Paint implements [gunim.Node].
func (b *Button) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	defer b.faint(p, box)()
	th := f.Theme
	r := geom.Rect{Max: box.Point()}
	radius := ButtonRadius.Get(th)

	// Squash toward the centre while held. Scaling about the middle is
	// what makes it read as a press.
	if s := 1 - ButtonSquash.Get(th)*b.squash.Value(); s != 1 {
		defer p.Push(paint.Scale(s, r.Center()))()
	}

	// The focus ring grows outward from the button's edge.
	b.paintRing(p, r, radius, th)

	fromRest, fromHover, fromInk := kindColours(b.was, th)
	rest, hover, ink := kindColours(b.is, th)
	t := min(max(b.tone.Value(), 0), 1)
	mix := func(from, to theme.Token[color.NRGBA]) color.NRGBA {
		return anim.Mix(anim.ColorCodec, from.Get(th), to.Get(th), t)
	}
	restFill, hoverFill := mix(fromRest, rest), mix(fromHover, hover)
	if b.Ghost {
		restFill = hoverFill
		restFill.A = 0
	}
	fill := anim.Mix(anim.ColorCodec, restFill, hoverFill, max(b.hover.Value(), min(b.walked.Value(), 1)))
	faint := 1 - 0.6*min(max(b.dim.Value(), 0), 1)
	fill.A = uint8(float32(fill.A) * faint)
	if shadow := ButtonShadow.Get(th); shadow.A > 0 && !b.Ghost {
		// Cast down and to the right, and pressed into it while held.
		off := 4 * (1 - min(max(b.squash.Value(), 0), 1))
		shadow.A = uint8(float32(shadow.A) * faint)
		p.RRect(geom.Rect{Min: geom.Pt(r.Min.X+off, r.Min.Y+off), Max: geom.Pt(r.Max.X+off, r.Max.Y+off)}, min(radius, box.H/2), paint.Solid(shadow))
	}
	p.RRect(r, min(radius, box.H/2), paint.Solid(fill))
	inked := mix(fromInk, ink)
	if b.Ink.Key() != "" {
		inked = b.Ink.Get(th)
	}
	if lit := b.lit.Value(); lit > 0 {
		inked = anim.Mix(anim.ColorCodec, inked, Accent.Get(th), min(lit, 1))
	}
	inked.A = uint8(float32(inked.A) * faint)
	// Squeezed, the icon keeps to the button's left edge, and the label
	// ends in an ellipsis inside the padding.
	pad := min(ButtonPadding.Get(th), box.W/4)
	if b.Label == "" {
		pad = 0
	}
	x := max((box.W-b.content(th))/2, pad)
	end := box.W - pad
	if b.Icon != nil {
		s := b.iconSize(th)
		if b.Label == "" {
			x = max((box.W-s)/2, 0)
		}
		drawIcon(p, th, b.Icon, geom.Rc(x, (box.H-s)/2, s, s), inked, 1)
		x += s + IconGap.Get(th)
	}
	if b.Label != "" && x < end {
		run := fitRun(b.text.shape(faceIn(Font, th), b.Label, TextSize.Get(th)), &b.ell, end-x)
		run.Paint(p, geom.Pt(x, (box.H-run.Height())/2), inked)
	}
}

// iconSize is the size the button draws its icon at.
func (b *Button) iconSize(th *theme.Live) float32 {
	if b.IconSize.Key() != "" {
		return b.IconSize.Get(th)
	}
	return IconSize.Get(th)
}

// content is the width of the button's icon and label, side by side.
func (b *Button) content(th *theme.Live) float32 {
	w := float32(0)
	if b.Label != "" || b.Icon == nil {
		w = b.text.shape(faceIn(Font, th), b.Label, TextSize.Get(th)).Advance
	}
	if b.Icon != nil {
		w += b.iconSize(th)
		if b.Label != "" {
			w += IconGap.Get(th)
		}
	}
	return w
}

// toneTime is how long a button takes to fade into a new kind's colours.
const toneTime = 350 * time.Millisecond

// kindColours returns the rest fill, hover fill and ink of a kind.
func kindColours(k ButtonKind, th *theme.Live) (rest, hover, ink theme.Token[color.NRGBA]) {
	switch k {
	case ButtonPrimary:
		// Its own ink where the theme sets one, and the strong ink a
		// theme may have set for both before there was one.
		if th.Sets(ButtonPrimaryInk.Key()) {
			return ButtonPrimaryFill, ButtonPrimaryHover, ButtonPrimaryInk
		}
		return ButtonPrimaryFill, ButtonPrimaryHover, ButtonStrongInk
	case ButtonDanger:
		return ButtonDangerFill, ButtonDangerHover, ButtonStrongInk
	case ButtonPlain:
	}
	return ButtonFill, ButtonHover, Ink
}

// ButtonKind says how much a [Button] stands out.
type ButtonKind uint8

// The kinds of button.
const (
	// ButtonPlain is the usual button.
	ButtonPlain ButtonKind = iota
	// ButtonPrimary is the action a dialog expects, in the accent colour.
	ButtonPrimary
	// ButtonDanger is an action that destroys, such as Delete, in red.
	ButtonDanger
)
