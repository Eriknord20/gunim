package widget

import (
	"image/color"
	"math"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// ChordInput tokens.
var (
	ChordInputFill = theme.Color("chordinput.fill", color.NRGBA{R: 0x1f, G: 0x2d, B: 0x4a, A: 0xff})
	// ChordInputPrompt colours the prompt, shown while no modifier is held.
	// ChordInputRefused colours the ring after a press the application turned down.
	ChordInputRefused = theme.Color("chordinput.refused", color.NRGBA{R: 0xf8, G: 0x6c, B: 0x6c, A: 0xff})
	ChordInputPrompt  = theme.Foreground("chordinput.prompt", color.NRGBA{R: 0xb4, G: 0xbb, B: 0xc9, A: 0xff})
)

// chordPulse is how long the ring of a [ChordInput] takes to swell and fade.
const chordPulse = 1200 * time.Millisecond

// ChordInput reads a keyboard shortcut by having it pressed. It listens
// while it has the keyboard, and shows Prompt until a modifier is held,
// then the modifiers held so far, such as "Ctrl+Shift+…".
//
// The width glides to fit what it says, each modifier's key fades in and
// out on its own, and a ring swells and fades for as long as it listens.
//
// Every key is its own: Tab, the function keys and a shortcut of the
// window's all come here first. The first key that is no modifier ends
// it with OnCommit. Escape with no modifier ends it with OnDismiss, and
// so does the keyboard going elsewhere. The press ending it is passed on
// whole, for the application to decide what it can bind.
type ChordInput struct {
	Control
	// Prompt is what it says with no modifier held, and its name for a
	// screen reader.
	Prompt string
	// OnCommit runs on the UI goroutine with the press that ended it. A
	// non-nil result is sent as the widget's intent.
	OnCommit func(k input.KeyPress, u *gunim.UI) gunim.Intent
	// OnDismiss runs on the UI goroutine when it ends with nothing
	// pressed. A non-nil result is sent as the widget's intent.
	OnDismiss func(u *gunim.UI) gunim.Intent
	// Refused turns the ring to say the last press was turned down. The application sets it, and the next key clears it.
	Refused bool

	held input.Mods
	done bool
	// phase is how far through the pulse the ring is, and on says the
	// keyboard is here, so the ring pulses.
	phase time.Duration
	on    bool
	// width is the box's width, gliding. Each modifier's key fades and
	// grows in by itself as it is held, and promptIn fades as the first comes.
	width    *anim.Float
	promptIn *anim.Float
	capIn    [len(chordMods)]*anim.Float
	capRun   [len(chordMods)]shapedText
	words    shapedText
	dots     shapedText
	laid     bool
}

// chordMods are the modifiers shown, in the order they are.
var chordMods = [...]struct {
	mod  input.Mods
	name string
}{{input.ModControl, "Ctrl"}, {input.ModAlt, "Alt"}, {input.ModShift, "Shift"}, {input.ModSuper, "Super"}}

// NewChordInput returns an input saying prompt until a modifier is held.
func NewChordInput(prompt string) *ChordInput {
	c := &ChordInput{Control: newControl(), Prompt: prompt, width: anim.NewFloat(0), promptIn: anim.NewFloat(1)}
	c.Add(c.width, c.promptIn)
	for i := range c.capIn {
		c.capIn[i] = anim.NewFloat(0)
		c.Add(c.capIn[i])
	}
	return c
}

// Held returns the modifiers held now, as the input shows them.
func (c *ChordInput) Held() input.Mods { return c.held }

// Listen starts it listening again after a press the application turned
// down, with the keyboard still here.
func (c *ChordInput) Listen(u *gunim.UI) {
	c.done, c.held = false, 0
	u.Invalidate()
}

// Step implements [gunim.Animator]: the ring pulses while the keyboard is here.
func (c *ChordInput) Step(dt time.Duration) bool {
	moving := c.Control.Step(dt)
	if !c.on {
		return moving
	}
	c.phase = (c.phase + dt) % chordPulse
	return true
}

// faded is ink at t of its strength.
func faded(ink color.NRGBA, t float32) color.NRGBA {
	ink.A = uint8(float32(ink.A) * t)
	return ink
}

// shown is what it says now.
func (c *ChordInput) shown() string {
	if c.held == 0 {
		return c.Prompt
	}
	var parts []string
	for _, m := range chordMods {
		if c.held.Has(m.mod) {
			parts = append(parts, m.name)
		}
	}
	return strings.Join(parts, "+") + "+…"
}

// Layout implements [gunim.Node].
func (c *ChordInput) Layout(cs gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	h := ChipHeight.Get(th)
	size := TextSize.Get(th) * 0.9
	motion := Quick.Get(th)
	pad, gap := ChipCapPad.Get(th), ChipCapGap.Get(th)
	want := c.words.shape(faceIn(Font, th), c.Prompt, size).Advance + h
	if c.held != 0 {
		want = c.dots.shape(faceIn(Font, th), "…", size).Advance + h/2
	}
	for i, m := range chordMods {
		to := float32(0)
		run := c.capRun[i].shape(faceIn(Font, th), m.name, size)
		if c.held.Has(m.mod) {
			to = 1
			want += run.Advance + 2*pad + gap
		}
		if !c.laid {
			c.capIn[i].Jump(to)
		} else if c.capIn[i].Target() != to {
			c.capIn[i].Animate(to, motion)
		}
	}
	promptTo := float32(0)
	if c.held == 0 {
		promptTo = 1
	}
	if !c.laid {
		c.width.Jump(want)
		c.promptIn.Jump(promptTo)
		c.laid = true
	} else {
		if c.width.Target() != want {
			c.width.Animate(want, motion)
		}
		if c.promptIn.Target() != promptTo {
			c.promptIn.Animate(promptTo, motion)
		}
	}
	c.follow(th)
	return cs.Constrain(geom.Sz(c.width.Value(), h))
}

// Paint implements [gunim.Node]: the field, its ring, and what it says, each modifier held drawn as a key.
func (c *ChordInput) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	defer c.faint(p, box)()
	th := f.Theme
	r := geom.Rect{Max: box.Point()}
	radius := ChipCapRadius.Get(th) + 2
	p.RRect(r, radius, paint.Solid(ChordInputFill.Get(th)))
	if c.Refused {
		ring := ChordInputRefused.Get(th)
		ring.A = uint8(float32(ring.A) * c.pulse())
		p.RRectStroke(r.Inset(geom.Uniform(-3)), radius+3, paint.Fill{}, paint.Stroke{Width: 2, Color: ring})
	} else {
		FocusRing(p, r, radius, c.pulse(), th)
	}
	defer p.Layer(paint.LayerOpts{Bounds: r, Clip: true, Opacity: 1})()
	t := min(max(c.promptIn.Value(), 0), 1)
	if t > 0.01 {
		c.words.run.Paint(p, geom.Pt(box.H/2, (box.H-c.words.run.Height())/2), faded(ChordInputPrompt.Get(th), t))
	}
	pad, gap, capR := ChipCapPad.Get(th), ChipCapGap.Get(th), ChipCapRadius.Get(th)
	const in = 3
	h := box.H - 2*in
	x := float32(in)
	for i := range chordMods {
		a := min(max(c.capIn[i].Value(), 0), 1)
		if a < 0.01 {
			continue
		}
		run := c.capRun[i].run
		w := run.Advance + 2*pad
		p.RRect(geom.Rc(x, in, w, h), capR, paint.Solid(faded(ChipCap.Get(th), a)))
		run.Paint(p, geom.Pt(x+pad, in+(h-run.Height())/2), faded(Ink.Get(th), a))
		x += (w + gap) * a
	}
	if d := 1 - t; d > 0.01 {
		c.dots.run.Paint(p, geom.Pt(x+gap, (box.H-c.dots.run.Height())/2), faded(Ink.Get(th), d))
	}
}

