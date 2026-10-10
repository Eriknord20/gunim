package filemanager

import (
	"errors"
	"slices"
	"sync"
	"testing"
)

// The failures and warnings a window shows reach Options.Log, in a
// banner, a notice or a dialog, and so does every other notice.
func TestFailuresWarningsAndNoticesAreLogged(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	h := newHarnessWith(t, func(o *Options) {
		o.Log = func(l string) {
			mu.Lock()
			lines = append(lines, l)
			mu.Unlock()
		}
	}, "a.txt")
	h.a.fail("Opening x: gone")
	h.a.patch(Notice{Title: "Stopped: copying", Body: "2 done.", Kind: "warning"})
	h.a.patch(Notice{Title: "Copied", Kind: "success"})
	h.a.showError(ErrorBox{Title: "Couldn't copy", Body: "Local: open D:\\x: denied"})
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(lines, []string{"files: Opening x: gone", "files: Stopped: copying: 2 done.", "files: Copied", `files: Couldn't copy: Local: open D:\x: denied`}) {
		t.Fatalf("the log has %q", lines)
	}
}

// A folder that cannot be listed is logged once, though the listing
// that says so is published again.
func TestAListingThatFailsIsLoggedOnce(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	h := newHarnessWith(t, func(o *Options) {
		o.Log = func(l string) {
			mu.Lock()
			lines = append(lines, l)
			mu.Unlock()
		}
	}, "a.txt")
	n := &h.a.nav
	h.a.listed(n.gen, n.path, nil, n.mod, errors.New("denied"))
	h.a.publishListing()
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"files: Listing " + h.a.ps.Show(n.path) + ": denied"}; !slices.Equal(lines, want) {
		t.Fatalf("the log has %q, want %q", lines, want)
	}
}
