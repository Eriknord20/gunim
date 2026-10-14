package themeedit

import (
	"fmt"
	"math"
	"strconv"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/widget"
)

// Instant is a spring that moves at once: the value is where it is going
// on the next frame, with nothing in between. A field of a motion, such
// as a cursor's, offers it as "Jumps".
var Instant = anim.Spring{Response: 0, Damping: 1}

// The presets every spring offers, but one whose field has its own.
var springPresets = []Preset{
	{Label: "Instant", Value: Instant},
	{Label: "Snappy", Value: anim.Snappy},
	{Label: "Gentle", Value: anim.Gentle},
	{Label: "Bouncy", Value: anim.Bouncy},
}

// The ranges of a spring's sliders: its speed is how long it takes, up
// to a second, and its bounce how far under critical its damping is.
const (
	maxResponse = 1
	maxBounce   = 0.9
)

// springControl edits a spring in words: its presets, a Play button that
// shows it in the preview, and, with Custom chosen, two sliders under
// the row, Speed and Bounce.
type springControl struct {
	presets *presetControl
	play    *widget.IconButton
	top     *widget.Flex
	speed   *widget.Slider
	bounce  *widget.Slider
	fold    *widget.Fold
	// spring is the value shown.
	spring anim.Spring
}

// newSpringControl returns a spring's control, offering presets.
// always offers Custom whatever the value, as the springs of All values
// do; a field with presets of its own offers it for a value none of them
// has.
func newSpringControl(label string, presets []Preset, always bool, edit editFunc, play func(*gunim.UI)) *springControl {
	c := &springControl{}
	c.presets = newPresets(presets, label, always, func(v any, commit bool, _ control, u *gunim.UI) {
		if s, ok := v.(anim.Spring); ok {
			c.showSliders(s, u)
		}
		c.fold.SetOpen(c.presets.custom, u)
		edit(v, commit, c, u)
		play(u)
	})
	c.presets.onCustom = func(u *gunim.UI) {
		c.fold.SetOpen(true, u)
		u.FocusFirstLaidOut(c.speed)
	}
	c.play = widget.NewIconButton(icon.Play, "Play "+label+" in the preview")
	c.play.IconSize = IconSize
	c.play.OnClick = func(u *gunim.UI) gunim.Intent {
		play(u)
		return nil
	}
	c.top = widget.Row(c.play, c.presets.node())
	c.top.Cross, c.top.Gap = widget.CrossCenter, NamesGap

	c.speed = widget.NewSlider(0, maxResponse)
	c.speed.Snap, c.speed.Label = 0.01, label+": speed"
	c.bounce = widget.NewSlider(0, maxBounce)
	c.bounce.Snap, c.bounce.Label = 0.01, label+": bounce"
	slid := func(commit bool) func(float32, *gunim.UI) gunim.Intent {
		return func(_ float32, u *gunim.UI) gunim.Intent {
			s := c.value()
			c.spring = s
			c.presets.show(s, u)
			edit(s, commit, c, u)
			if commit {
				play(u)
			}
			return nil
		}
	}
	c.speed.OnChange, c.speed.OnCommit = slid(false), slid(true)
	c.bounce.OnChange, c.bounce.OnCommit = slid(false), slid(true)
	speed := widget.NewSliderRow("Speed", c.speed)
	speed.Format = speedWords
	bounce := widget.NewSliderRow("Bounce", c.bounce)
	bounce.Format = bounceWords
	sliders := widget.Column(speed, bounce)
	sliders.Cross = widget.CrossStretch
	pad := widget.NewPad(sliders)
	pad.Padding = SliderPadding
	c.fold = widget.NewFold(pad, false)
	return c
}

// speedWords writes a spring's response for its Speed slider, in
// milliseconds, as "250 ms", or "Instant".
func speedWords(v float32) string {
	if v <= 0 {
		return "Instant"
	}
	return fmt.Sprintf("%.0f ms", v*1000)
}

// bounceWords writes a spring's bounce for its Bounce slider: "None", or
// how far under critical its damping is, as a percentage.
func bounceWords(v float32) string {
	if v < 0.005 {
		return "None"
	}
	return fmt.Sprintf("%.0f%%", v*100)
}

