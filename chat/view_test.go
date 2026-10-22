package chat

import (
	"image/color"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// now is when the tests' conversations are read.
var now = time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)

// harness shows a conversation in a view in an offscreen window, as a host would, and keeps the intents the view
// sends.
type harness struct {
	t *testing.T
	w *gunim.Window
	v *View
	// c is the conversation shown, but for its items, which msgs and newFrom make.
	c       Conversation
	msgs    []Message
	newFrom string
	intents []gunim.Intent
}

// grab is a patch that hands the test the view.
type grab struct{}

// onUI is a patch that runs a function with the window's UI.
type onUI struct{ fn func(u *gunim.UI) }

// history returns n messages, seven minutes apart and ending an hour before now, from two colleagues and the
// user in turn.
func history(n int) []Message {
	authors := []string{"Anna Berg", "Erik Lund", "Marcus"}
	out := make([]Message, n)
	for i := range out {
		a := authors[i%len(authors)]
		out[i] = Message{ID: "m" + strconv.Itoa(i), Author: a, Mine: a == "Marcus", Body: "Message " + strconv.Itoa(i),
			At: now.Add(-time.Hour - time.Duration(n-i)*7*time.Minute)}
	}
	return out
}

func newHarness(t *testing.T, o Options, msgs []Message) *harness {
	t.Helper()
	h := &harness{t: t, w: gunimtest.New(t, geom.Sz(800, 600), widget.NewSurface()), msgs: msgs,
		c: Conversation{ID: "c1", Title: "general", People: []string{"Anna Berg", "Erik Lund"}}}
	gunim.RegisterView(h.w, "chat", func(Conversation) *View { return NewView(o) }, (*View).Set)
	gunim.RegisterPatch(h.w, "chat", func(v *View, _ grab, _ *gunim.UI) { h.v = v })
	gunim.RegisterPatch(h.w, "chat", func(_ *View, p onUI, u *gunim.UI) { p.fn(u) })
	if err := h.w.Client().Mount(gunim.Root, "chat", "chat", h.state()); err != nil {
		t.Fatal(err)
	}
	if err := h.w.Client().Patch("chat", grab{}); err != nil {
		t.Fatal(err)
	}
	h.frames(30)
	return h
}

// state returns the conversation as the view is to show it.
func (h *harness) state() Conversation {
	c := h.c
	c.Items = Timeline(h.msgs, now, h.newFrom)
	return c
}

// publish hands the view the conversation as it is now, and draws a few frames.
func (h *harness) publish() {
	h.t.Helper()
	if err := h.w.Client().Update("chat", h.state()); err != nil {
		h.t.Fatal(err)
	}
	h.frames(5)
}

// do runs fn with the window's UI, and draws a couple of frames.
func (h *harness) do(fn func(u *gunim.UI)) {
	h.t.Helper()
	if err := h.w.Client().Patch("chat", onUI{fn}); err != nil {
		h.t.Fatal(err)
	}
	h.frames(2)
}

// frames draws n frames, keeping the intents that come meanwhile.
func (h *harness) frames(n int) {
	for range n {
		h.w.Frame(time.Second / 60)
		for more := true; more; {
			select {
			case ev := <-h.w.Client().Intents():
				h.intents = append(h.intents, ev.Intent)
			default:
				more = false
			}
		}
	}
}

// row returns the built row of the message id.
func (h *harness) row(id string) *msgRow {
	h.t.Helper()
	n, ok := h.v.list.Row(widget.Key(id))
	if !ok {
		h.t.Fatalf("message %s is not built", id)
	}
	return n.(*msgRow)
}

// add appends a message from who, a minute after the last, and returns its ID.
func (h *harness) add(who, body string) string {
	at := now.Add(-time.Hour)
	if n := len(h.msgs); n > 0 {
		at = h.msgs[n-1].At.Add(time.Minute)
	}
	id := "m" + strconv.Itoa(len(h.msgs))
	h.msgs = append(h.msgs, Message{ID: id, Author: who, Mine: who == "Marcus", Body: body, At: at})
	return id
}

