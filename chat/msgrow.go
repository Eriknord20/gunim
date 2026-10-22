package chat

import (
	"image/color"
	"strings"
	"unicode"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/markdown"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// Sizes of a message row.
const (
	gutter     = 64 // the room left of the text, for the avatar
	avatarSize = 36
	rowPad     = 6
	quoteH     = 26
	maxQuoteW  = 560
	dayH       = 40
	newH       = 24
)

// ToolIcon is the size of the icons in a message's toolbar.
var ToolIcon = theme.Length("chat.tool.icon", 15)

// msgRow is a row of the timeline: a day's heading, or a message with its author, its time, the message it replies
// to, its text and, for the user's own, how far it got. Under the pointer it lights and shows a toolbar.
type msgRow struct {
	anim.Group
	item Item
	// jump scrolls the timeline to a message and flashes it.
	jump func(id string, u *gunim.UI)

	// body shows the message's text, pictures its pictures, and tools its toolbar; all are nil in a day's heading.
	body      *markdown.View
	pictures  []*widget.Image
	poll      *pollCard
	preview   *previewCard
	file      *fileCard
	reactions *reactionBar
	tools     gunim.Node
	toolsAt   geom.Point
	// react opens the emoji picker for a message, and menu is the menu of
	// what to do with this one, while it is open.
	react    func(opener gunim.Node, id string, u *gunim.UI)
	menu     *gunim.Popup
	menuList *widget.Menu
	hover    *anim.Float
	flash    *anim.Float

	// laidFor is what the texts below were laid out for.
	laidFor struct {
		item        Item
		width, size float32
	}
	name, time, foot, quote, initials text.Run
	// status says how far one of the user's own messages got, after the time, or as a mark in the gutter when the
	// message shares the heading of the one before; statusBox is where it shows.
	status, bang                 text.Run
	bodyAt                       float32
	quoteBox, footBox, statusBox geom.Rect
	// height is how tall the row was at its last layout.
	height float32
}

func newMsgRow(item Item, jump func(string, *gunim.UI), group *markdown.Group, image func(id string) *paint.Image,
	react func(opener gunim.Node, id string, u *gunim.UI),
) *msgRow {
	r := &msgRow{item: item, jump: jump, react: react, hover: anim.NewFloat(0), flash: anim.NewFloat(0)}
	r.Add(r.hover, r.flash)
	if !item.heading() {
		r.body = markdown.New("")
		r.body.Breaks = true
		r.body.Group, r.body.Key = group, item.Key
		r.setBody()
		for _, p := range item.Pictures {
			if src := image(p.ID); src != nil {
				img := widget.NewImage(src)
				img.Fit, img.Radius, img.Size = widget.FitCover, 8, pictureSize(p)
				r.pictures = append(r.pictures, img)
			}
		}
		r.poll = newPollCard(item.ID, item.Poll)
		r.preview = newPreviewCard(item.Preview, image)
		r.file = newFileCard(item.File)
		r.reactions = newReactionBar(item.ID, item.Reactions, react)
		r.tools = newTools(item.Message, react)
	}
	return r
}

// setBody shows the message's text, or that it was withdrawn.
func (r *msgRow) setBody() {
	if r.item.Withdrawn {
		r.body.SetText("*This message was withdrawn.*")
		r.body.Ink = Faint
		return
	}
	r.body.SetText(r.item.Body)
	r.body.Ink = theme.Token[color.NRGBA]{}
}

// newTools makes a message's toolbar: react and reply, and for the user's own messages edit and withdraw.
func newTools(m Message, react func(opener gunim.Node, id string, u *gunim.UI)) gunim.Node {
	button := func(ic *icon.Icon, tip string, on gunim.Intent) gunim.Node {
		b := widget.NewIconButton(ic, tip)
		b.IconSize, b.KeepFocus = ToolIcon, true
		b.OnClick = widget.Sends(on)
		return b
	}
	smile := widget.NewIconButton(icon.SmilePlus, "React")
	smile.IconSize, smile.KeepFocus = ToolIcon, true
	smile.OnClick = func(u *gunim.UI) gunim.Intent {
		react(smile, m.ID, u)
		return nil
	}
	buttons := []gunim.Node{smile, button(icon.Reply, "Reply", ReplyAsked{ID: m.ID})}
	if m.Mine {
		buttons = append(buttons,
			button(icon.Pencil, "Edit", EditAsked{ID: m.ID}),
			button(icon.Trash2, "Withdraw", WithdrawAsked{ID: m.ID}))
	}
	return widget.NewToolbar(buttons...)
}

// set shows item in place of the row's old one.
func (r *msgRow) set(item Item, u *gunim.UI) {
	r.item = item
	if r.body != nil {
		r.setBody()
		r.poll.set(item.Poll, u)
		r.preview.set(item.Preview, u)
		r.reactions.set(item.ID, item.Reactions, u)
	}
	u.Invalidate()
}

// Flash lights the row for a moment, for a message the timeline jumped to.
func (r *msgRow) Flash() {
	r.flash.Jump(1)
	r.flash.Animate(0, anim.Tween{Duration: 1400_000_000, Ease: anim.EaseInOut})
}

// Children implements [gunim.Composite].
func (r *msgRow) Children() []gunim.Node {
	if r.body == nil {
		return nil
	}
	out := []gunim.Node{r.body}
	for _, p := range r.pictures {
		out = append(out, p)
	}
	return append(out, r.poll, r.preview, r.file, r.reactions, r.tools)
}

// showsTools reports whether the toolbar can show: on a message still there.
func (r *msgRow) showsTools() bool { return r.tools != nil && !r.item.Withdrawn }

// Layout implements [gunim.Node].
func (r *msgRow) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	th := f.Theme
	w := c.Max.W
	if r.item.heading() {
		r.lay(th, w)
		if r.item.New {
			return geom.Sz(w, newH)
		}
		return geom.Sz(w, dayH)
	}
	r.lay(th, w)
	body, tools := kids.At(0), kids.At(kids.Len()-1)
	textW := textWidth(w)
	bs := body.Layout(gunim.Constraints{Min: geom.Sz(textW, 0), Max: geom.Sz(textW, 0)})
	body.Place(geom.Pt(gutter, r.bodyAt))
	y := r.bodyAt + bs.H
	if r.item.Body == "" && !r.item.Withdrawn {
		y = r.bodyAt - blockGap
	}
	// Under the text come its pictures, its poll, its link's card and its reactions, each after a gap, and those
	// with nothing to show take no room.
	for i := 1; i < kids.Len()-1; i++ {
		kid := kids.At(i)
		s := kid.Layout(gunim.Loose(geom.Sz(textW, 1000)))
		if s.H <= 0 {
			kid.Place(geom.Pt(gutter, y))
			continue
		}
		kid.Place(geom.Pt(gutter, y+blockGap))
		y += s.H + blockGap
	}
	r.footBox = geom.Rect{}
	if footText(r.item.Message) != "" {
		r.footBox = geom.Rc(gutter, y+2, r.foot.Advance, r.foot.Height())
		y += r.foot.Height() + 2
	}
	ts := tools.Layout(gunim.Loose(geom.Sz(w, 60)))
	r.toolsAt = geom.Pt(w-ts.W-16, 2)
	tools.Place(r.toolsAt)
	r.height = max(y+rowPad, ts.H+4)
	return geom.Sz(w, r.height)
}

