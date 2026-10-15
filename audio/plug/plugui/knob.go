// Package plugui holds controls a Go plugin's views share, each working
// a parameter through the [plug.Host], so every turn is undone as the
// host undoes it.
package plugui

import (
	"image/color"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The knob's colours: the cap, from its top to its bottom, and the
// groove its arc runs in. The arc is the theme's accent.
var (
	KnobCapTop    = theme.Color("plug.knob.cap.top", color.NRGBA{R: 0x44, G: 0x4a, B: 0x5c, A: 0xff})
	KnobCapBottom = theme.Color("plug.knob.cap.bottom", color.NRGBA{R: 0x22, G: 0x26, B: 0x31, A: 0xff})
	KnobGroove    = theme.Color("plug.knob.groove", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x16})
	// KnobRing is the ring of the control moving a knob's value.
	KnobRing = theme.Color("plug.knob.ring", color.NRGBA{R: 0xff, G: 0xb3, B: 0x47, A: 0xff})
)

// KnobW and KnobH are how big a knob is: its cap, its name and its
// value under it; CompactW and CompactH how big a compact one is.
const (
	KnobW    = 54
	KnobH    = 76
	CompactW = 48
	CompactH = 62
)

// Knob turns a value, as a synth's knob does: a drag up turns it up,
// ten times finer with Shift; the arrow keys step it; a double click
// sets it back to its default. The wheel passes it by, so a pane of
// knobs scrolls under the pointer without turning one. Each turn is one gesture,
// one step to undo: of a parameter of the host's, or of whatever the
// knob is bound to. A right click on a host's parameter opens the
// host's menu for it, where the host is a [plug.ParamMenuHost]; where
// it is a [plug.ModulationHost], a ring outside the knob spans the
// reach of the control moving the parameter, either side of its value,
// and a dot on the arc shows its value as heard. A value that runs
// either side of zero fills its arc from zero. It lights as the pointer comes over it, and its tooltip
// says how to turn it. A second button pressed mid-turn ends the turn
// where it got to.
type Knob struct {
	anim.Group
	// h is the host whose parameter the knob turns, nil for a knob
	// bound to something else.
	h plug.Host
	p plug.Param
	// begin, set and end are a gesture on the value the knob turns.
	begin func(u *gunim.UI)
	set   func(v float64, u *gunim.UI)
	end   func(u *gunim.UI)
	v     float64
	held  bool
	from  geom.Point
	start float64
	hover *anim.Float
	tip   widget.PartTip
	// Disabled greys the knob, as for a section switched off, though it
	// still turns; Locked greys it and it does not turn, as a gain a cut
	// has none of.
	Disabled, Locked bool
	// Compact makes the knob smaller, its cap and its words.
	Compact bool
	// Tooltip, where set, says what the knob does, before how to turn it.
	Tooltip string
	// shown is the modulation the knob last stepped with, and modded
	// says control moved it then.
	shown  plug.Modulation
	modded bool
}

// NewKnob returns a knob for p, a parameter of h's, at its value in h.
func NewKnob(h plug.Host, p plug.Param) *Knob {
	k := NewKnobFunc(p, h.Param(p.ID),
		func(u *gunim.UI) { h.Begin(p.ID, u) },
		func(v float64, u *gunim.UI) { h.Set(p.ID, v, u) },
		func(u *gunim.UI) { h.End(p.ID, u) })
	k.h = h
	return k
}

// modulation is how control moves the knob's parameter, and false
// where none does.
func (k *Knob) modulation() (plug.Modulation, bool) {
	mh, ok := k.h.(plug.ModulationHost)
	if !ok {
		return plug.Modulation{}, false
	}
	return mh.Modulation(k.p.ID)
}

// Step implements [anim.Animator]: a knob whose parameter control moves
// asks for the next frame while the value heard or the depth changes,
// to show it move, and rests while they stand still, as with the song
// stopped.
func (k *Knob) Step(dt time.Duration) bool {
	moving := k.Group.Step(dt)
	m, modded := k.modulation()
	changed := modded != k.modded || modded && m != k.shown
	k.shown, k.modded = m, modded
	return moving || changed
}

