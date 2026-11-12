package widget

import (
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// RadioGroup tokens.
var (
	// RadioSize is the circle's size, and RadioGap the room between two choices.
	RadioSize = theme.Length("radio.size", 18)
	RadioGap  = theme.Length("radio.gap", 8)
	// RadioPadding is the room inside a choice's card.
	RadioPadding = theme.Insets("radio.padding", geom.Insets{Top: 12, Right: 16, Bottom: 12, Left: 16})
	// RadioTint is how much of the accent fills the chosen card, from 0 to 1, and RadioHover how much of the button's
	// hover fill a card under the pointer takes.
	RadioTint  = theme.Number("radio.tint", 0.12)
	RadioHover = theme.Number("radio.hover", 0.5)
	// RadioDetailScale is the size of a choice's explanation against [TextSize], and RadioDetailGap the room above it.
	RadioDetailScale = theme.Number("radio.detail.scale", 0.93)
	RadioDetailGap   = theme.Length("radio.detail.gap", 2)
	// RadioWidth is the group's width where it is given no bound.
	RadioWidth = theme.Length("radio.width", 360)
)

// RadioItem is one choice of a [RadioGroup].
type RadioItem struct {
	// Label says the choice, and Detail explains it in a line or two under it.
	Label  string
	Detail string
}

// RadioGroup is a column of choices, one of them chosen, each in a card with a line of explanation under its label.
//
// The chosen card's border and fill turn to the accent, and its circle fills with a dot that springs in, while the
// card left fades back. A click on a card chooses it. With focus, Up and Down, or Left and Right, choose the choice
// before or after, and Home and End the first and the last; the focus ring goes round the chosen card. Disabled, it
// fades faint and takes no clicks or keys. Labels and explanations wrap, and the card grows to hold them.
type RadioGroup struct {
	Control
	// OnChange runs on the UI goroutine when the user chooses; a non-nil result is sent to the application as the
	// group's intent.
	OnChange func(i int, u *gunim.UI) gunim.Intent

	items    []RadioItem
	selected int
	// on fills the chosen choice, and off empties the one left, was.
	was     int
	on, off *anim.Float
	// hot is the choice under the pointer, or -1; hotV lights it, and cold is the choice left, fading with coldV.
	hot, cold   int
	hotV, coldV *anim.Float
	// ringTop and ringH place the focus ring, gliding to the chosen card.
	ringTop, ringH *anim.Float
	click          Clicker

	// heights holds each choice's card and the gap after it, and tree their running sums. A choice is measured as it
	// comes into view: gens holds the generation each was measured in, and gen moves on whenever look changes, so a
	// new width or theme costs nothing until a card shows. fresh says the items changed since the last estimate.
	heights []float32
	tree    fenwick
	gens    []uint32
	gen     uint32
	look    radioLook
	fresh   bool
	labels  []laidText
	details []laidText
	// view is the part of the group the last paint showed, and from and to the choices it painted.
	viewTop, viewBottom float32
	size                geom.Size
	from, to            int
	// measures counts the cards measured, for a test.
	measures int
}

// radioLook holds every value a card's measure reads.
type radioLook struct {
	w, size, detailScale, detailGap, gap, radio, ctlGap float32
	pad                                                 geom.Insets
	bold, face                                          *text.Face
}

// NewRadioGroup returns a group of items, the first chosen.
func NewRadioGroup(items ...RadioItem) *RadioGroup {
	g := &RadioGroup{Control: newControl(), was: -1, hot: -1, cold: -1, on: anim.NewFloat(1), off: anim.NewFloat(0),
		hotV: anim.NewFloat(0), coldV: anim.NewFloat(0), ringTop: anim.NewFloat(0), ringH: anim.NewFloat(0),
		gen: 1, viewBottom: 2000}
	g.Add(g.on, g.off, g.hotV, g.coldV, g.ringTop, g.ringH)
	g.SetItems(items, nil)
	return g
}

// Items returns the choices.
func (g *RadioGroup) Items() []RadioItem { return g.items }

// SetItems makes items the choices, keeping the chosen one's place, and sends no intent.
func (g *RadioGroup) SetItems(items []RadioItem, u *gunim.UI) {
	g.items = items
	g.selected = min(max(g.selected, 0), max(len(items)-1, 0))
	g.labels = make([]laidText, len(items))
	g.details = make([]laidText, len(items))
	g.fresh = true
	u.Invalidate()
}

// Selected returns the chosen choice.
func (g *RadioGroup) Selected() int { return g.selected }

// SetSelected chooses choice i and sends no intent. Once laid out, its dot springs in and the old one's fades; before
// that, or with a nil u, it jumps.
func (g *RadioGroup) SetSelected(i int, u *gunim.UI) {
	if i < 0 || i >= len(g.items) || i == g.selected {
		return
	}
	g.was, g.selected = g.selected, i
	if !g.laid || u == nil {
		g.was = -1
		g.on.Jump(1)
		g.off.Jump(0)
		g.placeRing(nil)
		return
	}
	th := u.Theme()
	g.ensure(i)
	g.off.Jump(g.on.Value())
	g.off.Animate(0, Settle.Get(th))
	g.on.Jump(0)
	g.on.Animate(1, Bounce.Get(th))
	g.placeRing(th)
	u.Invalidate()
}

// choose chooses i and tells the application.
func (g *RadioGroup) choose(i int, u *gunim.UI) {
	if i < 0 || i >= len(g.items) || i == g.selected {
		return
	}
	g.SetSelected(i, u)
	act(u, g, gunim.CueSelect, g.OnChange, i)
}

// placeRing moves the focus ring to the chosen card, gliding with th's motion, or at once for a nil th.
func (g *RadioGroup) placeRing(th *theme.Live) {
	top, h := g.span(g.selected)
	if th == nil {
		g.ringTop.Jump(top)
		g.ringH.Jump(h)
		return
	}
	g.ringTop.Animate(top, Quick.Get(th))
	g.ringH.Animate(h, Quick.Get(th))
}

// span returns choice i's top and height, as last measured.
func (g *RadioGroup) span(i int) (top, h float32) {
	if i < 0 || i >= len(g.heights) {
		return 0, 0
	}
	return float32(g.tree.sum(i)), g.heights[i] - g.look.gap
}

// at returns the choice at y, or -1 for none, as in a gap.
func (g *RadioGroup) at(y float32) int {
	if len(g.heights) == 0 || y < 0 {
		return -1
	}
	i := g.tree.find(float64(y))
	if i >= len(g.heights) {
		return -1
	}
	if top, h := g.span(i); y >= top+h {
		return -1
	}
	return i
}

// Handle implements [gunim.Handler].
func (g *RadioGroup) Handle(e input.Event, u *gunim.UI) bool {
	g.showTip(e, u, g)
	th := u.Theme()
	if g.Disabled {
		g.setHot(-1, th)
		return g.handleDisabled(e, u, nil)
	}
	switch e := e.(type) {
	case input.PointerEnter:
		g.setHot(g.at(e.Pos.Y), th)
	case input.PointerMove:
		g.setHot(g.at(e.Pos.Y), th)
	case input.PointerLeave:
		g.setHot(-1, th)
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		g.click.Press(e, g.at(e.Pos.Y))
	case input.PointerUp:
		if i := g.at(e.Pos.Y); g.click.Release(e, i) && e.Pos.X >= 0 && e.Pos.X < g.size.W {
			g.choose(i, u)
		}
	case input.KeyPress:
		if e.Mods.Has(input.ModControl) || e.Mods.Has(input.ModAlt) || e.Mods.Has(input.ModSuper) {
			return false
		}
		switch e.Key {
		case input.KeyUp, input.KeyLeft:
			g.choose(g.selected-1, u)
		case input.KeyDown, input.KeyRight:
			g.choose(g.selected+1, u)
		case input.KeyHome:
			g.choose(0, u)
		case input.KeyEnd:
			g.choose(len(g.items)-1, u)
		default:
			return false
		}
	default:
		if !g.ringFollows(e, th) {
			return false
		}
	}
	u.Invalidate()
	return true
}

// setHot lights choice i under the pointer, or none for -1: it lights quickly, and the one left fades more slowly.
func (g *RadioGroup) setHot(i int, th *theme.Live) {
	if i == g.hot {
		return
	}
	if g.hot >= 0 {
		g.cold = g.hot
		g.coldV.Jump(g.hotV.Value())
		g.coldV.Animate(0, Settle.Get(th))
	}
	g.hot = i
	g.hotV.Jump(0)
	if i >= 0 {
		g.hotV.Animate(1, Quick.Get(th))
	}
}

// room is how wide the label and detail run in a card.
func (l radioLook) room() float32 { return max(0, l.w-l.pad.Left-l.pad.Right-l.radio-l.ctlGap) }

// lookIn gathers the values a card's measure reads, for a group w wide in th.
func lookIn(w float32, th *theme.Live) radioLook {
	return radioLook{w: w, size: TextSize.Get(th), detailScale: RadioDetailScale.Get(th),
		detailGap: RadioDetailGap.Get(th), gap: RadioGap.Get(th), radio: RadioSize.Get(th), ctlGap: ControlGap.Get(th),
		pad: RadioPadding.Get(th), bold: faceIn(BoldFont, th), face: faceIn(Font, th)}
}

// estimate is a card's height and gap before it is measured: one line of label.
func (l radioLook) estimate() float32 {
	return l.pad.Top + max(l.radio, l.size*1.25) + l.pad.Bottom + l.gap
}

// ensure measures choice i where its measure is older than the look.
func (g *RadioGroup) ensure(i int) {
	if i < 0 || i >= len(g.heights) || g.gens[i] == g.gen {
		return
	}
	g.measures++
	l, it := g.look, g.items[i]
	h := g.labels[i].layout(l.bold, it.Label, text.Style{Size: l.size}, l.room()).Size.H
	if it.Detail != "" {
		h += l.detailGap + g.details[i].layout(l.face, it.Detail, text.Style{Size: l.size * l.detailScale}, l.room()).Size.H
	}
	h = l.pad.Top + max(h, l.radio) + l.pad.Bottom + l.gap
	g.tree.add(i, float64(h-g.heights[i]))
	g.heights[i], g.gens[i] = h, g.gen
}

// ensureView measures the choices from top to bottom.
func (g *RadioGroup) ensureView(top, bottom float32) (from, to int) {
	from = min(g.tree.find(float64(max(top, 0))), max(len(g.heights)-1, 0))
	to = from - 1
	for i := from; i < len(g.heights); i++ {
		if t, _ := g.span(i); t >= bottom {
			break
		}
		g.ensure(i)
		to = i
	}
	return from, to
}

// Layout implements [gunim.Node]: as wide as it is given, and as tall as its cards.
func (g *RadioGroup) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	w := c.Max.W
	if w <= 0 {
		w = RadioWidth.Get(th)
	}
	if l := lookIn(w, th); l != g.look || g.fresh {
		g.look = l
		g.gen++
	}
	if g.fresh || len(g.heights) != len(g.items) {
		g.fresh = false
		est := g.look.estimate()
		g.heights = make([]float32, len(g.items))
		for i := range g.heights {
			g.heights[i] = est
		}
		g.gens = make([]uint32, len(g.items))
		g.tree.reset(g.heights)
	}
	g.ensureView(g.viewTop, g.viewBottom)
	g.ensure(g.selected)
	h := float32(0)
	if n := len(g.heights); n > 0 {
		h = float32(g.tree.sum(n)) - g.look.gap
	}
	first := !g.laid
	g.follow(th)
	if first || !g.ringTop.Active() {
		g.placeRing(nil)
	}
	g.size = c.Constrain(geom.Sz(w, h))
	return g.size
}