func TestShiftEnterStartsALineInTheMessageBox(t *testing.T) {
	h := newHarness(t, Options{}, history(20))
	h.w.Input(input.TextInput{Text: "one"})
	h.w.Input(input.KeyPress{Key: input.KeyEnter, Mods: input.ModShift})
	h.w.Input(input.TextInput{Text: "two"})
	h.frames(2)
	if got := h.v.composer.Text(); got != "one\ntwo" {
		t.Fatalf("message box holds %q, want two lines", got)
	}
	h.w.Input(input.KeyPress{Key: input.KeyEnter})
	h.frames(2)
	if i := slices.Index(h.intents, gunim.Intent(Submitted{Text: "one\ntwo"})); i < 0 {
		t.Fatalf("Enter sent %v, want the text submitted", h.intents)
	}
}

func TestTheUsersOwnMessageBringsTheTimelineToItsEnd(t *testing.T) {
	h := newHarness(t, Options{}, history(200))
	h.w.Input(input.TextInput{Text: "Hello there"})
	h.v.list.ScrollTo(0, widget.Quick.Default())
	h.frames(60)
	h.add("Marcus", "Hello there")
	h.msgs[len(h.msgs)-1].State = Pending
	h.c.Draft = Draft{Seq: 1}
	h.publish()
	h.frames(60)
	if !h.v.list.AtEnd() {
		t.Fatal("the timeline stayed where it was")
	}
	if got := h.v.composer.Text(); got != "" {
		t.Fatalf("message box holds %q after the draft was emptied", got)
	}
}

func TestAnotherConversationOpensAtItsEnd(t *testing.T) {
	h := newHarness(t, Options{}, history(200))
	before := h.v.list
	h.c.ID, h.msgs = "c2", history(150)
	h.publish()
	h.frames(30)
	if h.v.list == before {
		t.Fatal("the timeline was not replaced")
	}
	if !h.v.list.AtEnd() {
		t.Fatal("the new timeline is not at its end")
	}
	if h.v.list.Offset() <= 0 {
		t.Fatalf("offset %v, want the timeline scrolled to its end", h.v.list.Offset())
	}
}

func TestAMessageKeepsItsHeightAsItArrives(t *testing.T) {
	h := newHarness(t, Options{}, history(20))
	first, second := h.add("Marcus", "With a heading"), h.add("Marcus", "Sharing it")
	h.msgs[len(h.msgs)-2].State, h.msgs[len(h.msgs)-1].State = Pending, Pending
	h.publish()
	if !h.row(second).item.Continued {
		t.Fatal("the second message has a heading of its own")
	}
	headed, shared := h.row(first).height, h.row(second).height
	h.msgs[len(h.msgs)-2].State, h.msgs[len(h.msgs)-1].State = Sent, Sent
	h.publish()
	if got := h.row(first).height; got != headed {
		t.Fatalf("the message with a heading went from %v to %v tall as it arrived", headed, got)
	}
	if got := h.row(second).height; got != shared {
		t.Fatalf("the message sharing a heading went from %v to %v tall as it arrived", shared, got)
	}
}

func TestSwitchingConversationCrossfades(t *testing.T) {
	h := newHarness(t, Options{}, history(20))
	old := h.v.timeline.child.(*fader)
	h.c.ID = "c2"
	if err := h.w.Client().Update("chat", h.state()); err != nil {
		t.Fatal(err)
	}
	h.frames(4)
	next := h.v.timeline.child.(*fader)
	if next == old {
		t.Fatal("the timeline was not swapped")
	}
	if o, n := old.in.Value(), next.in.Value(); o <= 0 || o >= 1 || n <= 0 || n >= 1 {
		t.Fatalf("part way through, the old timeline is at %v and the new at %v; want both between 0 and 1", o, n)
	}
	h.frames(90)
	if o, n := old.in.Value(), next.in.Value(); o > 0.01 || n < 0.99 {
		t.Fatalf("after the switch, the old timeline is at %v and the new at %v", o, n)
	}
}

func TestTypingAfterAClickInAMessageGoesToTheMessageBox(t *testing.T) {
	h := newHarness(t, Options{}, history(20))
	body := h.row(string(h.v.last)).body
	h.do(func(u *gunim.UI) { u.Focus(body) })
	h.w.Input(input.TextInput{Text: "hi"})
	h.frames(2)
	if got := h.v.composer.Text(); got != "hi" {
		t.Fatalf("message box holds %q after typing, want hi", got)
	}
}

