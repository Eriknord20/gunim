//go:build linux || windows || darwin

package render

import (
	"math"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
)

const (
	// atlasSize is the glyph atlas's width and height in pixels. At
	// 2048 it holds a few thousand glyphs at interface sizes on a
	// phone's screen of three device pixels to the logical one, and the
	// masks a frame draws there, which at 1024 overflowed it every
	// frame, to be made again each time.
	atlasSize = 2048
	// subpixel is how many horizontal shifts of a glyph the atlas
	// keeps, so a glyph at any fractional x lands within a quarter
	// pixel of where it belongs.
	subpixel = 4
)

// The contrast glyph coverage is enhanced by, for greyscale glyphs and
// for glyphs on subpixels.
const (
	greyContrast = 1.0
	lcdContrast  = 0.5
)

// gammaRatios are Direct2D's coefficients for correcting glyph coverage
// blended in gamma space, for gammas from 1 to 2.2 in steps of a tenth.
var gammaRatios = [...][4]float32{
	{0, 0, 0, 0},
	{0.0166, -0.0807, 0.2227, -0.0751},
	{0.0350, -0.1760, 0.4325, -0.1370},
	{0.0543, -0.2821, 0.6302, -0.1876},
	{0.0739, -0.3963, 0.8167, -0.2287},
	{0.0933, -0.5161, 0.9926, -0.2616},
	{0.1121, -0.6395, 1.1588, -0.2877},
	{0.1300, -0.7649, 1.3159, -0.3080},
	{0.1469, -0.8911, 1.4644, -0.3234},
	{0.1627, -1.0170, 1.6051, -0.3347},
	{0.1773, -1.1420, 1.7385, -0.3426},
	{0.1908, -1.2652, 1.8650, -0.3476},
	{0.2031, -1.3864, 1.9851, -0.3501},
}

// ratiosFor returns the gamma ratios nearest gamma, scaled as the
// shader uses them.
func ratiosFor(gamma float32) [4]float32 {
	i := int(math.Round(float64((gamma - 1) * 10)))
	r := gammaRatios[min(max(i, 0), len(gammaRatios)-1)]
	for k := range r {
		r[k] /= 4
	}
	return r
}

// glyphTexture is a renderer's copy of a shared atlas: the glyphs it
// has drawn, from the atlas's epoch. The greyscale atlas has one
// channel, and the subpixel one three.
type glyphTexture struct {
	tex   uint32
	epoch int
	have  map[glyphKey]bool
}

func (r *Renderer) initGlyphs() {
	r.glyphs.have = map[glyphKey]bool{}
	r.lcdGlyphs.have = map[glyphKey]bool{}
	r.colorGlyphs.have = map[glyphKey]bool{}
	r.glyphs.tex = r.newAtlas(gl.TEXTURE0, glR8, glRed, 1)
}

// newAtlas makes an empty atlas texture on unit, with channels bytes a
// pixel.
func (r *Renderer) newAtlas(unit uint32, internal int32, format uint32, channels int) uint32 {
	g := r.GL
	tex := g.CreateTexture()
	g.ActiveTexture(unit)
	g.BindTexture(gl.TEXTURE_2D, tex)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, glLinear)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, glLinear)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	g.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	g.TexImage2D(gl.TEXTURE_2D, 0, internal, atlasSize, atlasSize, format, gl.UNSIGNED_BYTE,
		make([]byte, atlasSize*atlasSize*channels))
	g.ActiveTexture(gl.TEXTURE0)
	return tex
}

// text queues a shaped run's glyphs.
//
// A run under a plain translation snaps each glyph to the pixel grid,
// to the nearest quarter pixel across and a whole pixel down, so text
// at rest is sharp. Under a scale or rotation, such as a dialog growing
// into place, the glyphs keep their resting size in the atlas and the
// quads carry the transform; they soften while moving and sharpen when
// the transform settles back to a translation.
//
// Glyphs use the panel's subpixels only at rest, and straight on the
// canvas or the window, whose pixels behind them are opaque.
func (r *Renderer) text(op *paint.TextOp) {
	if len(op.Glyphs) == 0 || op.Size <= 0 || op.Color.A == 0 {
		return
	}
	r.uses(0)
	t := op.Transform
	plain := t.A == 1 && t.B == 0 && t.D == 0 && t.E == 1
	sizePx := op.Size * r.scale
	if plain && !r.cull.Empty() && r.outside(r.textBox(op)) {
		return
	}
	raster := text.Raster{Hint: r.textRendering.Hinting == text.HintingLight}
	add := op.Blend == paint.BlendAdd
	l := look{kind: kindGlyph, add: add, color0: rgba(op.Color), color1: r.gamma, stroke: greyContrast}
	if r.subpixels && plain && r.depth == 0 {
		raster.LCD = true
		l.radius, l.stroke = 1, lcdContrast
		if r.textRendering.Smoothing == text.SubpixelBGR {
			l.radius = 2
		}
	}
	// Corners arrive in device pixels, placed by the glyph's own
	// transform: the op's without its translation.
	shape := paint.Transform{A: t.A, B: t.B, D: t.D, E: t.E}
	// A colour glyph keeps its own colours, and fades with the text.
	colorLook := look{kind: kindGlyph, add: add, color0: [4]float32{1, 1, 1, float32(op.Color.A) / 255}, radius: 3}

	var (
		face   *text.Face
		faceID uint32
	)
	for _, gly := range op.Glyphs {
		if face == nil || gly.Face != faceID {
			f, ok := text.Lookup(gly.Face)
			if !ok {
				continue
			}
			face, faceID = f, gly.Face
		}
		o := t.Apply(gly.At)
		ox, oy := o.X*r.scale, o.Y*r.scale
		if face.IsColor(gly.ID) {
			if plain {
				ox, oy = float32(math.Round(float64(ox))), float32(math.Round(float64(oy)))
			}
			key := glyphKey{face: faceID, id: gly.ID, size: int32(math.Round(float64(sizePx) * 64)), color: true}
			slot, ok := r.glyph(key, func() text.Mask { return face.RasterizeColor(key.id, sizePx) })
			if !ok {
				continue
			}
			shape.C, shape.F = ox, oy
			r.quad(corners(slot.rect(), slot.uv()), shape, 1, &colorLook)
			continue
		}
		var shift uint8
		if plain {
			fx := float32(math.Floor(float64(ox)))
			s := int(math.Round(float64(ox-fx) * subpixel))
			if s == subpixel {
				fx, s = fx+1, 0
			}
			ox, oy, shift = fx, float32(math.Round(float64(oy))), uint8(s)
		}
		key := glyphKeyFor(faceID, gly.ID, sizePx, shift, raster)
		slot, ok := r.glyph(key, func() text.Mask {
			return face.Rasterize(key.id, sizePx, float32(key.shift)/subpixel, key.raster)
		})
		if !ok {
			continue
		}
		r.uses(0)
		shape.C, shape.F = ox, oy
		r.quad(corners(slot.rect(), slot.uv()), shape, 1, &l)
	}
}

