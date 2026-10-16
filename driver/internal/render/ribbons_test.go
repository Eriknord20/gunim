//go:build linux || windows || darwin

package render

import (
	"image/color"
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// line returns a ribbon of n points from a to b, of width w all along,
// in c.
func line(a, b geom.Point, n int, w float32, c color.NRGBA) []paint.RibbonPoint {
	pts := make([]paint.RibbonPoint, n)
	for i := range pts {
		t := float32(i) / float32(n-1)
		pts[i] = paint.RibbonPoint{At: geom.Pt(a.X+(b.X-a.X)*t, a.Y+(b.Y-a.Y)*t), Width: w, Color: c}
	}
	return pts
}

func TestARibbonCoversItsWidthAndNoMore(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	pix := drawn(r, func(p *paint.Painter) {
		p.Ribbon(line(geom.Pt(100, 100), geom.Pt(300, 100), 20, 10, white))
	})
	for _, c := range []struct {
		x, y int
		want [4]byte
		what string
	}{
		{200, 95, px(white), "its top row"},
		{200, 104, px(white), "its bottom row"},
		{200, 94, px(black), "the row above it"},
		{200, 105, px(black), "the row below it"},
		{100, 100, px(white), "its first column"},
		{299, 100, px(white), "its last column"},
		{99, 100, px(black), "the column before it"},
		{300, 100, px(black), "the column after it"},
	} {
		if got := pixelAt(pix, c.x, c.y); !near(got, c.want, 2) {
			t.Errorf("%s, at %d,%d, is %v, want %v", c.what, c.x, c.y, got, c.want)
		}
	}
}

func TestARibbonsEdgesAreSmooth(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	// Its edges lie halfway across a row of pixels.
	pix := drawn(r, func(p *paint.Painter) {
		p.Ribbon(line(geom.Pt(100, 100.5), geom.Pt(300, 100.5), 2, 10, white))
	})
	half := [4]byte{0x80, 0x80, 0x80, 0xff}
	if got := pixelAt(pix, 200, 95); !near(got, half, 3) {
		t.Errorf("the pixel its top edge halves is %v, want half lit", got)
	}
	if got := pixelAt(pix, 200, 105); !near(got, half, 3) {
		t.Errorf("the pixel its bottom edge halves is %v, want half lit", got)
	}
}

func TestARibbonsColourAndWidthChangeAlongIt(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	pix := drawn(r, func(p *paint.Painter) {
		p.Ribbon([]paint.RibbonPoint{
			{At: geom.Pt(100, 100), Width: 40, Color: red},
			{At: geom.Pt(300, 100), Width: 0, Color: blue},
		})
	})
	// Halfway along, at x 200.5 where the pixel's centre is.
	if got := pixelAt(pix, 200, 100); !near(got, [4]byte{0x7f, 0, 0x80, 0xff}, 3) {
		t.Errorf("halfway along it is %v, want halfway from red to blue", got)
	}
	// Halfway along it is 20 wide, and a quarter of the way 30.
	if got := pixelAt(pix, 200, 91); got[0] < 0x70 {
		t.Errorf("9 above its middle, halfway along, is %v, want it covered", got)
	}
	if got := pixelAt(pix, 200, 112); got != px(black) {
		t.Errorf("12 below its middle, halfway along, is %v, want it past the edge", got)
	}
	if got := pixelAt(pix, 150, 112); got[0] < 0x80 {
		t.Errorf("12 below its middle, a quarter of the way, is %v, want it covered", got)
	}
	if got := pixelAt(pix, 299, 102); got != px(black) {
		t.Errorf("by its pinched end it is %v, want nothing", got)
	}
}

func TestAThinRibbonFadesRatherThanBreakingUp(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	// A quarter of a pixel wide, across rows of pixels at a slant.
	pix := drawn(r, func(p *paint.Painter) {
		p.Ribbon(line(geom.Pt(100, 100), geom.Pt(300, 140), 50, 0.25, white))
	})
	for x := 120; x < 280; x += 7 {
		sum := 0
		for y := 90; y < 150; y++ {
			sum += int(pixelAt(pix, x, y)[0])
		}
		// A quarter of a pixel, slanting a fifth, covers about 0.255 of
		// the column.
		if sum < 50 || sum > 80 {
			t.Errorf("column %d holds %d of light, want about 65, a quarter of a pixel's", x, sum)
		}
	}
}

func TestARibbonBendsWithoutAGap(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	pix := drawn(r, func(p *paint.Painter) {
		p.Ribbon([]paint.RibbonPoint{
			{At: geom.Pt(100, 100), Width: 10, Color: white},
			{At: geom.Pt(200, 100), Width: 10, Color: white},
			{At: geom.Pt(200, 200), Width: 10, Color: white},
		})
	})
	// The outside corner of the bend, filled by the miter.
	if got := pixelAt(pix, 203, 97); !near(got, px(white), 2) {
		t.Errorf("the bend's outside corner is %v, want it filled", got)
	}
	if got := pixelAt(pix, 200, 150); !near(got, px(white), 2) {
		t.Errorf("the second stretch is %v, want it drawn", got)
	}
}

func TestARibbonKeepsToItsClipsTransformAndLayer(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	pts := line(geom.Pt(0, 50), geom.Pt(400, 50), 30, 10, white)
	// high runs near the top of a layer, through its corners.
	high := line(geom.Pt(0, 10), geom.Pt(400, 10), 30, 10, white)
	pix := drawn(r, func(p *paint.Painter) {
		// Cut to an upright rectangle, in place.
		end := p.Layer(paint.LayerOpts{Bounds: geom.Rc(100, 0, 100, 200), Opacity: 1, Clip: true})
		p.Ribbon(pts)
		end()
		// Moved down and cut to a rounded rectangle, offscreen.
		pop := p.Push(paint.Translate(geom.Pt(0, 100)))
		end = p.Layer(paint.LayerOpts{Bounds: geom.Rc(100, 0, 100, 100), Opacity: 1, Clip: true, Radius: 50})
		p.Ribbon(high)
		end()
		pop()
		// Cut to an ellipse, in place.
		pop = p.Push(paint.Translate(geom.Pt(0, 200)))
		end = p.Layer(paint.LayerOpts{Bounds: geom.Rc(100, 0, 100, 100), Opacity: 1, Clip: true, Ellipse: true})
		p.Ribbon(high)
		end()
		pop()
		// At half opacity, scaled to twice the size about its middle.
		pop = p.Push(paint.Scale(2, geom.Pt(200, 350)))
		end = p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, 300, 400, 100), Opacity: 0.5})
		pop2 := p.Push(paint.Translate(geom.Pt(0, 300)))
		p.Ribbon(pts)
		pop2()
		end()
		pop()
	})
	for _, c := range []struct {
		x, y int
		want [4]byte
		what string
	}{
		{150, 50, px(white), "inside the rectangle"},
		{99, 50, px(black), "left of the rectangle"},
		{200, 50, px(black), "right of the rectangle"},
		{150, 110, px(white), "inside the rounded rectangle"},
		{105, 110, px(black), "in the rounded rectangle's cut corner"},
		{150, 210, px(white), "inside the ellipse"},
		{105, 210, px(black), "outside the ellipse"},
		{200, 341, [4]byte{0x80, 0x80, 0x80, 0xff}, "the ribbon at half opacity, twice as wide"},
		{200, 339, px(black), "past the ribbon twice as wide"},
	} {
		if got := pixelAt(pix, c.x, c.y); !near(got, c.want, 3) {
			t.Errorf("%s, at %d,%d, is %v, want %v", c.what, c.x, c.y, got, c.want)
		}
	}
}

