package widget

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// drawnAlpha returns how solid the frame drew the first shape, which is the toast's card, and the most solid of
// its text, each through the layers round it.
func drawnAlpha(ops []paint.Op) (card, words float32) {
	stack := []float32{1}
	card = -1
	for _, op := range ops {
		at := stack[len(stack)-1]
		switch o := op.(type) {
		case *paint.LayerOp:
			stack = append(stack, at*o.Opts.Opacity)
		case *paint.LayerEndOp:
			stack = stack[:len(stack)-1]
		case *paint.TextOp:
			words = max(words, at*float32(o.Color.A)/255)
		default:
			if card < 0 {
				card = at
			}
		}
	}
	return max(card, 0), words
}

// A toast shown with the key of the newest keeps that toast's card: the card stays solid, some words show on every
// frame, and the card glides to the new words' height with its foot where it was.
func TestAToastThatTakesAnothersPlaceNeverShowsABareCard(t *testing.T) {
	ts := &Toasts{Life: time.Minute}
	w, on, frames := toastFrame(t, ts)
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "Exporting", Key: "k", Kind: ToastInfo}, u) })
	// A new toast's words wait for its card, as before.
	bare := 0
	frames(60, func() {
		if card, words := drawnAlpha(w.Offscreen().Ops()); words > 0.01 && card < 0.75 {
			t.Fatalf("arriving, the words show %v on a card only %v in", words, card)
		} else if words < 0.01 {
			bare++
		}
	})
	if bare == 0 {
		t.Fatal("a new toast's words did not wait for its card")
	}
	card := ts.cards[0]
	old := card.face
	was := card.size.H
	on(func(u *gunim.UI) {
		ts.Show(Toast{Title: "Exported song.wav", Body: "Loudness −19.3 LUFS · range 0.5 LU · true peak −5.7 dBTP, and more words to wrap on",
			Key: "k", Kind: ToastWarning}, u)
	})
	if len(ts.cards) != 1 || ts.cards[0] != card || card.face == old {
		t.Fatalf("the toast took a new card: %d cards, the same card %v, a new face %v", len(ts.cards), ts.cards[0] == card, card.face != old)
	}
	mid := false
	// Each frame is checked on the UI's side, where the stack's own box can be read.
	for range 90 {
		on(func(u *gunim.UI) {
			solid, words := drawnAlpha(w.Offscreen().Ops())
			if solid < 0.99 {
				t.Fatalf("the card is only %v solid as its words change", solid)
			}
			if words < 0.45 {
				t.Fatalf("the card is bare: its words show only %v (old %v, new %v)", words, old.alpha(), card.face.alpha())
			}
			if h := card.size.H; h < was-0.5 || h-was > 12 {
				t.Fatalf("the card's height went from %v to %v in a frame", was, h)
			}
			was = card.size.H
			// A stack that stands on its foot keeps the card's foot still: the stack ends where the card does.
			stack, _ := u.Bounds(ts)
			if foot := card.y.Value() + card.size.H; foot < stack.Size().H-0.5 || foot > stack.Size().H+0.5 {
				t.Fatalf("the card's foot is at %v in a stack %v tall", foot, stack.Size().H)
			}
			if o, n := old.alpha(), card.face.alpha(); o > 0.2 && n > 0.2 {
				mid = true
			}
		})
	}
	if !mid {
		t.Fatal("the old words and the new never rolled past each other")
	}
	if card.faces != 1 || card.face.fade.Value() < 0.999 || card.warn.Value() < 0.99 {
		t.Fatalf("at rest the card holds %d faces, the new one %v in, its warning edge %v", card.faces, card.face.fade.Value(), card.warn.Value())
	}
	if want := card.face.size.H; card.size.H < want-0.5 || card.size.H > want+0.5 || want <= 40 {
		t.Fatalf("at rest the card is %v tall, its words %v", card.size.H, want)
	}
	if card.h.Active() {
		t.Fatal("the card's height still moves at rest")
	}
}

// The time of a toast replaced is not the time of the one that took its place.
func TestAToastThatTakesAnothersPlaceHasItsOwnTime(t *testing.T) {
	ts := &Toasts{Life: time.Second}
	_, on, frames := toastFrame(t, ts)
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "One", Key: "k"}, u) })
	frames(40, func() {})
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "Two", Key: "k"}, u) })
	frames(40, func() {
		if ts.Len() != 1 {
			t.Fatal("the first toast's time took the second away")
		}
	})
	frames(60, func() {})
	if ts.Len() != 0 {
		t.Fatalf("%d toasts show once the second's time is up", ts.Len())
	}
}

