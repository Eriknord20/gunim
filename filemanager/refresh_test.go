package filemanager

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// F5 lists the folder again, and shows a file made there behind the
// file manager's back.
func TestF5ShowsAFileMadeSince(t *testing.T) {
	h := newHarness(t, "old.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	if err := os.WriteFile(filepath.Join(h.dir, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.w.Input(input.KeyPress{Key: input.KeyF5})
	h.until("F5 shows the new file", func() bool { return slices.Equal(h.shown(), []string{"new.txt", "old.txt"}) })
}

// F5 lists a pane's folder again too, as a program's window hosts it.
func TestF5InAPaneShowsAFileMadeSince(t *testing.T) {
	p := newPaneWorld(t, "old.txt")
	s := newPaneScreen(t, p.pw)
	p.pw.Attach(s.w.Client(), "slot")
	shown := func() int {
		b := s.browser()
		if b == nil || b.listing.cur == nil {
			return 0
		}
		return b.listing.cur.grid.Rows()
	}
	s.until("the pane shows the file", func() bool { return shown() == 1 })
	if err := s.w.Client().Patch("pane1/browser", focusPane{}); err != nil {
		t.Fatal(err)
	}
	s.frames(2)
	if err := os.WriteFile(filepath.Join(p.dir, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.w.Input(input.KeyPress{Key: input.KeyF5, Time: time.Now()})
	s.until("F5 shows the new file", func() bool { return shown() == 2 })
}

// Ctrl+R lists the folder again, as F5 does.
func TestCtrlRShowsAFileMadeSince(t *testing.T) {
	h := newHarness(t, "old.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	if err := os.WriteFile(filepath.Join(h.dir, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.w.Input(input.KeyPress{Key: input.KeyR, Mods: input.ModControl})
	h.until("Ctrl+R shows the new file", func() bool { return slices.Equal(h.shown(), []string{"new.txt", "old.txt"}) })
}

// The folder read again leaves the view where Page Down or the wheel
// put it, away from the row selected, and a name typed still brings
// its row into view.
func TestTheFolderReadAgainLeavesTheViewWhereItWas(t *testing.T) {
	names := make([]string, 0, 200)
	for i := range 200 {
		names = append(names, fmt.Sprintf("f%03d.txt", i))
	}
	h := newHarness(t, names...)
	h.until("the rows arrive", func() bool { return len(h.shown()) == 200 })
	h.do(FocusListing{})
	h.frames(2)
	g := h.b.listing.cur.grid
	settled := func() float64 {
		t.Helper()
		h.frames(90)
		return g.Top()
	}
	reread := func() {
		t.Helper()
		gen := h.a.nav.gen
		h.w.Input(input.KeyPress{Key: input.KeyF5, Time: time.Now()})
		h.until("F5 reads the folder again", func() bool { return h.a.nav.gen > gen })
	}

	h.press(input.KeyPageDown, 0)
	h.until("Page Down selects a row further on", func() bool { return h.a.nav.cursor > "f000.txt" })
	top := settled()
	reread()
	if got := settled(); got != top {
		t.Fatalf("after Page Down the view is at row %v, and read again at %v", top, got)
	}

	h.w.Input(input.Scroll{Pos: h.bounds(func(b *browser) gunim.Node { return b.listing }).Center(), Delta: geom.Pt(0, -2000), Notches: geom.Pt(0, -20), Time: time.Now()})
	top = settled()
	if top <= g.Visible() {
		t.Fatalf("the wheel left the view at row %v", top)
	}
	reread()
	if got := settled(); got != top {
		t.Fatalf("scrolled away, the view is at row %v, and read again at %v", top, got)
	}

	h.w.Input(input.TextInput{Text: "f00", Time: time.Now()})
	h.until("f00 goes to the first row", func() bool { return h.a.nav.cursor == "f000.txt" })
	if got := settled(); got != 0 {
		t.Fatalf("the name typed left the view at row %v, want it at the top", got)
	}
}
