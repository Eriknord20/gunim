package main

import (
	"bytes"
	"context"
	"image/png"
	"log"
	"maps"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/markdown"
	"github.com/marrasen/gunim/paint"
)

// me is the user's name.
const me = "Marcus"

// app is the application half. It holds the conversations, and stands in for the server and the colleagues on the
// other end: messages take a while to arrive, some fail, none go while offline, and colleagues type, reply, edit
// and withdraw.
type app struct {
	ctx context.Context
	rng *rand.Rand
	// later carries work from timers back to the loop in serve, which owns the state.
	later chan func()

	// window is the main window, and windows every window open, the main one first.
	*window
	windows []*window
	// openWindow opens a window for a conversation popped out of the main one, titled title.
	openWindow func(title string) (gunim.Client, error)
	// in carries every window's intents to the loop in serve.
	in chan windowIntent

	projects []*project
	link     Link
	light    bool
	nextID   int
	// failRate is the share of sends the server turns down.
	failRate float64
	// images holds every picture pasted or fetched, by ID.
	images map[string]*paint.Image
	// web fetches link previews.
	web *http.Client
}

// window is one window of the chat: the conversation it shows, and what the user is writing there. The main
// window shows the projects and their conversations; a popped out one shows its conversation alone.
type window struct {
	c    gunim.Client
	solo bool
	// picks counts the conversations and areas picked from the list.
	picks int
	// project is the project whose conversations the main window lists.
	project int
	current *conv
	// replying and editing are the IDs of the message the next one replies to and the one being edited.
	replying, editing string
	// text is what the message box holds, and draft what the application last put there.
	text   string
	draft  Draft
	typing string
	// pending are the pictures waiting to go with the next message.
	pending []Picture
	// newFrom is the first message of the conversation not read when it opened, and unread how many messages
	// from others it had then.
	newFrom string
	unread  int
	// first is the first message of the conversation the window shows, which older ones load in before, and
	// loading says older ones are on their way.
	first   string
	loading bool
	// loads numbers the loads of older messages, so a load the window has moved on from does nothing.
	loads int
	// area is what the pane shows: "" for the conversation, or "files", and path the folder of files open.
	area string
	path string
	// selected is the entry of the folder open to select, such as a file opened from a message.
	selected string
}

// windowIntent is an intent from one of the windows, or word that the window closed.
type windowIntent struct {
	w      *window
	intent gunim.Intent
	closed bool
}

type project struct {
	Name, Short string
	convs       []*conv
	// files is the project's top folder, and transfers the files on their way up to it.
	files     *folder
	transfers []*transfer
}

type conv struct {
	ID, Name string
	Direct   bool
	people   []string
	// lastWrote is when each person last wrote, for their presence.
	lastWrote map[string]time.Time
	msgs      []*msg
	byID      map[string]*msg
	unread    int
	// readTo is the ID of the last message the user has seen, or "" for none.
	readTo  string
	project *project
}

// msg is a message as the application keeps it.
type msg struct {
	ID, Author string
	At         time.Time
	Body       string
	State      State
	Edited     bool
	Withdrawn  bool
	ReplyTo    string
	Pictures   []Picture
	// reactions are the emoji people reacted with, in the order they first came, each with who reacted.
	reactions []reaction
	// preview is the card for the first link in the text, once the sender's app has fetched it.
	preview Preview
	// poll is the message's poll, or nil.
	poll *poll
	// private says only the user sees the message, as the answer to a command.
	private bool
	// file is a file of the project's the message shares, or has no name.
	file FileRef
}

// commands are the commands the message box takes, for /help to list.
var commands = []struct{ usage, what string }{
	{"/poll Question | option | option", "starts a poll, with two options or more"},
	{"/help", "shows this list"},
}

// helpText is the list /help answers with, in Markdown.
func helpText() string {
	var b strings.Builder
	b.WriteString("**Commands**\n")
	for _, c := range commands {
		b.WriteString("\n- `" + c.usage + "` " + c.what)
	}
	return b.String()
}

// poll is a question, its options, and each voter's choice.
type poll struct {
	question string
	options  []string
	votes    map[string]int
	// order is the voters in the order they first voted.
	order []string
}

// newPoll reads a poll from text written as "/poll Question | option | option", and reports false for text that
// is not one, or has fewer than two options.
func newPoll(text string) (*poll, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(text), "/poll ")
	if !ok {
		return nil, false
	}
	var parts []string
	for _, s := range strings.Split(rest, "|") {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) < 3 {
		return nil, false
	}
	return &poll{question: parts[0], options: parts[1:], votes: map[string]int{}}, true
}

