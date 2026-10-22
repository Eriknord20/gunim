package main

import (
	"testing"

	"github.com/marrasen/gunim/chat"
)

// newLineAt returns where the line over new messages is in the timeline's rows, or -1.
func newLineAt(items []chat.Item) int {
	for i, it := range items {
		if it.New {
			return i
		}
	}
	return -1
}

func TestTheNewLineGoesOverTheFirstUnreadMessage(t *testing.T) {
	h := newHarness(t)
	c := h.a.projects[0].convs[1]
	c.msgs[len(c.msgs)-1].Author, c.msgs[len(c.msgs)-2].Author, c.msgs[len(c.msgs)-3].Author = "Anna Berg", "Anna Berg", "Anna Berg"
	h.a.leaveUnread(c, 3)
	h.a.handle(ConversationChosen{ID: c.ID})
	s := h.a.state()
	at := newLineAt(s.Items)
	if at < 0 || s.Items[at+1].ID != c.msgs[len(c.msgs)-3].ID || s.Unread != 3 {
		t.Fatalf("new line at %d, unread %d; want it over the third message from the end, and 3 unread", at,
			s.Unread)
	}
	if c.unread != 0 {
		t.Fatalf("%d unread after opening, want none", c.unread)
	}
	// Away and back, it is all read.
	h.a.handle(ConversationChosen{ID: h.a.projects[0].convs[0].ID})
	h.a.handle(ConversationChosen{ID: c.ID})
	if at := newLineAt(h.a.state().Items); at >= 0 {
		t.Fatal("the new line is back after reading")
	}
}
