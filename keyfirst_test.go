package gunim

import (
	"testing"

	"github.com/marrasen/gunim/input"
)

// spaceFirst is a box that takes Space before the focused node inside it, unless that node takes text.
type spaceFirst struct {
	Box
	took int
}

func (b *spaceFirst) KeyFirst(e input.KeyPress, u *UI) bool {
	if e.Key != input.KeySpace || u.FocusTakesText() {
		return false
	}
	b.took++
	return true
}

// spaceTaker is a focusable node that takes Space and F1, as a button does, and may take text.
type spaceTaker struct {
	Box
	took int
	text bool
}

func (k *spaceTaker) Focusable() bool { return true }
func (k *spaceTaker) TakesText() bool { return k.text }
func (k *spaceTaker) Handle(e input.Event, _ *UI) bool {
	if p, ok := e.(input.KeyPress); ok && (p.Key == input.KeySpace || p.Key == input.KeyF1) {
		k.took++
		return true
	}
	return false
}

// A KeyFirst above the focus hears a press before the focused node, and what it leaves goes to the focus.
func TestAKeyFirstHearsAPressBeforeTheFocusedNode(t *testing.T) {
	w := newTestWindow()
	outer, inner := &spaceFirst{}, &spaceFirst{}
	button, field := &spaceTaker{}, &spaceTaker{text: true}
	w.ui.Insert(w.ui.Root(), outer)
	w.ui.Insert(outer, inner)
	w.ui.Insert(inner, button)
	w.ui.Insert(inner, field)
	run(w, 1)
	w.ui.Focus(button)
	w.ui.handlePlatform(input.KeyPress{Key: input.KeySpace})
	w.ui.handlePlatform(input.KeyPress{Key: input.KeyF1})
	if outer.took != 1 || inner.took != 0 || button.took != 1 {
		t.Fatalf("Space and F1 on a button: outer took %d, inner %d, button %d; want Space to the outer KeyFirst and F1 to the button", outer.took, inner.took, button.took)
	}
	w.ui.Focus(field)
	w.ui.handlePlatform(input.KeyPress{Key: input.KeySpace})
	if outer.took != 1 || field.took != 1 {
		t.Fatalf("Space in a text field went to the KeyFirst %d times and the field %d; want it in the field", outer.took-1, field.took)
	}
}

// A modal holding the keys keeps the KeyFirsts outside it out.
func TestAModalKeepsTheKeyFirstsOutsideItOut(t *testing.T) {
	w := newTestWindow()
	outer := &spaceFirst{}
	w.ui.Insert(w.ui.Root(), outer)
	run(w, 1)
	m := &testModal{}
	w.ui.Insert(outer, m)
	run(w, 1)
	inside := &spaceFirst{}
	button := &spaceTaker{}
	w.ui.Insert(m, inside)
	w.ui.Insert(inside, button)
	run(w, 1)
	w.ui.Focus(button)
	w.ui.handlePlatform(input.KeyPress{Key: input.KeySpace})
	if outer.took != 0 || inside.took != 1 || button.took != 0 {
		t.Fatalf("Space in a modal: outer KeyFirst took %d, the one in the modal %d, the button %d; want only the one in the modal", outer.took, inside.took, button.took)
	}
}
