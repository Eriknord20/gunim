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
