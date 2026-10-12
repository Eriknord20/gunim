package widget

import (
	"image/color"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// Chip tokens.
var (
	ChipHeight = theme.Length("chip.height", 24)
	ChipFill   = theme.Color("chip.fill", color.NRGBA{R: 0x2b, G: 0x2f, B: 0x3a, A: 0xff})
	ChipHover  = theme.Color("chip.hover", color.NRGBA{R: 0x3d, G: 0x45, B: 0x58, A: 0xff})
	// ChipLead colours a chip's lead, the part before its label, such as
	// the field a filter is on.
	ChipLead = theme.Color("chip.lead", color.NRGBA{R: 0x8a, G: 0x93, B: 0xa6, A: 0xff})
)

// Chip is a small rounded label with a cross that removes it, such as
// a filter in force. The cross sits at the chip's end; given less room
// than its text takes, the text before it ends in an ellipsis.
//
// The chip warms as the pointer comes over the cross, and a click on the
// cross removes it. It takes the keyboard by Tab, a ring fades in round the
// cross, and Delete or Backspace removes it. Disabled, it fades faint and
// takes no clicks, keys or focus.
type Chip struct {
	Control
	// Lead is drawn dim before the label.
	Lead  string
	Label string
	// Icon shows at the start of the chip, before the lead and the label, in the label's colour.
	Icon *icon.Icon
	// OnRemove runs on the UI goroutine when the cross is clicked, or Delete or Backspace is pressed; a non-nil
	// result is sent to the application as the chip's intent.
	OnRemove func(u *gunim.UI) gunim.Intent

	leadText, labelText shapedText
	leadEll, labelEll   shapedText
	// size is the chip's box and crossX the middle of its cross, from
	// the last layout or paint.
	size   geom.Size
	crossX float32
	click  Clicker
}

// NewChip returns a chip showing lead and label.
func NewChip(lead, label string) *Chip {
	return &Chip{Control: newControl(), Lead: lead, Label: label}
}

// Layout implements [gunim.Node].
func (c *Chip) Layout(cs gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	h := ChipHeight.Get(th)
	size := TextSize.Get(th) * 0.9
	w := h / 2
	if c.Icon != nil {
		w += IconSize.Get(th) + IconGap.Get(th)
	}
	if c.Lead != "" {
		w += c.leadText.shape(faceIn(BoldFont, th), c.Lead, size).Advance + 6
	}
	w += c.labelText.shape(faceIn(Font, th), c.Label, size).Advance
	c.size = cs.Constrain(geom.Sz(w+h, h))
	c.crossX = c.size.W - h/2
	c.follow(th)
	return c.size
}

// Paint implements [gunim.Node].
func (c *Chip) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	defer c.faint(p, box)()
	th := f.Theme
	c.size, c.crossX = box, box.W-box.H/2
	// The text has the room up to the cross, which takes a square at the end.
	end := box.W - box.H
	fill := anim.Mix(anim.ColorCodec, ChipFill.Get(th), ChipHover.Get(th), c.hover.Value())
	p.RRect(geom.Rect{Max: box.Point()}, box.H/2, paint.Solid(fill))
	x := box.H / 2
	if c.Icon != nil {
		if s := IconSize.Get(th); x+s <= end {
			drawIcon(p, th, c.Icon, geom.Rc(x, (box.H-s)/2, s, s), Ink.Get(th), 1)
			x += s + IconGap.Get(th)
		}
	}
	if c.Lead != "" && x < end {
		run := fitRun(c.leadText.run, &c.leadEll, end-x)
		run.Paint(p, geom.Pt(x, (box.H-run.Height())/2), ChipLead.Get(th))
		x += run.Advance + 6
	}
	if x < end {
		run := fitRun(c.labelText.run, &c.labelEll, end-x)
		run.Paint(p, geom.Pt(x, (box.H-run.Height())/2), Ink.Get(th))
	}
	// The ring goes round the cross, which is what the keys act on.
	r := box.H / 2.6
	c.paintRing(p, geom.Rc(c.crossX-r, box.H/2-r, 2*r, 2*r), r, th)
	paintCross(p, th, geom.Pt(c.crossX, box.H/2), box.H/3, Ink.Get(th))
}

// paintCross draws icon.X centred on at, with strokes size long and as thick as an icon's.
func paintCross(p *paint.Painter, th *theme.Live, at geom.Point, size float32, c color.NRGBA) {
	s := size * math.Sqrt2
	paintSmallIcon(p, th, icon.X, geom.Rc(at.X-s/2, at.Y-s/2, s, s), c)
}