// A toast that takes the place of an older one, not the newest, arrives as the newest, on a card of its own.
func TestAToastThatTakesAnOlderOnesPlaceArrivesAsTheNewest(t *testing.T) {
	ts := &Toasts{Life: time.Minute}
	_, on, frames := toastFrame(t, ts)
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "Old", Key: "a"}, u) })
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "Other", Key: "b"}, u) })
	frames(30, func() {})
	other := ts.cards[1]
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "New", Key: "a"}, u) })
	if len(ts.cards) != 2 || ts.cards[0] != other || ts.cards[1].face.title.Text != "New" {
		t.Fatalf("the stack holds %d cards, the other first %v", len(ts.cards), ts.cards[0] == other)
	}
}

// boundsOf returns where the frame drew each node.
func boundsOf(on func(func(*gunim.UI)), ns ...gunim.Node) []geom.Rect {
	out := make([]geom.Rect, len(ns))
	on(func(u *gunim.UI) {
		for i, n := range ns {
			out[i], _ = u.Bounds(n)
		}
	})
	return out
}

// A toast's button sits beside its words: at their right, in the close button's column and under it, where that
// leaves the words the most room. The words sit at the card's middle.
func TestAToastsButtonSitsBesideItsWords(t *testing.T) {
	ts := &Toasts{}
	_, on, frames := toastFrame(t, ts)
	on(func(u *gunim.UI) {
		ts.Show(Toast{Title: "Exported song.wav", Body: "Loudness −19.3 LUFS · range 0.5 LU · true peak −5.7 dBTP",
			Buttons: []ToastButton{{Label: "Show"}}}, u)
	})
	frames(60, func() {})
	card := ts.cards[0]
	fc := card.face
	if !fc.beside || fc.where != toastColumn {
		t.Fatalf("the button is placed %d, beside the words %v; want in the close button's column", fc.where, fc.beside)
	}
	r := boundsOf(on, card, fc.title, fc.body, fc.buttons, fc.close)
	box, title, body, button, closeAt := r[0], r[1], r[2], r[3], r[4]
	if button.Min.X < title.Max.X || button.Min.X < body.Max.X {
		t.Errorf("the button at %v overlaps the words at %v and %v", button, title, body)
	}
	if d := button.Max.X - closeAt.Max.X; d < -1 || d > 1 || button.Min.Y < closeAt.Max.Y {
		t.Errorf("the button at %v is not under the close button at %v, in its column", button, closeAt)
	}
	pad := CardPadding.Default()
	if button.Max.Y > box.Max.Y-pad.Bottom+1 {
		t.Errorf("the button at %v is not inside the padding of the card at %v", button, box)
	}
	if over, under := title.Min.Y-box.Min.Y-pad.Top, box.Max.Y-body.Max.Y-pad.Bottom; over < under-1 || over > under+1 {
		t.Errorf("the words have %v over them and %v under: not at the card's middle", over, under)
	}
	// The button is as wide as its word wants, as it is under the words.
	on(func(u *gunim.UI) {
		ts.Show(Toast{Title: "Exported song.wav", Body: "Loudness −19.3 LUFS · range 0.5 LU · true peak −5.7 dBTP",
			Buttons: []ToastButton{{Label: "Show"}, {Label: "Use MOTU M Series Number One"}}}, u)
	})
	frames(60, func() {})
	under := boundsOf(on, ts.cards[1].face.buttons.Children()[0])[0]
	if w, want := boundsOf(on, fc.buttons.Children()[0])[0].Size().W, under.Size().W; w < want-0.5 || w > want+0.5 {
		t.Errorf("beside the words the button is %v wide, under them %v", w, want)
	}
	// The card is shorter than with the button under the words.
	if stacked := boundsOf(on, ts.cards[1])[0].Size().H; box.Size().H >= stacked {
		t.Errorf("the card is %v tall with its button beside the words, %v with a button under them", box.Size().H, stacked)
	}
	if len(fc.body.laid.p.Lines) > toastBodyLines || fc.body.laid.p.Truncated {
		t.Errorf("beside the button the body is cut")
	}
}

