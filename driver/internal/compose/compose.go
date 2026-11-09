// Package compose lays windows on the one surface of a driver that has
// no system windows, as driver/android and driver/web are. The first
// window fills the surface and popups lie over it. Each is drawn into a
// texture of its own, except a window alone on the surface and filling
// it, which draws straight onto it.
//
// A driver's frame collects the frames waiting and the windows showing,
// and asks [Compositor.Dirty] whether there is anything to do. It then
// calls [Compositor.Begin], [Compositor.Draw] for each frame waiting, and
// [Compositor.Finish], and shows the surface in its own way. Each window
// that closes goes to [Compositor.Release], on the thread that draws.
package compose

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"time"

	"github.com/marrasen/gunim/driver/internal/render"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
)

// GL constants the gl package leaves out.
const (
	glColorBufferBit = 0x4000
	glLinear         = 0x2601
	glRGBA8          = 0x8058
	glStaticDraw     = 0x88E4
	glUnsignedShort  = 0x1403
	glScissorTest    = 0x0C11
)

// A Pane is what the compositor keeps for one window. It belongs to the
// thread that draws.
type Pane struct {
	// Transparent says the window shows what is under it where it paints
	// nothing, as a popup does. Sharp hears when the last frame drew a big
	// mask stretched, for the driver to draw it again sharp at that time,
	// or the zero time for never. A driver sets both as it makes the
	// window.
	Transparent bool
	Sharp       func(at time.Time)

	// R is the window's renderer, made as it draws its first frame.
	R *render.Renderer
	// Last is the last frame drawn, at LastW by LastH device pixels, for
	// drawing again where the window moves between the surface and its
	// texture or changes size. OnTex says its texture holds it, as it does
	// unless the window drew alone, straight onto the surface.
	Last         []paint.Op
	LastW, LastH int
	OnTex        bool

	drawer     drawer
	target     uint32
	tex, fbo   uint32
	texW, texH int
}

// Forget lets go of every GPU object, for a GL context that was lost. The
// last frame stays, to be drawn again in the new one.
func (p *Pane) Forget() {
	p.R, p.drawer = nil, nil
	p.tex, p.fbo, p.texW, p.texH = 0, 0, 0, 0
	p.target, p.OnTex = 0, false
}

// Release frees what a closed window holds of the GPU: its renderer, its
// texture and its framebuffer. It runs on the thread that draws, outside a
// frame. The pane holds nothing after it, so a window that closes while its
// frame waits is safe to release.
func (c *Compositor) Release(p *Pane) {
	if p.drawer != nil {
		p.drawer.release()
	}
	p.dropTexture(c.g)
	p.Forget()
	p.Last, p.LastW, p.LastH = nil, 0, 0
}

// A Layer is a window as the compositor lays it: its texture at X, Y on
// the surface, in device pixels from the top left, at W by H device
// pixels. Under is the colour under the window's frames, which it shows
// until it has a frame.
type Layer struct {
	Pane       *Pane
	X, Y, W, H int
	Under      color.NRGBA
}

// A Compositor draws windows and lays their textures on the surface. It
// belongs to the thread that draws, and to one GL context.
type Compositor struct {
	g      gl.Context
	name   string
	shared render.Shared
	open   func(*Pane) (drawer, error)

	prog     uint32
	rect     int32
	vao, vbo uint32
	ibo      uint32
	// last is the stack laid last, so a change in it, as a popup moving or
	// hiding, draws a frame with no window presenting.
	last []Layer
	// at holds a layer's rect for the GL, kept here so handing it through
	// gl.Context allocates nothing.
	at [4]float32
	// solo is the window the frame draws straight onto the surface, and
	// soloDrew says it has. See Begin.
	solo     *Pane
	soloDrew bool
}

const compositeVertex = `#version 300 es
uniform vec4 rect;
in vec2 corner;
out vec2 uv;
void main() {
	uv = corner;
	gl_Position = vec4(mix(rect.xy, rect.zw, corner), 0.0, 1.0);
}
`

const compositeFragment = `#version 300 es
precision mediump float;
uniform sampler2D tex;
in vec2 uv;
out vec4 color;
void main() {
	color = texture(tex, uv);
}
`

