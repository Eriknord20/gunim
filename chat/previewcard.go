package chat

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/markdown"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/widget"
)

// Sizes of a link's card.
const (
	cardMaxW  = 480
	cardThumb = 72
	cardPad   = 10
	cardBarW  = 3
	cardLines = 3
)

// previewCard is a link's card under the message that holds it: the site, the page's title as a link, a few lines
// of its description, and its picture. It takes no room while the message has none.
type previewCard struct {
	anim.Group
	p     Preview
	image func(id string) *paint.Image
	hover *anim.Float
	in    *anim.Float

	site, title text.Run
	desc        text.Paragraph
	// card is the card's box in the node, and thumb the picture's in the card.
	card, thumb geom.Rect
}

func newPreviewCard(p Preview, image func(id string) *paint.Image) *previewCard {
	c := &previewCard{p: p, image: image, hover: anim.NewFloat(0), in: anim.NewFloat(0)}
	c.Add(c.hover, c.in)
	if p.URL != "" {
		c.in.Jump(1)
	}
	return c
}

// set shows p, fading the card in when it first arrives.
func (c *previewCard) set(p Preview, u *gunim.UI) {
	if p == c.p {
		return
	}
	if c.p.URL == "" && p.URL != "" {
		c.in.Animate(1, widget.Settle.Get(u.Theme()))
	}
	c.p = p
	u.Invalidate()
}

// Layout implements [gunim.Node].
func (c *previewCard) Layout(cs gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	if c.p.URL == "" {
		return geom.Sz(cs.Max.W, 0)
	}
	th := f.Theme
	w := min(cs.Max.W, cardMaxW)
	textX := float32(cardBarW + cardPad)
	textW := w - textX - cardPad
	if c.p.Picture != "" {
		textW -= cardThumb + cardPad
	}
	c.site = widget.Font.Get(th).Shape(c.p.Site, SmallText.Get(th))
	c.title = shapeFit(widget.BoldFont.Get(th), c.p.Title, widget.TextSize.Get(th), textW)
	c.desc = widget.Font.Get(th).Layout(c.p.Description, text.Style{Size: SmallText.Get(th), MaxLines: cardLines}, textW)
	y := float32(cardPad)
	y += c.site.Height() + 2
	y += c.title.Height() + 2
	y += c.desc.Size.H
	h := max(y+cardPad, cardThumb+2*cardPad)
	if c.p.Picture != "" {
		c.thumb = geom.Rc(w-cardPad-cardThumb, cardPad, cardThumb, cardThumb)
	}
	c.card = geom.Rc(0, 0, w, h)
	return geom.Sz(cs.Max.W, h*min(max(c.in.Value(), 0), 1))
}

// Paint implements [gunim.Node].
func (c *previewCard) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	if c.p.URL == "" || box.H < 0.5 {
		return
	}
	th := f.Theme
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: min(max(c.in.Value(), 0), 1), Clip: true})()
	r := c.card
	p.RRect(r, 8, paint.Solid(QuoteFill.Get(th)))
	p.RRect(geom.Rc(r.Min.X, r.Min.Y, cardBarW, r.Size().H), 1.5, paint.Solid(AvatarTint(c.p.Site)))
	x, y := r.Min.X+cardBarW+cardPad, r.Min.Y+cardPad
	c.site.Paint(p, geom.Pt(x, y), Faint.Get(th))
	y += c.site.Height() + 2
	link := widget.LinkInk.Get(th)
	c.title.Paint(p, geom.Pt(x, y), link)
	if h := min(c.hover.Value(), 1); h > 0.01 {
		p.RRect(geom.Rc(x, y+c.title.Ascent+1.5, c.title.Advance, 1), 0, paint.Solid(fade(link, h)))
	}
	y += c.title.Height() + 2
	c.desc.Paint(p, geom.Pt(x, y), Faint.Get(th))
	if c.p.Picture == "" {
		return
	}
	img := c.image(c.p.Picture)
	if img == nil {
		return
	}
	// The picture fills its square, cut to it from the middle.
	iw, ih := img.Size()
	side := float32(min(iw, ih))
	src := geom.Rc((float32(iw)-side)/2, (float32(ih)-side)/2, side, side)
	p.Image(img, c.thumb.Add(r.Min), paint.ImageOpts{Src: src, Radius: 6, Opacity: 1})
}

// Handle implements [gunim.Handler]: the title lights under the pointer, and a click on the card opens the link.
func (c *previewCard) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		c.hover.Animate(map[bool]float32{false: 0, true: 1}[c.card.Contains(e.Pos)], widget.Quick.Get(u.Theme()))
	case input.PointerLeave:
		c.hover.Animate(0, widget.Settle.Get(u.Theme()))
	case input.PointerDown:
		if e.Button == input.ButtonPrimary && c.card.Contains(e.Pos) {
			u.Send(c, markdown.Link{URL: c.p.URL})
			return true
		}
	}
	return false
}

// Cursor implements [gunim.CursorShaper].
func (c *previewCard) Cursor(pt geom.Point) input.Cursor {
	if c.card.Contains(pt) {
		return input.CursorHand
	}
	return input.CursorArrow
}
