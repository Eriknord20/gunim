//go:build !windows

package vst3

import (
	"image"
	"os"
	"testing"
	"time"
)

// TestAnEditorEmbeds embeds an editor in a window of X's, as a host's,
// places it, hides it, and closes it before its parent, as a host must:
// a plugin whose windows X destroys under it may end the program, as
// Xlib does on an error no one handles.
func TestAnEditorEmbeds(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("no X display")
	}
	m := lsp(t)
	p, err := m.New(class(t, m, "Parametric Equalizer x16 Stereo"), Config{Rate: 44100})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var parent uintptr
	onUI(func() {
		if err = loadX(); err != nil {
			return
		}
		parent = x.CreateSimpleWindow(x.dpy, x.DefaultRootWindow(x.dpy), 0, 0, 1400, 900, 0, 0, 0)
		x.MapRaised(x.dpy, parent)
		x.Flush(x.dpy)
	})
	if err != nil {
		t.Skip("no X:", err)
	}
	sizes := make(chan [2]int, 8)
	w, h, err := p.EmbedEditor(parent, func(w, h int) { sizes <- [2]int{w, h} })
	if err != nil {
		t.Fatal(err)
	}
	if w <= 0 || h <= 0 {
		t.Fatalf("the editor embedded at %dx%d", w, h)
	}
	if !p.EditorOpen() {
		t.Fatal("embedded, the editor says it is closed")
	}
	pw, ph := p.PlaceEditor(image.Rect(20, 40, 20+w, 40+h))
	if pw != w || ph != h {
		t.Fatalf("placed at its own size, the editor took %dx%d, want %dx%d", pw, ph, w, h)
	}
	var child *window
	onUI(func() { child = p.ed.win })
	if !child.child || !p.EditorEmbedded() {
		t.Fatal("the editor's window is no child")
	}
	if os.Getenv("VST3_EDITOR") != "" {
		time.Sleep(4 * time.Second)
	}
	p.PlaceEditor(image.Rectangle{})
	p.CloseEditor()
	if p.EditorOpen() {
		t.Fatal("closed, the editor says it is open")
	}
	onUI(func() {
		x.DestroyWindow(x.dpy, parent)
		x.Flush(x.dpy)
	})
}