// vote gives who's vote to option i, or takes it back when it was there already.
func (p *poll) vote(who string, i int) {
	if i < 0 || i >= len(p.options) {
		return
	}
	if was, ok := p.votes[who]; ok && was == i {
		delete(p.votes, who)
		p.order = slices.DeleteFunc(p.order, func(s string) bool { return s == who })
		return
	}
	if _, ok := p.votes[who]; !ok {
		p.order = append(p.order, who)
	}
	p.votes[who] = i
}

// pollOf returns m's poll as the timeline shows it.
func pollOf(m *msg) Poll {
	if m.poll == nil {
		return Poll{}
	}
	out := Poll{Question: m.poll.question, Voters: len(m.poll.votes)}
	for i, text := range m.poll.options {
		o := PollOption{Text: text}
		var who []string
		for _, v := range m.poll.order {
			if m.poll.votes[v] != i {
				continue
			}
			o.Votes++
			if v == me {
				o.Mine = true
				v = "You"
			}
			who = append(who, v)
		}
		o.Who = strings.Join(who, ", ")
		out.Options = append(out.Options, o)
	}
	return out
}

// reaction is an emoji and the people who reacted with it, in order.
type reaction struct {
	emoji string
	who   []string
}

// toggle adds who's reaction of emoji to m, or takes it back when who had it.
func (m *msg) toggle(emoji, who string) {
	for i := range m.reactions {
		r := &m.reactions[i]
		if r.emoji != emoji {
			continue
		}
		if j := slices.Index(r.who, who); j >= 0 {
			r.who = slices.Delete(r.who, j, j+1)
			if len(r.who) == 0 {
				m.reactions = slices.Delete(m.reactions, i, i+1)
			}
			return
		}
		r.who = append(r.who, who)
		return
	}
	m.reactions = append(m.reactions, reaction{emoji: emoji, who: []string{who}})
}

// reactionsOf returns m's reactions as the timeline shows them.
func reactionsOf(m *msg) []Reaction {
	out := make([]Reaction, 0, len(m.reactions))
	for _, r := range m.reactions {
		names := make([]string, len(r.who))
		for i, w := range r.who {
			names[i] = w
			if w == me {
				names[i] = "You"
			}
		}
		out = append(out, Reaction{Emoji: r.emoji, Count: len(r.who), Mine: slices.Contains(r.who, me), Who: strings.Join(names, ", ")})
	}
	return out
}

// pollVote returns who's choice on m's poll, and whether they chose.
func (m *msg) pollVote(who string) (int, bool) {
	if m.poll == nil {
		return 0, false
	}
	i, ok := m.poll.votes[who]
	return i, ok
}

// reactionEmoji are the emoji the pretend colleagues react with.
var reactionEmoji = []string{"\U0001F44D", "\U0001F389", "\u2764\uFE0F", "\U0001F602", "\U0001F680", "\U0001F440", "\u2705"}

func newApp(ctx context.Context, c gunim.Client, seed uint64, history int, failRate float64) *app {
	main := &window{c: c}
	a := &app{ctx: ctx, rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), later: make(chan func(), 16),
		window: main, windows: []*window{main}, in: make(chan windowIntent, 16),
		failRate: failRate, images: map[string]*paint.Image{}, web: &http.Client{}}
	a.projects = []*project{
		a.newProject("Atlas", "AT",
			a.newConv("general", false, "Anna Berg", "Erik Lund", "Sara Nyström"),
			a.newConv("releases", false, "Anna Berg", "Erik Lund"),
			a.newConv("ci", false, "Erik Lund"),
			a.newConv("Anna Berg", true, "Anna Berg")),
		a.newProject("Beacon", "BC",
			a.newConv("general", false, "Sara Nyström", "Johan Ek"),
			a.newConv("training", false, "Johan Ek")),
		a.newProject("Cedar", "CD",
			a.newConv("general", false, "Lena Holm", "Anna Berg")),
	}
	now := time.Now().Round(0)
	for _, p := range a.projects {
		a.seedFiles(p)
	}
	for pi, p := range a.projects {
		for ci, cv := range p.convs {
			n := 30
			if pi == 0 && ci == 0 {
				n = history
			}
			a.fill(cv, n, now)
			a.leaveUnread(cv, a.rng.IntN(7))
		}
	}
	a.enter(main, a.projects[0].convs[0])
	return a
}

