//go:build android

package android

/*
#include "glue.h"
*/
import "C"

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"runtime"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/internal/render"
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

// render is the render thread. It owns the EGL context, which every
// window draws with. Each time a window presents, it draws the frames
// waiting into their windows' textures, lays every shown window on the
// surface, bottom first, swaps, and reports the frames drawn.
//
// With no surface, as while the activity is in the background, frames
// wait. The engine waits for its frame, so it draws nothing meanwhile.
func (d *Driver) render() {
	runtime.LockOSThread()
	g, c, err := startGL()
	if err != nil {
		log.Printf("gunim: android: %v", err)
		d.idle()
		return
	}
	var shared render.Shared

	var surface *C.ANativeWindow
	attached := false
	for {
		select {
		case <-d.quit:
			return
		case sc := <-d.surfaces:
			if sc.gone != nil {
				// A surface other than the one drawn on is an old
				// activity's, going after its replacement came.
				if sc.window == nil || sc.window == surface {
					C.gunim_egl_detach()
					attached = false
					if surface != nil {
						C.gunim_window_release(surface)
						surface = nil
					}
				}
				if sc.window != nil {
					C.gunim_window_release(sc.window)
				}
				close(sc.gone)
				continue
			}
			if sc.window == surface {
				// The same surface, at a new size: Android has
				// resized it already.
				C.gunim_window_release(sc.window)
				continue
			}
			if surface != nil {
				C.gunim_window_release(surface)
			}
			surface = sc.window
			if e := C.gunim_egl_attach(surface); e != 0 {
				log.Printf("gunim: android: EGL surface: error %#x", int(e))
				continue
			}
			attached = true
		case <-d.wake:
		}
		if !attached {
			continue
		}
		d.frame(g, &shared, c)
	}
}

// startGL makes the EGL context current on the render thread, loads
// GL, and builds the compositor.
func startGL() (gl.Context, *compositor, error) {
	if e := C.gunim_egl_init(); e != 0 {
		return nil, nil, fmt.Errorf("EGL: error %#x", int(e))
	}
	g, err := gl.NewDefaultContext()
	if err == nil {
		err = g.LoadFunctions()
	}
	if err != nil {
		return nil, nil, err
	}
	c, err := newCompositor(g)
	if err != nil {
		return nil, nil, err
	}
	return g, c, nil
}

// idle takes surfaces and lets them go, for a render thread that cannot
// draw, so the UI thread handing them over never waits for good.
func (d *Driver) idle() {
	for {
		select {
		case <-d.quit:
			return
		case <-d.wake:
		case sc := <-d.surfaces:
			if sc.window != nil {
				C.gunim_window_release(sc.window)
			}
			if sc.gone != nil {
				close(sc.gone)
			}
		}
	}
}

