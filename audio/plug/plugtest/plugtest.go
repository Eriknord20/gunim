// Package plugtest helps test plugins.
package plugtest

import (
	"runtime"
	"strings"
	"testing"
)

// AllocsUnder runs run, and returns how many things were made in it
// with a function named fn among their callers: the allocations of the
// code under test alone, as other goroutines allocate as they like
// meanwhile. fn is part of a function's full name, as
// "maximizer.(*processor).Process".
func AllocsUnder(t *testing.T, fn string, run func()) int64 {
	t.Helper()
	was := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = was }()
	count := func() int64 {
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
		var total int64
		for _, r := range recs {
			frames := runtime.CallersFrames(r.Stack())
			for {
				f, more := frames.Next()
				if strings.Contains(f.Function, fn) {
					total += r.AllocObjects
					break
				}
				if !more {
					break
				}
			}
		}
		return total
	}
	before := count()
	run()
	return count() - before
}