// pictureSize is how large a picture shows in the timeline: its own size, shrunk to fit 360 by 260 and keeping its
// shape.
func pictureSize(p Picture) geom.Size {
	w, h := float32(max(p.W, 1)), float32(max(p.H, 1))
	scale := min(1, 360/w, 260/h)
	return geom.Sz(w*scale, h*scale)
}

// blockGap is the room above each block under a message's text.
const blockGap = 6

// textWidth is how wide a message's text is in a row w wide.
func textWidth(w float32) float32 { return max(w-gutter-24, 40) }

// lay lays the row's texts out for a row w wide, unless they already are.
func (r *msgRow) lay(th *theme.Live, w float32) {
	size := widget.TextSize.Get(th)
	if same(r.laidFor.item, r.item) && r.laidFor.width == w && r.laidFor.size == size {
		return
	}
	r.laidFor.item, r.laidFor.width, r.laidFor.size = r.item, w, size
	small := SmallText.Get(th)
	regular, bold := widget.Font.Get(th), widget.BoldFont.Get(th)
	switch {
	case r.item.New:
		r.name = bold.Shape("New", small)
		return
	case r.item.Day != "":
		r.name = bold.Shape(r.item.Day, small)
		return
	}
	m := r.item.Message
	textW := textWidth(w)
	y := float32(rowPad)
	r.initials = bold.Shape(initials(m.Author), size)
	r.time = regular.Shape(m.At.Format("15:04"), small)
	if !m.Continued {
		r.name = bold.Shape(m.Author, size)
		y += r.name.Height() + 2
	}
	r.quoteBox = geom.Rect{}
	if m.Reply.ID != "" {
		q := m.Reply.Author + "  " + firstLine(markdown.Plain(m.Reply.Text))
		if m.Reply.Gone {
			q = "Message withdrawn"
		}
		r.quote = shapeFit(regular, q, small, min(textW, maxQuoteW)-24)
		r.quoteBox = geom.Rc(gutter, y, r.quote.Advance+24, quoteH)
		y += quoteH + 4
	}
	r.bodyAt = y
	ascent, descent, gap := regular.Metrics(size)
	line := ascent + descent + gap
	r.statusBox = geom.Rect{}
	if status := statusText(m); status != "" {
		if m.Continued {
			const mark = 14
			r.bang = bold.Shape("!", 11)
			r.statusBox = geom.Rc(gutter-12-mark, r.bodyAt+(line-mark)/2, mark, mark)
		} else {
			r.status = regular.Shape(status, small)
			x := gutter + r.name.Advance + 8 + r.time.Advance + 10
			r.statusBox = geom.Rc(x, rowPad+r.name.Ascent-r.status.Ascent, r.status.Advance, r.status.Height())
		}
	}
	r.foot = regular.Shape(footText(m), small)
}

