//go:build linux || windows || darwin

package render

import (
	"image"
	"image/color"
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
)

// ember is a dim orange, which two of added together make twice as
// bright, short of white in every channel.
var ember = color.NRGBA{R: 0x60, G: 0x30, B: 0x10, A: 0xff}

func TestAddedShapesSumTheirLightWhereTheyOverlap(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	t.Logf("dual-source blending: %v", r.dual)
	pix := drawn(r, func(p *paint.Painter) {
		p.RRect(geom.Rc(100, 100, 100, 50), 0, paint.Solid(ember))
		p.RRect(geom.Rc(150, 100, 100, 50), 0, paint.Solid(ember))
		defer p.Blend(paint.BlendAdd)()
		p.RRect(geom.Rc(100, 200, 100, 50), 0, paint.Solid(ember))
		p.RRect(geom.Rc(150, 200, 100, 50), 0, paint.Solid(ember))
	})
	twice := [4]byte{0xc0, 0x60, 0x20, 0xff}
	for _, c := range []struct {
		x, y int
		want [4]byte
		what string
	}{
		{125, 125, px(ember), "a shape drawn normally"},
		{175, 125, px(ember), "where two shapes drawn normally overlap"},
		{125, 225, px(ember), "an added shape on black"},
		{175, 225, twice, "where two added shapes overlap"},
	} {
		if got := pixelAt(pix, c.x, c.y); !near(got, c.want, 2) {
			t.Errorf("%s is %v, want %v", c.what, got, c.want)
		}
	}
}

func TestAnAddedMaskAndImageAddTheirLightToo(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < len(src.Pix); i += 4 {
		copy(src.Pix[i:], []byte{ember.R, ember.G, ember.B, 0xff})
	}
	img := paint.NewImage(src)
	n := 0
	pix := drawn(r, func(p *paint.Painter) {
		defer p.Blend(paint.BlendAdd)()
		p.Mask(countedSquare{n: &n, settled: true}, geom.Rc(100, 100, 100, 50), ember)
		p.Mask(countedSquare{n: &n, settled: true}, geom.Rc(150, 100, 100, 50), ember)
		p.Image(img, geom.Rc(100, 200, 100, 50), paint.ImageOpts{Opacity: 1})
		p.Image(img, geom.Rc(150, 200, 100, 50), paint.ImageOpts{Opacity: 1})
	})
	twice := [4]byte{0xc0, 0x60, 0x20, 0xff}
	if got := pixelAt(pix, 125, 125); !near(got, px(ember), 2) {
		t.Errorf("an added mask on black is %v, want %v", got, px(ember))
	}
	if got := pixelAt(pix, 175, 125); !near(got, twice, 2) {
		t.Errorf("where two added masks overlap is %v, want %v", got, twice)
	}
	if got := pixelAt(pix, 125, 225); !near(got, px(ember), 2) {
		t.Errorf("an added image on black is %v, want %v", got, px(ember))
	}
	if got := pixelAt(pix, 175, 225); !near(got, twice, 2) {
		t.Errorf("where two added images overlap is %v, want %v", got, twice)
	}
}

func TestAddedOpsDrawInTheBatchOfTheOnesAroundThem(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	n := 0
	run := text.Default().Shape("Glow", 13)
	frame := func(add bool) *paint.Painter {
		var p paint.Painter
		p.RRect(geom.Rect{Max: benchSize.Point()}, 0, paint.Solid(black))
		for i := range 20 {
			end := func() {}
			if add && i%2 == 1 {
				end = p.Blend(paint.BlendAdd)
			}
			x := float32(i * 20)
			p.RRect(geom.Rc(x, 10, 15, 15), 4, paint.Solid(ember))
			p.Mask(countedSquare{n: &n, settled: true}, geom.Rc(x, 40, 15, 15), ember)
			run.Paint(&p, geom.Pt(x, 70), ember)
			end()
		}
		return &p
	}
	draws := func(p *paint.Painter) int {
		w, h := int(benchSize.W), int(benchSize.H)
		r.Draw(p.Ops(), paint.Everything, w, h, 1)
		r.draws = 0
		r.Draw(p.Ops(), paint.Everything, w, h, 1)
		return r.draws
	}
	normal, mixed := draws(frame(false)), draws(frame(true))
	if mixed != normal {
		t.Errorf("a frame adding every other op drew in %d calls, want the %d of one drawn normally", mixed, normal)
	}
}

func TestAnAddedShapeInAFadedLayerAddsItsShareOfLight(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	light := color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff}
	pix := drawn(r, func(p *paint.Painter) {
		p.RRect(geom.Rc(50, 50, 400, 400), 0, paint.Solid(grey))
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(100, 100, 300, 300), Opacity: 0.5})()
		defer p.Blend(paint.BlendAdd)()
		p.RRect(geom.Rc(100, 100, 100, 100), 0, paint.Solid(light))
	})
	// Half of the light, added to the grey behind the layer.
	if got := pixelAt(pix, 150, 150); !near(got, [4]byte{0x80, 0x80, 0x80, 0xff}, 2) {
		t.Errorf("the added shape in a layer at half opacity is %v, want the grey and half the light", got)
	}
	if got := pixelAt(pix, 300, 300); !near(got, px(grey), 2) {
		t.Errorf("the rest of the layer is %v, want the grey behind it", got)
	}
}

