package audioui

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// Loudness reads a sound's loudness as it is heard, as a mastering
// suite's loudness panel shows it: momentary and short-term, eased so
// they read steadily; integrated since it started; how far it has
// ranged, read every half second; and its true peak.
type Loudness struct {
	lm *audio.LoudnessMeter
	tp audio.TruePeakMeter
	// Moment and Short are the momentary and short-term loudness, in
	// LUFS, eased; -70 for silence.
	Moment, Short float32
	// Low and High are where the loudness has ranged, in LUFS, as
	// Ranged says it has.
	Low, High float32
	Ranged    bool
	rangedAt  time.Duration
	// blocks and shorts hold the powers of the 400 ms blocks and the three-second windows heard, by loudness, so
	// the integrated loudness and the range cost the same however long the sound has played. frames counts the
	// frames heard, quarter is 100 ms of them, and seenBlocks and seenShorts how many of each the histograms hold.
	// integrated is the integrated loudness, read as each block comes.
	blocks, shorts         *loudHist
	frames, quarter        int
	seenBlocks, seenShorts int
	integrated             float32
}

// NewLoudness returns a reading of a sound at rate, from silence.
func NewLoudness(rate int) *Loudness {
	l := &Loudness{Moment: -70, Short: -70}
	l.Reset(rate)
	return l
}

// Reset starts the reading over, as another sound starts.
func (l *Loudness) Reset(rate int) {
	l.lm, l.tp, l.Ranged = audio.NewLoudnessMeter(rate), audio.TruePeakMeter{}, false
	l.blocks, l.shorts = &loudHist{}, &loudHist{}
	l.frames, l.quarter, l.seenBlocks, l.seenShorts, l.integrated = 0, max(rate/10, 1), 0, 0, -70
}

// Write takes frames heard, interleaved stereo.
func (l *Loudness) Write(frames []float32) {
	l.lm.Write(frames)
	l.tp.Write(frames)
	// The meter makes a block each 100 ms from the fourth on, and a three-second window from the 30th: only then
	// are its powers read, and only the new ones kept.
	l.frames += len(frames) / 2
	quarters := l.frames / l.quarter
	if quarters-3 > l.seenBlocks {
		var bs []float64
		bs, l.seenBlocks = l.lm.BlocksSince(l.seenBlocks)
		for _, p := range bs {
			l.blocks.add(p)
		}
		l.integrated = -70
		if v, ok := l.blocks.integrated(); ok {
			l.integrated = float32(v)
		}
	}
	if quarters-29 > l.seenShorts {
		var ss []float64
		ss, l.seenShorts = l.lm.ShortTermsSince(l.seenShorts)
		for _, p := range ss {
			l.shorts.add(p)
		}
	}
}

// Step eases the readings over dt toward the sound's, or, while it is
// not playing, toward silence. It returns whether they still move.
func (l *Loudness) Step(dt time.Duration, playing bool) bool {
	sec := float32(dt.Seconds())
	ease := func(v *float32, to, rise, fall float32) {
		rate := rise
		if to < *v {
			rate = fall
		}
		*v += (to - *v) * min(1, rate*sec)
	}
	m, s := float32(-70), float32(-70)
	if playing {
		m = float32(max(l.lm.Momentary(), -70))
		s = float32(max(l.lm.ShortTerm(), -70))
	}
	ease(&l.Moment, m, 20, 4)
	ease(&l.Short, s, 10, 3)
	// The range changes slowly, and is read over all the sound heard.
	if l.rangedAt += dt; l.rangedAt > 500*time.Millisecond {
		l.rangedAt = 0
		if low, high, ok := l.shorts.span(); ok {
			l.Ranged, l.Low, l.High = true, float32(low), float32(high)
		}
	}
	return l.Moment > -69
}

// Integrated is the loudness since the reading started, in LUFS, -70
// before there is any.
func (l *Loudness) Integrated() float32 { return l.integrated }

// TruePeak is the highest true peak heard, 1 at full scale.
func (l *Loudness) TruePeak() float64 { return l.tp.Peak() }

