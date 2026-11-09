package gunim

import "github.com/marrasen/gunim/input"

// A Modal is a node that holds the keyboard while it is in the tree, such as a dialog. It takes the focus as it
// arrives. While it stays, the focus and Tab keep to it and to the popups opened from inside it, and when it leaves,
// the focus goes back to where it was before, or to the node code last asked to focus meanwhile.
//
// A modal inside a [ModalScope] holds the keyboard only within the scope.
type Modal interface {
	Node
	Modal() bool
}

// A ModalScope is a node that keeps the modals inside it to itself, such as a pane among others that shows a dialog
// over its own content only. Such a modal holds the keyboard within the scope: the focus and Tab keep to it there,
// focusing anything else in the scope from outside it focuses the modal instead, and the keys pressed in it go no
// further, while the rest of the window works as before. It takes the focus as it arrives only when the focus is in the scope already, or
// nowhere.
type ModalScope interface {
	Node
	ModalScope()
}

// modalHold is a modal in the tree, the node that had the focus before it came, the node code last asked to focus
// behind it, and the scope it holds the keyboard in, or nil for the whole window.
type modalHold struct {
	s, back, asked, scope *state
}

// arrived gives a modal that just came into the tree the focus.
func (u *UI) arrived(s *state) {
	m, ok := s.node.(Modal)
	if !ok || !m.Modal() || u.held(s) {
		return
	}
	h := modalHold{s: s, back: u.focus, scope: scopeOf(s)}
	u.modals = append(u.modals, h)
	if h.scope == nil || u.focus == nil || inside(u.focus, h.scope) {
		u.Focus(s.node)
	}
}

// focusAfter has s take the focus when the last modal of h's scope that keeps s out leaves.
func (u *UI) focusAfter(h modalHold, s *state) {
	at := -1
	for i := len(u.modals) - 1; i >= 0; i-- {
		m := u.modals[i]
		if m.scope != h.scope || !u.live(m) {
			continue
		}
		if inside(s, m.s) {
			break
		}
		at = i
	}
	if at >= 0 {
		u.modals[at].asked = s
	}
}

// held reports whether s holds the keyboard as a modal already.
func (u *UI) held(s *state) bool {
	for _, h := range u.modals {
		if h.s == s && u.live(h) {
			return true
		}
	}
	return false
}

// rearrived takes the modals in s, a subtree coming back as it was
// leaving, as arriving again: they hold the keyboard again, as the
// subtree going had let them go.
func (u *UI) rearrived(s *state) {
	if s.presence == Exiting {
		return
	}
	u.arrived(s)
	for _, k := range s.kids {
		u.rearrived(k)
	}
}

// scopeOf returns the nearest [ModalScope] round s, or nil.
func scopeOf(s *state) *state {
	for a := s.up(); a != nil; a = a.up() {
		if _, ok := a.node.(ModalScope); ok {
			return a
		}
	}
	return nil
}

// live reports whether the modal h holds is still in the tree and not leaving.
func (u *UI) live(h modalHold) bool { return u.index[h.s.node] == h.s && !h.s.leaving() }

// modal returns the modal holding the keyboard for a node at s, or with nothing focused for a nil s: the last to
// arrive that is still in the tree and not leaving, of those over the whole window and those whose scope s is in.
func (u *UI) modal(s *state) *state {
	if h, ok := u.holdFor(s); ok {
		return h.s
	}
	return nil
}

// holdFor is modal, with the hold.
func (u *UI) holdFor(s *state) (modalHold, bool) {
	for i := len(u.modals) - 1; i >= 0; i-- {
		h := u.modals[i]
		if !u.live(h) {
			continue
		}
		if h.scope == nil || s != nil && inside(s, h.scope) {
			return h, true
		}
	}
	return modalHold{}, false
}

// scopedModal returns the modal holding the keyboard in scope s, when s is a [ModalScope] with one, or nil.
func (u *UI) scopedModal(s *state) *state {
	for i := len(u.modals) - 1; i >= 0; i-- {
		if h := u.modals[i]; h.scope == s && u.live(h) {
			return h.s
		}
	}
	return nil
}

// left forgets the modals that have left or are leaving, and gives the focus to the node asked for under the last of
// them to go, or else back to what had it before that one came, when it can still take it.
func (u *UI) left() {
	var asked, back *state
	kept := u.modals[:0]
	var found bool
	for _, h := range u.modals {
		if u.live(h) {
			kept = append(kept, h)
			continue
		}
		if !found {
			// A scoped modal that came while the keyboard was elsewhere gives it back to nothing outside its scope.
			found, asked, back = true, h.asked, h.back
			if h.scope != nil && back != nil && !inside(back, h.scope) {
				back = nil
			}
		}
	}
	u.modals = kept
	if u.focus != nil && !u.focus.leaving() {
		return
	}
	for _, s := range []*state{asked, back} {
		if s != nil && u.index[s.node] == s && !s.leaving() && u.Focus(s.node) {
			return
		}
	}
}

// stopsAtModal returns the modal that holds ev, or nil. A modal holds a
// key, a press or typed text, while it is open and has the focus. A
// release goes on, so a node that saw a key go down before the modal
// came hears it come up.
func (u *UI) stopsAtModal(ev input.Event) *state {
	switch ev.(type) {
	case input.KeyPress, input.TextInput, input.Composing, input.TextEdit:
	default:
		return nil
	}
	if m := u.modal(u.focus); m != nil && u.focus != nil && inside(u.focus, m) {
		return m
	}
	return nil
}

// inside reports whether s is m or lies within it, counting a popup's tree as within the node that opened it.
func inside(s, m *state) bool {
	for a := s; a != nil; {
		if a == m {
			return true
		}
		if a.parent == nil {
			a = a.opener
			continue
		}
		a = a.parent
	}
	return false
}
