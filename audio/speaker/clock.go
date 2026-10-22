package speaker

import "time"

// clockStart is the moment goNanotime counts from.
var clockStart = time.Now()

// goNanotime is a clock in nanoseconds, from Go's monotonic clock.
func goNanotime() int64 { return int64(time.Since(clockStart)) }
