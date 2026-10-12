package audioui

import (
	"math"
	"strconv"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// Fader is a gain in decibels, set by a fader up and down: a drag sets
// it, finer with Shift; the wheel steps it half a decibel; a
// double-click sets it to 0 dB. Its 0 dB is marked. The cap lights
// under the pointer. A second button pressed mid-drag ends the drag
// where it got to.
//
// It takes the keyboard by Tab, and a ring fades in round its cap. Up and
// Down, or Right and Left, step it half a decibel, a tenth of one with
// Shift; Page Up and Page Down step it a tenth of its travel; Home and
// End take it to the bottom and the top.
type Fader struct {
	anim.Group
	// Value reads the gain. OnChange runs on the UI goroutine as the user
	// moves it, with the gain within Range, to a tenth of a decibel, and
	// only as it changes; it sets the gain, and a non-nil result is sent
	// to the application as the fader's intent.
	Value    func() float32
	OnChange func(v float32, u *gunim.UI) gunim.Intent
	// Range is how far the fader goes either way, in decibels: 24 when
	// zero.
	Range float32
	// Low and High are the fader's ends in decibels where they are set,
	// as a mixer's channel fader runs from -60 to 6; where both are
	// zero, the fader runs from -Range to Range.
	Low, High float32
	// Curve bends the travel. Above 1, the gains near the top get more
	// of it, as on a mixer, where 0 dB stands most of the way up; 0 and
	// 1 run it straight.
	Curve float32
	// OnBegin runs as a drag on the fader starts, and OnEnd as it ends,
	// so the gains set between are one gesture to undo. A drag ends as
	// the button is let go, as a second button is pressed, or by
	// EndDrag.
	OnBegin, OnEnd func(u *gunim.UI)
	// Label names the fader for a screen reader, as the label beside it
	// does on screen.
	Label string
	hover *anim.Float
	ring  *anim.Float
	// nudge is how far the cap shows from the gain, in decibels, as it glides there after a key stepped it.
	nudge *anim.Float
	held  bool
	from  geom.Point
	// start is where along its travel, from 0 to 1, the cap stood as
	// the drag started.
	start float32
	size  geom.Size
}

// NewFader returns a fader that reads its gain with value, and runs
// onChange as the user moves it.
func NewFader(value func() float32, onChange func(v float32, u *gunim.UI) gunim.Intent) *Fader {
	f := &Fader{Value: value, OnChange: onChange, hover: anim.NewFloat(0), ring: anim.NewFloat(0), nudge: anim.NewFloat(0)}
	f.Add(f.hover, f.ring, f.nudge)
	return f
}

func (f *Fader) span() float32 {
	if f.Range == 0 {
		return 24
	}
	return f.Range
}

// ends are the gains at the fader's bottom and top.
func (f *Fader) ends() (lo, hi float32) {
	if f.Low == 0 && f.High == 0 || f.High <= f.Low {
		return -f.span(), f.span()
	}
	return f.Low, f.High
}

// curve is the power the travel is bent by.
func (f *Fader) curve() float64 {
	if f.Curve <= 0 {
		return 1
	}
	return float64(f.Curve)
}

// travel is where gain v stands along the fader, from 0 at the bottom
// to 1 at the top.
func (f *Fader) travel(v float32) float32 {
	lo, hi := f.ends()
	t := (max(lo, min(v, hi)) - lo) / (hi - lo)
	return float32(math.Pow(float64(t), f.curve()))
}

// gainAt is the gain at t along the fader, from 0 at the bottom to 1
// at the top.
func (f *Fader) gainAt(t float32) float32 {
	lo, hi := f.ends()
	t = max(0, min(t, 1))
	return lo + (hi-lo)*float32(math.Pow(float64(t), 1/f.curve()))
}

// Dragging reports whether a drag on the fader is under way.
func (f *Fader) Dragging() bool { return f.held }

// EndDrag ends a drag under way where it got to, as letting go does,
// for a window that ends a press on its own keys, as Escape.
func (f *Fader) EndDrag(u *gunim.UI) {
	if !f.held {
		return
	}
	f.held = false
	f.hover.Animate(0, anim.Gentle)
	if f.OnEnd != nil {
		f.OnEnd(u)
	}
	u.Invalidate()
}

// Overhear implements [gunim.Overhearer]: a second button pressed
// while a drag runs ends it, as the press goes elsewhere.
func (f *Fader) Overhear(e input.Event, u *gunim.UI) {
	if _, ok := e.(input.PointerDown); ok && f.held {
		f.EndDrag(u)
	}
}

func (f *Fader) set(v float32, u *gunim.UI) {
	lo, hi := f.ends()
	v = max(lo, min(v, hi))
	v = float32(math.Round(float64(v)*10) / 10)
	if v != f.Value() && f.OnChange != nil {
		if in := f.OnChange(v, u); in != nil {
			u.Send(f, in)
		}
	}
}

// Focusable implements [gunim.Focusable].
func (f *Fader) Focusable() bool { return true }

// key steps the gain for k, and reports whether k was one of the fader's keys.
func (f *Fader) key(k input.KeyPress, u *gunim.UI) bool {
	if k.Mods.Has(input.ModControl) || k.Mods.Has(input.ModAlt) {
		return false
	}
	step := float32(0.5)
	if k.Mods.Has(input.ModShift) {
		step = 0.1
	}
	v := f.Value()
	switch k.Key {
	case input.KeyUp, input.KeyRight:
		v += step
	case input.KeyDown, input.KeyLeft:
		v -= step
	case input.KeyPageUp:
		v = f.gainAt(f.travel(v) + 0.1)
	case input.KeyPageDown:
		v = f.gainAt(f.travel(v) - 0.1)
	case input.KeyHome:
		v, _ = f.ends()
	case input.KeyEnd:
		_, v = f.ends()
	default:
		return false
	}
	was := f.Value()
	f.set(v, u)
	// The cap glides to the new gain.
	if now := f.Value(); now != was {
		f.nudge.Jump(f.nudge.Value() + was - now)
		f.nudge.Animate(0, widget.Quick.Get(u.Theme()))
	}
	return true
}

// DragsTouch implements [gunim.TouchDragger].
func (f *Fader) DragsTouch() bool { return f.held }

// Handle implements [gunim.Handler].
func (f *Fader) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		f.hover.Animate(1, anim.Snappy)
	case input.PointerLeave:
		if !f.held {
			f.hover.Animate(0, anim.Gentle)
		}
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if e.Clicks == 2 {
			f.set(0, u)
			break
		}
		f.held, f.from, f.start = true, e.Pos, f.travel(f.Value())
		if f.OnBegin != nil {
			f.OnBegin(u)
		}
	case input.PointerMove:
		if !f.held {
			return false
		}
		per := 1 / max(f.size.H-24, 1)
		if e.Mods.Has(input.ModShift) {
			per /= 10
		}
		f.set(f.gainAt(f.start-(e.Pos.Y-f.from.Y)*per), u)
	case input.PointerUp:
		if f.held {
			f.held = false
			if f.OnEnd != nil {
				f.OnEnd(u)
			}
		}
	case input.Scroll:
		n := e.Notches.Y
		if n == 0 {
			n = e.Delta.Y / 40
		}
		f.set(f.Value()+0.5*n, u)
	case input.KeyPress:
		if !f.key(e, u) {
			return false
		}
	case input.FocusRing:
		to := float32(0)
		if e.On {
			to = 1
		}
		f.ring.Animate(to, widget.RingFade.Get(u.Theme()))
	case input.FocusLost:
		f.ring.Animate(0, widget.RingFade.Get(u.Theme()))
	default:
		return false
	}
	u.Invalidate()
	return true
}