// pulse is how strong the ring is: swelling and fading while the
// keyboard is here, and the ordinary ring as it goes.
func (c *ChordInput) pulse() float32 {
	if !c.on {
		return c.ring.Value()
	}
	wave := (1 - float32(math.Cos(2*math.Pi*float64(c.phase)/float64(chordPulse)))) / 2
	return 0.6 + 0.4*wave
}

// KeepsKey implements [gunim.KeyKeeper]: every key is the input's while it listens.
func (c *ChordInput) KeepsKey(input.KeyPress, *gunim.UI) bool { return !c.Disabled }

// isModifier reports whether k is a modifier alone.
func isModifier(k input.Key) bool {
	switch k {
	case input.KeyLeftShift, input.KeyRightShift, input.KeyLeftControl, input.KeyRightControl,
		input.KeyLeftAlt, input.KeyRightAlt, input.KeyLeftSuper, input.KeyRightSuper:
		return true
	}
	return false
}

// modOf is the modifier k holds down, or none.
func modOf(k input.Key) input.Mods {
	switch k {
	case input.KeyLeftShift, input.KeyRightShift:
		return input.ModShift
	case input.KeyLeftControl, input.KeyRightControl:
		return input.ModControl
	case input.KeyLeftAlt, input.KeyRightAlt:
		return input.ModAlt
	case input.KeyLeftSuper, input.KeyRightSuper:
		return input.ModSuper
	}
	return 0
}

// Handle implements [gunim.Handler].
func (c *ChordInput) Handle(e input.Event, u *gunim.UI) bool {
	c.showTip(e, u, c)
	if c.Disabled {
		return c.handleDisabled(e, u, nil)
	}
	c.ringFollows(e, u.Theme())
	switch e := e.(type) {
	case input.FocusGained:
		c.on, c.phase, c.done, c.held = true, 0, false, 0
		u.Invalidate()
		return false
	case input.FocusLost:
		c.on = false
		c.end(u, func() { act0(u, c, gunim.CueClose, c.OnDismiss) })
		return false
	case input.KeyPress:
		c.Refused = false
		if isModifier(e.Key) {
			c.held = e.Mods | modOf(e.Key)
			u.Invalidate()
			return true
		}
		if e.Repeat {
			return true
		}
		if e.Key == input.KeyEscape && e.Mods == 0 {
			c.end(u, func() { act0(u, c, gunim.CueClose, c.OnDismiss) })
			return true
		}
		c.held = e.Mods
		c.end(u, func() { act(u, c, gunim.CueSelect, c.OnCommit, e) })
		return true
	case input.KeyRelease:
		c.held = (e.Mods | c.held) &^ modOf(e.Key)
		u.Invalidate()
		return true
	case input.TextInput:
		return true
	}
	return false
}

// end runs fn the once, for the first thing to end the listening.
func (c *ChordInput) end(u *gunim.UI, fn func()) {
	if c.done {
		return
	}
	c.done = true
	u.Invalidate()
	fn()
}

// Access implements [gunim.Accessible].
func (c *ChordInput) Access() access.Info {
	return access.Info{Role: access.RoleTextField, Name: c.accessName(c.Prompt), Value: c.shown(), State: c.accessState()}
}
