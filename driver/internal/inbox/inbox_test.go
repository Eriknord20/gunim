package inbox

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

func TestInboxDeliversInOrderAndCloses(t *testing.T) {
	quit := make(chan struct{})
	defer close(quit)
	q := New(quit)
	for i := range 100 {
		q.Push(i)
	}
	q.Close()
	q.Push("after close")

	want := 0
	for ev := range q.Out() {
		if ev != want {
			t.Fatalf("got %v, want %d", ev, want)
		}
		want++
	}
	if want != 100 {
		t.Fatalf("delivered %d events, want 100", want)
	}
}

func TestInboxMergesQueuedMoves(t *testing.T) {
	move := func(x float32, mods input.Mods) input.PointerMove {
		return input.PointerMove{Pos: geom.Pt(x, 0), Mods: mods}
	}
	down := input.PointerDown{Pos: geom.Pt(3, 0), Button: input.ButtonPrimary}
	up := input.PointerUp{Pos: geom.Pt(6, 0), Button: input.ButtonPrimary}
	q := &Inbox{items: []any{
		move(1, 0), move(2, 0), move(3, 0),
		down,
		move(4, 0), move(5, 0), move(6, input.ModShift),
		up,
		move(7, 0),
	}}

	var got []any
	for len(q.items) > 0 {
		ev := q.items[0]
		q.items = q.items[1:]
		got = append(got, q.lastMove(ev))
	}
	want := []any{move(3, 0), down, move(5, 0), move(6, input.ModShift), up, move(7, 0)}
	if len(got) != len(want) {
		t.Fatalf("got %d events %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestInboxSwapsAWaitingMove(t *testing.T) {
	quit := make(chan struct{})
	defer close(quit)
	q := New(quit)
	drained := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			q.mu.Lock()
			n := len(q.items)
			q.mu.Unlock()
			if n == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%d events still queued", n)
			}
			time.Sleep(time.Millisecond)
		}
	}
	q.Push(input.PointerMove{Pos: geom.Pt(0, 0)})
	drained()
	for x := 1; x < 4; x++ {
		q.Push(input.PointerMove{Pos: geom.Pt(float32(x), 0)})
	}
	drained()
	q.Close()

	var got []any
	for ev := range q.Out() {
		got = append(got, ev)
	}
	if want := (input.PointerMove{Pos: geom.Pt(3, 0)}); len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want only %v", got, want)
	}
}

func TestInboxWaitingCountsWhatIsNotYetRead(t *testing.T) {
	quit := make(chan struct{})
	defer close(quit)
	q := New(quit)
	if q.Waiting() {
		t.Fatal("an empty inbox is waiting")
	}
	q.Push(1)
	if !q.Waiting() {
		t.Fatal("an item pushed isn't waiting")
	}
	// The goroutine has the item in hand, and it is still unread.
	time.Sleep(10 * time.Millisecond)
	if !q.Waiting() {
		t.Fatal("an item on its way out isn't waiting")
	}
	<-q.Out()
	for give := time.Now().Add(5 * time.Second); q.Waiting(); time.Sleep(time.Millisecond) {
		if time.Now().After(give) {
			t.Fatal("still waiting with everything read")
		}
	}
}