// value returns the spring the sliders hold.
func (c *springControl) value() anim.Spring {
	return anim.Spring{Response: c.speed.Value(), Damping: 1 - c.bounce.Value()}
}

// showSliders moves the sliders to s. A spring damped past critical
// bounces not at all.
func (c *springControl) showSliders(s anim.Spring, u *gunim.UI) {
	c.speed.SetValue(min(s.Response, maxResponse), u)
	c.bounce.SetValue(max(0, min(1-s.Damping, maxBounce)), u)
}

func (c *springControl) node() gunim.Node  { return c.top }
func (c *springControl) below() gunim.Node { return c.fold }

func (c *springControl) stops() []gunim.Node {
	out := []gunim.Node{c.play, c.presets.seg}
	if c.fold.Open() {
		out = append(out, c.speed, c.bounce)
	}
	return out
}

func (c *springControl) show(v any, u *gunim.UI) {
	s, ok := v.(anim.Spring)
	if !ok {
		return
	}
	c.spring = s
	c.presets.show(s, u)
	c.showSliders(s, u)
	c.fold.SetOpen(c.presets.custom, u)
}

// springSlider edits how long a spring takes with one slider, for a
// field with a range: a Play button, the slider, and the time in words
// after it, or the name of the preset that has it. The spring keeps its
// bounce.
type springSlider struct {
	play    *widget.IconButton
	slider  *widget.Slider
	words   *widget.Label
	row     *widget.Flex
	presets []Preset
	// damping is the bounce the spring keeps, as its damping.
	damping float32
}

func newSpringSlider(f Field, label string, def any, edit editFunc, play func(*gunim.UI)) *springSlider {
	c := &springSlider{presets: f.Presets, damping: 1}
	c.play = widget.NewIconButton(icon.Play, "Play "+label+" in the preview")
	c.play.IconSize = IconSize
	c.play.OnClick = func(u *gunim.UI) gunim.Intent {
		play(u)
		return nil
	}
	c.slider = widget.NewSlider(f.Min, f.Max)
	c.slider.Snap, c.slider.Label = 0.01, label
	if f.Step > 0 {
		c.slider.Snap = f.Step
	}
	if s, ok := def.(anim.Spring); ok && s.Response >= f.Min && s.Response <= f.Max {
		// A double click goes back to the program's own.
		c.slider.Rest, c.slider.HasRest = s.Response, true
	}
	moved := func(commit bool) func(float32, *gunim.UI) gunim.Intent {
		return func(v float32, u *gunim.UI) gunim.Intent {
			c.say(v)
			edit(anim.Spring{Response: v, Damping: c.damping}, commit, c, u)
			if commit {
				play(u)
			}
			return nil
		}
	}
	c.slider.OnChange, c.slider.OnCommit = moved(false), moved(true)
	c.words = widget.NewLabel("")
	c.words.NoWrap, c.words.MaxLines = true, 1
	c.row = widget.Row(c.play, &sized{child: c.slider, width: SliderWidth}, &sized{child: c.words, width: ValueWidth})
	c.row.Cross, c.row.Gap = widget.CrossCenter, NamesGap
	return c
}

// say writes how long v takes: the name of a preset that takes as
// long, or the time.
func (c *springSlider) say(v float32) {
	for _, p := range c.presets {
		if s, ok := p.Value.(anim.Spring); ok && abs(s.Response-v) < 0.005 {
			c.words.Text = p.Label
			return
		}
	}
	c.words.Text = timeWords(v)
}

// timeWords writes how long a spring takes: "Instant", "250 ms", or,
// from a second, "1.25 s".
func timeWords(v float32) string {
	if v >= 1 {
		return strconv.FormatFloat(math.Round(float64(v)*100)/100, 'f', -1, 64) + " s"
	}
	return speedWords(v)
}

func abs(v float32) float32 { return max(v, -v) }

func (c *springSlider) node() gunim.Node    { return c.row }
func (c *springSlider) below() gunim.Node   { return nil }
func (c *springSlider) stops() []gunim.Node { return []gunim.Node{c.play, c.slider} }

func (c *springSlider) show(v any, u *gunim.UI) {
	s, ok := v.(anim.Spring)
	if !ok {
		return
	}
	if s.Damping > 0 {
		c.damping = s.Damping
	}
	c.slider.SetValue(s.Response, u)
	c.say(s.Response)
}
