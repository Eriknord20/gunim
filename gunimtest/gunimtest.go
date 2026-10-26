// Package gunimtest helps test an interface built with gunim.
//
// Its windows are offscreen windows that fail the test that made them
// if, by the test's end, anything sent from a node or focused a node
// that was not in the tree. Such a call does nothing for the node, and
// what a test sees of it is a later symptom, an intent that never came
// or focus that stayed put, far from the cause. The failure names the
// call, the node's type and where the call came from.
package gunimtest

import (
	"errors"
	"image"
	"image/png"
	"os"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
)

// New returns an offscreen window of size with root at the top, as
// [gunim.NewOffscreen] does, which fails t at the end of the test for
// each stray it kept.
func New(t testing.TB, size geom.Size, root gunim.Node) *gunim.Window {
	t.Helper()
	w := gunim.NewOffscreen(size, root)
	t.Cleanup(w.Close)
	t.Cleanup(func() { Check(t, w) })
	return w
}

// NewWith returns an offscreen window set up by o, as
// [gunim.NewOffscreenWith] does, which fails t at the end of the test
// for each stray it kept.
func NewWith(t testing.TB, o gunim.WindowOptions) *gunim.Window {
	t.Helper()
	w := gunim.NewOffscreenWith(o)
	t.Cleanup(w.Close)
	t.Cleanup(func() { Check(t, w) })
	return w
}

// Check fails t for each stray w has kept since it was last asked. A
// test that expects a stray takes it with [gunim.Window.Strays] before
// the test ends.
func Check(t testing.TB, w *gunim.Window) {
	t.Helper()
	for _, s := range w.Strays() {
		t.Error(s)
	}
}

// Pixels returns w's last frame drawn, as [gunim.Window.Pixels] does, and skips t where nothing can draw it.
func Pixels(t testing.TB, w *gunim.Window) *image.RGBA {
	t.Helper()
	img, err := w.Pixels()
	if errors.Is(err, gunim.ErrNoPixels) {
		t.Skipf("no pixels to look at: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// WritePNG writes img to path as a PNG, for a person to look at, and fails t where it cannot.
func WritePNG(t testing.TB, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
