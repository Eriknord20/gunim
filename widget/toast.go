package widget

import (
	"image/color"
	"slices"
	"strconv"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// Toast tokens.
var (
	ToastWidth = theme.Length("toast.width", 340)
	ToastGap   = theme.Length("toast.gap", 10)
	// ToastMoreHeight is the height of the card that stands for the toasts the stack has no room for.
	ToastMoreHeight = theme.Length("toast.more.height", 30)
	// ToastInfoInk, ToastSuccessInk, ToastWarningInk and ToastErrorInk colour the icon of each kind of toast.
	ToastInfoInk    = theme.Color("toast.info", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0xff})
	ToastSuccessInk = theme.Color("toast.success", color.NRGBA{R: 0x4c, G: 0xc3, B: 0x8a, A: 0xff})
	ToastWarningInk = theme.Color("toast.warning", color.NRGBA{R: 0xe8, G: 0xb3, B: 0x4a, A: 0xff})
	ToastErrorInk   = theme.Color("toast.error", color.NRGBA{R: 0xff, G: 0x6b, B: 0x66, A: 0xff})
)

// toastLife is how long a toast stays when the pointer leaves it alone.
const toastLife = 5 * time.Second

// toastDrawOn is how long a toast's icon takes to draw itself on.
const toastDrawOn = 600 * time.Millisecond

// toastBodyLines is how many lines of its body a toast shows. The pointer resting on it shows the whole body.
const toastBodyLines = 3

// Toast is a short notice: a title, and a line or two under it. A body
// longer than three lines ends in an ellipsis, and shows whole while the
// pointer rests on the toast.
//
// An error toast stays until its close button is clicked, and its text
// can be selected and copied, so it can be read in full and reported.
type Toast struct {
	Title string
	Body  string
	// Action names a link on the toast, such as Undo, and OnClick runs on
	// the UI goroutine when it is clicked; a non-nil result is sent as the
	// toast's intent. The toast goes once it is clicked.
	Action  string
	OnClick func(u *gunim.UI) gunim.Intent
	// Kind picks the icon before the title and its colour. The plain kind shows none.
	Kind ToastKind
	// Icon, when set, replaces the kind's icon. A plain toast shows it in the ink.
	Icon *icon.Icon
	// Key names the toast, so [Toasts.Close] can take it away. A toast
	// shown with the key of one showing takes its place.
	Key string
	// Buttons are buttons under the text, the first the one the toast
	// expects. A toast with buttons asks: it stays until one of them,
	// or its close button, is clicked, and a click elsewhere on it does
	// nothing. Neither it nor its buttons take the keyboard as they are
	// shown or clicked; Tab still reaches them.
	Buttons []ToastButton
	// Check, when not empty, labels a tick box above the buttons. The
	// buttons' OnClick and OnDismiss hear whether it is ticked.
	Check string
	// OnDismiss runs on the UI goroutine when the close button of a toast
	// that asks is clicked, told whether the tick box is ticked; a non-nil
	// result is sent as the toast's intent.
	OnDismiss func(checked bool, u *gunim.UI) gunim.Intent
}

// ToastButton is a button of a [Toast] that asks.
type ToastButton struct {
	Label string
	// OnClick runs on the UI goroutine when the button is clicked, told
	// whether the toast's tick box is ticked; a non-nil result is sent as
	// the toast's intent. The toast goes once it is clicked.
	OnClick func(checked bool, u *gunim.UI) gunim.Intent
}

// ToastKind says what a [Toast] reports, and picks its icon and colour.
type ToastKind uint8

// The kinds of toast.
const (
	// ToastPlain shows no icon.
	ToastPlain ToastKind = iota
	// ToastInfo shows icon.Info in [ToastInfoInk].
	ToastInfo
	// ToastSuccess shows icon.CircleCheck in [ToastSuccessInk].
	ToastSuccess
	// ToastWarning shows icon.TriangleAlert in [ToastWarningInk].
	ToastWarning
	// ToastError shows icon.CircleAlert in [ToastErrorInk]. It stays
	// until its close button is clicked.
	ToastError
)

