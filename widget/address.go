package widget

import (
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// Theme tokens for an [AddressBar].
var (
	// AddressHeight is the bar's height.
	AddressHeight = theme.Length("address.height", 34)
	// AddressCrumbInk colours the places before the last, and AddressChevron the chevrons between them.
	AddressCrumbInk = theme.Foreground("address.crumb", color.NRGBA{R: 0x8a, G: 0x93, B: 0xa6, A: 0xff})
	AddressChevron  = theme.Foreground("address.chevron", color.NRGBA{R: 0x6b, G: 0x72, B: 0x80, A: 0xff})
)

// An AddressLead names where an [AddressBar]'s path is, before its places: the machine a file manager shows, say.
// It stays at the bar's start while the places scroll.
type AddressLead struct {
	Name    string
	Icon    *icon.Icon
	Tooltip string
}

// A Crumb is one place along an [AddressBar]'s path: the name it shows and the path it goes to.
type Crumb struct {
	Name, Path string
}

// AddressBar shows a path as the places along it, as a file manager's address bar does. A click on a place goes
// there, and a click beside them, or [AddressBar.Edit], turns the bar into a field holding the path. Tab visits the
// places as one stop, and the arrow keys, Home and End move between them.
type AddressBar struct {
	anim.Group
	// OnGo runs on the UI goroutine with the path of a place picked, or of the path typed in and entered; a non-nil
	// result is sent as the bar's intent.
	OnGo func(path string, u *gunim.UI) gunim.Intent
	// OnDone runs as the field gives the bar back to the places from the keyboard, with Enter or Escape, such as to
	// hand the keyboard to what the bar shows. A non-nil result is sent as the bar's intent.
	OnDone func(u *gunim.UI) gunim.Intent
	// OnLead runs on the UI goroutine as the lead is clicked; a non-nil result is sent as the bar's intent.
	OnLead func(u *gunim.UI) gunim.Intent

	crumbs *crumbBar
	themed gunim.Node
	field  *addressField
	path   string
	mix    *anim.Float
}

// NewAddressBar returns an empty address bar.
func NewAddressBar() *AddressBar {
	a := &AddressBar{mix: anim.NewFloat(0)}
	a.Add(a.mix)
	a.crumbs = &crumbBar{a: a, ring: anim.NewFloat(0), shift: anim.NewFloat(0), lead: NewButton("")}
	a.crumbs.Add(a.crumbs.ring, a.crumbs.shift)
	lead := a.crumbs.lead
	lead.Ghost, lead.KeepFocus, lead.Ink, lead.Disabled = true, true, Accent, true
	lead.OnClick = func(u *gunim.UI) gunim.Intent {
		if a.OnLead != nil {
			send(u, a, a.OnLead(u))
		}
		return nil
	}
	a.themed = NewThemed(a.crumbs, crumbTheme)
	a.field = &addressField{TextField: NewTextField(), a: a}
	return a
}

// SetPath shows the path full as the places along it, cs. The places both paths share stay, and the rest leave and
// arrive.
func (a *AddressBar) SetPath(full string, cs []Crumb, u *gunim.UI) {
	a.path = full
	a.crumbs.set(cs, u)
}

// SetLead shows l before the places, or nothing there when its Name is empty.
func (a *AddressBar) SetLead(l AddressLead, u *gunim.UI) {
	b := a.crumbs.lead
	if b.Label == l.Name && b.Icon == l.Icon && b.Tooltip == l.Tooltip {
		return
	}
	b.Label, b.Icon, b.Tooltip, b.Disabled = l.Name, l.Icon, l.Tooltip, l.Name == ""
	if u != nil {
		u.Invalidate()
	}
}

// Edit turns the places into a field holding the path, all selected, with the keyboard.
func (a *AddressBar) Edit(u *gunim.UI) {
	a.field.SetText(a.path, u)
	a.field.Select(0, len([]rune(a.path)))
	a.show(true, u)
	u.Focus(a.field)
}

// Text returns what the field holds, while the bar edits the path.
func (a *AddressBar) Text() string { return a.field.Text() }

// An AddressPlace is one place an [AddressBar] shows, as [AddressBar.Places] reports it.
type AddressPlace struct {
	Name, Path string
	// Last says it is the path's end, the place showing.
	Last bool
	// Rect is where its button is, in the window's space, and Focused says the button has the keyboard.
	Rect    geom.Rect
	Focused bool
}

// Places returns the places the bar shows, first to last, such as to find the one under a drop.
func (a *AddressBar) Places(u *gunim.UI) []AddressPlace {
	out := make([]AddressPlace, 0, len(a.crumbs.crumbs))
	for _, c := range a.crumbs.crumbs {
		pl := AddressPlace{Name: c.btn.Label, Path: c.path, Last: c.last, Focused: u.Focused() == gunim.Node(c.btn)}
		if r, ok := u.Bounds(c); ok {
			pl.Rect = geom.Rc(r.Min.X, r.Min.Y, c.pill, r.Size().H)
		}
		out = append(out, pl)
	}
	return out
}

// Editing reports whether the bar shows the field.
func (a *AddressBar) Editing() bool { return a.mix.Target() > 0.5 }

// show crossfades to the field, or back to the places.
func (a *AddressBar) show(editing bool, u *gunim.UI) {
	a.mix.Animate(map[bool]float32{false: 0, true: 1}[editing], Quick.Get(u.Theme()))
	u.Invalidate()
}

// stopEdit turns the field back into the places, and runs OnDone when the keyboard asked.
func (a *AddressBar) stopEdit(u *gunim.UI, done bool) {
	a.show(false, u)
	if done && a.OnDone != nil {
		send(u, a, a.OnDone(u))
	}
}

// Children implements [gunim.Composite].
func (a *AddressBar) Children() []gunim.Node {
	return []gunim.Node{a.themed, a.field}
}

// Layout implements [gunim.Node].
func (a *AddressBar) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Constrain(geom.Sz(c.Max.W, AddressHeight.Get(f.Theme)))
	for k := range kids.All {
		k.Layout(gunim.Tight(size))
		k.Place(geom.Point{})
	}
	return size
}

