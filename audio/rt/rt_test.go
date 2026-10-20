package rt

import (
	"sync"
	"testing"
)

// Enter and its leave pair up, again and again, nested and on many
// goroutines at once, without a panic.
func TestEnterLeavePairs(t *testing.T) {
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				outer := Enter()
				inner := Enter()
				inner()
				outer()
			}
		}()
	}
	wg.Wait()
}
