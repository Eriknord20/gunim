package text

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"

	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/font/opentype/tables"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/vector"
)

// IsColor reports whether glyph id is drawn in colours of its own, as an emoji font's are, rather than in the
// text's colour. [Face.RasterizeColor] renders it.
func (f *Face) IsColor(id uint32) bool {
	mu.Lock()
	defer mu.Unlock()
	if c, ok := f.colored[id]; ok {
		return c
	}
	_, colr := f.face.GlyphDataColor(font.GID(id))
	c := colr
	if !c {
		_, c = f.face.GlyphDataBitmap(font.GID(id))
	}
	if f.colored == nil {
		f.colored = map[uint32]bool{}
	}
	f.colored[id] = c
	return c
}

// RasterizeColor renders colour glyph id at sizePx device pixels, as premultiplied RGBA with four bytes a pixel.
// It draws the glyph's COLR paint, or scales its bitmap from the nearest size the font keeps, and returns an empty
// mask for a glyph with neither.
func (f *Face) RasterizeColor(id uint32, sizePx float32) Mask {
	mu.Lock()
	paint, colr := f.face.GlyphDataColor(font.GID(id))
	var bitmap font.GlyphBitmap
	isBitmap := false
	if !colr {
		bitmap, isBitmap = f.face.GlyphDataBitmap(font.GID(id))
	}
	mu.Unlock()
	switch {
	case colr:
		return f.rasterizeCOLR(id, paint.Paint, sizePx)
	case isBitmap:
		return f.rasterizeBitmap(id, bitmap, sizePx)
	}
	return Mask{}
}

// rasterizeBitmap scales a glyph's PNG to sizePx, and sets it on the baseline as the font's metrics say.
func (f *Face) rasterizeBitmap(id uint32, b font.GlyphBitmap, sizePx float32) Mask {
	if b.Format != font.PNG {
		return Mask{}
	}
	src, err := png.Decode(bytes.NewReader(b.Data))
	if err != nil {
		return Mask{}
	}
	mu.Lock()
	advance := f.face.HorizontalAdvance(font.GID(id))
	extents, _ := f.face.GlyphExtents(font.GID(id))
	mu.Unlock()
	scale := sizePx / f.upem
	w := int(math.Round(float64(advance * scale)))
	if w <= 0 {
		w = int(math.Round(float64(sizePx)))
	}
	sb := src.Bounds()
	h := max(1, int(math.Round(float64(w)*float64(sb.Dy())/float64(max(sb.Dx(), 1)))))
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, sb, draw.Src, nil)
	// The bitmap's top sits at the glyph's ascent, or at the font's when the glyph gives none.
	top := extents.YBearing * scale
	if top == 0 {
		a, _, _ := f.Metrics(sizePx)
		top = a
	}
	return Mask{Pix: dst.Pix, W: w, H: h, Offset: image.Pt(0, -int(math.Round(float64(top)))), Color: true}
}

// affine maps x, y to A*x + C*y + E, B*x + D*y + F.
type affine struct{ a, b, c, d, e, f float64 }

func (m affine) apply(x, y float64) (atX, atY float64) {
	return m.a*x + m.c*y + m.e, m.b*x + m.d*y + m.f
}

// then returns m applied after n: n first, then m.
func (m affine) then(n affine) affine {
	return affine{
		a: m.a*n.a + m.c*n.b, b: m.b*n.a + m.d*n.b,
		c: m.a*n.c + m.c*n.d, d: m.b*n.c + m.d*n.d,
		e: m.a*n.e + m.c*n.f + m.e, f: m.b*n.e + m.d*n.f + m.f,
	}
}

func (m affine) invert() (affine, bool) {
	det := m.a*m.d - m.b*m.c
	if math.Abs(det) < 1e-12 {
		return affine{}, false
	}
	a, b, c, d := m.d/det, -m.b/det, -m.c/det, m.a/det
	return affine{a: a, b: b, c: c, d: d, e: -(a*m.e + c*m.f), f: -(b*m.e + d*m.f)}, true
}