// New builds a compositor in g's context, which must be current. name
// starts its errors and what it logs, as "android" or "web".
func New(g gl.Context, name string) (*Compositor, error) {
	vs, err := compileShader(g, name, gl.VERTEX_SHADER, compositeVertex)
	if err != nil {
		return nil, err
	}
	fs, err := compileShader(g, name, gl.FRAGMENT_SHADER, compositeFragment)
	if err != nil {
		return nil, err
	}
	p := g.CreateProgram()
	g.AttachShader(p, vs)
	g.AttachShader(p, fs)
	g.BindAttribLocation(p, 0, "corner")
	g.LinkProgram(p)
	g.DeleteShader(vs)
	g.DeleteShader(fs)
	if g.GetProgrami(p, gl.LINK_STATUS) == gl.FALSE {
		return nil, fmt.Errorf("%s: link compositor: %s", name, g.GetProgramInfoLog(p))
	}
	c := &Compositor{g: g, name: name, prog: p, rect: g.GetUniformLocation(p, "rect")}
	c.open = c.openRenderer
	g.UseProgram(p)
	g.Uniform1i(g.GetUniformLocation(p, "tex"), 0)

	c.vao = g.CreateVertexArray()
	g.BindVertexArray(c.vao)
	c.vbo = g.CreateBuffer()
	g.BindBuffer(gl.ARRAY_BUFFER, c.vbo)
	var verts []byte
	for _, v := range []float32{0, 0, 1, 0, 1, 1, 0, 1} {
		verts = binary.LittleEndian.AppendUint32(verts, math.Float32bits(v))
	}
	g.BufferData(gl.ARRAY_BUFFER, verts, glStaticDraw)
	g.EnableVertexAttribArray(0)
	g.VertexAttribPointer(0, 2, gl.FLOAT, false, 8, 0)
	c.ibo = g.CreateBuffer()
	g.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, c.ibo)
	var idx []byte
	for _, i := range []uint16{0, 1, 2, 0, 2, 3} {
		idx = binary.LittleEndian.AppendUint16(idx, i)
	}
	g.BufferData(gl.ELEMENT_ARRAY_BUFFER, idx, glStaticDraw)
	g.BindVertexArray(0)
	return c, nil
}

func compileShader(g gl.Context, name string, kind uint32, src string) (uint32, error) {
	s := g.CreateShader(kind)
	g.ShaderSource(s, src)
	g.CompileShader(s)
	if g.GetShaderi(s, gl.COMPILE_STATUS) == gl.FALSE {
		return 0, fmt.Errorf("%s: compile compositor: %s", name, g.GetShaderInfoLog(s))
	}
	return s, nil
}

// Dirty reports whether stack differs from the stack laid last.
func (c *Compositor) Dirty(stack []Layer) bool {
	if len(stack) != len(c.last) {
		return true
	}
	for i := range stack {
		if stack[i] != c.last[i] {
			return true
		}
	}
	return false
}

// Begin starts a frame of the windows in stack, bottom first, on a surface
// of w×h device pixels. A window alone on the surface, filling it, draws
// straight onto it: a copy through its texture would cost the GPU the whole
// screen again each frame.
func (c *Compositor) Begin(stack []Layer, w, h int) {
	c.solo, c.soloDrew = nil, false
	if len(stack) == 1 {
		if l := stack[0]; l.X == 0 && l.Y == 0 && l.W == w && l.H == h {
			c.solo = l.Pane
		}
	}
}

// Draw draws a frame into its window, at w×h device pixels. It reports
// whether it did: a window with no size draws nothing. It runs between
// Begin and Finish.
func (c *Compositor) Draw(p *Pane, ops []paint.Op, damage []geom.Rect, w, h int, under color.NRGBA, scale float32) bool {
	if w <= 0 || h <= 0 {
		return false
	}
	if p.drawer == nil {
		d, err := c.open(p)
		if err != nil {
			log.Printf("gunim: %s: %v", c.name, err)
			return false
		}
		p.drawer = d
	}
	p.Last, p.LastW, p.LastH = ops, w, h
	if p == c.solo {
		p.onSurface(c.g)
		c.soloDrew = true
	} else {
		p.onTexture(c.g)
	}
	p.Sharp(p.drawer.drawRects(p.target, under, ops, damage, w, h, scale))
	return true
}