// Paint implements [gunim.Node]: the cards in view, then the focus ring round the chosen one.
func (g *RadioGroup) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	defer g.faint(p, box)()
	th := f.Theme
	top, bottom := float32(0), box.H
	// The next layout measures all the clip shows, however far the cards grow; with no clip, all of them.
	g.viewTop, g.viewBottom = 0, float32(math.Inf(1))
	if v, ok := p.Visible(); ok {
		top, bottom = max(top, v.Min.Y), min(bottom, v.Max.Y)
		g.viewTop, g.viewBottom = v.Min.Y, v.Max.Y
	}
	g.from, g.to = g.ensureView(top, bottom)
	for i := g.from; i <= g.to; i++ {
		g.paintCard(p, th, i, box.W)
	}
	radius := CardRadius.Get(th)
	g.paintRing(p, geom.Rc(0, g.ringTop.Value(), box.W, g.ringH.Value()), radius, th)
}

// paintCard paints choice i's card, w wide.
func (g *RadioGroup) paintCard(p *paint.Painter, th *theme.Live, i int, w float32) {
	it := g.items[i]
	top, h := g.span(i)
	card := geom.Rc(0, top, w, h)
	radius := CardRadius.Get(th)
	chosen := float32(0)
	switch i {
	case g.selected:
		chosen = g.on.Value()
	case g.was:
		chosen = g.off.Value()
	}
	chosen = min(max(chosen, 0), 1)
	lit := float32(0)
	switch i {
	case g.hot:
		lit = g.hotV.Value()
	case g.cold:
		lit = g.coldV.Value()
	}
	accent := Accent.Get(th)
	fill := paint.Faded(accent, min(max(RadioTint.Get(th), 0), 1)*chosen)
	if lit > 0.01 {
		fill = anim.Mix(anim.ColorCodec, fill, ButtonHover.Get(th), min(lit, 1)*(1-chosen)*RadioHover.Get(th))
	}
	border := anim.Mix(anim.ColorCodec, FieldBorder.Get(th), accent, chosen)
	p.RRectStroke(card.Inset(geom.Uniform(0.5)), radius, paint.Solid(fill), paint.Stroke{Width: 1 + chosen, Color: border})

	l := g.look
	label := g.labels[i].p
	first := label.LineHeight
	circle := geom.Rc(l.pad.Left, top+l.pad.Top+max(0, first-l.radio)/2, l.radio, l.radio)
	ring := anim.Mix(anim.ColorCodec, Placeholder.Get(th), accent, chosen)
	p.RRectStroke(circle.Inset(geom.Uniform(1)), l.radio/2-1, paint.Solid(paint.Faded(accent, chosen)),
		paint.Stroke{Width: 2, Color: ring})
	if chosen > 0.01 {
		dot := l.radio * 0.4 * max(chosen, 0)
		c := circle.Center()
		p.RRect(geom.Rc(c.X-dot/2, c.Y-dot/2, dot, dot), dot/2, paint.Solid(ButtonStrongInk.Get(th)))
	}

	x := circle.Max.X + l.ctlGap
	label.Paint(p, geom.Pt(x, top+l.pad.Top), Ink.Get(th))
	if it.Detail != "" {
		g.details[i].p.Paint(p, geom.Pt(x, top+l.pad.Top+label.Size.H+l.detailGap), Placeholder.Get(th))
	}
}

