package audio

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// allocsUnder runs run, and returns how many things were made in it
// with fn among their callers: the allocations of the code under test
// alone, as the package's other goroutines, as a stream's decoder or a
// mixer's teller left by another test, allocate as they like meanwhile.
// Where it counts any, it logs where they were made.
func allocsUnder(t *testing.T, fn string, run func()) int64 {
	t.Helper()
	was := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = was }()
	count := func() map[string]int64 {
		// The profile is as of the last collection, finished.
		runtime.GC()
		runtime.GC()
		var recs []runtime.MemProfileRecord
		n, _ := runtime.MemProfile(nil, true)
		for {
			recs = make([]runtime.MemProfileRecord, n+50)
			var ok bool
			if n, ok = runtime.MemProfile(recs, true); ok {
				recs = recs[:n]
				break
			}
		}
		made := map[string]int64{}
		for _, r := range recs {
			var stack strings.Builder
			under := false
			frames := runtime.CallersFrames(r.Stack())
			for {
				f, more := frames.Next()
				fmt.Fprintf(&stack, "\n\t%s %s:%d", f.Function, f.File, f.Line)
				under = under || strings.Contains(f.Function, fn)
				if !more {
					break
				}
			}
			if under {
				made[stack.String()] += r.AllocObjects
			}
		}
		return made
	}
	before := count()
	run()
	var total int64
	for stack, n := range count() {
		if n -= before[stack]; n != 0 {
			total += n
			t.Logf("%d made at:%s", n, stack)
		}
	}
	return total
}
