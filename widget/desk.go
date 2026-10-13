package widget

import (
	"image/color"
	"slices"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// Desk and Panel tokens.
var (
	// PanelFill, PanelBorder and PanelShadow are a floating panel's
	// card, its edge and the shadow it casts.
	PanelFill   = theme.Color("panel.fill", color.NRGBA{R: 0x1d, G: 0x20, B: 0x28, A: 0xff})
	PanelBorder = theme.Color("panel.border", color.NRGBA{R: 0x3a, G: 0x40, B: 0x50, A: 0xff})
	PanelShadow = theme.Color("panel.shadow", color.NRGBA{A: 0x80})
	// PanelRadius rounds a panel's corners.
	PanelRadius = theme.Length("panel.radius", 12)
	// PanelTitleHeight is how tall a panel's title bar stands.
	PanelTitleHeight = theme.Length("panel.title.height", 40)
	// PanelGrip is how far in from a panel's edge a press resizes it.
	PanelGrip = theme.Length("panel.grip", 6)
)

// panelMin is the smallest a panel gets where its Min says nothing.
var panelMin = geom.Sz(220, 140)

// Desk is a layer of floating panels over the rest of a window, as a
// plugin's windows float over a music program. Each [Panel] has a
// title bar, a cross that closes it, and content under the bar. A drag
// on the title bar moves a panel, and a drag on its edges or corners
// resizes it. A press anywhere on a panel brings it to the front.
// Panels stay inside the desk: as the desk shrinks, each panel moves
// in, and shrinks where it is bigger than the desk, and grows back to
// the size it was given as the desk grows again.
//
// The desk takes the pointer only over its panels, so what lies under
// it takes the rest. Lay it out over the whole area the panels may use,
// and paint it last.
//
// A press of a second button during a move or a resize drops it, and
// the panel goes back to where the press found it; so does [Desk.Drop].
type Desk struct {
	anim.Group
	// Return is the node the keyboard goes back to when a panel holding
	// it closes. Nil leaves the keyboard nowhere.
	Return gunim.Node
	// panels are the panels open, back to front.
	panels []*Panel
	size   geom.Size
	held   deskHold
}

// deskHold is a move or a resize under way: the panel, the edges a
// resize moves, the panel's place as the press found it, and where the
// press was, in the desk.
type deskHold struct {
	p     *Panel
	edges panelEdge
	from  geom.Rect
	at    geom.Point
}

// panelEdge says which edges of a panel a drag moves.
type panelEdge uint8

const (
	edgeLeft panelEdge = 1 << iota
	edgeTop
	edgeRight
	edgeBottom
	// edgeAll moves the whole panel, as a drag on its title bar does.
	edgeAll = edgeLeft | edgeTop | edgeRight | edgeBottom
)

// NewDesk returns a desk with no panels.
func NewDesk() *Desk { return &Desk{} }

// Panels returns the panels open, back to front.
func (d *Desk) Panels() []*Panel { return slices.Clone(d.panels) }

// Front returns the panel in front, or nil where none is open.
func (d *Desk) Front() *Panel {
	if len(d.panels) == 0 {
		return nil
	}
	return d.panels[len(d.panels)-1]
}

// IsOpen reports whether p is open on the desk.
func (d *Desk) IsOpen(p *Panel) bool { return slices.Contains(d.panels, p) }

// Held reports whether a move or a resize is under way.
func (d *Desk) Held() bool { return d.held.p != nil }

// Open opens p in front of the other panels, springing in, or brings it
// to the front where it is open already. A panel with no place set
// opens at its Min size, centred and stepped from the front panel. A
// nil u, before the desk is mounted, opens it at once.
func (d *Desk) Open(p *Panel, u *gunim.UI) {
	if d.IsOpen(p) {
		d.Raise(p, u)
		return
	}
	if p.rect.Empty() {
		p.rect = d.newPlace(p)
	}
	p.desk = d
	d.panels = append(d.panels, p)
	if u == nil {
		p.in.Jump(1)
		return
	}
	p.in.Animate(1, Bounce.Get(u.Theme()))
	u.InsertAt(d, 1<<30, p)
	u.Cue(gunim.CueOpen, p)
	u.Invalidate()
}

// newPlace is where p opens with no place set: at its smallest, in the
// middle, or stepped down and right of the front panel.
func (d *Desk) newPlace(p *Panel) geom.Rect {
	s := p.min()
	if f := d.Front(); f != nil {
		at := f.rect.Min.Add(geom.Pt(28, 28))
		return geom.Rect{Min: at, Max: at.Add(s.Point())}
	}
	at := geom.Pt(max(d.size.W-s.W, 0)/2, max(d.size.H-s.H, 0)/3)
	return geom.Rect{Min: at, Max: at.Add(s.Point())}
}

// Raise brings p to the front.
func (d *Desk) Raise(p *Panel, u *gunim.UI) {
	i := slices.Index(d.panels, p)
	if i < 0 || i == len(d.panels)-1 {
		return
	}
	d.panels = append(slices.Delete(d.panels, i, i+1), p)
	if u != nil {
		u.InsertAt(d, 1<<30, p)
		u.Invalidate()
	}
}

// Close closes p, which shrinks away. A move or a resize of it under
// way ends where it is, and the keyboard inside it goes to Return. It
// runs no OnClose: that is for the panel's own cross.
func (d *Desk) Close(p *Panel, u *gunim.UI) {
	i := slices.Index(d.panels, p)
	if i < 0 {
		return
	}
	d.panels = slices.Delete(d.panels, i, i+1)
	if d.held.p == p {
		d.held = deskHold{}
	}
	if u == nil {
		return
	}
	if u.HasFocus(p) {
		u.Focus(d.Return)
	}
	u.Remove(p)
	u.Cue(gunim.CueClose, p)
	u.Invalidate()
}

// Drop ends a move or a resize under way, the panel back where the
// press found it.
func (d *Desk) Drop(u *gunim.UI) {
	if d.held.p == nil {
		return
	}
	d.held.p.rect = d.held.from
	d.held = deskHold{}
	if u != nil {
		u.Invalidate()
	}
}

// Children implements [gunim.Composite].
func (d *Desk) Children() []gunim.Node {
	out := make([]gunim.Node, len(d.panels))
	for i, p := range d.panels {
		out[i] = p
	}
	return out
}

// Layout implements [gunim.Node]: the desk fills what it is given, and
// each panel stands where it was put, moved and cut to fit inside.
func (d *Desk) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	d.size = c.Max
	for k := range kids.All {
		p, ok := k.Node().(*Panel)
		if !ok {
			k.Layout(gunim.Tight(geom.Size{}))
			continue
		}
		if d.IsOpen(p) {
			p.shown = fitIn(p.rect, d.size)
		}
		k.Layout(gunim.Tight(p.shown.Size()))
		k.Place(p.shown.Min)
	}
	return d.size
}

