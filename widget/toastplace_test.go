package widget

import (
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// leastSolid returns how solid the frame drew the least solid of its shapes, text left out, through the layers
// round each: the cards, with toasts that show no icon and no button.
func leastSolid(ops []paint.Op) float32 {
	stack := []float32{1}
	least := float32(1)
	for _, op := range ops {
		at := stack[len(stack)-1]
		switch o := op.(type) {
		case *paint.LayerOp:
			stack = append(stack, at*o.Opts.Opacity)
		case *paint.LayerEndOp:
			stack = stack[:len(stack)-1]
		case *paint.TextOp:
		default:
			least = min(least, at)
		}
	}
	return least
}

// A toast shown with the key of an older toast, with newer ones under it, keeps that toast's card and its place in
// the stack. The card stays solid, its words hand over, and it glides to the new words' height. The toasts round it
// glide to make room: none of them jumps, and none overlaps another.
func TestAnOlderToastSwapsInPlaceAndItsNeighboursGlide(t *testing.T) {
	ts := &Toasts{Life: time.Minute}
	w, on, frames := toastFrame(t, ts)
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "Above"}, u) })
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "Exporting", Key: "k"}, u) })
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "Below"}, u) })
	frames(60, func() {})
	cards := []*toastCard{ts.cards[0], ts.cards[1], ts.cards[2]}
	card := cards[1]
	gap := ToastGap.Default()
	for _, to := range []Toast{
		{Title: "Exported song.wav", Body: "Loudness −19.3 LUFS · range 0.5 LU · true peak −5.7 dBTP, and more words to wrap on", Key: "k"},
		{Title: "Exporting again", Key: "k"},
	} {
		old, rest := card.face, card.size.H
		var y, h [3]float32
		for i, c := range cards {
			y[i], h[i] = c.y.Value(), c.size.H
		}
		on(func(u *gunim.UI) { ts.Show(to, u) })
		if len(ts.cards) != 3 || ts.cards[0] != cards[0] || ts.cards[1] != card || ts.cards[2] != cards[2] || card.face == old {
			t.Fatalf("%q: the stack holds %d cards, the toast on its own card in its place %v, with a new face %v",
				to.Title, len(ts.cards), len(ts.cards) == 3 && ts.cards[1] == card, card.face != old)
		}
		mid, crossed := false, false
		for range 90 {
			on(func(u *gunim.UI) {
				if solid := leastSolid(w.Offscreen().Ops()); solid < 0.99 {
					t.Fatalf("%q: a card is only %v solid as the words change", to.Title, solid)
				}
				o, n := old.alpha(), card.face.alpha()
				if o+n < 0.99 || o+n > 1.01 {
					t.Fatalf("%q: the old words are %v solid and the new %v, want 1 together", to.Title, o, n)
				}
				crossed = crossed || o > 0.2 && n > 0.2
				for i, c := range cards {
					if c.in.Value() < 0.999 || c.shown.Value() < 0.999 {
						t.Fatalf("%q: toast %d is %v in and %v shown: it left and came back", to.Title, i, c.in.Value(), c.shown.Value())
					}
					if dy, dh := c.y.Value()-y[i], c.size.H-h[i]; dy < -12 || dy > 12 || dh < -12 || dh > 12 {
						t.Fatalf("%q: toast %d moved %v and grew %v in a frame", to.Title, i, dy, dh)
					}
					y[i], h[i] = c.y.Value(), c.size.H
					if i == 0 {
						continue
					}
					over := cards[i-1]
					if room := c.y.Value() - (over.y.Value() + over.size.H); room < gap-1 || room > gap+1 {
						t.Fatalf("%q: toast %d stands %v under the one over it, want %v", to.Title, i, room, gap)
					}
				}
				// The stack ends where its newest toast does.
				stack, _ := u.Bounds(ts)
				if foot := cards[2].y.Value() + cards[2].size.H; foot < stack.Size().H-0.5 || foot > stack.Size().H+0.5 {
					t.Fatalf("%q: the newest toast's foot is at %v in a stack %v tall", to.Title, foot, stack.Size().H)
				}
				if d := card.size.H - rest; (d > 3 || d < -3) && card.h.Active() {
					mid = true
				}
			})
		}
		if !mid || !crossed {
			t.Fatalf("%q: the card glided %v, and its words rolled past each other %v", to.Title, mid, crossed)
		}
		want := card.face.size.H
		if d := want - rest; d > -10 && d < 10 {
			t.Fatalf("%q: the new words are %v tall, the old %v: the test needs them to differ", to.Title, want, rest)
		}
		if card.faces != 1 || card.h.Active() || card.size.H < want-0.5 || card.size.H > want+0.5 {
			t.Fatalf("%q: at rest the card holds %d faces and is %v tall, its words %v", to.Title, card.faces, card.size.H, want)
		}
		for i, c := range cards {
			if c.y.Active() {
				t.Fatalf("%q: toast %d still moves at rest", to.Title, i)
			}
		}
	}
}

