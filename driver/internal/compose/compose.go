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
	glNearest        = 0x2600
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
	// InCanvas says the frame stays in the renderer's canvas, which the
	// compositor lays itself: see [Compositor.SetTurn].
	InCanvas bool

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
	p.target, p.OnTex, p.InCanvas = 0, false, false
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
	turnLoc  int32
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
	// turned says solo is laid turned, from its canvas.
	turned bool
	// turn is how far the windows are turned on the surface, clockwise, and
	// turnM the matrix for it; see SetTurn.
	turn  int
	turnM [4]float32
	// shade is how much black lies over the whole surface, 0 to 1, and
	// laidShade the shade the surface was last laid with; see SetShade.
	// black is a texture of one black pixel, which lays it, and fadeLoc
	// the uniform it is laid at, with fadeV its value, kept here so
	// handing it through gl.Context allocates nothing.
	shade, laidShade float32
	black            uint32
	fadeLoc          int32
	fadeV            [1]float32
}

const compositeVertex = `#version 300 es
uniform vec4 rect;
uniform mat2 turn;
in vec2 corner;
out vec2 uv;
void main() {
	uv = corner;
	gl_Position = vec4(turn * mix(rect.xy, rect.zw, corner), 0.0, 1.0);
}
`

const compositeFragment = `#version 300 es
precision mediump float;
uniform sampler2D tex;
uniform float fade;
in vec2 uv;
out vec4 color;
void main() {
	color = texture(tex, uv) * fade;
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
	c := &Compositor{g: g, name: name, prog: p, rect: g.GetUniformLocation(p, "rect"), turnLoc: g.GetUniformLocation(p, "turn"),
		fadeLoc: g.GetUniformLocation(p, "fade")}
	c.open = c.openRenderer
	g.UseProgram(p)
	g.Uniform1i(g.GetUniformLocation(p, "tex"), 0)
	c.setFade(1)
	c.SetTurn(0)

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

// NoDualSource has every window's renderer blend with no second colour,
// so text is greyscale: for a GPU that would blend by a second colour in
// the shader, as Mesa's V3D does. It is called before the first window
// draws.
func (c *Compositor) NoDualSource() { c.shared.NoDualSource = true }

// SetTurn turns the windows on the surface by deg clockwise: 0, 90, 180 or
// 270, for a screen mounted another way up than its own, as a panel in
// portrait shown in landscape. The sizes and places the compositor is
// given are then the windows' own, the right way up for them: a surface
// turned 90° is h×w where the screen is w×h. A window alone and filling
// the screen stays in its renderer's canvas, which the compositor lays
// turned, so turning costs no more copies of the screen than not turning.
// Windows with a popup over them are laid through their textures, at one
// copy more.
func (c *Compositor) SetTurn(deg int) {
	c.turn = deg
	switch deg {
	case 90:
		c.turnM = [4]float32{0, -1, 1, 0}
	case 180:
		c.turnM = [4]float32{-1, 0, 0, -1}
	case 270:
		c.turnM = [4]float32{0, 1, -1, 0}
	default:
		c.turn, c.turnM = 0, [4]float32{1, 0, 0, 1}
	}
	c.g.UseProgram(c.prog)
	c.g.UniformMatrix2fv(c.turnLoc, c.turnM[:])
	c.last = c.last[:0]
}

// SetShade lays black over the whole surface, from 0 for none to 1 for
// black, as the last thing each frame: a driver dims the screen with it
// between its backlight's steps. A new shade makes the stack dirty, so the
// next frame lays it.
func (c *Compositor) SetShade(v float32) { c.shade = min(max(v, 0), 1) }

// setFade sets what the compositor's program multiplies each texel by.
func (c *Compositor) setFade(v float32) {
	c.fadeV[0] = v
	c.g.Uniform1fv(c.fadeLoc, c.fadeV[:])
}

// layShade lays the shade over the surface, w×h, where there is one.
func (c *Compositor) layShade(w, h int) {
	c.laidShade = c.shade
	if c.shade <= 0 {
		return
	}
	g := c.g
	if c.black == 0 {
		c.black = g.CreateTexture()
		g.BindTexture(gl.TEXTURE_2D, c.black)
		g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, glNearest)
		g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, glNearest)
		g.TexImage2D(gl.TEXTURE_2D, 0, glRGBA8, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, []byte{0, 0, 0, 0xff})
	}
	c.startLaying(w, h)
	c.setFade(c.shade)
	c.layOne(c.black, 0, 0, w, h, w, h, false)
	c.setFade(1)
	g.BindVertexArray(0)
}

// view returns the surface's own size for windows laid on w×h.
func (c *Compositor) view(w, h int) (vw, vh int32) {
	if c.turn == 90 || c.turn == 270 {
		return int32(h), int32(w)
	}
	return int32(w), int32(h)
}

// Dirty reports whether stack differs from the stack laid last, or the
// shade from the shade laid last.
func (c *Compositor) Dirty(stack []Layer) bool {
	if len(stack) != len(c.last) || c.shade != c.laidShade {
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
	c.solo, c.soloDrew, c.turned = nil, false, false
	if len(stack) == 1 {
		if l := stack[0]; l.X == 0 && l.Y == 0 && l.W == w && l.H == h {
			c.solo, c.turned = l.Pane, c.turn != 0
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
	switch {
	case p == c.solo && c.turned:
		p.inCanvas(c.g)
		c.soloDrew = true
	case p == c.solo:
		p.onSurface(c.g)
		c.soloDrew = true
	default:
		p.onTexture(c.g)
	}
	p.Sharp(p.drawer.drawRects(p.target, under, ops, damage, w, h, scale))
	return true
}

// Shoot hands fn the pixels of the frame [Compositor.Draw] just drew into
// p, w×h device pixels, on a goroutine of its own.
func (c *Compositor) Shoot(p *Pane, w, h int, fn func(*image.RGBA)) {
	fbo := p.target
	if p.InCanvas && p.drawer != nil {
		_, fbo = p.drawer.canvas()
	}
	c.g.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	pix := make([]byte, w*h*4)
	c.g.ReadPixels(pix, 0, 0, int32(w), int32(h), gl.RGBA, gl.UNSIGNED_BYTE)
	go fn(Upright(pix, w, h))
}

// Finish ends the frame: the surface now holds the windows in stack, the
// same stack Begin had, on a surface of w×h device pixels. The driver shows
// it next.
func (c *Compositor) Finish(stack []Layer, w, h int, scale float32) {
	switch {
	case c.solo != nil && c.turned:
		p := c.solo
		switch {
		case c.soloDrew:
		case p.Last == nil:
		case p.InCanvas && p.LastW == w && p.LastH == h:
			// The canvas still holds the last frame.
		default:
			// The last frame went to the window's texture, or at another
			// size: again, into the canvas.
			p.LastW, p.LastH = w, h
			p.redraw(c.g, inCanvas, scale)
		}
		c.layCanvas(stack[0], w, h)
	case c.solo != nil:
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
			c.solo.redraw(c.g, onSurface, scale)
		}
		c.last = append(c.last[:0], stack...)
	default:
		for _, l := range stack {
			// A window last drawn straight onto the surface has its texture
			// behind, and a window with a new size and no frame for it yet would
			// leave black round it. Each draws its last frame into its texture to
			// be laid.
			if sized := l.Pane.LastW != l.W || l.Pane.LastH != l.H; sized || !l.Pane.OnTex {
				if l.Pane.Last != nil {
					l.Pane.LastW, l.Pane.LastH = l.W, l.H
				}
				l.Pane.redraw(c.g, onTexture, scale)
			}
		}
		c.lay(stack, w, h)
	}
	c.layShade(w, h)
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
func (p *Pane) redraw(g gl.Context, where place, scale float32) {
	if p.drawer == nil || p.Last == nil {
		return
	}
	switch where {
	case onSurface:
		p.onSurface(g)
	case onTexture:
		p.onTexture(g)
	case inCanvas:
		p.inCanvas(g)
	}
	p.Sharp(p.drawer.draw(p.target, p.Last, p.LastW, p.LastH, scale))
}

// place is where a window's frame goes.
type place uint8

const (
	onSurface place = iota
	onTexture
	inCanvas
)

// keep tells the window's renderer whether its frames stay in its canvas.
func (p *Pane) keep(on bool) {
	if p.drawer != nil {
		p.drawer.keep(on)
	}
}

// inCanvas has the window's next draw stay in its renderer's canvas, for
// the compositor to lay turned. Its texture has no use then, as on the
// surface.
func (p *Pane) inCanvas(g gl.Context) {
	if p.texW != p.LastW || p.texH != p.LastH {
		p.dropTexture(g)
	}
	p.keep(true)
	p.target, p.OnTex, p.InCanvas = 0, false, true
}

// onSurface has the window's next draw go straight onto the surface. Its
// texture has no use then. One from a popup just closed stays for the next
// popup, while it is still the right size. One of another size goes, as it
// would only hold GPU memory: about 33 MB at 4K.
func (p *Pane) onSurface(g gl.Context) {
	if p.texW != p.LastW || p.texH != p.LastH {
		p.dropTexture(g)
	}
	p.keep(false)
	p.target, p.OnTex, p.InCanvas = 0, false, false
}

// onTexture has the window's next draw go into its texture, made at the size
// of its last frame.
func (p *Pane) onTexture(g gl.Context) {
	if p.texW != p.LastW || p.texH != p.LastH {
		p.resize(g, p.LastW, p.LastH)
	}
	p.keep(false)
	p.target, p.OnTex, p.InCanvas = p.fbo, true, false
}

// resize gives the window a texture of width×height device pixels to draw
// into.
func (p *Pane) resize(g gl.Context, width, height int) {
	if p.tex == 0 {
		p.tex = g.CreateTexture()
		p.fbo = g.CreateFramebuffer()
	}
	g.BindTexture(gl.TEXTURE_2D, p.tex)
	// Laid at its own size, pixel for pixel, a texture needs no filtering.
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, glNearest)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, glNearest)
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
	vw, vh := c.view(w, h)
	g.Viewport(0, 0, vw, vh)
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
	// An opaque window covering the surface at the bottom hides the clear,
	// and needs no blending.
	covered := len(stack) > 0 && stack[0].Pane.tex != 0 && covers(stack[0], stack[0].Pane.texW, stack[0].Pane.texH, w, h)
	if !covered {
		c.clear(color.NRGBA{}, w, h)
	}
	c.startLaying(w, h)
	for i, l := range stack {
		p := l.Pane
		if p.tex == 0 {
			continue
		}
		c.layOne(p.tex, l.X, l.Y, p.texW, p.texH, w, h, i == 0 && covered)
	}
	g.BindVertexArray(0)
}

// covers reports whether a layer of tw×th device pixels is opaque and covers
// a surface of w×h.
func covers(l Layer, tw, th, w, h int) bool {
	return !l.Pane.Transparent && l.X <= 0 && l.Y <= 0 && l.X+tw >= w && l.Y+th >= h
}

// layCanvas lays a window alone on the surface, filling it, from its
// renderer's canvas, turned: the frame's only copy onto the surface.
func (c *Compositor) layCanvas(l Layer, w, h int) {
	p := l.Pane
	c.last = append(c.last[:0], l)
	var tex uint32
	if p.drawer != nil && p.Last != nil {
		tex, _ = p.drawer.canvas()
	}
	if tex == 0 {
		// The first frame is still on its way.
		c.clear(l.Under, w, h)
		return
	}
	opaque := !p.Transparent
	if !opaque {
		c.clear(color.NRGBA{}, w, h)
	}
	c.startLaying(w, h)
	c.layOne(tex, 0, 0, w, h, w, h, opaque)
	c.g.BindVertexArray(0)
}

// startLaying binds what laying textures on the surface, w×h, draws with.
func (c *Compositor) startLaying(w, h int) {
	g := c.g
	g.BindFramebuffer(gl.FRAMEBUFFER, 0)
	vw, vh := c.view(w, h)
	g.Viewport(0, 0, vw, vh)
	g.Disable(glScissorTest)
	g.BlendFuncSeparate(gl.ONE, gl.ONE_MINUS_SRC_ALPHA, gl.ONE, gl.ONE_MINUS_SRC_ALPHA)
	g.UseProgram(c.prog)
	g.BindVertexArray(c.vao)
	g.ActiveTexture(gl.TEXTURE0)
}

// layOne lays the texture tex, tw×th, at x, y on the surface, w×h, over what
// is there, or in its place where opaque.
func (c *Compositor) layOne(tex uint32, x, y, tw, th, w, h int, opaque bool) {
	g := c.g
	if opaque {
		g.Disable(gl.BLEND)
	} else {
		g.Enable(gl.BLEND)
	}
	x0 := 2*float32(x)/float32(w) - 1
	x1 := 2*float32(x+tw)/float32(w) - 1
	y1 := 1 - 2*float32(y)/float32(h)
	y0 := 1 - 2*float32(y+th)/float32(h)
	c.at = [4]float32{x0, y0, x1, y1}
	g.Uniform4fv(c.rect, c.at[:])
	g.BindTexture(gl.TEXTURE_2D, tex)
	g.DrawElements(gl.TRIANGLES, 6, glUnsignedShort, 0)
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
	// keep has the frames stay in the canvas, or go to the framebuffer
	// handed to draw, and canvas returns the canvas's texture and
	// framebuffer.
	keep(on bool)
	canvas() (tex, fbo uint32)
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

func (d rendererDrawer) keep(on bool) { d.r.KeepInCanvas = on }

func (d rendererDrawer) canvas() (tex, fbo uint32) { return d.r.CanvasTexture(), d.r.Canvas() }
