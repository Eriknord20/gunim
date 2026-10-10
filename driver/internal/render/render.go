//go:build linux || windows || darwin

// Package render draws a [paint] op list with OpenGL 3.2 or OpenGL ES
// 3.0. Every driver that draws with GL shares it: the desktop one, and
// the Android one.
package render

import (
	"encoding/binary"
	"fmt"
	"image/color"
	"math"
	"os"
	"slices"
	"time"
	"unsafe"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
)

// GL constants the gl package leaves out.
const (
	glColorBufferBit     = 0x4000
	glLinear             = 0x2601
	glLinearMipmapLinear = 0x2703
	glTextureBaseLevel   = 0x813C
	glTextureMaxLevel    = 0x813D
	glUnsignedShort      = 0x1403
	glR8                 = 0x8229
	glRed                = 0x1903
	glRGB8               = 0x8051
	glRGB                = 0x1907
	glRGBA8              = 0x8058
	glStaticDraw         = 0x88E4
	glTexture1           = 0x84C1
	glTexture2           = 0x84C2
	glTexture3           = 0x84C3
	glTexture6           = 0x84C6
	glOneMinusSrc1Color  = 0x88FA
	glOneMinusSrc1Alpha  = 0x88FB
)

// A Renderer replays a [paint] op list with OpenGL. It lives on one
// window's render thread, with that window's context current.
//
// Every shape is one quad and one signed distance field, so a rounded
// rectangle stays crisp at any size, scale or fractional position.
// Shapes, shadows, glyphs, images and finished layers all go through
// one program, and each quad carries everything its pixels need in its
// vertices: the transform is applied on the CPU, and the colours and
// geometry ride along. So a run of ops of any of those kinds is one
// draw call. A batch ends when the frame moves to another target, when
// it needs another image, or when it is full.
//
// A layer draws into an offscreen texture the size of the window and
// is composited back with its opacity and, when it clips, its rounded
// bounds. A layer's Blur and Backdrop are separable Gaussian blurs, run
// at a reduced resolution when the radius is large; see blur.go.
// An opaque layer that clips to an upright rectangle, or not at all,
// draws in place instead, under a scissor.
//
// Text draws from a glyph atlas: each glyph is rasterized once per size
// and quarter-pixel shift; see glyphs.go. Masks, such as icons, share
// the greyscale atlas; see masks.go. Images upload once and stay
// on the GPU while frames use them; see images.go.
type Renderer struct {
	// Stats counts what frames send the GPU, for GUNIM_DEBUG_FRAMES.
	Stats  Stats
	GL     gl.Context
	shared *Shared
	isES   bool
	// cellsState draws grids of character cells; see cells.go.
	cellsState cellsState

	vao, vbo, ibo uint32
	drawProg      program
	blurProg      program
	// clipProg is drawProg cutting each quad to the ellipse it carries,
	// for what draws inside a layer clipped to one in place.
	clipProg program

	// verts is the batch being built: vertFloats floats a vertex, four
	// vertices a quad. tex is the texture the batch's images or layers
	// read, bound to unit 1 as it draws, or 0 for none.
	verts []float32
	tex   uint32
	// draws counts draw calls, for tests and benchmarks.
	draws int

	glyphs, lcdGlyphs, colorGlyphs glyphTexture
	images                         map[*paint.Image]*imageTexture
	// bigs holds the masks too big for the atlas; see bigmasks.go.
	bigs bigMasks
	// now is when the frame being drawn started, from clock, or time.Now where clock is nil.
	now   time.Time
	clock func() time.Time
	// scratchX is where the next mask goes in the scratch strip, and scratches counts the masks put there, for tests.
	scratchX, scratches int
	// dual says the draw program blends each channel by a colour of its
	// own, which glyphs on subpixels need.
	dual bool
	// textRendering is how the window draws text, and subpixels says
	// its glyphs may use the panel's subpixels: it asks for them, its
	// surface is opaque, and the program blends by channel. gamma holds
	// the ratios the shader corrects coverage by.
	textRendering text.Rendering
	subpixels     bool
	gamma         [4]float32

	// layers holds one offscreen target per nesting depth, reused from
	// frame to frame and resized with the window. layers[0] is the
	// canvas: the frame is drawn there and copied to the window.
	//
	// The canvas keeps the last frame, so a frame redraws only the part
	// that changed, and the copy puts the whole of it on screen. A
	// Backdrop needs it too: it reads what has been drawn so far, and
	// the window's own framebuffer cannot be read back.
	layers []target
	// canvasOK says the canvas holds the last frame, at canvasScale.
	canvasOK    bool
	canvasScale float32
	// direct is set for a frame drawn straight to the window, which is
	// quicker when most of it changed: it saves the copy. big says the
	// frame before changed most of the window too.
	direct, big bool
	// Redrawn is the device-pixel area the last frame redrew, for
	// tests.
	Redrawn geom.Rect
	// Stretched is the logical-pixel area where the last frame drew a
	// big mask stretched, while its size changed, and SharpAt when a
	// frame draws it sharp. The next frame redraws the area whatever
	// its damage; a driver with no new frame by SharpAt draws its last
	// one again.
	Stretched geom.Rect
	SharpAt   time.Time
	// Under is the window's background, where a frame's first op leaves
	// it uncovered; see driver.Backgrounder.
	Under color.NRGBA
	// Corner is the radius, in device pixels, the window's corners are
	// cut to on the screen, or 0 for square corners, and Edge the width
	// of the edge a cut window leaves for the border its drawn shadow
	// draws; see shape.
	Corner, Edge float32
	// cull is the device-pixel area a frame redraws in part, while its
	// commands are queued, and empty otherwise. A quad wholly outside it,
	// or outside the clip, is left out, since every target shares the
	// window's pixels.
	cull geom.Rect
	// WindowFBO is the framebuffer the window shows: 0, or on Windows
	// the texture DXGI presents. FlipWindow is set for that texture,
	// whose rows Direct3D reads from the top where OpenGL writes them
	// from the bottom, so the canvas goes to it upside down.
	WindowFBO  uint32
	FlipWindow bool
	// blurs holds two scratch targets for each downsampling factor.
	blurs [len(blurFactors)][2]target
	// stack is the layers open, innermost last. depth is the target
	// being drawn into: 0 for the canvas, or the window for a frame
	// drawn straight to it, and layers[depth] beneath. clip is the
	// device-pixel box drawing is scissored to, with its origin at the
	// top left.
	stack []openLayer
	depth int
	clip  geom.Rect
	// ellipse is the ellipse a layer clips to in place, which every quad
	// drawn meanwhile carries; see clipEllipse.
	ellipse clipEllipse
	// ramps holds the colours of gradients with stops; see ramps.go.
	ramps ramps
	// scenes draws 3D scenes; see scenes.go.
	scenes sceneState

	fbW, fbH int
	scale    float32
}

// target is an offscreen texture and the framebuffer that draws to it.
type target struct {
	tex, fbo uint32
	w, h     int
}

// program is a linked shader.
type program struct{ id uint32 }

// openLayer is a layer being drawn: the op that opened it, whether it
// draws in place, straight into the target around it, and the clip
// around it.
type openLayer struct {
	op      *paint.LayerOp
	inPlace bool
	clip    geom.Rect
	ellipse clipEllipse
}

// clipEllipse is an ellipse that quads are cut to as they draw, with
// its centre and its radii across and down in device pixels from the
// target's top left. A zero one cuts nothing.
type clipEllipse struct {
	on          bool
	centre, rad geom.Point
}