// Paint implements [gunim.Node]. Only the side showing is painted at rest, so only it takes the pointer.
func (a *AddressBar) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	t := min(max(a.mix.Value(), 0), 1)
	r := geom.Rect{Max: box.Point()}
	if t < 0.999 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: 1 - t})()
			kids.At(0).Paint(p)
		}()
	}
	if t > 0.001 {
		defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-4)), Opacity: t})()
		kids.At(1).Paint(p)
	}
}

// addressField is the field that edits the path: Enter goes there, and Escape or a click elsewhere turns it back
// into the places.
type addressField struct {
	*TextField
	a *AddressBar
}

// Handle implements [gunim.Handler].
func (f *addressField) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.KeyPress:
		switch e.Key {
		case input.KeyEscape:
			f.a.stopEdit(u, true)
			return true
		case input.KeyEnter, input.KeyKPEnter:
			if f.a.OnGo != nil {
				send(u, f.a, f.a.OnGo(f.Text(), u))
			}
			f.a.stopEdit(u, true)
			return true
		default:
		}
	case input.FocusLost:
		f.TextField.Handle(e, u)
		f.a.show(false, u)
		return true
	}
	return f.TextField.Handle(e, u)
}

// crumbBar shows the lead and the places along the path, with the last one at the right edge when they do not all
// fit. The places slide along to bring the one with the keyboard into view, and with the wheel, and fade where more
// lie past an edge. A name too long for the bar ends in an ellipsis.
type crumbBar struct {
	anim.Group
	a      *AddressBar
	lead   *Button
	crumbs []*crumb
	// from is where the places start, past the lead, at the last layout.
	from float32
	ring *anim.Float
	// shift is how far right of the last one at the right edge the places sit, to show the one with the keyboard;
	// most is how far they can go, and size the bar's size, at the last layout.
	shift *anim.Float
	most  float32
	size  geom.Size
	click Clicker
}

func (c *crumbBar) set(cs []Crumb, u *gunim.UI) {
	keep := 0
	for keep < len(cs) && keep < len(c.crumbs) && c.crumbs[keep].path == cs[keep].Path {
		keep++
	}
	// With a nil u the bar is still to be mounted: it mounts the places it holds then, so they are only kept.
	for _, old := range c.crumbs[keep:] {
		if u != nil {
			u.Remove(old)
		}
	}
	c.crumbs = c.crumbs[:keep]
	for i := keep; i < len(cs); i++ {
		n := newCrumb(cs[i], c.a)
		c.crumbs = append(c.crumbs, n)
		if u != nil {
			u.Insert(c, n)
		}
	}
	for i, n := range c.crumbs {
		n.setLast(i == len(c.crumbs)-1)
	}
	if u == nil {
		c.shift.Jump(0)
		return
	}
	c.shift.Animate(0, Settle.Get(u.Theme()))
	u.Invalidate()
}

// Children implements [gunim.Composite]: the lead, and the places set before the bar was mounted, which arrive with
// it.
func (c *crumbBar) Children() []gunim.Node {
	out := make([]gunim.Node, 0, len(c.crumbs)+1)
	out = append(out, c.lead)
	for _, n := range c.crumbs {
		out = append(out, n)
	}
	return out
}

