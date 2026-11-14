package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
)

// With GrowDown, a body that folds open grows the panel down: its top stands still, where a centred panel's would
// move up by half as much.
func TestADialogThatGrowsDownKeepsItsTop(t *testing.T) {
	t.Parallel()
	for _, down := range []bool{false, true} {
		w := gunimtest.New(t, geom.Sz(800, 600), nil)
		var d *Dialog
		var fold *Fold
		var title *Label
		gunim.RegisterView(w, "grow", func(string) *Dialog {
			d = NewDialog("")
			d.GrowDown = down
			title = NewLabel("Above")
			fold = NewFold(NewLabel("One\nTwo\nThree\nFour\nFive\nSix"), false)
			d.Body = Column(title, fold)
			d.SetButtons("OK", "Cancel")
			return d
		}, nil)
		gunim.RegisterPatch(w, "grow", func(d *Dialog, f func(*Dialog, *gunim.UI), u *gunim.UI) { f(d, u) })
		c := w.Client()
		if err := c.Mount(gunim.Root, "grow", "grow", ""); err != nil {
			t.Fatal(err)
		}
		step := func(n int) {
			for range n {
				w.Frame(time.Second / 60)
			}
		}
		top := func() float32 {
			var r geom.Rect
			if err := c.Patch("grow", func(_ *Dialog, u *gunim.UI) { r, _ = u.Bounds(title) }); err != nil {
				t.Fatal(err)
			}
			step(1)
			return r.Min.Y
		}
		step(60)
		was := top()
		if err := c.Patch("grow", func(_ *Dialog, u *gunim.UI) { fold.SetOpen(true, u) }); err != nil {
			t.Fatal(err)
		}
		step(4)
		mid := top()
		step(60)
		now := top()
		switch {
		case down && (mid != was || now != was):
			t.Fatalf("growing down, the words above moved from %v to %v, then %v", was, mid, now)
		case !down && now >= was:
			t.Fatalf("centred, the words above stayed at %v as the panel grew", now)
		}
	}
}

// A toast's words come in behind its card: none till it is four fifths in, then less of the way than it till near
// the end, and all of it as it stands. They are drawn inside the card's own fade, so they never show more than it.
func TestAToastsWordsNeverComeAheadOfItsCard(t *testing.T) {
	t.Parallel()
	last := float32(0)
	for i := range 101 {
		card := float32(i) / 100
		c := toastContent(card)
		if c < last || (card < 0.95 && c > card) {
			t.Fatalf("with the card %v in, the words are %v in", card, c)
		}
		last = c
	}
	if toastContent(0.8) != 0 || toastContent(1) != 1 {
		t.Fatalf("the words are %v in at four fifths and %v at the end", toastContent(0.8), toastContent(1))
	}
}
