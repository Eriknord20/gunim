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

// StatusSwitch tokens.
var (
	// StatusSwitchHeight is its height, StatusSwitchPadding the room at
	// its ends, and StatusSwitchGap the room between its words, its
	// Extra and its switch.
	StatusSwitchHeight  = theme.Length("statusswitch.height", 28)
	StatusSwitchPadding = theme.Length("statusswitch.padding", 10)
	StatusSwitchGap     = theme.Length("statusswitch.gap", 8)
	// StatusSwitchTrackWidth and StatusSwitchTrackHeight size its
	// switch's track.
	StatusSwitchTrackWidth  = theme.Length("statusswitch.track.width", 24)
	StatusSwitchTrackHeight = theme.Length("statusswitch.track.height", 14)
	// StatusSwitchTint is how much of the state's colour fills the chip,
	// from 0 to 1.
	StatusSwitchTint = theme.Length("statusswitch.tint", 0.11)
	// StatusSwitchBreath is how faint the knob gets as it breathes while
	// busy, from 0 to 1, and StatusSwitchBreathTime one breath.
	StatusSwitchBreath     = theme.Length("statusswitch.breath", 0.45)
	StatusSwitchBreathTime = theme.Length("statusswitch.breath.time", 2)
)

// StatusSwitch says how something is, in words in a rounded chip of the
// state's colour, with a switch at its end that changes it: a
// connection, a sync, a server. Its words say only the state, the same
// whatever a click will do.
//
// The knob slides as the state changes, and the chip's colour fades from
// one state's to the next. A press squashes the knob; under the pointer
// the chip lightens. While busy, as for something on its way on, the
// knob breathes.
//
// A click, or Space or Enter with the keyboard on it, runs OnChange
// with the state asked for; a key held down asks once. The app sets the
// state with SetChecked once it has changed.
type StatusSwitch struct {
	Control
	// Label is the state in words, as "connected" or "23 ms".
	Label string
	// Ink is the state's colour: of the words, the chip's fill and the
	// switch. Unset, it is the theme's [Ink].
	Ink theme.Token[color.NRGBA]
	// Bare leaves the words out, and the chip's fill shows only under
	// the pointer: the switch alone, its colour saying the state, for
	// where room is short.
	Bare bool
	// Extra, when set, is drawn between the words and the switch, such
	// as a compact [LiveGraph] of what goes over a connection.
	Extra gunim.Node
	// OnChange runs on the UI goroutine when the switch is flipped, with
	// the state asked for; a non-nil result is sent to the application
	// as the switch's intent.
	OnChange func(on bool, u *gunim.UI) gunim.Intent

	// checked and busy are the state; knob runs from off to on, and tone
	// from the last state's colour, was, to this one's, now.
	checked, busy bool
	knob, tone    *anim.Float
	now, was      color.NRGBA
	inked         bool
	click         Clicker
	size          geom.Size
	text          shapedText
	laid          bool
}

// NewStatusSwitch returns a status switch saying label, off.
func NewStatusSwitch(label string) *StatusSwitch {
	s := &StatusSwitch{Control: newControl(), Label: label, knob: anim.NewFloat(0), tone: anim.NewFloat(1)}
	s.Add(s.knob, s.tone)
	return s
}

// Checked reports whether the switch is on.
func (s *StatusSwitch) Checked() bool { return s.checked }

// SetChecked turns the switch on or off and sends no intent. Once laid
// out, the knob slides there; before, it is there at once. u may be nil.
func (s *StatusSwitch) SetChecked(on bool, u *gunim.UI) {
	if s.checked == on {
		return
	}
	s.checked = on
	u.Invalidate()
}

// Busy reports whether the knob breathes.
func (s *StatusSwitch) Busy() bool { return s.busy }

// SetBusy has the knob breathe, as for something on its way, or rest,
// and sends no intent. u may be nil.
func (s *StatusSwitch) SetBusy(busy bool, u *gunim.UI) {
	if s.busy == busy {
		return
	}
	s.busy = busy
	u.Invalidate()
}