// Access implements [gunim.Accessible]: a group with a radio button for each choice in view, the chosen one
// checked.
func (g *RadioGroup) Access() access.Info {
	info := access.Info{Role: access.RoleGroup, Name: g.Tooltip, State: g.accessState()}
	// Active counts the parts, which start at the first choice in view.
	if g.selected >= g.from && g.selected <= g.to {
		info.Active = g.selected - g.from + 1
	}
	for i := g.from; i <= g.to && i < len(g.items); i++ {
		it := g.items[i]
		top, h := g.span(i)
		part := access.Info{Role: access.RoleRadioButton, Name: it.Label, Description: it.Detail, Key: uint64(i) + 1,
			State: g.accessState(), Actions: []string{access.ActionPress},
			Bounds: geom.Rc(0, top, g.size.W, h)}
		if i == g.selected {
			part.State |= access.StateChecked
		}
		info.Parts = append(info.Parts, part)
	}
	return info
}

// AccessAct implements [gunim.AccessActor]: pressing a choice chooses it.
func (g *RadioGroup) AccessAct(r access.Request, u *gunim.UI) bool {
	i := g.from + r.Part
	if r.Action != access.ActionPress || r.Part < 0 || i > g.to || i >= len(g.items) || g.Disabled {
		return false
	}
	g.choose(i, u)
	return true
}