// NewKnobFunc returns a knob of what p describes, at v, turning it by
// a gesture of begin, a set for each move, and end: a value of a
// plugin's state, say, as a band's frequency.
func NewKnobFunc(p plug.Param, v float64, begin func(*gunim.UI), set func(float64, *gunim.UI), end func(*gunim.UI)) *Knob {
	k := &Knob{p: p, v: v, begin: begin, set: set, end: end, hover: anim.NewFloat(0)}
	k.Add(k.hover)
	return k
}

// SetParam makes the knob turn what p describes, at v, as a band's Q
// becoming its slope as it turns into a cut.
func (k *Knob) SetParam(p plug.Param, v float64, u *gunim.UI) {
	if k.held {
		k.held = false
		k.end(u)
	}
	k.p, k.v = p, v
	u.Invalidate()
}

// EndDrag ends a turn under way where it got to, as letting go does,
// for a window that ends a press on its own keys, as Escape.
func (k *Knob) EndDrag(u *gunim.UI) {
	if !k.held {
		return
	}
	k.held = false
	k.end(u)
	k.hover.Animate(0, widget.Quick.Get(u.Theme()))
	u.Invalidate()
}

// Dragging reports whether a turn by the pointer is under way.
func (k *Knob) Dragging() bool { return k.held }

// Overhear implements [gunim.Overhearer]: a second button pressed
// while a turn runs ends it, as the press goes elsewhere.
func (k *Knob) Overhear(e input.Event, u *gunim.UI) {
	if _, ok := e.(input.PointerDown); ok && k.held {
		k.EndDrag(u)
	}
}

// Value returns the value the knob shows.
func (k *Knob) Value() float64 { return k.v }

// Param returns the knob's parameter.
func (k *Knob) Param() plug.Param { return k.p }

// Follow shows v, set elsewhere, unless the knob is held.
func (k *Knob) Follow(v float64, u *gunim.UI) {
	if !k.held && v != k.v {
		k.v = v
		u.Invalidate()
	}
}

// Layout implements [gunim.Node].
func (k *Knob) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	if k.Compact {
		return c.Constrain(geom.Sz(CompactW, CompactH))
	}
	return c.Constrain(geom.Sz(KnobW, KnobH))
}

// Paint implements [gunim.Node].
func (k *Knob) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink, accent := widget.Ink.Get(th), widget.Accent.Get(th)
	if k.Disabled || k.Locked {
		accent = audioui.Faded(ink, 0.35)
	}
	hover := k.hover.Value()
	r, text := float32(17), float32(10.5)
	if k.Compact {
		r, text = 14, 9.5
	}
	end := widget.Faint(p, box, onOff(k.Locked)*0.6)
	defer end()
	c := geom.Pt(box.W/2, 4+r)
	if hover > 0.01 {
		// Under the pointer, a glow about the knob.
		p.ShadowRRect(geom.Rc(c.X-r+2, c.Y-r+2, 2*r-4, 2*r-4), r-2, paint.Solid(audioui.Faded(accent, 0)),
			paint.Shadow{Blur: 12 * hover, Color: audioui.Faded(accent, 0.35*hover)})
	}
	// The turn runs three quarters round, clockwise from bottom left.
	const from, sweep = 0.75 * math.Pi, 1.5 * math.Pi
	arc := func(t0, t1 float64, w float32, col color.NRGBA) {
		if t1 < t0 {
			t0, t1 = t1, t0
		}
		n := max(int((t1-t0)*32), 1)
		for i := range n {
			a0 := from + sweep*(t0+(t1-t0)*float64(i)/float64(n))
			a1 := from + sweep*(t0+(t1-t0)*float64(i+1)/float64(n))
			audioui.Segment(p, geom.Pt(c.X+r*float32(math.Cos(a0)), c.Y+r*float32(math.Sin(a0))),
				geom.Pt(c.X+r*float32(math.Cos(a1)), c.Y+r*float32(math.Sin(a1))), w, col)
		}
	}
	arc(0, 1, 3.5, audioui.Mix(KnobGroove.Get(th), audioui.Faded(ink, 0.25), 0.5*hover))
	t := k.p.Normal(k.v)
	zero := 0.0
	if k.p.Min < 0 && k.p.Max > 0 {
		zero = k.p.Normal(0)
	}
	if k.held {
		arc(zero, t, 5, audioui.Faded(accent, 0.25))
	}
	arc(zero, t, 3.5, accent)
	if m, ok := k.modulation(); ok {
		k.paintModulation(p, m, c, r, t, audioui.Mix(accent, KnobRing.Get(th), 0.7))
	}
	p.ShadowRRect(geom.Rc(c.X-r+6, c.Y-r+6, 2*r-12, 2*r-12), r-6, paint.Fill{Gradient: &paint.Gradient{
		From: geom.Pt(c.X, c.Y-r), To: geom.Pt(c.X, c.Y+r), Start: KnobCapTop.Get(th), End: KnobCapBottom.Get(th)}},
		paint.Shadow{Offset: geom.Pt(0, 2), Blur: 4, Color: color.NRGBA{A: 0x90}})
	a := from + sweep*t
	audioui.Segment(p, geom.Pt(c.X+(r-13)*float32(math.Cos(a)), c.Y+(r-13)*float32(math.Sin(a))),
		geom.Pt(c.X+(r-7)*float32(math.Cos(a)), c.Y+(r-7)*float32(math.Sin(a))), 2, ink)
	name := audioui.Shaped(k.p.Name, text, false, false)
	name.Paint(p, geom.Pt((box.W-name.Advance)/2, c.Y+r+4), audioui.Faded(ink, 0.6+0.3*hover))
	val := audioui.Shaped(k.p.Text(k.v), text, false, true)
	col := audioui.Faded(ink, 0.9)
	if k.held {
		col = accent
	}
	val.Paint(p, geom.Pt((box.W-val.Advance)/2, c.Y+r+4+name.Height()), col)
}

