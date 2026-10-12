package widget

// RowY is where row k of l is now, from the list's top, for the tests.
func RowY(l *List, k Key) (float32, bool) {
	r, ok := l.rows[k]
	if !ok {
		return 0, false
	}
	return r.y.Value(), true
}
