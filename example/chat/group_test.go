package main

import (
	"strings"
	"testing"
	"time"
)

func TestAColleagueEditsAMessageOnce(t *testing.T) {
	h := newHarness(t)
	c := h.a.current
	who := c.people[0]
	m := h.a.add(c, who, "Build times:\n\n| a | b |\n|--|--|\n| 1 | 2 |", time.Now())
	for range 200 {
		h.a.colleague()
	}
	if n := strings.Count(m.Body, "\n\n"); n > 2 {
		t.Fatalf("the message was edited %d times, want once at most:\n%s", n-1, m.Body)
	}
	if m.Edited && !strings.Contains(m.Body, "|\n\n") {
		t.Fatalf("the edit did not start a paragraph of its own:\n%s", m.Body)
	}
}
