// Package chat is a conversation's view: a timeline that opens at its latest message, or at the first not read,
// and stays there as messages arrive, and a message box under it with mentions, emoji, pictures, replies and
// edits. Messages carry reactions, polls, link cards and shared files, and the user's own say how far they got.
//
// The view is the window half. A host builds it with [NewView], places it in a view of its own, and hands it each
// conversation with [View.Set]. What the user does comes back as intents, such as [Submitted] and
// [ReactionToggled], which the host's application half acts on. [Timeline] turns a conversation's messages into
// the timeline's rows.
package chat

import (
	"image/color"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/emoji"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/markdown"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// Options turns the parts of a [View] on or off, and sets what its message box says and suggests.
type Options struct {
	// Header shows the conversation's name over the timeline, with buttons beside it.
	Header bool
	// Members puts a button in the header that slides the conversation's members in beside the timeline.
	Members bool
	// PopOut puts a button in the header that sends [PopOut].
	PopOut bool
	// LinkBar shows a bar over the view while the connection is down. A host that shows one over more than the
	// view makes its own with [NewLinkBar].
	LinkBar bool
	// HeaderLead goes before the conversation's name in the header, HeaderTools after it, before the members and
	// pop-out buttons, and HeaderEnd after those.
	HeaderLead, HeaderTools, HeaderEnd []gunim.Node
	// Placeholder is what the empty message box says. Empty, it says "Message", then "#" and the conversation's
	// title, or for one with a person, their name. Hints are shown in turn after it.
	Placeholder string
	Hints       []string
	// Triggers are characters that start a word Complete suggests endings for, besides @ for people and : for
	// emoji. Complete is asked for those too, and its suggestions follow the view's own.
	Triggers string
	Complete func(trigger rune, query string) []widget.Completion
}

// View is a conversation: its timeline and its message box, and as [Options] say, a header, the members, and the
// bar that says the connection is down. It is a node a host places in its own view, and fills the room it gets.
type View struct {
	root gunim.Node
	opts Options

	title    *widget.Label
	linkBar  *LinkBar
	timeline *slot
	list     *widget.VirtualList
	typing   *widget.Label
	reply    *replyBar
	composer *widget.TextArea

	// items holds the timeline's rows by key, for building them.
	items map[widget.Key]Item
	// images holds every picture the timeline and the message box show, by ID, and strip the pictures waiting to
	// go with the next message.
	images map[string]*paint.Image
	strip  *pictureStrip
	// catchUp says how many new messages lie below the view.
	catchUp *catchUp
	// picker is the emoji picker reactions come from.
	picker widget.EmojiPicker
	// order is the timeline's keys, and at where each is in it; group lets a selection run across its messages.
	order    []widget.Key
	at       map[widget.Key]int
	group    *markdown.Group
	current  string
	last     widget.Key
	draftSeq int
	// people are who take part in the conversation, for mentions.
	people []string
	// loading says older messages are on their way.
	loading *loadingPill
	// drawer slides the members of the conversation in beside it, which membersButton opens and closes.
	drawer        *widget.Drawer
	members       *membersPanel
	membersButton *widget.IconButton
}

// NewView builds an empty view with the parts o turns on. It calls [RegisterTypes].
func NewView(o Options) *View {
	RegisterTypes()
	v := &View{opts: o, items: map[widget.Key]Item{}, draftSeq: -1}

	v.title = widget.NewLabel("")
	v.title.Face, v.title.Size = widget.BoldFont, titleSize

	v.linkBar = NewLinkBar()
	v.list = v.newList()
	v.timeline = &slot{child: newFader(v.list)}
	v.catchUp = newCatchUp()

	v.typing = widget.NewLabel(" ")
	v.typing.Color, v.typing.Size = Faint, SmallText
	v.reply = &replyBar{open: anim.NewFloat(0)}
	v.reply.Add(v.reply.open)
	v.composer = widget.NewTextArea()
	v.composer.Rows, v.composer.MaxRows = 1, 8
	v.composer.Placeholders = o.Hints
	v.composer.Triggers, v.composer.CompleteAbove = "@:"+o.Triggers, true
	v.composer.Complete = v.complete
	v.composer.OnCommit = func(s string, u *gunim.UI) gunim.Intent { return Submitted{Text: s} }
	v.composer.OnChange = func(s string, u *gunim.UI) gunim.Intent { return Drafted{Text: s} }
	v.composer.OnPasteImage = func(png []byte, u *gunim.UI) gunim.Intent { return ImagePasted{PNG: png} }
	send := widget.NewIconButton(icon.SendHorizontal, "Send")
	send.OnClick = func(*gunim.UI) gunim.Intent { return Submitted{Text: v.composer.Text()} }
	box := widget.Row(v.composer, send).Grow(v.composer, 1)
	box.Cross = widget.CrossEnd
	v.strip = newPictureStrip()
	composer := &composerBox{child: widget.Column(v.strip, v.reply, box), reply: v.reply}
	composer.child.Cross = widget.CrossStretch

	bottom := widget.Column(v.typing, composer)
	bottom.Cross = widget.CrossStretch
	bottomPad := widget.NewPad(bottom)
	bottomPad.Padding = composerPad
	v.loading = newLoadingPill()
	timeline := &timelineBox{list: v.timeline, pill: v.catchUp, loading: v.loading}
	var parts []gunim.Node
	if o.LinkBar {
		parts = append(parts, v.linkBar)
	}
	if o.Header {
		parts = append(parts, widget.NewPad(v.newHeader()))
	}
	parts = append(parts, timeline, bottomPad)
	main := widget.Column(parts...).Grow(timeline, 1)
	main.Cross, main.Gap = widget.CrossStretch, zeroGap
	v.root = &pane{child: main, fill: PaneFill}
	if o.Header && o.Members {
		v.members = newMembersPanel(func(u *gunim.UI) { v.openMembers(false, u) })
		v.drawer = widget.NewDrawer(v.root, v.members.root)
		v.root = v.drawer
	}
	return v
}

// newHeader makes the header: the conversation's name, and the buttons the options ask for.
func (v *View) newHeader() *widget.Flex {
	hash := widget.NewIcon(icon.Hash, "Conversation")
	spacer := widget.NewSpacer()
	kids := append(append([]gunim.Node(nil), v.opts.HeaderLead...), hash, v.title, spacer)
	kids = append(kids, v.opts.HeaderTools...)
	if v.opts.Members {
		people := widget.NewIconButton(icon.Users, "Members")
		people.OnClick = func(u *gunim.UI) gunim.Intent {
			v.openMembers(!v.drawer.Open(), u)
			return nil
		}
		v.membersButton = people
		kids = append(kids, people)
	}
	if v.opts.PopOut {
		popOut := widget.NewIconButton(icon.SquareArrowOutUpRight, "Open in a window of its own")
		popOut.OnClick = widget.Sends(PopOut{})
		kids = append(kids, popOut)
	}
	kids = append(kids, v.opts.HeaderEnd...)
	header := widget.Row(kids...).Grow(spacer, 1)
	header.Cross = widget.CrossCenter
	return header
}

var (
	titleSize   = theme.Length("chat.title.size", 16)
	zeroGap     = theme.Length("chat.gap.none", 0)
	composerPad = theme.Insets("chat.composer.pad", geom.Insets{Top: 0, Right: 16, Bottom: 16, Left: 16})
)

// openMembers slides the members in, or out, and lights the button that does it while they show.
func (v *View) openMembers(on bool, u *gunim.UI) {
	v.drawer.SetOpen(on, u)
	v.membersButton.Active = on
	if !on {
		u.Focus(v.composer)
	}
	u.Invalidate()
}

// maxSuggestions is how many people or emoji the message box suggests at once.
const maxSuggestions = 8

// complete suggests, for a word typed after @, the people of the conversation whose names have a word starting
// with it, and after :, the emoji whose names hold it, of those the system draws; then what the options' Complete
// suggests.
func (v *View) complete(trigger rune, query string) []widget.Completion {
	var out []widget.Completion
	q := strings.ToLower(query)
	switch trigger {
	case '@':
		for _, p := range v.people {
			if matchesName(p, q) {
				out = append(out, widget.Completion{Text: "@" + p, Label: p, Swatch: AvatarTint(p)})
			}
		}
	case ':':
		// A colon and one letter is too often a smiley or the end of a sentence.
		if len([]rune(q)) < 2 {
			break
		}
		for _, e := range emoji.Search(strings.ReplaceAll(q, "_", " ")) {
			if !text.EmojiShows(e.Text) {
				continue
			}
			out = append(out, widget.Completion{Text: e.Text, Label: e.Text + "  " + e.Name})
			if len(out) == maxSuggestions {
				break
			}
		}
	}
	if v.opts.Complete != nil {
		out = append(out, v.opts.Complete(trigger, query)...)
	}
	return out[:min(len(out), maxSuggestions)]
}

// matchesName reports whether a word of name starts with q, or q is empty.
func matchesName(name, q string) bool {
	for _, w := range strings.Fields(strings.ToLower(name)) {
		if strings.HasPrefix(w, q) {
			return true
		}
	}
	return q == ""
}

// newList makes an empty timeline, which starts at its end and stays there as messages arrive.
func (v *View) newList() *widget.VirtualList {
	v.group = markdown.NewGroup(v.messagesBetween, func(key string) string {
		if it := v.items[widget.Key(key)]; !it.Withdrawn {
			return markdown.Plain(it.Body)
		}
		return ""
	})
	l := widget.NewVirtualList(func(k widget.Key) gunim.Node {
		return newMsgRow(v.items[k], v.jump, v.group, func(id string) *paint.Image { return v.images[id] }, v.react)
	})
	l.StickToEnd, l.HoldOnPrepend = true, true
	l.OnReachStart = func(u *gunim.UI) gunim.Intent { return OlderAsked{} }
	l.Estimate = 44
	l.Spacing = TimelineSpacing
	return l
}

// react opens the emoji picker below opener, to react to the message id.
func (v *View) react(opener gunim.Node, id string, u *gunim.UI) {
	if v.picker.IsOpen() {
		v.picker.Close(u)
	}
	v.picker.OnPick = func(emoji string, u *gunim.UI) gunim.Intent {
		u.Send(v, ReactionToggled{ID: id, Emoji: emoji})
		return nil
	}
	v.picker.Open(opener, geom.Rc(0, 0, 28, 28), u)
}

// messagesBetween returns the keys of the messages from one to another, both included, in the timeline's order.
func (v *View) messagesBetween(from, to string) []string {
	i, ok1 := v.at[widget.Key(from)]
	j, ok2 := v.at[widget.Key(to)]
	if !ok1 || !ok2 || i > j {
		return nil
	}
	var out []string
	for _, k := range v.order[i : j+1] {
		if !v.items[k].heading() {
			out = append(out, string(k))
		}
	}
	return out
}

// jump scrolls the timeline to the message id and flashes it.
func (v *View) jump(id string, u *gunim.UI) {
	v.list.ScrollToKey(widget.Key(id), u)
	if n, ok := v.list.Row(widget.Key(id)); ok {
		if row, is := n.(*msgRow); is {
			row.Flash()
		}
	}
}

// Set shows the conversation c. A conversation with another ID than the last gets a new timeline, which fades in
// over the old one.
func (v *View) Set(c Conversation, u *gunim.UI) {
	v.people = c.People
	if v.members != nil {
		v.members.set(c.Members, u)
	}
	v.loading.set(c.Loading, u)
	v.title.Text = c.Title
	v.composer.Placeholder = v.placeholder(c)
	if v.opts.LinkBar {
		v.linkBar.Set(c.Link, u)
	}
	v.reply.set(c.Replying, c.Editing != "", u)
	v.images = c.Images
	v.strip.set(c.Pending, c.Images, u)

	if c.ID != v.current {
		// A new conversation gets a new timeline, which opens at its end.
		v.current = c.ID
		v.list = v.newList()
		for _, it := range c.Items {
			if it.New {
				// It opens at the first message not read, for reading on from there.
				v.list.OpenAt(widget.Key(it.Key))
				break
			}
		}
		v.catchUp.set(v.list, c.Unread, u)
		v.timeline.swap(newFader(v.list), u)
		v.items = map[widget.Key]Item{}
		v.last = ""
	}
	keys := make([]widget.Key, len(c.Items))
	items := make(map[widget.Key]Item, len(c.Items))
	for i, it := range c.Items {
		k := widget.Key(it.Key)
		keys[i], items[k] = k, it
		if old, ok := v.items[k]; ok && !same(old, it) {
			if n, built := v.list.Row(k); built {
				if row, is := n.(*msgRow); is {
					row.set(it, u)
				}
			}
		}
	}
	v.items, v.order = items, keys
	v.at = make(map[widget.Key]int, len(keys))
	for i, k := range keys {
		v.at[k] = i
	}
	if v.last != "" && !v.list.AtEnd() {
		// Messages from others that come while the view is up the timeline wait below it.
		came := 0
		for i := len(c.Items) - 1; i >= 0 && c.Items[i].Key != string(v.last); i-- {
			if it := c.Items[i]; !it.heading() && !it.Mine {
				came++
			}
		}
		v.catchUp.set(v.list, v.catchUp.count+came, u)
	}
	v.list.SetKeys(keys, u)
	if n := len(keys); n > 0 && keys[n-1] != v.last {
		// The user's own message brings the timeline back to its end.
		if v.last != "" && c.Items[n-1].Mine {
			v.list.ScrollToEnd(widget.Quick.Get(u.Theme()))
		}
		v.last = keys[n-1]
	}

	if c.Draft.Seq != v.draftSeq {
		v.draftSeq = c.Draft.Seq
		v.composer.SetText(c.Draft.Text, u)
		u.Focus(v.composer)
	}
}

// placeholder is what the empty message box says for c.
func (v *View) placeholder(c Conversation) string {
	switch {
	case v.opts.Placeholder != "":
		return v.opts.Placeholder
	case c.Title != "" && !c.Direct:
		return "Message #" + c.Title
	}
	return "Message " + c.Title
}

// SetTyping shows who is typing, or nobody when who is empty.
func (v *View) SetTyping(who string, u *gunim.UI) {
	if who == "" {
		v.typing.Text = " "
	} else {
		v.typing.Text = who + " is typing…"
	}
	u.Invalidate()
}

// Focus gives the message box the keyboard.
func (v *View) Focus(u *gunim.UI) { u.Focus(v.composer) }

// Children implements [gunim.Composite].
func (v *View) Children() []gunim.Node { return []gunim.Node{v.root} }

// Layout implements [gunim.Node].
func (v *View) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	kid.Layout(gunim.Tight(c.Max))
	kid.Place(geom.Point{})
	return c.Max
}

