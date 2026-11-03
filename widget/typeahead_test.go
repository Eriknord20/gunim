package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

func TestFindTypedFindsAsExplorerDoes(t *testing.T) {
	names := []string{"apple", "Banana", "berry", "bread", "cherry"}
	name := func(i int) string { return names[i] }
	for _, c := range []struct {
		text string
		from int
		want int
	}{
		{"b", -1, 1},     // the first b, ignoring case
		{"br", 1, 3},     // more letters narrow it, from where the list is
		{"be", 2, 2},     // the item the list is on still counts
		{"b", 1, 2},      // one letter steps on to the next that starts with it
		{"bbb", 3, 1},    // and goes round
		{"CH", 0, 4},     // case does not matter
		{"apple", 4, 0},  // round from the end
		{"zebra", 0, -1}, // nothing
	} {
		if got := FindTyped(c.text, len(names), c.from, name); got != c.want {
			t.Errorf("FindTyped(%q, from %d) = %d, want %d", c.text, c.from, got, c.want)
		}
	}
}

// typedText is the intent a grid sends for the text typed at it.
type typedText struct{ Text string }

func TestAGridGathersTypedTextAndSpaceWhileTyping(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Name"})
	g.OnType = func(text string, u *gunim.UI) gunim.Intent { return typedText{text} }
	w, run := stage(t, g)
	var typed []string
	take := func() {
		for _, v := range sent(w) {
			if s, ok := v.(typedText); ok {
				typed = append(typed, s.Text)
			}
		}
	}
	type focus struct{}
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, _ focus, u *gunim.UI) { u.Focus(g) })
	if err := w.Client().Patch("stage", focus{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	// A Space alone is not typing; one while typing is part of the text
	w.Input(input.KeyPress{Key: input.KeyM, Typed: true})
	w.Input(input.TextInput{Text: "m"})
	w.Input(input.KeyPress{Key: input.KeySpace, Typed: true})
	w.Input(input.TextInput{Text: " "})
	w.Input(input.TextInput{Text: "f"})
	run(1)
	take()
	if len(typed) == 0 || typed[len(typed)-1] != "m f" {
		t.Fatalf("the grid sent %q, want it to end with %q", typed, "m f")
	}
	// A pause starts the text again
	for range 70 {
		w.Frame(time.Second / 60)
	}
	w.Input(input.TextInput{Text: "x"})
	run(1)
	take()
	if typed[len(typed)-1] != "x" {
		t.Fatalf("after a pause the grid sent %q, want %q", typed[len(typed)-1], "x")
	}
}

// spaceFirst holds a node, and takes Space before it, as an app whose Space plays and stops.
type spaceFirst struct {
	in   gunim.Node
	took int
}

func (s *spaceFirst) Children() []gunim.Node { return []gunim.Node{s.in} }
func (s *spaceFirst) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	return kids.At(0).Layout(c)
}
func (s *spaceFirst) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}
func (s *spaceFirst) KeyFirst(e input.KeyPress, u *gunim.UI) bool {
	if e.Key != input.KeySpace {
		return false
	}
	s.took++
	return true
}

// A grid keeps a Space typed while a search is under way, from the window that takes Space first; a Space alone is
// the window's.
func TestAGridKeepsASpaceWhileTypingFromAKeyFirst(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Name"})
	g.OnType = func(text string, u *gunim.UI) gunim.Intent { return typedText{text} }
	app := &spaceFirst{in: g}
	w, run := stage(t, app)
	type focus struct{}
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, _ focus, u *gunim.UI) { u.Focus(g) })
	if err := w.Client().Patch("stage", focus{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	w.Input(input.KeyPress{Key: input.KeySpace, Typed: true})
	w.Input(input.TextInput{Text: " "})
	run(1)
	if app.took != 1 {
		t.Fatalf("a Space alone went to the window %d times, want once", app.took)
	}
	w.Input(input.KeyPress{Key: input.KeyM, Typed: true})
	w.Input(input.TextInput{Text: "m"})
	w.Input(input.KeyPress{Key: input.KeySpace, Typed: true})
	w.Input(input.TextInput{Text: " "})
	w.Input(input.TextInput{Text: "f"})
	run(1)
	var typed []string
	for _, v := range sent(w) {
		if s, ok := v.(typedText); ok {
			typed = append(typed, s.Text)
		}
	}
	if app.took != 1 || len(typed) == 0 || typed[len(typed)-1] != "m f" {
		t.Fatalf("typing \"m f\": the window took %d Spaces and the grid sent %q; want 1 and \"m f\"", app.took, typed)
	}
}