// fitIn returns r moved inside a box size big, and cut to it where it
// is bigger.
func fitIn(r geom.Rect, size geom.Size) geom.Rect {
	w, h := min(r.Size().W, size.W), min(r.Size().H, size.H)
	x := min(max(r.Min.X, 0), max(size.W-w, 0))
	y := min(max(r.Min.Y, 0), max(size.H-h, 0))
	return geom.Rc(x, y, max(w, 0), max(h, 0))
}

// Paint implements [gunim.Node]: the panels, back to front.
func (d *Desk) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

// Covers implements [gunim.Shaped]: the desk takes the pointer over its
// panels only.
func (d *Desk) Covers(at geom.Point) bool { return d.panelAt(at) != nil }

// panelAt returns the front panel at at, in the desk, or nil.
func (d *Desk) panelAt(at geom.Point) *Panel {
	for _, p := range slices.Backward(d.panels) {
		if p.shown.Contains(at) {
			return p
		}
	}
	return nil
}

// Overhear implements [gunim.Overhearer]: a press on a panel brings it
// to the front before the press reaches it, and a second button pressed
// during a move or a resize drops it.
func (d *Desk) Overhear(e input.Event, u *gunim.UI) {
	down, ok := e.(input.PointerDown)
	if !ok {
		return
	}
	if d.held.p != nil {
		d.Drop(u)
	}
	at := down.Pos
	if b, ok := u.Bounds(d); ok {
		at = at.Sub(b.Min)
	}
	if p := d.panelAt(at); p != nil {
		d.Raise(p, u)
	}
}

