package main

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/widget"
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
	if n := len(h.v.strip.thumbs); n != 1 {
		t.Fatalf("the strip shows %d pictures, want 1", n)
	}
	h.typeAndSend("Look")
	it, ok := h.item("Look")
	if !ok || len(it.Pictures) != 1 || it.Pictures[0].W != 800 || it.Pictures[0].H != 600 {
		t.Fatalf("sent message %+v, want one 800 by 600 picture", it)
	}
	h.frames(30)
	if n := len(h.a.pending) + len(h.v.strip.thumbs); n != 0 {
		t.Fatalf("%d pictures still waiting after sending", n)
	}
	n, ok := h.v.list.Row(widget.Key(it.Key))
	if !ok || len(n.(*msgRow).pictures) != 1 {
		t.Fatal("the message's row shows no picture")
	}
	if s := n.(*msgRow).pictures[0].Size; s.H != 260 || s.W < 346 || s.W > 347 {
		t.Fatalf("the picture shows %v, want it shrunk to 260 high, keeping its shape", s)
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
	h.a.handle(PictureRemoved{ID: h.a.pending[0].ID})
	h.frames(30)
	if len(h.a.pending) != 1 || h.a.pending[0].W != 50 || len(h.v.strip.thumbs) != 1 {
		t.Fatalf("waiting %+v and %d shown, want the second alone", h.a.pending, len(h.v.strip.thumbs))
	}
}