// leadGap is the room between the lead and the places, where a line parts them.
const leadGap = 9

// crumbEdge is the room the places keep from either end of the bar.
const crumbEdge = 8

// Reveal implements [gunim.Revealer]: the places slide to bring r, a place given the keyboard, into view.
func (c *crumbBar) Reveal(r geom.Rect, u *gunim.UI) {
	to := c.shift.Value()
	switch {
	case r.Min.X < c.from+crumbEdge:
		to += c.from + crumbEdge - r.Min.X
	case r.Max.X > c.size.W-crumbEdge:
		to -= r.Max.X - (c.size.W - crumbEdge)
	default:
		return
	}
	c.shift.Animate(min(max(to, 0), c.most), Settle.Get(u.Theme()))
	u.Invalidate()
}

// TabGroup implements [gunim.TabGroup]: the places are one stop for Tab.
func (c *crumbBar) TabGroup() {}

// Layout implements [gunim.Node].
func (c *crumbBar) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := cs.Max
	c.size = size
	// The lead takes up to a third of the bar, and the places start past it.
	c.from = 0
	if lead := kids.At(0); c.lead.Label == "" {
		lead.Layout(gunim.Tight(geom.Size{}))
		lead.Place(geom.Point{})
	} else {
		s := lead.Layout(gunim.Loose(geom.Sz(max(size.W/3, 1), size.H)))
		lead.Place(geom.Pt(crumbEdge/2, (size.H-s.H)/2))
		c.from = crumbEdge/2 + s.W + leadGap
	}
	// Each place fits in the bar, its name cut short where it is longer.
	room := gunim.Loose(geom.Sz(max(size.W-c.from-2*crumbEdge, 1), size.H))
	total := float32(0)
	for k := range kids.All {
		if k.Node() == gunim.Node(c.lead) || k.Presence() == gunim.Exiting {
			continue
		}
		total += k.Layout(room).W
	}
	// When the places overflow, the last ones show, unless the keyboard or
	// the wheel has slid them along to show an earlier one.
	x := c.from + min(float32(crumbEdge), size.W-c.from-total-crumbEdge)
	c.most = c.from + crumbEdge - x
	x += min(max(c.shift.Value(), 0), c.most)
	for k := range kids.All {
		if k.Node() == gunim.Node(c.lead) {
			continue
		}
		s := k.Layout(room)
		cr, _ := k.Node().(*crumb)
		if k.Presence() == gunim.Exiting {
			if cr != nil {
				k.Place(geom.Pt(cr.x, (size.H-s.H)/2))
			}
			continue
		}
		if cr != nil {
			cr.x = x
		}
		k.Place(geom.Pt(x, (size.H-s.H)/2))
		x += s.W
	}
	return size
}

// Paint implements [gunim.Node].
func (c *crumbBar) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	r := geom.Rect{Max: box.Point()}
	radius := FieldRadius.Get(th)
	p.RRect(r, radius, paint.Solid(FieldFill.Get(th)))
	if c.lead.Label != "" {
		kids.At(0).Paint(p)
		x := c.from - leadGap/2
		p.RRect(geom.Rc(x, box.H/4, 1, box.H/2), 0, paint.Solid(FieldBorder.Get(th)))
	}
	func() {
		// The places fade where more of them lie past an edge.
		places := geom.Rc(c.from, 0, box.W-c.from, box.H)
		at, fade := min(max(c.shift.Value(), 0), c.most), ScrollFade.Get(th)
		defer p.Layer(paint.LayerOpts{Bounds: places, Opacity: 1, Clip: true, Radius: radius,
			Fade: geom.Insets{Left: fadeFor(c.most-at, fade), Right: fadeFor(at, fade)}})()
		for k := range kids.All {
			if k.Node() != gunim.Node(c.lead) {
				k.Paint(p)
			}
		}
	}()
	GroupRing(p, r, radius, c.ring.Value(), th)
}

