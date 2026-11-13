package widget

import (
	"image/color"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// HoldGlow is the glow a finger spreads on a [HoldButton]: no colour, so it suits both the plain fill and the kind's
// that fills over it.
var HoldGlow = theme.Color("hold.glow", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x2c})

// holdKeep is how long a finger holds a [HoldButton] still before the button keeps it: a finger that moves sooner
// scrolls what the button sits in, and lets the hold go. It comes before the engine's long press, at 400 ms.
const holdKeep = 250 * time.Millisecond

// HoldButton is a button for the gravest changes, such as revoking a device: it acts only once it has been held
// down for Hold, one second unless set.
//
// It starts quiet, in a plain button's colours. While it is held, its kind's colour fills it from its start to its
// end, at an even pace. Let go early, and the fill drains back on a spring, and nothing happens; press again before
// it has drained, and it fills on from where it is. Once it is full the button acts, once, springs back from its
// press, and shows a tick in place of its icon until it is let go. A press after it has acted starts from empty. It
// lights and squashes under the finger, as [Button] does. Inside something that scrolls, a finger that moves at once
// scrolls it, as on any other button, and one held still for a moment keeps the hold; where nothing scrolls, the
// button keeps the finger from the start.
//
// Space or Enter held down holds it from the keyboard. A screen reader's press acts at once, and no hold is asked
// of it there: the hold guards against a slip of the finger, and the PIN asked before the change is the real guard.
type HoldButton struct {
	Control

	Label string
	// Icon shows before the label.
	Icon *icon.Icon
	// Kind is the colour that fills the button as it is held. NewHoldButton makes it [ButtonDanger].
	Kind ButtonKind
	// Hold is how long the button must be held to act.
	Hold time.Duration
	// OnClick runs once the button has been held for Hold. A non-nil result is sent as the button's intent.
	OnClick func(u *gunim.UI) gunim.Intent

	// fill runs from 0 to 1 as the button fills, on the clock, and back as it drains; done runs to 1 as it acts.
	fill, done *anim.Float
	// holding says a press or a key holds the button now, key which key, fired that the last hold acted, and kept
	// that a finger has held still long enough for the button to keep it.
	holding, fired, kept bool
	key                  input.Key
	// stop ends the wait for the hold to be long enough, and start the wait of a finger's press inside something
	// that scrolls, or for the button to keep it.
	stop, start, keep func()
	size              geom.Size
	text              shapedText
	ell               shapedText
}

// NewHoldButton returns a button showing label that acts once held for a second.
func NewHoldButton(label string) *HoldButton {
	b := &HoldButton{Control: newControl(), Label: label, Kind: ButtonDanger, Hold: time.Second,
		fill: anim.NewFloat(0), done: anim.NewFloat(0)}
	// The fill keeps time: it fills over Hold however the theme speeds up or slows down motion.
	b.fill.RealTime()
	b.Add(b.fill, b.done)
	return b
}

// DragsTouch implements [gunim.TouchDragger]: once a finger has held the button still for a moment, it keeps
// holding it, and never turns into a long press or a scroll.
func (b *HoldButton) DragsTouch() bool { return b.held && b.kept }

// Progress returns how full the button is, from 0 to 1.
func (b *HoldButton) Progress() float32 { return min(max(b.fill.Value(), 0), 1) }

// Holding reports whether the button is held down now.
func (b *HoldButton) Holding() bool { return b.holding }

// begin starts a hold: the button fills from where it is, or from empty after a hold that acted, and acts once it
// is full.
func (b *HoldButton) begin(u *gunim.UI) {
	if b.holding {
		return
	}
	if b.fired {
		b.fill.Jump(0)
		b.done.Animate(0, Quick.Get(u.Theme()))
	}
	b.holding, b.fired = true, false
	hold := max(b.Hold, time.Millisecond)
	rest := time.Duration(float64(hold) * float64(1-b.Progress()))
	b.fill.Retarget(1, anim.Tween{Duration: rest, Ease: anim.Linear})
	b.stop = u.After(rest, b.complete)
	u.Invalidate()
}