// leaveUnread marks all but the last n messages of c read, and counts those from others as unread.
func (a *app) leaveUnread(c *conv, n int) {
	c.readTo, c.unread = "", 0
	if i := len(c.msgs) - 1 - n; i >= 0 {
		c.readTo = c.msgs[i].ID
	}
	for _, m := range c.msgs[max(0, len(c.msgs)-n):] {
		if m.Author != me {
			c.unread++
		}
	}
}

// enter makes c the conversation w shows: the line over new messages goes over the first from someone else since
// the user last read it, and all of it is read from now on.
func (a *app) enter(w *window, c *conv) {
	w.current, w.newFrom, w.unread = c, "", c.unread
	past := c.readTo == ""
	for _, m := range c.msgs {
		if past && m.Author != me {
			w.newFrom = m.ID
			break
		}
		if m.ID == c.readTo {
			past = true
		}
	}
	if len(c.msgs) > 0 {
		c.readTo = c.msgs[len(c.msgs)-1].ID
	}
	c.unread = 0
	// The window opens on the latest messages, reaching back to the first not read.
	from := max(0, len(c.msgs)-pageSize)
	if i := slices.IndexFunc(c.msgs, func(m *msg) bool { return m.ID == w.newFrom }); i >= 0 {
		from = max(0, min(from, i-10))
	}
	w.first, w.loading = "", false
	w.loads++
	if len(c.msgs) > 0 {
		w.first = c.msgs[from].ID
	}
}

// pageSize is how many messages a conversation opens with, and how many more each load of older ones brings.
const pageSize = 60

// shownFrom returns the index in c of the first message w shows.
func (w *window) shownFrom(c *conv) int {
	return max(0, slices.IndexFunc(c.msgs, func(m *msg) bool { return m.ID == w.first }))
}

// loadOlder fetches the page of messages before the first w shows, taking a moment as a server would.
func (a *app) loadOlder(w *window) {
	c := w.current
	from := w.shownFrom(c)
	if w.loading || from == 0 {
		return
	}
	w.loading = true
	w.loads++
	load := w.loads
	a.after(a.between(300*time.Millisecond, 700*time.Millisecond), func() {
		if w.loads != load || w.current != c {
			return
		}
		w.loading = false
		// The first message shown only ever moves back.
		w.first = c.msgs[max(0, min(w.shownFrom(c), from)-pageSize)].ID
		a.publish()
	})
}

func (a *app) newProject(name, short string, convs ...*conv) *project {
	p := &project{Name: name, Short: short, convs: convs}
	for _, c := range convs {
		c.project = p
	}
	return p
}

func (a *app) newConv(name string, direct bool, people ...string) *conv {
	a.nextID++
	return &conv{ID: "c" + strconv.Itoa(a.nextID), Name: name, Direct: direct, people: people, byID: map[string]*msg{}}
}

// fill gives c n messages of history, the latest a few minutes before now.
func (a *app) fill(c *conv, n int, now time.Time) {
	times := make([]time.Time, n)
	at := now.Add(-4 * time.Minute)
	for i := n - 1; i >= 0; i-- {
		times[i] = at
		at = at.Add(-time.Duration(a.rng.IntN(48)+1) * time.Minute)
	}
	for _, at := range times {
		author := me
		if a.rng.Float64() < 0.7 {
			author = a.pick(c.people)
		}
		m := a.add(c, author, a.pick(chatter), at)
		if len(c.msgs) > 3 && a.rng.Float64() < 0.12 {
			m.ReplyTo = c.msgs[len(c.msgs)-2-a.rng.IntN(2)].ID
		}
		if a.rng.Float64() < 0.04 {
			m.Edited = true
		}
		if a.rng.Float64() < 0.01 {
			m.Withdrawn = true
		}
	}
}

// add appends a message to c and returns it. Text written as "/poll Question | option | option" makes a poll.
func (a *app) add(c *conv, author, body string, at time.Time) *msg {
	a.nextID++
	m := &msg{ID: "m" + strconv.Itoa(a.nextID), Author: author, At: at, Body: body}
	if pl, ok := newPoll(body); ok {
		m.Body, m.poll = "", pl
	}
	c.msgs = append(c.msgs, m)
	c.byID[m.ID] = m
	if at.After(c.lastWrote[author]) {
		if c.lastWrote == nil {
			c.lastWrote = map[string]time.Time{}
		}
		c.lastWrote[author] = at
	}
	return m
}

