// Package plugtest helps test plugins: it counts what a processor
// allocates, and plays an instrument notes to check what comes out.
package plugtest

import (
	"math"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/marrasen/gunim/audio/plug"
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

// Play prepares pr as s says and plays it as an instrument, as a host
// does, for the given number of frames in blocks of s.MaxBlock, and
// returns what came out: s.Channels channels of that many frames. The
// first block carries each of params' Default as a change at frame 0. Each event's
// Frame counts from the start, and Play hands it to the block it falls
// in, in the order of their frames. Play fills Out with NaN before each
// block, so a frame the instrument leaves unwritten shows.
func Play(t *testing.T, pr plug.Processor, s plug.Setup, frames int, params []plug.Param, events ...plug.Event) [][]float32 {
	t.Helper()
	if frames <= 0 || s.MaxBlock <= 0 || s.Channels <= 0 {
		t.Fatalf("plugtest: Play needs frames, MaxBlock and Channels above 0, has %d, %d and %d", frames, s.MaxBlock, s.Channels)
	}
	events = slices.Clone(events)
	slices.SortStableFunc(events, func(a, b plug.Event) int { return a.Frame - b.Frame })
	for _, e := range events {
		if e.Frame < 0 || e.Frame >= frames {
			t.Fatalf("plugtest: an event at frame %d falls outside the %d played", e.Frame, frames)
		}
	}
	// Every block is made before the first plays, so the playing
	// allocates nothing of its own.
	var blocks []plug.Block
	for at := 0; at < frames; at += s.MaxBlock {
		n := min(s.MaxBlock, frames-at)
		b := plug.Block{Frames: n, Position: plug.Position{Frame: int64(at), Playing: true}}
		for len(events) > 0 && events[0].Frame < at+n {
			e := events[0]
			e.Frame -= at
			b.Events = append(b.Events, e)
			events = events[1:]
		}
		blocks = append(blocks, b)
	}
	for _, p := range params {
		blocks[0].Changes = append(blocks[0].Changes, plug.Change{ID: p.ID, Value: p.Default})
	}
	out := make([][]float32, s.Channels)
	for c := range out {
		out[c] = make([]float32, frames)
	}
	scratch := make([][]float32, s.Channels)
	for c := range scratch {
		scratch[c] = make([]float32, s.MaxBlock)
	}
	for i := range blocks {
		blocks[i].Out = make([][]float32, s.Channels)
		for c := range scratch {
			blocks[i].Out[c] = scratch[c][:blocks[i].Frames]
		}
	}
	pr.Prepare(s)
	playBlocks(pr, blocks, out)
	return out
}

// playBlocks runs pr over blocks, each with Out filled with NaN first,
// and copies what comes out to out.
func playBlocks(pr plug.Processor, blocks []plug.Block, out [][]float32) {
	nan := float32(math.NaN())
	at := 0
	for i := range blocks {
		b := &blocks[i]
		for _, ch := range b.Out {
			for f := range ch {
				ch[f] = nan
			}
		}
		pr.Process(b)
		for c, ch := range b.Out {
			copy(out[c][at:], ch)
		}
		at += b.Frames
	}
}
