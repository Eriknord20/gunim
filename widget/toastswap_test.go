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
	old := card.face
	// A press on the old toast, held as the new one takes its place.
	w.Input(input.PointerMove{Pos: geom.Pt(40, 20), Time: time.Now()})
	w.Input(input.PointerDown{Pos: geom.Pt(40, 20), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	on(func(u *gunim.UI) {
		ts.Show(Toast{Title: "Removed Pad", Key: "k", Action: "Undo", OnClick: Sends(undone{2})}, u)
	})
	w.Input(input.PointerUp{Pos: geom.Pt(40, 20), Button: input.ButtonPrimary, Time: time.Now()})
	frames(2, func() {})
	if got := sent(w); len(got) != 0 || ts.Len() != 1 {
		t.Fatalf("a press held through the handover sent %v and left %d toasts", got, ts.Len())
	}
	// With the old toast's Undo still fading, a click on the new toast's Undo, where it is drawn part way in, is
	// the new toast's.
	if old.alpha() < 0.2 || card.faces != 2 {
		t.Fatalf("the old toast is %v solid on a card of %d faces: the handover is over", old.alpha(), card.faces)
	}
	link := boundsOf(on, card.face.action)[0].Center()
	click(w, link.X, link.Y)
	frames(2, func() {})
	if got := sent(w); len(got) != 1 || got[0] != (undone{2}) || ts.Len() != 0 {
		t.Fatalf("mid handover a click on Undo sent %v and left %d toasts", got, ts.Len())
	}
}

// The pointer that makes a toast grow keeps its button under it: the button stays at the card's foot on every
// frame, as the card glides taller, and a click begun before the growth lands.
func TestAToastsButtonKeepsToTheCardsFootAsItGrows(t *testing.T) {
	ts := &Toasts{}
	w, on, frames := toastFrame(t, ts)
	body := strings.Repeat("A sentence of the long body. ", 12)
	clicked := 0
	on(func(u *gunim.UI) {
		ts.Show(Toast{Title: "Could not make the folder", Body: body, Kind: ToastError,
			Buttons: []ToastButton{{Label: "Try again", OnClick: func(bool, *gunim.UI) gunim.Intent { clicked++; return nil }}}}, u)
	})
	frames(60, func() {})
	card := ts.cards[0]
	button := card.face.buttons.Children()[0]
	// How far the button's foot is from the card's.
	gap := func() float32 {
		r := boundsOf(on, card, button)
		return r[0].Max.Y - r[1].Max.Y
	}
	rest, short := gap(), card.size.H
	at := boundsOf(on, button)[0].Center()
	// The press moves the pointer onto the toast, which grows; the release comes a frame later.
	w.Input(input.PointerMove{Pos: at, Time: time.Now()})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	for range 3 {
		if g := gap(); g < rest-0.5 || g > rest+0.5 {
			t.Fatalf("as the card grows its button is %v from the foot, was %v", g, rest)
		}
	}
	if card.size.H <= short+1 || !card.h.Active() {
		t.Fatalf("the card is %v tall, was %v, and glides %v: the body did not grow under the pointer", card.size.H, short, card.h.Active())
	}
	w.Input(input.PointerUp{Pos: boundsOf(on, button)[0].Center(), Button: input.ButtonPrimary, Time: time.Now()})
	frames(2, func() {})
	if clicked != 1 {
		t.Fatalf("the click on the button of a toast growing under the pointer was heard %d times", clicked)
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

// A tall toast replaced by a short one keeps the short one's button under the pointer as the card glides
// shorter: the button stays at the card's foot on every frame, and a click two frames in lands.
func TestAToastsButtonKeepsToTheCardsFootAsItShrinks(t *testing.T) {
	ts := &Toasts{}
	w, on, frames := toastFrame(t, ts)
	clicked := 0
	body := strings.Repeat("A sentence of the long body. ", 6)
	on(func(u *gunim.UI) {
		ts.Show(Toast{Title: "Open the song it came with?", Body: body, Check: "Don't ask again", Key: "k",
			Buttons: []ToastButton{{Label: "Open"}}}, u)
	})
	frames(60, func() {})
	card := ts.cards[0]
	tall := card.size.H
	on(func(u *gunim.UI) {
		ts.Show(Toast{Title: "Open it?", Check: "Don't ask again", Key: "k",
			Buttons: []ToastButton{{Label: "Open now", OnClick: func(bool, *gunim.UI) gunim.Intent { clicked++; return nil }}}}, u)
	})
	button := card.face.buttons.Children()[0]
	gap := func() float32 {
		r := boundsOf(on, card, button)
		return r[0].Max.Y - r[1].Max.Y
	}
	// It stands at the card's foot, less what is left of its roll in from below.
	rest := CardPadding.Default().Bottom
	for range 2 {
		if g := gap(); g < rest-toastRise-0.5 || g > rest+0.5 {
			t.Fatalf("as the card shrinks its button is %v from the foot, want %v, or up to %v less as it rolls in", g, rest, toastRise)
		}
	}
	if card.size.H >= tall-1 || card.size.H <= card.face.size.H+10 || !card.h.Active() {
		t.Fatalf("the card is %v tall, was %v, its face %v, gliding %v: want it part way down", card.size.H, tall, card.face.size.H, card.h.Active())
	}
	at := boundsOf(on, button)[0].Center()
	click(w, at.X, at.Y)
	frames(2, func() {})
	if clicked != 1 {
		t.Fatalf("the click on the button of a toast shrinking was heard %d times", clicked)
	}
}

// A button beside the words follows the card's middle as the card shrinks, and takes a click there too.
func TestAToastsButtonBesideItsWordsAnswersAsTheCardShrinks(t *testing.T) {
	ts := &Toasts{}
	w, on, frames := toastFrame(t, ts)
	clicked := 0
	on(func(u *gunim.UI) {
		ts.Show(Toast{Title: "Could not make the folder", Body: strings.Repeat("A sentence of the long body. ", 6), Key: "k"}, u)
	})
	frames(60, func() {})
	card := ts.cards[0]
	on(func(u *gunim.UI) {
		ts.Show(Toast{Title: "Saved", Key: "k",
			Buttons: []ToastButton{{Label: "Show", OnClick: func(bool, *gunim.UI) gunim.Intent { clicked++; return nil }}}}, u)
	})
	frames(1, func() {})
	if card.face.where != toastLeft || card.size.H <= card.face.size.H+10 {
		t.Fatalf("the button is placed %d on a card %v tall with a face %v tall", card.face.where, card.size.H, card.face.size.H)
	}
	// The lower edge of the button, which is past the face's own height.
	r := boundsOf(on, card.face.buttons.Children()[0])[0]
	click(w, r.Center().X, r.Max.Y-3)
	frames(2, func() {})
	if clicked != 1 {
		t.Fatalf("a click low on the button beside the words of a shrinking toast was heard %d times", clicked)
	}
}

// The same toast shown again while it is still rolling in rolls on: no third face, and no jump in how solid the
// old words and the new are.
func TestTheSameToastShownAgainMidHandoverRollsOn(t *testing.T) {
	ts := &Toasts{Life: time.Minute}
	w, on, frames := toastFrame(t, ts)
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "Saved the song", Key: "k"}, u) })
	frames(60, func() {})
	card := ts.cards[0]
	old := card.face
	exporting := Toast{Title: "Exporting the song", Key: "k"}
	on(func(u *gunim.UI) { ts.Show(exporting, u) })
	face := card.face
	frames(3, func() {})
	wasOld, wasNew, wasFade := old.alpha(), face.alpha(), face.fade.Value()
	on(func(u *gunim.UI) { ts.Show(exporting, u) })
	if card.face != face || card.faces != 2 {
		t.Fatalf("the same toast again made a new face %v, on a card of %d faces", card.face != face, card.faces)
	}
	if old.alpha() < 0.5 || old.alpha() > wasOld+0.01 || old.alpha() < wasOld-0.3 || face.alpha() < wasNew-0.01 || face.fade.Value() > wasFade+0.3 {
		t.Fatalf("the old words went from %v to %v and the new from %v to %v in a frame", wasOld, old.alpha(), wasNew, face.alpha())
	}
	rolled := false
	frames(60, func() {
		solid, words := drawnAlpha(w.Offscreen().Ops())
		if solid < 0.99 || words < 0.45 {
			t.Fatalf("the card is %v solid and its words %v", solid, words)
		}
		if o := old.alpha(); o > 0.2 && o < 0.8 {
			rolled = true
		}
	})
	if !rolled || card.faces != 1 || card.face.alpha() < 0.999 {
		t.Fatalf("the old words rolled out %v, leaving %d faces, the new %v solid", rolled, card.faces, card.face.alpha())
	}
}

