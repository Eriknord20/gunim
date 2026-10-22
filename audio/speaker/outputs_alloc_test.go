//go:build !race

package speaker

import (
	"testing"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/asio"
)

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

// TestMixingOnTheDriversThreadAllocatesNothing takes buffers from a
// direct output, four channels of them, as the driver's thread does:
// mixing them allocates nothing.
func TestMixingOnTheDriversThreadAllocatesNothing(t *testing.T) {
	useFake(t, asio.Info{Driver: "Fake", Rate: 48000, Buffer: 64, Bits: 32, Outputs: 8})
	m := audio.NewMixer()
	m.Play(steady(0.25), audio.Options{})
	var aux frameAux
	s, err := Open(m, Options{Driver: "Fake", Direct: true, Outputs: []int{0, 1, 4, 5}, Aux: aux.fill})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	r := ringOf(t, s)
	frames := make([]float32, 4*64)
	r.takeN(frames, 4)
	if allocs := testing.AllocsPerRun(100, func() { r.takeN(frames, 4) }); allocs != 0 {
		t.Errorf("mixing a buffer on the driver's thread allocates %v times", allocs)
	}
}
