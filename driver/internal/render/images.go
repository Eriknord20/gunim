//go:build linux || windows || darwin

package render

import (
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/paint"
)

const (
	// imageIdle is how long a texture stays after the last frame that
	// drew its image.
	imageIdle = 10 * time.Second
	// imageBudget is how many bytes of image textures a window keeps
	// before it lets go of the ones it drew longest ago.
	imageBudget = 256 << 20
)

// imageTexture is an image uploaded to the GPU.
type imageTexture struct {
	tex   uint32
	bytes int
	used  time.Time
}

// image queues an image, uploading it on first use.
func (r *Renderer) image(op *paint.ImageOp) {
	iw, ih := op.Image.Size()
	if iw == 0 || ih == 0 || op.Opacity <= 0 {
		return
	}
	tex := r.texture(op.Image)
	r.uses(tex)
	src := op.Src
	if src.Empty() {
		src = geom.Rc(0, 0, float32(iw), float32(ih))
	}
	uv := geom.Rect{
		Min: geom.Pt(src.Min.X/float32(iw), src.Min.Y/float32(ih)),
		Max: geom.Pt(src.Max.X/float32(iw), src.Max.Y/float32(ih)),
	}
	r.quad(corners(op.Rect, uv), op.Transform, r.scale, &look{
		rect: op.Rect, radius: op.Radius, kind: kindImage, add: op.Blend == paint.BlendAdd,
		color0: [4]float32{0, 0, 0, op.Opacity},
	})
}

// texture returns img's texture, uploading it with mipmaps on first
// use, so it stays smooth drawn at a fraction of its size.
func (r *Renderer) texture(img *paint.Image) uint32 {
	now := time.Now()
	if t, ok := r.images[img]; ok {
		t.used = now
		return t.tex
	}
	w, h := img.Size()
	g := r.GL
	t := &imageTexture{tex: g.CreateTexture(), bytes: w * h * 4 * 4 / 3, used: now}
	g.ActiveTexture(glTexture1)
	g.BindTexture(gl.TEXTURE_2D, t.tex)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, glLinearMipmapLinear)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, glLinear)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	g.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, int32(w), int32(h), gl.RGBA, gl.UNSIGNED_BYTE, img.Pix())
	g.ActiveTexture(gl.TEXTURE0)
	r.mipmaps(t.tex, w, h)
	r.images[img] = t
	return t.tex
}

// mipmaps fills in the mipmap levels of tex, a w by h texture holding
// level 0, each the one above averaged two by two by the blur shader.
// glGenerateMipmap would do the same, but it can stop Intel's Windows
// driver for good: see blurFactors.
//
// Each pass reads only the level above, through the texture's base
// and top levels, so it never reads the level it draws into.
func (r *Renderer) mipmaps(tex uint32, w, h int) {
	g := r.GL
	r.flush()
	fbo := g.CreateFramebuffer()
	g.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	g.UseProgram(r.blurProg.id)
	g.Disable(gl.BLEND)
	g.Disable(gl.SCISSOR_TEST)
	level := int32(0)
	for w > 1 || h > 1 {
		above := [4]float32{1 / float32(w), 1 / float32(h), 0, 2}
		w, h = max(w/2, 1), max(h/2, 1)
		level++
		g.ActiveTexture(glTexture1)
		g.BindTexture(gl.TEXTURE_2D, tex)
		g.TexImage2D(gl.TEXTURE_2D, level, gl.RGBA, int32(w), int32(h), gl.RGBA, gl.UNSIGNED_BYTE, nil)
		g.TexParameteri(gl.TEXTURE_2D, glTextureBaseLevel, level-1)
		g.TexParameteri(gl.TEXTURE_2D, glTextureMaxLevel, level-1)
		g.ActiveTexture(gl.TEXTURE0)
		g.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, tex, level)
		g.Viewport(0, 0, int32(w), int32(h))
		r.uses(tex)
		r.quad(corners(r.window(), geom.Rect{}), paint.Identity, r.scale, &look{extra: above})
		r.flush()
	}
	g.ActiveTexture(glTexture1)
	g.BindTexture(gl.TEXTURE_2D, tex)
	g.TexParameteri(gl.TEXTURE_2D, glTextureBaseLevel, 0)
	g.TexParameteri(gl.TEXTURE_2D, glTextureMaxLevel, level)
	g.ActiveTexture(gl.TEXTURE0)
	g.BindFramebuffer(gl.FRAMEBUFFER, r.fbo(r.depth))
	g.DeleteFramebuffer(fbo)
	g.Viewport(0, 0, int32(r.fbW), int32(r.fbH))
	r.applyClip()
	g.Enable(gl.BLEND)
	r.useDraw()
}

// evictImages lets go of textures the window has stopped drawing, and
// of the least recently drawn when they fill the budget.
func (r *Renderer) evictImages() {
	now := time.Now()
	total := 0
	for m, t := range r.images {
		if now.Sub(t.used) > imageIdle {
			r.dropImage(m)
			continue
		}
		total += t.bytes
	}
	for total > imageBudget {
		var oldest *paint.Image
		for m, t := range r.images {
			if oldest == nil || t.used.Before(r.images[oldest].used) {
				oldest = m
			}
		}
		total -= r.images[oldest].bytes
		r.dropImage(oldest)
	}
}

func (r *Renderer) dropImage(m *paint.Image) {
	r.GL.DeleteTexture(r.images[m].tex)
	delete(r.images, m)
}