// Each vertex is vertFloats floats, in nine attributes of two or four:
//
//	a_pos    where the vertex lands, in normalized device coordinates
//	a_local  the point in the shape's own space, for its distance field
//	a_rect   the shape's rectangle in its own space
//	a_param  corner radius, stroke width, kind, and a flag or the
//	         gradient's mode; for a glyph, its subpixel order and
//	         contrast in place of the first two. The kind is plus
//	         kindAdd for a quad that adds its light
//	a_color0 the fill, the shadow's colour or the glyph's; for an image
//	         or a layer, the opacity in alpha
//	a_color1 the gradient's end colour, or a glyph's gamma ratios
//	a_extra  the shadow's offset, blur and spread; or texture
//	         coordinates, then the point on a gradient
//	a_stroke the stroke's colour
//	a_clip   the vertex on the ellipse it is cut to, where the ellipse
//	         is the unit circle, times the depth; 1 where there is one;
//	         and the depth, 1 for a flat quad
const vertFloats = 32

// The kinds of quad, in a_param.z.
const (
	kindShape = iota
	kindShadow
	kindGlyph
	kindImage
	kindLayer
	kindInset
)

// kindAdd, added to a quad's kind, has it add its colour to what is
// beneath, for [paint.BlendAdd]. The blending stays as it is, so the
// quad shares a batch with the ones around it: under premultiplied
// blending, a colour of alpha 0 adds itself and hides nothing, so the
// shader outputs the quad's colour with alpha 0. Where the program
// blends by channel it hides nothing in any channel either.
const kindAdd = 8

// The gradient modes, in a_param.w: two colours, a_color0 to a_color1,
// along a line or out in circles, and from gradRamp a row of the ramp
// texture, gradRamp + 2*row, plus one for a radial gradient.
const (
	gradLinear = 1
	gradRadial = 2
	gradRamp   = 3
)

// maxQuads is the most quads one draw call carries: as many as 16-bit
// indices reach.
const maxQuads = 1 << 14

var attribs = [...]struct {
	name string
	size int32
}{
	{"a_pos", 2}, {"a_local", 2}, {"a_rect", 4}, {"a_param", 4},
	{"a_color0", 4}, {"a_color1", 4}, {"a_extra", 4}, {"a_stroke", 4},
	{"a_clip", 4},
}

const vertexShader = `
in vec2 a_pos;
in vec2 a_local;
in vec4 a_rect;
in vec4 a_param;
in vec4 a_color0;
in vec4 a_color1;
in vec4 a_extra;
in vec4 a_stroke;
in vec4 a_clip;
out vec2 v_local;
flat out vec4 v_rect;
flat out vec4 v_param;
flat out vec4 v_color0;
flat out vec4 v_color1;
out vec4 v_extra;
flat out vec4 v_stroke;
out vec4 v_clip;
// v_uv is where this point falls in a texture the size of the target,
// for passes that read one. It comes from the position the quad is
// drawn at, which holds for any target; gl_FragCoord flips under some
// drivers when one program draws both offscreen and to the window.
out vec2 v_uv;

void main() {
	v_local = a_local;
	v_rect = a_rect;
	v_param = a_param;
	v_color0 = a_color0;
	v_color1 = a_color1;
	v_extra = a_extra;
	v_stroke = a_stroke;
	v_clip = a_clip;
	v_uv = a_pos * 0.5 + 0.5;
	// a_clip.w is the quad's depth at the vertex, 1 where it is flat.
	gl_Position = vec4(a_pos * a_clip.w, 0.0, a_clip.w);
}
`

const sdfFunc = `
float sdRRect(vec2 p, vec4 rect, float r) {
	vec2 c = (rect.xy + rect.zw) * 0.5;
	vec2 halfSize = (rect.zw - rect.xy) * 0.5;
	r = clamp(r, 0.0, min(halfSize.x, halfSize.y));
	vec2 q = abs(p - c) - halfSize + r;
	return length(max(q, 0.0)) + min(max(q.x, q.y), 0.0) - r;
}

// coverage turns a distance into antialiased coverage, one device pixel
// wide whatever the transform.
float coverage(float d) {
	float w = max(fwidth(d), 1e-4);
	return clamp(0.5 - d / w, 0.0, 1.0);
}

vec4 premul(vec4 c) { return vec4(c.rgb * c.a, c.a); }

// ellipseCover returns the coverage of the unit circle at q, a point
// in a space that stretches an ellipse into it.
float ellipseCover(vec2 q) { return coverage(length(q) - 1.0); }
`

// drawOut declares the draw program's outputs: its colour and, where
// the program blends by channel, how much of what is behind each
// channel of it hides.
const drawOut = `
#ifdef DUAL
layout(location = 0, index = 0) out vec4 fragColor;
layout(location = 0, index = 1) out vec4 fragCover;
#else
out vec4 fragColor;
#endif
`

