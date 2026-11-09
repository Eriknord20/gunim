package widget

import "github.com/marrasen/gunim/geom"

// RowY is where row k of l is now, from the list's top, for the tests.
func RowY(l *List, k Key) (float32, bool) {
	r, ok := l.rows[k]
	if !ok {
		return 0, false
	}
	return r.y.Value(), true
}

// SelectionAt is where a list's selection fill is now.
func SelectionAt(l *List) geom.Rect { return l.sel.Value() }

// BadgeShown is how far a badge has grown in, 0 to 1.
func BadgeShown(b *Badge) float32 { return b.shown.Value() }

// LogAtEnd reports whether a log view is at its newest line, and
// LogOffset is how far down it is scrolled.
func LogAtEnd(v *LogView) bool     { return v.list.AtEnd() }
func LogOffset(v *LogView) float32 { return v.list.offset.Value() }

// KnobAt is how far a status switch's knob is towards on, 0 to 1.
func KnobAt(s *StatusSwitch) float32 { return s.knob.Value() }

// LogListAt is where a log view's lines start, inside it.
func LogListAt(v *LogView) geom.Point { return v.listAt }
