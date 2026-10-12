// Package plugtest helps test plugins: it counts what a processor
// allocates, plays an instrument notes, and drives a plugin's ports, to
// check what comes out.
package plugtest

import (
	"fmt"
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
// first block carries each settable parameter's Default as a change at
// frame 0, as [Drive]'s does. Each event's
// Frame counts from the start, and Play hands it to the block it falls
// in, in the order of their frames. Play fills Out with NaN before each
// block, so a frame the instrument leaves unwritten shows.
func Play(t *testing.T, pr plug.Processor, s plug.Setup, frames int, params []plug.Param, events ...plug.Event) [][]float32 {
	t.Helper()
	d := plug.Descriptor{Params: params}
	return Drive(t, &d, pr, s, frames, Ports{Events: events}).Out
}

// Ports is what [Drive] feeds a processor, each of it as long as the
// run, its frames counted from the start.
type Ports struct {
	// In is the main input's channels, or nil for an instrument.
	In [][]float32
	// AuxIn holds each aux input's channels, by [plug.Descriptor.AuxIn]:
	// nil, or a list too short, leaves one unconnected.
	AuxIn [][][]float32
	// ControlIn holds each control input's values, by
	// [plug.Descriptor.ControlIn]: nil, or a list too short, leaves one
	// unconnected.
	ControlIn [][]float32
	// Changes are changes to parameters, after each one's Default at
	// frame 0.
	Changes []plug.Change
	// Events are notes, for a plugin that takes them.
	Events []plug.Event
}

// Result is what came out of a processor [Drive] ran, each of it as long
// as the run.
type Result struct {
	// Out is the main output's channels.
	Out [][]float32
	// AuxOut holds each aux output's channels, by
	// [plug.Descriptor.AuxOut].
	AuxOut [][][]float32
	// ControlOut holds each control output's values, by
	// [plug.Descriptor.ControlOut].
	ControlOut [][]float32
}

// Drive prepares pr, a processor of the plugin d describes, as s says,
// and runs it as a host does, for the given number of frames in blocks
// of s.MaxBlock, with the ports in p connected and every output
// connected, and returns what came out. The first block carries each of
// d's settable parameters' Default as a change at frame 0, and p's changes and
// events go to the blocks they fall in, in the order of their frames.
// Drive fills every output with NaN before each block, so a frame the
// processor leaves unwritten shows.
func Drive(t *testing.T, d *plug.Descriptor, pr plug.Processor, s plug.Setup, frames int, p Ports) Result {
	t.Helper()
	if frames <= 0 || s.MaxBlock <= 0 || s.Channels <= 0 {
		t.Fatalf("plugtest: Drive needs frames, MaxBlock and Channels above 0, has %d, %d and %d", frames, s.MaxBlock, s.Channels)
	}
	if len(p.AuxIn) > len(d.AuxIn) || len(p.ControlIn) > len(d.ControlIn) {
		t.Fatalf("plugtest: Drive was given %d aux and %d control inputs, for a plugin of %d and %d",
			len(p.AuxIn), len(p.ControlIn), len(d.AuxIn), len(d.ControlIn))
	}
	long := func(what string, chans [][]float32) {
		for c, ch := range chans {
			if ch != nil && len(ch) < frames {
				t.Fatalf("plugtest: %s channel %d has %d frames, of the %d run", what, c, len(ch), frames)
			}
		}
	}
	long("the main input's", p.In)
	for i, port := range p.AuxIn {
		long(fmt.Sprintf("aux input %d's", i), port)
	}
	long("the control inputs'", p.ControlIn)
	events := slices.Clone(p.Events)
	slices.SortStableFunc(events, func(a, b plug.Event) int { return a.Frame - b.Frame })
	for _, e := range events {
		if e.Frame < 0 || e.Frame >= frames {
			t.Fatalf("plugtest: an event at frame %d falls outside the %d played", e.Frame, frames)
		}
	}
	var changes []plug.Change
	for _, prm := range d.Params {
		if prm.Flags&plug.ReadOnly == 0 {
			changes = append(changes, plug.Change{ID: prm.ID, Value: prm.Default})
		}
	}
	more := slices.Clone(p.Changes)
	slices.SortStableFunc(more, func(a, b plug.Change) int { return a.Frame - b.Frame })
	for _, c := range more {
		if c.Frame < 0 || c.Frame >= frames {
			t.Fatalf("plugtest: a change at frame %d falls outside the %d run", c.Frame, frames)
		}
	}
	changes = append(changes, more...)

	// What comes out, and the room each block writes to.
	res := Result{Out: channels(s.Channels, frames), AuxOut: make([][][]float32, len(d.AuxOut)),
		ControlOut: channels(len(d.ControlOut), frames)}
	auxOut := make([][][]float32, len(d.AuxOut))
	for i, port := range d.AuxOut {
		n := port.Channels
		if n == 0 {
			n = s.Channels
		}
		res.AuxOut[i] = channels(n, frames)
		auxOut[i] = channels(n, s.MaxBlock)
	}
	out, controlOut := channels(s.Channels, s.MaxBlock), channels(len(d.ControlOut), s.MaxBlock)

	// Every block is made before the first runs, so the running
	// allocates nothing of its own.
	var blocks []plug.Block
	for at := 0; at < frames; at += s.MaxBlock {
		n := min(s.MaxBlock, frames-at)
		b := plug.Block{Frames: n, Position: plug.Position{Frame: int64(at), Playing: true},
			In: window(p.In, at, n), Out: window(out, 0, n), ControlIn: window(p.ControlIn, at, n),
			ControlOut: window(controlOut, 0, n)}
		for _, port := range p.AuxIn {
			b.AuxIn = append(b.AuxIn, window(port, at, n))
		}
		for _, port := range auxOut {
			b.AuxOut = append(b.AuxOut, window(port, 0, n))
		}
		for len(events) > 0 && events[0].Frame < at+n {
			e := events[0]
			e.Frame -= at
			b.Events = append(b.Events, e)
			events = events[1:]
		}
		for len(changes) > 0 && changes[0].Frame < at+n {
			c := changes[0]
			c.Frame -= at
			b.Changes = append(b.Changes, c)
			changes = changes[1:]
		}
		blocks = append(blocks, b)
	}
	pr.Prepare(s)
	playBlocks(pr, blocks, &res)
	return res
}

// channels returns n channels of the given number of frames.
func channels(n, frames int) [][]float32 {
	out := make([][]float32, n)
	for c := range out {
		out[c] = make([]float32, frames)
	}
	return out
}

// window returns the frames from at, n of them, of each of chans, or nil
// where chans is nil, as a port unconnected.
func window(chans [][]float32, at, n int) [][]float32 {
	if chans == nil {
		return nil
	}
	out := make([][]float32, len(chans))
	for c, ch := range chans {
		if ch != nil {
			out[c] = ch[at : at+n]
		}
	}
	return out
}

// playBlocks runs pr over blocks, each with its outputs filled with NaN
// first, and copies what comes out to res.
func playBlocks(pr plug.Processor, blocks []plug.Block, res *Result) {
	at := 0
	for i := range blocks {
		b := &blocks[i]
		fill(b.Out)
		fill(b.ControlOut)
		for _, port := range b.AuxOut {
			fill(port)
		}
		pr.Process(b)
		keep(res.Out, b.Out, at)
		keep(res.ControlOut, b.ControlOut, at)
		for i, port := range b.AuxOut {
			keep(res.AuxOut[i], port, at)
		}
		at += b.Frames
	}
}

// fill fills each of chans with NaN.
func fill(chans [][]float32) {
	nan := float32(math.NaN())
	for _, ch := range chans {
		for f := range ch {
			ch[f] = nan
		}
	}
}

// keep copies each of chans to dst's, from frame at.
func keep(dst, chans [][]float32, at int) {
	for c, ch := range chans {
		copy(dst[c][at:], ch)
	}
}