func (a *app) pick(s []string) string { return s[a.rng.IntN(len(s))] }

// between returns a random duration from lo to hi.
func (a *app) between(lo, hi time.Duration) time.Duration {
	return lo + time.Duration(a.rng.Int64N(int64(hi-lo)))
}

// after runs fn on the loop in serve after d.
func (a *app) after(d time.Duration, fn func()) {
	time.AfterFunc(d, func() {
		select {
		case a.later <- fn:
		case <-a.ctx.Done():
		}
	})
}

// serve runs the application until the context ends or the main window closes.
func (a *app) serve() error {
	if err := a.c.Mount(gunim.Root, "chat", "chat", a.stateOf(a.window)); err != nil {
		return err
	}
	a.listen(a.window)
	a.after(a.between(3*time.Second, 6*time.Second), a.colleague)
	a.after(time.Minute, a.presenceTick)
	for {
		select {
		case <-a.ctx.Done():
			return nil
		case fn := <-a.later:
			fn()
		case wi := <-a.in:
			if wi.closed {
				if wi.w == a.window {
					return a.c.Err()
				}
				a.windows = slices.DeleteFunc(a.windows, func(w *window) bool { return w == wi.w })
				continue
			}
			a.handleIn(wi.w, wi.intent)
		}
	}
}

// presenceTick publishes the state once a minute, so a person who has been quiet stops showing as active.
func (a *app) presenceTick() {
	a.publish()
	a.after(time.Minute, a.presenceTick)
}

// listen passes w's intents on to the loop in serve, and says when w closes.
func (a *app) listen(w *window) {
	go func() {
		for ev := range w.c.Intents() {
			select {
			case a.in <- windowIntent{w: w, intent: ev.Intent}:
			case <-a.ctx.Done():
				return
			}
		}
		select {
		case a.in <- windowIntent{w: w, closed: true}:
		case <-a.ctx.Done():
		}
	}()
}

// handle acts on an intent from the main window.
func (a *app) handle(v gunim.Intent) { a.handleIn(a.window, v) }

// handleIn acts on an intent from w.
func (a *app) handleIn(w *window, v gunim.Intent) {
	switch v := v.(type) {
	case ProjectChosen:
		if v.Index >= 0 && v.Index < len(a.projects) {
			w.project, w.path = v.Index, ""
			a.open(w, a.projects[v.Index].convs[0])
		}
	case ConversationChosen:
		w.picks++
		for _, c := range a.projects[w.project].convs {
			if c.ID == v.ID {
				w.area = ""
				a.open(w, c)
			}
		}
	case AreaChosen:
		w.picks++
		if v.Area == "files" && !w.solo {
			w.area = v.Area
		}
	case FolderOpened:
		if _, ok := a.projects[w.project].folderAt(v.Path); ok {
			w.path, w.selected = v.Path, ""
		}
	case FileShared:
		a.share(w, v)
	case FileOpened:
		if _, ok := a.projects[w.project].folderAt(v.Folder); ok {
			w.area, w.path, w.selected = "files", v.Folder, v.Name
		}
	case FilesDropped:
		a.upload(a.projects[w.project], v.Folder, v.Paths)
	case TransferRetried:
		p := a.projects[w.project]
		if i := slices.IndexFunc(p.transfers, func(t *transfer) bool { return t.id == v.ID }); i >= 0 &&
			p.transfers[i].state == TransferFailed && p.transfers[i].size > 0 {
			a.startTransfer(p, p.transfers[i])
		}
	case TransferDismissed:
		p := a.projects[w.project]
		p.transfers = slices.DeleteFunc(p.transfers, func(t *transfer) bool {
			return t.id == v.ID && t.state == TransferFailed
		})
	case OlderAsked:
		a.loadOlder(w)
	case PopOut:
		a.popOut(w.current)
		return
	case Drafted:
		w.text = v.Text
		return
	case ImagePasted:
		a.pastePicture(w, v.PNG)
	case PollVoted:
		if m, ok := w.current.byID[v.ID]; ok && m.poll != nil && !m.Withdrawn {
			m.poll.vote(me, v.Option)
		}
	case ReactionToggled:
		if m, ok := w.current.byID[v.ID]; ok && !m.Withdrawn {
			m.toggle(v.Emoji, me)
		}
	case PictureRemoved:
		w.pending = slices.DeleteFunc(w.pending, func(p Picture) bool { return p.ID == v.ID })
	case Submitted:
		a.submit(w, v.Text)
	case ReplyAsked:
		w.replying, w.editing = v.ID, ""
		w.setDraft(w.text)
	case EditAsked:
		if m, ok := w.current.byID[v.ID]; ok && m.Author == me && !m.Withdrawn {
			w.editing, w.replying = v.ID, ""
			w.setDraft(m.Body)
		}
	case WithdrawAsked:
		if m, ok := w.current.byID[v.ID]; ok && m.Author == me {
			m.Withdrawn = true
		}
	case RetryAsked:
		if m, ok := w.current.byID[v.ID]; ok && m.State == Failed {
			a.send(m)
		}
	case Cancelled:
		if w.editing != "" {
			w.setDraft("")
		}
		w.replying, w.editing = "", ""
	case LinkToggled:
		a.toggleLink()
	case ThemeToggled:
		a.light = !a.light
		for _, o := range a.windows {
			if err := o.c.SetTheme(themeName(a.light)); err != nil {
				log.Print(err)
			}
		}
		return
	case markdown.Link:
		a.openLink(w, v.URL)
		return
	case gunim.CommandFailed:
		log.Printf("command %s failed: %s", v.Command, v.Reason)
		return
	default:
		return
	}
	a.publish()
}

