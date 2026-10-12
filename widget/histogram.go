package widget

import (
	"image/color"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// The histogram's tokens.
var (
	HistogramFill   = theme.Color("histogram.fill", color.NRGBA{R: 0x12, G: 0x14, B: 0x19, A: 0xff})
	HistogramHeight = theme.Length("histogram.height", 64)
	// HistogramMotion carries the bars to new counts.
	HistogramMotion = theme.Spring("motion.histogram", anim.Spring{Response: 0.22, Damping: 1})
	// HistogramRed, HistogramGreen and HistogramBlue are the channels' colours, as light: where two overlap they
	// mix, and where all three do they are near white.
	HistogramRed   = theme.Color("histogram.red", color.NRGBA{R: 0xf0, G: 0x5a, B: 0x5a, A: 0xff})
	HistogramGreen = theme.Color("histogram.green", color.NRGBA{R: 0x5a, G: 0xdc, B: 0x78, A: 0xff})
	HistogramBlue  = theme.Color("histogram.blue", color.NRGBA{R: 0x5a, G: 0x8c, B: 0xf0, A: 0xff})
)

// Histogram shows how a picture's red, green and blue spread from dark
// to light, as three overlapping areas that mix where they overlap, as a
// photo editor shows it. The counts are scaled by their square root
// against the tallest count away from the very ends, so a clipped
// highlight or shadow does not flatten the rest. New counts morph in from
// the last.
type Histogram struct {
	anim.Group
	from, to [3][256]float32
	t        *anim.Float
	empty    bool
}

// NewHistogram returns an empty histogram.
func NewHistogram() *Histogram {
	h := &Histogram{t: anim.NewFloat(1), empty: true}
	h.Add(h.t)
	return h
}

// SetCounts shows counts of red, green and blue, 256 of each, from dark
// to light; the bars glide to them. Call it from a view's update or patch
// function.
func (h *Histogram) SetCounts(counts [3][256]uint32, u *gunim.UI) {
	var top uint32
	for c := range counts {
		for i := 1; i < 255; i++ {
			top = max(top, counts[c][i])
		}
	}
	var next [3][256]float32
	if top > 0 {
		for c := range counts {
			for i, n := range &counts[c] {
				next[c][i] = min(1, float32(math.Sqrt(float64(n)/float64(top))))
			}
		}
	}
	h.from = h.shown()
	if h.empty {
		// The first counts rise from the floor.
		h.from = [3][256]float32{}
		h.empty = false
	}
	h.to = next
	h.t.Jump(0)
	h.t.Animate(1, HistogramMotion.Get(u.Theme()))
	u.Invalidate()
}

// shown returns the bars' heights now, 0 to 1.
func (h *Histogram) shown() [3][256]float32 {
	t := h.t.Value()
	var out [3][256]float32
	for c := range out {
		for i := range out[c] {
			out[c][i] = h.from[c][i] + (h.to[c][i]-h.from[c][i])*t
		}
	}
	return out
}

// Layout implements [gunim.Node]: as wide as it is given, and the
// theme's [HistogramHeight] tall.
func (h *Histogram) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	w := c.Max.W
	if w <= 0 {
		w = FieldWidth.Get(f.Theme)
	}
	return c.Constrain(geom.Sz(w, HistogramHeight.Get(f.Theme)))
}

// Paint implements [gunim.Node]: a column a pixel wide per step across,
// each in up to three layers, from where all three channels reach, to
// where two do, to where one does.
func (h *Histogram) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	back := HistogramFill.Get(th)
	p.RRect(geom.Rect{Max: box.Point()}, 6, paint.Solid(back))
	if h.empty {
		return
	}
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: 1, Clip: true, Radius: 6})()
	bars := h.shown()
	cols := max(1, int(box.W))
	w := box.W / float32(cols)
	top := box.H - 4
	inks := [3]color.NRGBA{HistogramRed.Get(th), HistogramGreen.Get(th), HistogramBlue.Get(th)}
	for col := range cols {
		// The tallest count among the bins this column covers.
		lo, hi := col*256/cols, max(col*256/cols+1, (col+1)*256/cols)
		var v [3]float32
		for c := range v {
			for i := lo; i < hi && i < 256; i++ {
				v[c] = max(v[c], bars[c][i])
			}
		}
		x := float32(col) * w
		// From the floor up: where every channel still reaches, then the
		// ones that reach higher, mixed as light.
		order := [3]int{0, 1, 2}
		for i := range 3 {
			for j := i + 1; j < 3; j++ {
				if v[order[j]] < v[order[i]] {
					order[i], order[j] = order[j], order[i]
				}
			}
		}
		floor := float32(0)
		for k, c := range order {
			if v[c] <= floor {
				continue
			}
			var mix [3]bool
			for _, d := range order[k:] {
				mix[d] = true
			}
			y0, y1 := box.H-floor*top, box.H-v[c]*top
			p.RRect(geom.Rect{Min: geom.Pt(x, y1), Max: geom.Pt(x+w+0.5, y0)}, 0, paint.Solid(lightMix(back, inks, mix)))
			floor = v[c]
		}
	}
}

// lightMix is the channels in mix, coloured inks, added as light over back, each at a little over half strength, as
// screen blending gives them.
func lightMix(back color.NRGBA, inks [3]color.NRGBA, mix [3]bool) color.NRGBA {
	out := [3]float32{float32(back.R) / 255, float32(back.G) / 255, float32(back.B) / 255}
	for c, on := range mix {
		if !on {
			continue
		}
		ink := inks[c]
		in := [3]float32{float32(ink.R) / 255, float32(ink.G) / 255, float32(ink.B) / 255}
		for i := range out {
			screen := 1 - (1-out[i])*(1-in[i])
			out[i] += (screen - out[i]) * 0.6
		}
	}
	return color.NRGBA{R: uint8(out[0]*255 + 0.5), G: uint8(out[1]*255 + 0.5), B: uint8(out[2]*255 + 0.5), A: 0xff}
}
