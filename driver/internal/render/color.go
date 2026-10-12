//go:build linux || windows || darwin

package render

import (
	"fmt"
	"math"
	"strconv"

	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/internal/icc"
)

// Colour management: the frame is drawn and blended in sRGB, as it
// always is, into the canvas. As the canvas is copied to the window, a
// table converts each pixel from sRGB to the display's own colours,
// where the display is not sRGB. The copy happens on every frame that
// is not drawn straight to the window, so the conversion costs a
// texture read or two per pixel in a pass that runs anyway. Frames are
// never drawn straight to the window while it converts.
//
// The table is icc.LUTSize steps along each axis, laid out in a 2D
// texture as that many squares side by side, one for each step of
// blue, with red across each square and green down it. A colour reads
// the two squares either side of its blue, each with the hardware's
// bilinear filter, and mixes them.

// GL constants for the colour table.
const (
	glTexture7  = 0x84C7
	glRGBA16F   = 0x881A
	glHalfFloat = 0x140B
)

// convertShader is the part of the draw program that converts its
// colour to the display's, compiled in under CONVERT. LUT_N is the
// table's steps along each axis.
const convertShader = `
#ifdef CONVERT
uniform sampler2D u_lut;

vec4 toDisplay(vec4 c) {
	if (c.a <= 0.0) {
		return c;
	}
	vec3 v = clamp(c.rgb / c.a, 0.0, 1.0) * (LUT_N - 1.0);
	float b0 = min(floor(v.b), LUT_N - 2.0);
	vec2 at = vec2(v.r + 0.5 + b0 * LUT_N, v.g + 0.5) / vec2(LUT_N * LUT_N, LUT_N);
	vec3 lo = texture(u_lut, at).rgb;
	vec3 hi = texture(u_lut, at + vec2(1.0 / LUT_N, 0.0)).rgb;
	return vec4(mix(lo, hi, v.b - b0) * c.a, c.a);
}
#endif
`

// lutN is the table's steps along each axis, as the shader has it.
var lutN = strconv.Itoa(icc.LUTSize) + ".0"

// colorState is the window's colour conversion.
type colorState struct {
	// lut is the table in use, or nil to draw sRGB as it is, and tex the
	// texture holding it.
	lut *icc.LUT
	tex uint32
	// changed says the table changed since the last frame, which then
	// goes to the screen whole.
	changed bool
}

// SetColor sets the table that converts the window's frames from sRGB
// to the display's colours, or nil to leave them as they are. It
// returns an error where the converting program cannot be built, and
// then converts nothing.
func (r *Renderer) SetColor(lut *icc.LUT) error {
	c := &r.color
	if lut == c.lut {
		return nil
	}
	if lut != nil && lut.N != icc.LUTSize {
		return fmt.Errorf("render: colour table of %d steps, want %d", lut.N, icc.LUTSize)
	}
	c.changed = true
	c.lut = nil
	if lut == nil {
		if c.tex != 0 {
			r.GL.DeleteTexture(c.tex)
			c.tex = 0
		}
		return nil
	}
	prog, err := r.shared.convertProgram(r.GL)
	if err != nil {
		return err
	}
	r.convertProg = prog
	g := r.GL
	if c.tex == 0 {
		c.tex = g.CreateTexture()
	}
	n := lut.N
	pix := make([]byte, 0, n*n*n*8)
	// Row g holds, for each blue b, the reds of that square.
	for gi := range n {
		for b := range n {
			for ri := range n {
				i := ((b*n+gi)*n + ri) * 3
				for _, v := range [4]float32{lut.RGB[i], lut.RGB[i+1], lut.RGB[i+2], 1} {
					h := half(v)
					pix = append(pix, byte(h), byte(h>>8))
				}
			}
		}
	}
	g.ActiveTexture(glTexture7)
	g.BindTexture(gl.TEXTURE_2D, c.tex)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, glLinear)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, glLinear)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	g.PixelStorei(gl.UNPACK_ALIGNMENT, 4)
	g.TexImage2D(gl.TEXTURE_2D, 0, glRGBA16F, int32(n*n), int32(n), gl.RGBA, glHalfFloat, pix)
	g.ActiveTexture(gl.TEXTURE0)
	c.lut = lut
	return nil
}

// Converting reports whether the renderer converts its frames to the
// display's colours.
func (r *Renderer) Converting() bool { return r.color.lut != nil }

// ToDisplay converts a straight sRGB colour, each channel 0 to 1, as
// the window's frames are converted, for pixels read back from the
// canvas. With no table it returns c as it is.
func (r *Renderer) ToDisplay(c [3]float32) [3]float32 {
	if r.color.lut == nil {
		return c
	}
	return r.color.lut.Apply(c)
}

// backgroundToDisplay converts a frame's background colour as the
// window's pixels are converted.
func (r *Renderer) backgroundToDisplay(bg [4]float32) [4]float32 {
	if r.color.lut == nil {
		return bg
	}
	c := r.color.lut.Apply([3]float32{bg[0], bg[1], bg[2]})
	return [4]float32{c[0], c[1], c[2], bg[3]}
}

// beginConvert switches to the converting program, with the table on
// unit 7, for the copy to the window. The batch queued is drawn first.
func (r *Renderer) beginConvert() {
	r.flush()
	g := r.GL
	g.ActiveTexture(glTexture7)
	g.BindTexture(gl.TEXTURE_2D, r.color.tex)
	g.ActiveTexture(gl.TEXTURE0)
	g.UseProgram(r.convertProg.id)
}

// endConvert draws the copy and goes back to the draw program.
func (r *Renderer) endConvert() {
	r.flush()
	r.useDraw()
}

// releaseColor frees the table's texture.
func (r *Renderer) releaseColor() {
	if r.color.tex != 0 {
		r.GL.DeleteTexture(r.color.tex)
		r.color.tex = 0
	}
	r.color.lut = nil
}

// half encodes v as an IEEE half float, rounded to nearest. The table
// holds values from 0 to 1, so the encoding needs no infinities.
func half(v float32) uint16 {
	if v <= 0 {
		return 0
	}
	b := math.Float32bits(v)
	exp := int((b>>23)&0xff) - 127 + 15
	mant := b & 0x7fffff
	if exp <= 0 {
		// Subnormal: shift the mantissa, with its hidden bit, into place.
		if exp < -10 {
			return 0
		}
		m := (mant | 0x800000) >> uint(1-exp)
		return uint16((m + 0x1000) >> 13)
	}
	if exp >= 31 {
		return 0x7bff
	}
	// Rounding may carry into the exponent, which is still correct.
	return uint16(exp<<10) + uint16((mant+0x1000)>>13)
}
