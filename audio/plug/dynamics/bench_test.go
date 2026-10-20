package dynamics

import (
	"fmt"
	"testing"

	"github.com/marrasen/gunim/audio/plug"
)

// benchSetups are the ways the benchmarks set the plugin: as it comes,
// with every part on and a band sidechain, and as a brickwall driven
// hard.
var benchSetups = []struct {
	name string
	set  []plug.Change
}{
	{"default", nil},
	{"everything", set(float64(GateOnID), 1, float64(GateThresholdID), -40, float64(SideModeID), SideBand,
		float64(CompThresholdID), -30, float64(CompRatioID), 4)},
	{"brickwall", limiting(12, -1)},
}

// BenchmarkLongOpenGate times a second of sound through a gate long
// open and a compressor long let go, after five seconds of the same:
// their gains have long since all but reached 0.
func BenchmarkLongOpenGate(b *testing.B) {
	const block = 128
	pr := Descriptor.New(nil).Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: block, Channels: 2})
	l := loud(48000, 1)
	for i := range l {
		l[i] *= 0.05
	}
	out := [][]float32{make([]float32, block), make([]float32, block)}
	blk := &plug.Block{Out: out, Frames: block,
		Changes: set(float64(GateOnID), 1, float64(GateThresholdID), -80, float64(GateAttackID), 0.5)}
	second := func() {
		for i := 0; i+block <= len(l); i += block {
			blk.In = [][]float32{l[i : i+block], l[i : i+block]}
			blk.Position.Frame += block
			pr.Process(blk)
			blk.Changes = nil
		}
	}
	for range 5 {
		second()
	}
	b.ResetTimer()
	for b.Loop() {
		second()
	}
}

// BenchmarkTail times a second of silence through the plugin with every
// part on, 1, 10 and 100 seconds after a second of loud sound: where its
// envelopes, gains and filters fall away toward 0, and would sink into
// numbers too small to work out fast.
func BenchmarkTail(b *testing.B) {
	const block = 128
	for _, secs := range []int{1, 10, 100} {
		b.Run(fmt.Sprintf("%ds", secs), func(b *testing.B) {
			pr := Descriptor.New(nil).Processor()
			pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: block, Channels: 2})
			l := loud(48000, 1)
			quiet := make([]float32, block)
			out := [][]float32{make([]float32, block), make([]float32, block)}
			blk := &plug.Block{Out: out, Frames: block, Changes: benchSetups[1].set}
			for i := 0; i+block <= len(l); i += block {
				blk.In = [][]float32{l[i : i+block], l[i : i+block]}
				pr.Process(blk)
				blk.Changes = nil
			}
			blk.In = [][]float32{quiet, quiet}
			second := func() {
				for range 48000 / block {
					blk.Position.Frame += block
					pr.Process(blk)
				}
			}
			for range secs {
				second()
			}
			b.ResetTimer()
			for b.Loop() {
				second()
			}
		})
	}
}

// BenchmarkProcess times a second of loud sound through the plugin, in
// blocks of 128 frames: an op is a second of sound.
func BenchmarkProcess(b *testing.B) {
	const block = 128
	for _, s := range benchSetups {
		b.Run(s.name, func(b *testing.B) {
			pr := Descriptor.New(nil).Processor()
			pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: block, Channels: 2})
			l, r := loud(48000, 1), loud(48000, 2)
			out := [][]float32{make([]float32, block), make([]float32, block)}
			pr.Process(&plug.Block{In: [][]float32{l[:block], r[:block]}, Out: out, Frames: block, Changes: s.set})
			blk := &plug.Block{Out: out, Frames: block}
			b.ResetTimer()
			for b.Loop() {
				for i := 0; i+block <= len(l); i += block {
					blk.In = [][]float32{l[i : i+block], r[i : i+block]}
					blk.Position.Frame += block
					pr.Process(blk)
				}
			}
		})
	}
}
