//go:build windows && (amd64 || arm64)

package desktop

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/access"
)

func TestTheNewRolesHaveTheirControlTypes(t *testing.T) {
	for r, want := range map[access.Role]int32{
		access.RoleStatus: ctText, access.RoleAlert: ctText, access.RoleRadioButton: ctRadioButton,
	} {
		if got := controlType(&access.Node{Info: access.Info{Role: r}}); got != want {
			t.Errorf("%v is control type %d, want %d", r, got, want)
		}
	}
}

func TestARadioButtonIsSelectedWhenChecked(t *testing.T) {
	on := &access.Node{Info: access.Info{Role: access.RoleRadioButton, State: access.StateCheckable | access.StateChecked}}
	off := &access.Node{Info: access.Info{Role: access.RoleRadioButton, State: access.StateCheckable}}
	box := &access.Node{Info: access.Info{Role: access.RoleCheckbox, State: access.StateCheckable | access.StateChecked}}
	if !isSelected(on) || isSelected(off) {
		t.Errorf("a chosen radio button reads selected %v, and one not chosen %v", isSelected(on), isSelected(off))
	}
	if isSelected(box) {
		t.Error("a ticked checkbox reads as selected")
	}
}

// queued returns a window whose events queue up untouched, and a count of what is queued.
func queued() (uw *uiaWindow, count func() int) {
	uw = &uiaWindow{events: make(chan func(), 64), stop: make(chan struct{}), speak: make(chan struct{}, 1),
		liveAt: map[uint64]time.Time{}, livePending: map[uint64]bool{}}
	return uw, func() int {
		n := 0
		for {
			select {
			case <-uw.events:
				n++
			default:
				return n
			}
		}
	}
}

func TestChoosingARadioButtonRaisesItsSelection(t *testing.T) {
	uw, count := queued()
	was := &access.Node{ID: 7, Info: access.Info{Role: access.RoleRadioButton, State: access.StateCheckable}}
	is := &access.Node{ID: 7, Info: access.Info{Role: access.RoleRadioButton, State: access.StateCheckable | access.StateChecked}}
	uw.diff(was, is, false)
	if n := count(); n != 2 {
		t.Fatalf("choosing a radio button raised %d events, want IsSelected's change and ElementSelected", n)
	}
	uw.diff(is, was, false)
	if n := count(); n != 1 {
		t.Fatalf("leaving a radio button raised %d events, want IsSelected's change alone", n)
	}
}

func TestAStatusThatChangesIsReadAgain(t *testing.T) {
	uw, count := queued()
	was := &access.Node{ID: 3, Info: access.Info{Role: access.RoleStatus, Name: "Syncing 3 files"}}
	is := &access.Node{ID: 3, Info: access.Info{Role: access.RoleStatus, Name: "You're up to date"}}
	uw.diff(was, is, false)
	if n := count(); n != 2 {
		t.Fatalf("a status's new words raised %d events, want the name's change and a live region's", n)
	}
	uw.diff(is, is, false)
	if n := count(); n != 0 {
		t.Fatalf("a status that stayed the same raised %d events", n)
	}
	label := &access.Node{ID: 4, Info: access.Info{Role: access.RoleLabel, Name: "a"}}
	label2 := &access.Node{ID: 4, Info: access.Info{Role: access.RoleLabel, Name: "b"}}
	uw.diff(label, label2, false)
	if n := count(); n != 1 {
		t.Fatalf("a label's new words raised %d events, want the name's change alone", n)
	}
}
