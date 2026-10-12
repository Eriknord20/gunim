package filemanager

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/input"
)

// At zoom 1.25 the pane is narrow, and every link under the path still
// lies within it.
func TestThePreviewLinksFitThePaneWhenZoomed(t *testing.T) {
	h := newHarness(t, "notes.txt")
	if err := h.w.Client().SetZoom(1.25); err != nil {
		t.Fatal(err)
	}
	h.until("the preview shows the folder's links", func() bool {
		return h.b.preview.cur != nil && len(h.b.preview.cur.links) > 0
	})
	h.frames(60)
	pane := h.bounds(func(b *browser) gunim.Node { return b.preview })
	for i, l := range h.b.preview.cur.links {
		r := h.bounds(func(b *browser) gunim.Node { return b.preview.cur.links[i] })
		if r.Min.X < pane.Min.X || r.Max.X > pane.Max.X || r.Min.Y < pane.Min.Y || r.Max.Y > pane.Max.Y {
			t.Errorf("the link %q lies at %v, outside the pane at %v", l.Text, r, pane)
		}
	}
}

// The folder read again with nothing selected shows its preview again
// in place: the page does not slide in anew each time the folder
// changes, nor at all when nothing it shows did.
func TestTheFolderPreviewStaysPutAsTheFolderChanges(t *testing.T) {
	h := newHarness(t, "a.txt")
	pv := func() *previewPane { return h.b.preview }
	h.until("the preview shows the folder", func() bool {
		return pv().cur != nil && pv().shown.Type == "This folder" && pv().shown.Facts[0].Value == "1 item"
	})
	h.frames(120)
	top := pv().deck.top
	reread := func() {
		t.Helper()
		gen := h.a.nav.gen
		h.w.Input(input.KeyPress{Key: input.KeyF5, Time: time.Now()})
		h.until("F5 reads the folder again", func() bool { return h.a.nav.gen > gen })
		h.frames(2)
	}
	reread()
	if pv().deck.top != top {
		t.Fatal("the folder read again, unchanged, made a new preview page")
	}
	if err := os.WriteFile(filepath.Join(h.dir, "b.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// The folder's time is kept to the second on some file systems.
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(h.dir, later, later); err != nil {
		t.Fatal(err)
	}
	reread()
	h.until("the preview counts the new file", func() bool { return pv().shown.Facts[0].Value == "2 items" })
	if in := pv().deck.top.in.Value(); in < 0.999 {
		t.Fatalf("the folder's preview shown again is coming in, at %v, not in place", in)
	}
}