const drawShader = `
in vec2 v_local;
flat in vec4 v_rect;
flat in vec4 v_param;
flat in vec4 v_color0;
flat in vec4 v_color1;
in vec4 v_extra;
flat in vec4 v_stroke;
in vec4 v_clip;
in vec2 v_uv;
uniform sampler2D u_atlas;
uniform sampler2D u_ramp;

// gradient returns a gradient's colour at g, which runs from 0 to 1
// along a linear one in g.x, and out from the centre of a radial one in
// length(g). mode says which, and where the colours are: gradLinear and
// gradRadial blend c0 to c1, and from gradRamp a row of the ramp holds
// them.
vec4 gradient(float mode, vec2 g, vec4 c0, vec4 c1) {
	int m = int(mode + 0.5);
	bool radial = m == 2 || m >= 3 && (m - 3) % 2 == 1;
	float t = clamp(radial ? length(g) : g.x, 0.0, 1.0);
	if (m < 3) {
		return mix(c0, c1, t);
	}
	float row = float((m - 3) / 2);
	return texture(u_ramp, vec2((t * 255.0 + 0.5) / 256.0, (row + 0.5) / 256.0));
}
uniform sampler2D u_lcd;
uniform sampler2D u_color;
uniform sampler2D u_tex;

// glyph returns a glyph's colour, with its coverage enhanced by the
// contrast in v_param.y and corrected for gamma by the ratios in
// v_color1. v_param.x is 0 for a greyscale glyph, 1 or 2 for one
// on subpixels that run red to blue or blue to red, and 3 for a colour
// glyph, whose own colours only fade with the text's alpha, and 4 for a
// greyscale mask read from u_tex in place of the atlas; cover gets the
// coverage of each channel.
vec4 glyph(out vec4 cover) {
	vec4 c = v_color0;
	vec4 g = v_color1;
	float k = v_param.y;
	if (v_param.x > 2.5 && v_param.x < 3.5) {
		vec4 col = texture(u_color, v_extra.xy) * c.a;
		cover = vec4(col.a);
		return col;
	}
	if (v_param.x < 0.5 || v_param.x > 3.5) {
		if (v_param.w > 0.5) {
			// A mask coloured by a gradient.
			c = gradient(v_param.w, v_extra.zw, c, c);
		}
		// A mask too big for the atlas has a texture of its own.
		float a = v_param.x > 3.5 ? texture(u_tex, v_extra.xy).r : texture(u_atlas, v_extra.xy).r;
		// The contrast applies to dark text and fades out for light.
		k *= clamp(4.0 * (0.75 - dot(c.rgb, vec3(0.30, 0.59, 0.11))), 0.0, 1.0);
		a = a * (k + 1.0) / (a * k + 1.0);
		float f = dot(c.rgb, vec3(0.25, 0.5, 0.25));
		a = clamp(a + a * (1.0 - a) * ((g.x * f + g.y) * a + (g.z * f + g.w)), 0.0, 1.0);
		vec4 col = premul(c) * a;
		cover = vec4(col.a);
		return col;
	}
	vec3 m = texture(u_lcd, v_extra.xy).rgb;
	if (v_param.x > 1.5) {
		m = m.bgr;
	}
	m = m * (k + 1.0) / (m * k + 1.0);
	m = clamp(m + m * (1.0 - m) * ((g.x * c.rgb + g.y) * m + (g.z * c.rgb + g.w)), 0.0, 1.0) * c.a;
	cover = vec4(m, max(m.r, max(m.g, m.b)));
	return vec4(c.rgb * m, cover.a);
}

vec4 shade(out vec4 cover) {
	// The kind, less kindAdd.
	int kind = int(v_param.z + 0.5) & 7;
	if (kind == 2) {
		return glyph(cover);
	}
	vec4 col;
	if (kind == 1) {
		// A shadow: the shape's distance field, offset, spread and
		// softened.
		float spread = v_extra.w;
		vec4 r = v_rect + vec4(-spread, -spread, spread, spread);
		float d = sdRRect(v_local - v_extra.xy, r, v_param.x + spread);
		float blur = max(v_extra.z, 0.5);
		col = premul(v_color0) * (1.0 - smoothstep(-blur, blur, d));
		if (v_param.y > 0.5) {
			// Kept outside the shape, as CSS's box-shadow is.
			col *= 1.0 - coverage(sdRRect(v_local, v_rect, v_param.x));
		}
	} else if (kind == 3) {
		float cov;
		if (v_param.y > 0.5) {
			vec2 hs = (v_rect.zw - v_rect.xy) * 0.5;
			cov = ellipseCover((v_local - v_rect.xy - hs) / hs);
		} else {
			cov = coverage(sdRRect(v_local, v_rect, v_param.x));
		}
		col = texture(u_tex, v_extra.xy) * v_color0.a * cov;
	} else if (kind == 4) {
		float cov = 1.0;
		if (v_param.w > 0.5 && v_param.y > 0.5) {
			vec2 hs = (v_rect.zw - v_rect.xy) * 0.5;
			cov = ellipseCover((v_local - v_rect.xy - hs) / hs);
		} else if (v_param.w > 0.5) {
			cov = coverage(sdRRect(v_local, v_rect, v_param.x));
		}
		// A faded layer fades out toward its bounds' edges, over the
		// widths in v_extra: left, top, right and bottom.
		vec4 f = v_extra;
		if (f.x > 0.0) cov *= clamp((v_local.x - v_rect.x) / f.x, 0.0, 1.0);
		if (f.y > 0.0) cov *= clamp((v_local.y - v_rect.y) / f.y, 0.0, 1.0);
		if (f.z > 0.0) cov *= clamp((v_rect.z - v_local.x) / f.z, 0.0, 1.0);
		if (f.w > 0.0) cov *= clamp((v_rect.w - v_local.y) / f.w, 0.0, 1.0);
		col = texture(u_tex, v_uv) * v_color0.a * cov;
	} else if (kind == 5) {
		// An inset shadow: inside the shape, where the shape moved by
		// the offset and shrunk by the spread leaves it, softened.
		float spread = v_extra.w;
		vec4 r = v_rect + vec4(spread, spread, -spread, -spread);
		float d = sdRRect(v_local - v_extra.xy, r, max(v_param.x - spread, 0.0));
		float blur = max(v_extra.z, 0.5);
		float inside = coverage(sdRRect(v_local, v_rect, v_param.x));
		col = premul(v_color0) * smoothstep(-blur, blur, d) * inside;
	} else {
		float d = sdRRect(v_local, v_rect, v_param.x);
		vec4 fill = v_color0;
		if (v_param.w > 0.5) {
			fill = gradient(v_param.w, v_extra.zw, v_color0, v_color1);
		}
		col = premul(fill) * coverage(d);
		float sw = v_param.y;
		if (sw > 0.0) {
			vec4 s = premul(v_stroke) * coverage(abs(d) - sw * 0.5);
			col = s + col * (1.0 - s.a);
		}
	}
	cover = vec4(col.a);
	return col;
}

void main() {
	vec4 cover;
	vec4 col = shade(cover);
#ifdef ELLIPSE
	// Cut to the ellipse a layer clips to in place.
	float c = ellipseCover(v_clip.xy / v_clip.w);
	col *= c;
	cover *= c;
#endif
	if (int(v_param.z + 0.5) >= 8) {
		// Added light: its colour, hiding nothing beneath it.
		col.a = 0.0;
		cover = vec4(0.0);
	}
	fragColor = col;
#ifdef DUAL
	fragCover = cover;
#endif
}
`

// New returns a renderer for a window's GL context g, OpenGL ES when
// isES, to be used on the thread that context is current on. Its
// programs come from sh, built on first use; its buffers are its own.
func New(g gl.Context, isES bool, sh *Shared) (*Renderer, error) {
	r := &Renderer{GL: g, shared: sh, isES: isES, images: map[*paint.Image]*imageTexture{}}
	var err error
	if r.drawProg, r.clipProg, r.blurProg, r.dual, err = sh.programs(g, isES); err != nil {
		return nil, err
	}

	r.vao = g.CreateVertexArray()
	g.BindVertexArray(r.vao)
	r.vbo = g.CreateBuffer()
	g.BindBuffer(gl.ARRAY_BUFFER, r.vbo)
	off := 0
	for i, a := range attribs {
		g.EnableVertexAttribArray(uint32(i))
		g.VertexAttribPointer(uint32(i), a.size, gl.FLOAT, false, vertFloats*4, off)
		off += int(a.size) * 4
	}
	// Every batch uses the same two triangles per quad.
	idx := make([]byte, 0, maxQuads*6*2)
	for q := range maxQuads {
		b := uint16(q * 4)
		for _, i := range [6]uint16{b, b + 1, b + 2, b, b + 2, b + 3} {
			idx = binary.LittleEndian.AppendUint16(idx, i)
		}
	}
	r.ibo = g.CreateBuffer()
	g.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, r.ibo)
	g.BufferInit(gl.ELEMENT_ARRAY_BUFFER, len(idx), glStaticDraw)
	g.BufferSubData(gl.ELEMENT_ARRAY_BUFFER, 0, idx)

	r.initGlyphs()
	r.Rebind()
	return r, nil
}

// Rebind sets the blending every frame takes as given, for a driver
// that has drawn with the context itself since the last frame.
func (r *Renderer) Rebind() {
	g := r.GL
	g.Enable(gl.BLEND)
	if r.dual {
		g.BlendFuncSeparate(gl.ONE, glOneMinusSrc1Color, gl.ONE, glOneMinusSrc1Alpha)
	} else {
		g.BlendFuncSeparate(gl.ONE, gl.ONE_MINUS_SRC_ALPHA, gl.ONE, gl.ONE_MINUS_SRC_ALPHA)
	}
}

// SetText sets how the renderer draws text, for a window whose surface
// blends with what is behind it when transparent.
func (r *Renderer) SetText(tr text.Rendering, transparent bool) {
	r.textRendering = tr
	r.subpixels = tr.Smoothing.Subpixel() && r.dual && !transparent
	r.gamma = ratiosFor(tr.Gamma)
}

