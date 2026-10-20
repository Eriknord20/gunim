package chat

import (
	"strconv"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/widget"
)

// Sizes of a poll's card.
const (
	pollMaxW    = 420
	pollPad     = 12
	pollOptionH = 34
	pollGap     = 6
)

// pollCard is a poll in a message: its question, an option a row, and how many voted. Each option's tooltip names
// who voted for it.
type pollCard struct {
	id   string
	p    Poll
	tips []*widget.Tooltip
	opts []*pollOption

	question text.Paragraph
	foot     text.Run
	card     geom.Rect
}

func newPollCard(id string, p Poll) *pollCard {
	c := &pollCard{id: id, p: p}
	for i, o := range p.Options {
		opt := newPollOption(id, i, o, p.Voters)
		c.opts = append(c.opts, opt)
		c.tips = append(c.tips, widget.NewTooltip(opt, o.Who))
	}
	return c
}

// set shows p, whose options are the card's from its start.
func (c *pollCard) set(p Poll, u *gunim.UI) {
	c.p = p
	for i, o := range p.Options {
		if i < len(c.opts) {
			c.opts[i].set(o, p.Voters, u)
			c.tips[i].Text = o.Who
		}
	}
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (c *pollCard) Children() []gunim.Node {
	out := make([]gunim.Node, len(c.tips))
	for i, t := range c.tips {
		out[i] = t
	}
	return out
}

// Layout implements [gunim.Node]: the question, then the options one under another, then the count of voters.
func (c *pollCard) Layout(cs gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	if c.p.Question == "" {
		for k := range kids.All {
			k.Layout(gunim.Tight(geom.Size{}))
		}
		return geom.Sz(cs.Max.W, 0)
	}
	th := f.Theme
	w := min(cs.Max.W, pollMaxW)
	inner := w - 2*pollPad
	c.question = widget.BoldFont.Get(th).Layout(c.p.Question, text.Style{Size: widget.TextSize.Get(th)}, inner)
	y := pollPad + c.question.Size.H + pollGap
	for k := range kids.All {
		k.Layout(gunim.Tight(geom.Sz(inner, pollOptionH)))
		k.Place(geom.Pt(pollPad, y))
		y += pollOptionH + pollGap
	}
	voters := strconv.Itoa(c.p.Voters) + " votes"
	if c.p.Voters == 1 {
		voters = "1 vote"
	}
	c.foot = widget.Font.Get(th).Shape(voters, SmallText.Get(th))
	y += c.foot.Height() + pollPad - pollGap/2
	c.card = geom.Rc(0, 0, w, y)
	return geom.Sz(cs.Max.W, y)
}

// Paint implements [gunim.Node].
func (c *pollCard) Paint(p *paint.Painter, f gunim.Frame, _ geom.Size, kids gunim.Children) {
	if c.p.Question == "" {
		return
	}
	th := f.Theme
	p.RRect(c.card, 10, paint.Solid(QuoteFill.Get(th)))
	c.question.Paint(p, geom.Pt(pollPad, pollPad), widget.Ink.Get(th))
	for k := range kids.All {
		k.Paint(p)
	}
	c.foot.Paint(p, geom.Pt(pollPad, c.card.Max.Y-pollPad+pollGap/2-c.foot.Height()), Faint.Get(th))
}

// pollOption is one option of a poll: a bar as long as its share of the votes, its text and its count, and a tick
// on the user's own vote. A click votes for it, on another moves the vote, and on the user's own takes it back.
type pollOption struct {
	anim.Group
	id    string
	i     int
	o     PollOption
	share *anim.Float
	hover *anim.Float
	text  text.Run
	count text.Run
}

func newPollOption(id string, i int, o PollOption, voters int) *pollOption {
	opt := &pollOption{id: id, i: i, o: o, share: anim.NewFloat(shareOf(o, voters)), hover: anim.NewFloat(0)}
	opt.Add(opt.share, opt.hover)
	return opt
}

// shareOf returns o's share of the votes.
func shareOf(o PollOption, voters int) float32 {
	if voters == 0 {
		return 0
	}
	return float32(o.Votes) / float32(voters)
}

// set shows o, the bar springing to its new share.
func (opt *pollOption) set(o PollOption, voters int, u *gunim.UI) {
	opt.o = o
	opt.share.Animate(shareOf(o, voters), widget.Settle.Get(u.Theme()))
}

// Layout implements [gunim.Node].
func (opt *pollOption) Layout(cs gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	opt.count = widget.BoldFont.Get(th).Shape(strconv.Itoa(opt.o.Votes), SmallText.Get(th))
	opt.text = shapeFit(widget.Font.Get(th), opt.o.Text, widget.TextSize.Get(th), cs.Max.W-60)
	return cs.Max
}

// Paint implements [gunim.Node].
func (opt *pollOption) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink, accent := widget.Ink.Get(th), widget.Accent.Get(th)
	row := geom.Rect{Max: box.Point()}
	p.RRect(row, 8, paint.Solid(ChipFillOff.Get(th)))
	if h := min(opt.hover.Value(), 1); h > 0.01 {
		p.RRect(row, 8, paint.Solid(fade(ink, 0.06*h)))
	}
	if s := min(max(opt.share.Value(), 0), 1); s > 0.001 {
		bar := fade(accent, 0.28)
		if opt.o.Mine {
			bar = fade(accent, 0.5)
		}
		p.RRect(geom.Rc(0, 0, box.W*s, box.H), 8, paint.Solid(bar))
	}
	if opt.o.Mine {
		p.RRectStroke(row, 8, paint.Fill{}, paint.Stroke{Width: 1, Color: accent})
	}
	mid := box.H / 2
	x := float32(12)
	if opt.o.Mine {
		s := float32(16)
		widget.PaintIcon(p, th, icon.Check, geom.Rc(x, mid-s/2, s, s), accent)
		x += s + 6
	}
	opt.text.Paint(p, geom.Pt(x, mid-opt.text.Height()/2), ink)
	opt.count.Paint(p, geom.Pt(box.W-12-opt.count.Advance, mid-opt.count.Height()/2), Faint.Get(th))
}

// Handle implements [gunim.Handler]: the option lights under the pointer, and a click votes for it. Other events
// go on to the tooltip around it.
func (opt *pollOption) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		opt.hover.Animate(1, widget.Quick.Get(u.Theme()))
	case input.PointerLeave:
		opt.hover.Animate(0, widget.Settle.Get(u.Theme()))
	case input.PointerDown:
		if e.Button == input.ButtonPrimary {
			u.Send(opt, PollVoted{ID: opt.id, Option: opt.i})
			return true
		}
	}
	return false
}

// Cursor implements [gunim.CursorShaper].
func (opt *pollOption) Cursor(geom.Point) input.Cursor { return input.CursorHand }