// Shoot hands fn the pixels of the frame [Compositor.Draw] just drew into
// p, w×h device pixels, on a goroutine of its own.
func (c *Compositor) Shoot(p *Pane, w, h int, fn func(*image.RGBA)) {
	c.g.BindFramebuffer(gl.FRAMEBUFFER, p.target)
	pix := make([]byte, w*h*4)
	c.g.ReadPixels(pix, 0, 0, int32(w), int32(h), gl.RGBA, gl.UNSIGNED_BYTE)
	go fn(Upright(pix, w, h))
}

// Finish ends the frame: the surface now holds the windows in stack, the
// same stack Begin had, on a surface of w×h device pixels. The driver shows
// it next.
func (c *Compositor) Finish(stack []Layer, w, h int, scale float32) {
	if c.solo != nil {
		switch {
		case c.soloDrew:
		case c.solo.Last == nil:
			// A surface that has just come, as a canvas hidden at first, with the
			// window's first frame still on its way.
			c.clear(stack[0].Under, w, h)
		default:
			// Nothing new to draw, as the stack changed round it or the surface
			// changed size, but the surface holds nothing after it was shown: the
			// last frame again, to fill it.
			c.solo.LastW, c.solo.LastH = w, h
			c.solo.redraw(c.g, true, scale)
		}
		c.last = append(c.last[:0], stack...)
	} else {
		for _, l := range stack {
			// A window last drawn straight onto the surface has its texture
			// behind, and a window with a new size and no frame for it yet would
			// leave black round it. Each draws its last frame into its texture to
			// be laid.
			if sized := l.Pane.LastW != l.W || l.Pane.LastH != l.H; sized || !l.Pane.OnTex {
				if l.Pane.Last != nil {
					l.Pane.LastW, l.Pane.LastH = l.W, l.H
				}
				l.Pane.redraw(c.g, false, scale)
			}
		}
		c.lay(stack, w, h)
	}
	// The renderers share the context's blending, which the compositor
	// changed.
	for _, l := range stack {
		if l.Pane.drawer != nil {
			l.Pane.drawer.rebind()
			break
		}
	}
}

// redraw draws the window's last frame again, whole, straight onto the
// surface or into its texture.
func (p *Pane) redraw(g gl.Context, surface bool, scale float32) {
	if p.drawer == nil || p.Last == nil {
		return
	}
	if surface {
		p.onSurface(g)
	} else {
		p.onTexture(g)
	}
	p.Sharp(p.drawer.draw(p.target, p.Last, p.LastW, p.LastH, scale))
}

// onSurface has the window's next draw go straight onto the surface. Its
// texture has no use then. One from a popup just closed stays for the next
// popup, while it is still the right size. One of another size goes, as it
// would only hold GPU memory: about 33 MB at 4K.
func (p *Pane) onSurface(g gl.Context) {
	if p.texW != p.LastW || p.texH != p.LastH {
		p.dropTexture(g)
	}
	p.target, p.OnTex = 0, false
}

// onTexture has the window's next draw go into its texture, made at the size
// of its last frame.
func (p *Pane) onTexture(g gl.Context) {
	if p.texW != p.LastW || p.texH != p.LastH {
		p.resize(g, p.LastW, p.LastH)
	}
	p.target, p.OnTex = p.fbo, true
}

// resize gives the window a texture of width×height device pixels to draw
// into.
func (p *Pane) resize(g gl.Context, width, height int) {
	if p.tex == 0 {
		p.tex = g.CreateTexture()
		p.fbo = g.CreateFramebuffer()
	}
	g.BindTexture(gl.TEXTURE_2D, p.tex)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, glLinear)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, glLinear)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	g.TexImage2D(gl.TEXTURE_2D, 0, glRGBA8, int32(width), int32(height), gl.RGBA, gl.UNSIGNED_BYTE, nil)
	g.BindFramebuffer(gl.FRAMEBUFFER, p.fbo)
	g.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, p.tex, 0)
	g.BindFramebuffer(gl.FRAMEBUFFER, 0)
	p.texW, p.texH = width, height
}

// dropTexture frees the window's texture and framebuffer, if it has them.
func (p *Pane) dropTexture(g gl.Context) {
	if p.tex == 0 {
		return
	}
	g.DeleteFramebuffer(p.fbo)
	g.DeleteTexture(p.tex)
	p.tex, p.fbo, p.texW, p.texH = 0, 0, 0, 0
}