// frame draws the frames waiting, lays the windows on the surface and
// swaps.
func (d *Driver) frame(g gl.Context, shared *render.Shared, c *compositor) {
	d.mu.Lock()
	type job struct {
		w        *Window
		f        *frame
		fbW, fbH int
		under    color.NRGBA
		shot     func(*image.RGBA)
		// again marks the window's last frame drawn again, to sharpen
		// what it drew stretched; the engine waits for none.
		again bool
	}
	var jobs []job
	var stack []layer
	scale, surfW, surfH := d.density, d.surfW, d.surfH
	pan, panning := d.panLocked(time.Now())
	for _, w := range d.windows {
		r := w.rectLocked()
		fbW, fbH := int(math.Round(float64(r.Size().W*scale))), int(math.Round(float64(r.Size().H*scale)))
		if w.next != nil {
			jobs = append(jobs, job{w: w, f: w.next, fbW: fbW, fbH: fbH, under: w.under, shot: w.shot})
			w.next, w.shot = nil, nil
		} else if w.sharpen && w.last != nil {
			jobs = append(jobs, job{w: w, f: &frame{ops: w.last}, fbW: fbW, fbH: fbH, under: w.under, again: true})
		}
		w.sharpen = false
		if !w.hidden {
			x, y := int(math.Round(float64(r.Min.X*scale))), int(math.Round(float64(r.Min.Y*scale)))-pan
			stack = append(stack, layer{w: w, x: x, y: y, fbW: fbW, fbH: fbH})
		}
	}
	d.mu.Unlock()
	if len(jobs) == 0 && !c.dirty(stack) {
		return
	}
	// A window alone on the surface, filling it, draws straight onto it:
	// a copy through its texture would cost the GPU the whole screen
	// again each frame.
	var solo *Window
	if len(stack) == 1 {
		if l := stack[0]; l.x == 0 && l.y == 0 && l.fbW == surfW && l.fbH == surfH {
			solo = l.w
		}
	}

	soloDrew := false
	for _, j := range jobs {
		w := j.w
		if j.fbW <= 0 || j.fbH <= 0 {
			continue
		}
		if w.r == nil {
			r, err := render.New(g, true, shared)
			if err != nil {
				log.Printf("gunim: android: %v", err)
				continue
			}
			r.SetText(text.Rendering{}, w.transparent)
			w.r = r
		}
		if w.texW != j.fbW || w.texH != j.fbH {
			w.resize(g, j.fbW, j.fbH)
		}
		w.r.Under = j.under
		w.last, w.lastW, w.lastH = j.f.ops, j.fbW, j.fbH
		if w == solo {
			w.r.WindowFBO, w.onTex = 0, false
			soloDrew = true
		} else {
			w.r.WindowFBO, w.onTex = w.fbo, true
		}
		w.r.Draw(j.f.ops, j.f.damage, j.fbW, j.fbH, scale)
		d.sharpenAt(w, w.r.SharpAt)
		if j.shot != nil {
			g.BindFramebuffer(gl.FRAMEBUFFER, w.r.WindowFBO)
			pix := make([]byte, j.fbW*j.fbH*4)
			g.ReadPixels(pix, 0, 0, int32(j.fbW), int32(j.fbH), gl.RGBA, gl.UNSIGNED_BYTE)
			go j.shot(upright(pix, j.fbW, j.fbH))
		}
	}

	if solo != nil {
		if !soloDrew {
			// Nothing new to draw, as the stack changed round it, but
			// the surface holds nothing after a swap: the last frame
			// again, onto it.
			solo.redraw(g, 0, scale)
		}
		c.last = append(c.last[:0], stack...)
	} else {
		// A window last drawn straight onto the surface has its texture
		// behind: it draws its last frame into it to be laid.
		for _, l := range stack {
			if !l.w.onTex {
				l.w.redraw(g, l.w.fbo, scale)
			}
		}
		c.draw(g, stack, surfW, surfH)
	}
	// The renderers share the context's blending, which the compositor
	// changed.
	for _, l := range stack {
		if l.w.r != nil {
			l.w.r.Rebind()
			break
		}
	}
	if e := C.gunim_egl_swap(); e != 0 {
		log.Printf("gunim: android: swap: error %#x", int(e))
	}
	if panning {
		d.kick() // the next frame slides on
	}
	now := time.Now()
	for _, j := range jobs {
		if j.again {
			continue
		}
		select {
		case j.w.presented <- driver.Frame{Shown: now}:
		case <-j.w.quit:
		}
	}
}

// sharpenAt has w draw its last frame again at the time at, the zero
// time for never, so a big mask the frame drew stretched is drawn
// sharp. A frame drawn before then takes its place. The engine keeps a
// frame's ops until it presents the frame after, so they hold.
func (d *Driver) sharpenAt(w *Window, at time.Time) {
	d.mu.Lock()
	w.sharpAt = at
	d.mu.Unlock()
	if at.IsZero() {
		return
	}
	time.AfterFunc(time.Until(at), func() {
		d.mu.Lock()
		w.sharpen = w.sharpen || w.sharpAt.Equal(at)
		d.mu.Unlock()
		d.kick()
	})
}

// upright turns pixels read from GL, bottom row first, into a picture
// the right way up.
func upright(pix []byte, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		copy(img.Pix[y*img.Stride:(y+1)*img.Stride], pix[(h-1-y)*w*4:(h-y)*w*4])
	}
	return img
}

// redraw draws the window's last frame again, whole, into fbo: its
// texture, or 0 for the surface. It runs on the render thread.
func (w *Window) redraw(g gl.Context, fbo uint32, scale float32) {
	if w.r == nil || w.last == nil {
		return
	}
	if fbo != 0 && (w.texW != w.lastW || w.texH != w.lastH) {
		w.resize(g, w.lastW, w.lastH)
	}
	w.r.WindowFBO = fbo
	w.r.Draw(w.last, paint.Everything, w.lastW, w.lastH, scale)
	w.onTex = fbo != 0
}

