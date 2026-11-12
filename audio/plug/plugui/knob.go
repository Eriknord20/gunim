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
	// KnobGlide is the motion of a knob's needle to a value set elsewhere: at rest in about 150 ms, with no overshoot.
	KnobGlide = theme.Spring("plug.knob.glide", anim.Spring{Response: 0.09, Damping: 1})
)

// knobFollows is how soon a value set after the last one makes the needle follow at once rather than glide, as an
// automation ramp moving it on every frame.
const knobFollows = 60 * time.Millisecond

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
// sets it back to its default. While it has the keyboard, the wheel
// steps it as an arrow key does, as a number field takes the wheel.
// Without the keyboard the wheel passes it by, so a pane of knobs
// scrolls under the pointer without turning one. Each turn is one gesture,
// one step to undo: of a parameter of the host's, or of whatever the
// knob is bound to. A right click on a host's parameter opens the
// host's menu for it, where the host is a [plug.ParamMenuHost]; where
// it is a [plug.ModulationHost], a ring outside the knob spans the
// reach of the control moving the parameter, either side of its value,
// and a dot on the arc shows its value as heard. A value that runs
// either side of zero fills its arc from zero. It lights as the pointer comes over it, and its tooltip
// says how to turn it. Tab brings the keyboard to it, and a focus ring
// fades in round it. A second button pressed mid-turn ends the turn
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
	// ring is how far the focus ring shows, and focused says the knob has the keyboard.
	ring    *anim.Float
	focused bool
	tip     widget.PartTip
	// Disabled greys the knob, as for a section switched off, though it
	// still turns; Locked greys it and it does not turn, as a gain a cut
	// has none of. Each glides there.
	Disabled, Locked bool
	// grey is how far the knob's arc has gone grey, faint how far a lock
	// has faded it, turned how far round its needle shows, and laid says
	// it has been laid out, before which they jump to where they are set.
	grey, faint, turned *anim.Float
	laid                bool
	// glide is the needle's motion, from the theme, and aimed how long ago the needle took its last new aim.
	glide anim.Spring
	aimed time.Duration
	// Compact makes the knob smaller, its cap and its words.
	Compact bool
	// Tooltip, where set, says what the knob does, before how to turn it.
	Tooltip string
	// Taper, where set, turns the knob unevenly across its values.
	Taper *Taper
	// OnTurn, where set, hears each value the knob is turned to, after
	// the value is set: for a view that draws what the value does.
	OnTurn func(v float64, u *gunim.UI)
	// shown is the modulation the knob last stepped with, and modded
	// says control moved it then.
	shown  plug.Modulation
	modded bool
}

// Taper is how far round a knob turns for its value, where that is not
// evenly, as a gain that spends most of its turn near 0 dB: To takes a
// parameter's Normal to how far round, both from 0 to 1, and From takes
// it back. Both rise from 0 to 1.
type Taper struct{ To, From func(float64) float64 }

// round returns how far round the knob turns for n, a Normal.
func (k *Knob) round(n float64) float64 {
	n = min(max(n, 0), 1)
	if k.Taper != nil {
		return k.Taper.To(n)
	}
	return n
}

// turnOf returns how far round the knob turns for v.
func (k *Knob) turnOf(v float64) float64 { return k.round(k.p.Normal(v)) }

