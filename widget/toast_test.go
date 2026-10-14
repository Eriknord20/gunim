package widget

import (
	"strconv"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// toastHost holds a stack of toasts at its top left, and shows a new
// one for each F2.
type toastHost struct {
	t     *Toasts
	shown int
}

func (h *toastHost) Children() []gunim.Node { return []gunim.Node{h.t} }

func (h *toastHost) Focusable() bool { return true }

func (h *toastHost) Handle(e input.Event, u *gunim.UI) bool {
	if k, ok := e.(input.KeyPress); ok && k.Key == input.KeyF2 {
		h.shown++
		h.t.Show(Toast{Title: "Notice " + strconv.Itoa(h.shown), Body: "Something happened."}, u)
		return true
	}
	return false
}

func (h *toastHost) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	k.Layout(gunim.Constraints{Max: c.Max})
	k.Place(geom.Point{})
	return c.Max
}

func (h *toastHost) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

func newToastStage(t *testing.T) (*gunim.Window, *toastHost, func(time.Duration)) {
	t.Helper()
	h := &toastHost{t: &Toasts{Life: 2 * time.Second}}
	w, run := stage(t, h)
	w.Input(input.PointerDown{Pos: geom.Pt(700, 500), Clicks: 1, Time: time.Now()})
	w.Input(input.PointerUp{Pos: geom.Pt(700, 500), Time: time.Now()})
	run(1)
	wait := func(d time.Duration) { run(int(d / (time.Second / 60))) }
	return w, h, wait
}

func TestToastsStackNewestNearestTheCornerAndLeaveInTime(t *testing.T) {
	w, h, wait := newToastStage(t)
	w.Input(input.KeyPress{Key: input.KeyF2})
	wait(300 * time.Millisecond)
	w.Input(input.KeyPress{Key: input.KeyF2})
	wait(time.Second)
	if n := h.t.Len(); n != 2 {
		t.Fatalf("%d toasts showing, want 2", n)
	}
	first, second := h.t.cards[0], h.t.cards[1]
	if first.y.Value() >= second.y.Value() {
		t.Fatalf("the older toast is at %v, the newer at %v; want the newer below", first.y.Value(), second.y.Value())
	}
	// The first has had its two seconds; the second follows.
	wait(900 * time.Millisecond)
	if n := h.t.Len(); n != 1 {
		t.Fatalf("after the first toast's time, %d showing, want 1", n)
	}
	wait(time.Second)
	if n := h.t.Len(); n != 0 {
		t.Fatalf("after both toasts' time, %d showing, want 0", n)
	}
}

func TestThePointerKeepsAToastAndAClickDismissesIt(t *testing.T) {
	w, h, wait := newToastStage(t)
	w.Input(input.KeyPress{Key: input.KeyF2})
	wait(500 * time.Millisecond)
	w.Input(input.PointerMove{Pos: geom.Pt(20, 20), Time: time.Now()})
	wait(3 * time.Second)
	if n := h.t.Len(); n != 1 {
		t.Fatalf("with the pointer on it, %d toasts showing after their time, want 1", n)
	}
	w.Input(input.PointerDown{Pos: geom.Pt(20, 20), Clicks: 1, Time: time.Now()})
	w.Input(input.PointerUp{Pos: geom.Pt(20, 20), Time: time.Now()})
	wait(100 * time.Millisecond)
	if n := h.t.Len(); n != 0 {
		t.Fatalf("after a click, %d toasts showing, want 0", n)
	}
}

type undone struct{ ID int }

func TestAToastsActionSendsItsIntentAndDismissesIt(t *testing.T) {
	h := &toastHost{t: &Toasts{}}
	w, run := stage(t, h)
	type do struct{ fn func(u *gunim.UI) }
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, d do, u *gunim.UI) { d.fn(u) })
	on := func(fn func(u *gunim.UI)) {
		if err := w.Client().Patch("stage", do{fn}); err != nil {
			t.Fatal(err)
		}
		run(1)
	}
	on(func(u *gunim.UI) {
		h.t.Show(Toast{Title: "Copied 3 files", Action: "Undo", OnClick: Sends(undone{7})}, u)
	})
	run(40)
	card := h.t.cards[0]
	var r, title geom.Rect
	var ok bool
	on(func(u *gunim.UI) {
		r, ok = u.Bounds(card.action)
		title, _ = u.Bounds(card.title)
	})
	if !ok {
		t.Fatal("the toast drew no action")
	}
	if title.Max.X > r.Min.X {
		t.Fatalf("the title reaches %v, past the action at %v", title.Max.X, r.Min.X)
	}
	at := r.Center()
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary, Time: time.Now()})
	run(1)
	if got := sent(w); len(got) != 1 || got[0] != (undone{7}) {
		t.Fatalf("a click on Undo sent %v", got)
	}
	if n := h.t.Len(); n != 0 {
		t.Fatalf("after its action, %d toasts showing, want 0", n)
	}
}

type toastAnswer struct {
	Button  int
	Checked bool
}

