package audio

import (
	"math"
	"math/cmplx"
)

// An Analyzer measures the sound a [Mixer] is playing as it is heard:
// how loud it is, and how loud each band of pitches is, for visuals
// that move with the music. Measure it once a frame of the display.
// An Analyzer is for one goroutine at a time.
type Analyzer struct {
	m      *Mixer
	size   int
	window []float32
	in     []float32
	fft    []complex128
	// edges are the bins each band starts at, one more than the bands,
	// at rate, the mixer's rate as they were worked out.
	edges []int
	bands int
	rate  int
	// Floor is the quietest level a band shows, in decibels: a band
	// that loud or quieter is at 0. It is -70 by default.
	Floor float32
	// spec holds Spectrum's window, samples and transform, made as it
	// is first asked for.
	spec *spectrum
	// Tilt lifts each band by this many decibels for each octave it
	// lies above 40 Hz. Music has less in its higher pitches, so a
	// tilt of 3 to 4.5 shows them as tall as the bass; zero, the
	// default, shows each band as loud as it is.
	Tilt float32
}

// NewAnalyzer returns an analyzer of m's sound that splits it into
// the given number of bands of pitch, from 40 Hz to 16 kHz, each as wide as the next
// to the ear: as many bands in each octave.
func NewAnalyzer(m *Mixer, bands int) *Analyzer {
	const size = 4096
	a := &Analyzer{
		m:      m,
		size:   size,
		window: make([]float32, size),
		in:     make([]float32, size),
		fft:    make([]complex128, size),
		Floor:  -70,
	}
	for i := range a.window {
		// A Hann window, so a pitch between two bins stays in them.
		a.window[i] = float32(0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(size-1)))
	}
	a.bands = bands
	a.place(m.Rate())
	return a
}

// place works out the bins each band starts at, for sound at rate.
func (a *Analyzer) place(rate int) {
	a.rate = rate
	lo, hi := math.Log2(40), math.Log2(16000)
	binHz := float64(rate) / float64(a.size)
	a.edges = make([]int, a.bands+1)
	prev := 0
	for i := range a.edges {
		hz := math.Exp2(lo + (hi-lo)*float64(i)/float64(a.bands))
		b := max(1, int(math.Round(hz/binHz)))
		if i > 0 {
			// Each band takes at least one bin of its own.
			b = max(b, prev+1)
		}
		a.edges[i] = min(b, a.size/2)
		prev = a.edges[i]
	}
}

// Bands fills out, one value a band, with how loud each band is being
// heard, from 0, at Floor or quieter, to 1, as loud as a full-scale
// sine. It returns the level of the whole sound the same way.
func (a *Analyzer) Bands(out []float32) (level float32) {
	if r := a.m.Rate(); r != a.rate {
		a.place(r)
	}
	a.m.recent(a.in)
	var sum float64
	for i, s := range a.in {
		sum += float64(s) * float64(s)
		a.fft[i] = complex(float64(s*a.window[i]), 0)
	}
	fft(a.fft)
	// A full-scale sine through the Hann window peaks at size/4.
	ref := float64(a.size) / 4
	for b := range out {
		if b+1 >= len(a.edges) {
			out[b] = 0
			continue
		}
		peak := 0.0
		for k := a.edges[b]; k < max(a.edges[b+1], a.edges[b]+1) && k < a.size/2; k++ {
			peak = max(peak, cmplx.Abs(a.fft[k]))
		}
		amp := peak / ref
		if a.Tilt != 0 {
			octaves := math.Log2(float64(a.edges[b]+a.edges[b+1]) / 2 * float64(a.rate) / float64(a.size) / 40)
			amp *= math.Pow(10, float64(a.Tilt)*max(octaves, 0)/20)
		}
		out[b] = a.scale(amp)
	}
	rms := math.Sqrt(sum / float64(len(a.in)))
	// A full-scale sine has an RMS of 1/√2.
	return a.scale(rms * math.Sqrt2)
}

// spectrumSize is how many frames Spectrum looks at: 170 ms, so its
// bins lie 5.9 Hz apart, fine enough to tell 30 Hz from 40.
const spectrumSize = 8192

type spectrum struct {
	window, in []float32
	fft        []complex128
	mag        []float64
}

// Spectrum fills heard and before, where not nil, with how loud the
// sound is at each of freqs, in hertz, rising: in decibels, 0 for a
// full-scale sine. heard is the sound as it is heard, and before as it
// was before the voices' inserts, such as an [EQ]; with no insert
// playing they are the same. Each frequency stands for the stretch
// between it and its neighbours, and reads the loudest bin there.
func (a *Analyzer) Spectrum(freqs, heard, before []float32) {
	if a.spec == nil {
		sp := &spectrum{window: make([]float32, spectrumSize), in: make([]float32, spectrumSize),
			fft: make([]complex128, spectrumSize), mag: make([]float64, spectrumSize/2)}
		for i := range sp.window {
			sp.window[i] = float32(0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(spectrumSize-1)))
		}
		a.spec = sp
	}
	if heard != nil {
		a.spectrumOf(freqs, heard, false)
	}
	if before != nil {
		a.spectrumOf(freqs, before, true)
	}
}