func translate(x, y float64) affine { return affine{a: 1, d: 1, e: x, f: y} }
func scaling(x, y float64) affine   { return affine{a: x, d: y} }
func rotation(turns float64) affine {
	s, c := math.Sincos(turns * math.Pi)
	return affine{a: c, b: s, c: -s, d: c}
}
func skewing(x, y float64) affine {
	return affine{a: 1, b: math.Tan(y * math.Pi), c: -math.Tan(x * math.Pi), d: 1}
}

// around returns m applied about the point x, y rather than the origin.
func around(m affine, x, y float64) affine {
	return translate(x, y).then(m).then(translate(-x, -y))
}

// f214 reads a 2.14 fixed point number.
func f214(v tables.Coord) float64 { return float64(v) / 16384 }

// layer is premultiplied RGBA, four floats a pixel.
type layer []float32

// colr draws one COLR glyph into a w by h layer.
type colr struct {
	face    *font.Face
	palette []tables.ColorRecord
	w, h    int
	// depth guards against a paint graph that loops.
	depth int
}

// rasterizeCOLR draws a COLR glyph's paint at sizePx, within its clip box, or the outline's bounds when it has
// none.
func (f *Face) rasterizeCOLR(id uint32, p tables.PaintTable, sizePx float32) Mask {
	scale := float64(sizePx / f.upem)
	mu.Lock()
	defer mu.Unlock()
	xMin, yMin, xMax, yMax, ok := f.colrBounds(id)
	if !ok {
		return Mask{}
	}
	x0, y0 := math.Floor(xMin*scale), math.Floor(-yMax*scale)
	x1, y1 := math.Ceil(xMax*scale), math.Ceil(-yMin*scale)
	w, h := int(x1-x0), int(y1-y0)
	if w <= 0 || h <= 0 || w > 1024 || h > 1024 {
		return Mask{}
	}
	var palette []tables.ColorRecord
	if len(f.face.CPAL) > 0 {
		palette = f.face.CPAL[0]
	}
	c := &colr{face: f.face, palette: palette, w: w, h: h}
	out := make(layer, w*h*4)
	// Font units, y up, to the mask's pixels, y down.
	c.paint(p, affine{a: scale, d: -scale, e: -x0, f: -y0}, out)
	pix := make([]byte, w*h*4)
	for i, v := range out {
		pix[i] = uint8(min(max(v, 0), 1)*255 + 0.5)
	}
	return Mask{Pix: pix, W: w, H: h, Offset: image.Pt(int(x0), int(y0)), Color: true}
}

// colrBounds returns a colour glyph's box in font units: its clip box, or else its outline's. It runs with mu held.
func (f *Face) colrBounds(id uint32) (xMin, yMin, xMax, yMax float64, ok bool) {
	if f.face.COLR != nil {
		if box, found := f.face.COLR.ClipList.Search(tables.GlyphID(id)); found {
			switch b := box.(type) {
			case tables.ClipBoxFormat1:
				return float64(b.XMin), float64(b.YMin), float64(b.XMax), float64(b.YMax), true
			case tables.ClipBoxFormat2:
				return float64(b.XMin), float64(b.YMin), float64(b.XMax), float64(b.YMax), true
			}
		}
	}
	ext, found := f.face.GlyphExtents(font.GID(id))
	if !found || ext.Width == 0 {
		return 0, 0, 0, 0, false
	}
	return float64(ext.XBearing), float64(ext.YBearing + ext.Height), float64(ext.XBearing + ext.Width),
		float64(ext.YBearing), true
}