// look returns the kind's icon and colour, or nil for the plain kind.
func (k ToastKind) look() (*icon.Icon, theme.Token[color.NRGBA]) {
	switch k {
	case ToastInfo:
		return icon.Info, ToastInfoInk
	case ToastSuccess:
		return icon.CircleCheck, ToastSuccessInk
	case ToastWarning:
		return icon.TriangleAlert, ToastWarningInk
	case ToastError:
		return icon.CircleAlert, ToastErrorInk
	case ToastPlain:
	}
	return nil, Ink
}

// name is what a screen reader calls the kind's icon, so an error reads as one.
func (k ToastKind) name() string {
	switch k {
	case ToastInfo:
		return "Information"
	case ToastSuccess:
		return "Success"
	case ToastWarning:
		return "Warning"
	case ToastError:
		return "Error"
	case ToastPlain:
	}
	return ""
}

// Toasts shows short notices in a stack, the newest nearest the corner
// it sits in. Each slides in from the side and fades up, stays a few
// seconds, and slides away; the pointer resting on one keeps it, and a
// click dismisses it. As one leaves, the rest glide together.
//
// It takes the size of its stack, so the window behind it keeps its
// clicks: place it in a corner, as the last child of a window's root.
//
// The stack keeps within the height it is given. Toasts with no room
// fade out, and a card over the stack counts them, as "+3 more": a
// click on it shows the next older toasts in their place, and after the
// oldest, the newest again. A new toast brings the newest back.
type Toasts struct {
	// Life is how long a toast stays; zero takes five seconds.
	Life time.Duration

	cards []*toastCard
	// more is the card that counts the toasts with no room, and skip how
	// many of the newest a click on it has paged past.
	more *toastMore
	skip int
}

// NewToasts returns an empty stack of toasts, each staying five seconds.
func NewToasts() *Toasts { return &Toasts{} }

// Show adds a toast to the stack. One with the key of a toast showing
// takes its place. A toast that asks stays until it is answered, and an
// error until it is closed.
func (t *Toasts) Show(to Toast, u *gunim.UI) {
	if to.Key != "" {
		// The new toast takes the old one's place, and sounds once, as it arrives.
		for _, c := range slices.Clone(t.cards) {
			if c.key == to.Key {
				t.take(c, u)
			}
		}
	}
	if t.more == nil {
		t.more = newToastMore(t)
		u.Insert(t, t.more)
	}
	c := newToastCard(t, to)
	t.cards = append(t.cards, c)
	t.skip = 0
	u.Insert(t, c)
	if !c.stays() {
		t.expire(c, u)
	}
	u.Cue(gunim.CueOpen, t)
	u.Invalidate()
}

// Close takes away the toast showing with key, if one is, as though its
// time were up: it sends nothing.
func (t *Toasts) Close(key string, u *gunim.UI) {
	if key == "" {
		return
	}
	for _, c := range slices.Clone(t.cards) {
		if c.key == key {
			t.dismiss(c, u)
		}
	}
}

// Len returns how many toasts are showing.
func (t *Toasts) Len() int { return len(t.cards) }

// expire takes c away once its time is up, or later, while the pointer
// rests on it.
func (t *Toasts) expire(c *toastCard, u *gunim.UI) {
	life := t.Life
	if life <= 0 {
		life = toastLife
	}
	u.After(life, func(u *gunim.UI) {
		if c.hovered {
			t.expire(c, u)
			return
		}
		t.dismiss(c, u)
	})
}

// dismiss takes c away with the close cue.
func (t *Toasts) dismiss(c *toastCard, u *gunim.UI) {
	if t.take(c, u) {
		u.Cue(gunim.CueClose, t)
	}
}

// take takes c away, and reports whether it was showing.
func (t *Toasts) take(c *toastCard, u *gunim.UI) bool {
	i := slices.Index(t.cards, c)
	if i < 0 {
		return false
	}
	t.cards = slices.Delete(t.cards, i, i+1)
	u.Remove(c)
	u.Invalidate()
	return true
}

