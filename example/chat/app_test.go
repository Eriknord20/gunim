package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/chat"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/markdown"
	"github.com/marrasen/gunim/widget"
)

// harness runs the app half against the real views in an offscreen window, with no colleagues about.
type harness struct {
	t *testing.T
	w *gunim.Window
	a *app
	v *chatView
}

// grab is a patch that hands the test the chat view.
type grab struct{}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessSized(t, geom.Sz(1100, 700))
}

// newHarnessSized is newHarness with a window of size, as 390×800 for one
// shaped like a phone.
func newHarnessSized(t *testing.T, size geom.Size) *harness {
	t.Helper()
	h := &harness{t: t, w: gunimtest.New(t, size, widget.NewSurface())}
	registerViews(h.w)
	gunim.RegisterPatch(h.w, "chat", func(v *chatView, _ grab, _ *gunim.UI) { h.v = v })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h.a = newApp(ctx, h.w.Client(), 1, 200, 0)
	if err := h.w.Client().Mount(gunim.Root, "chat", "chat", h.a.state()); err != nil {
		t.Fatal(err)
	}
	if err := h.w.Client().Patch("chat", grab{}); err != nil {
		t.Fatal(err)
	}
	h.frames(30)
	return h
}

// frames draws n frames, handling the intents and the timers that come due meanwhile.
func (h *harness) frames(n int) {
	for range n {
		h.w.Frame(time.Second / 60)
		for more := true; more; {
			select {
			case ev := <-h.w.Client().Intents():
				h.a.handle(ev.Intent)
			case fn := <-h.a.later:
				fn()
			default:
				more = false
			}
		}
	}
}

// until draws frames until ok holds, for up to five seconds.
func (h *harness) until(what string, ok func() bool) {
	h.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); {
		if time.Now().After(deadline) {
			h.t.Fatalf("waited in vain until %s", what)
		}
		h.frames(1)
		time.Sleep(5 * time.Millisecond)
	}
}

// typeAndSend types s in the message box and presses Enter.
func (h *harness) typeAndSend(s string) {
	h.w.Input(input.TextInput{Text: s})
	h.frames(1)
	h.w.Input(input.KeyPress{Key: input.KeyEnter})
	h.frames(2)
}

// item returns the timeline's row for the user's message with body, if it shows.
func (h *harness) item(body string) (chat.Item, bool) {
	for _, it := range h.a.state().Items {
		if it.Mine && it.Body == body {
			return it, true
		}
	}
	return chat.Item{}, false
}

// find returns the timeline's row for the message id, if it shows.
func (h *harness) find(id string) (chat.Item, bool) {
	for _, it := range h.a.state().Items {
		if it.Key == id {
			return it, true
		}
	}
	return chat.Item{}, false
}

func (h *harness) state(body string) chat.State {
	it, ok := h.item(body)
	if !ok {
		h.t.Fatalf("no message %q in the timeline", body)
	}
	return it.State
}

func TestASentMessageShowsAtOnceAndArrives(t *testing.T) {
	h := newHarness(t)
	h.typeAndSend("Hello there")
	if s := h.state("Hello there"); s != chat.Pending {
		t.Fatalf("state %v straight after sending, want pending", s)
	}
	if h.a.text != "" {
		t.Fatalf("message box holds %q after sending, want it empty", h.a.text)
	}
	h.until("the message is sent", func() bool { return h.state("Hello there") == chat.Sent })
}

func TestMessagesSentOfflineWaitForTheConnection(t *testing.T) {
	h := newHarness(t)
	h.a.handle(LinkToggled{})
	h.typeAndSend("Later")
	time.Sleep(1200 * time.Millisecond)
	h.frames(5)
	if s := h.state("Later"); s != chat.Pending {
		t.Fatalf("state %v while offline, want pending", s)
	}
	h.a.handle(LinkToggled{})
	h.until("the message is sent", func() bool { return h.state("Later") == chat.Sent })
}

func TestAFailedMessageGoesAgainOnAClick(t *testing.T) {
	h := newHarness(t)
	h.a.failRate = 1
	h.typeAndSend("Try me")
	h.until("the send fails", func() bool { return h.state("Try me") == chat.Failed })
	h.a.failRate = 0
	it, _ := h.item("Try me")
	h.a.handle(chat.RetryAsked{ID: it.ID})
	h.until("the message is sent", func() bool { return h.state("Try me") == chat.Sent })
}

