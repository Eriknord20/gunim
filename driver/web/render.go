//go:build js && wasm

package web

import (
	"encoding/binary"
	"fmt"
	"image/color"
	"log"
	"math"
	"os"
	"time"

	"github.com/marrasen/gunim/driver"
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

// frame runs in requestAnimationFrame. It draws the frames waiting
// into their windows, lays the windows on the canvas, bottom first,
// and tells each window its frame is shown. The browser shows the
// canvas as the callback returns.
func (d *Driver) frame() {
	d.mu.Lock()
	lost := d.lost
	d.mu.Unlock()
	if lost {
		return
	}
	g := d.g
	d.mu.Lock()
	type job struct {
		w        *Window
		f        *frame
		fbW, fbH int
		under    color.NRGBA
		// again says it is the last frame drawn over, which the engine never sent.
		again bool
	}
	var jobs []job
	var stack []layer
	scale, surfW, surfH := d.scale, d.surfW, d.surfH
	for _, w := range d.windows {
		again := false
		if (w.sharpen || w.shot != nil) && w.next == nil && w.last != nil {
			w.next = &frame{ops: w.last, damage: []geom.Rect{paint.Everything}}
			again = true
		}
		w.sharpen = false
		r := w.rectLocked()
		fbW, fbH := int(math.Round(float64(r.Size().W*scale))), int(math.Round(float64(r.Size().H*scale)))
		if w.next != nil {
			jobs = append(jobs, job{w: w, f: w.next, fbW: fbW, fbH: fbH, under: w.under, again: again})
			w.next = nil
		}
		if !w.hidden {
			x, y := int(math.Round(float64(r.Min.X*scale))), int(math.Round(float64(r.Min.Y*scale)))
			stack = append(stack, layer{w: w, x: x, y: y, fbW: fbW, fbH: fbH})
		}
	}
	d.mu.Unlock()
	if len(jobs) == 0 && !d.comp.dirty(stack) {
		return
	}
	// A window alone on the canvas, filling it, draws straight onto it.
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
			r, err := render.New(g, true, &d.shared)
			if err != nil {
				log.Printf("gunim: web: %v", err)
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
		start := time.Now()
		w.r.DrawRects(j.f.ops, j.f.damage, j.fbW, j.fbH, scale)
		d.sharpenAt(w, w.r.SharpAt)
		d.mu.Lock()
		shot := w.shot
		w.shot = nil
		d.mu.Unlock()
		if shot != nil {
			g.BindFramebuffer(gl.FRAMEBUFFER, w.r.WindowFBO)
			pix := make([]byte, j.fbW*j.fbH*4)
			g.ReadPixels(pix, 0, 0, int32(j.fbW), int32(j.fbH), gl.RGBA, gl.UNSIGNED_BYTE)
			img := upright(pix, j.fbW, j.fbH)
			go shot(img)
		}
		if framesDebug {
			var gpu time.Duration
			if framesFinish {
				t := time.Now()
				g.Finish()
				gpu = time.Since(t)
			}
			d.stats.add(time.Since(start), gpu, &w.r.Stats)
		}
	}
	if solo != nil {
		if !soloDrew {
			// The browser cleared the canvas after showing it.
			solo.redraw(g, 0, scale)
		}
		d.comp.last = append(d.comp.last[:0], stack...)
	} else {
		for _, l := range stack {
			if !l.w.onTex {
				l.w.redraw(g, l.w.fbo, scale)
			}
		}
		d.comp.draw(g, stack, surfW, surfH)
	}
	for _, l := range stack {
		if l.w.r != nil {
			l.w.r.Rebind()
			break
		}
	}
	now := time.Now()
	for _, j := range jobs {
		if j.again {
			continue
		}
		select {
		case j.w.presented <- driver.Frame{Shown: now, Lag: time.Duration(float64(time.Second) / d.rate)}:
		default:
		}
	}
}

// redraw draws the window's last frame again, whole, into fbo: its
// texture, or 0 for the canvas.
func (w *Window) redraw(g gl.Context, fbo uint32, scale float32) {
	if w.r == nil || w.last == nil {
		return
	}
	if fbo != 0 && (w.texW != w.lastW || w.texH != w.lastH) {
		w.resize(g, w.lastW, w.lastH)
	}
	w.r.WindowFBO = fbo
	w.r.Draw(w.last, paint.Everything, w.lastW, w.lastH, scale)
	w.d.sharpenAt(w, w.r.SharpAt)
	w.onTex = fbo != 0
}

// resize gives the window a texture of width×height device pixels.
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

// layer is a window as the compositor lays it, in device pixels from
// the canvas's top left.
type layer struct {
	w        *Window
	x, y     int
	fbW, fbH int
}

// A compositor lays windows' textures on the canvas.
type compositor struct {
	prog     uint32
	rect     int32
	vao, vbo uint32
	ibo      uint32
	last     []layer
	at       [4]float32
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
		return nil, fmt.Errorf("web: link compositor: %s", g.GetProgramInfoLog(p))
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
		return 0, fmt.Errorf("web: compile compositor: %s", g.GetShaderInfoLog(s))
	}
	return s, nil
}

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

// draw lays the windows on the canvas, w×h device pixels, bottom first.
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

// framesDebug is set by GUNIM_DEBUG_FRAMES=1, which logs each second
// how long the frames took to draw and what they sent the GPU. In a
// browser the page sets it in the Go object's env before running.
var framesDebug = os.Getenv("GUNIM_DEBUG_FRAMES") != ""

// framesFinish is set by GUNIM_DEBUG_FRAMES=gpu, which waits for the GPU
// after each frame and logs how long it took.
var framesFinish = os.Getenv("GUNIM_DEBUG_FRAMES") == "gpu"

type frameStats struct {
	from          time.Time
	n             int
	draw, drawMax time.Duration
	gpu, gpuMax   time.Duration
	quads         int
	redrawn       float64
}

func (s *frameStats) add(draw, gpu time.Duration, sent *render.Stats) {
	now := time.Now()
	if s.from.IsZero() {
		s.from = now
	}
	s.n++
	s.draw += draw
	s.drawMax = max(s.drawMax, draw)
	s.gpu += gpu
	s.gpuMax = max(s.gpuMax, gpu)
	s.quads += sent.Quads
	s.redrawn += sent.Redrawn
	*sent = render.Stats{}
	if now.Sub(s.from) < time.Second {
		return
	}
	n := float64(s.n)
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
	log.Printf("gunim web: %d frames drawn; draw %.1f ms (max %.1f), GPU %.1f ms (max %.1f); %.0f quads and %.0f%% of the window redrawn a frame",
		s.n, ms(s.draw)/n, ms(s.drawMax), ms(s.gpu)/n, ms(s.gpuMax), float64(s.quads)/n, 100*s.redrawn/n)
	*s = frameStats{from: now}
}