// Paint implements [gunim.Node].
func (v *View) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// Handle implements [gunim.Handler]: typing that nothing else takes, as after a click in a message, goes to the
// message box. A host can pass it typing from beyond the view too.
func (v *View) Handle(e input.Event, u *gunim.UI) bool {
	switch e.(type) {
	case input.TextInput, input.Composing:
		u.Focus(v.composer)
		return v.composer.Handle(e, u)
	}
	return false
}

// pane fills its box with a colour behind its child, blurring what is behind it as far as [Backdrop] says.
type pane struct {
	child gunim.Node
	fill  theme.Token[color.NRGBA]
}

// Children implements [gunim.Composite].
func (p *pane) Children() []gunim.Node { return []gunim.Node{p.child} }

// Layout implements [gunim.Node].
func (p *pane) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	kid.Layout(gunim.Tight(c.Max))
	kid.Place(geom.Point{})
	return c.Max
}

// Paint implements [gunim.Node].
func (p *pane) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	r := geom.Rect{Max: box.Point()}
	if b := Backdrop.Get(f.Theme); b > 0 {
		glass := pt.Layer(paint.LayerOpts{Bounds: r, Opacity: 1, Backdrop: b, Clip: true})
		pt.RRect(r, 0, paint.Solid(p.fill.Get(f.Theme)))
		glass()
	} else {
		pt.RRect(r, 0, paint.Solid(p.fill.Get(f.Theme)))
	}
	kids.At(0).Paint(pt)
}

