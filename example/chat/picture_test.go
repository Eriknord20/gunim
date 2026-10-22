package main

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/marrasen/gunim/chat"
	"github.com/marrasen/gunim/input"
)

// pngOf returns a w by h picture as PNG.
func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// pasteImage puts a w by h picture on the clipboard and presses Ctrl+V in the message box.
func (h *harness) pasteImage(w, ht int) {
	_ = h.w.Offscreen().SetClipboardImage(pngOf(h.t, w, ht))
	h.w.Input(input.KeyPress{Key: input.KeyV, Mods: input.ModControl})
	h.frames(3)
}

func TestAPastedPictureWaitsThenGoesWithTheMessage(t *testing.T) {
	h := newHarness(t)
	h.pasteImage(800, 600)
	if n := len(h.a.pending); n != 1 {
		t.Fatalf("%d pictures waiting, want 1", n)
	}
	if s := h.a.state(); len(s.Pending) != 1 || s.Images[s.Pending[0].ID] == nil {
		t.Fatalf("the state holds %d pictures waiting, want the one pasted", len(s.Pending))
	}
	h.typeAndSend("Look")
	it, ok := h.item("Look")
	if !ok || len(it.Pictures) != 1 || it.Pictures[0].W != 800 || it.Pictures[0].H != 600 {
		t.Fatalf("sent message %+v, want one 800 by 600 picture", it)
	}
	if n := len(h.a.pending); n != 0 {
		t.Fatalf("%d pictures still waiting after sending", n)
	}
}

func TestAPictureCanGoAlone(t *testing.T) {
	h := newHarness(t)
	h.pasteImage(40, 30)
	h.w.Input(input.KeyPress{Key: input.KeyEnter})
	h.frames(3)
	last := h.a.current.msgs[len(h.a.current.msgs)-1]
	if last.Author != me || last.Body != "" || len(last.Pictures) != 1 {
		t.Fatalf("last message %+v, want the picture alone", last)
	}
}

func TestAWaitingPictureCanBeTakenOff(t *testing.T) {
	h := newHarness(t)
	h.pasteImage(40, 30)
	h.pasteImage(50, 30)
	h.a.handle(chat.PictureRemoved{ID: h.a.pending[0].ID})
	h.frames(30)
	if len(h.a.pending) != 1 || h.a.pending[0].W != 50 {
		t.Fatalf("waiting %+v, want the second alone", h.a.pending)
	}
}