// With a title alone, shorter than the button, the title sits at the button's middle.
func TestAToastsButtonBesideATitleAloneSharesItsMiddle(t *testing.T) {
	ts := &Toasts{}
	_, on, frames := toastFrame(t, ts)
	on(func(u *gunim.UI) {
		ts.Show(Toast{Title: "Saved", Kind: ToastInfo, Buttons: []ToastButton{{Label: "Show"}}}, u)
	})
	frames(60, func() {})
	fc := ts.cards[0].face
	r := boundsOf(on, ts.cards[0], fc.title, fc.buttons, fc.mark, fc.close)
	box, title, button, mark := r[0], r[1], r[2], r[3]
	if fc.where != toastLeft || button.Max.X > r[4].Min.X {
		t.Errorf("the button at %v is placed %d, not left of the close button at %v", button, fc.where, r[4])
	}
	if !fc.beside || title.Center().Y < button.Center().Y-1 || title.Center().Y > button.Center().Y+1 {
		t.Errorf("beside %v: the title's middle is at %v, the button's at %v", fc.beside, title.Center().Y, button.Center().Y)
	}
	if mark.Center().Y < title.Center().Y-2 || mark.Center().Y > title.Center().Y+2 {
		t.Errorf("the icon at %v is not beside the title at %v", mark, title)
	}
	pad := CardPadding.Default()
	if top, bottom := button.Min.Y-box.Min.Y, box.Max.Y-button.Max.Y; top < pad.Top-1 || bottom < pad.Bottom-1 {
		t.Errorf("the button at %v leaves %v and %v of the card at %v", button, top, bottom, box)
	}
}

// Buttons go under the words where beside them they would take too much: in a narrow toast, with a body that
// would be cut, and with a tick box.
func TestAToastsButtonsGoUnderWordsTheyWouldCrowd(t *testing.T) {
	long := "A long body that takes three lines of the toast as it is, with the whole width of the card to itself, and so would be cut short were a button to take a part of each of its lines away"
	for name, c := range map[string]struct {
		width float32
		to    Toast
	}{
		"narrow":   {150, Toast{Title: "Exported song.wav", Body: "Loudness", Buttons: []ToastButton{{Label: "Show"}}}},
		"cut body": {300, Toast{Title: "Exported", Body: long, Buttons: []ToastButton{{Label: "Show"}}}},
		"tick box": {300, Toast{Title: "Open it?", Check: "Always", Buttons: []ToastButton{{Label: "Yes"}}}},
		"squeezed": {300, Toast{Title: "Selected recording inputs could not be found", Buttons: []ToastButton{{Label: "Change the inputs"}}}},
		"wide":     {300, Toast{Title: "Which card?", Buttons: []ToastButton{{Label: "Use MOTU M Series Number One"}}}},
	} {
		ts := &Toasts{}
		w, run := stage(t, &frame{child: ts, size: geom.Sz(c.width, 400)})
		type do struct{ fn func(u *gunim.UI) }
		gunim.RegisterPatch(w, "stage", func(_ gunim.Node, d do, u *gunim.UI) { d.fn(u) })
		on := func(fn func(u *gunim.UI)) {
			if err := w.Client().Patch("stage", do{fn}); err != nil {
				t.Fatal(err)
			}
			run(1)
		}
		on(func(u *gunim.UI) { ts.Show(c.to, u) })
		run(60)
		fc := ts.cards[0].face
		r := boundsOf(on, ts.cards[0], fc.title, fc.buttons)
		box, title, button := r[0], r[1], r[2]
		if fc.beside || button.Min.Y < title.Max.Y {
			t.Errorf("%s: the buttons at %v are beside the title at %v", name, button, title)
		}
		if button.Max.X > box.Max.X || button.Max.Y > box.Max.Y || button.Min.X < box.Min.X {
			t.Errorf("%s: the buttons at %v leave the card at %v", name, button, box)
		}
	}
}

// The pointer on a toast with a button beside its words shows the whole body, and the button stays beside it.
func TestAToastGrowsUnderThePointerWithItsButtonStillBeside(t *testing.T) {
	ts := &Toasts{}
	w, on, frames := toastFrame(t, ts)
	body := "Loudness −19.3 LUFS · range 0.5 LU · true peak −5.7 dBTP"
	on(func(u *gunim.UI) {
		ts.Show(Toast{Title: "Exported", Body: body, Buttons: []ToastButton{{Label: "Show"}}}, u)
	})
	frames(60, func() {})
	card := ts.cards[0]
	if !card.face.beside {
		t.Fatal("the button is under the words")
	}
	was := card.size.H
	w.Input(input.PointerMove{Pos: geom.Pt(100, card.y.Value()+10), Time: time.Now()})
	frames(60, func() {
		if !card.face.beside {
			t.Fatal("under the pointer the button went under the words")
		}
		if h := card.size.H; h < was-0.5 || h > was+0.5 {
			t.Fatalf("under the pointer a body that already showed whole changed the card from %v to %v", was, h)
		}
	})
}