// A toast that takes the place of one the stack has no room for arrives as the newest, on a card of its own, so
// that it is seen.
func TestAToastThatTakesAHiddenOnesPlaceArrivesAsTheNewest(t *testing.T) {
	ts := &Toasts{Life: time.Minute}
	_, on, frames := toastFrame(t, ts)
	on(func(u *gunim.UI) {
		ts.Show(Toast{Title: "Old", Key: "a", Buttons: []ToastButton{{Label: "Yes"}}}, u)
	})
	for i := range 8 {
		on(func(u *gunim.UI) { ts.Show(asking(i), u) })
	}
	frames(60, func() {})
	old := ts.cards[0]
	if old.shown.Target() != 0 {
		t.Fatal("the stack has room for all nine toasts: the test needs the oldest hidden")
	}
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "New", Key: "a"}, u) })
	if len(ts.cards) != 9 || ts.cards[0] == old || ts.cards[8].face.title.Text != "New" {
		t.Fatalf("the stack holds %d cards, the old one still first %v, the last reading %q",
			len(ts.cards), ts.cards[0] == old, ts.cards[len(ts.cards)-1].face.title.Text)
	}
	frames(60, func() {})
	if c := ts.cards[8]; c.shown.Target() != 1 || c.in.Value() < 0.999 {
		t.Fatalf("the new toast is shown %v and %v in", c.shown.Target(), c.in.Value())
	}
}

// focusStage stages root, and returns what toastFrame does, and focused, which reads where the keyboard is.
func focusStage(t *testing.T, root gunim.Node) (w *gunim.Window, on func(func(*gunim.UI)), frames func(int), focused func() gunim.Node) {
	t.Helper()
	w, run := stage(t, &frame{child: root, size: geom.Sz(300, 400)})
	type do struct{ fn func(u *gunim.UI) }
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, d do, u *gunim.UI) { d.fn(u) })
	on = func(fn func(u *gunim.UI)) {
		if err := w.Client().Patch("stage", do{fn}); err != nil {
			t.Fatal(err)
		}
		run(1)
	}
	focused = func() (n gunim.Node) {
		on(func(u *gunim.UI) { n = u.Focused() })
		return n
	}
	return w, on, run, focused
}

// tabTo presses Tab until the keyboard is on n.
func tabTo(t *testing.T, w *gunim.Window, frames func(int), focused func() gunim.Node, n gunim.Node) {
	t.Helper()
	for range 12 {
		tab(w, frames, 0)
		if focused() == n {
			return
		}
	}
	t.Fatal("Tab never reached the control")
}

// heardToast is a toast that asks, whose buttons and close button write what was clicked to heard, after name.
func heardToast(name, title string, heard *[]string, labels ...string) Toast {
	to := Toast{Title: title, Key: "k", OnDismiss: func(bool, *gunim.UI) gunim.Intent {
		*heard = append(*heard, name+" closed")
		return nil
	}}
	for _, l := range labels {
		to.Buttons = append(to.Buttons, ToastButton{Label: l, OnClick: func(bool, *gunim.UI) gunim.Intent {
			*heard = append(*heard, name+" "+l)
			return nil
		}})
	}
	return to
}