// buildPrograms compiles and links the shared programs and points
// their samplers at their texture units: the glyph atlas on unit 0, an
// image, a layer or a blur's source on unit 1, the subpixel glyph atlas
// on unit 2, colour glyphs on unit 3, and the gradients' ramp on unit
// 6. The draw programs blend by channel where the context has
// dual-source blending, and dual says so. clip is the draw program that
// cuts what it draws to an ellipse; the plain one leaves that work out
// of every other pixel.
func buildPrograms(g gl.Context, isES bool) (draw, clip, blur program, dual bool, err error) {
	header, dualHeader := "#version 150\n", "#version 330\n#define DUAL\n"
	if isES {
		header = "#version 300 es\nprecision highp float;\n"
		dualHeader = "#version 300 es\n#extension GL_EXT_blend_func_extended : require\nprecision highp float;\n" +
			"#define DUAL\n"
	}
	draw, err = link(g, dualHeader+vertexShader, dualHeader+sdfFunc+drawOut+drawShader)
	dual = err == nil && !noDual
	if !dual {
		if draw.id != 0 {
			g.DeleteProgram(draw.id)
		}
		dualHeader = header
		if draw, err = link(g, header+vertexShader, header+sdfFunc+drawOut+drawShader); err != nil {
			return program{}, program{}, program{}, false, err
		}
	}
	ellipse := dualHeader + "#define ELLIPSE\n"
	if clip, err = link(g, ellipse+vertexShader, ellipse+sdfFunc+drawOut+drawShader); err != nil {
		return program{}, program{}, program{}, false, err
	}
	for _, p := range []program{draw, clip} {
		g.UseProgram(p.id)
		g.Uniform1i(g.GetUniformLocation(p.id, "u_atlas"), 0)
		g.Uniform1i(g.GetUniformLocation(p.id, "u_tex"), 1)
		g.Uniform1i(g.GetUniformLocation(p.id, "u_lcd"), 2)
		g.Uniform1i(g.GetUniformLocation(p.id, "u_color"), 3)
		g.Uniform1i(g.GetUniformLocation(p.id, "u_ramp"), 6)
	}
	if blur, err = link(g, header+vertexShader, header+blurShader); err != nil {
		return program{}, program{}, program{}, false, err
	}
	g.UseProgram(blur.id)
	g.Uniform1i(g.GetUniformLocation(blur.id, "u_tex"), 1)
	return draw, clip, blur, dual, nil
}

// noDual is set by GUNIM_NO_DUAL_SOURCE=1, which draws as a context
// without dual-source blending would, with greyscale text.
var noDual = os.Getenv("GUNIM_NO_DUAL_SOURCE") == "1"

// link compiles and links a program, with the attributes at the
// locations the vertex layout gives them.
func link(g gl.Context, vs, fs string) (program, error) {
	v, err := compile(g, gl.VERTEX_SHADER, vs)
	if err != nil {
		return program{}, err
	}
	defer g.DeleteShader(v)
	f, err := compile(g, gl.FRAGMENT_SHADER, fs)
	if err != nil {
		return program{}, err
	}
	defer g.DeleteShader(f)

	id := g.CreateProgram()
	g.AttachShader(id, v)
	g.AttachShader(id, f)
	for i, a := range attribs {
		g.BindAttribLocation(id, uint32(i), a.name)
	}
	g.LinkProgram(id)
	if g.GetProgrami(id, gl.LINK_STATUS) == gl.FALSE {
		defer g.DeleteProgram(id)
		return program{}, fmt.Errorf("desktop: link shader: %s", g.GetProgramInfoLog(id))
	}
	return program{id: id}, nil
}

func compile(g gl.Context, kind uint32, src string) (uint32, error) {
	s := g.CreateShader(kind)
	g.ShaderSource(s, src)
	g.CompileShader(s)
	if g.GetShaderi(s, gl.COMPILE_STATUS) == gl.FALSE {
		defer g.DeleteShader(s)
		return 0, fmt.Errorf("desktop: compile shader: %s", g.GetShaderInfoLog(s))
	}
	return s, nil
}

// Canvas returns the framebuffer the frame is drawn in, the right way
// up, for reading a frame back.
func (r *Renderer) Canvas() uint32 { return r.layers[0].fbo }

// Release frees the renderer's GL objects. The programs belong to every
// window and stay.
func (r *Renderer) Release() {
	g := r.GL
	for _, t := range r.layers {
		g.DeleteFramebuffer(t.fbo)
		g.DeleteTexture(t.tex)
	}
	for _, pair := range r.blurs {
		for _, t := range pair {
			g.DeleteFramebuffer(t.fbo)
			g.DeleteTexture(t.tex)
		}
	}
	for m := range r.images {
		r.dropImage(m)
	}
	g.DeleteTexture(r.glyphs.tex)
	if r.ramps.tex != 0 {
		g.DeleteTexture(r.ramps.tex)
	}
	r.releaseScenes()
	r.releaseBigMasks()
	if r.lcdGlyphs.tex != 0 {
		g.DeleteTexture(r.lcdGlyphs.tex)
	}
	if r.colorGlyphs.tex != 0 {
		g.DeleteTexture(r.colorGlyphs.tex)
	}
	r.releaseCells()
	g.DeleteBuffer(r.vbo)
	g.DeleteBuffer(r.ibo)
	g.DeleteVertexArray(r.vao)
}

// Draw replays ops into the canvas, within damage, the logical-pixel
// area that changed since the last frame, and copies the canvas to the
// window.
func (r *Renderer) Draw(ops []paint.Op, damage geom.Rect, fbW, fbH int, scale float32) {
	if fbW <= 0 || fbH <= 0 {
		return
	}
	g := r.GL
	r.fbW, r.fbH, r.scale = fbW, fbH, scale
	r.now = time.Now()
	if r.clock != nil {
		r.now = r.clock()
	}
	// What the last frame drew stretched is drawn again, to be sharp.
	if !r.Stretched.Empty() {
		damage = damage.Union(r.Stretched)
		r.Stretched, r.SharpAt = geom.Rect{}, time.Time{}
	}
	r.bigs.newFrame()
	r.stack, r.depth = r.stack[:0], 0
	r.ellipse = clipEllipse{}
	// bindDraw, below, makes the plain draw program current.
	r.scratchX = 0
	if len(r.layers) == 0 {
		r.layers = append(r.layers, target{})
	}
	backdrop := false
	for _, op := range ops {
		// A blur spreads a change past its bounds, and a backdrop reads
		// what the canvas holds.
		if l, ok := op.(*paint.LayerOp); ok && (l.Opts.Backdrop > 0 || l.Opts.Blur > 0) {
			damage = paint.Everything
			backdrop = backdrop || l.Opts.Backdrop > 0
		}
	}
	box := r.deviceBox(damage)
	window := geom.Rect{Max: geom.Pt(float32(fbW), float32(fbH))}
	// A frame that changed more than half the window, after one that
	// did too, goes straight to it, unless it needs the canvas for a
	// backdrop. The canvas then falls out of date, and the next frame
	// that draws there draws all of it. After a small frame the canvas
	// is kept, since big frames between small ones, as an animation's
	// between the breaths of a mark beside it, would each leave the
	// next small one to draw the whole window.
	s, ws := box.Size(), window.Size()
	big := s.W*s.H > ws.W*ws.H/2
	r.direct = !backdrop && !r.FlipWindow && big && r.big && r.Corner == 0
	r.big = big
	if r.fit(&r.layers[0]) || !r.canvasOK || scale != r.canvasScale {
		if !r.direct {
			box = window
		}
	}
	r.Redrawn = box
	if r.direct {
		// Straight to the window, which holds nothing after a swap.
		box = window
		r.canvasOK = false
	}
	g.BindFramebuffer(gl.FRAMEBUFFER, r.fbo(0))
	g.Viewport(0, 0, int32(fbW), int32(fbH))
	r.bindDraw()
	bg, alpha := background(ops)
	if alpha == 0 && r.Under.A > 0 {
		bg, alpha = rgba(r.Under), float32(r.Under.A)/0xff
	}
	if r.direct {
		clearWindow(g, bg, alpha)
	}
	if !box.Empty() {
		r.setClip(box)
		g.Clear(glColorBufferBit)
		g.ClearColor(0, 0, 0, 0)
		// What lies outside the box, or outside the clip it is drawn
		// under, is dropped before it reaches the GPU: on a whole frame
		// too, as a long list in a scroll sends thousands of glyphs the
		// clip would only have cut. A layer draws into a target the
		// window's size, in the window's pixels, and anything it blurs
		// was cut to the clip first, so the test holds inside one too.
		r.cull = box
		r.replay(ops)
		r.flush()
		r.cull = geom.Rect{}
		r.setClip(window)
	}
	if !r.direct {
		r.canvasOK, r.canvasScale = true, scale
		g.BindFramebuffer(gl.FRAMEBUFFER, r.WindowFBO)
		if r.Corner > 0 {
			// Transparent outside the rounded corners, the background inside them
			g.Clear(glColorBufferBit)
			if alpha > 0 {
				c := [4]float32{bg[0], bg[1], bg[2], alpha}
				rect, radius := r.shape()
				r.quad(corners(r.window(), geom.Rect{}), paint.Identity, r.scale, &look{
					rect: rect, radius: radius, kind: kindShape, color0: c, color1: c,
				})
			}
		} else {
			clearWindow(g, bg, alpha)
			g.Clear(glColorBufferBit)
			g.ClearColor(0, 0, 0, 0)
		}
		r.present(r.layers[0].tex)
		r.flush()
	}
	r.evictImages()
	r.evictBigMasks()
	r.evictMeshes()
}