// themeName names the theme to show.
func themeName(light bool) string { return map[bool]string{false: "dark", true: "light"}[light] }

// popOut opens c in a window of its own, beside the main one.
func (a *app) popOut(c *conv) {
	if a.openWindow == nil {
		return
	}
	title := c.Name
	if !c.Direct {
		title = "#" + c.Name
	}
	cl, err := a.openWindow(title)
	if err != nil {
		log.Printf("popping out %s: %v", title, err)
		return
	}
	w := &window{c: cl, solo: true}
	a.enter(w, c)
	if a.light {
		if err := cl.SetTheme(themeName(true)); err != nil {
			log.Print(err)
		}
	}
	if err := cl.Mount(gunim.Root, "chat", "chat", a.stateOf(w)); err != nil {
		log.Printf("popping out %s: %v", title, err)
		cl.Close()
		return
	}
	a.windows = append(a.windows, w)
	a.listen(w)
}

// openLink opens a web or mail address from a message in the system's browser or mail program. Anything else,
// such as a path to a file, stays closed: messages come from other people.
func (a *app) openLink(w *window, url string) {
	lower := strings.ToLower(url)
	if !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "mailto:") {
		log.Printf("not opening %q: only web and mail addresses open", url)
		return
	}
	if err := w.c.Open(url); err != nil {
		log.Printf("opening %s: %v", url, err)
	}
}

// post runs fn on the loop in serve, as soon as the loop gets to it.
func (a *app) post(fn func()) {
	select {
	case a.later <- fn:
	case <-a.ctx.Done():
	}
}

// fetchPreview fetches the card for the first link in m, as the sender's app does, and puts it in m when it comes.
// A link with no card to make, or a fetch that fails, leaves m without one.
func (a *app) fetchPreview(m *msg) {
	link := firstLink(m.Body)
	if link == "" || a.link != Online {
		return
	}
	web := a.web
	go func() {
		p, err := fetchPage(a.ctx, web, link)
		if err != nil {
			log.Printf("preview of %s: %v", link, err)
			return
		}
		a.post(func() {
			pv := Preview{URL: p.url, Site: p.site, Title: p.title, Description: p.description}
			if p.picture != nil {
				a.nextID++
				pv.Picture = "p" + strconv.Itoa(a.nextID)
				a.images[pv.Picture] = paint.NewImage(p.picture)
			}
			m.preview = pv
			a.publish()
		})
	}()
}

// pastePicture puts a pasted picture, as PNG, with the pictures waiting to go with w's next message.
func (a *app) pastePicture(w *window, b []byte) {
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		log.Printf("pasted picture: %v", err)
		return
	}
	a.nextID++
	id := "p" + strconv.Itoa(a.nextID)
	a.images[id] = paint.NewImage(img)
	size := img.Bounds().Size()
	w.pending = append(w.pending, Picture{ID: id, W: size.X, H: size.Y})
}

// open makes c the conversation w shows.
func (a *app) open(w *window, c *conv) {
	if c == w.current {
		return
	}
	a.enter(w, c)
	w.replying, w.editing = "", ""
	w.setTyping("")
	w.setDraft("")
}

