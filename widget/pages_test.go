package widget_test

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/widget"
)

type pagesState struct{ Page int }

// The page shown takes the place of the one before, which fades out
// where it stood and is gone once it has; asked for again, the page
// shown stays the same node.
func TestPagesFadeOneOutAsTheNextComesIn(t *testing.T) {
	w := gunimtest.New(t, geom.Sz(400, 300), nil)
	labels := []*widget.Label{widget.NewLabel("one"), widget.NewLabel("two")}
	var pages *widget.Pages
	var ui *gunim.UI
	gunim.RegisterView(w, "pages",
		func(pagesState) *widget.Pages {
			pages = widget.NewPages(labels[0])
			return pages
		},
		func(p *widget.Pages, s pagesState, u *gunim.UI) {
			ui = u
			p.Show(labels[s.Page], geom.Pt(0, 1), u)
		})
	c := w.Client()
	if err := c.Mount(gunim.Root, "pages", "pages", pagesState{}, "page"); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(2)
	if err := c.Publish("page", pagesState{Page: 1}); err != nil {
		t.Fatal(err)
	}
	run(3)
	if pages.Page() != labels[1] {
		t.Fatalf("the page shown is %v", pages.Page())
	}
	if _, ok := ui.Bounds(labels[0]); !ok {
		t.Fatal("the page before went at once, not fading out")
	}
	run(120)
	if _, ok := ui.Bounds(labels[0]); ok {
		t.Fatal("the page before is still there once it has faded")
	}
	if _, ok := ui.Bounds(labels[1]); !ok {
		t.Fatal("the page shown is not laid out")
	}
}
