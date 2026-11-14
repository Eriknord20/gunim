//go:build windows && (amd64 || arm64)

package desktop

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/access"
)

func TestAStatusThatChangesFastIsReadAtMostOnceAWhile(t *testing.T) {
	liveEvery = 100 * time.Millisecond
	defer func() { liveEvery = time.Second }()
	uw, count := queued()
	status := func(words string) *access.Node {
		return &access.Node{ID: 3, Info: access.Info{Role: access.RoleStatus, Name: words}}
	}
	uw.diff(status("1.0 MB/s"), status("1.1 MB/s"), false)
	uw.diff(status("1.1 MB/s"), status("1.2 MB/s"), false)
	uw.diff(status("1.2 MB/s"), status("1.3 MB/s"), false)
	// Three name changes, and one reading at once.
	if n := count(); n != 4 {
		t.Fatalf("three quick changes raised %d events, want the three name changes and one reading", n)
	}
	time.Sleep(250 * time.Millisecond)
	// The last words are read once the wait is up.
	if n := count(); n != 1 {
		t.Fatalf("after the wait %d more events came, want one reading of the latest words", n)
	}
}

func TestAnAlertInTheFirstTreeIsRead(t *testing.T) {
	uw, count := queued()
	alert := &access.Node{ID: 9, Info: access.Info{Role: access.RoleAlert, Name: "4 files can't sync"}}
	root := &access.Node{ID: 1, Info: access.Info{Role: access.RoleWindow}, Children: []*access.Node{alert}}
	uw.first(access.NewTree(root))
	if n := count(); n != 2 {
		t.Fatalf("a first tree with an alert raised %d events, want its children's and the alert's", n)
	}
}

func TestAnAnnouncementSurvivesAFullQueueAndOnlyTheLatestCounts(t *testing.T) {
	uw, _ := queued()
	for len(uw.events) < cap(uw.events) {
		uw.events <- func() {}
	}
	uw.say("Syncing 3 files")
	uw.say("You're up to date")
	if s := uw.spoken.Load(); s == nil || *s != "You're up to date" {
		t.Fatalf("with the queue full, the announcement waiting is %v, want the latest words", s)
	}
	if len(uw.speak) != 1 {
		t.Fatal("nothing woke the goroutine for the announcement")
	}
}

func TestARadioButtonSaysWhereItIsInItsGroup(t *testing.T) {
	radio := func(id uint64) *access.Node {
		return &access.Node{ID: id, Info: access.Info{Role: access.RoleRadioButton}}
	}
	group := &access.Node{ID: 1, Info: access.Info{Role: access.RoleGroup}, Children: []*access.Node{
		{ID: 2, Info: access.Info{Role: access.RoleLabel}}, radio(3), radio(4), radio(5)}}
	if pos, size := radioPlace(group, 4); pos != 2 || size != 3 {
		t.Fatalf("the middle radio button is %d of %d, want 2 of 3", pos, size)
	}
	if pos, size := radioPlace(group, 2); pos != 0 || size != 0 {
		t.Fatalf("a label is %d of %d, want no place", pos, size)
	}
}