// valueAt returns the value of the knob turned t of the way round.
func (k *Knob) valueAt(t float64) float64 {
	t = min(max(t, 0), 1)
	if k.Taper != nil {
		t = k.Taper.From(t)
	}
	return k.p.Plain(t)
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

// Step implements [anim.Animator]: the needle glides to a value set
// elsewhere, and follows a drag or a value that moves on every frame at
// once. Before the knob is laid out, everything jumps. A knob whose
// parameter control moves asks for the next frame while the value heard
// or the depth changes, to show it move, and rests while they stand
// still, as with the song stopped.
func (k *Knob) Step(dt time.Duration) bool {
	k.aimed += dt
	if !k.laid {
		k.aim(func(a *anim.Float, v float32) { a.Jump(v) })
	} else {
		k.aim(func(a *anim.Float, v float32) {
			switch {
			case a.Target() == v:
				return
			case a != k.turned:
				a.Animate(v, anim.Snappy)
				return
			case k.held || k.aimed < knobFollows:
				a.Jump(v)
			default:
				a.Animate(v, k.glide)
			}
			k.aimed = 0
		})
	}
	moving := k.Group.Step(dt)
	m, modded := k.modulation()
	changed := modded != k.modded || modded && m != k.shown
	k.shown, k.modded = m, modded
	return moving || changed
}

// Transition implements [gunim.Transitioner]: a knob coming into the tree shows where it is set at once.
func (k *Knob) Transition(p gunim.Presence, _ gunim.Frame) bool {
	if p == gunim.Entering {
		k.aim(func(a *anim.Float, v float32) { a.Jump(v) })
	}
	return true
}

// aim moves its greys by to where Disabled and Locked have them, and its needle to its value.
func (k *Knob) aim(to func(a *anim.Float, v float32)) {
	to(k.grey, anim.OnOff(k.Disabled || k.Locked))
	to(k.faint, anim.OnOff(k.Locked))
	to(k.turned, float32(k.turnOf(k.v)))
}

// needle returns how far round the needle shows, from 0 to 1.
func (k *Knob) needle() float64 { return min(max(float64(k.turned.Value()), 0), 1) }

// normalAt returns the Normal of the knob turned t of the way round.
func (k *Knob) normalAt(t float64) float64 {
	if k.Taper != nil {
		return k.Taper.From(t)
	}
	return t
}

// firstLayout puts every spring where it is set on the first layout.
func (k *Knob) firstLayout() {
	if !k.laid {
		k.laid = true
		k.aim(func(a *anim.Float, v float32) { a.Jump(v) })
	}
}

// jumpUnlaid puts the needle at the value at once while there is no UI or no layout yet to glide in.
func (k *Knob) jumpUnlaid(u *gunim.UI) {
	if u == nil || !k.laid {
		k.turned.Jump(float32(k.turnOf(k.v)))
	}
}

// NewKnobFunc returns a knob of what p describes, at v, turning it by
// a gesture of begin, a set for each move, and end: a value of a
// plugin's state, say, as a band's frequency.
func NewKnobFunc(p plug.Param, v float64, begin func(*gunim.UI), set func(float64, *gunim.UI), end func(*gunim.UI)) *Knob {
	k := &Knob{p: p, v: v, begin: begin, set: set, end: end, hover: anim.NewFloat(0), ring: anim.NewFloat(0),
		grey: anim.NewFloat(0), faint: anim.NewFloat(0), turned: anim.NewFloat(0), glide: KnobGlide.Default(),
		aimed: knobFollows}
	k.turned.Jump(float32(k.turnOf(v)))
	k.Add(k.hover, k.ring, k.grey, k.faint, k.turned)
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
	k.jumpUnlaid(u)
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

// Follow shows v, set elsewhere, unless the knob is held: the needle
// glides there once the knob is laid out, and jumps with a nil UI.
func (k *Knob) Follow(v float64, u *gunim.UI) {
	if !k.held && v != k.v {
		k.v = v
		k.jumpUnlaid(u)
		u.Invalidate()
	}
}

// Layout implements [gunim.Node].
func (k *Knob) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	k.firstLayout()
	if k.Compact {
		return c.Constrain(geom.Sz(CompactW, CompactH))
	}
	return c.Constrain(geom.Sz(KnobW, KnobH))
}

// Paint implements [gunim.Node].
func (k *Knob) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	k.glide = KnobGlide.Get(th)
	ink, accent := widget.Ink.Get(th), widget.Accent.Get(th)
	accent = anim.MixColor(accent, paint.Faded(ink, 0.35), k.grey.Value())
	hover := k.hover.Value()
	r, text := float32(17), float32(10.5)
	if k.Compact {
		r, text = 14, 9.5
	}
	end := widget.Faint(p, box, k.faint.Value()*0.6)
	defer end()
	c := geom.Pt(box.W/2, 4+r)
	if hover > 0.01 {
		// Under the pointer, a glow about the knob.
		p.ShadowRRect(geom.Rc(c.X-r+2, c.Y-r+2, 2*r-4, 2*r-4), r-2, paint.Solid(paint.Faded(accent, 0)),
			paint.Shadow{Blur: 12 * hover, Color: paint.Faded(accent, 0.35*hover)})
	}
	arc := func(t0, t1 float64, w float32, col color.NRGBA) { arcRibbon(p, c, r, t0, t1, w, col) }
	arc(0, 1, 3.5, anim.MixColor(KnobGroove.Get(th), paint.Faded(ink, 0.25), 0.5*hover))
	t := k.needle()
	zero := 0.0
	if k.p.Min < 0 && k.p.Max > 0 {
		zero = k.turnOf(0)
	}
	if k.held {
		arc(zero, t, 5, paint.Faded(accent, 0.25))
	}
	arc(zero, t, 3.5, accent)
	if m, ok := k.modulation(); ok {
		k.paintModulation(p, m, c, r, anim.MixColor(accent, KnobRing.Get(th), 0.7))
	}
	p.ShadowRRect(geom.Rc(c.X-r+6, c.Y-r+6, 2*r-12, 2*r-12), r-6, paint.Fill{Gradient: &paint.Gradient{
		From: geom.Pt(c.X, c.Y-r), To: geom.Pt(c.X, c.Y+r), Start: KnobCapTop.Get(th), End: KnobCapBottom.Get(th)}},
		paint.Shadow{Offset: geom.Pt(0, 2), Blur: 4, Color: color.NRGBA{A: 0x90}})
	capRing(p, c, r, k.ring.Value(), th)
	a := knobFrom + knobSweep*t
	audioui.Segment(p, geom.Pt(c.X+(r-13)*float32(math.Cos(a)), c.Y+(r-13)*float32(math.Sin(a))),
		geom.Pt(c.X+(r-7)*float32(math.Cos(a)), c.Y+(r-7)*float32(math.Sin(a))), 2, ink)
	name := audioui.Shaped(k.p.Name, text, false, false)
	name.Paint(p, geom.Pt((box.W-name.Advance)/2, c.Y+r+4), paint.Faded(ink, 0.6+0.3*hover))
	val := audioui.Shaped(k.p.Text(k.v), text, false, true)
	col := paint.Faded(ink, 0.9)
	if k.held {
		col = accent
	}
	val.Paint(p, geom.Pt((box.W-val.Advance)/2, c.Y+r+4+name.Height()), col)
}