// paintModulation draws how control moves the knob's value: a ring
// outside the arc, of radius r about c, as far below and above the
// value, at t along it, as the control reaches, or all round where
// control sets the value; and a dot on the arc at the value heard, all in ring.
func (k *Knob) paintModulation(p *paint.Painter, m plug.Modulation, c geom.Point, r float32, t float64, ring color.NRGBA) {
	const from, sweep = 0.75 * math.Pi, 1.5 * math.Pi
	out := r + 5
	at := func(t float64, rad float32) geom.Point {
		a := from + sweep*t
		return geom.Pt(c.X+rad*float32(math.Cos(a)), c.Y+rad*float32(math.Sin(a)))
	}
	t0, t1, a := min(max(t+m.Low, 0), 1), min(max(t+m.High, 0), 1), float32(0.85)
	if m.Sets {
		t0, t1, a = 0, 1, 0.35
	}
	if t1 < t0 {
		t0, t1 = t1, t0
	}
	if n := int((t1 - t0) * 40); n > 0 {
		for i := range n {
			audioui.Segment(p, at(t0+(t1-t0)*float64(i)/float64(n), out), at(t0+(t1-t0)*float64(i+1)/float64(n), out), 1.5, audioui.Faded(ring, a))
		}
	}
	dot := at(k.p.Normal(m.Now), r)
	p.ShadowRRect(geom.Rc(dot.X-3, dot.Y-3, 6, 6), 3, paint.Solid(ring), paint.Shadow{Blur: 6, Color: audioui.Faded(ring, 0.6)})
}

