package main

import (
	"image/color"
	"strconv"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// Reaction chip tokens.
var (
	ChipHeight  = theme.Length("chat.reaction.height", 26)
	ChipFillOff = theme.Color("chat.reaction.fill", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x0e})
	ChipFillOn  = theme.Color("chat.reaction.fill.on", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x30})
)

// reactionBar is the row of a message's reactions, each a chip with its emoji and count, and a chip at its end
// to add one. It takes no room while the message has none.
type reactionBar struct {
	add   *addChip
	chips map[string]*widget.Tooltip
	order []string
	// first holds the chips the bar starts with, before the engine has it.
	first []gunim.Node
}

func newReactionBar(id string, rs []Reaction, react func(opener gunim.Node, id string, u *gunim.UI)) *reactionBar {
	add := &addChip{id: id, react: react, hover: anim.NewFloat(0)}
	add.Add(add.hover)
	b := &reactionBar{add: add, chips: map[string]*widget.Tooltip{}}
	for _, r := range rs {
		tip := b.chip(id, r)
		b.first = append(b.first, tip)
	}
	return b
}

// chip makes the chip for r, and keeps it.
func (b *reactionBar) chip(id string, r Reaction) *widget.Tooltip {
	tip := widget.NewTooltip(newReactChip(id, r), r.Who)
	b.chips[r.Emoji] = tip
	b.order = append(b.order, r.Emoji)
	return tip
}

var chipGap = theme.Length("chat.reaction.gap", 6)

// set shows rs: new reactions arrive as chips before the one that adds, gone ones leave, and a changed count pops.
func (b *reactionBar) set(id string, rs []Reaction, u *gunim.UI) {
	keep := make(map[string]bool, len(rs))
	for _, r := range rs {
		keep[r.Emoji] = true
		if tip, ok := b.chips[r.Emoji]; ok {
			tip.Text = r.Who
			if chip, is := tip.Children()[0].(*reactChip); is {
				chip.set(r, u)
			}
			continue
		}
		at := len(b.order)
		u.InsertAt(b, at, b.chip(id, r))
	}
	kept := b.order[:0]
	for _, e := range b.order {
		if keep[e] {
			kept = append(kept, e)
			continue
		}
		u.Remove(b.chips[e])
		delete(b.chips, e)
	}
	b.order = kept
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (b *reactionBar) Children() []gunim.Node {
	return append(append([]gunim.Node(nil), b.first...), b.add)
}

// Layout implements [gunim.Node]: the chips in a row, going on to another where the row runs out.
func (b *reactionBar) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	gap := chipGap.Get(f.Theme)
	var x, y, line float32
	for k := range kids.All {
		if k.Node() == b.add && len(b.chips) == 0 {
			k.Layout(gunim.Tight(geom.Size{}))
			continue
		}
		s := k.Layout(gunim.Loose(c.Max))
		if x > 0 && x+s.W > c.Max.W {
			x, y, line = 0, y+line+gap, 0
		}
		k.Place(geom.Pt(x, y))
		x += s.W + gap
		line = max(line, s.H)
	}
	if len(b.chips) == 0 {
		return geom.Sz(c.Max.W, 0)
	}
	return geom.Sz(c.Max.W, y+line)
}

// Paint implements [gunim.Node].
func (b *reactionBar) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	if len(b.chips) == 0 {
		return
	}
	for k := range kids.All {
		k.Paint(p)
	}
}

// reactChip is one reaction: its emoji and how many reacted with it, lit when the user is one of them. A click adds
// or takes back the user's.
type reactChip struct {
	anim.Group
	id    string
	r     Reaction
	pop   *anim.Float
	hover *anim.Float
	on    *anim.Float
	emoji text.Run
	count text.Run
}

func newReactChip(id string, r Reaction) *reactChip {
	c := &reactChip{id: id, r: r, pop: anim.NewFloat(0.6), hover: anim.NewFloat(0), on: anim.NewFloat(0)}
	c.Add(c.pop, c.hover, c.on)
	if r.Mine {
		c.on.Jump(1)
	}
	return c
}

// set shows r, popping when its count changed.
func (c *reactChip) set(r Reaction, u *gunim.UI) {
	if r.Count != c.r.Count {
		c.pop.Jump(1.3)
		c.pop.Animate(1, widget.Bounce.Get(u.Theme()))
	}
	c.r = r
	c.on.Animate(map[bool]float32{false: 0, true: 1}[r.Mine], widget.Quick.Get(u.Theme()))
	u.Invalidate()
}