// setDraft puts s in w's message box.
func (w *window) setDraft(s string) {
	w.text = s
	w.draft = Draft{Text: s, Seq: w.draft.Seq + 1}
}

// submit sends w's message box's text as a new message, a reply or an edit.
func (a *app) submit(w *window, text string) {
	text = strings.TrimSpace(text)
	c := w.current
	if w.editing != "" {
		if text == "" {
			return
		}
		if m, ok := c.byID[w.editing]; ok && m.Body != text {
			m.Body, m.Edited = text, true
		}
		w.editing = ""
		w.setDraft("")
		return
	}
	if text == "" && len(w.pending) == 0 {
		return
	}
	if text == "/help" {
		h := a.add(c, "Help", helpText(), time.Now().Round(0))
		h.private, c.readTo = true, h.ID
		w.setDraft("")
		return
	}
	m := a.add(c, me, text, time.Now().Round(0))
	c.readTo = m.ID
	m.ReplyTo, w.replying = w.replying, ""
	m.Pictures, w.pending = w.pending, nil
	w.setDraft("")
	a.send(m)
	a.fetchPreview(m)
	// Somebody usually answers.
	if a.rng.Float64() < 0.6 {
		a.after(a.between(700*time.Millisecond, 2*time.Second), func() {
			a.say(c, a.pick(c.people), a.pick(answers), m.ID)
		})
	}
}

// send hands one of the user's messages to the server, which takes it after a while, or turns it down. While
// offline it waits, and goes when the connection is back.
func (a *app) send(m *msg) {
	m.State = Pending
	if a.link != Online {
		return
	}
	a.after(a.between(150*time.Millisecond, 900*time.Millisecond), func() {
		if a.link != Online || m.State != Pending {
			return // the connection dropped with the outcome unknown: it goes again on reconnecting
		}
		m.State = Sent
		if a.rng.Float64() < a.failRate {
			m.State = Failed
		}
		a.publish()
	})
}

// toggleLink drops the connection, or starts connecting again and sends what waited.
func (a *app) toggleLink() {
	if a.link == Online {
		a.link = Offline
		for _, w := range a.windows {
			w.setTyping("")
		}
		return
	}
	a.link = Reconnecting
	a.after(a.between(800*time.Millisecond, 1600*time.Millisecond), func() {
		a.link = Online
		a.resumeTransfers()
		for _, p := range a.projects {
			for _, c := range p.convs {
				for _, m := range c.msgs {
					if m.State == Pending {
						a.send(m)
					}
				}
			}
		}
		a.publish()
	})
}

// colleague has somebody do something, and comes back after a while to do the next thing.
func (a *app) colleague() {
	defer a.after(a.between(4*time.Second, 11*time.Second), a.colleague)
	if a.link != Online {
		return
	}
	c := a.current
	who := a.pick(c.people)
	switch r := a.rng.Float64(); {
	case r < 0.45:
		a.say(c, who, a.pick(chatter), "")
	case r < 0.56:
		if m := a.lastBy(c, func(m *msg) bool { return m.Author != who }); m != nil {
			a.say(c, who, a.pick(answers), m.ID)
		}
	case r < 0.63:
		// A colleague edits a message once, adding a paragraph.
		if m := a.lastBy(c, func(m *msg) bool { return m.Author == who && !m.Edited }); m != nil {
			m.Body += "\n\n" + a.pick(afterthoughts)
			m.Edited = true
			a.publish()
		}
	case r < 0.7:
		// Somebody votes on a poll they have not voted on.
		if m := a.lastBy(c, func(m *msg) bool { _, voted := m.pollVote(who); return m.poll != nil && !voted }); m != nil {
			m.poll.vote(who, a.rng.IntN(len(m.poll.options)))
			a.publish()
			return
		}
		fallthrough
	case r < 0.83:
		// Somebody reacts, mostly to one of the user's messages.
		m := a.lastBy(c, func(m *msg) bool { return m.Author == me })
		if m == nil || a.rng.Float64() < 0.3 {
			m = a.lastBy(c, func(m *msg) bool { return m.Author != who })
		}
		if m != nil {
			m.toggle(a.pick(reactionEmoji), who)
			a.publish()
		}
	case r < 0.86:
		if m := a.lastBy(c, func(m *msg) bool { return m.Author == who }); m != nil {
			m.Withdrawn = true
			a.publish()
		}
	default:
		// Somewhere else: the message waits unread.
		p := a.projects[a.rng.IntN(len(a.projects))]
		other := p.convs[a.rng.IntN(len(p.convs))]
		if a.shown(other) {
			return
		}
		a.add(other, a.pick(other.people), a.pick(chatter), time.Now().Round(0))
		other.unread++
		a.publish()
	}
}