// clear fills the surface, w×h device pixels, with a colour, over black where the
// colour has alpha, as the surface has none.
func (c *Compositor) clear(under color.NRGBA, w, h int) {
	g := c.g
	g.BindFramebuffer(gl.FRAMEBUFFER, 0)
	g.Viewport(0, 0, int32(w), int32(h))
	g.Disable(glScissorTest)
	a := float32(under.A) / 0xff
	g.ClearColor(float32(under.R)/0xff*a, float32(under.G)/0xff*a, float32(under.B)/0xff*a, 1)
	g.Clear(glColorBufferBit)
	g.ClearColor(0, 0, 0, 0)
}

// lay lays the windows on the surface, w×h device pixels, bottom first.
// Each texture holds its window's frame premultiplied, with its first row
// at the bottom, as the window's own framebuffer would.
func (c *Compositor) lay(stack []Layer, w, h int) {
	g := c.g
	c.last = append(c.last[:0], stack...)
	c.clear(color.NRGBA{}, w, h)
	g.Enable(gl.BLEND)
	g.BlendFuncSeparate(gl.ONE, gl.ONE_MINUS_SRC_ALPHA, gl.ONE, gl.ONE_MINUS_SRC_ALPHA)
	g.UseProgram(c.prog)
	g.BindVertexArray(c.vao)
	g.ActiveTexture(gl.TEXTURE0)
	for _, l := range stack {
		p := l.Pane
		if p.tex == 0 {
			continue
		}
		x0 := 2*float32(l.X)/float32(w) - 1
		x1 := 2*float32(l.X+p.texW)/float32(w) - 1
		y1 := 1 - 2*float32(l.Y)/float32(h)
		y0 := 1 - 2*float32(l.Y+p.texH)/float32(h)
		c.at = [4]float32{x0, y0, x1, y1}
		g.Uniform4fv(c.rect, c.at[:])
		g.BindTexture(gl.TEXTURE_2D, p.tex)
		g.DrawElements(gl.TRIANGLES, 6, glUnsignedShort, 0)
	}
	g.BindVertexArray(0)
}

// Upright turns pixels read from GL, bottom row first, into a picture the
// right way up.
func Upright(pix []byte, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		copy(img.Pix[y*img.Stride:(y+1)*img.Stride], pix[(h-1-y)*w*4:(h-y)*w*4])
	}
	return img
}

// A drawer is what the compositor asks of a window's renderer: draw a
// frame into a framebuffer, say when it wants the frame drawn again sharp,
// and let go of what it holds. A test stands in for it, as a renderer needs
// a real GL.
type drawer interface {
	drawRects(fbo uint32, under color.NRGBA, ops []paint.Op, damage []geom.Rect, w, h int, scale float32) (sharpAt time.Time)
	draw(fbo uint32, ops []paint.Op, w, h int, scale float32) (sharpAt time.Time)
	rebind()
	release()
}

// openRenderer makes p's renderer, and is the drawer of every real window.
func (c *Compositor) openRenderer(p *Pane) (drawer, error) {
	r, err := render.New(c.g, true, &c.shared)
	if err != nil {
		return nil, err
	}
	p.styleText(r)
	p.R = r
	return rendererDrawer{r}, nil
}

// styleText tells the window's renderer how to draw text.
func (p *Pane) styleText(r textStyler) { r.SetText(text.Rendering{}, p.Transparent) }

// textStyler is the part of a renderer that [Pane.styleText] tells, so a test can
// stand in for it.
type textStyler interface {
	SetText(tr text.Rendering, transparent bool)
}

// rendererDrawer is a [render.Renderer] as a drawer. It is one pointer, so
// a Pane holding it allocates nothing further.
type rendererDrawer struct{ r *render.Renderer }

func (d rendererDrawer) drawRects(fbo uint32, under color.NRGBA, ops []paint.Op, damage []geom.Rect, w, h int, scale float32) time.Time {
	d.r.WindowFBO, d.r.Under = fbo, under
	d.r.DrawRects(ops, damage, w, h, scale)
	return d.r.SharpAt
}

func (d rendererDrawer) draw(fbo uint32, ops []paint.Op, w, h int, scale float32) time.Time {
	d.r.WindowFBO = fbo
	d.r.Draw(ops, paint.Everything, w, h, scale)
	return d.r.SharpAt
}

func (d rendererDrawer) rebind() { d.r.Rebind() }

func (d rendererDrawer) release() { d.r.Release() }