// paint draws p onto dst, with m taking the paint's coordinates to dst's pixels.
func (c *colr) paint(p tables.PaintTable, m affine, dst layer) {
	if p == nil || c.depth > 64 {
		return
	}
	c.depth++
	defer func() { c.depth-- }()
	switch p := p.(type) {
	case tables.PaintColrLayers:
		layers, err := c.face.COLR.LayerList.Resolve(p)
		if err != nil {
			return
		}
		for _, q := range layers {
			c.paint(q, m, dst)
		}
	case tables.PaintColrGlyph:
		if q, ok := c.face.COLR.Search(p.GlyphID); ok {
			c.paint(q, m, dst)
		}
	case tables.PaintGlyph:
		c.glyph(p, m, dst)
	case tables.PaintSolid:
		c.fill(dst, func(float64, float64) [4]float32 { return c.color(p.PaletteIndex, f214(p.Alpha)) }, m, false)
	case tables.PaintVarSolid:
		c.fill(dst, func(float64, float64) [4]float32 { return c.color(p.PaletteIndex, f214(p.Alpha)) }, m, false)
	case tables.PaintLinearGradient:
		c.linear(p.ColorLine.Extend, stops(p.ColorLine.ColorStops), p.X0, p.Y0, p.X1, p.Y1, p.X2, p.Y2, m, dst)
	case tables.PaintVarLinearGradient:
		c.linear(p.ColorLine.Extend, varStops(p.ColorLine.ColorStops), p.X0, p.Y0, p.X1, p.Y1, p.X2, p.Y2, m, dst)
	case tables.PaintRadialGradient:
		c.radial(p.ColorLine.Extend, stops(p.ColorLine.ColorStops), p.X0, p.Y0, p.Radius0, p.X1, p.Y1, p.Radius1, m, dst)
	case tables.PaintVarRadialGradient:
		c.radial(p.ColorLine.Extend, varStops(p.ColorLine.ColorStops), p.X0, p.Y0, p.Radius0, p.X1, p.Y1, p.Radius1, m, dst)
	case tables.PaintSweepGradient:
		c.sweep(p.ColorLine.Extend, stops(p.ColorLine.ColorStops), p.CenterX, p.CenterY, p.StartAngle, p.EndAngle, m, dst)
	case tables.PaintVarSweepGradient:
		c.sweep(p.ColorLine.Extend, varStops(p.ColorLine.ColorStops), p.CenterX, p.CenterY, p.StartAngle, p.EndAngle, m, dst)
	case tables.PaintTransform:
		t := p.Transform
		c.paint(p.Paint, m.then(affine{float64(t.Xx), float64(t.Yx), float64(t.Xy), float64(t.Yy), float64(t.Dx), float64(t.Dy)}), dst)
	case tables.PaintVarTransform:
		t := p.Transform
		c.paint(p.Paint, m.then(affine{float64(t.Xx), float64(t.Yx), float64(t.Xy), float64(t.Yy), float64(t.Dx), float64(t.Dy)}), dst)
	case tables.PaintTranslate:
		c.paint(p.Paint, m.then(translate(float64(p.Dx), float64(p.Dy))), dst)
	case tables.PaintVarTranslate:
		c.paint(p.Paint, m.then(translate(float64(p.Dx), float64(p.Dy))), dst)
	case tables.PaintScale:
		c.paint(p.Paint, m.then(scaling(f214(p.ScaleX), f214(p.ScaleY))), dst)
	case tables.PaintVarScale:
		c.paint(p.Paint, m.then(scaling(f214(p.ScaleX), f214(p.ScaleY))), dst)
	case tables.PaintScaleAroundCenter:
		c.paint(p.Paint, m.then(around(scaling(f214(p.ScaleX), f214(p.ScaleY)), float64(p.CenterX), float64(p.CenterY))), dst)
	case tables.PaintVarScaleAroundCenter:
		c.paint(p.Paint, m.then(around(scaling(f214(p.ScaleX), f214(p.ScaleY)), float64(p.CenterX), float64(p.CenterY))), dst)
	case tables.PaintScaleUniform:
		c.paint(p.Paint, m.then(scaling(f214(p.Scale), f214(p.Scale))), dst)
	case tables.PaintVarScaleUniform:
		c.paint(p.Paint, m.then(scaling(f214(p.Scale), f214(p.Scale))), dst)
	case tables.PaintScaleUniformAroundCenter:
		c.paint(p.Paint, m.then(around(scaling(f214(p.Scale), f214(p.Scale)), float64(p.CenterX), float64(p.CenterY))), dst)
	case tables.PaintVarScaleUniformAroundCenter:
		c.paint(p.Paint, m.then(around(scaling(f214(p.Scale), f214(p.Scale)), float64(p.CenterX), float64(p.CenterY))), dst)
	case tables.PaintRotate:
		c.paint(p.Paint, m.then(rotation(f214(p.Angle))), dst)
	case tables.PaintVarRotate:
		c.paint(p.Paint, m.then(rotation(f214(p.Angle))), dst)
	case tables.PaintRotateAroundCenter:
		c.paint(p.Paint, m.then(around(rotation(f214(p.Angle)), float64(p.CenterX), float64(p.CenterY))), dst)
	case tables.PaintVarRotateAroundCenter:
		c.paint(p.Paint, m.then(around(rotation(f214(p.Angle)), float64(p.CenterX), float64(p.CenterY))), dst)
	case tables.PaintSkew:
		c.paint(p.Paint, m.then(skewing(f214(p.XSkewAngle), f214(p.YSkewAngle))), dst)
	case tables.PaintVarSkew:
		c.paint(p.Paint, m.then(skewing(f214(p.XSkewAngle), f214(p.YSkewAngle))), dst)
	case tables.PaintSkewAroundCenter:
		c.paint(p.Paint, m.then(around(skewing(f214(p.XSkewAngle), f214(p.YSkewAngle)), float64(p.CenterX), float64(p.CenterY))), dst)
	case tables.PaintVarSkewAroundCenter:
		c.paint(p.Paint, m.then(around(skewing(f214(p.XSkewAngle), f214(p.YSkewAngle)), float64(p.CenterX), float64(p.CenterY))), dst)
	case tables.PaintComposite:
		c.composite(p, m, dst)
	}
}

