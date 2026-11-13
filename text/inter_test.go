package text

import (
	"sync"
	"testing"
)

// Inter reads its faces once, however many goroutines ask at the same moment, and hands out the same two.
func TestInterIsReadOnce(t *testing.T) {
	var wg sync.WaitGroup
	got := make([]*Face, 8)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = Inter(i%2 == 1)
		}()
	}
	wg.Wait()
	for i, f := range got {
		if want := Inter(i%2 == 1); f != want || f == nil {
			t.Fatalf("call %d returned %p, want %p", i, f, want)
		}
	}
	if Inter(false) == Inter(true) {
		t.Fatal("regular and semibold are the same face")
	}
}

// Inter's semibold is wider than its regular: the two are different weights, each with metrics of its own.
func TestInterHasTwoWeights(t *testing.T) {
	regular, semibold := Inter(false).Shape("Plants", 24), Inter(true).Shape("Plants", 24)
	if semibold.Advance <= regular.Advance {
		t.Fatalf("semibold is %v wide, regular %v; want semibold wider", semibold.Advance, regular.Advance)
	}
	if a, d, _ := Inter(false).Metrics(17); a <= 0 || d <= 0 {
		t.Fatalf("Inter's extents at 17 are %v above and %v below, want both above 0", a, d)
	}
}
