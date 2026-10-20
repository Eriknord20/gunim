package least

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

// naive returns the least of the last n of vs, or of all where fewer.
func naive(vs []float64, n int) float64 {
	return slices.Min(vs[max(len(vs)-n, 0):])
}

func TestItIsTheLeastOfTheLastValues(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, n := range []int{1, 2, 3, 7, 241, 1000} {
		w := New(1000)
		w.Resize(n)
		vs := make([]float64, 0, 5000)
		for i := range 5000 {
			v := r.Float64()
			if i%50 < 10 {
				v = 1 // runs of equal values, as gains needing nothing are
			}
			vs = append(vs, v)
			if got, want := w.Push(v), naive(vs, n); got != want {
				t.Fatalf("window of %d, value %d: %g, not %g", n, i, got, want)
			}
		}
	}
}

func TestAResizedWindowStartsEmpty(t *testing.T) {
	w := New(10)
	for range 23 {
		w.Push(0.1)
	}
	w.Resize(4)
	if w.Len() != 4 {
		t.Fatalf("resized to 4, it is %d long", w.Len())
	}
	if got := w.Push(0.5); got != 0.5 {
		t.Errorf("the first value after a resize gives %g, not itself", got)
	}
	w.Resize(100)
	if w.Len() != 10 {
		t.Errorf("resized past its most, it is %d long, not 10", w.Len())
	}
	w.Reset()
	if got := w.Push(math.Inf(1)); !math.IsInf(got, 1) {
		t.Errorf("an emptied window gives %g", got)
	}
}

func BenchmarkPush(b *testing.B) {
	w := New(241)
	r := rand.New(rand.NewPCG(1, 2))
	vs := make([]float64, 4096)
	for i := range vs {
		vs[i] = r.Float64()
	}
	s := 0.0
	for b.Loop() {
		for _, v := range vs {
			s += w.Push(v)
		}
	}
	_ = s
}