// The turn runs three quarters round, clockwise from bottom left: from where it starts, in radians, and how far.
const knobFrom, knobSweep = 0.75 * math.Pi, 1.5 * math.Pi

// arcRibbon draws the arc of a knob's turn from t0 to t1, of radius rad about c, w wide in col, as one ribbon with
// round ends.
func arcRibbon(p *paint.Painter, c geom.Point, rad float32, t0, t1 float64, w float32, col color.NRGBA) {
	if t1 < t0 {
		t0, t1 = t1, t0
	}
	length := float64(rad) * knobSweep * (t1 - t0)
	if length < 0.01 {
		return
	}
	// About one point per 3 px, and two more at each end to round it
	const capPts = 2
	n := max(int(math.Ceil(length/3)), 1)
	var buf [64]paint.RibbonPoint
	pts := buf[:0]
	h := w / 2
	at := func(t float64) (geom.Point, geom.Point) {
		a := knobFrom + knobSweep*t
		sin, cos := math.Sincos(a)
		return geom.Pt(c.X+rad*float32(cos), c.Y+rad*float32(sin)), geom.Pt(-float32(sin), float32(cos))
	}
	round := func(end, dir geom.Point, out float32, from, to int, step int) {
		for j := from; j != to; j += step {
			th := float64(j) / capPts * math.Pi / 2
			d := h * float32(math.Sin(th)) * out
			pts = append(pts, paint.RibbonPoint{At: end.Add(dir.Mul(d)), Width: w * float32(math.Cos(th)), Color: col})
		}
	}
	start, dir := at(t0)
	round(start, dir, -1, capPts, 0, -1)
	for i := range n + 1 {
		pt, _ := at(t0 + (t1-t0)*float64(i)/float64(n))
		pts = append(pts, paint.RibbonPoint{At: pt, Width: w, Color: col})
	}
	end, dir := at(t1)
	round(end, dir, 1, 1, capPts+1, 1)
	p.Ribbon(pts)
}