// lastBy returns the latest message in c, of the last ten, that keep accepts, that is still there, and that others
// can see.
func (a *app) lastBy(c *conv, keep func(*msg) bool) *msg {
	for i := len(c.msgs) - 1; i >= 0 && i >= len(c.msgs)-10; i-- {
		if m := c.msgs[i]; !m.Withdrawn && !m.private && keep(m) {
			return m
		}
	}
	return nil
}

// say has who type for a moment in c, then send body, replying to replyTo when it is set.
func (a *app) say(c *conv, who, body, replyTo string) {
	if a.link != Online {
		return // nobody's typing reaches a user who is offline
	}
	a.typingIn(c, who)
	a.after(a.between(1200*time.Millisecond, 3500*time.Millisecond), func() {
		a.typingIn(c, "")
		if a.link != Online {
			return
		}
		m := a.add(c, who, body, time.Now().Round(0))
		if a.shown(c) {
			c.readTo = m.ID
		} else {
			c.unread++
		}
		m.ReplyTo = replyTo
		a.publish()
		// The colleague's own app fetches the card for a link they send.
		a.fetchPreview(m)
	})
}

// shown reports whether a window shows c, so the user sees what comes in it.
func (a *app) shown(c *conv) bool {
	return slices.ContainsFunc(a.windows, func(w *window) bool { return w.current == c })
}

// typingIn shows who is typing in every window that shows c.
func (a *app) typingIn(c *conv, who string) {
	for _, w := range a.windows {
		if w.current == c {
			w.setTyping(who)
		}
	}
}

// setTyping shows who is typing in w's conversation.
func (w *window) setTyping(who string) {
	if who == w.typing {
		return
	}
	w.typing = who
	if err := w.c.Patch("chat", Typing{Who: who}); err != nil {
		log.Print(err)
	}
}

// publish sends each window the state as it is now.
func (a *app) publish() {
	for _, w := range a.windows {
		if err := w.c.Update("chat", a.stateOf(w)); err != nil {
			log.Print(err)
		}
	}
}

// state returns what the main window shows.
func (a *app) state() Chat { return a.stateOf(a.window) }

// stateOf returns what w shows, in values of its own.
func (a *app) stateOf(w *window) Chat {
	s := Chat{Solo: w.solo, Project: w.project, Current: w.current.ID, Title: w.current.Name, Link: a.link,
		Editing: w.editing, Draft: w.draft, Picks: w.picks}
	for _, p := range a.projects {
		unread := 0
		for _, c := range p.convs {
			unread += c.unread
		}
		s.Projects = append(s.Projects, Project{Name: p.Name, Short: p.Short, Unread: unread})
	}
	if !w.solo {
		s.Conversations = append(s.Conversations, Conversation{ID: "area:files", Name: "Files", Area: "files"})
	}
	for _, c := range a.projects[w.project].convs {
		s.Conversations = append(s.Conversations, Conversation{ID: c.ID, Name: c.Name, Direct: c.Direct, Unread: c.unread})
	}
	if w.area == "files" {
		p := a.projects[w.project]
		f, ok := p.folderAt(w.path)
		if !ok {
			// The folder open went away: back to the top.
			w.path, f = "", p.files
		}
		s.Area, s.Files = w.area, Files{Path: w.path, Entries: entriesOf(f), Transfers: transfersOf(p),
			Selected: w.selected}
	}
	if m, ok := w.current.byID[w.replying]; ok {
		s.Replying = quote(m)
	}
	s.People = w.current.people
	s.Members = a.membersOf(w.current, time.Now())
	from := w.shownFrom(w.current)
	s.Items = timeline(w.current, time.Now(), w.newFrom, from)
	s.Loading = w.loading
	if w.newFrom != "" {
		s.NewKey, s.Unread = newKey(w.current), w.unread
	}
	s.Pending, s.Images = slices.Clone(w.pending), maps.Clone(a.images)
	return s
}

// groupFor is how long a message shares the heading of the one that started its group, by the same author.
const groupFor = 5 * time.Minute

// newKey is the key of the line over c's new messages.
func newKey(c *conv) string { return "new:" + c.ID }

