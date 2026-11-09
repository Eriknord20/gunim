package gunim

import (
	"testing"

	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// A node code asks to focus while a modal holds the keyboard takes it once the modal leaves.
func TestFocusAskedUnderAModalComesAsItLeaves(t *testing.T) {
	w := newTestWindow()
	m := &testModal{}
	w.ui.Insert(w.ui.Root(), m)
	run(w, 1)
	view := &focusRecorder{}
	w.ui.Insert(w.ui.Root(), view)
	run(w, 1)
	if w.ui.Focus(view) {
		t.Fatalf("the modal let the view have the keyboard")
	}
	w.ui.Remove(m)
	run(w, 2)
	if f := w.ui.Focused(); f != view {
		t.Fatalf("after the modal left, %T has the keyboard, want the view asked for", f)
	}
}

// Under two modals, the node asked for waits for both: the keyboard goes back to the first as the second leaves.
func TestFocusAskedUnderStackedModalsWaitsForAll(t *testing.T) {
	w := newTestWindow()
	view := &focusRecorder{}
	w.ui.Insert(w.ui.Root(), view)
	first := &testModal{}
	w.ui.Insert(w.ui.Root(), first)
	run(w, 1)
	second := &testModal{}
	w.ui.Insert(w.ui.Root(), second)
	run(w, 1)
	w.ui.Focus(view)
	w.ui.Remove(second)
	run(w, 2)
	if f := w.ui.Focused(); f != first {
		t.Fatalf("after the second modal left, %T has the keyboard, want the first modal", f)
	}
	w.ui.Remove(first)
	run(w, 2)
	if f := w.ui.Focused(); f != view {
		t.Fatalf("after both modals left, %T has the keyboard, want the view asked for", f)
	}
}

// cornerModal is a modal in the window's top left corner, leaving the rest of the window to be pressed.
type cornerModal struct{ testModal }

func (*cornerModal) Layout(Constraints, Frame, Children) geom.Size { return geom.Sz(50, 50) }

// namedFocus is a focusable node assistive technology can find by its name.
type namedFocus struct {
	focusRecorder
	name string
}

func (n *namedFocus) Access() access.Info { return access.Info{Role: access.RoleButton, Name: n.name} }

// A press on a node behind a modal is not remembered for when the modal leaves.
func TestAPressBehindAModalIsNotRemembered(t *testing.T) {
	w := newTestWindow()
	before, behind := &focusRecorder{}, &focusRecorder{}
	w.ui.Insert(w.ui.Root(), before)
	w.ui.Insert(w.ui.Root(), behind)
	run(w, 1)
	w.ui.Focus(before)
	m := &cornerModal{}
	w.ui.Insert(w.ui.Root(), m)
	run(w, 1)
	w.Input(input.PointerDown{Pos: geom.Pt(400, 300)})
	w.Input(input.PointerUp{Pos: geom.Pt(400, 300)})
	run(w, 1)
	w.ui.Remove(m)
	run(w, 2)
	if f := w.ui.Focused(); f != before {
		t.Fatalf("after the modal left, %T has the keyboard, want the node from before it", f)
	}
}

// A screen reader's request to focus a node behind a modal is not remembered for when the modal leaves.
func TestAScreenReaderFocusBehindAModalIsNotRemembered(t *testing.T) {
	w := newTestWindow()
	before, behind := &focusRecorder{}, &namedFocus{name: "Behind"}
	w.ui.Insert(w.ui.Root(), before)
	w.ui.Insert(w.ui.Root(), behind)
	w.Offscreen().ListenForAccess()
	run(w, 1)
	w.ui.Focus(before)
	m := &cornerModal{}
	w.ui.Insert(w.ui.Root(), m)
	run(w, 2)
	id, ok := accessID(w.Offscreen().AccessTree().Root, "Behind")
	if !ok {
		t.Fatal("no node named Behind in the access tree")
	}
	w.Input(access.Request{ID: id, Focus: true})
	run(w, 1)
	w.ui.Remove(m)
	run(w, 2)
	if f := w.ui.Focused(); f != before {
		t.Fatalf("after the modal left, %T has the keyboard, want the node from before it", f)
	}
}

// accessID returns the ID of the node named name in the access tree under n.
func accessID(n *access.Node, name string) (uint64, bool) {
	if n == nil {
		return 0, false
	}
	if n.Name == name {
		return n.ID, true
	}
	for _, k := range n.Children {
		if id, ok := accessID(k, name); ok {
			return id, true
		}
	}
	return 0, false
}

// A node asked for inside a modal waits only for the modals over that one, not for the modals under it.
func TestFocusAskedInsideAModalWaitsForTheModalsOverIt(t *testing.T) {
	w := newTestWindow()
	under := &testModal{}
	w.ui.Insert(w.ui.Root(), under)
	run(w, 1)
	dialog := &testModal{}
	w.ui.Insert(w.ui.Root(), dialog)
	field := &focusRecorder{}
	w.ui.Insert(dialog, field)
	run(w, 1)
	over := &testModal{}
	w.ui.Insert(w.ui.Root(), over)
	run(w, 1)
	w.ui.Focus(field)
	w.ui.Remove(over)
	run(w, 2)
	if f := w.ui.Focused(); f != field {
		t.Fatalf("after the modal over the dialog left, %T has the keyboard, want the field asked for", f)
	}
}

// A node asked for under a modal that leaves first does not cost the keyboard its way back to the node from before.
func TestFocusAskedThatLeavesFallsBackToTheNodeFromBefore(t *testing.T) {
	w := newTestWindow()
	before, gone := &focusRecorder{}, &focusRecorder{}
	w.ui.Insert(w.ui.Root(), before)
	w.ui.Insert(w.ui.Root(), gone)
	run(w, 1)
	w.ui.Focus(before)
	m := &testModal{}
	w.ui.Insert(w.ui.Root(), m)
	run(w, 1)
	w.ui.Focus(gone)
	w.ui.Remove(gone)
	run(w, 2)
	w.ui.Remove(m)
	run(w, 2)
	if f := w.ui.Focused(); f != before {
		t.Fatalf("after the modal left, %T has the keyboard, want the node from before it", f)
	}
}

// A node in a scope asked for from outside it takes the focus once the scope's modal leaves.
func TestFocusAskedIntoAScopeComesAsItsModalLeaves(t *testing.T) {
	w := newTestWindow()
	outside, inner := &focusRecorder{}, &focusRecorder{}
	scope := &scopeBox{}
	w.ui.Insert(w.ui.Root(), outside)
	w.ui.Insert(w.ui.Root(), scope)
	w.ui.Insert(scope, inner)
	run(w, 1)
	w.ui.Focus(outside)
	m := &testModal{}
	w.ui.Insert(scope, m)
	run(w, 1)
	w.ui.Focus(inner)
	w.ui.Remove(m)
	run(w, 2)
	if f := w.ui.Focused(); f != inner {
		t.Fatalf("after the scope's modal left, %T has the keyboard, want the node asked for", f)
	}
}
