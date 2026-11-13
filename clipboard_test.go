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

// Sensitive text reaches the clipboard marked so, and comes off again only while it is still what the clipboard holds.
func TestSensitiveTextComesOffTheClipboardOnlyWhileItIsStillThere(t *testing.T) {
	w := newTestWindow()
	if err := w.ui.SetClipboardSensitive("hunter2"); err != nil {
		t.Fatal(err)
	}
	if s, _ := w.ui.ReadClipboard(); s != "hunter2" || !w.Offscreen().ClipboardSensitive() {
		t.Fatalf("the clipboard holds %q, sensitive %v; want the password, marked", s, w.Offscreen().ClipboardSensitive())
	}
	// The user copies something else: clearing leaves it alone.
	w.ui.SetClipboard("a path")
	if w.Offscreen().ClipboardSensitive() {
		t.Fatal("text copied the plain way is marked sensitive")
	}
	if cleared, err := w.ui.ClearClipboardIf("hunter2"); cleared || err != nil {
		t.Fatalf("clearing the password once the user copied else cleared %v, %v", cleared, err)
	}
	if s, _ := w.ui.ReadClipboard(); s != "a path" {
		t.Fatalf("the clipboard holds %q, want what the user copied", s)
	}
	// Still there: it comes off.
	if err := w.ui.SetClipboardSensitive("hunter2"); err != nil {
		t.Fatal(err)
	}
	if cleared, err := w.ui.ClearClipboardIf("hunter2"); !cleared || err != nil {
		t.Fatalf("clearing the password still on the clipboard cleared %v, %v", cleared, err)
	}
	if s, _ := w.ui.ReadClipboard(); s != "" {
		t.Fatalf("after clearing, the clipboard holds %q", s)
	}
}

// A clipboard that cannot be reached says so, to both calls, rather than reporting nothing to clear.
func TestASensitiveClipboardThatCannotBeReachedSaysSo(t *testing.T) {
	for _, plain := range []bool{false, true} {
		off := driver.Offscreen(geom.Sz(100, 100))
		var dw driver.Window = off
		if plain {
			dw = textOnly{off}
		}
		w := newWindow(dw, nil)
		off.SetClipboardError(errors.New("no display"))
		if err := w.ui.SetClipboardSensitive("hunter2"); err == nil {
			t.Errorf("plain %v: a sensitive copy that failed said nothing", plain)
		}
		if cleared, err := w.ui.ClearClipboardIf("hunter2"); cleared || err == nil {
			t.Errorf("plain %v: clearing a clipboard that could not be read gave %v, %v; want an error", plain, cleared, err)
		}
	}
}

// A window with no way to mark text sensitive puts it there plainly, and clears it by comparing.
func TestSensitiveTextOnAPlainClipboardIsSetAndCleared(t *testing.T) {
	off := driver.Offscreen(geom.Sz(100, 100))
	w := newWindow(textOnly{off}, nil)
	if err := w.ui.SetClipboardSensitive("hunter2"); err != nil {
		t.Fatal(err)
	}
	if s, _ := off.Clipboard(); s != "hunter2" || off.ClipboardSensitive() {
		t.Fatalf("the clipboard holds %q, sensitive %v; want the text, put plainly", s, off.ClipboardSensitive())
	}
	if cleared, err := w.ui.ClearClipboardIf("something else"); cleared || err != nil {
		t.Fatalf("clearing other text cleared %v, %v", cleared, err)
	}
	if cleared, err := w.ui.ClearClipboardIf("hunter2"); !cleared || err != nil {
		t.Fatalf("clearing the text there cleared %v, %v", cleared, err)
	}
	if s, _ := off.Clipboard(); s != "" {
		t.Fatalf("after clearing, the clipboard holds %q", s)
	}
}