// Handle implements [gunim.Handler]: a press on a panel's title bar
// moves it, and one on its edge resizes it. The desk takes every press
// and wheel over a panel that the panel's content leaves, so they reach
// nothing under it.
func (d *Desk) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerDown:
		p := d.panelAt(e.Pos)
		if p == nil {
			return false
		}
		if e.Button != input.ButtonPrimary {
			return true
		}
		if edges := p.edgesAt(e.Pos.Sub(p.shown.Min), u.Theme()); edges != 0 {
			d.held = deskHold{p: p, edges: edges, from: p.shown, at: e.Pos}
			u.Invalidate()
		}
		return true
	case input.PointerMove:
		h := d.held
		if h.p == nil {
			return false
		}
		h.p.rect = panelDragged(h.from, h.edges, e.Pos.Sub(h.at), h.p.min(), d.size)
		u.Invalidate()
		return true
	case input.PointerUp:
		h := d.held
		if h.p == nil {
			return d.panelAt(e.Pos) != nil
		}
		d.held = deskHold{}
		u.Invalidate()
		if h.p.OnPlace != nil && h.p.rect != h.from {
			if in := h.p.OnPlace(h.p.rect, u); in != nil {
				u.Send(d, in)
			}
		}
		return true
	case input.Scroll:
		return d.panelAt(e.Pos) != nil
	}
	return false
}

// Cursor implements [gunim.CursorShaper]: during a resize, the arrows
// of the edges it moves.
func (d *Desk) Cursor(geom.Point) input.Cursor {
	if d.held.p == nil {
		return input.CursorInherit
	}
	return edgeCursorOf(d.held.edges)
}

// panelDragged returns from, its edges moved by by, kept no smaller than
// least and inside a box size big.
func panelDragged(from geom.Rect, edges panelEdge, by geom.Point, least, size geom.Size) geom.Rect {
	r := from
	if edges == edgeAll {
		w, h := from.Size().W, from.Size().H
		x := min(max(from.Min.X+by.X, 0), max(size.W-w, 0))
		y := min(max(from.Min.Y+by.Y, 0), max(size.H-h, 0))
		return geom.Rc(x, y, w, h)
	}
	if edges&edgeLeft != 0 {
		r.Min.X = min(max(from.Min.X+by.X, 0), from.Max.X-least.W)
	}
	if edges&edgeRight != 0 {
		r.Max.X = max(min(from.Max.X+by.X, size.W), from.Min.X+least.W)
	}
	if edges&edgeTop != 0 {
		r.Min.Y = min(max(from.Min.Y+by.Y, 0), from.Max.Y-least.H)
	}
	if edges&edgeBottom != 0 {
		r.Max.Y = max(min(from.Max.Y+by.Y, size.H), from.Min.Y+least.H)
	}
	return r
}

// edgeCursorOf is the pointer's shape over edges.
func edgeCursorOf(edges panelEdge) input.Cursor {
	switch edges {
	case edgeAll:
		return input.CursorArrow
	case edgeLeft | edgeTop, edgeRight | edgeBottom:
		return input.CursorResizeNWSE
	case edgeRight | edgeTop, edgeLeft | edgeBottom:
		return input.CursorResizeNESW
	case edgeLeft, edgeRight:
		return input.CursorResizeH
	case edgeTop, edgeBottom:
		return input.CursorResizeV
	}
	return input.CursorInherit
}

// Panel is a floating panel on a [Desk]: a title bar with a title, a
// quieter detail after it and a cross, and content under the bar,
// laid out to fill the rest.
type Panel struct {
	anim.Group
	// Title names the panel in its bar, and Detail follows it, quieter.
	Title, Detail string
	// Tint, where it is not clear, shows as a dot before the title.
	Tint color.NRGBA
	// Min is the smallest a resize leaves the panel; zero is 220 by
	// 140.
	Min geom.Size
	// OnClose, when set, runs as the cross closes the panel; a non-nil
	// result is sent to the application.
	OnClose func(u *gunim.UI) gunim.Intent
	// OnPlace, when set, runs as a move or a resize of the panel ends,
	// with its new place on the desk; a non-nil result is sent to the
	// application.
	OnPlace func(r geom.Rect, u *gunim.UI) gunim.Intent

	desk    *Desk
	content gunim.Node
	shut    *IconButton
	// rect is the panel's place as set, and shown where it stands, in
	// the desk.
	rect, shown               geom.Rect
	in                        *anim.Float
	shapedTitle, shapedDetail shapedText
	ell                       shapedText
	// th is the theme the panel was last laid out in.
	th *theme.Live
}