// Layout implements [gunim.Node]. The newest toast sits at the bottom,
// the rest above it; a toast on its way out keeps its place. Toasts past
// the height given fade out under the card that counts them.
func (t *Toasts) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	th := f.Theme
	w := min(ToastWidth.Get(th), max(c.Max.W, 1))
	gap := ToastGap.Get(th)
	sizes := map[gunim.Node]geom.Size{}
	for k := range kids.All {
		cs := gunim.Constraints{Min: geom.Sz(w, 0), Max: geom.Sz(w, c.Max.H)}
		if k.Node() == gunim.Node(t.more) {
			cs = gunim.Tight(geom.Sz(w, ToastMoreHeight.Get(th)))
		}
		sizes[k.Node()] = k.Layout(cs)
	}
	from, to := t.fit(sizes, c.Max.H, gap, ToastMoreHeight.Get(th))
	hidden := len(t.cards) - (to - from)
	// Heights from the bottom up, for the toasts shown.
	total := float32(0)
	if hidden > 0 {
		total = ToastMoreHeight.Get(th) + gap
	}
	for i := from; i < to; i++ {
		total += sizes[t.cards[i]].H
		if i > from {
			total += gap
		}
	}
	y := total
	motion := Settle.Get(th)
	for i := len(t.cards) - 1; i >= 0; i-- {
		card := t.cards[i]
		shown := i >= from && i < to
		card.shown.Animate(value(shown), motion)
		if !shown {
			continue
		}
		y -= sizes[card].H
		if !card.placed {
			card.y.Jump(y)
			card.placed = true
		}
		card.y.Animate(y, motion)
		y -= gap
	}
	if t.more != nil {
		t.more.count = hidden
		t.more.in.Animate(value(hidden > 0), motion)
	}
	height := total
	for k := range kids.All {
		switch n := k.Node().(type) {
		case *toastCard:
			k.Place(geom.Pt(0, n.y.Value()))
			if n.shown.Target() == 1 {
				height = max(height, n.y.Value()+k.Size().H)
			}
		case *toastMore:
			k.Place(geom.Point{})
		}
	}
	if c.Max.H > 0 {
		height = min(height, c.Max.H)
	}
	return geom.Sz(w, max(height, 0))
}

// fit returns the toasts shown, from and to in t.cards: the newest past
// t.skip, as many as fit in room with the card that counts the rest,
// and at least one. A room of zero holds them all.
func (t *Toasts) fit(sizes map[gunim.Node]geom.Size, room, gap, more float32) (from, to int) {
	n := len(t.cards)
	t.skip = min(t.skip, max(n-1, 0))
	height := func(from, to int) float32 {
		h := float32(0)
		for i := from; i < to; i++ {
			h += sizes[t.cards[i]].H
			if i > from {
				h += gap
			}
		}
		return h
	}
	if room <= 0 || t.skip == 0 && height(0, n) <= room {
		return 0, n
	}
	to = n - t.skip
	from = to - 1
	for from > 0 && height(from-1, to)+gap+more <= room {
		from--
	}
	return from, to
}

// page shows the toasts older than those shown, and the newest again
// after the oldest.
func (t *Toasts) page(u *gunim.UI) {
	shown := 0
	for _, c := range t.cards {
		if c.shown.Target() == 1 {
			shown++
		}
	}
	t.skip += max(shown, 1)
	if t.skip >= len(t.cards) {
		t.skip = 0
	}
	u.Invalidate()
}

// Paint implements [gunim.Node]. A toast with no room is drawn as it
// fades, and then left out, so it takes no clicks.
func (t *Toasts) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		switch n := k.Node().(type) {
		case *toastCard:
			if n.shown.Value() <= 0.01 && n.shown.Target() == 0 {
				continue
			}
		case *toastMore:
			if n.in.Value() <= 0.01 && n.in.Target() == 0 {
				continue
			}
		}
		k.Paint(p)
	}
}

// toastMore is the card over a stack of toasts that counts those with no
// room, and shows them on a click.
type toastMore struct {
	anim.Group
	owner *Toasts
	// count is how many toasts have no room, and in runs to 1 while any
	// do.
	count int
	in    *anim.Float
	hover *anim.Float
	text  shapedText
	size  geom.Size
	click Clicker
}

