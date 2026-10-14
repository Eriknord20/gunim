package asio

import (
	"slices"
	"testing"
)

func TestFitBuffer(t *testing.T) {
	for _, c := range []struct{ want, lo, hi, pref, gran, out int }{
		{0, 64, 2048, 256, -1, 256},
		{1000, 64, 2048, 256, -1, 1024},
		{5000, 64, 2048, 256, -1, 2048},
		{10, 64, 2048, 256, -1, 64},
		{700, 64, 2048, 256, 0, 256},
		{700, 96, 1024, 192, 32, 704},
		{700, 512, 512, 512, 1, 512},
		{1100, 100, 1000, 500, 100, 1000},
	} {
		if got := fitBuffer(c.want, c.lo, c.hi, c.pref, c.gran); got != c.out {
			t.Errorf("fitBuffer(%d, %d, %d, %d, %d) = %d, want %d", c.want, c.lo, c.hi, c.pref, c.gran, got, c.out)
		}
	}
}

// TestLayoutPlaysWhatTheDriverHas lays out the channels of the sound
// on a driver's outputs: the first two, or one, where none are named;
// those named, in their order; and nowhere for one the driver lacks or
// that is named twice.
func TestLayoutPlaysWhatTheDriverHas(t *testing.T) {
	for _, c := range []struct {
		want []int
		outs int
		out  []int
	}{
		{nil, 8, []int{0, 1}},
		{nil, 1, []int{0}},
		{[]int{2, 3}, 8, []int{2, 3}},
		{[]int{0, 1, 4, 5}, 8, []int{0, 1, 4, 5}},
		{[]int{0, 1, 2, 3}, 2, []int{0, 1, -1, -1}},
		{[]int{3, 3, -1, 2}, 4, []int{3, -1, -1, 2}},
		{[]int{}, 4, []int{}},
	} {
		if got := layout(c.want, c.outs); !slices.Equal(got, c.out) {
			t.Errorf("layout(%v, %d) = %v, want %v", c.want, c.outs, got, c.out)
		}
	}
}

// TestLayoutPlaysMaxOutputsAtMost names more outputs than a device
// plays, and finds those past MaxOutputs playing nowhere.
func TestLayoutPlaysMaxOutputsAtMost(t *testing.T) {
	want := make([]int, MaxOutputs+3)
	for i := range want {
		want[i] = i
	}
	got := layout(want, len(want))
	for i, o := range got {
		if (i < MaxOutputs) != (o == i) || (i >= MaxOutputs && o != -1) {
			t.Fatalf("channel %d plays on %d", i, o)
		}
	}
}