// slot holds one child at a time. A child swapped in fades in over the one leaving, which fades out.
type slot struct{ child gunim.Node }

// Children implements [gunim.Composite].
func (s *slot) Children() []gunim.Node { return []gunim.Node{s.child} }

// swap puts n in place of the child.
func (s *slot) swap(n gunim.Node, u *gunim.UI) {
	u.Remove(s.child)
	s.child = n
	u.Insert(s, n)
}

// Layout implements [gunim.Node]: each child, the one leaving too, fills the slot.
func (s *slot) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	for kid := range kids.All {
		kid.Layout(gunim.Tight(c.Max))
		kid.Place(geom.Point{})
	}
	return c.Max
}

// Paint implements [gunim.Node].
func (s *slot) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for kid := range kids.All {
		kid.Paint(p)
	}
}

// fader fades its child in, rising a little, as it arrives, and fades it out as it leaves.
type fader struct {
	anim.Group
	child gunim.Node
	in    *anim.Float
}

func newFader(child gunim.Node) *fader {
	f := &fader{child: child, in: anim.NewFloat(0)}
	f.Add(f.in)
	return f
}

// Children implements [gunim.Composite].
func (f *fader) Children() []gunim.Node { return []gunim.Node{f.child} }

// Transition implements [gunim.Transitioner].
func (f *fader) Transition(pr gunim.Presence, fr gunim.Frame) bool {
	switch pr {
	case gunim.Entering:
		f.in.Animate(1, widget.Settle.Get(fr.Theme))
	case gunim.Exiting:
		f.in.Animate(0, widget.Quick.Get(fr.Theme))
	case gunim.Present:
	}
	return !f.in.Active()
}