func TestASelectionRunsAcrossTheTimelinesMessages(t *testing.T) {
	h := newHarness(t, Options{}, history(20))
	var first, last Item
	for _, it := range h.state().Items {
		if it.Day != "" {
			continue
		}
		if first.Key == "" {
			first = it
		}
		last = it
	}
	keys := h.v.group.Keys(first.Key, last.Key)
	if keys[0] != first.Key || keys[len(keys)-1] != last.Key {
		t.Fatalf("keys run from %s to %s, want %s to %s", keys[0], keys[len(keys)-1], first.Key, last.Key)
	}
	for _, k := range keys {
		if h.v.items[widget.Key(k)].Day != "" {
			t.Fatalf("the selection's keys hold the day heading %s", k)
		}
	}
	if body := h.row(string(h.v.last)).body; body.Group != h.v.group || body.Key != string(h.v.last) {
		t.Fatal("the last message is not in the timeline's group")
	}
}

func TestAPrivateMessageSaysOnlyTheUserSeesIt(t *testing.T) {
	if got := statusText(Message{Author: "Help", Private: true}); got != "Only visible to you" {
		t.Fatalf("a private message's status says %q", got)
	}
}

func TestAMessageOpensAMenuOfWhatItsToolbarDoes(t *testing.T) {
	h := newHarness(t, Options{}, history(20))
	id := h.add("Marcus", "Mine to change")
	h.publish()
	r := h.row(id)
	// A right click, or a finger held on the message, as the engine
	// sends it.
	h.do(func(u *gunim.UI) {
		r.Handle(input.PointerDown{Pos: geom.Pt(200, 20), Button: input.ButtonSecondary, Clicks: 1, Touch: true}, u)
	})
	if r.menuList == nil {
		t.Fatal("no menu opened")
	}
	items := make([]string, 0, len(r.menuList.Items()))
	for _, it := range r.menuList.Items() {
		items = append(items, it.Label)
	}
	if want := []string{"React", "Reply", "Edit", "Withdraw"}; !slices.Equal(items, want) {
		t.Fatalf("the menu holds %q, want %q", items, want)
	}
	h.do(func(u *gunim.UI) { r.menuList.OnPick(1, u) })
	h.frames(5)
	if r.menu != nil {
		t.Fatal("the menu stayed open after a pick")
	}
	if !slices.Contains(h.intents, gunim.Intent(ReplyAsked{ID: id})) {
		t.Fatalf("Reply sent %v, want a reply asked for %s", h.intents, id)
	}
}

func TestTheMembersSlideInFromTheHeader(t *testing.T) {
	h := newHarness(t, Options{Header: true, Members: true}, history(20))
	h.c.Members = []Member{{Name: "Marcus", Status: "You", Active: true, You: true}, {Name: "Anna Berg", Status: "Away"}}
	h.publish()
	if h.v.drawer == nil || h.v.membersButton == nil {
		t.Fatal("the view has no members to slide in")
	}
	if got := h.v.members.title.Text; got != "Members · 2" {
		t.Fatalf("the members' title says %q", got)
	}
	h.do(func(u *gunim.UI) { h.v.membersButton.OnClick(u) })
	if !h.v.drawer.Open() || !h.v.membersButton.Active {
		t.Fatal("the button did not slide the members in")
	}
}

func TestAViewWithoutAHeaderHasNoMembers(t *testing.T) {
	h := newHarness(t, Options{Members: true, LinkBar: true}, history(20))
	if h.v.drawer != nil || h.v.membersButton != nil {
		t.Fatal("a view without a header has members, with no button to show them")
	}
	h.c.Link = Offline
	h.publish()
	if h.v.linkBar.open.Target() != 1 {
		t.Fatal("the bar did not open while offline")
	}
}

