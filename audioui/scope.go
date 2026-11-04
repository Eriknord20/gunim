package audioui

import (
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// ScopeFrames is how many of the last frames heard a [Scope] draws.
const ScopeFrames = 1200

// Scope shows a sound's stereo image: a vectorscope, mid up and side
// across, of the last frames heard, and the correlation of its
// channels, from -1, out of phase, to +1, mono.
type Scope struct {
	frames []float32
	// Corr is the correlation, eased.
	Corr float32
}

// NewScope returns a scope of silence.
func NewScope() *Scope { return &Scope{Corr: 1} }

// Write takes frames heard, interleaved stereo.
func (s *Scope) Write(frames []float32) {
	s.frames = append(s.frames, frames...)
	if over := len(s.frames) - 2*ScopeFrames; over > 0 {
		s.frames = s.frames[over:]
	}
}

// Step eases the correlation over dt toward that of the frames drawn.
func (s *Scope) Step(dt time.Duration) {
	var lr, ll, rr float64
	for i := 0; i+1 < len(s.frames); i += 2 {
		l, r := float64(s.frames[i]), float64(s.frames[i+1])
		lr += l * r
		ll += l * l
		rr += r * r
	}
	c := float32(1)
	if ll > 1e-9 && rr > 1e-9 {
		c = float32(lr / math.Sqrt(ll*rr))
	}
	s.Corr += (c - s.Corr) * min(1, 8*float32(dt.Seconds()))
}

// Paint draws the vectorscope as a disc filling area, a square: the
// frames as points, brighter the newer, over guides for mid, side and
// each channel alone.
func (s *Scope) Paint(p *paint.Painter, th *theme.Live, area geom.Rect) {
	ink, ground, sound := widget.Ink.Get(th), Ground.Get(th), Sound.Get(th)
	side := area.Size().W
	mid := area.Min.Add(geom.Pt(side/2, side/2))
	rad := side / 2
	p.RRect(area, rad, paint.Solid(paint.Faded(ground, 0.6)))
	p.RRect(geom.Rc(mid.X-0.5, area.Min.Y+8, 1, side-16), 0, paint.Solid(paint.Faded(ink, 0.1)))
	p.RRect(geom.Rc(area.Min.X+8, mid.Y-0.5, side-16, 1), 0, paint.Solid(paint.Faded(ink, 0.06)))
	d := rad * 0.68
	Segment(p, geom.Pt(mid.X-d, mid.Y-d), geom.Pt(mid.X+d, mid.Y+d), 1, paint.Faded(ink, 0.06))
	Segment(p, geom.Pt(mid.X+d, mid.Y-d), geom.Pt(mid.X-d, mid.Y+d), 1, paint.Faded(ink, 0.06))
	Shaped("L", 9, true, false).Paint(p, geom.Pt(mid.X-d-10, mid.Y-d-10), paint.Faded(ink, 0.3))
	Shaped("R", 9, true, false).Paint(p, geom.Pt(mid.X+d+4, mid.Y-d-10), paint.Faded(ink, 0.3))
	n := len(s.frames) / 2
	k := rad * 0.9 / math.Sqrt2
	for i := 0; i < n; i += 2 {
		l, r := s.frames[2*i], s.frames[2*i+1]
		x := mid.X + (r-l)*k
		y := mid.Y - (l+r)*k
		if math.Hypot(float64(x-mid.X), float64(y-mid.Y)) > float64(rad) {
			continue
		}
		a := 0.15 + 0.6*float32(i)/float32(n)
		p.RRect(geom.Rc(x-0.9, y-0.9, 1.8, 1.8), 0.9, paint.Solid(paint.Faded(sound, a)))
	}
}

// PaintCorrelation draws the correlation as a mark along bar, -1 at its
// left and +1 at its right, the two ends named either side of it.
func (s *Scope) PaintCorrelation(p *paint.Painter, th *theme.Live, bar geom.Rect) {
	ink := widget.Ink.Get(th)
	y := bar.Min.Y
	p.RRect(bar, bar.Size().H/2, paint.Solid(paint.Faded(ink, 0.08)))
	p.RRect(geom.Rc(bar.Min.X+bar.Size().W/2-0.5, y-3, 1, bar.Size().H+6), 0, paint.Solid(paint.Faded(ink, 0.3)))
	cx := bar.Min.X + bar.Size().W*(s.Corr+1)/2
	c := CorrColor(th, s.Corr)
	p.ShadowRRect(geom.Rc(cx-5, y-3, 10, bar.Size().H+6), 4, paint.Solid(c), paint.Shadow{Blur: 8, Color: paint.Faded(c, 0.5)})
	Shaped("-1", 9, false, true).Paint(p, geom.Pt(bar.Min.X-22, y-2), paint.Faded(ink, 0.4))
	Shaped("+1", 9, false, true).Paint(p, geom.Pt(bar.Max.X+6, y-2), paint.Faded(ink, 0.4))
}

// CorrColor colours a correlation: Sound as the channels agree, Near as
// they part, Over as they work against each other, which a mono
// listener loses.
func CorrColor(th *theme.Live, c float32) color.NRGBA {
	sound, near, over := Sound.Get(th), Near.Get(th), Over.Get(th)
	switch {
	case c >= 0.3:
		return sound
	case c >= 0:
		return anim.MixColor(near, sound, c/0.3)
	}
	return anim.MixColor(near, over, min(-c/0.5, 1))
}