// Layout implements [gunim.Node].
func (f *fader) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	kid.Layout(gunim.Tight(c.Max))
	kid.Place(geom.Pt(0, 14*(1-min(max(f.in.Value(), 0), 1))))
	return c.Max
}

// Paint implements [gunim.Node].
func (f *fader) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: min(max(f.in.Value(), 0), 1), Clip: true})()
	kids.At(0).Paint(p)
}

// thumbSize is the size of a picture waiting to go with the next message.
const thumbSize = 72

// pictureStrip shows the pictures waiting to go with the next message, each with a button that takes it off. It
// opens over the message box while there are any.
type pictureStrip struct {
	anim.Group
	open   *anim.Float
	row    *widget.Flex
	thumbs map[string]*thumb
}

func newPictureStrip() *pictureStrip {
	s := &pictureStrip{open: anim.NewFloat(0), row: widget.Row(), thumbs: map[string]*thumb{}}
	s.Add(s.open)
	return s
}

// set shows ps, whose pictures images holds: new ones arrive in the row, and ones gone leave it.
func (s *pictureStrip) set(ps []Picture, images map[string]*paint.Image, u *gunim.UI) {
	keep := make(map[string]bool, len(ps))
	for _, p := range ps {
		keep[p.ID] = true
		if _, ok := s.thumbs[p.ID]; ok || images[p.ID] == nil {
			continue
		}
		t := newThumb(p.ID, images[p.ID])
		s.thumbs[p.ID] = t
		u.Insert(s.row, t)
	}
	for id, t := range s.thumbs {
		if !keep[id] {
			u.Remove(t)
			delete(s.thumbs, id)
		}
	}
	s.open.Animate(map[bool]float32{false: 0, true: 1}[len(s.thumbs) > 0], widget.Quick.Get(u.Theme()))
}

