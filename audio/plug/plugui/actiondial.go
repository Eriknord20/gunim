package plugui

import (
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// ActionDial is a button for a task that takes a while, as a knob
// stands: a cap with its icon on it, and under it, as under a knob,
// its title and a word on how it stands. While the task runs, the
// knob's arc fills as far as it has got, and the icon is a square, to
// stop it; as it ends, the arc glows a moment. Stale greys its word, as
// what it last did no longer holds.
type ActionDial struct {
	anim.Group
	// Title is written under the cap, and Value under it, as a knob's
	// name and value; Icon is on the cap at rest.
	Title, Value string
	Icon         *icon.Icon
	// Tooltip says what a click does; Disabled greys it, and a click
	// does nothing.
	Tooltip  string
	Disabled bool
	// Stale greys the value: what it says no longer holds.
	Stale bool
	// OnClick is called as it is clicked.
	OnClick func(u *gunim.UI)
	running bool
	// progress is how far the task has got, gliding; hover how far the
	// pointer is on it, and glow how far it glows, as the task ends.
	progress, hover, glow *anim.Float
	tip                   widget.PartTip
}

// NewActionDial returns a dial titled title, with ic on its cap.
func NewActionDial(title string, ic *icon.Icon) *ActionDial {
	d := &ActionDial{Title: title, Icon: ic, progress: anim.NewFloat(0), hover: anim.NewFloat(0), glow: anim.NewFloat(0)}
	d.Add(d.progress, d.hover, d.glow)
	return d
}

// Running says the task runs.
func (d *ActionDial) Running() bool { return d.running }

// Start shows the task running, from none of the way.
func (d *ActionDial) Start(u *gunim.UI) {
	d.running = true
	d.progress.Jump(0)
	d.glow.Jump(0)
	if u != nil {
		u.Invalidate()
	}
}

// SetProgress shows the task got as far as t, from 0 to 1.
func (d *ActionDial) SetProgress(t float32, u *gunim.UI) {
	if u == nil {
		d.progress.Jump(t)
		return
	}
	d.progress.Animate(min(max(t, 0), 1), anim.Snappy)
	u.Invalidate()
}

// Finish shows the task ended: done, the arc filling and glowing a
// moment before it fades, or stopped, the arc emptying.
func (d *ActionDial) Finish(done bool, u *gunim.UI) {
	d.running = false
	if u == nil {
		d.progress.Jump(0)
		return
	}
	if done {
		d.progress.Jump(1)
		d.glow.Jump(1)
		d.glow.Animate(0, anim.Spring{Response: 1.2, Damping: 1})
	}
	d.progress.Animate(0, anim.Spring{Response: 1.2, Damping: 1})
	u.Invalidate()
}

// Step implements [gunim.Animator].
func (d *ActionDial) Step(dt time.Duration) bool { return d.Group.Step(dt) }

// Layout implements [gunim.Node]: as big as a knob.
func (d *ActionDial) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Constrain(geom.Sz(KnobW, KnobH))
}