// NewPanel returns a panel showing content under a bar titled title.
func NewPanel(title string, content gunim.Node) *Panel {
	p := &Panel{Title: title, content: content, in: anim.NewFloat(0)}
	p.Add(p.in)
	p.shut = NewIconButton(icon.X, "Close")
	p.shut.OnClick = func(u *gunim.UI) gunim.Intent {
		if p.desk != nil {
			p.desk.Close(p, u)
		}
		if p.OnClose != nil {
			return p.OnClose(u)
		}
		return nil
	}
	return p
}

// Content returns what the panel shows under its bar.
func (p *Panel) Content() gunim.Node { return p.content }

// SetContent shows n under the bar in place of what it shows. A nil u,
// before the panel is mounted, swaps it at once.
func (p *Panel) SetContent(n gunim.Node, u *gunim.UI) {
	if n == p.content {
		return
	}
	if u == nil {
		p.content = n
		return
	}
	if p.content != nil {
		u.Remove(p.content)
	}
	p.content = n
	if n != nil {
		u.Insert(p, n)
	}
	u.Invalidate()
}

// Rect returns the panel's place on its desk as set.
func (p *Panel) Rect() geom.Rect { return p.rect }

// Shown returns where the panel stood on its desk in the last frame:
// its place, moved and cut to fit inside the desk.
func (p *Panel) Shown() geom.Rect { return p.shown }

// SetRect sets the panel's place on its desk. The desk moves it inside,
// and cuts it to fit, as it lays it out.
func (p *Panel) SetRect(r geom.Rect) {
	least := p.min()
	r.Max = geom.Pt(max(r.Max.X, r.Min.X+least.W), max(r.Max.Y, r.Min.Y+least.H))
	p.rect = r
}

// ContentSize returns how big the content stands under the bar at a
// panel size big.
func (p *Panel) ContentSize(size geom.Size, th *theme.Live) geom.Size {
	return geom.Sz(size.W, max(size.H-PanelTitleHeight.Get(th), 0))
}

// min is the smallest the panel gets.
func (p *Panel) min() geom.Size {
	if p.Min.W <= 0 && p.Min.H <= 0 {
		return panelMin
	}
	return geom.Sz(max(p.Min.W, 1), max(p.Min.H, 1))
}

// edgesAt returns the edges a press at at, in the panel, drags: one or
// two near the edges and corners, all of them on the title bar, and
// none elsewhere or on the cross.
func (p *Panel) edgesAt(at geom.Point, th *theme.Live) panelEdge {
	size := p.shown.Size()
	grip := PanelGrip.Get(th)
	corner := 2.5 * grip
	var e panelEdge
	near := func(v, length, band float32) (low, high bool) { return v < band, v >= length-band }
	l, r := near(at.X, size.W, grip)
	t, b := near(at.Y, size.H, grip)
	if l || r {
		// Near a side, the corner's band reaches further along it.
		t, b = near(at.Y, size.H, corner)
	}
	if t || b {
		l, r = near(at.X, size.W, corner)
		if !l && !r {
			l, r = near(at.X, size.W, grip)
		}
	}
	if l {
		e |= edgeLeft
	}
	if r {
		e |= edgeRight
	}
	if t {
		e |= edgeTop
	}
	if b {
		e |= edgeBottom
	}
	if e != 0 {
		return e
	}
	if at.Y < PanelTitleHeight.Get(th) && !p.shutRect(size, th).Contains(at) {
		return edgeAll
	}
	return 0
}

// shutRect is where the cross stands, in a panel size big.
func (p *Panel) shutRect(size geom.Size, th *theme.Live) geom.Rect {
	h := PanelTitleHeight.Get(th)
	s := min(h-8, 30)
	return geom.Rc(size.W-s-(h-s)/2, (h-s)/2, s, s)
}

// Children implements [gunim.Composite].
func (p *Panel) Children() []gunim.Node {
	if p.content == nil {
		return []gunim.Node{p.shut}
	}
	return []gunim.Node{p.shut, p.content}
}