// Children implements [gunim.Composite].
func (s *pictureStrip) Children() []gunim.Node { return []gunim.Node{s.row} }

// Layout implements [gunim.Node].
func (s *pictureStrip) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	kid.Layout(gunim.Loose(geom.Sz(c.Max.W, thumbSize)))
	kid.Place(geom.Pt(0, 2))
	return geom.Sz(c.Max.W, (thumbSize+10)*max(0, s.open.Value()))
}

// Paint implements [gunim.Node].
func (s *pictureStrip) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	if box.H < 0.5 {
		return
	}
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: min(1, s.open.Value()), Clip: true})()
	kids.At(0).Paint(p)
}

// thumb is a picture waiting to go with the next message, and a button at its corner that takes it off.
type thumb struct {
	img    *widget.Image
	remove *widget.IconButton
}

func newThumb(id string, src *paint.Image) *thumb {
	img := widget.NewImage(src)
	img.Fit, img.Radius, img.Size = widget.FitCover, 8, geom.Sz(thumbSize, thumbSize)
	remove := widget.NewIconButton(icon.X, "Remove the picture")
	remove.IconSize, remove.Ghost, remove.KeepFocus = ToolIcon, false, true
	remove.OnClick = widget.Sends(PictureRemoved{ID: id})
	return &thumb{img: img, remove: remove}
}

// Children implements [gunim.Composite].
func (t *thumb) Children() []gunim.Node { return []gunim.Node{t.img, t.remove} }

// Layout implements [gunim.Node].
func (t *thumb) Layout(_ gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	img, remove := kids.At(0), kids.At(1)
	img.Layout(gunim.Tight(geom.Sz(thumbSize, thumbSize)))
	img.Place(geom.Point{})
	bs := remove.Layout(gunim.Loose(geom.Sz(thumbSize, thumbSize)))
	remove.Place(geom.Pt(thumbSize-bs.W-3, 3))
	return geom.Sz(thumbSize, thumbSize)
}

// Paint implements [gunim.Node].
func (t *thumb) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

// composerBox is the message box with the reply bar over it. Escape in it drops a reply or an edit.
type composerBox struct {
	child *widget.Flex
	// reply is the bar over the box saying a reply or an edit is under way.
	reply *replyBar
}