// complete acts, once the button has been held long enough.
func (b *HoldButton) complete(u *gunim.UI) {
	b.stop = nil
	if !b.holding || b.fired {
		return
	}
	b.fired = true
	th := u.Theme()
	b.fill.Animate(1, Quick.Get(th))
	b.done.Animate(1, Bounce.Get(th))
	// The press springs back, so the button shows it has acted while the finger is still on it.
	b.squash.Animate(0, Bounce.Get(th))
	act0(u, b, gunim.CuePress, b.OnClick)
}

// end ends a hold: a fill short of full drains back, and a full one drains once let go, its tick going with it.
func (b *HoldButton) end(th *theme.Live) {
	for _, stop := range []*func(){&b.start, &b.keep} {
		if *stop != nil {
			(*stop)()
			*stop = nil
		}
	}
	b.kept = false
	if !b.holding {
		return
	}
	b.holding = false
	if b.stop != nil {
		b.stop()
		b.stop = nil
	}
	b.squash.Animate(0, Bounce.Get(th))
	b.fill.Retarget(0, Settle.Get(th))
	b.done.Animate(0, Settle.Get(th))
}

// Handle implements [gunim.Handler].
func (b *HoldButton) Handle(e input.Event, u *gunim.UI) bool {
	b.showTip(e, u, b)
	th := u.Theme()
	if b.Disabled {
		if _, lost := e.(input.FocusLost); lost {
			b.end(th)
		}
		return b.handleDisabled(e, u, nil)
	}
	switch e := e.(type) {
	case input.PointerEnter:
		b.hover.Animate(1, Quick.Get(th))
	case input.PointerLeave:
		b.hover.Animate(0, Settle.Get(th))
		// Dragged off, the hold lets go, though the button keeps the pointer until it is released.
		if b.held {
			b.end(th)
		}
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		b.held = true
		b.squashIn(th)
		b.press(e, u)
	case input.PointerMove:
		// A finger that slides off lets go of the hold; the button keeps it until it lifts.
		if b.held && b.holding && over(e.Pos, b.size) < 0 {
			b.end(th)
		}
		return b.held
	case input.PointerUp:
		if !b.held {
			return false
		}
		b.held = false
		b.end(th)
	case input.KeyPress:
		if e.Key != input.KeySpace && e.Key != input.KeyEnter && e.Key != input.KeyKPEnter {
			return false
		}
		if !e.Repeat && !b.holding {
			b.key = e.Key
			b.squash.Animate(1, Quick.Get(th))
			b.begin(u)
		}
	case input.KeyRelease:
		if !b.holding || b.held || e.Key != b.key {
			return false
		}
		b.end(th)
	case input.FocusLost:
		b.ringFollows(e, th)
		if !b.held {
			b.end(th)
		}
	default:
		return b.ringFollows(e, th)
	}
	return true
}

// press starts the hold of a press: at once, or for a finger inside something that scrolls, once the press has
// waited to show, as the light does. A finger held still a moment is then kept.
func (b *HoldButton) press(e input.PointerDown, u *gunim.UI) {
	if !e.Touch {
		b.begin(u)
		return
	}
	if !scrollsAround(u, b) {
		// Nothing round it scrolls: the button keeps the finger at once, and a finger that rolls a little holds on.
		b.kept = true
	} else {
		b.keep = u.After(holdKeep, func(*gunim.UI) {
			b.keep = nil
			b.kept = b.held
		})
	}
	if !b.touch.waiting {
		b.begin(u)
		return
	}
	b.start = u.After(pressWait, func(u *gunim.UI) {
		b.start = nil
		if b.held {
			b.begin(u)
		}
	})
}

