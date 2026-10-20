// Package least finds the least of the last few values of a stream, as
// a limiter finds the least gain the frames ahead of it need: in a few
// steps a value, the same steps every time, and nothing to guess.
//
// It is van Herk's and Gil and Werman's way. The stream is cut into
// blocks as long as the window. The window over the last values is the
// end of the block before and the start of the one being filled; so the
// least of it is the least of the block before from a place to its end,
// worked out once that block was full, and the least of the one being
// filled so far. A queue of the values that may yet be the least, the
// usual way, takes as few steps on average, but how many each value
// takes depends on the values, which costs a processor more in wrong
// guesses than the steps themselves.
package least

import "math"

// Window holds the last values of a stream, as many as its length, for
// their least. Make it with [New]; it allocates nothing after.
type Window struct {
	// n is the window's length; block the block being filled, at where
	// the next goes, and first the least of it so far; after the least
	// of the block before from each place to its end, one more than n
	// long, its last always +Inf.
	n     int
	block []float64
	after []float64
	at    int
	first float64
}

// New returns an empty window that may be up to most values long, as
// long as most.
func New(most int) *Window {
	most = max(most, 1)
	w := &Window{block: make([]float64, most), after: make([]float64, most+1)}
	w.Resize(most)
	return w
}

// Resize makes the window n values long, from 1 to the most it was made
// for, and empties it.
func (w *Window) Resize(n int) {
	w.n = min(max(n, 1), len(w.block))
	w.Reset()
}

// Len returns how many values long the window is.
func (w *Window) Len() int { return w.n }

// Reset empties the window: it holds no values, and the least of none
// is +Inf.
func (w *Window) Reset() {
	inf := math.Inf(1)
	for i := range w.after[:w.n+1] {
		w.after[i] = inf
	}
	w.at, w.first = 0, inf
}

// Push adds v, and returns the least of the last values, as many as the
// window's length, v among them: of all pushed since the window was
// emptied, where fewer.
func (w *Window) Push(v float64) float64 {
	w.block[w.at] = v
	w.first = min(w.first, v)
	least := min(w.after[w.at+1], w.first)
	w.at++
	if w.at == w.n {
		// The block is full: the least of it from each place to its end,
		// for the windows over it and the next.
		s := math.Inf(1)
		for i := w.n - 1; i >= 0; i-- {
			s = min(s, w.block[i])
			w.after[i] = s
		}
		w.at, w.first = 0, math.Inf(1)
	}
	return least
}