func newToastMore(t *Toasts) *toastMore {
	m := &toastMore{owner: t, in: anim.NewFloat(0), hover: anim.NewFloat(0)}
	m.Add(m.in, m.hover)
	return m
}

// label is what the card reads.
func (m *toastMore) label() string { return "+" + strconv.Itoa(m.count) + " more" }

// KeepsFocus implements [gunim.FocusKeeper].
func (m *toastMore) KeepsFocus() {}

// Layout implements [gunim.Node].
func (m *toastMore) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	m.size = c.Constrain(c.Max)
	return m.size
}

// Paint implements [gunim.Node].
func (m *toastMore) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	t := min(max(m.in.Value(), 0), 1)
	if t <= 0.001 {
		return
	}
	r := geom.Rect{Max: box.Point()}
	defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-24)), Opacity: t})()
	fill := anim.Mix(anim.ColorCodec, DialogFill.Get(th), MenuFill.Get(th), min(max(m.hover.Value(), 0), 1))
	p.ShadowRRect(r, box.H/2, paint.Solid(fill), paint.Shadow{Offset: geom.Pt(0, 4), Blur: 16, Color: DialogShadow.Get(th)})
	p.RRectStroke(r, box.H/2, paint.Fill{}, paint.Stroke{Width: 1, Color: DialogBorder.Get(th)})
	run := m.text.shape(faceIn(Font, th), m.label(), TextSize.Get(th))
	run.Paint(p, geom.Pt((box.W-run.Advance)/2, (box.H-run.Height())/2), PaletteHint.Get(th))
}

// Handle implements [gunim.Handler]: a click shows the toasts with no room.
func (m *toastMore) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		m.hover.Animate(1, Quick.Get(u.Theme()))
	case input.PointerLeave:
		m.hover.Animate(0, Settle.Get(u.Theme()))
	case input.PointerDown:
		m.click.Press(e, over(e.Pos, m.size))
	case input.PointerUp:
		if m.click.Release(e, over(e.Pos, m.size)) {
			m.owner.page(u)
		}
	default:
		return false
	}
	return true
}

// Access implements [gunim.Accessible].
func (m *toastMore) Access() access.Info {
	return access.Info{Role: access.RoleButton, Name: m.label(), Actions: []string{access.ActionPress}}
}

// AccessAct implements [gunim.AccessActor].
func (m *toastMore) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress {
		return false
	}
	m.owner.page(u)
	return true
}

// toastCard is one toast.
type toastCard struct {
	anim.Group
	owner  *Toasts
	title  *Label
	body   *Label
	action *Link
	// mark is the kind's icon before the title, or nil.
	mark *Icon
	// key is the toast's key. A toast that asks has buttons, a close
	// button, and check, its tick box, or nil. An error has a close button.
	key     string
	buttons *Flex
	close   *IconButton
	check   *Checkbox
	in      *anim.Float
	y       *anim.Float
	hover   *anim.Float
	// shown runs to 1 while the stack has room for the card.
	shown   *anim.Float
	hovered bool
	placed  bool
	// size is the card's size at its last layout, for telling a release
	// on it from one off it.
	size  geom.Size
	click Clicker
}

func newToastCard(t *Toasts, to Toast) *toastCard {
	c := &toastCard{owner: t, title: NewLabel(to.Title), body: NewLabel(to.Body),
		in: anim.NewFloat(0), y: anim.NewFloat(0), hover: anim.NewFloat(0), shown: anim.NewFloat(1)}
	c.body.Color = PaletteHint
	c.body.MaxLines = toastBodyLines
	c.Add(c.in, c.y, c.hover, c.shown)
	if to.Action != "" {
		c.action = NewLink(to.Action)
		on := to.OnClick
		c.action.OnClick = func(u *gunim.UI) gunim.Intent {
			t.dismiss(c, u)
			if on == nil {
				return nil
			}
			return on(u)
		}
	}
	c.key = to.Key
	if len(to.Buttons) > 0 {
		c.ask(to)
	} else if to.Kind == ToastError {
		c.closer(nil)
	}
	if to.Kind == ToastError {
		// An error is read with care, and reported: its text can be selected and copied.
		c.title.Selectable = true
		c.body.Selectable = true
	}
	ic, ink := to.Kind.look()
	if to.Icon != nil {
		ic = to.Icon
	}
	if ic != nil {
		c.mark = NewIcon(ic, to.Kind.name())
		c.mark.Color = ink
		c.mark.DrawOn(toastDrawOn)
	}
	return c
}