// Layout implements [gunim.Node]: the cross at the bar's right, and the
// content under the bar.
func (p *Panel) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	th := f.Theme
	p.th = th
	h := PanelTitleHeight.Get(th)
	for k := range kids.All {
		switch k.Node() {
		case p.shut:
			r := p.shutRect(size, th)
			k.Layout(gunim.Tight(r.Size()))
			k.Place(r.Min)
		case p.content:
			k.Layout(gunim.Tight(p.ContentSize(size, th)))
			k.Place(geom.Pt(0, h))
		default:
			// Content on its way out, where the content stands.
			k.Layout(gunim.Tight(p.ContentSize(size, th)))
			k.Place(geom.Pt(0, h))
		}
	}
	return size
}

// Transition implements [gunim.Transitioner]: a panel springs in as it
// opens, and shrinks away as it closes.
func (p *Panel) Transition(pr gunim.Presence, f gunim.Frame) bool {
	switch pr {
	case gunim.Entering:
		p.in.Animate(1, Bounce.Get(f.Theme))
	case gunim.Exiting:
		p.in.Animate(0, Quick.Get(f.Theme))
	case gunim.Present:
	}
	return !p.in.Active()
}

// Paint implements [gunim.Node]: a card with a shadow, the title bar
// along its top, and the content clipped to the card.
func (p *Panel) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	t := p.in.Value()
	if t < 0.01 || box.W < 1 || box.H < 1 {
		return
	}
	fade := min(t, 1)
	card := geom.Rect{Max: box.Point()}
	radius := PanelRadius.Get(th)
	defer pt.Layer(paint.LayerOpts{Bounds: card.Inset(geom.Uniform(-40)), Opacity: fade})()
	defer pt.Push(paint.Scale(0.94+0.06*t, card.Center()))()
	shadow := PanelShadow.Get(th)
	shadow.A = uint8(float32(shadow.A) * fade)
	pt.ShadowRRect(card, radius, paint.Solid(PanelFill.Get(th)), paint.Shadow{Offset: geom.Pt(0, 8), Blur: 28, Color: shadow})
	h := PanelTitleHeight.Get(th)
	x := float32(14)
	if p.Tint.A > 0 {
		pt.ShadowRRect(geom.Rc(x, h/2-5, 10, 10), 5, paint.Solid(p.Tint), paint.Shadow{Blur: 8, Color: fadedBy(p.Tint, 0.5)})
		x += 18
	}
	room := p.shutRect(box, th).Min.X - 8 - x
	size := TextSize.Get(th)
	title := fitRun(p.shapedTitle.shape(faceIn(BoldFont, th), p.Title, size), &p.ell, room)
	title.Paint(pt, geom.Pt(x, (h-size)/2-1), Ink.Get(th))
	if p.Detail != "" && room-title.Advance > 40 {
		ds := size * 0.8
		detail := fitRun(p.shapedDetail.shape(faceIn(Font, th), p.Detail, ds), &p.ell, room-title.Advance-10)
		detail.Paint(pt, geom.Pt(x+title.Advance+10, (h-ds)/2), fadedBy(Ink.Get(th), 0.5))
	}
	pt.RRect(geom.Rc(0, h-0.5, box.W, 1), 0, paint.Solid(fadedBy(PanelBorder.Get(th), 0.6)))
	for k := range kids.All {
		if k.Node() == p.shut {
			k.Paint(pt)
			continue
		}
		func() {
			// Inside the card's round corners, and under the bar.
			defer pt.Layer(paint.LayerOpts{Bounds: card, Opacity: 1, Clip: true, Radius: radius})()
			defer pt.Layer(paint.LayerOpts{Bounds: geom.Rect{Min: geom.Pt(0, h), Max: box.Point()}, Opacity: 1, Clip: true})()
			k.Paint(pt)
		}()
	}
	pt.RRectStroke(card.Inset(geom.Uniform(0.5)), radius, paint.Fill{}, paint.Stroke{Width: 1, Color: PanelBorder.Get(th)})
}

// ClaimsPointer implements [gunim.PointerClaimer]: the panel takes the
// pointer on its edges and its title bar, ahead of its content, for
// its desk to move and resize it.
func (p *Panel) ClaimsPointer(at geom.Point) bool {
	return p.desk != nil && p.edgesAt(at, p.th) != 0
}

// Cursor implements [gunim.CursorShaper]: resize arrows near the edges.
func (p *Panel) Cursor(at geom.Point) input.Cursor {
	if e := p.edgesAt(at, p.th); e != 0 && e != edgeAll {
		return edgeCursorOf(e)
	}
	return input.CursorInherit
}