// glyph draws a PaintGlyph: its paint, cut to the glyph's outline.
func (c *colr) glyph(p tables.PaintGlyph, m affine, dst layer) {
	outline, ok := c.face.GlyphDataOutline(font.GID(p.GlyphID))
	if !ok || len(outline.Segments) == 0 {
		return
	}
	var r vector.Rasterizer
	r.Reset(c.w, c.h)
	pt := func(s font.SegmentPoint) (float32, float32) {
		x, y := m.apply(float64(s.X), float64(s.Y))
		return float32(x), float32(y)
	}
	for _, s := range outline.Segments {
		switch s.Op {
		case ot.SegmentOpMoveTo:
			r.MoveTo(pt(s.Args[0]))
		case ot.SegmentOpLineTo:
			r.LineTo(pt(s.Args[0]))
		case ot.SegmentOpQuadTo:
			bx, by := pt(s.Args[0])
			cx, cy := pt(s.Args[1])
			r.QuadTo(bx, by, cx, cy)
		case ot.SegmentOpCubeTo:
			bx, by := pt(s.Args[0])
			cx, cy := pt(s.Args[1])
			dx, dy := pt(s.Args[2])
			r.CubeTo(bx, by, cx, cy, dx, dy)
		}
	}
	r.ClosePath()
	mask := image.NewAlpha(image.Rect(0, 0, c.w, c.h))
	r.Draw(mask, mask.Bounds(), image.Opaque, image.Point{})
	src := make(layer, len(dst))
	c.paint(p.Paint, m, src)
	for i, a := range mask.Pix {
		if a == 0 {
			continue
		}
		k := float32(a) / 255
		over(dst[i*4:i*4+4], src[i*4]*k, src[i*4+1]*k, src[i*4+2]*k, src[i*4+3]*k)
	}
}

// over puts premultiplied r, g, b, a over d.
func over(d []float32, r, g, b, a float32) {
	k := 1 - a
	d[0], d[1], d[2], d[3] = r+d[0]*k, g+d[1]*k, b+d[2]*k, a+d[3]*k
}

// color returns palette entry i, premultiplied and faded by alpha. The foreground entry, 0xFFFF, is black.
func (c *colr) color(i uint16, alpha float64) [4]float32 {
	col := color.NRGBA{A: 255}
	if int(i) < len(c.palette) {
		e := c.palette[i]
		col = color.NRGBA{R: e.Red, G: e.Green, B: e.Blue, A: e.Alpha}
	}
	a := float32(float64(col.A) / 255 * alpha)
	return [4]float32{float32(col.R) / 255 * a, float32(col.G) / 255 * a, float32(col.B) / 255 * a, a}
}

// fill puts shade, a colour for each point in the paint's coordinates, over every pixel of dst.
func (c *colr) fill(dst layer, shade func(x, y float64) [4]float32, m affine, local bool) {
	inv, ok := m.invert()
	if !ok {
		return
	}
	for y := range c.h {
		for x := range c.w {
			px, py := float64(x)+0.5, float64(y)+0.5
			if local {
				px, py = inv.apply(px, py)
			}
			col := shade(px, py)
			if col[3] <= 0 {
				continue
			}
			i := (y*c.w + x) * 4
			over(dst[i:i+4], col[0], col[1], col[2], col[3])
		}
	}
}