// Children implements [gunim.Composite]: Extra, when set.
func (s *StatusSwitch) Children() []gunim.Node {
	if s.Extra == nil {
		return nil
	}
	return []gunim.Node{s.Extra}
}

// target is the state's colour.
func (s *StatusSwitch) target(th *theme.Live) color.NRGBA {
	if s.Ink.Key() != "" {
		return s.Ink.Get(th)
	}
	return Ink.Get(th)
}

// colour is the chip's colour now, fading from the state before's.
func (s *StatusSwitch) colour() color.NRGBA {
	return anim.Mix(anim.ColorCodec, s.was, s.now, min(max(s.tone.Value(), 0), 1))
}

// Layout implements [gunim.Node]: as wide as its words, Extra and
// switch, or as it is made wider; the switch at the end.
func (s *StatusSwitch) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	th := f.Theme
	s.follow(th)
	to := float32(0)
	if s.checked {
		to = 1
	}
	if !s.laid {
		s.knob.Jump(to)
	} else {
		s.knob.Animate(to, Settle.Get(th))
	}
	// The colour fades from the last state's to this one's.
	if ink := s.target(th); !s.inked || ink != s.now {
		s.was = ink
		if s.inked {
			s.was = s.colour()
			s.tone.Jump(0)
			s.tone.Animate(1, Settle.Get(th))
		}
		s.now, s.inked = ink, true
	}
	s.laid = true
	pad, gap, tw := StatusSwitchPadding.Get(th), StatusSwitchGap.Get(th), StatusSwitchTrackWidth.Get(th)
	w := pad + tw + pad
	var extra geom.Size
	if s.Extra != nil {
		extra = kids.At(0).Layout(gunim.Constraints{Max: geom.Sz(c.Max.W, StatusSwitchHeight.Get(th))})
		if extra.W > 0 {
			w += extra.W + gap
		}
	}
	if !s.Bare {
		w += s.text.shape(faceIn(Font, th), s.Label, BadgeSize.Get(th)).Advance + gap
	}
	s.size = c.Constrain(geom.Sz(w, StatusSwitchHeight.Get(th)))
	if s.Extra != nil {
		kids.At(0).Place(geom.Pt(s.size.W-pad-tw-gap-extra.W, (s.size.H-extra.H)/2))
	}
	return s.size
}

// Paint implements [gunim.Node].
func (s *StatusSwitch) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	defer s.faint(p, box)()
	th := f.Theme
	ink := s.colour()
	r := geom.Rect{Max: box.Point()}
	radius := box.H / 2
	fill := ink
	fill.A = uint8(float32(fill.A) * min(max(StatusSwitchTint.Get(th), 0), 1))
	if s.Bare {
		fill.A = 0
	}
	fill = anim.Mix(anim.ColorCodec, fill, ChipHover.Get(th), min(max(s.hover.Value(), 0), 1))
	p.RRect(r, radius, paint.Solid(fill))
	s.paintRing(p, r, radius, th)
	pad, gap := StatusSwitchPadding.Get(th), StatusSwitchGap.Get(th)
	tw, tht := StatusSwitchTrackWidth.Get(th), StatusSwitchTrackHeight.Get(th)
	x := box.W - pad - tw
	if s.Bare && s.Extra == nil {
		x = (box.W - tw) / 2
	}
	if !s.Bare {
		run := s.text.shape(faceIn(Font, th), s.Label, BadgeSize.Get(th))
		run.PaintFaded(p, geom.Pt(pad, box.H/2-run.Height()/2), ink, max(x-pad-gap, 0), TextFade.Get(th))
	}
	if s.Extra != nil {
		kids.At(0).Paint(p)
	}
	s.paintSwitch(p, f, geom.Rc(x, box.H/2-tht/2, tw, tht), ink)
}

