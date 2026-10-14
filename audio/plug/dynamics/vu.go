package dynamics

import (
	"image/color"
	"math"
	"strconv"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// The VU meter's colours: its face, from top to bottom, its ink, the
// red of its scale over 0 VU, its needle, and the bezel round it.
var (
	VUFace    = theme.Color("dynamics.vu.face", color.NRGBA{R: 0xf4, G: 0xe9, B: 0xc8, A: 0xff})
	VUFaceLow = theme.Color("dynamics.vu.face.low", color.NRGBA{R: 0xe0, G: 0xcf, B: 0x9f, A: 0xff})
	VUInk     = theme.Color("dynamics.vu.ink", color.NRGBA{R: 0x24, G: 0x1f, B: 0x18, A: 0xff})
	VURed     = theme.Color("dynamics.vu.red", color.NRGBA{R: 0xc4, G: 0x30, B: 0x25, A: 0xff})
	VUNeedle  = theme.Color("dynamics.vu.needle", color.NRGBA{R: 0x16, G: 0x13, B: 0x10, A: 0xff})
	VUBezel   = theme.Color("dynamics.vu.bezel", color.NRGBA{R: 0x2b, G: 0x2b, B: 0x30, A: 0xff})
)

// vuSpring moves a VU meter's needle as the standard has it: across 99%
// of a step along the scale in 300 ms, overshooting by about 1.3%.
var vuSpring = anim.Spring{Response: 0.4681, Damping: 0.81}

// The scale, in VU: its ends, and its marks.
const vuLow, vuHigh = -20, 3

var vuMarks = []float32{-20, -10, -7, -5, -3, -2, -1, 0, 1, 2, 3}

// vuAt is where along the scale, from 0 at its left end to 1 at its
// right, a level in VU is: by voltage, as a VU meter's scale runs.
func vuAt(db float32) float32 {
	v := func(d float32) float64 { return math.Pow(10, float64(d)/20) }
	return float32((v(db) - v(vuLow)) / (v(vuHigh) - v(vuLow)))
}

// vuSweep is half the angle the needle sweeps, in radians.
const vuSweep = 50 * math.Pi / 180

// vu is a classic VU meter: a needle over a scale from -20 to +3 VU,
// on a cream face under glass, moving as a VU meter's moves.
type vu struct {
	anim.Group
	// name is printed on the face; read reads the level to show, and
	// whether the sound moves; needle is where along the scale the
	// needle is, as vuAt has it.
	name   string
	read   func(dt time.Duration) (db float32, live bool)
	needle *anim.Float
}

// vuRest is where the needle rests, a little under the scale's end,
// and vuPin where it stops at the other, a little over: a level past
// either holds it there, as a meter's pins do.
const vuRest, vuPin = vuLow - 2, vuHigh + 0.6

func newVU(name string, read func(time.Duration) (float32, bool)) *vu {
	m := &vu{name: name, read: read, needle: anim.NewFloat(vuAt(vuRest))}
	// The needle measures, so it swings in real time at any pace.
	m.needle.RealTime()
	m.Add(m.needle)
	return m
}

// Step implements [gunim.Animator]: the needle swings to the level.
func (m *vu) Step(dt time.Duration) bool {
	db, live := m.read(dt)
	m.needle.Animate(vuAt(max(vuRest, min(db, vuPin))), vuSpring)
	return m.Group.Step(dt) || live
}

// Layout implements [gunim.Node].
func (m *vu) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size { return c.Max }

// Paint implements [gunim.Node].
func (m *vu) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink, red := VUInk.Get(th), VURed.Get(th)
	outer := geom.Rect{Max: box.Point()}
	p.ShadowRRect(outer, 10, paint.Solid(VUBezel.Get(th)), paint.Shadow{Blur: 12, Color: audioui.Faded(color.NRGBA{A: 0xff}, 0.35)})
	face := outer.Inset(geom.Uniform(7))
	p.RRect(face, 5, paint.Fill{Gradient: &paint.Gradient{From: face.Min, To: geom.Pt(face.Min.X, face.Max.Y),
		Start: VUFace.Get(th), End: VUFaceLow.Get(th)}})
	// The needle's pivot sits below the face, as on a meter: the scale
	// is an arc of a circle about it.
	w, h := face.Size().W, face.Size().H
	pivot := geom.Pt(face.Min.X+w/2, face.Max.Y+h*0.18)
	radius := h * 0.92
	along := func(x, r float32) geom.Point {
		a := float64(-vuSweep + 2*vuSweep*x)
		return geom.Pt(pivot.X+r*float32(math.Sin(a)), pivot.Y-r*float32(math.Cos(a)))
	}
	at := func(db, r float32) geom.Point { return along(vuAt(db), r) }
	end := p.Layer(paint.LayerOpts{Bounds: face, Opacity: 1, Clip: true, Radius: 5})
	// The scale: its arc, red from 0 VU up, and its marks.
	arc := func(from, to float32, c color.NRGBA, width float32) {
		const steps = 40
		prev := at(from, radius)
		for i := 1; i <= steps; i++ {
			next := at(from+(to-from)*float32(i)/steps, radius)
			audioui.Segment(p, prev, next, width, c)
			prev = next
		}
	}
	arc(vuLow, 0, ink, 1.5)
	arc(0, vuHigh, red, 4)
	for _, mk := range vuMarks {
		c := ink
		if mk > 0 {
			c = red
		}
		long := mk == 0 || mk == -10 || mk == -20 || mk == vuHigh
		inner := radius - 7
		if long {
			inner = radius - 11
		}
		audioui.Segment(p, at(mk, inner), at(mk, radius), 1.5, c)
		label := audioui.Shaped(vuLabel(mk), max(8, h*0.085), long, false)
		pos := at(mk, radius+h*0.08)
		label.Paint(p, geom.Pt(pos.X-label.Advance/2, pos.Y-h*0.06), c)
	}
	// The name and VU, under the scale.
	vuText := audioui.Shaped("VU", max(12, h*0.16), true, false)
	vuText.Paint(p, geom.Pt(pivot.X-vuText.Advance/2, face.Min.Y+h*0.52), ink)
	name := audioui.Shaped(m.name, max(8, h*0.075), true, false)
	name.Paint(p, geom.Pt(pivot.X-name.Advance/2, face.Max.Y-h*0.13), audioui.Faded(ink, 0.7))
	// The needle, and the shadow it throws on the face.
	// The pins stop the needle where its spring would swing it past.
	tip := along(max(vuAt(vuRest), min(m.needle.Value(), vuAt(vuPin))), radius+3)
	audioui.Segment(p, geom.Pt(pivot.X+2, pivot.Y+2), geom.Pt(tip.X+2, tip.Y+3), 2, audioui.Faded(ink, 0.18))
	audioui.Segment(p, pivot, tip, 1.6, VUNeedle.Get(th))
	// Glass: a sheen over the top half.
	p.RRect(face, 5, paint.Fill{Gradient: &paint.Gradient{From: face.Min, To: geom.Pt(face.Min.X, face.Min.Y+h*0.55),
		Start: color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x30}, End: color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0}}})
	end()
}

// vuLabel writes a mark's level as a VU meter's scale does, without
// its sign: 20, 10, … 0, 1, 2, 3.
func vuLabel(db float32) string { return strconv.Itoa(int(math.Abs(float64(db)))) }
