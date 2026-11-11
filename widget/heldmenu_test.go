package widget

import (
	"context"
	"testing"
	"time"

	"github.com/marrasen/gunim"
)

// Under a held clock, a submenu opens exactly its wait after the pointer rests on its item, and closes exactly its
// wait after the pointer moves on to another item, however the frames fall.
func TestAHeldSubmenuOpensAndClosesAtExactlyItsWait(t *testing.T) {
	w, c, _ := subStage(t)
	clock := gunim.NewHeldClock(gunim.HoldOptions{})
	clock.Join(w)
	ctx := context.Background()
	advance := func(d time.Duration) {
		t.Helper()
		if err := clock.Advance(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	frame := func(int) { advance(0) }

	pointAt(c, 1, frame)
	advance(subDelay - time.Millisecond)
	if c.menu.sub != nil {
		t.Fatalf("the submenu opened before %v", subDelay)
	}
	advance(time.Millisecond)
	if c.menu.sub == nil {
		t.Fatalf("the submenu did not open at %v", subDelay)
	}
	advance(100 * time.Millisecond)

	pointAt(c, 3, frame)
	advance(subDelay - time.Millisecond)
	if c.menu.sub == nil {
		t.Fatalf("the submenu closed before %v", subDelay)
	}
	advance(time.Millisecond)
	if c.menu.sub != nil {
		t.Fatalf("the submenu was still open %v after the pointer moved on", subDelay)
	}
}
