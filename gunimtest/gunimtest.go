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
	t.Cleanup(func() { Check(t, w) })
	return w
}

// NewWith returns an offscreen window set up by o, as
// [gunim.NewOffscreenWith] does, which fails t at the end of the test
// for each stray it kept.
func NewWith(t testing.TB, o gunim.WindowOptions) *gunim.Window {
	t.Helper()
	w := gunim.NewOffscreenWith(o)
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
