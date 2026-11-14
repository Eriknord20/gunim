package widget

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// toastFrame stages toasts in a box 300 by 400, and returns a way to
// run code on the window's UI, and to run frames with a check after
// each.
func toastFrame(t *testing.T, ts *Toasts) (w *gunim.Window, on func(func(*gunim.UI)), frames func(n int, check func())) {
	t.Helper()
	w, run := stage(t, &frame{child: ts, size: geom.Sz(300, 400)})
	type do struct{ fn func(u *gunim.UI) }
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, d do, u *gunim.UI) { d.fn(u) })
	on = func(fn func(u *gunim.UI)) {
		if err := w.Client().Patch("stage", do{fn}); err != nil {
			t.Fatal(err)
		}
		run(1)
	}
	frames = func(n int, check func()) {
		for range n {
			run(1)
			check()
		}
	}
	return w, on, frames
}

// asking is a toast that asks, so it stays until answered.
func asking(i int) Toast {
	return Toast{Title: "Question " + strconv.Itoa(i), Body: "Keep it?",
		Buttons: []ToastButton{{Label: "Yes"}, {Label: "No"}}}
}

func TestToastsThatPileUpKeepWithinTheHeightGiven(t *testing.T) {
	ts := &Toasts{}
	w, on, frames := toastFrame(t, ts)
	inside := func() {
		t.Helper()
		for i, c := range ts.cards {
			if c.shown.Target() != 1 {
				continue
			}
			if top, bottom := c.y.Value(), c.y.Value()+c.size.H; top < -0.5 || bottom > 400.5 {
				t.Fatalf("toast %d of %d spans %v to %v, outside the 400 px given", i, len(ts.cards), top, bottom)
			}
		}
	}
	for i := range 12 {
		on(func(u *gunim.UI) { ts.Show(asking(i), u) })
		frames(3, inside)
	}
	frames(60, inside)
	shown := func() (n int, newest int) {
		newest = -1
		for i, c := range ts.cards {
			if c.shown.Target() == 1 {
				n++
				newest = i
			}
		}
		return n, newest
	}
	n, newest := shown()
	if n == 0 || n >= 12 || newest != 11 {
		t.Fatalf("%d of 12 toasts show, the newest shown %d; want the newest few", n, newest)
	}
	if ts.more.count != 12-n || ts.more.in.Value() < 0.99 {
		t.Fatalf("the card over the stack counts %d, shown %v; want %d", ts.more.count, ts.more.in.Value(), 12-n)
	}
	// A click on it shows the older ones in their place.
	click(w, 150, ToastMoreHeight.Default()/2)
	frames(60, inside)
	if _, older := shown(); older >= 12-n {
		t.Fatalf("after a click on the count, the newest toast shown is %d, want one older than %d", older, 12-n)
	}
	// A new toast brings the newest back.
	on(func(u *gunim.UI) { ts.Show(asking(12), u) })
	frames(60, inside)
	if _, newest := shown(); newest != 12 {
		t.Fatalf("after a new toast, the newest shown is %d, want 12", newest)
	}
}

func TestALongToastBodyShowsThreeLinesAndAllOfThemUnderThePointer(t *testing.T) {
	ts := &Toasts{Life: time.Minute}
	w, on, frames := toastFrame(t, ts)
	body := strings.Repeat("A sentence of the long body. ", 40)
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "Long", Body: body}, u) })
	frames(30, func() {})
	c := ts.cards[0]
	if lines := len(c.face.body.laid.p.Lines); lines != toastBodyLines || !c.face.body.laid.p.Truncated {
		t.Fatalf("the body shows %d lines, cut %v; want %d, cut", lines, c.face.body.laid.p.Truncated, toastBodyLines)
	}
	short := c.size.H
	w.Input(input.PointerMove{Pos: geom.Pt(150, c.y.Value()+10), Time: time.Now()})
	frames(30, func() {
		if c.size.H > 400.5 {
			t.Fatalf("under the pointer the toast is %v tall, past the 400 px given", c.size.H)
		}
	})
	if lines := len(c.face.body.laid.p.Lines); lines <= toastBodyLines || c.size.H <= short {
		t.Fatalf("under the pointer the body shows %d lines and the toast is %v tall, was %v", lines, c.size.H, short)
	}
}