// debugClear is set by GUNIM_DEBUG_CLEAR=1, which clears the window to
// magenta before each frame, so a part of the window no frame covers
// shows. It is for finding where a stray band of colour comes from.
var debugClear = os.Getenv("GUNIM_DEBUG_CLEAR") == "1"

// clearWindow sets the colour the window's framebuffer clears to: bg
// at alpha, premultiplied as the window blends, which is the frame's
// background when it has an opaque one, else the window's own, or
// transparent with alpha zero; or magenta under GUNIM_DEBUG_CLEAR.
// Offscreen targets clear to transparent, and the caller sets that back
// after the clear.
func clearWindow(g gl.Context, bg [4]float32, alpha float32) {
	switch {
	case debugClear:
		g.ClearColor(1, 0, 1, 1)
	case alpha > 0:
		g.ClearColor(bg[0]*alpha, bg[1]*alpha, bg[2]*alpha, alpha)
	}
}

// background returns the colour of a frame's background, and 1, or 0
// when it has none: its first op,
// when that is a plain opaque rectangle from the window's top left
// corner, as a window's surface paints, that does not add its light.
// A frame drawn for a smaller window than the buffer holds leaves a
// strip the clear fills, and the background colour makes that strip
// look like the window's own.
func background(ops []paint.Op) (bg [4]float32, alpha float32) {
	if len(ops) == 0 {
		return [4]float32{}, 0
	}
	op, ok := ops[0].(*paint.RRectOp)
	if !ok || op.Radius != 0 || op.Transform != paint.Identity || op.Fill.Gradient != nil ||
		op.Fill.Solid.A != 0xff || op.Shadow.Color.A != 0 || op.Stroke.Width > 0 || op.Blend == paint.BlendAdd ||
		op.Rect.Min.X > 0 || op.Rect.Min.Y > 0 {
		return [4]float32{}, 0
	}
	return rgba(op.Fill.Solid), 1
}

// present queues the canvas's copy to the window, upside down for a
// window that reads its rows from the top.
func (r *Renderer) present(canvas uint32) {
	if !r.FlipWindow {
		r.composite(canvas, nil, 1, false, 0, false)
		return
	}
	r.uses(canvas)
	win := r.window()
	// Drawn as an image, whose texture coordinates are its own: the top
	// of the window takes the texture's first row, where a layer's copy
	// would take its last.
	uv := geom.Rect{Max: geom.Pt(1, 1)}
	rect, radius := win, float32(0)
	if r.Corner > 0 {
		rect, radius = r.shape()
	}
	r.quad(corners(win, uv), paint.Identity, r.scale, &look{
		rect: rect, radius: radius, kind: kindImage, color0: [4]float32{0, 0, 0, 1},
	})
}

// shape is the part of a window with cut corners that shows its frame, in logical pixels: the window less its edge,
// with corners that much tighter.
func (r *Renderer) shape() (rect geom.Rect, radius float32) {
	w := r.window()
	e := r.Edge / r.scale
	return geom.Rect{Min: geom.Pt(w.Min.X+e, w.Min.Y+e), Max: geom.Pt(w.Max.X-e, w.Max.Y-e)}, max(r.Corner-r.Edge, 0) / r.scale
}

// deviceBox turns damage in logical pixels into the whole device pixels
// it touches, within the window.
func (r *Renderer) deviceBox(d geom.Rect) geom.Rect {
	x0 := max(0, float32(math.Floor(float64(d.Min.X*r.scale)))-1)
	y0 := max(0, float32(math.Floor(float64(d.Min.Y*r.scale)))-1)
	x1 := min(float32(r.fbW), float32(math.Ceil(float64(d.Max.X*r.scale)))+1)
	y1 := min(float32(r.fbH), float32(math.Ceil(float64(d.Max.Y*r.scale)))+1)
	if x1 <= x0 || y1 <= y0 || d.Empty() {
		return geom.Rect{}
	}
	return geom.Rect{Min: geom.Pt(x0, y0), Max: geom.Pt(x1, y1)}
}

// replay queues ops into the canvas.
func (r *Renderer) replay(ops []paint.Op) {
	for _, op := range ops {
		if _, ok := op.(*paint.CellsOp); !ok && r.cellsState.pending != nil {
			// The cells first, with the glyphs that spill out of them,
			// beneath whatever comes after them, as a cursor.
			r.flush()
		}
		switch op := op.(type) {
		case *paint.RRectOp:
			r.rrect(op)
		case *paint.LayerOp:
			r.openLayer(op)
		case *paint.LayerEndOp:
			r.closeLayer()
		case *paint.TextOp:
			r.text(op)
		case *paint.ImageOp:
			r.image(op)
		case *paint.MaskOp:
			r.mask(op)
		case *paint.CellsOp:
			r.cells(op)
		case *paint.SceneOp:
			r.scene(op)
		}
	}
	// A layer left open by a node that forgot to close it still shows.
	for len(r.stack) > 0 {
		r.closeLayer()
	}
}

// Stats counts what frames send the GPU, while GUNIM_DEBUG_FRAMES is
// set: draw calls, quads, the bytes of their vertices, and layers.
type Stats struct {
	Flushes, Quads, Bytes, Layers int
	// FlushTime is spent handing vertices and draws to the driver.
	FlushTime time.Duration
}

// framesDebug is set by GUNIM_DEBUG_FRAMES, which has the renderer
// count what each frame sends the GPU in [Renderer.Stats].
var framesDebug = os.Getenv("GUNIM_DEBUG_FRAMES") != ""

// bindDraw makes the draw program and the renderer's vertices current,
// with the glyph atlas on unit 0.
func (r *Renderer) bindDraw() {
	g := r.GL
	r.useDraw()
	g.BindVertexArray(r.vao)
	g.BindBuffer(gl.ARRAY_BUFFER, r.vbo)
	g.ActiveTexture(gl.TEXTURE0)
	g.BindTexture(gl.TEXTURE_2D, r.glyphs.tex)
	if r.ramps.tex != 0 {
		g.ActiveTexture(glTexture6)
		g.BindTexture(gl.TEXTURE_2D, r.ramps.tex)
		g.ActiveTexture(gl.TEXTURE0)
	}
}

