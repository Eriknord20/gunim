package audioui

import (
	"fmt"
	"math"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// Levels are a pair of channels' levels as a meter shows them, in
// decibels: each channel's peak, falling back at 12 dB a second; its
// RMS, over the last 300 ms or so; and its peak held a moment and a
// half. A meter's levels start at -90, silence.
type Levels struct {
	Peak, RMS, Hold [2]float32
	// Top is the highest peak of each channel since the levels started.
	Top  [2]float32
	held [2]time.Duration
	// ms is each channel's mean square over its window: over a frame's
	// sound alone, an RMS flickers.
	ms [2]float64
	// lm measures the loudness, from the first time it is asked for:
	// a meter that shows only levels never pays for it. rate is the
	// rate of the frames taken last.
	lm   *audio.LoudnessMeter
	rate int
}

// NewLevels returns levels of silence.
func NewLevels() *Levels {
	l := &Levels{}
	for ch := range 2 {
		l.Peak[ch], l.RMS[ch], l.Hold[ch], l.Top[ch] = -90, -90, -90, -90
	}
	return l
}

// Take takes the frames heard over dt, interleaved stereo at rate:
// their peaks rise at once, their RMS eases, and both fall back as the
// sound quietens.
func (l *Levels) Take(frames []float32, rate int, dt time.Duration) {
	if l.lm != nil && l.rate != rate {
		l.lm = audio.NewLoudnessMeter(rate)
	}
	l.rate = rate
	if l.lm != nil {
		l.lm.Write(frames)
	}
	sec := float32(dt.Seconds())
	for ch := range 2 {
		var peak, ss float32
		n := 0
		for i := ch; i < len(frames); i += 2 {
			v := frames[i]
			peak = max(peak, float32(math.Abs(float64(v))))
			ss += v * v
			n++
		}
		p := float32(-90)
		if n > 0 {
			p = float32(DB(float64(peak)))
		}
		l.Peak[ch] = max(p, l.Peak[ch]-12*sec)
		if n > 0 {
			k := 1 - math.Exp(-dt.Seconds()/0.3)
			l.ms[ch] += (float64(ss/float32(n)) - l.ms[ch]) * k
		}
		l.RMS[ch] = float32(DB(math.Sqrt(l.ms[ch])))
		l.Top[ch] = max(l.Top[ch], p)
		if p >= l.Hold[ch] {
			l.Hold[ch], l.held[ch] = p, 0
		} else if l.held[ch] += dt; l.held[ch] > 1500*time.Millisecond {
			l.Hold[ch] = max(l.Peak[ch], l.Hold[ch]-30*sec)
		}
	}
}

// Quiet lets the levels fall over dt, as nothing new is heard. The
// loudness keeps what it measured.
func (l *Levels) Quiet(dt time.Duration) {
	sec := float32(dt.Seconds())
	for ch := range 2 {
		l.Peak[ch] = max(-90, l.Peak[ch]-12*sec)
		l.ms[ch] *= math.Exp(-dt.Seconds() / 0.3)
		l.RMS[ch] = float32(DB(math.Sqrt(l.ms[ch])))
		if l.held[ch] += dt; l.held[ch] > 1500*time.Millisecond {
			l.Hold[ch] = max(l.Peak[ch], l.Hold[ch]-30*sec)
		}
	}
}

// ShortTerm returns the short-term loudness of the frames taken, in
// LUFS, and false before there is any. The loudness is measured from
// the first call on, of the frames taken after it: the K-weighting it
// takes costs more than the levels, so levels whose loudness no one
// reads skip it.
func (l *Levels) ShortTerm() (float64, bool) {
	if l.lm == nil {
		if l.rate > 0 {
			l.lm = audio.NewLoudnessMeter(l.rate)
		}
		return 0, false
	}
	s := l.lm.ShortTerm()
	return s, s > -70
}

// MeterAt is where on a meter, from 0 at the bottom to 1 at the top, a
// level in decibels is: from -60 up, the top of the scale given more
// room, as a mastering meter gives it.
func MeterAt(db float32) float32 {
	u := (max(db, -60) + 60) / 60
	return float32(math.Pow(float64(u), 1.8))
}

// MeterTicks are the levels a meter's scale is marked at.
var MeterTicks = []float32{0, -3, -6, -10, -15, -20, -30, -40, -60}

// MeterW is the width of a meter: two bars, BarW wide, BarPitch apart.
const (
	BarW     = 18
	BarPitch = 28
	MeterW   = BarPitch + BarW
)

// PaintMeter draws l as a pair of bars, left and right, from r.Min, as
// tall as r: each channel's RMS solid, its peak over it fainter, and
// its hold a line, coral at full scale. Over the bars, in the 36 points
// above r, go each channel's highest peak and the short-term loudness.
func PaintMeter(p *paint.Painter, th *theme.Live, r geom.Rect, l *Levels) {
	ink, ground, over := widget.Ink.Get(th), Ground.Get(th), Over.Get(th)
	top, bottom := r.Min.Y, r.Max.Y
	yOf := func(db float32) float32 { return bottom - (bottom-top)*MeterAt(db) }
	runs := topRuns(l.Top, "%.1f")
	if runs[0].Advance+runs[1].Advance+labelGap > MeterW {
		// Two long readouts side by side lose their tenths, to stay
		// apart.
		runs = topRuns(l.Top, "%.0f")
	}
	x0 := meterLabelX(r.Min.X, BarW/2, runs[0].Advance)
	x1 := meterLabelX(r.Min.X, BarPitch+BarW/2, runs[1].Advance)
	if x0+runs[0].Advance+labelGap > x1 {
		x0, x1 = r.Min.X, r.Min.X+MeterW-runs[1].Advance
	}
	for ch, x := range []float32{x0, x1} {
		c := Faded(ink, 0.7)
		if l.Top[ch] > -1 {
			c = over
		}
		runs[ch].Paint(p, geom.Pt(x, top-34), c)
	}
	loud := "—"
	if s, ok := l.ShortTerm(); ok {
		loud = fmt.Sprintf("%.1f", s)
	}
	run := Shaped(loud, 11, true, true)
	run.Paint(p, geom.Pt(meterLabelX(r.Min.X, MeterW/2, run.Advance), top-20), ink)
	for ch := range 2 {
		bx := r.Min.X + float32(ch)*BarPitch
		p.RRect(geom.Rc(bx, top, BarW, bottom-top), 2, paint.Solid(Faded(ground, 0.85)))
		ry, py := yOf(l.RMS[ch]), yOf(l.Peak[ch])
		if l.Peak[ch] > -59 {
			p.RRect(geom.Rc(bx, py, BarW, bottom-py), 2, paint.Solid(Faded(ink, 0.3)))
		}
		if l.RMS[ch] > -59 {
			p.RRect(geom.Rc(bx, ry, BarW, bottom-ry), 2, paint.Solid(Faded(Mix(ink, ground, 0.12), 0.9)))
		}
		if l.Hold[ch] > -59 {
			hc := ink
			if l.Hold[ch] > -0.1 {
				hc = over
			}
			p.RRect(geom.Rc(bx, yOf(l.Hold[ch])-1, BarW, 2), 1, paint.Solid(hc))
		}
	}
}

// labelGap is the least room between a meter's two peak readouts.
const labelGap = 3

// topRuns are the readouts of each channel's highest peak in top,
// written by format, or a dash for none.
func topRuns(top [2]float32, format string) [2]text.Run {
	var out [2]text.Run
	for ch := range 2 {
		words := "—"
		if top[ch] > -89 {
			words = fmt.Sprintf(format, top[ch])
		}
		out[ch] = Shaped(words, 8, true, true)
	}
	return out
}

// meterLabelX is where a label w wide starts, centred mid along a meter
// whose left edge is at x, and kept within the meter's width.
func meterLabelX(x, mid, w float32) float32 {
	return x + max(0, min(mid-w/2, MeterW-w))
}

// PaintMeterScale draws a meter's scale, its ticks' levels centred on x,
// from top to bottom.
func PaintMeterScale(p *paint.Painter, th *theme.Live, x, top, bottom float32) {
	ink := widget.Ink.Get(th)
	for _, t := range MeterTicks {
		label := fmt.Sprintf("%.0f", t)
		if t == -60 {
			label = "-inf"
		}
		run := Shaped(label, 9, false, true)
		run.Paint(p, geom.Pt(x-run.Advance/2, bottom-(bottom-top)*MeterAt(t)-6), Faded(ink, 0.4))
	}
}