// The keyboard on a toast's button stays on the button of the same name as another toast takes the card, wherever
// that button stands on the new toast. Its focus ring shows there, and Enter is the new toast's.
func TestTheKeyboardFollowsAToastsButtonThroughASwap(t *testing.T) {
	ts := &Toasts{Life: time.Minute}
	w, on, frames, focused := focusStage(t, ts)
	var heard []string
	on(func(u *gunim.UI) { ts.Show(heardToast("first", "Open the song it came with?", &heard, "Open", "Later"), u) })
	frames(60)
	card := ts.cards[0]
	old := card.face
	was := old.buttons.Children()[0].(*Button)
	tabTo(t, w, frames, focused, was)
	frames(30)
	if was.ring.Value() < 0.9 {
		t.Fatalf("Tab left the button's focus ring at %v", was.ring.Value())
	}
	on(func(u *gunim.UI) { ts.Show(heardToast("second", "Open the newer song?", &heard, "Later", "Open"), u) })
	now := card.face.buttons.Children()[1].(*Button)
	if card.face == old || now.Label != "Open" {
		t.Fatalf("the toast kept its face %v; its second button reads %q", card.face == old, now.Label)
	}
	// On every frame of the handover the keyboard is on the new toast's Open.
	for i := range 40 {
		if got := focused(); got != gunim.Node(now) {
			t.Fatalf("frame %d of the swap: the keyboard is on %T %v, want the new toast's Open", i, got, got)
		}
		if i == 3 && card.faces != 2 {
			t.Fatalf("the card holds %d faces three frames in: the handover is over", card.faces)
		}
	}
	if now.ring.Value() < 0.9 || was.ring.Target() != 0 {
		t.Fatalf("the new button's focus ring is at %v, the old one's goes to %v", now.ring.Value(), was.ring.Target())
	}
	w.Input(input.KeyPress{Key: input.KeyEnter})
	frames(2)
	if len(heard) != 1 || heard[0] != "second Open" || ts.Len() != 0 {
		t.Fatalf("Enter was heard as %v, leaving %d toasts", heard, ts.Len())
	}
}

// Enter pressed while the old toast's words still show is the new toast's: the old button is out of reach from the
// frame the new toast is shown.
func TestEnterMidSwapIsTheNewToasts(t *testing.T) {
	ts := &Toasts{Life: time.Minute}
	w, on, frames, focused := focusStage(t, ts)
	var heard []string
	on(func(u *gunim.UI) { ts.Show(heardToast("first", "Remove the take?", &heard, "Remove"), u) })
	frames(60)
	card := ts.cards[0]
	old := card.face
	tabTo(t, w, frames, focused, old.buttons.Children()[0])
	on(func(u *gunim.UI) { ts.Show(heardToast("second", "Remove the lane?", &heard, "Remove"), u) })
	if old.alpha() < 0.5 || card.faces != 2 {
		t.Fatalf("the old words are %v solid on a card of %d faces: the handover is over", old.alpha(), card.faces)
	}
	w.Input(input.KeyPress{Key: input.KeyEnter})
	frames(2)
	if len(heard) != 1 || heard[0] != "second Remove" {
		t.Fatalf("Enter mid swap was heard as %v", heard)
	}
}

// Where the new toast has no button of that name, the keyboard goes to its close button, so Tab goes on from the
// toast. The old button hears nothing.
func TestTheKeyboardGoesToTheCloseButtonOfAToastWithoutTheButton(t *testing.T) {
	ts := &Toasts{Life: time.Minute}
	w, on, frames, focused := focusStage(t, ts)
	var heard []string
	on(func(u *gunim.UI) { ts.Show(heardToast("first", "Open the song it came with?", &heard, "Open"), u) })
	frames(60)
	card := ts.cards[0]
	tabTo(t, w, frames, focused, card.face.buttons.Children()[0])
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "Could not open the song", Kind: ToastError, Key: "k"}, u) })
	if got := focused(); card.face.close == nil || got != gunim.Node(card.face.close) {
		t.Fatalf("the keyboard is on %T %v, want the new toast's close button", got, got)
	}
	// Another toast with other buttons: the close button hands on to the close button.
	on(func(u *gunim.UI) { ts.Show(heardToast("third", "Try another song?", &heard, "Browse"), u) })
	if got := focused(); got != gunim.Node(card.face.close) {
		t.Fatalf("the keyboard is on %T %v, want the third toast's close button", got, got)
	}
	w.Input(input.KeyPress{Key: input.KeyEnter})
	frames(2)
	if len(heard) != 1 || heard[0] != "third closed" || ts.Len() != 0 {
		t.Fatalf("Enter on the close button was heard as %v, leaving %d toasts", heard, ts.Len())
	}
}