// ask gives the card the buttons, the close button and the tick box of
// a toast that asks. None of them takes the keyboard when clicked.
func (c *toastCard) ask(to Toast) {
	checked := func() bool { return c.check != nil && c.check.Checked() }
	if to.Check != "" {
		c.check = NewCheckbox(to.Check)
		c.check.KeepFocus = true
	}
	kids := make([]gunim.Node, 0, len(to.Buttons))
	for i, tb := range to.Buttons {
		b := NewButton(tb.Label)
		b.KeepFocus = true
		if i == 0 {
			b.Kind = ButtonPrimary
		}
		on := tb.OnClick
		b.OnClick = func(u *gunim.UI) gunim.Intent {
			if on != nil {
				send(u, c, on(checked(), u))
			}
			c.owner.dismiss(c, u)
			return nil
		}
		kids = append(kids, b)
	}
	c.buttons = Row(kids...)
	c.buttons.Justify = JustifyEnd
	c.buttons.Gap = Gap
	dismiss := to.OnDismiss
	c.closer(func(u *gunim.UI) gunim.Intent {
		if dismiss == nil {
			return nil
		}
		return dismiss(checked(), u)
	})
}

// closer gives the card a close button, which runs then, if not nil,
// and takes the card away. It does not take the keyboard when clicked.
func (c *toastCard) closer(then func(u *gunim.UI) gunim.Intent) {
	c.close = NewIconButton(icon.X, "Close")
	c.close.KeepFocus = true
	c.close.OnClick = func(u *gunim.UI) gunim.Intent {
		if then != nil {
			send(u, c, then(u))
		}
		c.owner.dismiss(c, u)
		return nil
	}
}

// asks reports whether the card is of a toast that asks.
func (c *toastCard) asks() bool { return c.buttons != nil }

// stays reports whether the card stays until it is closed: a toast that
// asks, or an error.
func (c *toastCard) stays() bool { return c.close != nil }

// corner is the link or the button at the right of the title, or nil.
func (c *toastCard) corner() gunim.Node {
	switch {
	case c.close != nil:
		return c.close
	case c.action != nil:
		return c.action
	}
	return nil
}

// KeepsFocus implements [gunim.FocusKeeper]: a click on a toast leaves
// the keyboard where it is.
func (c *toastCard) KeepsFocus() {}

// Children implements [gunim.Composite].
func (c *toastCard) Children() []gunim.Node {
	out := []gunim.Node{c.title}
	if c.body.Text != "" {
		out = append(out, c.body)
	}
	if c.check != nil {
		out = append(out, c.check)
	}
	if c.buttons != nil {
		out = append(out, c.buttons)
	}
	if n := c.corner(); n != nil {
		out = append(out, n)
	}
	if c.mark != nil {
		out = append(out, c.mark)
	}
	return out
}

// Transition implements [gunim.Transitioner]: in from the side and up
// from clear, and back the way it came.
func (c *toastCard) Transition(p gunim.Presence, f gunim.Frame) bool {
	switch p {
	case gunim.Entering:
		c.in.Animate(1, Settle.Get(f.Theme))
	case gunim.Exiting:
		c.in.Animate(0, Quick.Get(f.Theme))
	case gunim.Present:
	}
	return !c.in.Active()
}

// Layout implements [gunim.Node]. The icon sits before the title, and the text after it. The body shows three lines,
// and all of them while the pointer rests on the card, as many as fit in the height given.
func (c *toastCard) Layout(cs gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	c.body.MaxLines = toastBodyLines
	if !c.hovered {
		return c.lay(cs, f, kids)
	}
	c.body.MaxLines = 0
	size := c.lay(cs, f, kids)
	if cs.Max.H <= 0 || size.H <= cs.Max.H {
		return size
	}
	para := c.body.laid.p
	if para.LineHeight <= 0 {
		return size
	}
	over := int((size.H - cs.Max.H + para.LineHeight - 1) / para.LineHeight)
	c.body.MaxLines = max(toastBodyLines, len(para.Lines)-over)
	return c.lay(cs, f, kids)
}

