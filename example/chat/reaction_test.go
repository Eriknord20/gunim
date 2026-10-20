package main

import (
	"testing"

	"github.com/marrasen/gunim/chat"
)

const thumbs, party = "\U0001F44D", "\U0001F389"

func TestReactingAddsAReactionAndReactingAgainTakesItBack(t *testing.T) {
	h := newHarness(t)
	last := h.a.current.msgs[len(h.a.current.msgs)-1]
	h.a.handle(chat.ReactionToggled{ID: last.ID, Emoji: thumbs})
	h.frames(20)
	it, _ := h.find(last.ID)
	if len(it.Reactions) != 1 || it.Reactions[0] != (chat.Reaction{Emoji: thumbs, Count: 1, Mine: true, Who: "You"}) {
		t.Fatalf("reactions %+v, want the user's thumbs up", it.Reactions)
	}

	// A colleague joins in with the same emoji, and another.
	last.toggle(thumbs, "Anna Berg")
	last.toggle(party, "Anna Berg")
	h.a.publish()
	h.frames(20)
	it, _ = h.find(last.ID)
	if len(it.Reactions) != 2 || it.Reactions[0].Count != 2 || it.Reactions[0].Who != "You, Anna Berg" ||
		it.Reactions[1].Emoji != party {
		t.Fatalf("reactions %+v, want two thumbs up and a party popper, in the order they came", it.Reactions)
	}

	h.a.handle(chat.ReactionToggled{ID: last.ID, Emoji: thumbs})
	h.a.handle(chat.ReactionToggled{ID: last.ID, Emoji: party})
	h.frames(20)
	it, _ = h.find(last.ID)
	if len(it.Reactions) != 2 || it.Reactions[0].Mine || it.Reactions[1].Count != 2 {
		t.Fatalf("reactions %+v, want Anna's thumbs up and both party poppers", it.Reactions)
	}
}
