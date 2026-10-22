package chat

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/widget"
)

// Sizes of a shared file's card.
const (
	fileCardW = 360
	fileCardH = 56
)

// fileCard is a file of the project's shared in a message: its icon, its name, its size and the folder it is in. A
// click opens it among the project's files. It takes no room while the message shares none.
type fileCard struct {
	anim.Group
	f     FileRef
	hover *anim.Float

	name, about text.Run
	card        geom.Rect
}

func newFileCard(f FileRef) *fileCard {
	c := &fileCard{f: f, hover: anim.NewFloat(0)}
	c.Add(c.hover)
	return c
}

// Layout implements [gunim.Node].
func (c *fileCard) Layout(cs gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	if c.f.Name == "" {
		return geom.Sz(cs.Max.W, 0)
	}
	th := f.Theme
	w := min(cs.Max.W, fileCardW)
	textW := w - 56 - 12
	c.name = shapeFit(widget.BoldFont.Get(th), c.f.Name, widget.TextSize.Get(th), textW)
	where := "the project's top"
	if c.f.Folder != "" {
		where = c.f.Folder
	}
	c.about = shapeFit(widget.Font.Get(th), SizeText(c.f.Size)+" · in "+where, SmallText.Get(th), textW)
	c.card = geom.Rc(0, 0, w, fileCardH)
	return geom.Sz(cs.Max.W, fileCardH)
}

// Paint implements [gunim.Node].
func (c *fileCard) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	if c.f.Name == "" {
		return
	}
	th := f.Theme
	r := c.card
	p.RRect(r, 8, paint.Solid(QuoteFill.Get(th)))
	if h := min(c.hover.Value(), 1); h > 0.01 {
		p.RRect(r, 8, paint.Solid(fade(RowHot.Get(th), h)))
	}
	widget.PaintIcon(p, th, FileIcon(c.f.Name), geom.Rc(16, r.Size().H/2-12, 24, 24), Faint.Get(th))
	y := (r.Size().H - c.name.Height() - c.about.Height() - 2) / 2
	c.name.Paint(p, geom.Pt(56, y), widget.LinkInk.Get(th))
	c.about.Paint(p, geom.Pt(56, y+c.name.Height()+2), Faint.Get(th))
}

// Handle implements [gunim.Handler]: the card lights under the pointer, and a click opens the file among the
// project's files.
func (c *fileCard) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		c.hover.Animate(map[bool]float32{false: 0, true: 1}[c.card.Contains(e.Pos)], widget.Quick.Get(u.Theme()))
	case input.PointerLeave:
		c.hover.Animate(0, widget.Settle.Get(u.Theme()))
	case input.PointerDown:
		if e.Button == input.ButtonPrimary && c.card.Contains(e.Pos) {
			u.Send(c, FileOpened{Folder: c.f.Folder, Name: c.f.Name})
			return true
		}
	}
	return false
}

// Cursor implements [gunim.CursorShaper].
func (c *fileCard) Cursor(pt geom.Point) input.Cursor {
	if c.card.Contains(pt) {
		return input.CursorHand
	}
	return input.CursorArrow
}