// Paint implements [gunim.Node].
func (d *ActionDial) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink, accent := widget.Ink.Get(th), widget.Accent.Get(th)
	hover, glow := d.hover.Value(), d.glow.Value()
	end := widget.Faint(p, box, onOff(d.Disabled)*0.6)
	defer end()
	const r, text = float32(17), float32(10.5)
	c := geom.Pt(box.W/2, 4+r)
	if lit := max(hover, glow); lit > 0.01 {
		p.ShadowRRect(geom.Rc(c.X-r+2, c.Y-r+2, 2*r-4, 2*r-4), r-2, paint.Solid(audioui.Faded(accent, 0)),
			paint.Shadow{Blur: 12 + 8*glow, Color: audioui.Faded(accent, 0.35*hover+0.6*glow)})
	}
	// The arc, as a knob's, three quarters round from bottom left: its
	// groove, and how far the task has got.
	const from, sweep = 0.75 * math.Pi, 1.5 * math.Pi
	arc := func(t1 float64, w float32, col color.NRGBA) {
		n := max(int(t1*32), 1)
		for i := range n {
			a0 := from + sweep*t1*float64(i)/float64(n)
			a1 := from + sweep*t1*float64(i+1)/float64(n)
			audioui.Segment(p, geom.Pt(c.X+r*float32(math.Cos(a0)), c.Y+r*float32(math.Sin(a0))),
				geom.Pt(c.X+r*float32(math.Cos(a1)), c.Y+r*float32(math.Sin(a1))), w, col)
		}
	}
	arc(1, 3.5, audioui.Mix(KnobGroove.Get(th), audioui.Faded(ink, 0.25), 0.5*hover))
	if t := float64(min(max(d.progress.Value(), 0), 1)); t > 0.005 {
		arc(t, 3.5, accent)
	}
	p.ShadowRRect(geom.Rc(c.X-r+6, c.Y-r+6, 2*r-12, 2*r-12), r-6, paint.Fill{Gradient: &paint.Gradient{
		From: geom.Pt(c.X, c.Y-r), To: geom.Pt(c.X, c.Y+r), Start: KnobCapTop.Get(th), End: KnobCapBottom.Get(th)}},
		paint.Shadow{Offset: geom.Pt(0, 2), Blur: 4, Color: color.NRGBA{A: 0x90}})
	// On the cap, its icon at rest; while the task runs, a square, to
	// stop it.
	mark := audioui.Mix(audioui.Faded(ink, 0.75), accent, max(hover, glow))
	if d.running {
		p.RRect(geom.Rc(c.X-4, c.Y-4, 8, 8), 1.5, paint.Solid(accent))
	} else if d.Icon != nil {
		widget.PaintIcon(p, th, d.Icon, geom.Rc(c.X-7, c.Y-7, 14, 14), mark)
	}
	name := audioui.Shaped(d.Title, text, false, false)
	name.Paint(p, geom.Pt((box.W-name.Advance)/2, c.Y+r+4), audioui.Faded(ink, 0.6+0.3*hover))
	val := audioui.Shaped(d.Value, text, false, true)
	col := audioui.Faded(ink, 0.9)
	switch {
	case d.running:
		col = accent
	case d.Stale:
		col = audioui.Faded(ink, 0.4)
	}
	// Cut short where the value is wider than the dial.
	vx := max((box.W-val.Advance)/2, 0)
	clip := p.Layer(paint.LayerOpts{Bounds: geom.Rc(-8, c.Y+r, box.W+16, box.H-c.Y-r), Opacity: 1, Clip: true})
	val.Paint(p, geom.Pt(vx, c.Y+r+4+name.Height()), col)
	clip()
}

// Focusable implements [gunim.Focusable].
func (d *ActionDial) Focusable() bool { return !d.Disabled }

// Handle implements [gunim.Handler]: a click, or Space or Enter, clicks
// it.
func (d *ActionDial) Handle(e input.Event, u *gunim.UI) bool {
	d.tip.Handle(e, u, d, d.Tooltip)
	switch e := e.(type) {
	case input.PointerEnter:
		d.hover.Animate(1, widget.Quick.Get(u.Theme()))
		return false
	case input.PointerLeave:
		d.hover.Animate(0, widget.Quick.Get(u.Theme()))
		return false
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		d.tip.Hide(u)
		d.click(u)
	case input.KeyPress:
		if e.Key != input.KeySpace && e.Key != input.KeyEnter {
			return false
		}
		d.click(u)
	default:
		return false
	}
	return true
}

// click clicks it, unless it is disabled.
func (d *ActionDial) click(u *gunim.UI) {
	if d.Disabled || d.OnClick == nil {
		return
	}
	u.Cue(gunim.CueTick, d)
	d.OnClick(u)
}

// Access implements [gunim.Accessible]: a button, named by its title,
// its value saying how it stands.
func (d *ActionDial) Access() access.Info {
	st := access.State(0)
	if d.Disabled {
		st = access.StateDisabled
	}
	return access.Info{Role: access.RoleButton, Name: d.Title, Value: d.Value, Description: d.Tooltip, State: st,
		Actions: []string{access.ActionPress}}
}

// AccessAct implements [gunim.AccessActor]: pressing clicks it.
func (d *ActionDial) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress {
		return false
	}
	d.click(u)
	return true
}