// The same toast shown again keeps what the user did on it: the tick stays, the pointer's light stays, and a
// press held on its button still clicks. Only what the buttons do is the new toast's.
func TestTheSameToastShownAgainKeepsItsControls(t *testing.T) {
	ts := &Toasts{Life: time.Minute}
	w, on, frames := toastFrame(t, ts)
	heard := ""
	asks := func(name string) Toast {
		return Toast{Title: "Open the song it came with?", Check: "Don't ask again", Key: "k",
			Buttons: []ToastButton{{Label: "Open", OnClick: func(ticked bool, _ *gunim.UI) gunim.Intent {
				heard = name + " " + strconv.FormatBool(ticked)
				return nil
			}}},
			OnDismiss: func(bool, *gunim.UI) gunim.Intent { heard = name + " closed"; return nil }}
	}
	on(func(u *gunim.UI) { ts.Show(asks("first"), u) })
	frames(60, func() {})
	card := ts.cards[0]
	face, box, button := card.face, card.face.check, card.face.buttons.Children()[0]
	tick := boundsOf(on, box)[0].Center()
	click(w, tick.X, tick.Y)
	frames(30, func() {})
	if !box.Checked() {
		t.Fatal("the click did not tick the box")
	}
	// The pointer rests on the button, pressed, as the same toast is shown again.
	at := boundsOf(on, button)[0].Center()
	w.Input(input.PointerMove{Pos: at, Time: time.Now()})
	frames(30, func() {})
	lit := card.hover.Value()
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	on(func(u *gunim.UI) { ts.Show(asks("second"), u) })
	frames(2, func() {
		if card.face != face || card.faces != 1 || card.face.check != box || !box.Checked() {
			t.Fatalf("shown again, the toast has a new face %v, %d faces, its box ticked %v", card.face != face, card.faces, box.Checked())
		}
		if card.hover.Value() < lit-0.01 || !card.hovered {
			t.Fatalf("shown again, the pointer's light went from %v to %v", lit, card.hover.Value())
		}
	})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary, Time: time.Now()})
	frames(2, func() {})
	if heard != "second true" || ts.Len() != 0 {
		t.Fatalf("the press held through the second showing was heard as %q, leaving %d toasts", heard, ts.Len())
	}
}

// A toast that asks for more width gets it, as far as the stack has room, and the others keep to the stack's
// right edge.
func TestAToastCanBeWiderThanTheStack(t *testing.T) {
	ts := &Toasts{}
	_, on, frames := toastFrame(t, ts)
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "Saved"}, u) })
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "Exported", Width: 280}, u) })
	on(func(u *gunim.UI) { ts.Show(Toast{Title: "Far too wide", Width: 900}, u) })
	frames(60, func() {})
	base := min(ToastWidth.Default(), 300)
	r := boundsOf(on, ts, ts.cards[0], ts.cards[1], ts.cards[2])
	for i, want := range []float32{base, max(base, 280), 300} {
		if got := r[i+1].Size().W; got != want || r[i+1].Max.X != r[0].Max.X {
			t.Errorf("toast %d is %v wide, ending at %v; want %v wide, ending at the stack's %v", i, got, r[i+1].Max.X, want, r[0].Max.X)
		}
	}
	if r[0].Size().W != 300 {
		t.Errorf("the stack is %v wide, want its widest toast's 300", r[0].Size().W)
	}
}