func onOff(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// turn sets the knob to v as one move of the gesture held.
func (k *Knob) turn(v float64, u *gunim.UI) {
	v = k.p.Clamp(v)
	if v == k.v {
		return
	}
	k.v = v
	k.set(v, u)
	u.Invalidate()
}

// stepTo sets the knob to v as a gesture of its own.
func (k *Knob) stepTo(v float64, u *gunim.UI) {
	if v = k.p.Clamp(v); v == k.v {
		return
	}
	k.begin(u)
	k.turn(v, u)
	k.end(u)
}

// tooltip is what the knob's tooltip says.
func (k *Knob) tooltip() string {
	how := "Drag up or down to turn, with Shift for fine steps; double-click to reset"
	if k.Tooltip != "" {
		return k.Tooltip + ". " + how
	}
	return k.p.Name + ": " + strings.ToLower(how[:1]) + how[1:]
}

// stepSize is a step of an arrow key, along Normal.
func (k *Knob) stepSize() float64 {
	if k.p.Steps > 0 {
		return 1 / float64(k.p.Steps)
	}
	return 0.01
}

// Focusable implements [gunim.Focusable].
func (k *Knob) Focusable() bool { return !k.Locked }

// DragsTouch says a finger drags the knob rather than scrolling.
func (k *Knob) DragsTouch() bool { return true }

// Handle implements [gunim.Handler].
func (k *Knob) Handle(e input.Event, u *gunim.UI) bool {
	if !k.held {
		k.tip.Handle(e, u, k, k.tooltip())
	}
	switch e.(type) {
	case input.PointerEnter:
		k.hover.Animate(1, widget.Quick.Get(u.Theme()))
		return false
	case input.PointerLeave:
		if !k.held {
			k.hover.Animate(0, widget.Quick.Get(u.Theme()))
		}
		return false
	}
	if k.Locked {
		return false
	}
	switch e := e.(type) {
	case input.PointerDown:
		if e.Button == input.ButtonSecondary && !k.held {
			return k.openMenu(e.Pos, u)
		}
		if e.Button != input.ButtonPrimary {
			return false
		}
		k.tip.Hide(u)
		if e.Clicks == 2 {
			k.stepTo(k.p.Default, u)
			return true
		}
		k.held, k.from, k.start = true, e.Pos, k.p.Normal(k.v)
		k.begin(u)
		u.Invalidate()
	case input.PointerMove:
		if !k.held {
			return false
		}
		px := float32(180)
		if e.Mods.Has(input.ModShift) {
			px *= 10
		}
		k.turn(k.p.Plain(k.start+float64((k.from.Y-e.Pos.Y)/px)), u)
	case input.PointerUp:
		if !k.held {
			return false
		}
		k.held = false
		k.end(u)
		k.hover.Animate(0, widget.Quick.Get(u.Theme()))
		u.Invalidate()
	case input.KeyPress:
		d := k.stepSize()
		if e.Mods.Has(input.ModShift) {
			d *= 10
		}
		switch e.Key {
		case input.KeyUp, input.KeyRight:
		case input.KeyDown, input.KeyLeft:
			d = -d
		case input.KeyHome:
			d = -1
		case input.KeyEnd:
			d = 1
		default:
			return false
		}
		k.stepTo(k.p.Plain(k.p.Normal(k.v)+d), u)
	default:
		return false
	}
	return true
}

// openMenu opens the host's menu for the knob's parameter, at pos in
// the knob, and reports whether it opened one.
func (k *Knob) openMenu(pos geom.Point, u *gunim.UI) bool {
	mh, ok := k.h.(plug.ParamMenuHost)
	if !ok {
		return false
	}
	b, ok := u.Bounds(k)
	if !ok {
		return false
	}
	k.tip.Hide(u)
	return mh.ParamMenu(k.p.ID, b.Min.Add(pos), u)
}

// Access implements [gunim.Accessible]: a slider, named as its
// parameter, its value as the parameter writes it.
func (k *Knob) Access() access.Info {
	st := access.State(0)
	if k.Disabled || k.Locked {
		st = access.StateDisabled
	}
	step := 0.0
	if k.p.Steps > 0 {
		step = (k.p.Max - k.p.Min) / float64(k.p.Steps)
	}
	return access.Info{Role: access.RoleSlider, Name: k.p.Name, Value: k.p.Text(k.v), State: st,
		Range: &access.Range{Min: k.p.Min, Max: k.p.Max, Value: k.v, Step: step}}
}

// AccessAct implements [gunim.AccessActor]: a value set, as one gesture.
func (k *Knob) AccessAct(r access.Request, u *gunim.UI) bool {
	if !r.SetValue || k.Locked {
		return false
	}
	k.stepTo(r.Value, u)
	return true
}

// String returns the knob's value, written, for tests and logs.
func (k *Knob) String() string { return k.p.Name + " " + strconv.FormatFloat(k.v, 'g', 4, 64) }