// Transition implements [gunim.Transitioner]: a new chip pops in.
func (c *reactChip) Transition(pr gunim.Presence, f gunim.Frame) bool {
	switch pr {
	case gunim.Entering:
		c.pop.Animate(1, widget.Bounce.Get(f.Theme))
	case gunim.Present:
	case gunim.Exiting:
		c.pop.Animate(0, widget.Quick.Get(f.Theme))
	}
	return !c.pop.Active()
}

// Layout implements [gunim.Node].
func (c *reactChip) Layout(_ gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	h := ChipHeight.Get(th)
	c.emoji = text.Default().Shape(c.r.Emoji, h*0.62)
	c.count = widget.BoldFont.Get(th).Shape(strconv.Itoa(c.r.Count), SmallText.Get(th))
	return geom.Sz(10+c.emoji.Advance+5+c.count.Advance+10, h)
}

// Paint implements [gunim.Node].
func (c *reactChip) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	s := max(c.pop.Value(), 0)
	defer p.Push(paint.Scale(s, geom.Pt(box.W/2, box.H/2)))()
	on := min(max(c.on.Value(), 0), 1)
	fill := anim.Mix(anim.ColorCodec, ChipFillOff.Get(th), ChipFillOn.Get(th), on)
	if h := min(c.hover.Value(), 1); h > 0 {
		fill = anim.Mix(anim.ColorCodec, fill, widget.Ink.Get(th), 0.08*h)
	}
	border := fade(widget.Accent.Get(th), on)
	r := geom.Rect{Max: box.Point()}
	p.RRectStroke(r, box.H/2, paint.Solid(fill), paint.Stroke{Width: 1, Color: border})
	mid := box.H / 2
	c.emoji.Paint(p, geom.Pt(10, mid-c.emoji.Height()/2), widget.Ink.Get(th))
	ink := anim.Mix(anim.ColorCodec, Faint.Get(th), widget.Accent.Get(th), on)
	c.count.Paint(p, geom.Pt(10+c.emoji.Advance+5, mid-c.count.Height()/2), ink)
}

// Handle implements [gunim.Handler].
func (c *reactChip) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		c.hover.Animate(1, widget.Quick.Get(u.Theme()))
	case input.PointerLeave:
		c.hover.Animate(0, widget.Settle.Get(u.Theme()))
	case input.PointerDown:
		if e.Button == input.ButtonPrimary {
			u.Send(c, ReactionToggled{ID: c.id, Emoji: c.r.Emoji})
			return true
		}
	}
	return false
}

// Cursor implements [gunim.CursorShaper].
func (c *reactChip) Cursor(geom.Point) input.Cursor { return input.CursorHand }

// addChip is the chip at the end of a message's reactions that opens the emoji picker to add one.
type addChip struct {
	anim.Group
	id    string
	react func(opener gunim.Node, id string, u *gunim.UI)
	hover *anim.Float
	size  geom.Size
}

// Layout implements [gunim.Node].
func (c *addChip) Layout(_ gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	h := ChipHeight.Get(f.Theme)
	c.size = geom.Sz(h*1.4, h)
	return c.size
}

// Paint implements [gunim.Node].
func (c *addChip) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	fill := anim.Mix(anim.ColorCodec, ChipFillOff.Get(th), widget.Ink.Get(th), 0.08*min(c.hover.Value(), 1))
	p.RRect(geom.Rect{Max: box.Point()}, box.H/2, paint.Solid(fill))
	s := box.H * 0.6
	widget.PaintIcon(p, th, icon.SmilePlus, geom.Rc((box.W-s)/2, (box.H-s)/2, s, s), Faint.Get(th))
}

// Handle implements [gunim.Handler].
func (c *addChip) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		c.hover.Animate(1, widget.Quick.Get(u.Theme()))
	case input.PointerLeave:
		c.hover.Animate(0, widget.Settle.Get(u.Theme()))
	case input.PointerDown:
		if e.Button == input.ButtonPrimary {
			c.react(c, c.id, u)
			return true
		}
	}
	return false
}

// Cursor implements [gunim.CursorShaper].
func (c *addChip) Cursor(geom.Point) input.Cursor { return input.CursorHand }
