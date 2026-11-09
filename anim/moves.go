package anim

// Moves counts the frames a view's springs changed a value in, for the key of a [gunim.Steady] node that draws by them:
// the key changes in each such frame, and holds once they rest. It counts the frame they come to rest in too, as the
// last step lands them on their target and reports them still.
//
// A view's Step passes on what its springs said:
//
//	func (v *View) Step(dt time.Duration) bool { return v.moves.Note(v.Group.Step(dt)) }
//
// The zero Moves is ready. Keep one for each group of springs, so a meter that falls on every frame leaves the keys of
// the views it has no part in alone.
type Moves struct {
	count uint64
	was   bool
}

// Note takes whether the springs moved in this step, and returns it.
func (m *Moves) Note(moving bool) bool {
	if moving || m.was {
		m.count++
	}
	m.was = moving
	return moving
}

// Count is the frames counted so far, the value for a key.
func (m *Moves) Count() uint64 { return m.count }