func TestARibbonAddsItsLightUnderBlendAdd(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	pix := drawn(r, func(p *paint.Painter) {
		p.RRect(geom.Rc(100, 80, 200, 40), 0, paint.Solid(ember))
		defer p.Blend(paint.BlendAdd)()
		p.Ribbon(line(geom.Pt(100, 100), geom.Pt(300, 100), 2, 10, ember))
	})
	if got := pixelAt(pix, 200, 100); !near(got, [4]byte{0xc0, 0x60, 0x20, 0xff}, 2) {
		t.Errorf("the added ribbon over its own colour is %v, want it twice as bright", got)
	}
}

func TestARibbonDrawsInTheBatchOfTheShapesAroundIt(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	pts := line(geom.Pt(100, 100), geom.Pt(300, 200), 100, 12, blue)
	frame := func(ribbon bool) []paint.Op {
		var p paint.Painter
		p.RRect(geom.Rect{Max: benchSize.Point()}, 0, paint.Solid(black))
		p.RRect(geom.Rc(10, 10, 50, 50), 4, paint.Solid(red))
		if ribbon {
			p.Ribbon(pts)
		}
		p.RRect(geom.Rc(10, 10, 50, 50), 4, paint.Solid(red))
		return p.Ops()
	}
	w, h := int(benchSize.W), int(benchSize.H)
	cost := func(ops []paint.Op) (draws int, allocs float64) {
		r.canvasOK, r.direct = false, false
		r.Draw(ops, paint.Everything, w, h, 1)
		r.draws = 0
		r.Draw(ops, paint.Everything, w, h, 1)
		draws = r.draws
		allocs = testing.AllocsPerRun(10, func() { r.Draw(ops, paint.Everything, w, h, 1) })
		return draws, allocs
	}
	plainDraws, plainAllocs := cost(frame(false))
	draws, allocs := cost(frame(true))
	if draws != plainDraws {
		t.Errorf("a frame of shapes and a ribbon took %d draw calls, want the %d of the shapes alone", draws, plainDraws)
	}
	if allocs > plainAllocs {
		t.Errorf("a frame with a ribbon allocated %v times, want the %v of one without", allocs, plainAllocs)
	}
}

