package gunim

import (
	"errors"
	"testing"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

// ReadClipboard tells an empty clipboard from one that could not be
// read; Clipboard reads both as nothing.
func TestAClipboardThatCannotBeReadSaysSo(t *testing.T) {
	w := newTestWindow()
	if s, err := w.ui.ReadClipboard(); s != "" || err != nil {
		t.Fatalf("empty, the clipboard reads %q, %v", s, err)
	}
	w.Offscreen().SetClipboardError(errors.New("no display"))
	if _, err := w.ui.ReadClipboard(); err == nil {
		t.Fatal("a clipboard that could not be read said nothing")
	}
	if s := w.ui.Clipboard(); s != "" {
		t.Fatalf("Clipboard read %q", s)
	}
}

// A picture put on the clipboard, from the UI or from the application's side, is the picture read back.
func TestAPicturePutOnTheClipboardReadsBack(t *testing.T) {
	w := newTestWindow()
	if err := w.ui.SetClipboardImage([]byte("first")); err != nil {
		t.Fatal(err)
	}
	if b, err := w.ui.ClipboardImage(); err != nil || string(b) != "first" {
		t.Fatalf("the clipboard reads %q, %v", b, err)
	}
	if err := w.Client().SetClipboardImage([]byte("second")); err != nil {
		t.Fatal(err)
	}
	if b, err := w.Offscreen().ClipboardImage(); err != nil || string(b) != "second" {
		t.Fatalf("the clipboard reads %q, %v", b, err)
	}
}

// textOnly is a window whose clipboard holds text alone.
type textOnly struct{ driver.Window }

// Where the window cannot put a picture on the clipboard, it says so.
func TestAClipboardThatTakesNoPictureSaysSo(t *testing.T) {
	w := newWindow(textOnly{driver.Offscreen(geom.Sz(100, 100))}, nil)
	if err := w.Client().SetClipboardImage([]byte("png")); !errors.Is(err, driver.ErrNoClipboardImage) {
		t.Fatalf("got %v, want ErrNoClipboardImage", err)
	}
	if err := w.ui.SetClipboardImage([]byte("png")); !errors.Is(err, driver.ErrNoClipboardImage) {
		t.Fatalf("got %v, want ErrNoClipboardImage", err)
	}
}

// Text the clipboard will not take is said to the application, rather
// than dropped: the copy did nothing, and the user is to be told.
func TestAClipboardThatTakesNoTextSaysSo(t *testing.T) {
	w := newTestWindow()
	c := w.Client()
	for len(c.Intents()) > 0 {
		<-c.Intents()
	}
	w.Offscreen().SetClipboardError(errors.New("no display"))
	w.ui.SetClipboard("copied")
	if f, ok := As[ClipboardFailed](take(t, c)); !ok || f.Reason != "no display" {
		t.Fatalf("a copy that failed sent %#v", f)
	}
	w.Offscreen().SetClipboardError(nil)
	w.ui.SetClipboard("copied")
	if s := w.ui.Clipboard(); s != "copied" {
		t.Fatalf("the clipboard reads %q", s)
	}
	if n := len(c.Intents()); n != 0 {
		t.Fatalf("a copy that worked sent %d intents", n)
	}
}
