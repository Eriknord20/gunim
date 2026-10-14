package audioui

import (
	"strconv"
	"sync"
	"testing"
)

// Every window paints on a goroutine of its own, and each shapes its
// labels through the one cache: run with -race, this catches a cache
// left unguarded.
func TestShapedFromManyWindows(t *testing.T) {
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Go(func() {
			for i := range 500 {
				s := strconv.Itoa((i + w*37) % 300)
				if r := Shaped(s, 8, w%2 == 0, i%2 == 0); len(r.Glyphs) == 0 {
					t.Errorf("Shaped(%q) has no glyphs", s)
					return
				}
			}
		})
	}
	wg.Wait()
}
