package gunim

import (
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// fingerEvent takes a finger from the driver, in a window that takes
// them: its FingerDown goes to the node under it, or
// the nearest around it that takes it, and the rest of that finger to
// the same node, wherever it goes meanwhile, as a pointer's press does.
func (u *UI) fingerEvent(root *state, e input.Finger) {
	if e.Phase == input.FingerDown {
		// A driver gives an id out again once its finger lifts, so a
		// finger still held under it had its lift lost: it lifts first.
		if old := u.fingers[e.ID]; old != nil {
			delete(u.fingers, e.ID)
			u.deliver(old, input.Finger{ID: e.ID, Pos: input.Away, Phase: input.FingerUp, Time: e.Time})
		}
		took := u.dispatchAt(root, e.Pos, func(local geom.Point) input.Event {
			return input.Finger{ID: e.ID, Pos: local, Phase: e.Phase, Time: e.Time}
		})
		if took != nil {
			if u.fingers == nil {
				u.fingers = map[int]*state{}
			}
			u.fingers[e.ID] = took
		}
		return
	}
	s := u.fingers[e.ID]
	if e.Phase == input.FingerUp {
		delete(u.fingers, e.ID)
	}
	if s == nil {
		return
	}
	pos := e.Pos
	if pos != input.Away {
		pos = u.localHeld(s, pos)
	}
	u.deliver(s, input.Finger{ID: e.ID, Pos: pos, Phase: e.Phase, Time: e.Time})
}

// holdsFinger reports whether s took a finger that is still down.
func (u *UI) holdsFinger(s *state) bool {
	for _, to := range u.fingers {
		if to == s {
			return true
		}
	}
	return false
}

// loseFingers lets the fingers that went to s, or a node inside it,
// which is leaving the tree, go nowhere more.
func (u *UI) loseFingers(s *state) {
	for id, to := range u.fingers {
		if to.within(s) {
			delete(u.fingers, id)
		}
	}
}