// The same toast shown again stands as it stood: its words don't roll, and its time starts again.
func TestTheSameToastShownAgainStandsStill(t *testing.T) {
	ts := &Toasts{Life: time.Second}
	w, on, frames := toastFrame(t, ts)
	same := Toast{Title: "Importing kick.wav", Key: "k", Kind: ToastInfo}
	on(func(u *gunim.UI) { ts.Show(same, u) })
	frames(40, func() {})
	card := ts.cards[0]
	on(func(u *gunim.UI) { ts.Show(same, u) })
	frames(40, func() {
		if _, words := drawnAlpha(w.Offscreen().Ops()); words < 0.999 || card.faces > 1 {
			t.Fatalf("the same toast shown again drew its words %v solid, on %d faces", words, card.faces)
		}
		if ts.Len() != 1 || ts.cards[0] != card {
			t.Fatal("the first showing's time took the toast away")
		}
	})
	frames(60, func() {})
	if ts.Len() != 0 {
		t.Fatal("the toast outstayed the time of its second showing")
	}
}

// As one toast hands over to another, a click lands on the new one only: the old toast's action is out of reach,
// and a press begun on the old toast is no click on the new.
func TestAClickDuringTheHandoverReachesOnlyTheNewToast(t *testing.T) {
	ts := &Toasts{Life: time.Minute}
	w, on, frames := toastFrame(t, ts)
	on(func(u *gunim.UI) {
		ts.Show(Toast{Title: "Removed Bass", Key: "k", Action: "Undo", OnClick: Sends(undone{1})}, u)
	})
	frames(40, func() {})
	card := ts.cards[0]
	link := boundsOf(on, card.face.action)[0].Center()
	// A press on the old toast, held as the new one takes its place.
	w.Input(input.PointerMove{Pos: geom.Pt(40, 20), Time: time.Now()})
	w.Input(input.PointerDown{Pos: geom.Pt(40, 20), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	on(func(u *gunim.UI) {
		ts.Show(Toast{Title: "Exported song.wav", Key: "k", Buttons: []ToastButton{{Label: "Show", OnClick: func(bool, *gunim.UI) gunim.Intent { return undone{2} }}}}, u)
	})
	w.Input(input.PointerUp{Pos: geom.Pt(40, 20), Button: input.ButtonPrimary, Time: time.Now()})
	frames(2, func() {})
	// Where the old toast's Undo still fades.
	click(w, link.X, link.Y)
	frames(2, func() {})
	if got := sent(w); len(got) != 0 || ts.Len() != 1 {
		t.Fatalf("mid handover the clicks sent %v and left %d toasts", got, ts.Len())
	}
	// The new toast's button answers once the card has grown to hold it.
	frames(40, func() {})
	show := boundsOf(on, card.face.buttons.Children()[0])[0].Center()
	click(w, show.X, show.Y)
	frames(2, func() {})
	if got := sent(w); len(got) != 1 || got[0] != (undone{2}) || ts.Len() != 0 {
		t.Fatalf("a click on the new toast's button sent %v and left %d toasts", got, ts.Len())
	}
}

// Toasts that take each other's place faster than one can roll in still never leave the card bare: together the
// faces are as solid as one, and one of them shows well.
func TestToastsThatChangeFastNeverShowABareCard(t *testing.T) {
	ts := &Toasts{Life: time.Minute}
	w, on, frames := toastFrame(t, ts)
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "Undid 0", Key: "k"}, u) })
	frames(60, func() {})
	card := ts.cards[0]
	faces := []*toastFace{card.face}
	check := func() {
		t.Helper()
		var sum float32
		for _, fc := range faces {
			sum += fc.alpha()
		}
		if sum < 0.99 || sum > 1.01 {
			t.Fatalf("the card's %d faces are %v solid together, want 1", len(faces), sum)
		}
		if solid, words := drawnAlpha(w.Offscreen().Ops()); solid < 0.99 || words < 0.2 {
			t.Fatalf("the card is %v solid and its words show only %v", solid, words)
		}
	}
	for i := range 12 {
		// A new toast every other frame, as Ctrl+Z held down.
		on(func(u *gunim.UI) {
			if i == 5 {
				// Two within a frame: the first of them never gets a frame to come in.
				ts.Show(Toast{Title: "Undid in passing", Key: "k"}, u)
				faces = append(faces, card.face)
			}
			ts.Show(Toast{Title: "Undid " + strconv.Itoa(i+1), Body: strings.Repeat("more ", i%3), Key: "k"}, u)
		})
		faces = append(faces, card.face)
		check()
		frames(1, check)
	}
	frames(90, check)
	if card.faces != 1 || card.face.alpha() < 0.999 || card.face.title.Text != "Undid 12" {
		t.Fatalf("at rest the card holds %d faces, the last %v solid, reading %q", card.faces, card.face.alpha(), card.face.title.Text)
	}
}