// Where the new toast has nothing to take the keyboard, the keyboard is on nothing: Enter and Space reach neither
// the old button nor the toast, and Tab starts again from the window's first control.
func TestTheKeyboardLeavesAToastThatSwapsToOneWithNoControls(t *testing.T) {
	ts := &Toasts{Life: time.Minute}
	other := NewButton("Other")
	w, on, frames, focused := focusStage(t, Column(other, ts))
	var heard []string
	on(func(u *gunim.UI) { ts.Show(heardToast("first", "Open the song it came with?", &heard, "Open"), u) })
	frames(60)
	card := ts.cards[0]
	old := card.face
	tabTo(t, w, frames, focused, old.buttons.Children()[0])
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "Opened the song", Key: "k"}, u) })
	for i := range 40 {
		if got := focused(); got != nil {
			t.Fatalf("frame %d of the swap: the keyboard is on %T %v, want nothing", i, got, got)
		}
		if i == 2 {
			// The old button still shows, part clear.
			if old.alpha() < 0.3 {
				t.Fatalf("the old words are only %v solid two frames in", old.alpha())
			}
			w.Input(input.KeyPress{Key: input.KeyEnter})
			w.Input(input.KeyPress{Key: input.KeySpace})
		}
	}
	if len(heard) != 0 || ts.Len() != 1 {
		t.Fatalf("keys after the swap were heard as %v, leaving %d toasts", heard, ts.Len())
	}
	tab(w, frames, 0)
	if got := focused(); got != gunim.Node(other) {
		t.Fatalf("Tab after the swap went to %T %v, want the window's first control", got, got)
	}
}

// The keyboard elsewhere in the window stays where it is as a toast swaps.
func TestASwapLeavesTheKeyboardElsewhereAlone(t *testing.T) {
	ts := &Toasts{Life: time.Minute}
	other := NewButton("Other")
	w, on, frames, focused := focusStage(t, Column(other, ts))
	var heard []string
	on(func(u *gunim.UI) { ts.Show(heardToast("first", "Open the song it came with?", &heard, "Open"), u) })
	frames(60)
	tabTo(t, w, frames, focused, other)
	on(func(u *gunim.UI) { ts.Show(heardToast("second", "Open the newer song?", &heard, "Open"), u) })
	frames(40)
	if got := focused(); got != gunim.Node(other) {
		t.Fatalf("the swap moved the keyboard to %T %v", got, got)
	}
}

// A press held on a toast's button as another toast takes the card does nothing on release, though the new toast's
// button stands under the pointer: a click is a press and a release on the same button. The next whole click lands.
func TestAPressHeldOnAButtonThroughASwapDoesNothing(t *testing.T) {
	ts := &Toasts{Life: time.Minute}
	w, on, frames, _ := focusStage(t, ts)
	var heard []string
	// Under the words, both toasts' buttons stand in the same place.
	body := strings.Repeat("A sentence of the body. ", 3)
	first := heardToast("first", "Open the song it came with?", &heard, "Open", "Later")
	second := heardToast("second", "Open the newer song now?", &heard, "Open", "Later")
	first.Body, second.Body, first.Check, second.Check = body, body, "Don't ask again", "Don't ask me again"
	on(func(u *gunim.UI) { ts.Show(first, u) })
	frames(60)
	card := ts.cards[0]
	old := card.face
	at := boundsOf(on, old.buttons.Children()[0])[0].Center()
	w.Input(input.PointerMove{Pos: at, Time: time.Now()})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	frames(2)
	on(func(u *gunim.UI) { ts.Show(second, u) })
	now := card.face.buttons.Children()[0]
	if card.face == old {
		t.Fatal("the second toast kept the first one's face")
	}
	if r := boundsOf(on, now)[0]; !r.Contains(at) {
		t.Fatalf("the new toast's button at %v is not under the pointer at %v: the test needs it there", r, at)
	}
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary, Time: time.Now()})
	frames(2)
	if len(heard) != 0 || ts.Len() != 1 {
		t.Fatalf("a press held through the swap was heard as %v, leaving %d toasts", heard, ts.Len())
	}
	if b := now.(*Button); b.held || b.squash.Target() != 0 {
		t.Fatalf("the new button is held %v, squashed to %v", b.held, b.squash.Target())
	}
	click(w, at.X, at.Y)
	frames(2)
	if len(heard) != 1 || heard[0] != "second Open" {
		t.Fatalf("a whole click after the swap was heard as %v", heard)
	}
}
