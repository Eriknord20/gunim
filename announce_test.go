package gunim

import (
	"slices"
	"testing"
)

func TestAnAnnouncementReachesTheWindow(t *testing.T) {
	w := newTestWindow()
	if !w.ui.Announce("Syncing 3 files") || !w.ui.Announce("You're up to date") {
		t.Fatal("an announcement a window took reports it was not passed on")
	}
	if got, want := w.Offscreen().Announced(), []string{"Syncing 3 files", "You're up to date"}; !slices.Equal(got, want) {
		t.Fatalf("the window was told %q, want %q", got, want)
	}
}

func TestAnnouncingOnANilUIDoesNothing(t *testing.T) {
	var u *UI
	if u.Announce("nothing hears this") {
		t.Fatal("an announcement with no window reports it was passed on")
	}
}
