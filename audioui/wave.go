package audioui

import (
	"image/color"
	"math"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// WaveBuckets is how many stretches a [Wave] keeps a file's loudness
// in, for an overview: some 20 ms each for a five-minute track.
const WaveBuckets = 16384

// WaveFinest is the frames of a stretch of a waveform's finest level:
// zoomed in past it, a view reads the samples themselves.
const WaveFinest = 128

// Wave is a file drawn as its loudness along it, made once as the file
// is read, by a [WaveScan], and never changed after, so it may be
// shared between goroutines.
type Wave struct {
	// Peak and RMS are each channel's peak and RMS, from 0 to 1, in
	// WaveBuckets stretches, for an overview.
	Peak, RMS [2][]float32
	// Frames is the file's length, and Rate its rate.
	Frames int64
	Rate   int
	// Gram is its spectrogram.
	Gram *Gram
	// Levels are the waveform for a view zoomed in: each stretch's
	// lowest and highest sample and its RMS, the finest first, of
	// WaveFinest frames a stretch, and each after four times coarser.
	Levels []WaveLevel
}

// WaveLevel is a waveform at one fineness: Per frames a stretch.
type WaveLevel struct {
	Per           int
	Min, Max, RMS [2][]float32
}

// WaveScan makes a file's [Wave] from its frames as they are read.
type WaveScan struct {
	w      *Wave
	counts []int
	per    float64
	fine   WaveLevel
	gram   *GramScan
	at     int64
}

// NewWaveScan returns a scan of a file frames long, at rate.
func NewWaveScan(frames int64, rate int) *WaveScan {
	s := &WaveScan{w: &Wave{Frames: frames, Rate: rate}, counts: make([]int, WaveBuckets),
		per: max(float64(frames)/WaveBuckets, 1), fine: WaveLevel{Per: WaveFinest}, gram: NewGramScan(rate)}
	nFine := int(max(1, (frames+WaveFinest-1)/WaveFinest))
	for ch := range 2 {
		s.w.Peak[ch], s.w.RMS[ch] = make([]float32, WaveBuckets), make([]float32, WaveBuckets)
		s.fine.Min[ch], s.fine.Max[ch], s.fine.RMS[ch] = make([]float32, nFine), make([]float32, nFine), make([]float32, nFine)
	}
	return s
}

// Write takes the file's next frames, interleaved stereo.
func (s *WaveScan) Write(frames []float32) {
	s.gram.Write(frames)
	w, nFine := s.w, len(s.fine.Min[0])
	for i := range len(frames) / 2 {
		f := s.at + int64(i)
		b := min(int(float64(f)/s.per), WaveBuckets-1)
		fb := min(int(f/WaveFinest), nFine-1)
		for ch := range 2 {
			v := frames[2*i+ch]
			s.fine.Min[ch][fb] = min(s.fine.Min[ch][fb], v)
			s.fine.Max[ch][fb] = max(s.fine.Max[ch][fb], v)
			s.fine.RMS[ch][fb] += v * v
			w.Peak[ch][b] = max(w.Peak[ch][b], float32(math.Abs(float64(v))))
			w.RMS[ch][b] += v * v
		}
		s.counts[b]++
	}
	s.at += int64(len(frames) / 2)
}

// Done returns the wave.
func (s *WaveScan) Done() *Wave {
	w := s.w
	for ch := range 2 {
		for b, c := range s.counts {
			if c > 0 {
				w.RMS[ch][b] = float32(math.Sqrt(float64(w.RMS[ch][b] / float32(c))))
			}
		}
		for b := range s.fine.RMS[ch] {
			k := min(int64(WaveFinest), w.Frames-int64(b)*WaveFinest)
			s.fine.RMS[ch][b] = float32(math.Sqrt(float64(s.fine.RMS[ch][b] / float32(max(k, 1)))))
		}
	}
	w.Levels = coarser(s.fine)
	w.Gram = s.gram.Done()
	return w
}

// coarser builds the coarser levels from the finest, four stretches to
// one, until a level is a few thousand stretches long.
func coarser(fine WaveLevel) []WaveLevel {
	out := []WaveLevel{fine}
	for prev := fine; len(prev.Min[0]) > 4096; {
		n := (len(prev.Min[0]) + 3) / 4
		l := WaveLevel{Per: prev.Per * 4}
		for ch := range 2 {
			l.Min[ch], l.Max[ch], l.RMS[ch] = make([]float32, n), make([]float32, n), make([]float32, n)
			for b := range n {
				lo, hi, ms, k := float32(0), float32(0), float32(0), 0
				for j := 4 * b; j < min(4*b+4, len(prev.Min[ch])); j++ {
					lo, hi = min(lo, prev.Min[ch][j]), max(hi, prev.Max[ch][j])
					ms += prev.RMS[ch][j] * prev.RMS[ch][j]
					k++
				}
				l.Min[ch][b], l.Max[ch][b], l.RMS[ch][b] = lo, hi, float32(math.Sqrt(float64(ms/float32(k))))
			}
		}
		out = append(out, l)
		prev = l
	}
	return out
}

// Thumbnail is the wave in n columns, each the loudest RMS of both
// channels across its stretch, from 0 to 1.
func (w *Wave) Thumbnail(n int) []float32 {
	out := make([]float32, n)
	per := float64(len(w.Peak[0])) / float64(n)
	for i := range out {
		for b := int(float64(i) * per); b < int(float64(i+1)*per) && b < len(w.Peak[0]); b++ {
			out[i] = max(out[i], w.RMS[0][b], w.RMS[1][b])
		}
	}
	return out
}

// Level returns the finest level no finer than a column of fpp frames,
// or nil for a wave with none.
func (w *Wave) Level(fpp float64) *WaveLevel {
	if len(w.Levels) == 0 {
		return nil
	}
	lv := &w.Levels[0]
	for i := range w.Levels {
		if float64(w.Levels[i].Per) <= fpp {
			lv = &w.Levels[i]
		}
	}
	return lv
}

// Column returns the lowest and highest sample of channel ch from frame
// f0 to f1, and their RMS.
func (l *WaveLevel) Column(ch int, f0, f1 int64) (lo, hi, rms float32) {
	if l == nil {
		return 0, 0, 0
	}
	n := int64(len(l.Min[ch]))
	b0 := min(f0/int64(l.Per), n-1)
	b1 := min(max(b0, (f1-1)/int64(l.Per)), n-1)
	for b := b0; b <= b1; b++ {
		lo, hi = min(lo, l.Min[ch][b]), max(hi, l.Max[ch][b])
		rms = max(rms, l.RMS[ch][b])
	}
	return lo, hi, rms
}

// Samples is a stretch of a file's samples, interleaved stereo, from
// frame From, for a view zoomed in past a wave's finest level.
type Samples struct {
	From int64
	Data []float32
}

// End is the frame after the last.
func (s *Samples) End() int64 { return s.From + int64(len(s.Data)/2) }

// Column returns the lowest and highest sample of channel ch from frame
// f0 to f1, and their RMS.
func (s *Samples) Column(ch int, f0, f1 int64) (lo, hi, rms float32) {
	f0, f1 = max(f0, s.From), min(f1, s.End())
	if f1 <= f0 {
		return 0, 0, 0
	}
	lo, hi = float32(math.Inf(1)), float32(math.Inf(-1))
	var ss float32
	for f := f0; f < f1; f++ {
		v := s.Data[2*(f-s.From)+int64(ch)]
		lo, hi = min(lo, v), max(hi, v)
		ss += v * v
	}
	lo, hi = min(lo, 0), max(hi, 0)
	return lo, hi, float32(math.Sqrt(float64(ss / float32(f1-f0))))
}

// WaveView is how a channel of a [Wave] is drawn: the file's time from
// V0 to V1 seconds across Width pixels.
type WaveView struct {
	V0, V1 float64
	Width  float32
	// Gain draws the wave louder, to see quiet sound; 0 is as 1.
	Gain float32
	// Shape is the gain the sound is heard at, at time t, and whether t
	// is heard at all: the sound as heard is drawn bright, over the
	// file faint. Nil draws the whole file as heard.
	Shape func(t float64) (gain float32, heard bool)
}

// NewWaveView returns a view of the file's time from v0 to v1 seconds across width pixels, the whole file heard.
func NewWaveView(v0, v1 float64, width float32) *WaveView {
	return &WaveView{V0: v0, V1: v1, Width: width}
}

// X is where time t is across the view.
func (v WaveView) X(t float64) float32 { return float32((t - v.V0) / (v.V1 - v.V0) * float64(v.Width)) }

// T is the time at x across the view.
func (v WaveView) T(x float32) float64 { return v.V0 + float64(x)/float64(v.Width)*(v.V1-v.V0) }

func (v WaveView) shape(t float64) (float32, bool) {
	if v.Shape == nil {
		return 1, true
	}
	return v.Shape(t)
}

// PaintChannel draws channel ch of w about the line at mid, half high
// either way, a column a pixel, from the finest level the view needs.
// Zoomed in past the finest level, it draws from raw where raw holds
// the samples in view, and, at fewer than two frames a pixel, the line
// the samples make, a dot on each once they stand apart.
//
// The columns keep to a grid in time, so each shows the same stretch
// of sound as the view scrolls by whole columns. Off it, a stretch's
// loudest moment falls now in one column, now in the next, and the
// waveform shimmers.
func (w *Wave) PaintChannel(p *paint.Painter, th *theme.Live, ch int, mid, half float32, v WaveView, raw *Samples) {
	ink, sound := widget.Ink.Get(th), Sound.Get(th)
	rate := float64(w.Rate)
	fpp := (v.V1 - v.V0) * rate / float64(max(v.Width, 1))
	useRaw := fpp < WaveFinest && raw != nil && len(raw.Data) > 0
	p.RRect(geom.Rc(0, mid, v.Width, 1), 0, paint.Solid(paint.Faded(ink, 0.06)))
	k := v.Gain
	if k == 0 {
		k = 1
	}
	clip := func(x float32) float32 { return max(-half, min(x*half*k, half)) }
	if useRaw && fpp < 2 {
		w.paintLine(p, ch, mid, clip, v, raw, ink, sound)
		return
	}
	lv := w.Level(fpp)
	per := (v.V1 - v.V0) / float64(v.Width)
	first := math.Floor(v.V0 / per)
	colT := func(x float32) float64 { return (first + float64(x)) * per }
	for x := float32(0); x < v.Width; x++ {
		f0, f1 := int64(math.Round(colT(x)*rate)), int64(math.Round(colT(x+1)*rate))
		if f1 <= 0 || f0 >= w.Frames {
			continue
		}
		f0, f1 = max(0, f0), min(w.Frames, max(f1, f0+1))
		var lo, hi, rms float32
		if useRaw {
			lo, hi, rms = raw.Column(ch, f0, f1)
		} else {
			lo, hi, rms = lv.Column(ch, f0, f1)
		}
		if hi <= lo {
			continue
		}
		y0, y1 := mid-clip(hi), mid-clip(lo)
		p.RRect(geom.Rc(x, y0, 1, max(1, y1-y0)), 0, paint.Solid(paint.Faded(ink, 0.12)))
		g, heard := v.shape((colT(x) + colT(x+1)) / 2)
		if !heard {
			continue
		}
		ey0, ey1 := mid-clip(hi*g), mid-clip(lo*g)
		r := clip(rms * g)
		p.RRect(geom.Rc(x, ey0, 1, max(1, ey1-ey0)), 0, paint.Solid(paint.Faded(sound, 0.55)))
		p.RRect(geom.Rc(x, mid-r, 1, 2*r), 0, paint.Solid(paint.Faded(anim.MixColor(sound, ink, 0.4), 0.85)))
	}
}

// paintLine draws channel ch's samples in view as the line they make:
// the file faint, the sound as heard bright.
func (w *Wave) paintLine(p *paint.Painter, ch int, mid float32, clip func(float32) float32, v WaveView,
	raw *Samples, ink, sound color.NRGBA) {
	rate := float64(w.Rate)
	f0 := max(raw.From, int64(v.V0*rate)-1)
	f1 := min(raw.End(), int64(v.V1*rate)+2)
	apart := float64(v.Width) / ((v.V1 - v.V0) * rate)
	var was, wasHeard geom.Point
	for f := f0; f < f1; f++ {
		t := float64(f) / rate
		s := raw.Data[2*(f-raw.From)+int64(ch)]
		x := v.X(t)
		pt := geom.Pt(x, mid-clip(s))
		g, heard := v.shape(t)
		if !heard {
			g = 0
		}
		hp := geom.Pt(x, mid-clip(s*g))
		if f > f0 {
			Segment(p, was, pt, 1, paint.Faded(ink, 0.25))
			Segment(p, wasHeard, hp, 1.5, sound)
		}
		if apart > 8 {
			p.RRect(geom.Rc(hp.X-2, hp.Y-2, 4, 4), 2, paint.Solid(sound))
		}
		was, wasHeard = pt, hp
	}
}