func TestAReplyQuotesItsMessage(t *testing.T) {
	h := newHarness(t)
	target := h.a.current.msgs[len(h.a.current.msgs)-3]
	h.a.handle(chat.ReplyAsked{ID: target.ID})
	h.frames(2)
	h.typeAndSend("Replying")
	it, ok := h.item("Replying")
	if !ok || it.Reply.ID != target.ID || it.Reply.Text != target.Body {
		t.Fatalf("reply %+v, want a quote of %s", it.Reply, target.ID)
	}
}

func TestEditingReplacesTheText(t *testing.T) {
	h := newHarness(t)
	h.typeAndSend("Frist")
	it, _ := h.item("Frist")
	h.a.handle(chat.EditAsked{ID: it.ID})
	h.frames(2)
	if h.a.text != "Frist" {
		t.Fatalf("message box holds %q for the edit, want the message", h.a.text)
	}
	h.w.Input(input.KeyPress{Key: input.KeyA, Mods: input.ModControl})
	h.typeAndSend("First")
	it, ok := h.item("First")
	if !ok || !it.Edited || it.ID == "" {
		t.Fatalf("edited message %+v, want the new text marked edited", it)
	}
	if _, ok := h.item("Frist"); ok {
		t.Fatal("the old text still shows")
	}
}

func TestOnlyWebAndMailLinksOpen(t *testing.T) {
	h := newHarness(t)
	var opened []string
	h.w.Offscreen().SetLauncher(func(path string) error {
		opened = append(opened, path)
		return nil
	}, nil)
	for _, url := range []string{"https://example.com/x", "mailto:anna@example.com", `C:\Windows\notepad.exe`,
		"file:///etc/passwd"} {
		h.a.handle(markdown.Link{URL: url})
	}
	if want := []string{"https://example.com/x", "mailto:anna@example.com"}; !slices.Equal(opened, want) {
		t.Fatalf("opened %v, want %v", opened, want)
	}
}

func TestHelpListsTheCommandsForTheUserAlone(t *testing.T) {
	h := newHarness(t)
	before := len(h.a.current.msgs)
	h.a.handle(chat.Submitted{Text: "/help"})
	h.frames(5)
	msgs := h.a.current.msgs
	if len(msgs) != before+1 {
		t.Fatalf("/help added %d messages, want one", len(msgs)-before)
	}
	m := msgs[len(msgs)-1]
	if !m.private || m.State != 0 || !strings.Contains(m.Body, "/poll") {
		t.Fatalf("/help answered %+v, want a private list with /poll in it", m)
	}
	if it, _ := h.find(m.ID); !it.Private {
		t.Fatalf("the timeline shows the answer as %+v, want it private", it)
	}
}

func TestColleaguesLeaveTheHelpAlone(t *testing.T) {
	h := newHarness(t)
	h.a.handle(chat.Submitted{Text: "/help"})
	if m := h.a.lastBy(h.a.current, func(*msg) bool { return true }); m != nil && m.private {
		t.Fatal("colleagues can pick the private /help answer to reply or react to")
	}
}

func TestAConversationOpensOnItsLatestMessagesAndLoadsOlderOnes(t *testing.T) {
	h := newHarness(t)
	count := func() int {
		n := 0
		for _, it := range h.a.state().Items {
			if it.Day == "" && !it.New {
				n++
			}
		}
		return n
	}
	if n, all := count(), len(h.a.current.msgs); n >= all || n < pageSize {
		t.Fatalf("the conversation opens showing %d of %d messages, want the latest page", n, all)
	}
	before := count()
	h.a.handle(chat.OlderAsked{})
	if !h.a.state().Loading {
		t.Fatal("the state does not say older messages are on their way")
	}
	h.a.handle(chat.OlderAsked{})
	h.until("older messages arrive", func() bool { return count() == before+pageSize })
}

func TestMembersListTheUserFirstThenThoseActive(t *testing.T) {
	h := newHarness(t)
	c := h.a.current
	// The last of the conversation's people wrote just now.
	last := c.people[len(c.people)-1]
	h.a.add(c, last, "Here", time.Now())
	ms := h.a.membersOf(c, time.Now())
	if ms[0].Name != me {
		t.Fatalf("the members start with %s, want the user", ms[0].Name)
	}
	seenAway := false
	for _, m := range ms[1:] {
		if m.Active && seenAway {
			t.Fatalf("an active member comes after one away: %+v", ms)
		}
		seenAway = seenAway || !m.Active
		if m.Name == last && (!m.Active || m.Status != "Active now") {
			t.Fatalf("%s, who just wrote, stands as %+v", last, m)
		}
	}
	if len(ms) != len(c.people)+1 {
		t.Fatalf("%d members, want the conversation's people and the user", len(ms))
	}
}
