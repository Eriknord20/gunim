package main

import (
	"testing"
	"time"
)

// The icon is drawn at every size, with a tile that fills its middle
// and leaves its corners clear, soon enough to open the window with.
func TestTheIconIsDrawnAtEverySize(t *testing.T) {
	began := time.Now()
	imgs := icons()
	fastest := time.Since(began)
	if len(imgs) != len(iconSizes) {
		t.Fatalf("drew %d icons", len(imgs))
	}
	for i, img := range imgs {
		n := iconSizes[i]
		if b := img.Bounds(); b.Dx() != n || b.Dy() != n {
			t.Fatalf("the %d icon is %v", n, b)
		}
		if _, _, _, a := img.At(n/2, n/2).RGBA(); a != 0xffff {
			t.Fatalf("the %d icon's middle is not solid", n)
		}
		if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
			t.Fatalf("the %d icon's corner is not clear", n)
		}
	}
	// The fastest of a few drawings: other programs, and the other
	// packages' tests, only ever add to the time one takes.
	budget := slowdown * 500 * time.Millisecond
	for tries := 1; tries < 5 && fastest > budget; tries++ {
		began := time.Now()
		icons()
		fastest = min(fastest, time.Since(began))
	}
	t.Logf("drawn in %v", fastest)
	if fastest > budget {
		t.Fatalf("drawing the icons took %v at the fastest of 5", fastest)
	}
}