func TestARibbonMovingRedrawsInPartAsItWouldInFull(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	w, h := int(benchSize.W), int(benchSize.H)
	var p paint.Painter
	record := func(y float32) {
		p.Reset()
		p.RRect(geom.Rect{Max: benchSize.Point()}, 0, paint.Solid(black))
		p.RRect(geom.Rc(500, 400, 100, 100), 8, paint.Solid(red))
		p.Ribbon([]paint.RibbonPoint{
			{At: geom.Pt(100, 100), Width: 6, Color: red},
			{At: geom.Pt(150, y), Width: 20, Color: blue},
			{At: geom.Pt(200, 100), Width: 0, Color: white},
		})
	}
	// The first frame goes straight to the window, and the second
	// fills the canvas, so the third is the first to redraw in part.
	for _, y := range []float32{140, 140, 160} {
		record(y)
		r.Draw(p.Ops(), p.Damage(), w, h, 1)
	}
	if s := r.Redrawn.Size(); s.W > 160 || s.H > 120 {
		t.Fatalf("a ribbon bending further redrew %v, want about the ribbon", r.Redrawn)
	}
	partial := canvas(r)
	r.canvasOK = false
	r.Draw(p.Ops(), p.Damage(), w, h, 1)
	full := canvas(r)
	for i := range full {
		if d := int(full[i]) - int(partial[i]); d > 1 || d < -1 {
			px := i / 4
			t.Fatalf("pixel (%d, %d) is %d redrawn in part and %d in full", px%w, h-1-px/w, partial[i], full[i])
		}
	}
}

func TestARibbonInATiltedLayerTurnsWithIt(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	pix := drawn(r, func(p *paint.Painter) {
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(300, 200, 200, 200), Opacity: 1, Tilt: paint.Tilt{Y: 1}})()
		p.Ribbon(line(geom.Pt(300, 300), geom.Pt(500, 300), 10, 20, white))
	})
	lit := 0
	for x := 250; x < 550; x++ {
		if pixelAt(pix, x, 300)[0] > 0x80 {
			lit++
		}
	}
	// Turned a radian away, it shows about as wide as cos 1 makes it.
	if lit < 80 || lit > 150 {
		t.Errorf("the turned ribbon shows %d pixels wide, want about 110", lit)
	}
}

func TestARibbonsEndsAreSmooth(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	// Its ends lie a quarter of a pixel into a column of pixels.
	pix := drawn(r, func(p *paint.Painter) {
		p.Ribbon(line(geom.Pt(100.75, 100), geom.Pt(300.25, 100), 2, 10, white))
	})
	quarter := [4]byte{0x40, 0x40, 0x40, 0xff}
	for _, c := range []struct {
		x    int
		want [4]byte
		what string
	}{
		{99, px(black), "the column before its start"},
		{100, quarter, "the column its start cuts a quarter into"},
		{101, px(white), "the column after its start"},
		{299, px(white), "the column before its end"},
		{300, quarter, "the column its end cuts a quarter into"},
		{301, px(black), "the column after its end"},
	} {
		if got := pixelAt(pix, c.x, 100); !near(got, c.want, 3) {
			t.Errorf("%s, at %d, is %v, want %v", c.what, c.x, got, c.want)
		}
	}
}

func TestARibbonOfManyShortStretchesShowsNoSeams(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	// Stretches of a third of a pixel, and of four pixels and a bit.
	short := drawn(r, func(p *paint.Painter) {
		p.Ribbon(line(geom.Pt(100.3, 100.2), geom.Pt(300.6, 120.7), 601, 9.5, white))
	})
	long := drawn(r, func(p *paint.Painter) {
		p.Ribbon(line(geom.Pt(100.3, 100.2), geom.Pt(300.6, 120.7), 46, 9.5, white))
	})
	for y := 85; y < 135; y++ {
		for x := 95; x < 306; x++ {
			if a, b := pixelAt(short, x, y), pixelAt(long, x, y); !near(a, b, 2) {
				t.Fatalf("at %d,%d the ribbon of short stretches is %v and of long ones %v", x, y, a, b)
			}
		}
	}
}