func TestAToastThatAsksStaysTakesNoKeyboardAndAnswersWithItsTickBox(t *testing.T) {
	w, h, wait := newToastStage(t)
	type do struct{ fn func(u *gunim.UI) }
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, d do, u *gunim.UI) { d.fn(u) })
	on := func(fn func(u *gunim.UI)) {
		if err := w.Client().Patch("stage", do{fn}); err != nil {
			t.Fatal(err)
		}
		wait(time.Second / 60)
	}
	var focused gunim.Node
	on(func(u *gunim.UI) { focused = u.Focused() })
	if focused != gunim.Node(h) {
		t.Fatalf("the host does not have the keyboard to begin with: %v", focused)
	}
	answer := func(i int) func(bool, *gunim.UI) gunim.Intent {
		return func(c bool, _ *gunim.UI) gunim.Intent { return toastAnswer{i, c} }
	}
	ask := Toast{Title: "report.docx changed", Body: "Upload it?", Key: "k", Check: "Don't ask again",
		Buttons:   []ToastButton{{Label: "Upload", OnClick: answer(0)}, {Label: "Not now", OnClick: answer(1)}},
		OnDismiss: answer(-1)}
	on(func(u *gunim.UI) { h.t.Show(ask, u) })
	// Well past the life of a toast that only tells.
	wait(3 * time.Second)
	if n := h.t.Len(); n != 1 {
		t.Fatalf("%d toasts showing after a toast that asks had its time, want 1", n)
	}
	card := h.t.cards[0]
	var box, upload, body geom.Rect
	on(func(u *gunim.UI) {
		box, _ = u.Bounds(card.check)
		upload, _ = u.Bounds(card.buttons.Children()[0])
		body, _ = u.Bounds(card.title)
		focused = u.Focused()
	})
	if focused != gunim.Node(h) {
		t.Fatalf("showing the toast moved the keyboard to %v", focused)
	}
	if box.Empty() || upload.Empty() || upload.Min.Y < box.Max.Y {
		t.Fatalf("the tick box is at %v and Upload at %v", box, upload)
	}
	// A click on the text keeps the toast, and the keyboard.
	click(w, body.Center().X, body.Center().Y)
	wait(100 * time.Millisecond)
	click(w, box.Min.X+4, box.Center().Y)
	wait(100 * time.Millisecond)
	if n := h.t.Len(); n != 1 || !card.check.Checked() {
		t.Fatalf("after clicks on the text and the box, %d toasts show, ticked %v", n, card.check.Checked())
	}
	_ = sent(w)
	click(w, upload.Center().X, upload.Center().Y)
	wait(100 * time.Millisecond)
	if got := sent(w); len(got) != 1 || got[0] != (toastAnswer{0, true}) {
		t.Fatalf("a click on Upload sent %v", got)
	}
	on(func(u *gunim.UI) { focused = u.Focused() })
	if focused != gunim.Node(h) {
		t.Fatalf("the clicks on the toast moved the keyboard to %v", focused)
	}
	if n := h.t.Len(); n != 0 {
		t.Fatalf("after an answer, %d toasts showing, want 0", n)
	}

	// The program can take one away by its key; one shown again with
	// the key takes the place of the one showing.
	on(func(u *gunim.UI) { h.t.Show(ask, u) })
	on(func(u *gunim.UI) { h.t.Show(ask, u) })
	if n := h.t.Len(); n != 1 {
		t.Fatalf("two toasts of one key show %d", n)
	}
	on(func(u *gunim.UI) { h.t.Close("k", u) })
	wait(time.Second)
	if n := h.t.Len(); n != 0 || len(sent(w)) != 0 {
		t.Fatalf("after Close, %d toasts show", n)
	}
}

func TestAnErrorToastStaysUntilClosedAndItsTextCopies(t *testing.T) {
	w, h, wait := newToastStage(t)
	type do struct{ fn func(u *gunim.UI) }
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, d do, u *gunim.UI) { d.fn(u) })
	on := func(fn func(u *gunim.UI)) {
		if err := w.Client().Patch("stage", do{fn}); err != nil {
			t.Fatal(err)
		}
		wait(time.Second / 60)
	}
	on(func(u *gunim.UI) {
		h.t.Show(Toast{Title: "Could not open", Body: "song.mrd: permission denied", Kind: ToastError}, u)
	})
	// Well past the life of a toast that only tells.
	wait(3 * time.Second)
	if n := h.t.Len(); n != 1 {
		t.Fatalf("%d toasts showing after an error had its time, want 1", n)
	}
	card := h.t.cards[0]
	var body, closer geom.Rect
	on(func(u *gunim.UI) {
		body, _ = u.Bounds(card.body)
		closer, _ = u.Bounds(card.close)
	})
	if closer.Empty() {
		t.Fatal("the error drew no close button")
	}
	// A triple click on the body selects it all, and Ctrl+C copies it;
	// the toast stays.
	at := body.Center()
	for i := 1; i <= 3; i++ {
		w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: i, Time: time.Now()})
		w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary, Time: time.Now()})
	}
	w.Input(input.KeyPress{Key: input.KeyC, Mods: input.ModControl})
	wait(100 * time.Millisecond)
	if got, err := w.Offscreen().Clipboard(); err != nil || got != "song.mrd: permission denied" {
		t.Fatalf("Ctrl+C copied %q, %v", got, err)
	}
	if n := h.t.Len(); n != 1 {
		t.Fatalf("after clicks on its text, %d toasts showing, want 1", n)
	}
	click(w, closer.Center().X, closer.Center().Y)
	wait(100 * time.Millisecond)
	if n := h.t.Len(); n != 0 {
		t.Fatalf("after its close button, %d toasts showing, want 0", n)
	}
}
