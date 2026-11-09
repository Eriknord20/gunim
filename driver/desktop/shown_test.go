//go:build !js

package desktop

import (
	"testing"
	"time"
)

// The counter of these tests counts each microsecond.
const testFreq = 1_000_000

// While frames come one a refresh, each report measures the frame it
// names from its Present to SyncQPCTime, and lets go of the frames up
// to it; the frames after wait for their own report.
func TestDXGIStatisticsMeasureTheFrameShown(t *testing.T) {
	var l shownLog
	// Frame n is presented at n×8333 µs and lights the screen two
	// refreshes on, 16 666 µs later, at refresh 100+n+2.
	for n := uint32(1); n <= 40; n++ {
		l.presented(n, int64(n)*8333, false)
		if n < 3 {
			continue
		}
		shown := n - 2
		l.learn(frameStatistics{PresentCount: shown, PresentRefreshCount: 100 + n, SyncRefreshCount: 100 + n,
			SyncQPCTime: int64(shown)*8333 + 16666}, testFreq)
		if !l.has || l.lag != 16666*time.Microsecond {
			t.Fatalf("frame %d: lag %v (measured %v), want 16.666ms", n, l.lag, l.has)
		}
		if len(l.sent) != 2 || l.sent[0].count != n-1 {
			t.Fatalf("frame %d: %d frames wait, the first %d; want frames %d and %d", n, len(l.sent), l.sent[0].count, n-1, n)
		}
	}
}

// After the window rests, the last frame stays shown, and DXGI reports
// it with the time of a later refresh. That frame is not measured, and
// the lag stays as it was.
func TestDXGIStatisticsLeaveAFrameShownRefreshesBefore(t *testing.T) {
	var l shownLog
	l.presented(1, 1000, false)
	l.learn(frameStatistics{PresentCount: 1, PresentRefreshCount: 50, SyncRefreshCount: 50, SyncQPCTime: 9000}, testFreq)
	if l.lag != 8*time.Millisecond {
		t.Fatalf("lag %v, want 8ms", l.lag)
	}
	l.presented(2, 20_000, false)
	// 300 ms later: frame 2 lit the screen at refresh 52, and the
	// statistics speak of refresh 88.
	for k := range uint32(3) {
		l.learn(frameStatistics{PresentCount: 2, PresentRefreshCount: 52, SyncRefreshCount: 88 + k,
			SyncQPCTime: 320_000 + int64(k)*8333}, testFreq)
		if l.lag != 8*time.Millisecond {
			t.Fatalf("report %d after a rest: lag %v, want it left at 8ms", k, l.lag)
		}
		if len(l.sent) != 0 {
			t.Fatalf("report %d after a rest: %d frames wait, want none", k, len(l.sent))
		}
	}
}

// A frame presented while the window is covered never shows: the log
// starts again, and a report of a frame before it measures nothing.
func TestDXGIStatisticsForgetFramesAtAnOccludedPresent(t *testing.T) {
	var l shownLog
	l.presented(1, 1000, false)
	l.presented(2, 9000, false)
	l.presented(3, 17_000, true)
	if len(l.sent) != 0 {
		t.Fatalf("%d frames wait after an occluded present, want none", len(l.sent))
	}
	l.learn(frameStatistics{PresentCount: 2, PresentRefreshCount: 9, SyncRefreshCount: 9, SyncQPCTime: 30_000}, testFreq)
	if l.has {
		t.Fatalf("a frame from before the occluded present was measured: %v", l.lag)
	}
	l.presented(4, 40_000, false)
	l.learn(frameStatistics{PresentCount: 4, PresentRefreshCount: 12, SyncRefreshCount: 12, SyncQPCTime: 48_000}, testFreq)
	if !l.has || l.lag != 8*time.Millisecond {
		t.Fatalf("the frame after: lag %v (measured %v), want 8ms", l.lag, l.has)
	}
}

// Present counts wrap at 2³²; the frames on either side of the wrap
// keep their order. The counter here runs at 10 MHz, as Windows' does.
func TestDXGIStatisticsKeepOrderAcrossTheCountsWrap(t *testing.T) {
	var l shownLog
	for i, c := range []uint32{1<<32 - 2, 1<<32 - 1, 0, 1} {
		l.presented(c, int64(i)*100_000, false)
	}
	l.learn(frameStatistics{PresentCount: 0, PresentRefreshCount: 5, SyncRefreshCount: 5, SyncQPCTime: 250_000}, 10_000_000)
	if l.lag != 5*time.Millisecond {
		t.Fatalf("lag %v, want 5ms", l.lag)
	}
	if len(l.sent) != 1 || l.sent[0].count != 1 {
		t.Fatalf("frames waiting %v, want frame 1 alone", l.sent)
	}
}
