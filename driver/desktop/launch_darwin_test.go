package desktop

import (
	"path/filepath"
	"testing"
)

// open(1) answers for a file that isn't there, opening nothing.
func TestShowingAFileThatIsGoneSaysSo(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone.hej")
	w := &Window{}
	if err := w.Reveal(gone); err == nil {
		t.Error("revealing a file that's gone said nothing")
	}
	if err := w.Open(gone); err == nil {
		t.Error("opening a file that's gone said nothing")
	}
}

// Only web pages open as links.
func TestOnlyAWebPageOpensAsALink(t *testing.T) {
	w := &Window{}
	for _, link := range []string{"-a", "file:///Applications/Calculator.app", "", "x-man-page://ls"} {
		if err := w.OpenLink(link); err == nil {
			t.Errorf("%q opened as a link", link)
		}
	}
}