// Layout implements [gunim.Node]: the icon, a gap and the label, inside the button's padding.
func (b *HoldButton) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	b.follow(th)
	if b.Disabled && b.holding {
		b.end(th)
	}
	h := ButtonHeight.Get(th)
	w := 2*ButtonPadding.Get(th) + IconSize.Get(th)
	if b.Label != "" {
		w += IconGap.Get(th) + b.text.shape(faceIn(Font, th), b.Label, TextSize.Get(th)).Advance
	}
	b.size = c.Constrain(geom.Sz(w, h))
	return b.size
}

// Paint implements [gunim.Node]: a plain button's fill, the kind's colour over it as far as the button is full,
// and the icon and label, in the plain ink over the plain fill and the kind's ink over the kind's.
func (b *HoldButton) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	defer b.faint(p, box)()
	th := f.Theme
	r := geom.Rect{Max: box.Point()}
	radius := min(ButtonRadius.Get(th), box.H/2)
	if s := 1 - ButtonSquash.Get(th)*b.squash.Value(); s != 1 {
		defer p.Push(paint.Scale(s, r.Center()))()
	}
	b.paintRing(p, r, radius, th)
	plainRest, plainHover, plainInk := kindColours(ButtonPlain, th)
	rest, hover, ink := kindColours(b.Kind, th)
	lit := min(max(b.hover.Value(), 0), 1)
	p.RRect(r, radius, paint.Solid(anim.Mix(anim.ColorCodec, plainRest.Get(th), plainHover.Get(th), lit)))
	split := box.W * b.Progress()
	if split > 0.5 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, 0, split, box.H), Clip: true, Opacity: 1})()
			p.RRect(r, radius, paint.Solid(anim.Mix(anim.ColorCodec, rest.Get(th), hover.Get(th), lit)))
		}()
	}
	// A glow of no colour, which suits the plain fill and the kind's alike.
	b.paintGlowIn(p, r, radius, HoldGlow.Get(th))
	// The ink changes where the fill does, so the words read over both.
	if split < box.W-0.5 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(split, 0, box.W-split, box.H), Clip: true, Opacity: 1})()
			b.paintContent(p, th, box, plainInk.Get(th))
		}()
	}
	if split > 0.5 {
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, 0, split, box.H), Clip: true, Opacity: 1})()
		b.paintContent(p, th, box, ink.Get(th))
	}
}

// paintContent draws the icon, or the tick once the button has acted, and the label, in ink.
func (b *HoldButton) paintContent(p *paint.Painter, th *theme.Live, box geom.Size, ink color.NRGBA) {
	pad := min(ButtonPadding.Get(th), box.W/4)
	s := IconSize.Get(th)
	x := pad
	if b.Label == "" {
		x = (box.W - s) / 2
	}
	at := geom.Rc(x, (box.H-s)/2, s, s)
	done := min(max(b.done.Value(), 0), 1)
	if b.Icon != nil && done < 1 {
		drawIcon(p, th, b.Icon, at, ink, 1-done)
	}
	if done > 0 {
		drawIcon(p, th, icon.Check, at, ink, done)
	}
	if b.Label == "" {
		return
	}
	x += s + IconGap.Get(th)
	if end := box.W - pad; x < end {
		run := fitRun(b.text.shape(faceIn(Font, th), b.Label, TextSize.Get(th)), &b.ell, end-x)
		run.Paint(p, geom.Pt(x, (box.H-run.Height())/2), ink)
	}
}

// Access implements [gunim.Accessible]: a button, which a screen reader presses as any other.
func (b *HoldButton) Access() access.Info {
	name := b.accessName(b.Label)
	if name == "" {
		name = iconName(b.Icon)
	}
	return access.Info{Role: access.RoleButton, Name: name, State: b.accessState(), Actions: []string{access.ActionPress}}
}

// AccessAct implements [gunim.AccessActor]: a press acts at once.
func (b *HoldButton) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || b.Disabled {
		return false
	}
	act0(u, b, gunim.CuePress, b.OnClick)
	return true
}
