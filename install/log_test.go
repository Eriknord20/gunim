package install

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// A problem a window shows is logged once, as it first shows.
func TestAProblemShownIsLoggedOnce(t *testing.T) {
	var buf bytes.Buffer
	was := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(was)
	var l logged
	l.log("update", scene{Problem: "no network"})
	l.log("update", scene{Problem: "no network"})
	l.log("update", scene{NotesErr: "gone"})
	l.log("update", scene{})
	l.log("update", scene{Trouble: true, Status: "Couldn't check: offline"})
	got := buf.String()
	if n := strings.Count(got, "update: no network"); n != 1 {
		t.Errorf("the problem is logged %d times, want once: %q", n, got)
	}
	if !strings.Contains(got, "update: Couldn't check: offline") {
		t.Errorf("the failed check is not logged: %q", got)
	}
	if !strings.Contains(got, "update: reading what's new: gone") {
		t.Errorf("the notes' failure is not logged: %q", got)
	}
}
