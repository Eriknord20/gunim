//go:build linux || windows || darwin

package render

import (
	"image/color"
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
)

// fadeScene paints a page and fading scrolls on it, as a test of the two
// ways a fading layer draws.
type fadeScene struct {
	// noFill leaves out the page's opaque fill, so the canvas is clear
	// under the scrolls.
	noFill bool
	// lists are the scrolls, painted in turn, each holding the next
	// where nested says so.
	lists  []fadeList
	nested bool
	// round, if set, clips everything to a rounded box drawn in place.
	round geom.Rect
	// at scrolls each list's rows.
	at float32
}

// fadeList is one scroll: its bounds and how far its edges fade.
type fadeList struct {
	bounds geom.Rect
	fade   geom.Insets
}

// rows paints a list's rows, reaching past the list on every side, scrolled
// by at.
func rows(p *paint.Painter, at float32, tint uint8) {
	defer p.Push(paint.Translate(geom.Pt(0, -at)))()
	p.RRect(geom.Rc(0, -100, 700, 1400), 0, paint.Solid(color.NRGBA{R: 0x30, G: 0x40, B: tint, A: 0xff}))
	for k := range 30 {
		y := float32(k) * 37.3
		c := color.NRGBA{R: uint8(40 * k), G: uint8(255 - 7*k), B: tint, A: 0xff}
		p.RRect(geom.Rc(30, 40+y, 480, 70+y), 8, paint.Solid(c))
		text.Default().Shape("Row of a list that scrolls", 15).Paint(p, geom.Pt(40, 50+y), black)
	}
}

func (s fadeScene) ops() []paint.Op {
	var p paint.Painter
	if !s.noFill {
		p.RRect(geom.Rect{Max: benchSize.Point()}, 0, paint.Solid(white))
	}
	if !s.round.Empty() {
		defer p.Layer(paint.LayerOpts{Bounds: s.round, Opacity: 1, Clip: true, Radius: 30})()
	}
	var paintList func(i int)
	paintList = func(i int) {
		if i == len(s.lists) {
			return
		}
		l := s.lists[i]
		end := p.Layer(paint.LayerOpts{Bounds: l.bounds, Opacity: 1, Clip: true, Fade: l.fade})
		rows(&p, s.at, uint8(60+60*i))
		if s.nested {
			paintList(i + 1)
		}
		end()
		if !s.nested {
			paintList(i + 1)
		}
	}
	paintList(0)
	return p.Ops()
}

// drawCanvas draws ops whole, at scale, and returns the canvas's pixels.
// fadedInPlace is how many layers the whole frame faded in place.
var fadedInPlace int

func drawCanvas(r *Renderer, ops []paint.Op, scale float32) []byte {
	w, h := int(benchSize.W*scale), int(benchSize.H*scale)
	r.canvasOK, r.direct, r.big = false, false, false
	r.Stats.FadesInPlace = 0
	r.Draw(ops, paint.Everything, w, h, scale)
	fadedInPlace = r.Stats.FadesInPlace
	// A whole frame after a whole frame goes straight to the window: the
	// second, of a pixel, leaves the canvas as the first drew it.
	r.Draw(ops, geom.Rc(0, 0, 0.5, 0.5), w, h, scale)
	return canvas(r)
}

// drawPartly draws first whole, then next in part, over damage, at scale,
// and returns the canvas's pixels.
func drawPartly(r *Renderer, first, next []paint.Op, damage geom.Rect, scale float32) []byte {
	w, h := int(benchSize.W*scale), int(benchSize.H*scale)
	drawCanvas(r, first, scale)
	r.Draw(next, damage, w, h, scale)
	return canvas(r)
}