func TestAnAddedShapeInAnEllipseIsCutToIt(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	pix := drawn(r, func(p *paint.Painter) {
		p.RRect(geom.Rc(100, 100, 200, 200), 0, paint.Solid(ember))
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(100, 100, 200, 200), Opacity: 1, Clip: true, Ellipse: true})()
		defer p.Blend(paint.BlendAdd)()
		p.RRect(geom.Rc(100, 100, 200, 200), 0, paint.Solid(ember))
	})
	if got := pixelAt(pix, 200, 200); !near(got, [4]byte{0xc0, 0x60, 0x20, 0xff}, 2) {
		t.Errorf("inside the ellipse is %v, want the light added twice", got)
	}
	if got := pixelAt(pix, 103, 103); !near(got, px(ember), 2) {
		t.Errorf("in the corner outside the ellipse is %v, want the light added once", got)
	}
}

func TestAShapeThatAsksToDrawNormallyCoversInsideAnAddScope(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	pix := drawn(r, func(p *paint.Painter) {
		defer p.Blend(paint.BlendAdd)()
		p.RRect(geom.Rc(100, 100, 200, 100), 0, paint.Solid(ember))
		p.DrawRRect(paint.RRectOp{Rect: geom.Rc(100, 100, 100, 100), Fill: paint.Solid(ember), Blend: paint.BlendNormal})
		p.RRect(geom.Rc(200, 100, 100, 100), 0, paint.Solid(ember))
	})
	if got := pixelAt(pix, 150, 150); !near(got, px(ember), 2) {
		t.Errorf("the shape drawn normally over added light is %v, want it covering, %v", got, px(ember))
	}
	if got := pixelAt(pix, 250, 150); !near(got, [4]byte{0xc0, 0x60, 0x20, 0xff}, 2) {
		t.Errorf("the shape added over added light is %v, want the light added twice", got)
	}
}

// grey is what added text and layers are drawn over: dim, so the light
// added to it stays short of white.
var grey = color.NRGBA{R: 0x40, G: 0x40, B: 0x40, A: 0xff}

func TestAddedTextSumsItsLightWithTheColourBeneath(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	runs := map[string]text.Run{"plain text": text.Default().Shape("Glowing text", 24)}
	if text.EmojiShows("🔥") {
		runs["an emoji"] = text.Default().Shape("🔥", 24)
	} else {
		t.Log("no colour emoji font here; the emoji goes untested")
	}
	glow := color.NRGBA{R: 0x80, G: 0x60, B: 0x40, A: 0xff}
	renderings := map[string]text.Rendering{"greyscale": {Smoothing: text.Greyscale, Gamma: 1.8}}
	if r.dual {
		renderings["on subpixels"] = lcd
	}
	for rname, tr := range renderings {
		r.SetText(tr, false)
		for name, run := range runs {
			// The same run, at whole pixels from the other, so its glyphs
			// raster the same: drawn normally on black, and added on grey.
			pix := drawn(r, func(p *paint.Painter) {
				p.RRect(geom.Rc(0, 100, 400, 100), 0, paint.Solid(grey))
				run.Paint(p, geom.Pt(20, 20), glow)
				defer p.Blend(paint.BlendAdd)()
				run.Paint(p, geom.Pt(20, 120), glow)
			})
			lit, bad := 0, 0
			for y := 10; y < 90; y++ {
				for x := 10; x < 390; x++ {
					alone := pixelAt(pix, x, y)
					if alone[0]|alone[1]|alone[2] != 0 {
						lit++
					}
					want := [4]byte{0, 0, 0, 0xff}
					for i := range 3 {
						want[i] = byte(min(255, int(alone[i])+int(grey.R)))
					}
					if got := pixelAt(pix, x, y+100); !near(got, want, 2) {
						bad++
						if bad <= 3 {
							t.Errorf("%s, %s: added at %d,%d is %v, want the grey and the text's %v", rname, name, x, y+100, got, want)
						}
					}
				}
			}
			if lit < 20 {
				t.Errorf("%s, %s: drew %d lit pixels, want some text", rname, name, lit)
			}
		}
	}
}

func TestAnAddedLayerAddsItsContentsTimesItsOpacity(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	light := color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff}
	for _, c := range []struct {
		what string
		opts paint.LayerOpts
	}{
		{"an added layer", paint.LayerOpts{Opacity: 0.5, Blend: paint.BlendAdd}},
		{"an added layer that would clip in place", paint.LayerOpts{Opacity: 1, Clip: true, Blend: paint.BlendAdd}},
		{"a tilted added layer", paint.LayerOpts{Opacity: 0.5, Tilt: paint.Tilt{X: 1e-4}, Blend: paint.BlendAdd}},
	} {
		pix := drawn(r, func(p *paint.Painter) {
			p.RRect(geom.Rc(50, 50, 400, 400), 0, paint.Solid(grey))
			c.opts.Bounds = geom.Rc(100, 100, 300, 300)
			defer p.Layer(c.opts)()
			p.RRect(geom.Rc(100, 100, 100, 100), 0, paint.Solid(light))
		})
		v := byte(0x40 + float32(0x80)*c.opts.Opacity)
		if got := pixelAt(pix, 150, 150); !near(got, [4]byte{v, v, v, 0xff}, 2) {
			t.Errorf("%s: its shape is %v, want the grey and the light times the opacity, %#x", c.what, got, v)
		}
		if got := pixelAt(pix, 300, 300); !near(got, px(grey), 2) {
			t.Errorf("%s: the rest of it is %v, want the grey behind it", c.what, got)
		}
	}
}
