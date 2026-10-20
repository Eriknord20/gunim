package maximizer

import (
	"fmt"
	"testing"

	"github.com/marrasen/gunim/audio/plug"
)

// benchModes are the modes the benchmarks run, each 6 dB up.
var benchModes = []struct {
	name string
	set  []plug.Change
}{
	{"transparent", set(float64(GainID), 6, float64(ModeID), Transparent)},
	{"punchy", set(float64(GainID), 6, float64(ModeID), Punchy, float64(LinkID), 50)},
	{"loud", set(float64(GainID), 6, float64(ModeID), Loud, float64(CharacterID), 7)},
}

// BenchmarkTruePeak times a second of a channel's true peaks: an op is
// a second of sound.
func BenchmarkTruePeak(b *testing.B) {
	m := music(48000, 1)
	x, out := make([]float64, len(m)), make([]float64, len(m))
	for i, v := range m {
		x[i] = float64(v)
	}
	var t truePeak
	for b.Loop() {
		t.run(x, out)
	}
}

// BenchmarkTail times a second of silence through the maximizer, 1, 10
// and 100 seconds after a second of music, as its gains come back and
// what it follows falls away toward 0.
func BenchmarkTail(b *testing.B) {
	const block = 128
	for _, secs := range []int{1, 10, 100} {
		b.Run(fmt.Sprintf("%ds", secs), func(b *testing.B) {
			pr := Descriptor.New(nil).Processor()
			pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: block, Channels: 2})
			l := music(48000, 1)
			quiet := make([]float32, block)
			out := [][]float32{make([]float32, block), make([]float32, block)}
			blk := &plug.Block{Out: out, Frames: block, Changes: benchModes[0].set}
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

// BenchmarkProcess times a second of music through the maximizer in
// each mode, in blocks of 128 frames: an op is a second of sound.
func BenchmarkProcess(b *testing.B) {
	const block = 128
	for _, m := range benchModes {
		b.Run(m.name, func(b *testing.B) {
			pr := Descriptor.New(nil).Processor()
			pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: block, Channels: 2})
			l, r := music(48000, 1), music(48000, 2)
			out := [][]float32{make([]float32, block), make([]float32, block)}
			pr.Process(&plug.Block{In: [][]float32{l[:block], r[:block]}, Out: out, Frames: block, Changes: m.set})
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
