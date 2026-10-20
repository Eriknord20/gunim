package main

import (
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/chat"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/widget"
)

func TestAConversationPopsOutIntoAWindowOfItsOwn(t *testing.T) {
	h := newHarness(t)
	var opened []*gunim.Window
	h.a.openWindow = func(string) (gunim.Client, error) {
		w := gunimtest.New(t, geom.Sz(560, 720), widget.NewSurface())
		registerViews(w)
		opened = append(opened, w)
		return w.Client(), nil
	}
	general := h.a.current
	h.a.handle(chat.PopOut{})
	if len(opened) != 1 || len(h.a.windows) != 2 {
		t.Fatalf("%d windows opened and %d open, want one popped out", len(opened), len(h.a.windows))
	}
	pop := h.a.windows[1]
	if s := h.a.stateOf(pop); !s.Solo || s.ID != general.ID {
		t.Fatalf("the popped out window shows %q, solo %v; want %q alone", s.ID, s.Solo, general.ID)
	}

	// A message written in the popped out window shows in the main one too.
	h.a.handleIn(pop, chat.Submitted{Text: "From the other window"})
	h.frames(5)
	if _, ok := h.item("From the other window"); !ok {
		t.Fatal("the main window does not show the message sent from the popped out one")
	}

	// The main window moves on; the popped out one stays.
	h.a.handle(ConversationChosen{ID: h.a.projects[0].convs[1].ID})
	if pop.current != general || h.a.current == general {
		t.Fatal("changing the main window's conversation moved the popped out one")
	}

	// Typing in the popped out conversation shows there.
	h.a.typingIn(general, "Anna Berg")
	if pop.typing != "Anna Berg" || h.a.typing != "" {
		t.Fatalf("typing shows %q there and %q in the main window, want it where the conversation is", pop.typing,
			h.a.typing)
	}
	for range 5 {
		opened[0].Frame(0)
	}
}
