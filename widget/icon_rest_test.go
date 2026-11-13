package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/icon"
)

// A spinning icon keeps a window drawing while it shows. Scrolled out of
// sight, it lets the window rest; scrolled back, it spins again.
func TestASpinnerScrolledOutOfSightLetsTheWindowRest(t *testing.T) {
	spin := NewIcon(icon.Loader2, "Loading")
	spin.Spin = true
	s := NewScroll(Column(spin, newSpot(300, 2000)))
	w := gunimtest.New(t, geom.Sz(300, 400), nil)
	// Every frame paints, as on a screen: what showed is what was painted.
	w.PaintWhenLooked(false)
	gunim.RegisterView(w, "page", func(struct{}) gunim.Node { return s }, nil)
	if err := w.Client().Mount(gunim.Root, "page", "page", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run := func(n int) bool {
		for range n {
			w.Frame(time.Second / 60)
		}
		return w.Still()
	}
	if run(10) {
		t.Fatal("a window with a spinner in sight is still")
	}
	s.ScrollTo(1000, Quick.Default())
	if !run(60) {
		t.Fatal("a window whose spinner is scrolled out of sight is not still")
	}
	s.ScrollTo(0, Quick.Default())
	run(60)
	if run(5) {
		t.Fatal("a window with its spinner scrolled back into sight is still")
	}
}
