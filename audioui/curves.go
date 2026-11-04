package audioui

import (
	"fmt"
	"image/color"
	"math"
	"sort"
	"strconv"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The loudness curves a view of a sound may draw over it, as bits of a
// set.
const (
	// CurveM is the momentary loudness, over 400 ms.
	CurveM uint8 = 1 << iota
	// CurveS is the short-term loudness, over three seconds.
	CurveS
	// CurveI is the integrated loudness, as it grows from the start,
	// and of the whole sound.
	CurveI
	// CurveLRA is the loudness range, as a band.
	CurveLRA
)

// CurveNames name the curves, in their bits' order.
var CurveNames = []struct {
	Bit  uint8
	Name string
}{{CurveM, "M"}, {CurveS, "S"}, {CurveI, "I"}, {CurveLRA, "LRA"}}

// The loudness's scale, in LUFS, over a view's height: CurveTop at the
// top, CurveBottom at the bottom.
const (
	CurveTop    = 0
	CurveBottom = -30
)

// Curves are a sound's loudness along it, as measured through.
type Curves struct {
	// Blocks are the mean weighted powers of its 400 ms blocks, 100 ms
	// apart, and Shorts of its three-second windows, as an
	// [audio.LoudnessMeter] gives them.
	Blocks, Shorts []float64
	// Running is its integrated loudness from the start to each second,
	// as [RunningLoudness] makes it.
	Running []float32
	// LUFS is its integrated loudness, where Loud says it has one.
	LUFS float32
	Loud bool
	// Low and High are where its loudness ranged, where Ranged says it
	// did, and LRA how far.
	Low, High, LRA float32
	Ranged         bool
	// read holds the loudness of Blocks and of Shorts, read as they
	// change: Paint draws a frame from them without reading every
	// block's again.
	read [2]loudnesses
}

// loudnesses is the loudness of each of a slice of powers, kept while
// the slice is the same one, and read on for powers appended to it.
type loudnesses struct {
	from, ls []float64
}

// of returns the loudness of each of powers, read only where powers
// are new: another slice, or the same one grown.
func (o *loudnesses) of(powers []float64) []float64 {
	if len(powers) < len(o.ls) || len(o.ls) > 0 && &powers[0] != &o.from[0] {
		o.ls = o.ls[:0]
	}
	for _, p := range powers[len(o.ls):] {
		o.ls = append(o.ls, LUFSOf(p))
	}
	o.from = powers
	return o.ls
}

// CurveView is where and how [Curves] are drawn.
type CurveView struct {
	// Area is what the curves are drawn over, its height the scale.
	Area geom.Rect
	// X is where the sound's time t, in seconds, is across.
	X func(t float64) float32
	// Fade is how far each curve is drawn, in CurveNames' order, from
	// 0, gone, to 1.
	Fade [4]float32
	// Alpha is how strongly all are drawn: faint where the sound has
	// changed since it was measured.
	Alpha float32
	// Target is the loudness aimed for, marked across.
	Target float32
	// Right is where the scale's labels end, at the right.
	Right float32
}

// NewCurveView returns a view of the curves over area, time placed across by x, every curve shown in full, and the
// scale's labels ending at area's right edge.
func NewCurveView(area geom.Rect, x func(t float64) float32) *CurveView {
	return &CurveView{Area: area, X: x, Fade: [4]float32{1, 1, 1, 1}, Alpha: 1, Right: area.Max.X}
}

// CurveColor is the colour of a curve, at alpha a: the momentary
// loudness faint ink, the short-term Short, the integrated Near, and
// the range Spread.
func CurveColor(th *theme.Live, bit uint8, a float32) color.NRGBA {
	switch bit {
	case CurveM:
		return paint.Faded(widget.Ink.Get(th), 0.7*a)
	case CurveS:
		return paint.Faded(Short.Get(th), a)
	case CurveI:
		return paint.Faded(Near.Get(th), a)
	}
	return paint.Faded(Spread.Get(th), a)
}

// Paint draws the curves as v says: the momentary and short-term
// loudness as lines, the integrated as it grows and as a line across,
// and the range as a band; with the target and the scale, while a
// line is drawn.
func (c *Curves) Paint(p *paint.Painter, th *theme.Live, v CurveView) {
	var on uint8
	for i, n := range CurveNames {
		v.Fade[i] = min(max(v.Fade[i], 0), 1)
		if v.Fade[i] > 0.01 {
			on |= n.Bit
		}
	}
	if on == 0 {
		return
	}
	area, w := v.Area, v.Area.Size().W
	yOf := func(l float64) float32 {
		u := (l - CurveBottom) / (CurveTop - CurveBottom)
		return area.Max.Y - area.Size().H*float32(max(0, min(u, 1)))
	}
	end := p.Layer(paint.LayerOpts{Bounds: area, Opacity: 1, Clip: true})
	defer end()
	ink, ground := widget.Ink.Get(th), Ground.Get(th)
	alpha := v.Alpha * v.Fade[3]
	if on&CurveLRA != 0 && c.Ranged {
		y0, y1 := yOf(float64(c.High)), yOf(float64(c.Low))
		p.RRect(geom.Rc(area.Min.X, y0, w, y1-y0), 0, paint.Solid(CurveColor(th, CurveLRA, 0.12*alpha)))
		p.RRect(geom.Rc(area.Min.X, y0, w, 1), 0, paint.Solid(CurveColor(th, CurveLRA, 0.5*alpha)))
		p.RRect(geom.Rc(area.Min.X, y1, w, 1), 0, paint.Solid(CurveColor(th, CurveLRA, 0.5*alpha)))
		Shaped(fmt.Sprintf("LRA %.1f", c.LRA), 10, true, true).Paint(p, geom.Pt(area.Min.X+40, y1+3),
			CurveColor(th, CurveLRA, alpha))
	}
	// line draws a curve of loudness, each point step seconds after the
	// last, the first at lead, over a dark edge so it reads on any
	// ground. The edge and the line are each drawn whole, solid, and
	// faded as one, so where two pieces meet neither shows darker, nor
	// does a piece of the edge cross the line. Only the points in view
	// and one past each side are visited, found by binary search: X
	// runs left to right.
	var segs [][2]geom.Point
	line := func(bit uint8, n int, loudness func(k int) float64, lead, step float64, width float32) {
		segs = segs[:0]
		var prev geom.Point
		was := false
		lastX := float32(-1e9)
		xOf := func(k int) float32 { return v.X(lead + float64(k)*step) }
		first := max(sort.Search(n, func(k int) bool { return xOf(k) >= area.Min.X-20 })-1, 0)
		last := min(sort.Search(n, func(k int) bool { return xOf(k) > area.Max.X+20 })+1, n)
		for k := first; k < last; k++ {
			l := loudness(k)
			x := xOf(k)
			if x < area.Min.X-20 || x > area.Max.X+20 {
				was = false
				continue
			}
			if x-lastX < 1 && k+1 < n {
				continue
			}
			if l < CurveBottom {
				was = false
				continue
			}
			pt := geom.Pt(x, yOf(l))
			if was {
				segs = append(segs, [2]geom.Point{prev, pt})
			}
			prev, was, lastX = pt, true, x
		}
		if len(segs) == 0 {
			return
		}
		whole := func(c color.NRGBA, width float32) {
			end := p.Layer(paint.LayerOpts{Bounds: area, Opacity: float32(c.A) / 255})
			c.A = 255
			for _, s := range segs {
				Segment(p, s[0], s[1], width, c)
			}
			end()
		}
		whole(paint.Faded(ground, 0.55*alpha), width+2)
		whole(CurveColor(th, bit, alpha), width)
	}
	if on&CurveM != 0 {
		alpha = v.Alpha * v.Fade[0]
		ms := c.read[0].of(c.Blocks)
		line(CurveM, len(ms), func(k int) float64 { return ms[k] }, 0.4, 0.1, 1)
	}
	if on&CurveS != 0 {
		alpha = v.Alpha * v.Fade[1]
		ss := c.read[1].of(c.Shorts)
		line(CurveS, len(ss), func(k int) float64 { return ss[k] }, 3, 0.1, 2)
	}
	alpha = v.Alpha * v.Fade[2]
	if on&CurveI != 0 && c.Loud {
		// The integrated loudness as it grows, from the start to each
		// second: a passage that lifts it shows as a rise.
		line(CurveI, len(c.Running), func(k int) float64 { return float64(c.Running[k]) }, 1, 1, 2)
		y := yOf(float64(c.LUFS))
		for x := area.Min.X; x < area.Max.X; x += 10 {
			p.RRect(geom.Rc(x, y-0.75, 6, 1.5), 0.75, paint.Solid(CurveColor(th, CurveI, alpha)))
		}
		run := Shaped(fmt.Sprintf("I %.1f", c.LUFS), 10, true, true)
		run.Paint(p, geom.Pt(v.Right-run.Advance-8, y-14), CurveColor(th, CurveI, alpha))
	}
	if on&(CurveS|CurveM|CurveI) != 0 {
		alpha = v.Alpha * max(v.Fade[0], v.Fade[1], v.Fade[2])
		y := yOf(float64(v.Target))
		p.RRect(geom.Rc(area.Min.X, y, w, 1), 0, paint.Solid(paint.Faded(ink, 0.18*alpha)))
		for l := -5; l >= -25; l -= 5 {
			y := yOf(float64(l))
			run := Shaped(strconv.Itoa(l), 9, false, true)
			x := v.Right - run.Advance - 6
			p.RRect(geom.Rc(x-4, y, run.Advance+8, 1), 0, paint.Solid(paint.Faded(ink, 0.25)))
			run.Paint(p, geom.Pt(x, y-11), paint.Faded(ink, 0.45))
		}
	}
}

// Loudnesses are mean weighted powers' loudnesses, in LUFS.
func Loudnesses(powers []float64) []float64 {
	out := make([]float64, len(powers))
	for i, pw := range powers {
		out[i] = LUFSOf(pw)
	}
	return out
}

// LUFSOf is the loudness of a mean weighted power, -inf for none.
func LUFSOf(power float64) float64 {
	if power <= 0 {
		return math.Inf(-1)
	}
	return -0.691 + 10*math.Log10(power)
}

// RunningLoudness is the integrated loudness of the 400 ms blocks'
// powers, 100 ms apart, from the first to each second's, -inf before
// there is any.
//
// The blocks go into a histogram one by one, so each second costs a
// pass over its bins, however long the sound.
func RunningLoudness(blocks []float64) []float32 {
	out := make([]float32, 0, len(blocks)/10+1)
	h := &loudHist{}
	for k := 10; k <= len(blocks)+9; k += 10 {
		for _, p := range blocks[k-10 : min(k, len(blocks))] {
			h.add(p)
		}
		l, ok := h.integrated()
		if !ok {
			l = math.Inf(-1)
		}
		out = append(out, float32(l))
	}
	return out
}

// LegendRects lays out a legend of the curves' switches, in
// CurveNames' order, along y, ending at right.
func LegendRects(right, y float32) []geom.Rect {
	out := make([]geom.Rect, len(CurveNames))
	x := right
	for i := len(CurveNames) - 1; i >= 0; i-- {
		w := Shaped(CurveNames[i].Name, 10, true, false).Advance + 26
		x -= w
		out[i] = geom.Rc(x, y, w, 20)
		x -= 4
	}
	return out
}

// PaintLegend draws the curves' switches in rects, as [LegendRects]
// lays them out: each a dot of its colour and its name, lit while on
// has its bit.
func PaintLegend(p *paint.Painter, th *theme.Live, rects []geom.Rect, on uint8) {
	ink, ground := widget.Ink.Get(th), Ground.Get(th)
	for i, r := range rects {
		c := CurveNames[i]
		lit := on&c.Bit != 0
		p.RRect(r, 10, paint.Solid(paint.Faded(ground, 0.75)))
		dot := CurveColor(th, c.Bit, 0.35)
		if lit {
			dot = CurveColor(th, c.Bit, 1)
			p.RRectStroke(r, 10, paint.Solid(color.NRGBA{}), paint.Stroke{Width: 1, Color: CurveColor(th, c.Bit, 0.6)})
		}
		p.RRect(geom.Rc(r.Min.X+8, r.Min.Y+7, 6, 6), 3, paint.Solid(dot))
		words := paint.Faded(ink, 0.45)
		if lit {
			words = ink
		}
		Shaped(c.Name, 10, true, false).Paint(p, geom.Pt(r.Min.X+18, r.Min.Y+4), words)
	}
}