// Children implements [gunim.Composite].
func (b *composerBox) Children() []gunim.Node { return []gunim.Node{b.child} }

// Layout implements [gunim.Node].
func (b *composerBox) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	size := kid.Layout(gunim.Constraints{Min: geom.Sz(c.Max.W, 0), Max: geom.Sz(c.Max.W, c.Max.H)})
	kid.Place(geom.Point{})
	return size
}

// Paint implements [gunim.Node].
func (b *composerBox) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// Handle implements [gunim.Handler]: Escape stops a reply or an edit,
// and goes on past the box when there is neither.
func (b *composerBox) Handle(e input.Event, u *gunim.UI) bool {
	if k, ok := e.(input.KeyPress); ok && k.Key == input.KeyEscape && b.reply != nil && b.reply.open.Target() > 0 {
		u.Send(b, Cancelled{})
		return true
	}
	return false
}

// LinkBar is the strip that opens while the connection is down, saying so.
type LinkBar struct {
	anim.Group
	open *anim.Float
	link Link
	run  text.Run
	msg  string
}

// NewLinkBar makes a bar for a connection that is up, which takes no room.
func NewLinkBar() *LinkBar {
	b := &LinkBar{open: anim.NewFloat(0)}
	b.Add(b.open)
	return b
}

// Set shows the state of the connection, opening the bar while it is down and closing it once it is back.
func (b *LinkBar) Set(l Link, u *gunim.UI) {
	b.link = l
	switch l {
	case Offline:
		b.msg = "You are offline. Messages and files you send wait, and go when the connection is back."
	case Reconnecting:
		b.msg = "Connecting…"
	default:
		// Online says nothing: the banner is closing.
	}
	b.open.Animate(map[bool]float32{false: 0, true: 1}[l != Online], widget.Settle.Get(u.Theme()))
	u.Invalidate()
}

// Layout implements [gunim.Node].
func (b *LinkBar) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	b.run = widget.Font.Get(f.Theme).Shape(b.msg, SmallText.Get(f.Theme))
	return geom.Sz(c.Max.W, 30*max(0, b.open.Value()))
}

// Paint implements [gunim.Node].
func (b *LinkBar) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	if box.H < 0.5 {
		return
	}
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: min(1, b.open.Value()), Clip: true})()
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(WarnFill.Get(f.Theme)))
	b.run.Paint(p, geom.Pt(16, (box.H-b.run.Height())/2), widget.Ink.Get(f.Theme))
}

// replyBar is the strip over the message box that says what the next message replies to, or that it edits one.
type replyBar struct {
	anim.Group
	open    *anim.Float
	msg     string
	run     text.Run
	editing bool
}

func (b *replyBar) set(q Quote, editing bool, u *gunim.UI) {
	b.editing = editing
	switch {
	case editing:
		b.msg = "Editing a message. Escape stops."
	case q.ID != "":
		b.msg = "Replying to " + q.Author + ": " + firstLine(markdown.Plain(q.Text))
	}
	b.open.Animate(map[bool]float32{false: 0, true: 1}[editing || q.ID != ""], widget.Quick.Get(u.Theme()))
	u.Invalidate()
}

// Layout implements [gunim.Node].
func (b *replyBar) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	b.run = widget.Font.Get(f.Theme).Shape(b.msg, SmallText.Get(f.Theme))
	return geom.Sz(c.Max.W, 30*max(0, b.open.Value()))
}

// Paint implements [gunim.Node].
func (b *replyBar) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	if box.H < 0.5 {
		return
	}
	th := f.Theme
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: min(1, b.open.Value()), Clip: true})()
	r := geom.Rc(0, 2, box.W, box.H-6)
	p.RRect(r, 6, paint.Solid(QuoteFill.Get(th)))
	p.RRect(geom.Rc(0, 2, 3, box.H-6), 1.5, paint.Solid(widget.Accent.Get(th)))
	b.run.Paint(p, geom.Pt(12, r.Min.Y+(r.Size().H-b.run.Height())/2), Faint.Get(th))
}

// Handle implements [gunim.Handler]: a click on the bar drops the reply or the edit.
func (b *replyBar) Handle(e input.Event, u *gunim.UI) bool {
	if d, ok := e.(input.PointerDown); ok && d.Button == input.ButtonPrimary {
		u.Send(b, Cancelled{})
		return true
	}
	return false
}