// statusText says how far one of the user's own messages got, or nothing once it arrived, or that only the user sees
// the message.
func statusText(m Message) string {
	switch {
	case m.Withdrawn:
		return ""
	case m.Private:
		return "Only visible to you"
	case m.State == Pending:
		return "Sending…"
	case m.State == Failed:
		return "Not sent. Click to try again."
	}
	return ""
}

// footText is the line under a message: whether it was edited.
func footText(m Message) string {
	if m.Edited && !m.Withdrawn {
		return "(edited)"
	}
	return ""
}

// Paint implements [gunim.Node].
func (r *msgRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	switch {
	case r.item.New:
		r.paintNew(p, th, box)
		return
	case r.item.Day != "":
		r.paintDay(p, th, box)
		return
	}
	m := r.item.Message
	full := geom.Rect{Max: box.Point()}
	if h := min(r.hover.Value(), 1); h > 0.01 {
		p.RRect(full, 6, paint.Solid(fade(RowHot.Get(th), h)))
	}
	if fl := min(r.flash.Value(), 1); fl > 0.01 {
		p.RRect(full, 6, paint.Solid(fade(widget.Accent.Get(th), 0.25*fl)))
	}
	ink, faint := widget.Ink.Get(th), Faint.Get(th)
	if !m.Continued {
		r.paintAvatar(p, geom.Rc(16, rowPad+2, avatarSize, avatarSize), AvatarTint(m.Author))
		r.name.Paint(p, geom.Pt(gutter, rowPad), ink)
		r.time.Paint(p, geom.Pt(gutter+r.name.Advance+8, rowPad+r.name.Ascent-r.time.Ascent), faint)
	} else if h := min(r.hover.Value(), 1); h > 0.01 && r.statusBox.Empty() {
		r.time.Paint(p, geom.Pt(gutter-12-r.time.Advance, r.bodyAt+2), fade(faint, h))
	}
	r.paintStatus(p, th)
	if !r.quoteBox.Empty() {
		q := r.quoteBox
		p.RRect(q, 6, paint.Solid(QuoteFill.Get(th)))
		p.RRect(geom.Rc(q.Min.X, q.Min.Y, 3, q.Size().H), 1.5, paint.Solid(AvatarTint(m.Reply.Author)))
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: q, Opacity: 1, Clip: true})()
			r.quote.Paint(p, geom.Pt(q.Min.X+12, q.Min.Y+(quoteH-r.quote.Height())/2), faint)
		}()
	}
	func() {
		if m.State != Sent && !m.Withdrawn {
			// A message on its way is paler.
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: 0.6})()
		}
		for i := range kids.Len() - 1 {
			kids.At(i).Paint(p)
		}
	}()
	if !r.footBox.Empty() {
		r.foot.Paint(p, r.footBox.Min, faint)
	}
	// The toolbar shows under the pointer, and gives way to the menu.
	if h := min(r.hover.Value(), 1); h > 0.01 && r.showsTools() && r.menu == nil {
		tools := kids.At(kids.Len() - 1)
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Min: r.toolsAt, Max: r.toolsAt.Add(tools.Size().Point())}, Opacity: h})()
		tools.Paint(p)
	}
}

