package chat

import (
	"image"
	"testing"

	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

const thumbs, party = "\U0001F44D", "\U0001F389"

func TestReactionsArriveAsChipsInTheOrderTheyCame(t *testing.T) {
	h := newHarness(t, Options{}, history(20))
	last := &h.msgs[len(h.msgs)-1]
	last.Reactions = []Reaction{{Emoji: thumbs, Count: 1, Mine: true, Who: "You"}}
	h.publish()
	h.frames(20)
	bar := h.row(last.ID).reactions
	if len(bar.chips) != 1 {
		t.Fatalf("%d chips, want 1", len(bar.chips))
	}

	// A colleague joins in with the same emoji, and another.
	last.Reactions = []Reaction{{Emoji: thumbs, Count: 2, Mine: true, Who: "You, Anna Berg"}, {Emoji: party, Count: 1, Who: "Anna Berg"}}
	h.publish()
	h.frames(20)
	if len(bar.chips) != 2 || bar.order[0] != thumbs || bar.order[1] != party {
		t.Fatalf("chips in order %q, want the order they came in", bar.order)
	}
	if got := bar.chips[thumbs].Text; got != "You, Anna Berg" {
		t.Fatalf("the thumbs up's tooltip says %q", got)
	}

	last.Reactions = []Reaction{{Emoji: party, Count: 2, Who: "Anna Berg, You"}}
	h.publish()
	h.frames(20)
	if len(bar.chips) != 1 || bar.order[0] != party {
		t.Fatalf("chips %q, want the party popper alone", bar.order)
	}
}

func TestAMessageBuiltWithReactionsShowsThem(t *testing.T) {
	it := Item{Key: "m1", Message: Message{ID: "m1", Author: "Erik Lund", Body: "Shipped",
		Reactions: []Reaction{{Emoji: party, Count: 3, Who: "Anna Berg, Sara Nyström, Erik Lund"}}}}
	r := newMsgRow(it, nil, nil, nil, nil, nil)
	bar := r.reactions
	if len(bar.chips) != 1 || len(bar.Children()) != 2 {
		t.Fatalf("%d chips and %d children, want the party popper and the chip that adds", len(bar.chips),
			len(bar.Children()))
	}
}

// picture returns a w by h picture.
func picture(w, h int) *paint.Image { return paint.NewImage(image.NewRGBA(image.Rect(0, 0, w, h))) }

func TestPicturesWaitInTheStripThenShowInTheirMessage(t *testing.T) {
	h := newHarness(t, Options{}, history(20))
	h.c.Images = map[string]*paint.Image{"p1": picture(800, 600), "p2": picture(40, 30)}
	h.c.Pending = []Picture{{ID: "p1", W: 800, H: 600}, {ID: "p2", W: 40, H: 30}}
	h.publish()
	if n := len(h.v.strip.thumbs); n != 2 {
		t.Fatalf("the strip shows %d pictures, want 2", n)
	}
	h.c.Pending = h.c.Pending[1:]
	h.publish()
	if _, ok := h.v.strip.thumbs["p2"]; !ok || len(h.v.strip.thumbs) != 1 {
		t.Fatalf("the strip shows %d pictures, want the second alone", len(h.v.strip.thumbs))
	}

	id := h.add("Marcus", "Look")
	h.msgs[len(h.msgs)-1].Pictures = []Picture{{ID: "p1", W: 800, H: 600}}
	h.c.Pending = nil
	h.publish()
	h.frames(30)
	if n := len(h.v.strip.thumbs); n != 0 {
		t.Fatalf("%d pictures still waiting after sending", n)
	}
	r := h.row(id)
	if len(r.pictures) != 1 {
		t.Fatal("the message's row shows no picture")
	}
	if s := r.pictures[0].Size; s.H != 260 || s.W < 346 || s.W > 347 {
		t.Fatalf("the picture shows %v, want it shrunk to 260 high, keeping its shape", s)
	}
}

func TestAPollsCardFollowsItsVotes(t *testing.T) {
	h := newHarness(t, Options{}, history(20))
	id := h.add("Marcus", "")
	m := &h.msgs[len(h.msgs)-1]
	m.Poll = Poll{Question: "Ship on Friday?", Voters: 1, Options: []PollOption{
		{Text: "Yes", Votes: 1, Mine: true, Who: "You"}, {Text: "No"}}}
	h.publish()
	h.frames(30)
	card := h.row(id).poll
	if s := card.opts[0].share.Value(); s < 0.99 {
		t.Fatalf("Yes's bar is at %v, want all of it", s)
	}
	m.Poll.Voters, m.Poll.Options[0].Votes, m.Poll.Options[0].Who = 2, 2, "You, Anna Berg"
	h.publish()
	if got := card.tips[0].Text; got != "You, Anna Berg" {
		t.Fatalf("Yes's tooltip says %q, want who voted for it", got)
	}
	if got := card.tips[1].Text; got != "" {
		t.Fatalf("No's tooltip says %q, want nothing", got)
	}
}

func TestALongCatchUpOpensAtTheNewLineAndThePillCounts(t *testing.T) {
	msgs := history(200)
	for i := range msgs[160:] {
		msgs[160+i].Author, msgs[160+i].Mine = "Anna Berg", false
	}
	h := newHarness(t, Options{}, history(20))
	h.c.ID, h.msgs, h.newFrom, h.c.Unread = "c2", msgs, msgs[160].ID, 40
	h.publish()
	h.frames(30)
	if h.v.list.AtEnd() {
		t.Fatal("the timeline opened at its end, past the 40 unread")
	}
	if n, ok := h.v.list.Row(widget.Key("new:" + msgs[160].ID)); !ok || n == nil {
		t.Fatal("the new line is not in view")
	}
	if got := h.v.catchUp.count; got != 40 {
		t.Fatalf("the pill counts %d, want 40", got)
	}
	h.v.list.ScrollToEnd(widget.Quick.Default())
	h.frames(90)
	if got := h.v.catchUp.count; got != 0 {
		t.Fatalf("the pill counts %d at the end, want none", got)
	}
}

func TestMessagesThatComeWhileScrolledUpWaitBelow(t *testing.T) {
	h := newHarness(t, Options{}, history(200))
	h.v.list.ScrollTo(0, widget.Quick.Default())
	h.frames(60)
	h.add("Anna Berg", "Are you there?")
	h.publish()
	if got := h.v.catchUp.count; got != 1 {
		t.Fatalf("the pill counts %d, want the message that came", got)
	}
}