// Paint draws the readings from the top of r, across it, against the
// target loudness: the short-term large, then bars of the momentary,
// short-term and integrated loudness and of the range, and the true
// peak. It returns where they end.
func (l *Loudness) Paint(p *paint.Painter, th *theme.Live, r geom.Rect, target float32) float32 {
	ink, over := widget.Ink.Get(th), Over.Get(th)
	x, y, right := r.Min.X, r.Min.Y, r.Max.X
	// In a narrow panel the readout shrinks to fit, its label goes where it has no room, the bars go under 78 px
	// and the figures beside them under 60.
	barX, barW := x+24, r.Size().W-78
	bars, figures := barW > 0, r.Size().W >= 60
	// fits paints run at at, where it ends inside the panel.
	fits := func(run text.Run, at geom.Point, c color.NRGBA) {
		if at.X >= x && at.X+run.Advance <= right {
			run.Paint(p, at, c)
		}
	}
	big := Shaped(LUFSText(l.Short), 34, true, true)
	if w := r.Size().W; big.Advance > w && w > 0 {
		big = Shaped(LUFSText(l.Short), float32(math.Floor(float64(34*w/big.Advance))), true, true)
	}
	c := ink
	if l.Short > -69 {
		c = LoudnessColor(th, l.Short-target)
	}
	fits(big, geom.Pt(x, y), c)
	fits(Shaped("LUFS short-term", 10, false, false), geom.Pt(x+4+big.Advance, y+22), paint.Faded(ink, 0.45))
	y += 50
	for _, row := range []struct {
		name string
		v    float32
	}{{"M", l.Moment}, {"S", l.Short}, {"I", l.Integrated()}} {
		if bars {
			PaintLoudnessBar(p, th, geom.Rc(barX, y, barW, 10), row.v, target)
		}
		fits(Shaped(row.name, 11, true, false), geom.Pt(x, y-2), paint.Faded(ink, 0.6))
		if figures {
			fits(Shaped(LUFSText(row.v), 11, false, true), geom.Pt(right-46, y-2), paint.Faded(ink, 0.8))
		}
		y += 22
	}
	fits(Shaped("LRA", 9, true, false), geom.Pt(x, y), paint.Faded(ink, 0.6))
	if bars {
		PaintRangeBar(p, th, geom.Rc(barX, y, barW, 10), l.Low, l.High, l.Ranged)
	}
	words := "—"
	if l.Ranged {
		words = fmt.Sprintf("%.1f LU", l.High-l.Low)
	}
	if figures {
		fits(Shaped(words, 11, false, true), geom.Pt(right-46, y-2), paint.Faded(ink, 0.8))
	}
	y += 22
	tp := float32(DB(l.TruePeak()))
	tpColor := paint.Faded(ink, 0.8)
	if tp > -1 {
		tpColor = over
	}
	fits(Shaped("TP", 11, true, false), geom.Pt(x, y), paint.Faded(ink, 0.6))
	tpWords := "—"
	if l.TruePeak() > 0 {
		tpWords = fmt.Sprintf("%.1f dBTP", tp)
	}
	fits(Shaped(tpWords, 11, false, true), geom.Pt(x+24, y), tpColor)
	return y + 18
}

// LUFSText writes a loudness, a dash for none.
func LUFSText(v float32) string {
	if v <= -69 {
		return "—"
	}
	return Readout("%.1f", float64(v))
}

// LoudnessColor colours a reading by how far off its target it is:
// Sound within half a unit, Near within one and a half, Over past
// three, and blends between.
func LoudnessColor(th *theme.Live, off float32) color.NRGBA {
	sound, near, over := Sound.Get(th), Near.Get(th), Over.Get(th)
	a := math.Abs(float64(off))
	switch {
	case a <= 0.5:
		return sound
	case a <= 1.5:
		return anim.MixColor(sound, near, float32((a-0.5)/1))
	case a <= 3:
		return anim.MixColor(near, over, float32((a-1.5)/1.5))
	}
	return over
}

// The scale of a loudness bar, in LUFS.
const barLow, barHigh = -36, 0

func barX(bar geom.Rect, l float32) float32 {
	return bar.Min.X + bar.Size().W*min(max((l-barLow)/(barHigh-barLow), 0), 1)
}

// PaintLoudnessBar draws loudness v as a bar from -36 to 0 LUFS,
// coloured by how far off target it is, the target marked.
func PaintLoudnessBar(p *paint.Painter, th *theme.Live, bar geom.Rect, v, target float32) {
	ink := widget.Ink.Get(th)
	p.RRect(bar, 5, paint.Solid(paint.Faded(ink, 0.08)))
	if v > barLow {
		fill := bar
		fill.Max.X = barX(bar, v)
		p.RRect(fill, 5, paint.Solid(LoudnessColor(th, v-target)))
	}
	tx := barX(bar, target)
	p.RRect(geom.Rc(tx-1, bar.Min.Y-3, 2, bar.Size().H+6), 1, paint.Solid(ink))
}

// PaintRangeBar draws a loudness range, from low to high LUFS, as a
// band on a loudness bar's scale, where ranged says there is one.
func PaintRangeBar(p *paint.Painter, th *theme.Live, bar geom.Rect, low, high float32, ranged bool) {
	p.RRect(bar, 5, paint.Solid(paint.Faded(widget.Ink.Get(th), 0.08)))
	if !ranged {
		return
	}
	spread := Spread.Get(th)
	x0, x1 := barX(bar, low), barX(bar, high)
	band := geom.Rc(x0, bar.Min.Y, max(x1-x0, 3), bar.Size().H)
	p.ShadowRRect(band, 5, paint.Solid(paint.Faded(spread, 0.85)), paint.Shadow{Blur: 8, Color: paint.Faded(spread, 0.4)})
}