// paintStatus draws how far one of the user's own messages got: words after the time, or in the gutter a ring
// while it is on its way and a red mark once it failed.
func (r *msgRow) paintStatus(p *paint.Painter, th *theme.Live) {
	b := r.statusBox
	if b.Empty() {
		return
	}
	failed := r.item.State == Failed
	ink := Faint.Get(th)
	if failed {
		ink = ErrorInk.Get(th)
	}
	if !r.item.Continued {
		r.status.Paint(p, b.Min, ink)
		return
	}
	radius := b.Size().W / 2
	if !failed {
		p.RRectStroke(b.Inset(geom.Uniform(1.5)), radius, paint.Fill{}, paint.Stroke{Width: 1.5, Color: ink})
		return
	}
	p.RRect(b, radius, paint.Solid(ink))
	c := b.Center()
	r.bang.Paint(p, geom.Pt(c.X-r.bang.Advance/2, c.Y-r.bang.Height()/2), widget.ButtonStrongInk.Get(th))
}

// paintNew draws the line over the messages not yet read: a line across the row, and "New" at its end.
func (r *msgRow) paintNew(p *paint.Painter, th *theme.Live, box geom.Size) {
	red := BadgeFill.Get(th)
	mid := box.H / 2
	w := r.name.Advance + 16
	pill := geom.Rc(box.W-16-w, mid-10, w, 20)
	p.RRect(geom.Rc(16, mid-0.5, pill.Min.X-16, 1), 0, paint.Solid(red))
	p.RRect(pill, 10, paint.Solid(red))
	r.name.Paint(p, geom.Pt(pill.Min.X+8, mid-r.name.Height()/2), widget.ButtonStrongInk.Get(th))
}

// paintDay draws a day's heading: its name in a pill on a line across the row.
func (r *msgRow) paintDay(p *paint.Painter, th *theme.Live, box geom.Size) {
	mid := box.H / 2
	p.RRect(geom.Rc(16, mid, box.W-32, 1), 0, paint.Solid(widget.FieldBorder.Get(th)))
	w := r.name.Advance + 24
	pill := geom.Rc((box.W-w)/2, mid-12, w, 24)
	p.RRectStroke(pill, 12, paint.Solid(PaneFill.Get(th)), paint.Stroke{Width: 1, Color: widget.FieldBorder.Get(th)})
	r.name.Paint(p, geom.Pt(pill.Min.X+12, mid-r.name.Height()/2), Faint.Get(th))
}

