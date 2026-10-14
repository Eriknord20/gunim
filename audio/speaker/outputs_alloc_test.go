//go:build !race

package speaker

import "testing"

// TestTakingManyChannelsAllocatesNothing takes a buffer of four
// channels, as the driver's thread does, and fills and interleaves
// one, as the feed does: neither allocates.
func TestTakingManyChannelsAllocatesNothing(t *testing.T) {
	r := &ringOutput{s: &Speaker{}, chans: 4, wake: make(chan struct{}, 1)}
	q := newRing(1024, 4)
	r.q.Store(q)
	var aux frameAux
	r.s.setAux(aux.fill)
	main, a, wide := make([]float32, 2*64), make([]float32, 2*64), make([]float32, 4*64)
	frames := make([]float32, 4*64)
	allocs := testing.AllocsPerRun(100, func() {
		r.s.pullAux(a, 2, 0)
		interleave(wide, main, a, 4)
		q.push(wide)
		r.takeN(frames, 4)
	})
	if allocs != 0 {
		t.Errorf("a buffer of four channels allocates %v times", allocs)
	}
}
