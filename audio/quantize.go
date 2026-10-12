package audio

import (
	"math"
	"math/rand/v2"
)

// A Quantizer rounds sound to the steps of integer samples of Bits
// bits, 16 or 24, as a WAV file of those samples holds it, and leaves
// it as floats: played, it is heard as such a file sounds. Dither adds
// the noise a [WAVWriter] adds before it rounds. Bits of 32, or 0,
// leaves the sound as it is. A Quantizer is for one goroutine at a
// time.
type Quantizer struct {
	Bits   int
	Dither bool
	rng    *rand.Rand
}

// Ready makes what Process needs, so it makes nothing as it rounds.
func (q *Quantizer) Ready() {
	if q.rng == nil {
		q.rng = rand.New(rand.NewPCG(0x7175, 0x616e))
	}
}

// Process rounds frames in place.
func (q *Quantizer) Process(frames []float32) {
	if q.Bits != 16 && q.Bits != 24 {
		return
	}
	q.Ready()
	scale := float64(int64(1) << (q.Bits - 1))
	for i, s := range frames {
		v, _ := quantize(s, scale, q.Dither, q.rng)
		frames[i] = float32(float64(v) / scale)
	}
}

// quantize rounds s, from -1 to 1, to an integer sample of full scale
// scale, 2^(bits-1), with triangular dither an LSB either way where
// dither says. Full scale is held to one under at the top; clipped
// says s was past it.
func quantize(s float32, scale float64, dither bool, rng *rand.Rand) (v int32, clipped bool) {
	full := scale - 1
	x := float64(s) * scale
	if dither {
		x += rng.Float64() - rng.Float64()
	}
	x = math.Round(x)
	if x > full || x < -full-1 {
		clipped = true
		x = max(-full-1, min(x, full))
	}
	return int32(x), clipped
}