// Handle implements [gunim.Handler]: the ring follows the keyboard, a press beside the places edits the path, and
// the arrow keys, Home and End move between the places.
func (c *crumbBar) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.FocusRing:
		c.ring.Animate(ringTo(e), RingFade.Get(u.Theme()))
		return true
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		c.click.Press(e, over(e.Pos, c.size))
		return true
	case input.PointerUp:
		// A click beside the places, pressed and let go there, edits the path.
		if c.click.Release(e, over(e.Pos, c.size)) {
			c.a.Edit(u)
		}
		return e.Button == input.ButtonPrimary
	case input.Scroll:
		// A wheel turns either way along the places, which overflow; a
		// finger drags them.
		d := e.Delta.X
		if d == 0 {
			d = e.Delta.Y
		}
		to := min(max(c.shift.Target()+d, 0), c.most)
		if c.most <= 0 || to == c.shift.Target() {
			return false
		}
		c.shift.Animate(to, Quick.Get(u.Theme()))
		u.Invalidate()
		return true
	case input.KeyPress:
		if e.Mods != 0 {
			return false
		}
		switch e.Key {
		case input.KeyLeft, input.KeyRight:
			u.FocusWithin(c, e.Key == input.KeyRight)
			return true
		case input.KeyHome, input.KeyEnd:
			for u.FocusWithin(c, e.Key == input.KeyEnd) {
			}
			return true
		default:
		}
	}
	return false
}

// Cursor implements [gunim.CursorShaper].
func (c *crumbBar) Cursor(geom.Point) input.Cursor { return input.CursorText }

// Access implements [gunim.Accessible]: the places along the path.
func (c *crumbBar) Access() access.Info { return access.Info{Role: access.RoleGroup, Name: "Path"} }

// crumbTheme sizes the places' buttons to fit the bar.
var crumbTheme = theme.Make("address.crumbs",
	theme.Set(ButtonHeight, 26),
	theme.Set(ButtonPadding, 6),
	theme.Set(ButtonRadius, 6),
)

// crumb is one place along the path: a button that goes there, and a chevron after all but the last. The last one's
// button edits the path.
type crumb struct {
	anim.Group
	path string
	last bool
	btn  *Button
	// x is where the crumb was placed, and pill its button's width, at the last layout.
	x, pill float32
	in      *anim.Float
	sep     text.Run
	size    float32
	a       *AddressBar
}

func newCrumb(c Crumb, a *AddressBar) *crumb {
	n := &crumb{path: c.Path, btn: NewButton(c.Name), in: anim.NewFloat(0), a: a}
	n.Add(n.in)
	n.btn.Ghost, n.btn.KeepFocus = true, true
	n.btn.OnClick = func(u *gunim.UI) gunim.Intent {
		switch {
		case n.last:
			a.Edit(u)
		case a.OnGo != nil:
			send(u, a, a.OnGo(n.path, u))
		}
		return nil
	}
	return n
}

// setLast makes the crumb the last, the place showing, or one before it.
func (n *crumb) setLast(last bool) {
	n.last = last
	n.btn.Ink = Ink
	if !last {
		n.btn.Ink = AddressCrumbInk
	}
}

// Children implements [gunim.Composite].
func (n *crumb) Children() []gunim.Node { return []gunim.Node{n.btn} }

// Transition implements [gunim.Transitioner]: a crumb slides in from the left and fades up, and fades as it leaves.
func (n *crumb) Transition(p gunim.Presence, f gunim.Frame) bool {
	switch p {
	case gunim.Entering:
		n.in.Animate(1, Bounce.Get(f.Theme))
	case gunim.Exiting:
		n.in.Animate(0, Quick.Get(f.Theme))
	case gunim.Present:
	}
	return !n.in.Active()
}

// Layout implements [gunim.Node].
func (n *crumb) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	if size := TextSize.Get(f.Theme); n.size != size || len(n.sep.Glyphs) == 0 {
		n.size, n.sep = size, text.Default().Shape("›", size)
	}
	kid := kids.At(0)
	sep := float32(0)
	if !n.last {
		sep = n.sep.Advance + 8
	}
	room := c.Max
	if room.W > 0 {
		room.W = max(room.W-sep, 1)
	}
	s := kid.Layout(gunim.Loose(room))
	kid.Place(geom.Point{})
	n.pill = s.W
	w := s.W + sep
	return c.Constrain(geom.Sz(w, s.H))
}

// Paint implements [gunim.Node].
func (n *crumb) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	t := min(max(n.in.Value(), 0), 1)
	if t <= 0.001 {
		return
	}
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}.Inset(geom.Uniform(-4)), Opacity: t})()
	defer p.Push(paint.Translate(geom.Pt(-10*(1-t), 0)))()
	kids.At(0).Paint(p)
	if !n.last {
		n.sep.Paint(p, geom.Pt(n.pill+4, (box.H-n.sep.Height())/2), AddressChevron.Get(f.Theme))
	}
}

// Cursor implements [gunim.CursorShaper].
func (n *crumb) Cursor(p geom.Point) input.Cursor {
	if p.X > n.pill || n.last {
		return input.CursorText
	}
	return input.CursorHand
}
