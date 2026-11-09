package audioui

import (
	"fmt"
	"image/color"
	"math"
	"math/cmplx"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// A spectrum's view: its levels tilted SpectrumTilt decibels an octave
// about 1 kHz, as studio analyzers show music, so that a balanced mix
// lies about level, between SpectrumTop and SpectrumBottom.
const (
	SpectrumTilt   = 4.5
	SpectrumTop    = 0
	SpectrumBottom = -84
)

// LogFreqs returns n frequencies from 20 Hz to 20 kHz, evenly by
// octaves, as a spectrum shows them.
func LogFreqs(n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(20 * math.Pow(1000, float64(i)/float64(max(n-1, 1))))
	}
	return out
}

// Spectrum is a sound's spectrum as drawn, from 20 Hz to 20 kHz: the
// sound out, filled, and, where given, the sound in, a line over it,
// so that where they part shows what was done to it. Its levels rise
// at once and fall slowly.
type Spectrum struct {
	// Freqs are the frequencies drawn.
	Freqs []float32
	// Out and In are the levels drawn at each, in decibels, tilted.
	Out, In []float32
	// ShowIn says the sound in is drawn, and HideOut that the sound out
	// is not.
	ShowIn, HideOut bool
	// Switches says the legend is drawn as switches of the two, each
	// lit while it is drawn, at [Spectrum.LegendRects], for a click to
	// turn it on and off.
	Switches bool
}

// NewSpectrum returns a spectrum of points frequencies, silent.
func NewSpectrum(points int) *Spectrum {
	s := &Spectrum{Freqs: LogFreqs(points), Out: make([]float32, points), In: make([]float32, points)}
	for i := range s.Out {
		s.Out[i], s.In[i] = SpectrumBottom, SpectrumBottom
	}
	return s
}

// Tilted is level db at frequency i, tilted as the spectrum draws it.
func (s *Spectrum) Tilted(i int, db float32) float32 {
	return db + float32(SpectrumTilt*math.Log2(float64(s.Freqs[i])/1000))
}

// Step eases the levels drawn over dt toward out and in, in decibels
// at each of Freqs, untilted; in may be nil. It returns whether the
// sound out is drawn where it is.
func (s *Spectrum) Step(dt time.Duration, out, in []float32) bool {
	sec := float32(dt.Seconds())
	ease := func(v *float32, to float32) {
		rate := float32(25)
		if to < *v {
			rate = 5
		}
		*v += (to - *v) * min(1, rate*sec)
	}
	settled := true
	for i := range s.Freqs {
		to := s.Tilted(i, out[i])
		ease(&s.Out[i], to)
		if in != nil {
			ease(&s.In[i], s.Tilted(i, in[i]))
		}
		settled = settled && math.Abs(float64(s.Out[i]-to)) < 0.5
	}
	return settled
}

// Bands fills bands with how loud the sound out is in as many bands of
// its frequencies, low to high, from 0 to 1, as for little bars moving
// with the music.
func (s *Spectrum) Bands(bands []float32) {
	n := len(s.Out)
	for k := range bands {
		// The lowest and highest frequencies left out: little moves
		// there.
		i0 := max(2, n*k/len(bands))
		i1 := min(n-1, n*(k+1)/len(bands))
		var v float32
		for i := i0; i < i1; i++ {
			v = max(v, (s.Out[i]-SpectrumBottom)/(SpectrumTop-SpectrumBottom))
		}
		bands[k] = min(max(v, 0), 1)
	}
}

// Paint draws the spectrum into area, the pitches marked, and, while
// the sound in is drawn or the legend switches them, which line is
// which at its top right.
func (s *Spectrum) Paint(p *paint.Painter, th *theme.Live, area geom.Rect) {
	ink, ground, sound := widget.Ink.Get(th), Ground.Get(th), Sound.Get(th)
	p.RRect(area, 10, paint.Solid(paint.Faded(ground, 0.6)))
	w := area.Size().W / float32(len(s.Out))
	at := func(i int, v float32) geom.Point {
		t := min(max((v-SpectrumBottom)/(SpectrumTop-SpectrumBottom), 0), 1)
		return geom.Pt(area.Min.X+float32(i)*w+w/2, area.Max.Y-area.Size().H*t)
	}
	var prev, prevIn geom.Point
	for i, v := range s.Out {
		pt := at(i, v)
		if !s.HideOut {
			p.RRect(geom.Rc(pt.X-w/2, pt.Y, w+0.5, area.Max.Y-pt.Y), 0, paint.Solid(paint.Faded(sound, 0.22)))
		}
		pin := at(i, s.In[i])
		if i > 0 {
			if !s.HideOut {
				Segment(p, prev, pt, 1.4, paint.Faded(sound, 0.8))
			}
			if s.ShowIn {
				Segment(p, prevIn, pin, 1.2, paint.Faded(ink, 0.5))
			}
		}
		prev, prevIn = pt, pin
	}
	PaintPitches(p, th, area, false)
	if !s.ShowIn && !s.Switches {
		return
	}
	out, in := s.LegendRects(area)
	for _, l := range []struct {
		name string
		r    geom.Rect
		c    color.NRGBA
		on   bool
	}{{"OUT", out, sound, !s.HideOut}, {"IN", in, paint.Faded(ink, 0.6), s.ShowIn}} {
		c := l.c
		if !l.on {
			c = paint.Faded(ink, 0.25)
		}
		if s.Switches {
			p.RRect(l.r, 9, paint.Solid(paint.Faded(ground, 0.75)))
			if l.on {
				p.RRectStroke(l.r, 9, paint.Solid(color.NRGBA{}), paint.Stroke{Width: 1, Color: paint.Faded(c, 0.6)})
			}
		}
		x := l.r.Min.X + 6
		p.RRect(geom.Rc(x, l.r.Min.Y+8, 10, 2), 1, paint.Solid(c))
		Shaped(l.name, 9, true, false).Paint(p, geom.Pt(x+14, l.r.Min.Y+3), c)
	}
}