func TestThePlaceholderNamesTheConversation(t *testing.T) {
	h := newHarness(t, Options{}, history(3))
	if got := h.v.composer.Placeholder; got != "Message #general" {
		t.Fatalf("placeholder %q", got)
	}
	h.c.Title, h.c.Direct = "Anna Berg", true
	h.publish()
	if got := h.v.composer.Placeholder; got != "Message Anna Berg" {
		t.Fatalf("placeholder %q for a conversation with a person", got)
	}
	o := newHarness(t, Options{Placeholder: "Say something"}, history(3))
	if got := o.v.composer.Placeholder; got != "Say something" {
		t.Fatalf("placeholder %q, want the options'", got)
	}
}

func TestExtraTriggersCompleteAfterThePeople(t *testing.T) {
	bars := func(trigger rune, query string) []widget.Completion {
		if trigger != '@' || query == "" || query[0] < '0' || query[0] > '9' {
			return nil
		}
		return []widget.Completion{{Text: "@" + query, Label: "Bar " + query}}
	}
	v := NewView(Options{Triggers: "#", Complete: bars})
	v.people = []string{"Anna Berg", "Erik Lund"}
	if v.composer.Triggers != "@:#" {
		t.Fatalf("triggers %q, want the options' after the view's own", v.composer.Triggers)
	}
	if got := v.complete('@', "81"); len(got) != 1 || got[0].Label != "Bar 81" {
		t.Fatalf("@81 suggests %+v, want the bar", got)
	}
	if got := v.complete('@', "an"); len(got) != 1 || got[0].Label != "Anna Berg" {
		t.Fatalf("@an suggests %+v, want Anna", got)
	}
}

func TestTheTypingLineNamesWhoIsTyping(t *testing.T) {
	h := newHarness(t, Options{}, history(3))
	h.do(func(u *gunim.UI) { h.v.SetTyping("Anna Berg", u) })
	if got := h.v.typing.Text; got != "Anna Berg is typing…" {
		t.Fatalf("the typing line says %q", got)
	}
	h.do(func(u *gunim.UI) { h.v.SetTyping("", u) })
	if got := h.v.typing.Text; got != " " {
		t.Fatalf("the typing line says %q with nobody typing", got)
	}
}

func TestAViewCanSitOnGlass(t *testing.T) {
	blurs := func(h *harness) bool {
		return slices.ContainsFunc(h.w.Offscreen().Ops(), func(op paint.Op) bool {
			l, ok := op.(*paint.LayerOp)
			return ok && l.Opts.Backdrop > 0
		})
	}
	h := newHarness(t, Options{}, history(3))
	if blurs(h) {
		t.Fatal("the view blurs what is behind it by default")
	}
	h.w.RegisterTheme(widget.Dark().With(theme.Set(Backdrop, 6)))
	if err := h.w.Client().SetTheme("dark"); err != nil {
		t.Fatal(err)
	}
	h.frames(60)
	if !blurs(h) {
		t.Fatal("the view does not blur what is behind it with a Backdrop")
	}
}

// The host's Tint colours the people it knows, in the suggestions, their rows and the bars of their quotes and
// cards; the rest keep the colour their name picks.
func TestTintColoursThePeopleTheHostKnows(t *testing.T) {
	sara := color.NRGBA{R: 0x10, G: 0x20, B: 0x30, A: 0xff}
	v := NewView(Options{Tint: func(name string) (color.NRGBA, bool) { return sara, name == "Sara" }})
	v.people = []string{"Sara", "Ola"}
	got := map[string]color.NRGBA{}
	for _, c := range v.complete('@', "") {
		got[c.Label] = c.Swatch
	}
	if got["Sara"] != sara || got["Ola"] != AvatarTint("Ola") {
		t.Fatalf("the suggestions are in %v", got)
	}
	r := newMsgRow(Item{Key: "m1", Message: Message{ID: "m1", Author: "Sara", Body: "Hej"}}, nil, nil, nil, nil, v.tint)
	if tintOf(r.tint, "Sara") != sara || tintOf(r.tint, "Ola") != AvatarTint("Ola") || tintOf(nil, "Sara") != AvatarTint("Sara") {
		t.Fatal("a row colours people other than the host says")
	}
	if c := newPreviewCard(Preview{URL: "x", Site: "Sara"}, nil, v.tint); tintOf(c.tint, c.p.Site) != sara {
		t.Fatal("a card from Sara is not in her colour")
	}
}
