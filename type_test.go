package gunim

import (
	"testing"

	"github.com/marrasen/gunim/input"
)

// UI.Type hands a key to the focused node as the keyboard would, and in a window going away it reaches nothing, as a
// key from the driver does not.
func TestTypeReachesNothingInAWindowGoingAway(t *testing.T) {
	w := newTestWindow()
	k := &taker{}
	w.ui.Insert(w.ui.Root(), k)
	run(w, 1)
	w.ui.Focus(k)
	w.ui.Type(input.KeyPress{Key: input.KeyF1})
	if k.took != 1 {
		t.Fatalf("the focused node took %d keys typed, want 1", k.took)
	}
	w.ui.goingAway = true
	w.ui.Type(input.KeyPress{Key: input.KeyF1})
	if k.took != 1 {
		t.Fatalf("in a window going away the focused node took %d keys typed, want none more", k.took-1)
	}
	var nobody *UI
	nobody.Type(input.KeyPress{Key: input.KeyF1})
}
