//go:build !js

package desktop

import "time"

// shownLog matches the frames a window presents through DXGI with
// DXGI's frame statistics, which tell when the last frame shown lit the
// screen. It is Windows' alone, kept apart from the calls so that a test
// on any system can feed it statistics of its own.
type shownLog struct {
	// sent holds the frames presented that DXGI has yet to report
	// shown, oldest first.
	sent []sentFrame
	// lag is how long the last frame measured took from Present to the
	// screen, and has says one has been.
	lag time.Duration
	has bool
}

// sentFrame is a frame presented: its present count, and the
// performance counter as Present returned.
type sentFrame struct {
	count uint32
	qpc   int64
}

// frameStatistics is DXGI_FRAME_STATISTICS.
type frameStatistics struct {
	PresentCount        uint32
	PresentRefreshCount uint32
	SyncRefreshCount    uint32
	SyncQPCTime         int64
	SyncGPUTime         int64
}

// presented notes frame count, presented as the performance counter
// read qpc. A frame presented while the window is covered whole
// (DXGI_STATUS_OCCLUDED) never shows, and those before it are past
// telling: the log starts again.
func (l *shownLog) presented(count uint32, qpc int64, occluded bool) {
	if occluded {
		l.sent = l.sent[:0]
		return
	}
	if len(l.sent) == 8 {
		l.sent = l.sent[:copy(l.sent, l.sent[1:])]
	}
	if l.sent == nil {
		l.sent = make([]sentFrame, 0, 8)
	}
	l.sent = append(l.sent, sentFrame{count: count, qpc: qpc})
}

// learn takes in DXGI's statistics, with the performance counter
// counting freq a second. Each frame up to the one they report shown,
// PresentCount, is past. That one lit the screen at the refresh
// numbered PresentRefreshCount; SyncQPCTime is the time of refresh
// SyncRefreshCount, the latest. Where the two are one, as while frames
// come one a refresh, the frame lit the screen at SyncQPCTime. Where
// the frame was shown refreshes before, as the last frame before the
// window rested, the time of its own refresh is not told, and it is
// not measured.
func (l *shownLog) learn(st frameStatistics, freq int64) {
	i := 0
	for ; i < len(l.sent) && int32(l.sent[i].count-st.PresentCount) <= 0; i++ {
		f := l.sent[i]
		if f.count != st.PresentCount || st.SyncRefreshCount != st.PresentRefreshCount ||
			st.SyncQPCTime < f.qpc || freq <= 0 {
			continue
		}
		if d := time.Duration(float64(st.SyncQPCTime-f.qpc) * 1e9 / float64(freq)); d < time.Second {
			l.lag, l.has = d, true
		}
	}
	l.sent = l.sent[:copy(l.sent, l.sent[i:])]
}