func (a *Analyzer) spectrumOf(freqs, out []float32, before bool) {
	sp := a.spec
	a.m.recentOf(sp.in, before)
	for i, s := range sp.in {
		sp.fft[i] = complex(float64(s*sp.window[i]), 0)
	}
	fft(sp.fft)
	ref := float64(spectrumSize) / 4
	for k := range sp.mag {
		sp.mag[k] = cmplx.Abs(sp.fft[k]) / ref
	}
	binHz := float64(a.m.Rate()) / spectrumSize
	for i, f := range freqs {
		if i >= len(out) {
			break
		}
		lo, hi := float64(f), float64(f)
		if i > 0 {
			lo = math.Sqrt(float64(f) * float64(freqs[i-1]))
		}
		if i+1 < len(freqs) {
			hi = math.Sqrt(float64(f) * float64(freqs[i+1]))
		}
		k0, k1 := int(math.Ceil(lo/binHz)), int(math.Floor(hi/binHz))
		var amp float64
		if k1 < k0 {
			// Narrower than a bin: read between the two bins about f.
			x := float64(f) / binHz
			k := min(int(x), len(sp.mag)-2)
			t := x - float64(k)
			amp = sp.mag[k]*(1-t) + sp.mag[k+1]*t
		} else {
			for k := max(k0, 0); k <= k1 && k < len(sp.mag); k++ {
				amp = max(amp, sp.mag[k])
			}
		}
		out[i] = float32(20 * math.Log10(max(amp, 1e-9)))
	}
}

// scale maps an amplitude, 1 full scale, to 0 to 1 over the decibels
// from Floor to 0.
func (a *Analyzer) scale(amp float64) float32 {
	if amp <= 0 {
		return 0
	}
	db := 20 * math.Log10(amp)
	return float32(max(0, min(1, 1-db/float64(a.Floor))))
}

// Wave fills out with the last len(out) samples heard, mono, oldest
// first, for drawing the wave itself. It holds at most a little under
// a second.
func (a *Analyzer) Wave(out []float32) { a.m.recent(out[:min(len(out), historyFrames/2)]) }

// Heard appends to dst the frames heard since frame from, both
// channels, interleaved, and returns them with the count of frames
// heard so far, to pass as from next time: so a meter that reads each
// frame once, as a loudness meter must, reads every frame heard. It
// keeps up to a little under a second; a caller that looks less often
// misses the frames before that. The first call starts the mixer
// keeping the frames, so it returns none.
func (a *Analyzer) Heard(dst []float32, from int64) (frames []float32, now int64) {
	return a.heardOf(dst, from, false)
}

// HeardBefore is [Analyzer.Heard] of the frames as the voices' inserts
// found them: for meters that read a sound as it is, while what is heard
// of it is changed only to listen, as in mono.
func (a *Analyzer) HeardBefore(dst []float32, from int64) (frames []float32, now int64) {
	return a.heardOf(dst, from, true)
}

func (a *Analyzer) heardOf(dst []float32, from int64, before bool) (frames []float32, now int64) {
	m := a.m
	now = m.Heard()
	rings := m.rings.Load()
	ring := rings.stereo
	if before {
		ring = rings.dryStereo
	}
	if ring == nil {
		m.wantRings(false, !before, before)
		return dst, now
	}
	// Frames the mixer has written over since are left out: a little
	// under a second is kept, far behind what it writes.
	from = max(from, now-historyFrames/2, m.played.Load()-historyFrames, 0)
	for f := from; f < now; f++ {
		at := 2 * (f & (historyFrames - 1))
		dst = append(dst, floatFrom(ring[at].Load()), floatFrom(ring[at+1].Load()))
	}
	return dst, now
}

// recent fills out with the last len(out) frames heard, as mono.
func (m *Mixer) recent(out []float32) { m.recentOf(out, false) }

// recentOf fills out with the last len(out) frames heard, as mono: as
// they were before the voices' inserts, with before.
func (m *Mixer) recentOf(out []float32, before bool) {
	history := m.history
	if dry := m.rings.Load().dryHistory; before && dry != nil {
		history = dry
	}
	end := m.Heard()
	start := end - int64(len(out))
	played := m.played.Load()
	for i := range out {
		f := start + int64(i)
		if f < 0 || f < played-historyFrames {
			out[i] = 0
			continue
		}
		out[i] = floatFrom(history[f&(historyFrames-1)].Load())
	}
}

// fft transforms x in place; len(x) is a power of two.
func fft(x []complex128) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		w := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			wk := complex(1, 0)
			for k := range size / 2 {
				u, t := x[start+k], wk*x[start+k+size/2]
				x[start+k], x[start+k+size/2] = u+t, u-t
				wk *= w
			}
		}
	}
}