// capRing draws the focus ring of a knob of radius r about c, at strength t, between its cap and its arc.
func capRing(p *paint.Painter, c geom.Point, r, t float32, th *theme.Live) {
	// Halfway between the cap (r-6) and the arc (r-1.75), less the 3 px FocusRing adds
	h := r - 6.875
	widget.FocusRing(p, geom.Rc(c.X-h, c.Y-h, 2*h, 2*h), h, t, th)
}

// paintModulation draws how control moves the knob's value: a ring
// outside the arc, of radius r about c, as far below and above the
// value as the control reaches, or all round where
// control sets the value; and a dot on the arc at the value heard, all in ring.
func (k *Knob) paintModulation(p *paint.Painter, m plug.Modulation, c geom.Point, r float32, ring color.NRGBA) {
	at := func(t float64, rad float32) geom.Point {
		a := knobFrom + knobSweep*t
		return geom.Pt(c.X+rad*float32(math.Cos(a)), c.Y+rad*float32(math.Sin(a)))
	}
	n := k.normalAt(k.needle())
	t0, t1, a := k.round(n+m.Low), k.round(n+m.High), float32(0.85)
	if m.Sets {
		t0, t1, a = 0, 1, 0.35
	}
	if math.Abs(t1-t0) >= 1.0/40 {
		arcRibbon(p, c, r+5, t0, t1, 1.5, paint.Faded(ring, a))
	}
	dot := at(k.turnOf(m.Now), r)
	p.ShadowRRect(geom.Rc(dot.X-3, dot.Y-3, 6, 6), 3, paint.Solid(ring), paint.Shadow{Blur: 6, Color: paint.Faded(ring, 0.6)})
}

// turn sets the knob to v as one move of the gesture held.
func (k *Knob) turn(v float64, u *gunim.UI) {
	v = k.p.Clamp(v)
	if v == k.v {
		return
	}
	k.v = v
	k.set(v, u)
	if k.OnTurn != nil {
		k.OnTurn(v, u)
	}
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

// stepSize is a step of an arrow key, of the way round.
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
	case input.FocusGained:
		k.focused = true
	case input.FocusLost:
		k.focused = false
	}
	if followRing(k.ring, e, u) {
		return true
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
		k.held, k.from, k.start = true, e.Pos, k.turnOf(k.v)
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
		k.turn(k.valueAt(k.start+float64((k.from.Y-e.Pos.Y)/px)), u)
	case input.PointerUp:
		if !k.held {
			return false
		}
		k.held = false
		k.end(u)
		k.hover.Animate(0, widget.Quick.Get(u.Theme()))
		u.Invalidate()
	case input.Scroll:
		// Only while the knob has the keyboard, as a number field takes it, so scrolling a pane turns no knob it
		// passes. The wheel turned up gives a positive Delta.Y, and turns the knob up.
		if !k.focused || e.Touch || e.Delta.Y == 0 {
			return false
		}
		k.stepTo(k.valueAt(k.turnOf(k.v)+math.Copysign(k.stepSize(), float64(e.Delta.Y))), u)
	case input.KeyPress:
		// Ctrl, Alt or Super with a key is the window's shortcut, such as
		// Alt+Left to the loop's start: the knob leaves it be.
		if e.Mods.Has(input.ModControl) || e.Mods.Has(input.ModAlt) || e.Mods.Has(input.ModSuper) {
			return false
		}
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
		k.stepTo(k.valueAt(k.turnOf(k.v)+d), u)
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