// timeline returns c's messages as the timeline's rows, with a heading for each day. A message shares the heading
// of the one before when both are by the same author and its group started under groupFor ago, so even a steady
// stream from one person shows a heading every few minutes. The line over new messages goes before newFrom.
func timeline(c *conv, now time.Time, newFrom string, from int) []Item {
	out := make([]Item, 0, len(c.msgs)-from+(len(c.msgs)-from)/20)
	var prev *msg
	var groupAt time.Time
	day := ""
	for _, m := range c.msgs[from:] {
		if d := m.At.Format(time.DateOnly); d != day {
			day, prev = d, nil
			// Keyed by the day's first message shown, so the heading that older messages push up is a new one, and
			// the timeline holds the message the user was reading still.
			out = append(out, Item{Key: "day:" + m.ID, Day: dayName(m.At, now)})
		}
		if m.ID == newFrom {
			out = append(out, Item{Key: newKey(c), New: true})
			prev = nil
		}
		it := Item{Key: m.ID, Message: Message{ID: m.ID, Author: m.Author, Mine: m.Author == me, At: m.At, Body: m.Body,
			State: m.State, Edited: m.Edited, Withdrawn: m.Withdrawn, Pictures: slices.Clone(m.Pictures),
			Reactions: reactionsOf(m), Preview: m.preview, Poll: pollOf(m), Private: m.private, File: m.file}}
		it.Continued = prev != nil && prev.Author == m.Author && m.At.Sub(groupAt) < groupFor && m.ReplyTo == "" &&
			!m.private
		if !it.Continued {
			groupAt = m.At
		}
		if q, ok := c.byID[m.ReplyTo]; ok {
			it.Reply = quote(q)
		}
		out = append(out, it)
		prev = m
	}
	return out
}

func quote(m *msg) Quote {
	text := m.Body
	if text == "" && m.file.Name != "" {
		text = "File: " + m.file.Name
	}
	return Quote{ID: m.ID, Author: m.Author, Text: text, Gone: m.Withdrawn}
}

// dayName names the day of t: today, yesterday, or its weekday and date.
func dayName(t, now time.Time) string {
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "Today"
	case t.Add(24*time.Hour).Format(time.DateOnly) == now.Format(time.DateOnly):
		return "Yesterday"
	}
	return t.Format("Monday 2 January")
}

var chatter = []string{
	"The build runner is pinned to 17.4 and the server is on 17.6. Bump it after the release?",
	"Pushed a fix for the **thumbnail cache**. Can someone look at `thumbs.go`?",
	"Lunch at 12?",
	"The nightly build failed again. Looks like the Windows runner ran out of disk.",
	"I'll take the review.\nTwo small things in the diff, otherwise fine.",
	"Does anyone know why the Linux test is flaky? It passes every time on my machine.",
	"Merged 👍",
	"Release checklist:\n- [ ] update the changelog\n- [ ] tag v5.3.0\n- [ ] tell the customers",
	"Review please: https://example.com/merge_requests/412",
	"Meeting moved to 14:00.",
	"Can we talk about the sync protocol tomorrow? I have some thoughts on the event IDs.",
	"Sounds good.",
	"The customer in Lund says the export is slow on their big datasets. I measured it: most of the time goes to " +
		"sorting the rows before we write them, which we could skip when the view is already in order. I'll try " +
		"that and see how far it gets us.",
	"> the export is slow\nIs that the CSV one or the report?",
	"```\ngo test ./... -run TestSync -count 50\n```\nfails about one time in twenty for me.",
	"Back in an hour.",
	"Thanks!",
	"/poll Where do we go for the team lunch on Friday? | Thai | Pizza | Sushi | Something new",
	"Good read on how range over functions works: https://go.dev/blog/range-functions",
	"Build times this week:\n\n| Runner | Mon | Fri |\n|:--|--:|--:|\n| Windows | 14 min | 9 min |\n| Linux | 6 min | 6 min |",
	"The flaky one:\n```\nwidget/virtual_test.go:377: offset 4712, want the end at 6392 (TestVirtualListThatSticksFollowsNewRows, seed 1817263)\n```",
}

var answers = []string{
	"Good point.",
	"Agreed, let's do that.",
	"Hmm, not sure. Let me check.",
	"On it.",
	"Can you say more about that?",
	"Yes, after lunch.",
	"I think Erik knows more about that one.",
}

var afterthoughts = []string{
	"Edit: make that 15:00.",
	"(Fixed the link.)",
	"Actually, never mind the second point.",
}