// textBox returns a box in device pixels that holds a run under a plain translation, with room for any glyph.
func (r *Renderer) textBox(op *paint.TextOp) geom.Rect {
	lo, hi := op.Glyphs[0].At, op.Glyphs[0].At
	for _, g := range op.Glyphs[1:] {
		lo = geom.Pt(min(lo.X, g.At.X), min(lo.Y, g.At.Y))
		hi = geom.Pt(max(hi.X, g.At.X), max(hi.Y, g.At.Y))
	}
	pad, t := 2*op.Size, op.Transform
	return geom.Rect{
		Min: geom.Pt((lo.X+t.C-pad)*r.scale, (lo.Y+t.F-pad)*r.scale),
		Max: geom.Pt((hi.X+t.C+pad)*r.scale, (hi.Y+t.F+pad)*r.scale),
	}
}

// rect returns the glyph's box, in device pixels from its origin.
func (s glyphSlot) rect() geom.Rect {
	x0, y0 := float32(s.off.X), float32(s.off.Y)
	return geom.Rect{Min: geom.Pt(x0, y0), Max: geom.Pt(x0+float32(s.w), y0+float32(s.h))}
}

// uv returns where the glyph sits in its atlas, as texture coordinates.
func (s glyphSlot) uv() geom.Rect {
	return geom.Rect{
		Min: geom.Pt(float32(s.x)/atlasSize, float32(s.y)/atlasSize),
		Max: geom.Pt(float32(s.x+s.w)/atlasSize, float32(s.y+s.h)/atlasSize),
	}
}

// glyph returns where a glyph sits in its atlas, rasterizing it with raster and copying it into this
// renderer's texture on first use. It reports false for a glyph with
// nothing to draw.
func (r *Renderer) glyph(key glyphKey, raster func() text.Mask) (glyphSlot, bool) {
	slot, epoch, ok := r.shared.glyph(key, raster)
	a, unit, format, channels := &r.glyphs, uint32(gl.TEXTURE0), uint32(glRed), 1
	switch {
	case key.color:
		a, unit, format, channels = &r.colorGlyphs, glTexture3, gl.RGBA, 4
		if a.tex == 0 {
			a.tex, a.epoch = r.newAtlas(unit, glRGBA8, format, channels), epoch
		}
	case key.raster.LCD:
		a, unit, format, channels = &r.lcdGlyphs, glTexture2, glRGB, 3
		if a.tex == 0 {
			a.tex, a.epoch = r.newAtlas(unit, glRGB8, format, channels), epoch
		}
	}
	g := r.GL
	if epoch != a.epoch {
		// The atlas started again, here or in another window. What is
		// queued was placed in the old one, so it draws first.
		r.flush()
		clear(a.have)
		a.epoch = epoch
		g.ActiveTexture(unit)
		g.BindTexture(gl.TEXTURE_2D, a.tex)
		g.TexSubImage2D(gl.TEXTURE_2D, 0, 0, 0, atlasSize, atlasSize, format, gl.UNSIGNED_BYTE,
			make([]byte, atlasSize*atlasSize*channels))
		g.ActiveTexture(gl.TEXTURE0)
	}
	if !ok {
		return glyphSlot{}, false
	}
	if !a.have[key] {
		// Queued glyphs are drawn later, from the texture as it will be
		// then, which holds them all: a new glyph only fills an empty
		// place.
		g.ActiveTexture(unit)
		g.BindTexture(gl.TEXTURE_2D, a.tex)
		g.TexSubImage2D(gl.TEXTURE_2D, 0, int32(slot.x), int32(slot.y), int32(slot.w), int32(slot.h), format,
			gl.UNSIGNED_BYTE, slot.pix)
		g.ActiveTexture(gl.TEXTURE0)
		a.have[key] = true
	}
	return slot.glyphSlot, true
}