// useDraw makes current the draw program for the clip in force: the one
// that cuts to an ellipse while a layer clips to one in place. The batch
// queued must be drawn first.
func (r *Renderer) useDraw() {
	if r.ellipse.on {
		r.GL.UseProgram(r.clipProg.id)
		return
	}
	r.GL.UseProgram(r.drawProg.id)
}

// flush draws the batch, and the rows of cells queued before it.
func (r *Renderer) flush() {
	r.flushCells()
	n := len(r.verts) / (4 * vertFloats)
	if n == 0 {
		return
	}
	g := r.GL
	if r.tex != 0 {
		g.ActiveTexture(glTexture1)
		g.BindTexture(gl.TEXTURE_2D, r.tex)
		g.ActiveTexture(gl.TEXTURE0)
	}
	data := unsafe.Slice((*byte)(unsafe.Pointer(&r.verts[0])), len(r.verts)*4)
	// A fresh store each time lets the driver keep the last one for the
	// draw still reading it.
	var t0 time.Time
	if framesDebug {
		t0 = time.Now()
	}
	g.BufferInit(gl.ARRAY_BUFFER, len(data), gl.STREAM_DRAW)
	g.BufferSubData(gl.ARRAY_BUFFER, 0, data)
	g.DrawElements(gl.TRIANGLES, int32(n*6), glUnsignedShort, 0)
	r.draws++
	if framesDebug {
		r.Stats.Flushes++
		r.Stats.Quads += n
		r.Stats.Bytes += len(data)
		r.Stats.FlushTime += time.Since(t0)
	}
	r.verts = r.verts[:0]
	r.tex = 0
}

// uses readies the batch for a quad that reads tex on unit 1, drawing
// what is queued first when it reads another texture or is full.
func (r *Renderer) uses(tex uint32) {
	if len(r.verts)/(4*vertFloats) >= maxQuads || (tex != 0 && r.tex != 0 && r.tex != tex) {
		r.flush()
	}
	if tex != 0 {
		r.tex = tex
	}
}

// quadVert is one corner of a quad: the point in the shape's own space
// and, for images and glyphs, its texture coordinates.
type quadVert struct {
	local geom.Point
	uv    geom.Point
}

// gradAt returns where p, in the gradient's own space, falls on it: in
// x along a linear one, from 0 at From to 1 at To, and for a radial one
// the point moved from From and scaled by the distance to To, whose
// length is the place. An ellipse's point is taken along the line to To
// and across it, the part across scaled by its Aspect as well.
func gradAt(gr *paint.Gradient, p geom.Point) geom.Point {
	d, v := gr.To.Sub(gr.From), p.Sub(gr.From)
	if gr.Radial {
		rad := float32(math.Hypot(float64(d.X), float64(d.Y)))
		if rad <= 0 {
			return geom.Pt(1, 0)
		}
		if gr.Aspect <= 0 || gr.Aspect == 1 {
			return geom.Pt(v.X/rad, v.Y/rad)
		}
		along := (v.X*d.X + v.Y*d.Y) / (rad * rad)
		across := (d.X*v.Y - d.Y*v.X) / (rad * rad * gr.Aspect)
		return geom.Pt(along, across)
	}
	n := d.X*d.X + d.Y*d.Y
	if n <= 0 {
		return geom.Point{}
	}
	return geom.Pt((v.X*d.X+v.Y*d.Y)/n, 0)
}

// look is what every pixel of a quad shares.
type look struct {
	rect           geom.Rect
	radius, stroke float32
	kind           int
	flag           bool
	// add has the quad add its colour to what is beneath; see kindAdd.
	add            bool
	color0, color1 [4]float32
	extra          [4]float32
	strokeColor    [4]float32
	// grad is the gradient the quad is coloured by, in mode, a
	// gradient mode, with toGrad taking a corner's local point into the
	// gradient's space.
	grad   *paint.Gradient
	mode   float32
	toGrad func(geom.Point) geom.Point
}

// quad queues a quad with corners in the shape's own space, placed
// through t. Glyph quads arrive already in device pixels, with t the
// identity and scale 1.
func (r *Renderer) quad(corners [4]quadVert, t paint.Transform, scale float32, l *look) {
	var at [4]geom.Point
	for i, c := range corners {
		at[i] = t.Apply(c.local)
	}
	if !r.cull.Empty() {
		lo, hi := at[0], at[0]
		for _, p := range at[1:] {
			lo = geom.Pt(min(lo.X, p.X), min(lo.Y, p.Y))
			hi = geom.Pt(max(hi.X, p.X), max(hi.Y, p.Y))
		}
		if r.outside(geom.Rect{Min: lo.Mul(scale), Max: hi.Mul(scale)}) {
			return
		}
	}
	r.emit(corners, at, [4]float32{1, 1, 1, 1}, scale, l)
}

// emit queues a quad with corners in the shape's own space, shown at
// at, in logical pixels of the target, divided by the depths w, which
// are 1 for a flat quad. A quad with depth in it, as a tilted layer's,
// has its points within it spread in perspective.
func (r *Renderer) emit(corners [4]quadVert, at [4]geom.Point, w [4]float32, scale float32, l *look) {
	flag := l.mode
	if l.flag {
		flag = 1
	}
	kind := float32(l.kind)
	if l.add {
		kind += kindAdd
	}
	sx, sy := 2*scale/float32(r.fbW), 2*scale/float32(r.fbH)
	for i, c := range corners {
		p := at[i]
		extra := l.extra
		if l.kind == kindGlyph || l.kind == kindImage {
			extra[0], extra[1] = c.uv.X, c.uv.Y
		}
		if l.grad != nil {
			gp := c.local
			if l.toGrad != nil {
				gp = l.toGrad(gp)
			}
			g := gradAt(l.grad, gp)
			extra[2], extra[3] = g.X, g.Y
		}
		// The point on the ellipse goes multiplied by the depth, which
		// the shader divides out, so it spreads evenly over the target
		// whatever the depth.
		clip := [4]float32{0, 0, 0, w[i]}
		if e := r.ellipse; e.on {
			clip = [4]float32{(p.X*scale - e.centre.X) / e.rad.X * w[i], (p.Y*scale - e.centre.Y) / e.rad.Y * w[i], 1, w[i]}
		}
		r.verts = append(r.verts,
			p.X*sx-1, 1-p.Y*sy,
			c.local.X, c.local.Y,
			l.rect.Min.X, l.rect.Min.Y, l.rect.Max.X, l.rect.Max.Y,
			l.radius, l.stroke, kind, flag,
			l.color0[0], l.color0[1], l.color0[2], l.color0[3],
			l.color1[0], l.color1[1], l.color1[2], l.color1[3],
			extra[0], extra[1], extra[2], extra[3],
			l.strokeColor[0], l.strokeColor[1], l.strokeColor[2], l.strokeColor[3],
			clip[0], clip[1], clip[2], clip[3],
		)
	}
}

// outside reports whether b, in device pixels, misses what a frame drawn in part redraws within the clip.
func (r *Renderer) outside(b geom.Rect) bool {
	c := intersect(r.cull, r.clip)
	return b.Max.X < c.Min.X || b.Min.X > c.Max.X || b.Max.Y < c.Min.Y || b.Min.Y > c.Max.Y
}

// corners returns a rectangle's corners in the order the index buffer
// expects, with texture coordinates spanning uv.
func corners(q, uv geom.Rect) [4]quadVert {
	return [4]quadVert{
		{q.Min, uv.Min},
		{geom.Pt(q.Max.X, q.Min.Y), geom.Pt(uv.Max.X, uv.Min.Y)},
		{q.Max, uv.Max},
		{geom.Pt(q.Min.X, q.Max.Y), geom.Pt(uv.Min.X, uv.Max.Y)},
	}
}

