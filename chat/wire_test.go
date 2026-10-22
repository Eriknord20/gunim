package chat

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/paint"
)

// TestWire checks that the conversation and every intent the view sends would also cross a socket.
func TestWire(t *testing.T) {
	RegisterTypes()
	at := time.Date(2026, 10, 20, 14, 15, 0, 0, time.UTC)
	c := Conversation{ID: "c1", Title: "general", Link: Reconnecting, Editing: "m2", Unread: 3, Loading: true,
		Replying: Quote{ID: "m1", Author: "Anna Berg", Text: "Lunch?", Gone: true},
		Draft:    Draft{Text: "Hej!", Seq: 4},
		Pending:  []Picture{{ID: "p1", W: 2, H: 2}},
		Images:   map[string]*paint.Image{"p1": picture(2, 2)},
		People:   []string{"Anna Berg"},
		Members:  []Member{{Name: "Marcus", Status: "You", Active: true, You: true}},
		Items: []Item{{Key: "day:m1", Day: "Today"}, {Key: "new:m1", New: true}, {Key: "m1", Message: Message{
			ID: "m1", Author: "Anna Berg", At: at, Body: "**Lunch?**", State: Failed, Edited: true, Continued: true,
			Reply:     Quote{ID: "m0", Author: "Marcus", Text: "Hungry"},
			Pictures:  []Picture{{ID: "p1", W: 2, H: 2}},
			Reactions: []Reaction{{Emoji: thumbs, Count: 2, Mine: true, Who: "You, Erik Lund"}},
			Preview:   Preview{URL: "https://go.dev", Site: "Go", Title: "Go", Description: "A language", Picture: "p1"},
			Poll:      Poll{Question: "Where?", Voters: 1, Options: []PollOption{{Text: "Thai", Votes: 1, Mine: true, Who: "You"}}},
			File:      FileRef{Folder: "Design", Name: "Colours.pdf", Size: 1_204_551},
			Private:   true,
		}}},
	}
	err := gunim.CheckWire(
		c,
		Submitted{Text: "Hej!"},
		Drafted{Text: "He"},
		ImagePasted{PNG: []byte{0x89, 'P', 'N', 'G'}},
		PictureRemoved{ID: "p1"},
		ReactionToggled{ID: "m1", Emoji: party},
		PollVoted{ID: "m1", Option: 2},
		ReplyAsked{ID: "m1"},
		EditAsked{ID: "m1"},
		WithdrawAsked{ID: "m1"},
		RetryAsked{ID: "m1"},
		FileOpened{Folder: "Design", Name: "Colours.pdf"},
		TransferRetried{ID: "t1"},
		TransferDismissed{ID: "t1"},
		OlderAsked{},
		PopOut{},
		Cancelled{},
	)
	if err != nil {
		t.Fatal(err)
	}
}
