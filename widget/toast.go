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
	ToastMoreHeight = theme.TextRow("toast.more.height", 30)
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

// toastEdge is how wide the amber edge of a warning toast is.
const toastEdge = 3

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
	// shown with the key of one showing takes its place. Where that one is
	// the newest, its card stays: the old words fade out as the new fade
	// in, and the card glides to their size.
	Key string
	// Buttons are the toast's buttons, the first the one the toast
	// expects. They sit beside the text, at its right, where one row of
	// them leaves the text room, and under it otherwise. A toast with
	// buttons asks: it stays until one of them,
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
	// ToastWarning shows icon.TriangleAlert in [ToastWarningInk], with an edge and border in it.
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

// drawsOn reports whether the kind's icon draws its strokes on; an alert sign fades in whole instead.
func (k ToastKind) drawsOn() bool { return k != ToastWarning && k != ToastError }

// Toasts shows short notices in a stack, the newest nearest the corner
// it sits in. Each slides in from the side and fades up, stays a few
// seconds, and slides away; the pointer resting on one keeps it, and a
// click dismisses it. As one leaves, the rest glide together. A toast
// shown with the key of the newest takes its card: the card stays, its
// words roll over to the new ones, and it glides to their height.
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
// takes its place: on the same card where that toast is the newest, its
// words handing over to the new ones, and as a new card otherwise. A toast
// that asks stays until it is answered, and an error until it is closed.
func (t *Toasts) Show(to Toast, u *gunim.UI) {
	if to.Key != "" {
		// The new toast takes the old one's place, and sounds once, as it arrives.
		for i, c := range slices.Clone(t.cards) {
			if c.key != to.Key {
				continue
			}
			if i == len(t.cards)-1 {
				// The newest keeps its card and its place, and shows the new toast on it.
				c.swap(to, u)
				t.skip = 0
				if !c.stays() {
					t.expire(c, u)
				}
				u.Cue(gunim.CueOpen, t)
				return
			}
			t.take(c, u)
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

// expire takes c away once the time of the toast it shows is up, or later,
// while the pointer rests on it. A toast shown in its place since has a
// time of its own.
func (t *Toasts) expire(c *toastCard, u *gunim.UI) {
	life := t.Life
	if life <= 0 {
		life = toastLife
	}
	face := c.face
	u.After(life, func(u *gunim.UI) {
		if c.face != face {
			return
		}
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
		if card, ok := k.Node().(*toastCard); ok {
			// A card gliding to a new height is placed by that height, so it and its place move as one.
			sizes[k.Node()] = geom.Sz(w, card.want)
		}
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
		card.shown.Animate(anim.OnOff(shown), motion)
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
		t.more.in.Animate(anim.OnOff(hidden > 0), motion)
	}
	// The stack is as tall as its lowest toast is drawn, so a stack that stands on its foot keeps a toast's foot
	// still as the toast glides taller.
	height, some := float32(0), false
	for k := range kids.All {
		switch n := k.Node().(type) {
		case *toastCard:
			k.Place(geom.Pt(0, n.y.Value()))
			if n.shown.Target() == 1 {
				height, some = max(height, n.y.Value()+k.Size().H), true
			}
		case *toastMore:
			k.Place(geom.Point{})
		}
	}
	if !some {
		height = total
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

// toastCard is one toast: the card, and on it a face, which holds the toast's words, icon and buttons. A toast shown
// in the card's place brings a new face, and the card stays.
type toastCard struct {
	anim.Group
	owner *Toasts
	// face is the toast the card shows. A face it showed before stays its child while it fades out.
	face *toastFace
	// faces is how many faces the card held at its last layout: more than one while one hands over to the next.
	faces int
	key   string
	in    *anim.Float
	y     *anim.Float
	hover *anim.Float
	// shown runs to 1 while the stack has room for the card.
	shown *anim.Float
	// h is the card's height, which glides to want, the height of its face.
	h    *anim.Float
	want float32
	// warn runs to 1 while the card shows a warning, for its amber edge and border.
	warn    *anim.Float
	hovered bool
	placed  bool
	// sized says the card has been laid out, so its height glides from here on.
	sized bool
	// size is the card's size at its last layout, for telling a release
	// on it from one off it.
	size  geom.Size
	click Clicker
}

func newToastCard(t *Toasts, to Toast) *toastCard {
	c := &toastCard{owner: t, key: to.Key, in: anim.NewFloat(0), y: anim.NewFloat(0), hover: anim.NewFloat(0),
		shown: anim.NewFloat(1), h: anim.NewFloat(0), warn: anim.NewFloat(anim.OnOff(to.Kind == ToastWarning))}
	c.Add(c.in, c.y, c.hover, c.shown, c.h, c.warn)
	c.face = newToastFace(c, to, true)
	return c
}

// swap shows to on the card in place of what it shows. The card stays as it is: the old face fades out as the new
// one fades in, and the card's height glides to the new one's.
func (c *toastCard) swap(to Toast, u *gunim.UI) {
	old := c.face
	c.face = newToastFace(c, to, false)
	if !old.says(to) {
		// The same toast shown again stands as it stood. Another one rolls in.
		old.gone = true
		c.face.fade.Jump(0)
	}
	c.key = to.Key
	// A press held on the old toast is not a click on the new one.
	c.click = Clicker{}
	c.warn.Animate(anim.OnOff(to.Kind == ToastWarning), Settle.Get(u.Theme()))
	u.Insert(c, c.face)
	u.Remove(old)
	u.Invalidate()
}

// stays reports whether the card stays until it is closed: a toast that
// asks, or an error.
func (c *toastCard) stays() bool { return c.face.close != nil }

// KeepsFocus implements [gunim.FocusKeeper]: a click on a toast leaves
// the keyboard where it is.
func (c *toastCard) KeepsFocus() {}

// Children implements [gunim.Composite].
func (c *toastCard) Children() []gunim.Node { return []gunim.Node{c.face} }

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

// Layout implements [gunim.Node]. The card is as tall as its face, and glides there when the face changes or grows.
func (c *toastCard) Layout(cs gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	c.faces = kids.Len()
	for k := range kids.All {
		s := k.Layout(cs)
		k.Place(geom.Point{})
		if k.Node() == gunim.Node(c.face) {
			c.want = s.H
		}
	}
	if !c.sized {
		c.h.Jump(c.want)
		c.sized = true
	}
	c.h.Animate(c.want, Settle.Get(f.Theme))
	c.size = geom.Sz(cs.Max.W, max(c.h.Value(), 0))
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
	layer := paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-24)), Opacity: t * shown}
	if t < 0.999 {
		// Keep the sliding card inside its stack's right edge
		layer.Bounds.Max.X, layer.Clip = box.W, true
	}
	defer p.Layer(layer)()
	defer p.Push(paint.Translate(geom.Pt(48*(1-t), 0)))()
	shadow := DialogShadow.Get(th)
	fill := anim.Mix(anim.ColorCodec, DialogFill.Get(th), MenuFill.Get(th), min(max(c.hover.Value(), 0), 1))
	radius := CardRadius.Get(th)
	p.ShadowRRect(r, radius, paint.Solid(fill), paint.Shadow{Offset: geom.Pt(0, 4), Blur: 16, Color: shadow})
	border := DialogBorder.Get(th)
	if warn := min(max(c.warn.Value(), 0), 1); warn > 0.001 {
		// An amber edge down the left and a faint amber border
		amber := ToastWarningInk.Get(th)
		border = anim.Mix(anim.ColorCodec, border, amber, 0.45*warn)
		edge := p.Layer(paint.LayerOpts{Bounds: r, Clip: true, Radius: radius, Opacity: warn})
		p.RRect(geom.Rect{Max: geom.Pt(toastEdge, r.Max.Y)}, 0, paint.Solid(amber))
		edge()
	}
	p.RRectStroke(r, radius, paint.Fill{}, paint.Stroke{Width: 1, Color: border})
	// The words and icon come in behind the card, never ahead of it: a dark card on a dark window shows less at
	// a low opacity than light words on it do, so they wait till it stands. While the card changes height, or one
	// face hands over to the next, they keep inside it.
	a := toastContent(t)
	if moving := c.faces > 1 || c.h.Active(); a < 0.999 || moving {
		defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: a, Clip: moving, Radius: radius})()
	}
	for k := range kids.All {
		k.Paint(p)
	}
}

// toastContent is how far a toast's words and icon have come in, as the card has come t of the way: nothing till the
// card is four fifths in, then easing to all of it with the card.
func toastContent(t float32) float32 {
	x := min(max((t-0.8)/0.2, 0), 1)
	return x * x * (3 - 2*x)
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

// toastSide is the most of a toast's width its buttons take beside its words. Wider, they sit under the words.
const toastSide = 0.45

// toastSideText is the least width the words keep with the buttons beside them.
const toastSideText = 120

// toastRise is how far a face moves as it hands over: the old one up and out, the new one up and in.
const toastRise = 14

// toastFace is what a toast card shows: the title and body, the icon before them, and the action, buttons, tick box
// and close button. The first face of a card arrives with it. One that takes another's place fades in as that one
// fades out.
type toastFace struct {
	anim.Group
	card *toastCard
	// said is the toast the face shows.
	said   Toast
	title  *Label
	body   *Label
	action *Link
	// mark is the kind's icon before the title, or nil.
	mark *Icon
	kind ToastKind
	// A toast that asks has buttons, a close button, and check, its tick box, or nil. An error has a close button.
	buttons *Wrap
	close   *IconButton
	check   *Checkbox
	// fade runs to 1 as the face shows, and back to 0 once another takes its place, which gone says.
	fade *anim.Float
	gone bool
	// where is where the buttons sit, and beside says that is beside the words, not under them. It is worked out
	// once for a width and text size, which decided holds.
	where   toastButtons
	beside  bool
	decided toastFit
	laid    bool
	size    geom.Size
}

// toastButtons is where a toast's buttons sit.
type toastButtons uint8

const (
	// toastUnder puts the buttons under the words, at the right.
	toastUnder toastButtons = iota
	// toastLeft puts them beside the words, left of the close button, at the words' middle.
	toastLeft
	// toastColumn puts them beside the words, under the close button, in its column.
	toastColumn
)

// toastColumnGap is the room between the close button and buttons under it.
const toastColumnGap = 2

// toastColumnOver is how much taller a toast may be with its buttons under the close button than with them left
// of it, and still put them there, for the room that gives the words.
const toastColumnOver = 8

// toastFit is what the place of a toast's buttons was worked out for.
type toastFit struct{ width, text float32 }

// newToastFace returns the face of to on c. arrives says it comes with its card, so its icon draws on as the card
// stands; one that takes another's place shows its icon whole, and fades in with its words.
func newToastFace(c *toastCard, to Toast, arrives bool) *toastFace {
	fc := &toastFace{card: c, said: to, title: NewLabel(to.Title), body: NewLabel(to.Body), kind: to.Kind, fade: anim.NewFloat(1)}
	fc.body.Color = PaletteHint
	fc.body.MaxLines = toastBodyLines
	fc.Add(fc.fade)
	t := c.owner
	if to.Action != "" {
		fc.action = NewLink(to.Action)
		on := to.OnClick
		fc.action.OnClick = func(u *gunim.UI) gunim.Intent {
			t.dismiss(c, u)
			if on == nil {
				return nil
			}
			return on(u)
		}
	}
	if len(to.Buttons) > 0 {
		fc.ask(to)
	} else if to.Kind == ToastError {
		fc.closer(nil)
	}
	if to.Kind == ToastError {
		// An error is read with care, and reported: its text can be selected and copied.
		fc.title.Selectable = true
		fc.body.Selectable = true
	}
	ic, ink := to.Kind.look()
	if to.Icon != nil {
		ic = to.Icon
	}
	if ic != nil {
		fc.mark = NewIcon(ic, to.Kind.name())
		fc.mark.Color = ink
		if arrives && to.Kind.drawsOn() {
			fc.mark.DrawOn(toastDrawOn)
		}
	}
	return fc
}

// says reports whether the face shows what to would: the same words, icon and buttons.
func (fc *toastFace) says(to Toast) bool {
	was := fc.said
	return was.Title == to.Title && was.Body == to.Body && was.Action == to.Action && was.Kind == to.Kind &&
		was.Icon == to.Icon && was.Check == to.Check &&
		slices.EqualFunc(was.Buttons, to.Buttons, func(a, b ToastButton) bool { return a.Label == b.Label })
}

// ask gives the face the buttons, the close button and the tick box of
// a toast that asks. None of them takes the keyboard when clicked.
func (fc *toastFace) ask(to Toast) {
	c := fc.card
	checked := func() bool { return fc.check != nil && fc.check.Checked() }
	if to.Check != "" {
		fc.check = NewCheckbox(to.Check)
		fc.check.KeepFocus = true
	}
	kids := make([]gunim.Node, 0, len(to.Buttons))
	for i, tb := range to.Buttons {
		b := NewButton(tb.Label)
		b.KeepFocus = true
		// A label wider than the card still cuts, and this shows the whole of it.
		b.Tooltip = tb.Label
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
	// They wrap: squeezed into a row, their words would clip.
	fc.buttons = NewWrap(kids...)
	fc.buttons.Justify = JustifyEnd
	dismiss := to.OnDismiss
	fc.closer(func(u *gunim.UI) gunim.Intent {
		if dismiss == nil {
			return nil
		}
		return dismiss(checked(), u)
	})
}

// closer gives the face a close button, which runs then, if not nil,
// and takes the card away. It does not take the keyboard when clicked.
func (fc *toastFace) closer(then func(u *gunim.UI) gunim.Intent) {
	c := fc.card
	fc.close = NewIconButton(icon.X, "Close")
	fc.close.KeepFocus = true
	fc.close.OnClick = func(u *gunim.UI) gunim.Intent {
		if then != nil {
			send(u, c, then(u))
		}
		c.owner.dismiss(c, u)
		return nil
	}
}

// corner is what sits at the right of the title, from the right: the
// close button, then the action.
func (fc *toastFace) corner() []gunim.Node {
	var out []gunim.Node
	if fc.close != nil {
		out = append(out, fc.close)
	}
	if fc.action != nil {
		out = append(out, fc.action)
	}
	return out
}

// Children implements [gunim.Composite].
func (fc *toastFace) Children() []gunim.Node {
	out := []gunim.Node{fc.title}
	if fc.body.Text != "" {
		out = append(out, fc.body)
	}
	if fc.check != nil {
		out = append(out, fc.check)
	}
	if fc.buttons != nil {
		out = append(out, fc.buttons)
	}
	out = append(out, fc.corner()...)
	if fc.mark != nil {
		out = append(out, fc.mark)
	}
	return out
}

// Transition implements [gunim.Transitioner]: a face that takes another's place fades in, and the one it takes the
// place of fades out, at the same pace, so the card is never bare. A face that leaves with its card goes as the
// card does.
func (fc *toastFace) Transition(p gunim.Presence, f gunim.Frame) bool {
	switch p {
	case gunim.Entering:
		fc.fade.Animate(1, Settle.Get(f.Theme))
	case gunim.Exiting:
		if fc.gone {
			fc.fade.Animate(0, Settle.Get(f.Theme))
		}
	case gunim.Present:
	}
	return !fc.fade.Active()
}

// Layout implements [gunim.Node]. The icon sits before the title, and the text after it. The body shows three lines,
// and all of them while the pointer rests on the card, as many as fit in the height given. Buttons sit beside the
// words where that leaves the words room, and under them where it doesn't.
func (fc *toastFace) Layout(cs gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	fc.body.MaxLines = toastBodyLines
	size := fc.place(cs, f, kids)
	if !fc.card.hovered {
		return size
	}
	fc.body.MaxLines = 0
	size = fc.lay(cs, f, kids, fc.where)
	if cs.Max.H <= 0 || size.H <= cs.Max.H {
		return size
	}
	para := fc.body.laid.p
	if para.LineHeight <= 0 {
		return size
	}
	over := int((size.H - cs.Max.H + para.LineHeight - 1) / para.LineHeight)
	fc.body.MaxLines = max(toastBodyLines, len(para.Lines)-over)
	return fc.lay(cs, f, kids, fc.where)
}

// place lays the face out with its body at three lines, the buttons beside the words where they fit: in one row
// that takes no more than [toastSide] of the width, with every word of the body still showing.
func (fc *toastFace) place(cs gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	fit := toastFit{cs.Max.W, TextSize.Get(f.Theme)}
	if fc.laid && fc.decided == fit {
		// Buttons that no longer fit beside the words, as after a theme's padding changed, are placed anew.
		if size := fc.lay(cs, f, kids, fc.where); size.W > 0 || fc.where == toastUnder {
			return size
		}
	}
	fc.laid, fc.decided, fc.where, fc.beside = true, fit, toastUnder, false
	if fc.buttons != nil && fc.check == nil {
		// try lays the buttons out at where, and returns the face's height, or zero where that cuts the body or
		// doesn't fit.
		try := func(where toastButtons) float32 {
			if size := fc.lay(cs, f, kids, where); size.W > 0 && !fc.body.laid.p.Truncated {
				return size.H
			}
			return 0
		}
		column, left := try(toastColumn), try(toastLeft)
		switch {
		case column > 0 && (left == 0 || column <= left+toastColumnOver):
			// The corner's column leaves the words the most room, so it wins unless it makes a taller toast.
			fc.where = toastColumn
		case left > 0:
			fc.where = toastLeft
		case column > 0, left > 0:
		}
		fc.beside = fc.where != toastUnder
	}
	return fc.lay(cs, f, kids, fc.where)
}

// lay lays the face out with the body as it stands, the buttons where it is told. Asked for buttons beside words
// they don't fit beside, it returns no size.
func (fc *toastFace) lay(cs gunim.Constraints, f gunim.Frame, kids gunim.Children, where toastButtons) geom.Size {
	th := f.Theme
	pad := CardPadding.Get(th)
	gap := Gap.Get(th)
	w := cs.Max.W
	left := pad.Left
	if fc.mark != nil {
		left += IconSize.Get(th) + IconGap.Get(th)
	}
	room := w - left - pad.Right
	// The close button, and the action before it, sit at the right of
	// the title; act is the width they take.
	var act float32
	// foot is how far down the face the close button ends, or zero with none.
	var foot float32
	corner := fc.corner()
	// Children lists the corner in its order, from the right.
	for k := range kids.All {
		if !slices.Contains(corner, k.Node()) {
			continue
		}
		if act > 0 {
			act += gap
		}
		s := k.Layout(gunim.Constraints{Max: geom.Sz(max(0, room-act), 0)})
		act += s.W
		top := pad.Top
		if k.Node() == gunim.Node(fc.close) {
			// The close button's box is larger than its cross; it
			// keeps to the corner rather than push the title down.
			top = pad.Top / 2
			foot = top + s.H
		}
		k.Place(geom.Pt(w-pad.Right-act, top))
	}
	// taken is what the corner takes of the title's line, with the gap before it.
	var taken float32
	if act > 0 {
		taken = act + gap
	}
	if where == toastUnder {
		return fc.words(f, kids, w, left, room, room-taken, room, geom.Size{}, 0)
	}
	var buttons geom.Size
	for k := range kids.All {
		if k.Node() != gunim.Node(fc.buttons) {
			continue
		}
		// Given the whole width, the buttons are as wide as their words want. Left of the corner, the corner's
		// width is not theirs to share.
		most := room
		if where == toastLeft {
			most = max(0, room-taken)
		}
		k.Layout(gunim.Constraints{Max: geom.Sz(most, 0)})
		if fc.buttons.lines != 1 || fc.buttons.wide > most*toastSide {
			return geom.Size{}
		}
		buttons = k.Layout(gunim.Tight(geom.Sz(fc.buttons.wide, fc.buttons.tall)))
	}
	if where == toastColumn {
		// In the corner's column, under the close button.
		if foot == 0 {
			return geom.Size{}
		}
		r := room - max(buttons.W, act) - gap
		if r < toastSideText {
			return geom.Size{}
		}
		return fc.words(f, kids, w, left, room, r, r, buttons, foot+toastColumnGap)
	}
	// Left of the corner, at the words' middle.
	r := room - taken - buttons.W - gap
	if r < toastSideText {
		return geom.Size{}
	}
	return fc.words(f, kids, w, left, room, r, r, buttons, 0)
}

// words places the title in titleRoom and the rest in bodyRoom, from left, and the icon before the title. With
// buttons beside the words, of the size given, it places them at the right, at the words' middle. With a clear
// above zero they stand in the corner's column, no higher than clear, and the face is tall enough to hold them.
func (fc *toastFace) words(f gunim.Frame, kids gunim.Children, w, left, room, titleRoom, bodyRoom float32, buttons geom.Size, clear float32) geom.Size {
	th := f.Theme
	pad := CardPadding.Get(th)
	beside := buttons.W > 0
	// The words go down from the top, each at its y from the first.
	type put struct {
		k gunim.Child
		y float32
	}
	var puts []put
	var title geom.Size
	// line is the height of the title's first line, which the icon sits beside.
	var line float32
	var y float32
	for k := range kids.All {
		n := k.Node()
		if slices.Contains(fc.corner(), n) || n == gunim.Node(fc.mark) || beside && n == gunim.Node(fc.buttons) {
			continue
		}
		r := bodyRoom
		if n == gunim.Node(fc.title) {
			r = titleRoom
		}
		r = max(0, r)
		kc := gunim.Constraints{Max: geom.Sz(r, 0)}
		if n == gunim.Node(fc.buttons) {
			// The buttons sit at the right, under a little room.
			kc.Min.W = r
			y += 4
		}
		s := k.Layout(kc)
		puts = append(puts, put{k, y})
		if n == gunim.Node(fc.title) {
			title = s
			line = fc.title.paragraph(f, r).LineHeight
		}
		y += s.H + 4
	}
	words := y - 4
	// Words shorter than the buttons beside them sit at the buttons' middle, and the buttons at the words'.
	block := max(words, buttons.H)
	x := w - pad.Right - buttons.W
	if clear > 0 {
		block = max(block, clear+buttons.H-pad.Top)
	} else if beside {
		x = left + bodyRoom + Gap.Get(th)
	}
	at := max(pad.Top+(block-buttons.H)/2, clear)
	top := pad.Top + (block-words)/2
	for _, p := range puts {
		p.k.Place(geom.Pt(left, top+p.y))
	}
	for k := range kids.All {
		switch k.Node() {
		case gunim.Node(fc.mark):
			if fc.mark == nil {
				continue
			}
			s := k.Layout(gunim.Constraints{Max: geom.Sz(room, 0)})
			k.Place(geom.Pt(pad.Left, top+(min(line, title.H)-s.H)/2))
		case gunim.Node(fc.buttons):
			if beside {
				k.Place(geom.Pt(x, at))
			}
		}
	}
	fc.size = geom.Sz(w, pad.Top+block+pad.Bottom)
	return fc.size
}

// Paint implements [gunim.Node]. A face handing over is drawn part clear, a little above or below its place.
func (fc *toastFace) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	x := min(max(fc.fade.Value(), 0), 1)
	// The words change over in the middle of the roll, so the old and the new are both half clear for a moment
	// only, and one of them is always more than half there.
	a := min(max((x-0.25)/0.5, 0), 1)
	a = a * a * (3 - 2*a)
	if a <= 0.001 {
		return
	}
	if x < 0.999 {
		rise := toastRise * (1 - x)
		if fc.gone {
			rise = -rise
		}
		r := geom.Rect{Max: box.Point()}
		defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-toastRise)), Opacity: a})()
		defer p.Push(paint.Translate(geom.Pt(0, rise)))()
	}
	for k := range kids.All {
		k.Paint(p)
	}
}
