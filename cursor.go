package gunim

import (
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// shapePointer sets the pointer's shape for the pointer at p, in the
// space of the window whose tree starts at root: the shape the node
// holding the pointer names, or else the node under it, or else the
// nodes around that. The window is told only when the shape changes.
func (u *UI) shapePointer(root *state, p geom.Point) {
	s := u.capture
	if s == nil || !s.within(root) {
		s = u.hit(root, p)
	}
	c := input.CursorArrow
	if edge, ok := u.edgeAt(p); ok && root == u.root {
		c, s = edgeCursor(edge), nil
	}
	for ; s != nil; s = s.parent {
		cs, ok := s.node.(CursorShaper)
		if !ok {
			continue
		}
		named := input.CursorInherit
		u.ask(s, func() { named = cs.Cursor(u.local(s, p)) })
		if named != input.CursorInherit {
			c = named
			break
		}
	}
	dw := u.w.dw
	for _, sf := range u.popups {
		if sf.root == root && sf.dw != nil {
			dw = sf.dw
		}
	}
	if u.cursors == nil {
		u.cursors = map[driver.Window]input.Cursor{}
	}
	if last, ok := u.cursors[dw]; ok && last == c {
		return
	}
	u.cursors[dw] = c
	if cs, ok := dw.(driver.CursorSetter); ok {
		cs.SetCursor(c)
	}
}