// lay lays the card out with the body as it stands.
func (c *toastCard) lay(cs gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	th := f.Theme
	pad := CardPadding.Get(th)
	w := cs.Max.W
	left := pad.Left
	if c.mark != nil {
		left += IconSize.Get(th) + IconGap.Get(th)
	}
	room := w - left - pad.Right
	// The action, or the close button, sits at the right of the title.
	var act, title geom.Size
	// line is the height of the title's first line, which the icon sits beside.
	var line float32
	y := pad.Top
	corner := c.corner()
	for k := range kids.All {
		if corner != nil && k.Node() == corner {
			act = k.Layout(gunim.Constraints{Max: geom.Sz(room, 0)})
			top := pad.Top
			if corner == gunim.Node(c.close) {
				// The close button's box is larger than its cross; it
				// keeps to the corner rather than push the title down.
				top = pad.Top / 2
			}
			k.Place(geom.Pt(w-pad.Right-act.W, top))
		}
	}
	for k := range kids.All {
		switch n := k.Node(); n {
		case corner, gunim.Node(c.mark):
		default:
			r := room
			if n == gunim.Node(c.title) && act.W > 0 {
				r = max(0, room-act.W-Gap.Get(th))
			}
			cs := gunim.Constraints{Max: geom.Sz(r, 0)}
			if c.buttons != nil && n == gunim.Node(c.buttons) {
				// The buttons sit at the right, under a little room.
				cs.Min.W = r
				y += 4
			}
			s := k.Layout(cs)
			k.Place(geom.Pt(left, y))
			if n == gunim.Node(c.title) {
				title = s
				line = c.title.paragraph(f, r).LineHeight
			}
			y += s.H + 4
		}
	}
	for k := range kids.All {
		if c.mark != nil && k.Node() == gunim.Node(c.mark) {
			s := k.Layout(gunim.Constraints{Max: geom.Sz(room, 0)})
			k.Place(geom.Pt(pad.Left, pad.Top+(min(line, title.H)-s.H)/2))
		}
	}
	c.size = geom.Sz(w, y-4+pad.Bottom)
	return c.size
}

// Paint implements [gunim.Node].
func (c *toastCard) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	t := min(max(c.in.Value(), 0), 1)
	if t <= 0.001 {
		return
	}
	r := geom.Rect{Max: box.Point()}
	shown := min(max(c.shown.Value(), 0), 1)
	defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-24)), Opacity: t * shown})()
	defer p.Push(paint.Translate(geom.Pt(48*(1-t), 0)))()
	shadow := DialogShadow.Get(th)
	fill := anim.Mix(anim.ColorCodec, DialogFill.Get(th), MenuFill.Get(th), min(max(c.hover.Value(), 0), 1))
	p.ShadowRRect(r, CardRadius.Get(th), paint.Solid(fill), paint.Shadow{Offset: geom.Pt(0, 4), Blur: 16, Color: shadow})
	p.RRectStroke(r, CardRadius.Get(th), paint.Fill{}, paint.Stroke{Width: 1, Color: DialogBorder.Get(th)})
	for k := range kids.All {
		k.Paint(p)
	}
}

// Handle implements [gunim.Handler]: the pointer resting keeps the
// toast, and a click dismisses it, unless it stays until closed.
func (c *toastCard) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		c.hovered = true
		c.hover.Animate(1, Quick.Get(u.Theme()))
		u.Invalidate()
	case input.PointerLeave:
		c.hovered = false
		c.hover.Animate(0, Settle.Get(u.Theme()))
		u.Invalidate()
	case input.PointerDown:
		c.click.Press(e, 0)
	case input.PointerUp:
		if c.click.Release(e, over(e.Pos, c.size)) && !c.stays() {
			c.owner.dismiss(c, u)
		}
	default:
		return false
	}
	return true
}
