package main

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

// registerViews is the window half: the chat view, its patches and the themes.
func registerViews(w *gunim.Window) {
	w.RegisterTheme(darkTheme())
	w.RegisterTheme(lightTheme())
	gunim.RegisterView(w, "chat", buildChat, (*chatView).set)
	gunim.RegisterPatch(w, "chat", func(v *chatView, t Typing, u *gunim.UI) { v.setTyping(t.Who, u) })
}

// Widths of the rail and the sidebar.
const (
	railW    = 68
	sidebarW = 248
)

// TimelineSpacing is the room between the timeline's rows.
var TimelineSpacing = theme.Length("chat.timeline.spacing", 0)

// chatView is the whole window: the project rail, the sidebar of conversations, and the open conversation.
type chatView struct {
	root gunim.Node
	// screens lays out the rail, the sidebar and the open conversation,
	// side by side or, on a phone, one screen at a time; sidebar is the
	// sidebar's panel. picks is the count of picks from the list seen.
	screens *screens
	sidebar *panel
	picks   int

	rail        *rail
	projectName *widget.Label
	convs       *widget.List

	title    *widget.Label
	link     *widget.Button
	linkBar  *linkBar
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
	// solo says the window shows its conversation alone, without the projects and the conversations.
	solo bool
	// people are who take part in the open conversation, for mentions.
	people []string
	// loading says older messages are on their way.
	loading *loadingPill
	// drawer slides the members of the conversation in beside it, which people opens and closes.
	drawer        *widget.Drawer
	members       *membersPanel
	membersButton *widget.IconButton
	// areas holds the conversation and the project's files, and shows one of them; files is the second.
	areas *areas
	files *filesPane
}

// buildChat builds the chat's window: the projects, the conversations and the one open, or for a window popped out
// of the main one, its conversation alone.
func buildChat(s Chat) *chatView {
	v := &chatView{items: map[widget.Key]Item{}, draftSeq: -1, solo: s.Solo}

	v.rail = &rail{hover: -1}
	v.projectName = widget.NewLabel("")
	v.projectName.Face, v.projectName.Size = widget.BoldFont, sidebarTitle
	v.convs = widget.NewList()
	v.convs.OnActivate = func(k widget.Key, u *gunim.UI) gunim.Intent {
		if area, ok := strings.CutPrefix(string(k), "area:"); ok {
			return AreaChosen{Area: area}
		}
		return ConversationChosen{ID: string(k)}
	}
	side := widget.Column(v.projectName, v.convs)
	side.Cross = widget.CrossStretch
	sideTheme := theme.Make("chat.side", theme.Set(widget.ListSpacing, 2))
	sidebar := &panel{child: widget.NewThemed(widget.NewPad(side), sideTheme), fill: SidebarFill, width: sidebarW}
	v.sidebar = sidebar

	v.title = widget.NewLabel("")
	v.title.Face, v.title.Size = widget.BoldFont, sidebarTitle
	hash := widget.NewIcon(icon.Hash, "Conversation")
	v.link = widget.NewButton("Online")
	v.link.Icon, v.link.Ghost = icon.Wifi, true
	v.link.OnClick = widget.Sends(LinkToggled{})
	themeButton := widget.NewIconButton(icon.SunMoon, "Switch theme")
	themeButton.OnClick = widget.Sends(ThemeToggled{})
	spacer := widget.NewSpacer()
	popOut := widget.NewIconButton(icon.SquareArrowOutUpRight, "Open in a window of its own")
	popOut.OnClick = widget.Sends(PopOut{})
	people := widget.NewIconButton(icon.Users, "Members")
	people.OnClick = func(u *gunim.UI) gunim.Intent {
		v.openMembers(!v.drawer.Open(), u)
		return nil
	}
	v.membersButton = people
	back := widget.NewIconButton(icon.ArrowLeft, "Conversations")
	back.OnClick = func(u *gunim.UI) gunim.Intent {
		v.screens.show(false, u)
		return nil
	}
	v.screens = newScreens()
	header := widget.Row(&narrowOnly{s: v.screens, on: true, child: back}, hash, v.title, spacer, v.link, people,
		&narrowOnly{s: v.screens, child: popOut}, themeButton).Grow(spacer, 1)
	header.Cross = widget.CrossCenter

	v.linkBar = &linkBar{open: anim.NewFloat(0)}
	v.linkBar.Add(v.linkBar.open)
	v.list = v.newList()
	v.timeline = &slot{child: newFader(v.list)}
	v.catchUp = newCatchUp(v)

	v.typing = widget.NewLabel(" ")
	v.typing.Color, v.typing.Size = Faint, SmallText
	v.reply = &replyBar{open: anim.NewFloat(0)}
	v.reply.Add(v.reply.open)
	v.composer = widget.NewTextArea()
	v.composer.Rows, v.composer.MaxRows = 1, 8
	v.composer.Placeholders = []string{"/help to show commands"}
	v.composer.Triggers, v.composer.CompleteAbove = "@:", true
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
	parts := []gunim.Node{timeline, bottomPad}
	if s.Solo {
		// A window of its own names the conversation in its title bar
		parts = append([]gunim.Node{v.linkBar}, parts...)
	} else {
		parts = append([]gunim.Node{widget.NewPad(header)}, parts...)
	}
	main := widget.Column(parts...).Grow(timeline, 1)
	main.Cross, main.Gap = widget.CrossStretch, zeroGap
	pane := &panel{child: main, fill: PaneFill}

	if !s.Solo {
		v.files = newFilesPane()
		v.members = newMembersPanel(func(u *gunim.UI) { v.openMembers(false, u) })
		v.drawer = widget.NewDrawer(pane, v.members.root)
		v.areas = newAreas(v.drawer, v.files.root)
		// The bar saying the connection is down spans every area
		right := widget.Column(v.linkBar, v.areas).Grow(v.areas, 1)
		right.Cross, right.Gap = widget.CrossStretch, zeroGap
		rightPane := &panel{child: right, fill: PaneFill}
		v.screens.rail, v.screens.sidebar, v.screens.right = v.rail, sidebar, rightPane
		v.root = v.screens
		return v
	}
	row := widget.Row(pane).Grow(pane, 1)
	row.Cross, row.Gap = widget.CrossStretch, zeroGap
	v.root = row
	return v
}