// colorStop is a colour stop on a gradient's line: where it is, and its palette entry and alpha.
type colorStop struct {
	at    float64
	index uint16
	alpha float64
}

func stops(s []tables.ColorStop) []colorStop {
	out := make([]colorStop, len(s))
	for i, v := range s {
		out[i] = colorStop{f214(v.StopOffset), v.PaletteIndex, f214(v.Alpha)}
	}
	return sortStops(out)
}

func varStops(s []tables.VarColorStop) []colorStop {
	out := make([]colorStop, len(s))
	for i, v := range s {
		out[i] = colorStop{f214(v.StopOffset), v.PaletteIndex, f214(v.Alpha)}
	}
	return sortStops(out)
}

func sortStops(s []colorStop) []colorStop {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].at < s[j-1].at; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	return s
}

// ramp returns the colour at t along a gradient's stops, beyond them as extend says.
func (c *colr) ramp(s []colorStop, extend tables.Extend, t float64) [4]float32 {
	if len(s) == 0 {
		return [4]float32{}
	}
	first, last := s[0].at, s[len(s)-1].at
	if span := last - first; span > 0 {
		switch extend {
		case tables.ExtendRepeat:
			t = first + math.Mod(math.Mod(t-first, span)+span, span)
		case tables.ExtendReflect:
			u := math.Mod(math.Mod(t-first, 2*span)+2*span, 2*span)
			if u > span {
				u = 2*span - u
			}
			t = first + u
		default:
			// Padding is the default the gradient is built with.
		}
	}
	if t <= first {
		return c.color(s[0].index, s[0].alpha)
	}
	for i := 1; i < len(s); i++ {
		if t <= s[i].at {
			a, b := c.color(s[i-1].index, s[i-1].alpha), c.color(s[i].index, s[i].alpha)
			k := float32(0)
			if d := s[i].at - s[i-1].at; d > 0 {
				k = float32((t - s[i-1].at) / d)
			}
			return [4]float32{a[0] + (b[0]-a[0])*k, a[1] + (b[1]-a[1])*k, a[2] + (b[2]-a[2])*k, a[3] + (b[3]-a[3])*k}
		}
	}
	return c.color(s[len(s)-1].index, s[len(s)-1].alpha)
}

// linear draws a linear gradient from p0 toward p1, its lines of equal colour parallel to p0 to p2.
func (c *colr) linear(extend tables.Extend, s []colorStop, x0, y0, x1, y1, x2, y2 int16, m affine, dst layer) {
	p0x, p0y := float64(x0), float64(y0)
	dx, dy := float64(x1)-p0x, float64(y1)-p0y
	nx, ny := -(float64(y2) - p0y), float64(x2)-p0x
	if nn := nx*nx + ny*ny; nn > 0 {
		// Project p0 to p1 onto the normal of p0 to p2.
		k := (dx*nx + dy*ny) / nn
		dx, dy = nx*k, ny*k
	}
	dd := dx*dx + dy*dy
	if dd == 0 {
		return
	}
	c.fill(dst, func(x, y float64) [4]float32 {
		return c.ramp(s, extend, ((x-p0x)*dx+(y-p0y)*dy)/dd)
	}, m, true)
}

// radial draws a gradient between two circles, as a two point conical gradient.
func (c *colr) radial(extend tables.Extend, s []colorStop, x0, y0 int16, r0 uint16, x1, y1 int16, r1 uint16, m affine, dst layer) {
	c0x, c0y, rad0 := float64(x0), float64(y0), float64(r0)
	cdx, cdy, dr := float64(x1)-c0x, float64(y1)-c0y, float64(r1)-rad0
	a := cdx*cdx + cdy*cdy - dr*dr
	c.fill(dst, func(x, y float64) [4]float32 {
		px, py := x-c0x, y-c0y
		b := px*cdx + py*cdy + rad0*dr
		cc := px*px + py*py - rad0*rad0
		var t float64
		if math.Abs(a) < 1e-9 {
			if b == 0 {
				return [4]float32{}
			}
			t = cc / (2 * b)
		} else {
			disc := b*b - a*cc
			if disc < 0 {
				return [4]float32{}
			}
			sq := math.Sqrt(disc)
			t = (b + sq) / a
			if rad0+t*dr < 0 {
				t = (b - sq) / a
			}
		}
		if rad0+t*dr < 0 {
			return [4]float32{}
		}
		return c.ramp(s, extend, t)
	}, m, true)
}

