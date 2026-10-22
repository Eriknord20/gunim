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

// catchUp is the pill over the foot of the timeline that says how many new messages lie below the view, and takes
// the view down to them on a click. It shows while there are some and the view is not at the end, and they count as
// read once the view gets there.
type catchUp struct {
	anim.Group
	// list is the timeline the pill counts for, which a new conversation replaces.
	list  *widget.VirtualList
	count int
	in    *anim.Float
	hover *anim.Float
	run   text.Run
}

func newCatchUp() *catchUp {
	c := &catchUp{in: anim.NewFloat(0), hover: anim.NewFloat(0)}
	c.Add(c.in, c.hover)
	return c
}

// set makes n the number of new messages below list.
func (c *catchUp) set(list *widget.VirtualList, n int, u *gunim.UI) {
	c.list, c.count = list, n
	u.Invalidate()
}

// Layout implements [gunim.Node]. The view at the end has seen what was new.
func (c *catchUp) Layout(_ gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	if c.count > 0 && c.list != nil && c.list.AtEnd() {
		c.count = 0
	}
	c.in.Animate(map[bool]float32{false: 0, true: 1}[c.count > 0], widget.Quick.Get(th))
	label := strconv.Itoa(c.count) + " new messages"
	if c.count == 1 {
		label = "1 new message"
	}
	if c.count > 0 {
		c.run = widget.BoldFont.Get(th).Shape(label, SmallText.Get(th))
	}
	return geom.Sz(14+16+6+c.run.Advance+14, 30)
}

// Paint implements [gunim.Node]: it grows in from its middle as it fades in.
func (c *catchUp) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	t := min(max(c.in.Value(), 0), 1)
	if t < 0.01 {
		return
	}
	th := f.Theme
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Min: geom.Pt(-8, -8), Max: box.Point().Add(geom.Pt(8, 8))}, Opacity: t})()
	defer p.Push(paint.Scale(0.85+0.15*t, geom.Pt(box.W/2, box.H/2)))()
	fill := widget.Accent.Get(th)
	if h := min(c.hover.Value(), 1); h > 0 {
		fill = anim.Mix(anim.ColorCodec, fill, widget.ButtonStrongInk.Get(th), 0.15*h)
	}
	r := geom.Rect{Max: box.Point()}
	p.ShadowRRect(r, box.H/2, paint.Solid(fill), paint.Shadow{Offset: geom.Pt(0, 2), Blur: 6, Color: fade(fill, 0.4)})
	white := widget.ButtonStrongInk.Get(th)
	widget.PaintIcon(p, th, icon.ArrowDown, geom.Rc(14, (box.H-16)/2, 16, 16), white)
	c.run.Paint(p, geom.Pt(14+16+6, (box.H-c.run.Height())/2), white)
}

// Handle implements [gunim.Handler]: a click takes the view to the end.
func (c *catchUp) Handle(e input.Event, u *gunim.UI) bool {
	if c.count == 0 {
		return false
	}
	switch e := e.(type) {
	case input.PointerEnter:
		c.hover.Animate(1, widget.Quick.Get(u.Theme()))
	case input.PointerLeave:
		c.hover.Animate(0, widget.Settle.Get(u.Theme()))
	case input.PointerDown:
		if e.Button == input.ButtonPrimary && c.list != nil {
			c.list.ScrollToEnd(widget.Settle.Get(u.Theme()))
			return true
		}
	}
	return false
}

// Cursor implements [gunim.CursorShaper].
func (c *catchUp) Cursor(geom.Point) input.Cursor { return input.CursorHand }

// timelineBox is the timeline with the catch up pill over its foot.
type timelineBox struct {
	list    gunim.Node
	pill    *catchUp
	loading *loadingPill
}

// Children implements [gunim.Composite].
func (b *timelineBox) Children() []gunim.Node { return []gunim.Node{b.list, b.pill, b.loading} }

// Layout implements [gunim.Node].
func (b *timelineBox) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	list, pill, loading := kids.At(0), kids.At(1), kids.At(2)
	list.Layout(gunim.Tight(c.Max))
	list.Place(geom.Point{})
	ps := pill.Layout(gunim.Loose(c.Max))
	pill.Place(geom.Pt((c.Max.W-ps.W)/2, c.Max.H-ps.H-12))
	ls := loading.Layout(gunim.Loose(c.Max))
	loading.Place(geom.Pt((c.Max.W-ls.W)/2, 10))
	return c.Max
}

// Paint implements [gunim.Node].
func (b *timelineBox) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for kid := range kids.All {
		kid.Paint(p)
	}
}

// loadingPill says, at the top of the timeline, that older messages are on their way, fading in and out.
type loadingPill struct {
	anim.Group
	in   *anim.Float
	text text.Run
}

func newLoadingPill() *loadingPill {
	l := &loadingPill{in: anim.NewFloat(0)}
	l.Add(l.in)
	return l
}

// set shows the pill while loading.
func (l *loadingPill) set(loading bool, u *gunim.UI) {
	l.in.Animate(map[bool]float32{false: 0, true: 1}[loading], widget.Quick.Get(u.Theme()))
	u.Invalidate()
}

// Layout implements [gunim.Node].
func (l *loadingPill) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	l.text = widget.Font.Get(f.Theme).Shape("Loading older messages…", SmallText.Get(f.Theme))
	return geom.Sz(l.text.Advance+28, 28)
}

// Paint implements [gunim.Node].
func (l *loadingPill) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	t := min(max(l.in.Value(), 0), 1)
	if t <= 0.01 {
		return
	}
	th := f.Theme
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}.Inset(geom.Uniform(-12)), Opacity: t})()
	defer p.Push(paint.Translate(geom.Pt(0, -8*(1-t))))()
	r := geom.Rect{Max: box.Point()}
	p.ShadowRRect(r, box.H/2, paint.Solid(widget.MenuFill.Get(th)),
		paint.Shadow{Color: widget.MenuShadow.Get(th), Blur: 10, Offset: geom.Pt(0, 2)})
	l.text.Paint(p, geom.Pt(14, (box.H-l.text.Height())/2), Faint.Get(th))
}