// Handle implements [gunim.Handler]: a click on the cross removes the
// chip, and so do Delete and Backspace.
func (c *Chip) Handle(e input.Event, u *gunim.UI) bool {
	c.showTip(e, u, c)
	th := u.Theme()
	if c.Disabled {
		return c.handleDisabled(e, u, nil)
	}
	switch e := e.(type) {
	case input.PointerMove:
		to := float32(0)
		if e.Pos.X >= c.crossX-c.size.H/2 {
			to = 1
		}
		c.hover.Animate(to, Quick.Get(th))
	case input.PointerLeave:
		c.hover.Animate(0, Settle.Get(th))
	case input.PointerDown:
		if e.Button != input.ButtonPrimary || c.onCross(e.Pos) < 0 {
			return false
		}
		c.click.Press(e, 0)
		return true
	case input.PointerUp:
		if c.click.Release(e, c.onCross(e.Pos)) {
			c.remove(u)
		}
		return true
	case input.KeyPress:
		if (e.Key != input.KeyDelete && e.Key != input.KeyBackspace) || e.Mods != 0 {
			return false
		}
		c.remove(u)
		return true
	}
	return c.ringFollows(e, th)
}

// onCross is the chip's one target, the cross: 0 for pos on it, and -1 elsewhere.
func (c *Chip) onCross(pos geom.Point) int {
	if pos.X < c.crossX-c.size.H/2 {
		return -1
	}
	return over(pos, c.size)
}

func (c *Chip) remove(u *gunim.UI) { act0(u, c, gunim.CuePress, c.OnRemove) }

// Access implements [gunim.Accessible]: the chip reads as a button that
// removes it.
func (c *Chip) Access() access.Info {
	name := c.Label
	if c.Lead != "" {
		name = c.Lead + " " + c.Label
	}
	return access.Info{Role: access.RoleButton, Name: "Remove " + name, State: c.accessState(), Actions: []string{access.ActionPress}}
}

// AccessAct implements [gunim.AccessActor].
func (c *Chip) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || c.Disabled {
		return false
	}
	c.remove(u)
	return true
}

// Wrap sets its children in a row, and starts a new row where the next
// child would pass its width, as words wrap. It arrives with the
// children it was built with, and more come and go with
// [gunim.UI.Insert] and [gunim.UI.Remove].
type Wrap struct {
	// Gap is the space between children, and the theme's [Gap] when
	// unset.
	Gap theme.Token[float32]
	// Cross puts each child at the top of its line, or with CrossCenter or CrossEnd in its middle or at its bottom.
	Cross Cross

	kids []gunim.Node
}

// NewWrap returns a wrap of kids.
func NewWrap(kids ...gunim.Node) *Wrap { return &Wrap{Gap: Gap, kids: kids} }

// Children implements [gunim.Composite].
func (w *Wrap) Children() []gunim.Node { return w.kids }

// Layout implements [gunim.Node]. The wrap is as wide as it is given,
// and as tall as its rows.
func (w *Wrap) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	gap := float32(0)
	if w.Gap.Key() != "" {
		gap = w.Gap.Get(f.Theme)
	}
	width := c.Max.W
	var x, y, line float32
	// start is the first child of the line being filled, and at and sizes where each child goes and how big it is
	start := 0
	var at []geom.Point
	var sizes []geom.Size
	endLine := func() {
		for i := start; i < len(at); i++ {
			switch w.Cross {
			case CrossCenter:
				at[i].Y += (line - sizes[i].H) / 2
			case CrossEnd:
				at[i].Y += line - sizes[i].H
			case CrossStart, CrossStretch:
			}
		}
		start = len(at)
	}
	for kid := range kids.All {
		s := kid.Layout(gunim.Loose(geom.Sz(width, 0)))
		if x > 0 && x+s.W > width {
			endLine()
			x, y, line = 0, y+line+gap, 0
		}
		at, sizes = append(at, geom.Pt(x, y)), append(sizes, s)
		x += s.W + gap
		line = max(line, s.H)
	}
	endLine()
	i := 0
	for kid := range kids.All {
		kid.Place(at[i])
		i++
	}
	return c.Constrain(geom.Sz(width, y+line))
}

// Paint implements [gunim.Node].
func (w *Wrap) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for kid := range kids.All {
		kid.Paint(p)
	}
}