// sweep draws a gradient around a centre, from one angle to another, counter-clockwise.
func (c *colr) sweep(extend tables.Extend, s []colorStop, cx, cy int16, start, end tables.Coord, m affine, dst layer) {
	from, to := (f214(start)+1)*180, (f214(end)+1)*180
	if to == from {
		return
	}
	c.fill(dst, func(x, y float64) [4]float32 {
		angle := math.Atan2(y-float64(cy), x-float64(cx)) * 180 / math.Pi
		if angle < 0 {
			angle += 360
		}
		return c.ramp(s, extend, (angle-from)/(to-from))
	}, m, true)
}

// composite draws a PaintComposite: its source and backdrop on layers of their own, put together as its mode
// says, and the result over dst.
func (c *colr) composite(p tables.PaintComposite, m affine, dst layer) {
	src, back := make(layer, len(dst)), make(layer, len(dst))
	c.paint(p.SourcePaint, m, src)
	c.paint(p.BackdropPaint, m, back)
	for i := 0; i < len(dst); i += 4 {
		s, b := src[i:i+4], back[i:i+4]
		sa, ba := s[3], b[3]
		var out [4]float32
		switch p.CompositeMode {
		case tables.CompositeClear:
		case tables.CompositeSrc:
			copy(out[:], s)
		case tables.CompositeDest:
			copy(out[:], b)
		case tables.CompositeDestOver:
			for k := range 4 {
				out[k] = b[k] + s[k]*(1-ba)
			}
		case tables.CompositeSrcIn:
			for k := range 4 {
				out[k] = s[k] * ba
			}
		case tables.CompositeDestIn:
			for k := range 4 {
				out[k] = b[k] * sa
			}
		case tables.CompositeSrcOut:
			for k := range 4 {
				out[k] = s[k] * (1 - ba)
			}
		case tables.CompositeDestOut:
			for k := range 4 {
				out[k] = b[k] * (1 - sa)
			}
		case tables.CompositeSrcAtop:
			for k := range 4 {
				out[k] = s[k]*ba + b[k]*(1-sa)
			}
		case tables.CompositeDestAtop:
			for k := range 4 {
				out[k] = b[k]*sa + s[k]*(1-ba)
			}
		case tables.CompositeXor:
			for k := range 4 {
				out[k] = s[k]*(1-ba) + b[k]*(1-sa)
			}
		case tables.CompositePlus:
			for k := range 4 {
				out[k] = min(s[k]+b[k], 1)
			}
		case tables.CompositeMultiply:
			for k := range 3 {
				out[k] = s[k]*b[k] + s[k]*(1-ba) + b[k]*(1-sa)
			}
			out[3] = sa + ba - sa*ba
		case tables.CompositeScreen:
			for k := range 4 {
				out[k] = s[k] + b[k] - s[k]*b[k]
			}
		default:
			// Other blend modes draw the source over the backdrop.
			for k := range 4 {
				out[k] = s[k] + b[k]*(1-sa)
			}
		}
		over(dst[i:i+4], out[0], out[1], out[2], out[3])
	}
}

// shows remembers, for each emoji asked about, whether the system draws it in colour.
var shows = map[string]bool{}

// EmojiShows reports whether s, one emoji, draws in colour as one picture: whether the fonts at hand have it.
// A picker shows only such emoji, since one newer than the system's emoji font would draw as a box.
func EmojiShows(s string) bool {
	mu.Lock()
	known, ok := shows[s]
	mu.Unlock()
	if ok {
		return known
	}
	run := Default().Shape(s, 16)
	show := len(run.Glyphs) == 1
	if show {
		f, found := Lookup(run.Glyphs[0].Face)
		show = found && f.IsColor(run.Glyphs[0].ID)
	}
	mu.Lock()
	shows[s] = show
	mu.Unlock()
	return show
}