// paintSwitch draws the switch in track, as a [Switch] draws its own:
// the track the state's colour while on and the switch's off colour,
// edged in it, while off; the knob squashed by a press, and breathing
// while busy.
func (s *StatusSwitch) paintSwitch(p *paint.Painter, f gunim.Frame, track geom.Rect, ink color.NRGBA) {
	th := f.Theme
	on := min(max(s.knob.Value(), 0), 1)
	h := track.Size().H
	p.RRect(track, h/2, paint.Solid(anim.Mix(anim.ColorCodec, SwitchOff.Get(th), ink, on)))
	p.RRectStroke(track.Inset(geom.Uniform(0.5)), h/2, paint.Fill{}, paint.Stroke{Width: 1, Color: ink})
	inset := float32(2)
	d := h - 2*inset
	stretch := d * 0.3 * s.squash.Value()
	kx := track.Min.X + inset + (track.Size().W-2*inset-d-stretch)*on
	knob := Knob.Get(th)
	if s.busy {
		// Breathing, a breath each StatusSwitchBreathTime seconds.
		period := float64(max(StatusSwitchBreathTime.Get(th), 0.1))
		phase := math.Mod(float64(f.Now.UnixMilli())/1000, period) / period
		deep := 1 - float32(math.Abs(1-2*phase))*min(max(StatusSwitchBreath.Get(th), 0), 1)
		knob.A = uint8(float32(knob.A) * deep)
		f.RedrawAt(f.Now.Add(time.Second / 30))
	}
	p.ShadowRRect(geom.Rc(kx, track.Min.Y+inset, d+stretch, d), d/2, paint.Solid(knob),
		paint.Shadow{Offset: geom.Pt(0, 1), Blur: 2, Color: KnobShadow.Get(th)})
}

// Handle implements [gunim.Handler]: a click, or Space or Enter with
// the keyboard on it, asks for the other state.
func (s *StatusSwitch) Handle(e input.Event, u *gunim.UI) bool {
	s.showTip(e, u, s)
	th := u.Theme()
	if s.Disabled {
		return s.handleDisabled(e, u, nil)
	}
	if s.ringFollows(e, th) {
		_, lost := e.(input.FocusLost)
		return !lost
	}
	switch e := e.(type) {
	case input.PointerEnter:
		s.hover.Animate(1, Quick.Get(th))
	case input.PointerLeave:
		s.hover.Animate(0, Settle.Get(th))
		s.squash.Animate(0, Bounce.Get(th))
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		s.held = true
		s.click.Press(e, 0)
		s.squash.Animate(1, Quick.Get(th))
	case input.PointerUp:
		if !s.held {
			return false
		}
		s.held = false
		s.squash.Animate(0, Bounce.Get(th))
		if s.click.Release(e, over(e.Pos, s.size)) {
			s.flip(u)
		}
	case input.KeyPress:
		if e.Mods != 0 || e.Key != input.KeySpace && e.Key != input.KeyEnter && e.Key != input.KeyKPEnter {
			return false
		}
		if !e.Repeat {
			s.squash.Retarget(1, Quick.Get(th))
			s.squash.Animate(0, Bounce.Get(th))
			s.flip(u)
		}
	default:
		return false
	}
	return true
}

// flip runs OnChange with the state asked for, sounding the toggle.
func (s *StatusSwitch) flip(u *gunim.UI) {
	on := !s.checked
	cue := gunim.CueToggleOn
	if !on {
		cue = gunim.CueToggleOff
	}
	if s.OnChange == nil {
		u.Cue(cue, s)
		return
	}
	act0(u, s, cue, func(u *gunim.UI) gunim.Intent { return s.OnChange(on, u) })
}

// Access implements [gunim.Accessible]: a switch, named by its words.
func (s *StatusSwitch) Access() access.Info {
	st := s.accessState() | access.StateCheckable
	if s.checked {
		st |= access.StateChecked
	}
	return access.Info{Role: access.RoleSwitch, Name: s.accessName(s.Label), State: st, Actions: []string{access.ActionPress}}
}

// AccessAct implements [gunim.AccessActor]: a screen reader's press
// flips it.
func (s *StatusSwitch) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || s.Disabled {
		return false
	}
	s.flip(u)
	return true
}
