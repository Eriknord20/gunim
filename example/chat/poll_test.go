package main

import (
	"slices"
	"testing"

	"github.com/marrasen/gunim/chat"
)

func TestSlashPollMakesAPoll(t *testing.T) {
	p, ok := newPoll("/poll Lunch? | Thai | Pizza | ")
	if !ok || p.question != "Lunch?" || !slices.Equal(p.options, []string{"Thai", "Pizza"}) {
		t.Fatalf("poll %+v, %v, want Lunch? with two options", p, ok)
	}
	for _, text := range []string{"/poll Lunch? | Thai", "poll: Lunch? | a | b", "/pollen | a | b"} {
		if _, ok := newPoll(text); ok {
			t.Fatalf("%q made a poll", text)
		}
	}
}

func TestVotingMovesAndTakesBack(t *testing.T) {
	p, _ := newPoll("/poll Lunch? | Thai | Pizza")
	m := &msg{poll: p}
	p.vote("Anna Berg", 0)
	p.vote(me, 0)
	p.vote(me, 1)
	got := pollOf(m)
	if got.Voters != 2 || got.Options[0].Votes != 1 || got.Options[1].Votes != 1 || !got.Options[1].Mine ||
		got.Options[0].Mine || got.Options[0].Who != "Anna Berg" {
		t.Fatalf("poll %+v, want Anna on Thai and the user moved to Pizza", got)
	}
	p.vote(me, 1)
	if got := pollOf(m); got.Voters != 1 || got.Options[1].Votes != 0 {
		t.Fatalf("poll %+v, want the user's vote taken back", got)
	}
}

func TestAPollSentShowsItsCardAndTakesVotes(t *testing.T) {
	h := newHarness(t)
	h.typeAndSend("/poll Ship on Friday? | Yes | No")
	last := h.a.current.msgs[len(h.a.current.msgs)-1]
	if last.poll == nil || last.Body != "" {
		t.Fatalf("last message %+v, want a poll with no text", last)
	}
	h.a.handle(chat.PollVoted{ID: last.ID, Option: 0})
	h.frames(30)
	it, _ := h.find(last.ID)
	if it.Poll.Question != "Ship on Friday?" || it.Poll.Voters != 1 || !it.Poll.Options[0].Mine {
		t.Fatalf("poll %+v, want the user's vote for Yes", it.Poll)
	}
	last.poll.vote("Anna Berg", 0)
	if it, _ := h.find(last.ID); it.Poll.Options[0].Who != "You, Anna Berg" || it.Poll.Options[1].Who != "" {
		t.Fatalf("poll %+v, want the user and Anna on Yes", it.Poll)
	}
}

func TestEveryWayInMakesAPollOfSlashPoll(t *testing.T) {
	h := newHarness(t)
	m := h.a.add(h.a.current, "Sara Nyström", "/poll Lunch? | Thai | Pizza", h.a.current.msgs[0].At)
	if m.poll == nil || m.Body != "" {
		t.Fatalf("message %+v, want a poll", m)
	}
}