// A layer that fades only toward its top and bottom draws in place, with
// what lay under its fading bands laid back over them, where that gives
// the picture the layer drawn whole gives: the two ways match to 1 in 255
// in every scene, and the scenes where the copy would go wrong draw the
// layer whole. inPlace is how many layers each scene draws in place.
func TestAFadeInPlaceDrawsAsTheLayerWhole(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	r.SetText(text.Rendering{Smoothing: text.Greyscale, Gamma: 1.8}, false)
	wasDebug := framesDebug
	framesDebug = true // for Stats.FadesInPlace
	defer func() { noFadeInPlace, framesDebug = false, wasDebug }()
	fade := geom.Insets{Top: 24, Bottom: 24}
	box := func(x0, y0, x1, y1 float32) geom.Rect { return geom.Rect{Min: geom.Pt(x0, y0), Max: geom.Pt(x1, y1)} }
	onPixels := box(20, 40, 500, 420)
	for _, c := range []struct {
		name    string
		scene   fadeScene
		scale   float32
		partial bool
		inPlace int
	}{
		{"both edges", fadeScene{lists: []fadeList{{onPixels, geom.Insets{Top: 17.3, Bottom: 23.9}}}, at: 13.7}, 1, false, 1},
		// At 1.75, 40 and 420 are on device pixels (70 and 735), and 20 and
		// 500 too (35 and 875).
		{"both edges at 1.75", fadeScene{lists: []fadeList{{onPixels, geom.Insets{Top: 17.3, Bottom: 23.9}}}, at: 101.35}, 1.75, false, 1},
		{"the bottom edge only", fadeScene{lists: []fadeList{{onPixels, geom.Insets{Bottom: 24}}}}, 1.25, false, 1},
		{"the top edge only", fadeScene{lists: []fadeList{{onPixels, geom.Insets{Top: 9.6}}}, at: 760.4}, 1, false, 1},
		{"two lists side by side", fadeScene{lists: []fadeList{{box(20, 40, 380, 420), fade}, {box(400, 60, 780, 580), fade}}, at: 31.2}, 1, false, 2},
		{"in part", fadeScene{lists: []fadeList{{onPixels, fade}}, at: 13.7}, 1, true, 1},
		// What the copy would get wrong, drawn whole.
		{"off whole pixels", fadeScene{lists: []fadeList{{box(20.3, 40.6, 500.2, 420.7), fade}}, at: 13.7}, 1, false, 0},
		// The review's case: an outer list at y 40 to 460, an inner one at
		// 30 to 230, both fading 24.
		{"nested", fadeScene{lists: []fadeList{{box(20, 40, 520, 460), fade}, {box(40, 30, 480, 230), fade}}, nested: true, at: 7.5}, 1, false, 1},
		{"on a clear canvas", fadeScene{noFill: true, lists: []fadeList{{onPixels, fade}}, at: 13.7}, 1, false, 0},
		// 40 pixels high, under the two fades' 48.
		{"bands that meet", fadeScene{lists: []fadeList{{box(20, 40, 500, 80), fade}}, at: 13.7}, 1, false, 0},
		{"in a rounded clip", fadeScene{lists: []fadeList{{onPixels, fade}}, round: onPixels, at: 13.7}, 1, false, 0},
	} {
		ops := c.scene.ops()
		draw := func() []byte {
			if !c.partial {
				return drawCanvas(r, ops, c.scale)
			}
			// The rows move on 3 pixels, and only a box across the top
			// band and the middle is drawn again.
			next := c.scene
			next.at += 3
			return drawPartly(r, ops, next.ops(), box(100, 20, 300, 120), c.scale)
		}
		noFadeInPlace = true
		whole := draw()
		noFadeInPlace = false
		inPlace := draw()
		if got := fadedInPlace; got != c.inPlace {
			t.Errorf("%s: %d layers faded in place in the whole frame, want %d", c.name, got, c.inPlace)
		}
		worst, off := 0, 0
		for i := range whole {
			d := int(whole[i]) - int(inPlace[i])
			if d < 0 {
				d = -d
			}
			worst = max(worst, d)
			if d > 1 {
				off++
			}
		}
		if off > 0 {
			t.Errorf("%s: %d channels differ by more than 1 of 255, at most by %d", c.name, off, worst)
		}
		t.Logf("%s: at most %d of 255 apart", c.name, worst)
	}
}