func (r *Renderer) rrect(op *paint.RRectOp) {
	r.uses(0)
	// Every part of an added shape adds, its shadow too, which makes it
	// a halo.
	add := op.Blend == paint.BlendAdd
	// The shadow goes first, underneath, on a quad grown to hold it.
	if sh := op.Shadow; sh.Color.A > 0 {
		grow := sh.Blur + sh.Spread + 2
		l := &look{
			rect: op.Rect, radius: op.Radius, kind: kindShadow, add: add,
			color0: rgba(sh.Color),
			extra:  [4]float32{sh.Offset.X, sh.Offset.Y, sh.Blur, sh.Spread},
		}
		if sh.Outside {
			// The stroke's place carries Outside to the shader: a
			// shadow has no stroke.
			l.stroke = 1
		}
		r.quad(corners(grow4(op.Rect.Add(sh.Offset), grow), geom.Rect{}), op.Transform, r.scale, l)
	}

	l := look{rect: op.Rect, radius: op.Radius, kind: kindShape, add: add, color0: rgba(op.Fill.Solid)}
	l.color1 = l.color0
	empty := l.color0[3] == 0
	if gr := op.Fill.Gradient; gr != nil {
		l.grad, l.mode = gr, r.gradMode(gr)
		if l.mode < gradRamp {
			l.color0, l.color1 = rgba(gr.Start), rgba(gr.End)
		}
		empty = gr.Start.A == 0 && gr.End.A == 0 && !slices.ContainsFunc(gr.Stops, func(s paint.Stop) bool { return s.Color.A > 0 })
	}
	stroked := op.Stroke.Width > 0 && op.Stroke.Color.A > 0
	inset := op.Inset[0].Color.A > 0 || op.Inset[1].Color.A > 0
	if empty && !stroked && !inset {
		return
	}
	grown := corners(grow4(op.Rect, op.Stroke.Width/2+2), geom.Rect{})
	if !inset {
		l.stroke, l.strokeColor = op.Stroke.Width, rgba(op.Stroke.Color)
		r.quad(grown, op.Transform, r.scale, &l)
		return
	}
	// Shaded inside: the fill, the inset shadows over it, and the stroke
	// over them, each a quad of the same batch.
	if !empty {
		r.quad(grown, op.Transform, r.scale, &l)
	}
	for _, sh := range op.Inset {
		if sh.Color.A == 0 {
			continue
		}
		r.quad(corners(grow4(op.Rect, 2), geom.Rect{}), op.Transform, r.scale, &look{
			rect: op.Rect, radius: op.Radius, kind: kindInset, add: add, color0: rgba(sh.Color),
			extra: [4]float32{sh.Offset.X, sh.Offset.Y, sh.Blur, sh.Spread},
		})
	}
	if stroked {
		r.quad(grown, op.Transform, r.scale, &look{rect: op.Rect, radius: op.Radius, kind: kindShape, add: add,
			stroke: op.Stroke.Width, strokeColor: rgba(op.Stroke.Color)})
	}
}

// openLayer starts drawing into a fresh offscreen target, or goes on
// drawing into the current one, scissored, for a layer that can draw
// in place.
func (r *Renderer) openLayer(op *paint.LayerOp) {
	r.flush()
	r.stack = append(r.stack, openLayer{op: op, clip: r.clip, ellipse: r.ellipse})
	if box, ok := r.inPlace(op); ok {
		r.stack[len(r.stack)-1].inPlace = true
		r.setClip(intersect(r.clip, box))
		if op.Opts.Clip && op.Opts.Ellipse {
			b := r.region(op, true)
			rad := geom.Pt((b.Max.X-b.Min.X)/2, (b.Max.Y-b.Min.Y)/2)
			r.ellipse = clipEllipse{on: rad.X > 0 && rad.Y > 0, centre: b.Min.Add(rad), rad: rad}
			r.useDraw()
		}
		return
	}
	// The layer is cut as it is composited, once.
	r.ellipse = clipEllipse{}
	r.useDraw()
	if tilted(op) {
		// A tilted layer draws flat, all of it, and the clip round it
		// cuts it where it shows once it is tilted.
		r.setClip(geom.Rect{Max: geom.Pt(float32(r.fbW), float32(r.fbH))})
	}
	r.depth++
	r.Stats.Layers++
	for len(r.layers) <= r.depth {
		r.layers = append(r.layers, target{})
	}
	t := &r.layers[r.depth]
	r.fit(t)
	g := r.GL
	g.BindFramebuffer(gl.FRAMEBUFFER, t.fbo)
	g.Clear(glColorBufferBit)
}

// inPlace reports whether a layer draws the same straight into the
// target around it as composited from a target of its own, and the
// device-pixel box it clips to: it is opaque, blurs nothing, adds
// nothing, and clips
// to an upright rectangle, to an upright ellipse inside no other, or
// not at all. The quads drawn inside an ellipse carry it, and are cut
// to it as they draw; a grid of cells keeps to its box.
func (r *Renderer) inPlace(op *paint.LayerOp) (geom.Rect, bool) {
	o, t := op.Opts, op.Transform
	switch {
	case o.Opacity < 1 || o.Blur > 0 || o.Backdrop > 0 || o.Fade != (geom.Insets{}) || tilted(op) ||
		o.Blend == paint.BlendAdd:
		return geom.Rect{}, false
	case !o.Clip:
		return r.region(op, false), true
	case t.B != 0 || t.D != 0:
		return geom.Rect{}, false
	case o.Ellipse:
		if r.ellipse.on {
			return geom.Rect{}, false
		}
		b := r.region(op, true)
		return geom.Rect{
			Min: geom.Pt(float32(math.Floor(float64(b.Min.X))), float32(math.Floor(float64(b.Min.Y)))),
			Max: geom.Pt(float32(math.Ceil(float64(b.Max.X))), float32(math.Ceil(float64(b.Max.Y)))),
		}, true
	case o.Radius > 0:
		return geom.Rect{}, false
	}
	b := r.region(op, true)
	round := func(v float32) float32 { return float32(math.Round(float64(v))) }
	return geom.Rect{Min: geom.Pt(round(b.Min.X), round(b.Min.Y)), Max: geom.Pt(round(b.Max.X), round(b.Max.Y))}, true
}

// closeLayer composites the innermost layer into the one around it.
//
// A Backdrop goes first: what the parent holds so far is blurred and
// drawn back over itself within the layer's bounds, rounded when the
// layer clips. The layer's contents go on top, blurred first when the
// layer asks for Blur.
func (r *Renderer) closeLayer() {
	r.flush()
	top := r.stack[len(r.stack)-1]
	r.stack = r.stack[:len(r.stack)-1]
	r.setClip(top.clip)
	if r.ellipse != top.ellipse {
		r.ellipse = top.ellipse
		r.useDraw()
	}
	if top.inPlace {
		return
	}
	depth := r.depth
	r.depth--
	op := top.op
	o := op.Opts
	radius := float32(0)
	if o.Clip {
		radius = o.Radius
	}

	if o.Backdrop > 0 {
		behind := r.blur(r.layers[depth-1].tex, r.region(op, true), o.Backdrop*r.scale)
		r.GL.BindFramebuffer(gl.FRAMEBUFFER, r.fbo(depth-1))
		r.composite(behind, op, o.Opacity, true, radius, false)
		// The next blur at this resolution reuses the texture.
		r.flush()
	}

	contents := r.layers[depth].tex
	if o.Blur > 0 {
		contents = r.blur(contents, r.region(op, o.Clip || tilted(op)), o.Blur*r.scale)
	}
	r.GL.BindFramebuffer(gl.FRAMEBUFFER, r.fbo(depth-1))
	if tilted(op) {
		r.compositeTilted(contents, op, o.Opacity, radius)
	} else {
		r.composite(contents, op, o.Opacity, o.Clip, radius, o.Blend == paint.BlendAdd)
	}
	// The next layer at this depth draws into the same texture.
	r.flush()
}