// paintAvatar draws an author's initials in a circle of their colour.
func (r *msgRow) paintAvatar(p *paint.Painter, at geom.Rect, tint color.NRGBA) {
	p.RRect(at, at.Size().W/2, paint.Solid(tint))
	c := at.Center()
	r.initials.Paint(p, geom.Pt(c.X-r.initials.Advance/2, c.Y-r.initials.Height()/2), color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
}

// Handle implements [gunim.Handler]: the row lights under the pointer, a click on a quote jumps to the message it
// quotes, and a click on a failed message's status sends it again. A right click, or a finger held on the
// message, opens a menu of what the toolbar does.
func (r *msgRow) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		r.hover.Animate(1, widget.Quick.Get(u.Theme()))
	case input.PointerLeave:
		if r.menu == nil {
			r.hover.Animate(0, widget.Settle.Get(u.Theme()))
		}
	case input.PointerDown:
		if e.Button == input.ButtonSecondary && r.showsTools() {
			r.openMenu(e.Pos, u)
			return true
		}
		if e.Button != input.ButtonPrimary {
			return false
		}
		m := r.item.Message
		switch {
		case r.quoteBox.Contains(e.Pos) && !m.Reply.Gone:
			r.jump(m.Reply.ID, u)
			return true
		case r.statusBox.Contains(e.Pos) && m.State == Failed:
			u.Send(r, RetryAsked{ID: m.ID})
			return true
		}
	}
	return false
}

// openMenu opens the menu of what to do with the message at at: react and reply, and for the user's own
// messages edit and withdraw, as the toolbar does. The message stays lit while it is open, as the one the menu
// is for.
func (r *msgRow) openMenu(at geom.Point, u *gunim.UI) {
	r.closeMenu(u)
	m := r.item.Message
	items := []widget.MenuItem{{Label: "React", Icon: icon.SmilePlus}, {Label: "Reply", Icon: icon.Reply}}
	if m.Mine {
		items = append(items, widget.MenuItem{Label: "Edit", Icon: icon.Pencil}, widget.MenuItem{Label: "Withdraw", Icon: icon.Trash2})
	}
	menu := widget.NewMenu(items)
	menu.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		r.closeMenu(u)
		switch items[i].Label {
		case "React":
			r.react(r, m.ID, u)
		case "Reply":
			u.Send(r, ReplyAsked{ID: m.ID})
		case "Edit":
			u.Send(r, EditAsked{ID: m.ID})
		case "Withdraw":
			u.Send(r, WithdrawAsked{ID: m.ID})
		}
		return nil
	}
	r.hover.Animate(1, widget.Quick.Get(u.Theme()))
	r.menuList = menu
	r.menu = u.OpenPopup(r, menu, gunim.PopupOptions{
		Anchor:  geom.Rect{Min: at, Max: at},
		Max:     geom.Sz(600, 480),
		Dismiss: r.closeMenu,
	})
}

// closeMenu closes the message's menu, if it is open, and the row stops being lit.
func (r *msgRow) closeMenu(u *gunim.UI) {
	if r.menu == nil {
		return
	}
	r.menu.Close()
	r.menu, r.menuList = nil, nil
	r.hover.Animate(0, widget.Settle.Get(u.Theme()))
}

// Cursor implements [gunim.CursorShaper]: a hand over what takes a click.
func (r *msgRow) Cursor(p geom.Point) input.Cursor {
	m := r.item.Message
	if (r.quoteBox.Contains(p) && !m.Reply.Gone) || (r.statusBox.Contains(p) && m.State == Failed) {
		return input.CursorHand
	}
	return input.CursorArrow
}

// initials returns the first letters of the first two words of a name.
func initials(name string) string {
	var out []rune
	for _, word := range strings.Fields(name) {
		for _, c := range word {
			out = append(out, unicode.ToUpper(c))
			break
		}
		if len(out) == 2 {
			break
		}
	}
	return string(out)
}

// shapeFit shapes s in face at size, cut short with an ellipsis where it would run wider than w.
func shapeFit(face *text.Face, s string, size, w float32) text.Run {
	run := face.Shape(s, size)
	if run.Advance <= w {
		return run
	}
	rs := []rune(s)
	lo, hi := 0, len(rs)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if face.Shape(strings.TrimRight(string(rs[:mid]), " ")+"…", size).Advance <= w {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return face.Shape(strings.TrimRight(string(rs[:lo]), " ")+"…", size)
}

// firstLine returns s up to its first line break.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}
