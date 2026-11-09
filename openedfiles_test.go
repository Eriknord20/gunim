package gunim

import (
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// hearsOpened records the files it hears of, and takes them as take
// says.
type hearsOpened struct {
	take bool
	got  [][]string
}

func (*hearsOpened) Layout(c Constraints, _ Frame, _ Children) geom.Size { return c.Max }
func (*hearsOpened) Paint(*paint.Painter, Frame, geom.Size, Children)    {}
func (*hearsOpened) Focusable() bool                                     { return true }
func (h *hearsOpened) Handle(e input.Event, _ *UI) bool {
	if o, ok := e.(input.FilesOpened); ok {
		h.got = append(h.got, o.Paths)
		return h.take
	}
	return false
}

// rootOf is an empty root that views mount in.
type rootOf struct{}

func (rootOf) Layout(c Constraints, _ Frame, kids Children) geom.Size {
	for kid := range kids.All {
		kid.Layout(Tight(c.Max))
	}
	return c.Max
}
func (rootOf) Paint(p *paint.Painter, _ Frame, _ geom.Size, kids Children) {
	for kid := range kids.All {
		kid.Paint(p)
	}
}

// heard reports whether h heard each batch of want once, in order.
func heard(h *hearsOpened, want ...[]string) bool {
	return slices.EqualFunc(h.got, want, slices.Equal)
}

func TestFilesOpenedReachTheFocusedNodeOnce(t *testing.T) {
	h := &hearsOpened{take: true}
	w := NewOffscreen(geom.Sz(200, 200), h)
	w.Frame(time.Second / 60)
	w.Input(driver.FilesOpened{Paths: []string{"/a.hej"}})
	for range 5 {
		w.Frame(time.Second / 60)
	}
	if !heard(h, []string{"/a.hej"}) {
		t.Fatalf("heard %v", h.got)
	}
}

// Files that start the application arrive ahead of the first frame, and
// of the view that takes them: the view mounts frames later, past an
// intro, and hears them then, once, with each frame between keeping them.
func TestFilesThatStartTheApplicationWaitForAViewToTakeThem(t *testing.T) {
	w := NewOffscreen(geom.Sz(200, 200), rootOf{})
	w.Input(driver.FilesOpened{Paths: []string{"/a.hej"}})
	w.Input(driver.FilesOpened{Paths: []string{"/b.hej"}})
	for range 10 {
		w.Frame(time.Second / 60)
		if n := len(w.ui.opened); n != 2 {
			t.Fatalf("with no view, the window holds %d batches, want 2", n)
		}
	}
	h := &hearsOpened{take: true}
	RegisterView(w, "v", func(struct{}) *hearsOpened { return h }, nil)
	c := w.Client()
	if err := c.Mount(Root, "v", "v", struct{}{}); err != nil {
		t.Fatal(err)
	}
	for f := range 10 {
		w.Frame(time.Second / 60)
		if f > 1 && !heard(h, []string{"/a.hej"}, []string{"/b.hej"}) {
			t.Fatalf("frame %d: heard %v, want both batches once, oldest first", f, h.got)
		}
	}
	if len(w.ui.opened) != 0 {
		t.Fatalf("the window still holds %v", w.ui.opened)
	}
}

// A dialog holding the keyboard passes on files; the view under it takes
// them, and the dialog hears them once however long that takes.
func TestFilesOpenedPastADialogReachTheViewUnderIt(t *testing.T) {
	w := NewOffscreen(geom.Sz(200, 200), rootOf{})
	view, dialog := &hearsOpened{take: true}, &hearsOpened{}
	RegisterView(w, "view", func(struct{}) *hearsOpened { return view }, nil)
	RegisterView(w, "dialog", func(struct{}) *hearsOpened { return dialog }, nil)
	c := w.Client()
	if err := c.Mount(Root, "view", "view", struct{}{}); err != nil {
		t.Fatal(err)
	}
	if err := c.Mount(Root, "dialog", "dialog", struct{}{}); err != nil {
		t.Fatal(err)
	}
	_ = c.Focus("dialog")
	w.Frame(time.Second / 60)
	w.Input(driver.FilesOpened{Paths: []string{"/a.hej"}})
	for range 5 {
		w.Frame(time.Second / 60)
	}
	if !heard(view, []string{"/a.hej"}) {
		t.Fatalf("the view heard %v", view.got)
	}
	if !heard(dialog, []string{"/a.hej"}) {
		t.Fatalf("the dialog heard %v", dialog.got)
	}
}

// The first view to take files keeps them from the next.
func TestFilesOpenedReachOneNode(t *testing.T) {
	w := NewOffscreen(geom.Sz(200, 200), rootOf{})
	first, second := &hearsOpened{take: true}, &hearsOpened{take: true}
	RegisterView(w, "first", func(struct{}) *hearsOpened { return first }, nil)
	RegisterView(w, "second", func(struct{}) *hearsOpened { return second }, nil)
	c := w.Client()
	if err := c.Mount(Root, "first", "first", struct{}{}); err != nil {
		t.Fatal(err)
	}
	if err := c.Mount(Root, "second", "second", struct{}{}); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	w.Input(driver.FilesOpened{Paths: []string{"/a.hej"}})
	w.Frame(time.Second / 60)
	if !heard(first, []string{"/a.hej"}) || len(second.got) != 0 {
		t.Fatalf("the first heard %v and the second %v", first.got, second.got)
	}
}

// A hidden window hears files like one in front: a phone opens them
// while the application lies in the background.
func TestFilesOpenedWhileHiddenArriveBeforeTheWindowShows(t *testing.T) {
	h := &hearsOpened{take: true}
	w := NewOffscreen(geom.Sz(200, 200), h)
	w.Frame(time.Second / 60)
	w.Input(driver.WindowShown{Shown: false})
	w.Input(driver.FilesOpened{Paths: []string{"/a.hej"}})
	if !heard(h, []string{"/a.hej"}) {
		t.Fatalf("heard %v", h.got)
	}
	w.Input(driver.WindowShown{Shown: true})
	w.Frame(time.Second / 60)
	if !heard(h, []string{"/a.hej"}) {
		t.Fatalf("heard %v after showing", h.got)
	}
}