// composite queues tex, a window-sized texture, drawn into the bound
// target at opacity. With clip it covers op's bounds, rounded by
// radius; without, the whole window. A layer that fades covers its
// bounds too, fading toward their edges. A nil op composites the whole
// window unclipped. With add, tex adds its colours to what is beneath,
// times opacity, and hides nothing.
func (r *Renderer) composite(tex uint32, op *paint.LayerOp, opacity float32, clip bool, radius float32, add bool) {
	r.uses(tex)
	l := look{kind: kindLayer, add: add, color0: [4]float32{0, 0, 0, opacity}}
	fade := op != nil && op.Opts.Fade != (geom.Insets{})
	if clip && op != nil || fade {
		b := op.Opts.Bounds
		l.rect = b
		if clip {
			l.radius, l.flag = radius, true
			if op.Opts.Ellipse {
				l.stroke = 1
			}
		}
		if fade {
			f := op.Opts.Fade
			l.extra = [4]float32{max(f.Left, 0), max(f.Top, 0), max(f.Right, 0), max(f.Bottom, 0)}
		}
		grow := float32(2)
		if !clip {
			// Only the antialiased edge of a clip reaches past the bounds.
			grow = 0
		}
		r.quad(corners(grow4(b, grow), geom.Rect{}), op.Transform, r.scale, &l)
		return
	}
	r.quad(corners(r.window(), geom.Rect{}), paint.Identity, r.scale, &l)
}

// tilted reports whether a layer turns in depth.
func tilted(op *paint.LayerOp) bool { return op.Opts.Tilt.X != 0 || op.Opts.Tilt.Y != 0 }

// compositeTilted queues tex, a window-sized texture holding a tilted
// layer drawn flat, drawn into the bound target at opacity: its Bounds,
// rounded by radius or cut to an ellipse where it clips, turned in
// perspective about their middle. A one-sided layer showing its back
// draws nothing, and nor does one that reaches behind the eye.
func (r *Renderer) compositeTilted(tex uint32, op *paint.LayerOp, opacity, radius float32) {
	o, t := op.Opts, op.Transform
	if o.Tilt.OneSided && !o.Tilt.Facing() {
		return
	}
	b := o.Bounds
	h := o.Tilt.Homography(t.Apply(geom.Pt((b.Min.X+b.Max.X)/2, (b.Min.Y+b.Max.Y)/2)))
	// The quad reaches a little past the bounds, for their antialiased
	// edge, and each corner shows the texture where its point lies flat.
	cs := corners(grow4(b, 1), geom.Rect{})
	var at [4]geom.Point
	var w [4]float32
	for i := range cs {
		flat := t.Apply(cs[i].local)
		cs[i].uv = geom.Pt(flat.X*r.scale/float32(r.fbW), 1-flat.Y*r.scale/float32(r.fbH))
		at[i], w[i] = h.Apply(flat)
		if w[i] <= paint.MinDepth {
			return
		}
	}
	r.uses(tex)
	l := look{kind: kindImage, rect: b, add: o.Blend == paint.BlendAdd, color0: [4]float32{0, 0, 0, opacity}}
	if o.Clip && o.Ellipse {
		l.stroke = 1
	} else if o.Clip {
		l.radius = radius
	}
	r.emit(cs, at, w, r.scale, &l)
}

// fbo returns the framebuffer for nesting depth d: the canvas, or the
// window for a frame drawn straight to it, at depth 0, and a layer's
// target beneath it.
func (r *Renderer) fbo(d int) uint32 {
	if d == 0 && r.direct {
		return r.WindowFBO
	}
	return r.layers[d].fbo
}

// window returns the whole window in logical pixels.
func (r *Renderer) window() geom.Rect {
	return geom.Rect{Max: geom.Pt(float32(r.fbW)/r.scale, float32(r.fbH)/r.scale)}
}

// region returns the device-pixel rectangle a layer covers: its bounds
// under its transform when bounded, or the whole window.
func (r *Renderer) region(op *paint.LayerOp, bounded bool) geom.Rect {
	if !bounded {
		return geom.Rect{Max: geom.Pt(float32(r.fbW), float32(r.fbH))}
	}
	b, t := op.Opts.Bounds, op.Transform
	first := t.Apply(b.Min)
	out := geom.Rect{Min: first, Max: first}
	for _, c := range []geom.Point{{X: b.Max.X, Y: b.Min.Y}, b.Max, {X: b.Min.X, Y: b.Max.Y}} {
		p := t.Apply(c)
		out.Min = geom.Pt(min(out.Min.X, p.X), min(out.Min.Y, p.Y))
		out.Max = geom.Pt(max(out.Max.X, p.X), max(out.Max.Y, p.Y))
	}
	return geom.Rect{Min: out.Min.Mul(r.scale), Max: out.Max.Mul(r.scale)}
}

// fit sizes t to the window, creating it on first use, and reports
// whether it changed.
func (r *Renderer) fit(t *target) bool { return r.fitSize(t, r.fbW, r.fbH) }

// fitSize sizes t to w by h pixels, creating it on first use, and
// reports whether it changed. A changed target holds nothing.
func (r *Renderer) fitSize(t *target, w, h int) bool {
	g := r.GL
	if t.tex != 0 && t.w == w && t.h == h {
		return false
	}
	if t.tex == 0 {
		t.tex = g.CreateTexture()
		t.fbo = g.CreateFramebuffer()
	}
	t.w, t.h = w, h
	g.ActiveTexture(glTexture1)
	g.BindTexture(gl.TEXTURE_2D, t.tex)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, glLinear)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, glLinear)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	g.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, int32(t.w), int32(t.h), gl.RGBA, gl.UNSIGNED_BYTE, nil)
	g.ActiveTexture(gl.TEXTURE0)
	g.BindFramebuffer(gl.FRAMEBUFFER, t.fbo)
	g.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, t.tex, 0)
	return true
}

func grow4(r geom.Rect, by float32) geom.Rect {
	return geom.Rect{
		Min: geom.Pt(r.Min.X-by, r.Min.Y-by),
		Max: geom.Pt(r.Max.X+by, r.Max.Y+by),
	}
}

func rgba(c color.NRGBA) [4]float32 {
	return [4]float32{float32(c.R) / 255, float32(c.G) / 255, float32(c.B) / 255, float32(c.A) / 255}
}

// setClip scissors drawing to c, a device-pixel box with its origin at
// the top left.
func (r *Renderer) setClip(c geom.Rect) {
	r.clip = c
	r.applyClip()
}

// applyClip sets GL's scissor to the renderer's clip, or turns it off
// where the clip holds the whole target.
func (r *Renderer) applyClip() {
	g, c := r.GL, r.clip
	if c.Min.X <= 0 && c.Min.Y <= 0 && c.Max.X >= float32(r.fbW) && c.Max.Y >= float32(r.fbH) {
		g.Disable(gl.SCISSOR_TEST)
		return
	}
	g.Enable(gl.SCISSOR_TEST)
	g.Scissor(scissor(c, r.fbW, r.fbH))
}

// intersect returns the part of a that b covers too.
func intersect(a, b geom.Rect) geom.Rect {
	return geom.Rect{
		Min: geom.Pt(max(a.Min.X, b.Min.X), max(a.Min.Y, b.Min.Y)),
		Max: geom.Pt(min(a.Max.X, b.Max.X), min(a.Max.Y, b.Max.Y)),
	}
}