var (
	sidebarTitle = theme.Length("chat.title.size", 16)
	zeroGap      = theme.Length("chat.gap.none", 0)
	composerPad  = theme.Insets("chat.composer.pad", geom.Insets{Top: 0, Right: 16, Bottom: 16, Left: 16})
)

// openMembers slides the members in, or out, and lights the button that does it while they show.
func (v *chatView) openMembers(on bool, u *gunim.UI) {
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
// with it, and after :, the emoji whose names hold it, of those the system draws.
func (v *chatView) complete(trigger rune, query string) []widget.Completion {
	var out []widget.Completion
	q := strings.ToLower(query)
	switch trigger {
	case '@':
		for _, p := range v.people {
			if matchesName(p, q) {
				out = append(out, widget.Completion{Text: "@" + p, Label: p, Swatch: avatarTint(p)})
			}
		}
	case ':':
		if len([]rune(q)) < 2 {
			// A colon and one letter is too often a smiley or the end of a sentence.
			return nil
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
func (v *chatView) newList() *widget.VirtualList {
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
func (v *chatView) react(opener gunim.Node, id string, u *gunim.UI) {
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
func (v *chatView) messagesBetween(from, to string) []string {
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
func (v *chatView) jump(id string, u *gunim.UI) {
	v.list.ScrollToKey(widget.Key(id), u)
	if n, ok := v.list.Row(widget.Key(id)); ok {
		if row, is := n.(*msgRow); is {
			row.Flash()
		}
	}
}

// set shows the state s.
func (v *chatView) set(s Chat, u *gunim.UI) {
	if !v.solo {
		v.rail.set(s.Projects, s.Project, u)
		if s.Project < len(s.Projects) {
			v.projectName.Text = s.Projects[s.Project].Name
		}
		widget.Sync(v.convs, u, s.Conversations,
			func(c Conversation) widget.Key { return widget.Key(c.ID) },
			func(c Conversation) *convRow { return newConvRow(c, chosen(s, c)) },
			func(r *convRow, c Conversation, u *gunim.UI) { r.set(c, chosen(s, c), u) })
		v.setArea(s, u)
	}
	v.people = s.People
	if v.members != nil {
		v.members.set(s.Members, u)
	}
	v.loading.set(s.Loading, u)
	v.title.Text = s.Title
	v.composer.Placeholder = "Message " + s.Title
	if s.Title != "" && !isDirect(s) {
		v.composer.Placeholder = "Message #" + s.Title
	}
	v.setLink(s.Link, u)
	v.reply.set(s.Replying, s.Editing != "", u)
	v.images = s.Images
	v.strip.set(s.Pending, s.Images, u)

	if s.Picks != v.picks {
		// On a phone, a conversation or an area picked from the list
		// slides in over it.
		v.picks = s.Picks
		v.screens.show(true, u)
	}
	if s.Current != v.current {
		// A new conversation gets a new timeline, which opens at its end.
		v.current = s.Current
		v.list = v.newList()
		if s.NewKey != "" {
			// It opens at the first message not read, for reading on from there.
			v.list.OpenAt(widget.Key(s.NewKey))
		}
		v.catchUp.set(s.Unread, u)
		v.timeline.swap(newFader(v.list), u)
		v.items = map[widget.Key]Item{}
		v.last = ""
	}
	keys := make([]widget.Key, len(s.Items))
	items := make(map[widget.Key]Item, len(s.Items))
	for i, it := range s.Items {
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
		for i := len(s.Items) - 1; i >= 0 && s.Items[i].Key != string(v.last); i-- {
			if it := s.Items[i]; !it.heading() && !it.Mine {
				came++
			}
		}
		v.catchUp.set(v.catchUp.count+came, u)
	}
	v.list.SetKeys(keys, u)
	if n := len(keys); n > 0 && keys[n-1] != v.last {
		// The user's own message brings the timeline back to its end.
		if v.last != "" && s.Items[n-1].Mine {
			v.list.ScrollToEnd(widget.Quick.Get(u.Theme()))
		}
		v.last = keys[n-1]
	}

	if s.Draft.Seq != v.draftSeq {
		v.draftSeq = s.Draft.Seq
		v.composer.SetText(s.Draft.Text, u)
		u.Focus(v.composer)
	}
}

// chosen reports whether c is the row the pane shows: the area open, or the conversation when none is.
func chosen(s Chat, c Conversation) bool {
	if c.Area != "" || s.Area != "" {
		return c.Area == s.Area
	}
	return c.ID == s.Current
}

// setArea shows the area of the project s says, and hands it the keyboard as it arrives.
func (v *chatView) setArea(s Chat, u *gunim.UI) {
	was := v.areas.shown
	switch s.Area {
	case "files":
		name := ""
		if s.Project < len(s.Projects) {
			name = s.Projects[s.Project].Name
		}
		v.files.set(name, s.Files, s.Conversations, u)
		v.areas.show(1, u)
		if was != 1 {
			u.Focus(v.files.grid)
		}
	default:
		v.areas.show(0, u)
		if was != 0 {
			u.Focus(v.composer)
		}
	}
}

// isDirect reports whether the open conversation is with one person.
func isDirect(s Chat) bool {
	for _, c := range s.Conversations {
		if c.ID == s.Current {
			return c.Direct
		}
	}
	return false
}

// setTyping shows who is typing, or nobody.
func (v *chatView) setTyping(who string, u *gunim.UI) {
	if who == "" {
		v.typing.Text = " "
	} else {
		v.typing.Text = who + " is typing…"
	}
	u.Invalidate()
}

// setLink shows the state of the connection, in the header's button and the bar under it.
func (v *chatView) setLink(l Link, u *gunim.UI) {
	switch l {
	case Online:
		v.link.Label = "Online"
		v.link.Icon = icon.Wifi
	case Offline:
		v.link.Label = "Offline"
		v.link.Icon = icon.WifiOff
	case Reconnecting:
		v.link.Label = "Connecting"
		v.link.Icon = icon.Wifi
	}
	v.linkBar.set(l, u)
}

// Children implements [gunim.Composite].
func (v *chatView) Children() []gunim.Node { return []gunim.Node{v.root} }

// Layout implements [gunim.Node].
func (v *chatView) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	kid.Layout(gunim.Tight(c.Max))
	kid.Place(geom.Point{})
	return c.Max
}

// Paint implements [gunim.Node].
func (v *chatView) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// Handle implements [gunim.Handler]: typing that nothing else takes, as after a click in a message, goes to the
// message box, and on a phone Escape goes back to the list.
func (v *chatView) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.TextInput, input.Composing:
		u.Focus(v.composer)
		return v.composer.Handle(e, u)
	case input.KeyPress:
		// On a phone, Escape, as the system's back gesture sends it, goes
		// back from the conversation to the list, where nothing else took it.
		if e.Key == input.KeyEscape && v.screens.narrow && v.screens.showing() {
			v.screens.show(false, u)
			return true
		}
	}
	return false
}

// panel fills its box with a colour behind its child, and is width wide when width is set.
type panel struct {
	child gunim.Node
	fill  theme.Token[color.NRGBA]
	width float32
}

// Children implements [gunim.Composite].
func (p *panel) Children() []gunim.Node { return []gunim.Node{p.child} }

// Layout implements [gunim.Node].
func (p *panel) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	if p.width > 0 {
		size.W = p.width
	}
	kid := kids.At(0)
	kid.Layout(gunim.Tight(size))
	kid.Place(geom.Point{})
	return size
}

// Paint implements [gunim.Node].
func (p *panel) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	pt.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(p.fill.Get(f.Theme)))
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

// linkBar is the strip that opens under the header while the connection is down.
type linkBar struct {
	anim.Group
	open *anim.Float
	link Link
	run  text.Run
	msg  string
}

func (b *linkBar) set(l Link, u *gunim.UI) {
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
func (b *linkBar) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	b.run = widget.Font.Get(f.Theme).Shape(b.msg, SmallText.Get(f.Theme))
	return geom.Sz(c.Max.W, 30*max(0, b.open.Value()))
}

// Paint implements [gunim.Node].
func (b *linkBar) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
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
