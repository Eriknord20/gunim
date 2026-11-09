//go:build darwin && !ios

package capture

import "math"

// A converter converts a device's frames from its rate to the rate a
// stream records at, as they come in, on the device's thread: by
// Hermite's curve through the four frames around each frame made, as
// package audio converts a file. It allocates nothing after it is made.
//
// A device runs at another rate than the one asked for where it refuses
// that one, or the speakers play through it at their own.
type converter struct {
	ch int
	// step is how many of the device's frames pass in a frame made.
	step float64
	// most is how many frames convert takes in at once; held keeps the
	// last frames taken in, n of them, before the next; at is where the
	// next frame made falls among them, or past them, where step is large.
	most int
	held []float32
	n    int
	at   float64
	out  []float32
}

// newConverter returns a converter of ch channels from rate from to
// rate to, that takes in at most limit frames at once.
func newConverter(ch, from, to, limit int) *converter {
	c := &converter{ch: ch, step: float64(from) / float64(to), most: limit}
	// One frame of silence stands before the first, for the curve.
	c.held = make([]float32, (3+limit)*ch)
	c.n, c.at = 1, 1
	c.out = make([]float32, (int(float64(limit+3)/c.step)+2)*ch)
	return c
}

// convert takes in frames, the first of which reached the input ago of
// the device's frames before now, and hands put each piece made, with
// how many frames, at the rate made, its first reached the input
// before now.
func (c *converter) convert(frames []float32, ago int, put func(in []float32, ago int64)) {
	ch := c.ch
	for len(frames) >= ch {
		k := min(len(frames)/ch, c.most)
		first := c.n
		copy(c.held[c.n*ch:], frames[:k*ch])
		c.n += k
		start := c.at
		made := 0
		for int(c.at)+2 < c.n {
			i := int(c.at)
			t := float32(c.at - float64(i))
			a, b, cc, d := c.held[(i-1)*ch:], c.held[i*ch:], c.held[(i+1)*ch:], c.held[(i+2)*ch:]
			out := c.out[made*ch:]
			for j := range ch {
				out[j] = hermite(a[j], b[j], cc[j], d[j], t)
			}
			made++
			c.at += c.step
		}
		if made > 0 {
			// The first frame made falls start-first frames after the
			// first taken in, which came in ago frames before now.
			lag := float64(ago) - (start - float64(first))
			put(c.out[:made*ch], int64(math.Round(lag/c.step)))
		}
		// Keep the frame before the next to make, and those after; at
		// past them all skips the frames between in the next call.
		drop := min(int(c.at)-1, c.n)
		copy(c.held, c.held[drop*ch:c.n*ch])
		c.n -= drop
		c.at -= float64(drop)
		frames = frames[k*ch:]
		ago -= k
	}
}

// hermite returns the point t of the way from b to c on the curve
// through a, b, c and d.
func hermite(a, b, c, d, t float32) float32 {
	c1 := 0.5 * (c - a)
	c2 := a - 2.5*b + 2*c - 0.5*d
	c3 := 0.5*(d-a) + 1.5*(b-c)
	return ((c3*t+c2)*t+c1)*t + b
}