// LegendRects are where the legend of a spectrum drawn into area names
// the sound out and the sound in, at its top right.
func (s *Spectrum) LegendRects(area geom.Rect) (out, in geom.Rect) {
	right := area.Max.X - 4
	rect := func(name string) geom.Rect {
		w := Shaped(name, 9, true, false).Advance + 26
		right -= w
		r := geom.Rc(right, area.Min.Y+3, w, 18)
		right -= 4
		return r
	}
	in = rect("IN")
	out = rect("OUT")
	return out, in
}

// PaintPitches marks 100 Hz, 1 kHz and 10 kHz across area, as a
// spectrum lays its frequencies out from 20 Hz to 20 kHz, or, up, as a
// spectrogram does, up it.
func PaintPitches(p *paint.Painter, th *theme.Live, area geom.Rect, up bool) {
	ink := widget.Ink.Get(th)
	for _, hz := range []float64{100, 1000, 10000} {
		at := float32(math.Log(hz/20) / math.Log(1000))
		label := fmt.Sprintf("%.0f", hz)
		if hz >= 1000 {
			label = fmt.Sprintf("%.0fk", hz/1000)
		}
		run := Shaped(label, 9, false, false)
		if up {
			y := area.Max.Y - area.Size().H*at
			p.RRect(geom.Rc(area.Min.X, y, area.Size().W, 1), 0, paint.Solid(paint.Faded(ink, 0.08)))
			if run.Advance+4 <= area.Size().W {
				run.Paint(p, geom.Pt(area.Min.X+4, y-12), paint.Faded(ink, 0.45))
			}
			continue
		}
		x := area.Min.X + area.Size().W*at
		p.RRect(geom.Rc(x, area.Min.Y, 1, area.Size().H), 0, paint.Solid(paint.Faded(ink, 0.06)))
		// The label goes right of its line, or left of it near the right edge, and not at all where neither fits.
		switch lx := x + 3; {
		case lx+run.Advance <= area.Max.X:
			run.Paint(p, geom.Pt(lx, area.Max.Y-14), paint.Faded(ink, 0.35))
		case x-3-run.Advance >= area.Min.X:
			run.Paint(p, geom.Pt(x-3-run.Advance, area.Max.Y-14), paint.Faded(ink, 0.35))
		}
	}
}

// PaintBars draws bands as little bars moving with the music, centred
// on mid, in c.
func PaintBars(p *paint.Painter, bands []float32, mid geom.Point, c color.NRGBA) {
	x0 := mid.X - float32(7*len(bands)-3)/2
	for i, v := range bands {
		h := 4 + 14*v
		p.RRect(geom.Rc(x0+float32(i)*7, mid.Y+9-h, 4, h), 2, paint.Solid(c))
	}
}

// Spectrometer takes the spectrum of a span of frames.
type Spectrometer struct {
	n      int
	fft    *audio.FFT
	bins   []complex128
	window []float64
}

// NewSpectrometer returns a spectrometer of n frames, a power of two.
func NewSpectrometer(n int) *Spectrometer {
	s := &Spectrometer{n: n, fft: audio.NewFFT(n), bins: make([]complex128, n), window: make([]float64, n)}
	for i := range s.window {
		s.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
	}
	return s
}

// Frames is how many frames the spectrometer takes.
func (s *Spectrometer) Frames() int { return s.n }

// Measure fills out with how loud the last Frames frames of frames,
// interleaved stereo at rate, are at each of freqs, in decibels, 0 for
// a full-scale sine: each frequency the loudest bin between it and its
// neighbours. It returns false, and leaves out as it was, where there
// are too few frames.
func (s *Spectrometer) Measure(frames []float32, rate int, freqs, out []float32) bool {
	n := s.n
	if rate <= 0 || len(frames) < 2*n {
		return false
	}
	frames = frames[len(frames)-2*n:]
	for i := range n {
		s.bins[i] = complex(float64(frames[2*i]+frames[2*i+1])/2*s.window[i], 0)
	}
	s.fft.Transform(s.bins)
	bin := func(hz float64) float64 { return hz * float64(n) / float64(rate) }
	for i, f := range freqs {
		lo, hi := float64(f), float64(f)
		if i > 0 {
			lo = math.Sqrt(float64(freqs[i-1]) * float64(f))
		}
		if i+1 < len(freqs) {
			hi = math.Sqrt(float64(freqs[i+1]) * float64(f))
		}
		b0 := max(1, min(int(bin(lo)), n/2-1))
		b1 := max(b0, min(int(math.Ceil(bin(hi))), n/2-1))
		var peak float64
		for k := b0; k <= b1; k++ {
			peak = max(peak, cmplx.Abs(s.bins[k]))
		}
		out[i] = float32(20 * math.Log10(max(peak*4/float64(n), 1e-9)))
	}
	return true
}