// resize gives the window a texture of w×h device pixels to draw into.
// It runs on the render thread.
func (w *Window) resize(g gl.Context, width, height int) {
	if w.tex == 0 {
		w.tex = g.CreateTexture()
		w.fbo = g.CreateFramebuffer()
	}
	g.BindTexture(gl.TEXTURE_2D, w.tex)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, glLinear)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, glLinear)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	g.TexImage2D(gl.TEXTURE_2D, 0, glRGBA8, int32(width), int32(height), gl.RGBA, gl.UNSIGNED_BYTE, nil)
	g.BindFramebuffer(gl.FRAMEBUFFER, w.fbo)
	g.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, w.tex, 0)
	g.BindFramebuffer(gl.FRAMEBUFFER, 0)
	w.texW, w.texH = width, height
}

// layer is a window as the compositor lays it: its texture at x, y on
// the surface, in device pixels from the top left.
type layer struct {
	w        *Window
	x, y     int
	fbW, fbH int
}

// A compositor lays windows' textures on the surface.
type compositor struct {
	prog     uint32
	rect     int32
	vao, vbo uint32
	ibo      uint32
	// last is the stack laid last, so a change in it, as a popup
	// moving or hiding, draws a frame with no window presenting.
	last []layer
	// at holds a layer's rect for the GL, kept here so handing it
	// through gl.Context allocates nothing.
	at [4]float32
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

func newCompositor(g gl.Context) (*compositor, error) {
	vs, err := compileShader(g, gl.VERTEX_SHADER, compositeVertex)
	if err != nil {
		return nil, err
	}
	fs, err := compileShader(g, gl.FRAGMENT_SHADER, compositeFragment)
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
		return nil, fmt.Errorf("android: link compositor: %s", g.GetProgramInfoLog(p))
	}
	c := &compositor{prog: p, rect: g.GetUniformLocation(p, "rect")}
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
	g.BufferInit(gl.ARRAY_BUFFER, len(verts), glStaticDraw)
	g.BufferSubData(gl.ARRAY_BUFFER, 0, verts)
	g.EnableVertexAttribArray(0)
	g.VertexAttribPointer(0, 2, gl.FLOAT, false, 8, 0)
	c.ibo = g.CreateBuffer()
	g.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, c.ibo)
	var idx []byte
	for _, i := range []uint16{0, 1, 2, 0, 2, 3} {
		idx = binary.LittleEndian.AppendUint16(idx, i)
	}
	g.BufferInit(gl.ELEMENT_ARRAY_BUFFER, len(idx), glStaticDraw)
	g.BufferSubData(gl.ELEMENT_ARRAY_BUFFER, 0, idx)
	g.BindVertexArray(0)
	return c, nil
}

func compileShader(g gl.Context, kind uint32, src string) (uint32, error) {
	s := g.CreateShader(kind)
	g.ShaderSource(s, src)
	g.CompileShader(s)
	if g.GetShaderi(s, gl.COMPILE_STATUS) == gl.FALSE {
		return 0, fmt.Errorf("android: compile compositor: %s", g.GetShaderInfoLog(s))
	}
	return s, nil
}

// dirty reports whether stack differs from the stack laid last.
func (c *compositor) dirty(stack []layer) bool {
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

// draw lays the windows on the surface, w×h device pixels, bottom
// first. Each texture holds its window's frame premultiplied, with its
// first row at the bottom, as the window's own framebuffer would.
func (c *compositor) draw(g gl.Context, stack []layer, w, h int) {
	c.last = append(c.last[:0], stack...)
	g.BindFramebuffer(gl.FRAMEBUFFER, 0)
	g.Viewport(0, 0, int32(w), int32(h))
	g.Disable(glScissorTest)
	g.ClearColor(0, 0, 0, 1)
	g.Clear(glColorBufferBit)
	g.ClearColor(0, 0, 0, 0)
	g.Enable(gl.BLEND)
	g.BlendFuncSeparate(gl.ONE, gl.ONE_MINUS_SRC_ALPHA, gl.ONE, gl.ONE_MINUS_SRC_ALPHA)
	g.UseProgram(c.prog)
	g.BindVertexArray(c.vao)
	g.ActiveTexture(gl.TEXTURE0)
	for _, l := range stack {
		if l.w.tex == 0 {
			continue
		}
		x0 := 2*float32(l.x)/float32(w) - 1
		x1 := 2*float32(l.x+l.w.texW)/float32(w) - 1
		y1 := 1 - 2*float32(l.y)/float32(h)
		y0 := 1 - 2*float32(l.y+l.w.texH)/float32(h)
		c.at = [4]float32{x0, y0, x1, y1}
		g.Uniform4fv(c.rect, c.at[:])
		g.BindTexture(gl.TEXTURE_2D, l.w.tex)
		g.DrawElements(gl.TRIANGLES, 6, glUnsignedShort, 0)
	}
	g.BindVertexArray(0)
}