// Access implements [gunim.Accessible]: a slider of the gain, in decibels.
func (f *Fader) Access() access.Info {
	v := f.Value()
	lo, hi := f.ends()
	return access.Info{Role: access.RoleSlider, Name: f.Label, Value: strconv.FormatFloat(float64(v), 'f', 1, 32) + " dB",
		Range: &access.Range{Min: float64(lo), Max: float64(hi), Value: float64(v), Step: 0.1}}
}

// AccessAct implements [gunim.AccessActor]: a new value sets the gain.
func (f *Fader) AccessAct(r access.Request, u *gunim.UI) bool {
	if !r.SetValue {
		return false
	}
	f.set(float32(r.Value), u)
	u.Invalidate()
	return true
}

// Layout implements [gunim.Node]: a fader takes the room it is given.
func (f *Fader) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	f.size = c.Max
	return c.Max
}

// Paint implements [gunim.Node]: the fader's track, its 0 dB marked,
// and its cap, at the gain.
func (f *Fader) Paint(p *paint.Painter, fr gunim.Frame, box geom.Size, _ gunim.Children) {
	ink, ground, raised := widget.Ink.Get(fr.Theme), Ground.Get(fr.Theme), Raised.Get(fr.Theme)
	mid := box.W / 2
	// The track keeps 12 px clear at each end for the cap, less in a box under 24 tall, and the cap stays on the
	// track for a gain past Range.
	edge := min(12, box.H/2)
	top, bottom := edge, box.H-edge
	p.RRect(geom.Rc(mid-2, top, 4, bottom-top), 2, paint.Solid(Faded(ground, 0.9)))
	if lo, hi := f.ends(); lo < 0 && hi > 0 {
		zero := bottom - (bottom-top)*f.travel(0)
		p.RRect(geom.Rc(mid-7, zero, 14, 1), 0, paint.Solid(Faded(ink, 0.3)))
	}
	y := bottom - (bottom-top)*f.travel(f.Value()+f.nudge.Value())
	lit := f.hover.Value()
	if f.held {
		lit = 1
	}
	capH := min(18, box.H)
	knob := geom.Rc(2, y-capH/2, max(0, box.W-4), capH)
	widget.FocusRing(p, knob, 5, f.ring.Value(), fr.Theme)
	p.ShadowRRect(knob, 5, paint.Solid(Mix(raised, Mix(raised, ink, 0.25), lit)),
		paint.Shadow{Blur: 6, Color: Faded(ground, 0.6)})
	p.RRect(geom.Rc(knob.Min.X+5, y-0.75, max(0, knob.Size().W-10), 1.5), 0.75, paint.Solid(Faded(ink, 0.8)))
}
