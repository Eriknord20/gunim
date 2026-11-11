//go:build linux || windows || darwin || (js && wasm)

package render

import (
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/paint"
)

// A settled mask bigger than settledMost gets a texture of its own, at its device size, so it draws as sharp as a
// small one and leaves the atlas to text. Rasterizing one costs milliseconds, so while its size keeps changing, as
// under a window being resized, it draws its nearest size stretched and is drawn again at the new size once the size
// holds for a frame, or every bigMaskStale while it never does.
const (
	// bigMaskSettle is how long after a frame that stretched a big mask the frame that draws it sharp comes.
	bigMaskSettle = 50 * time.Millisecond
	// bigMaskStale is the longest a big mask whose size keeps changing draws stretched before it is rasterized
	// again.
	bigMaskStale = 250 * time.Millisecond
	// bigMaskBudget is how many bytes of big mask textures a window keeps before it lets go of the ones it drew
	// longest ago.
	bigMaskBudget = 64 << 20
)

// bigMask is one size of a big mask, uploaded to the GPU.
type bigMask struct {
	key  glyphKey
	tex  uint32
	used time.Time
}

// bigMasks holds a renderer's big masks: every size of each shape, when each shape was last rasterized, and the
// sizes the last frame drew stretched.
type bigMasks struct {
	sizes map[paint.Shape][]*bigMask
	made  map[paint.Shape]time.Time
	// asked holds the sizes this frame drew stretched, and wanted those the frame before it did.
	asked, wanted map[glyphKey]bool
}

// newFrame starts a frame's record of the sizes it draws stretched.
func (b *bigMasks) newFrame() {
	b.wanted, b.asked = b.asked, b.wanted
	clear(b.asked)
}

// bigMask returns the texture that draws shape at w by h device pixels: its own, or that of its nearest size, which
// is then stretched, so stretched is set.
func (r *Renderer) bigMask(shape paint.Shape, w, h int) (tex uint32, stretched bool) {
	b := &r.bigs
	if b.sizes == nil {
		b.sizes, b.made = map[paint.Shape][]*bigMask{}, map[paint.Shape]time.Time{}
		b.asked, b.wanted = map[glyphKey]bool{}, map[glyphKey]bool{}
	}
	key := glyphKey{shape: shape, w: int32(w), h: int32(h)}
	sizes := b.sizes[shape]
	var near *bigMask
	for _, m := range sizes {
		if m.key == key {
			m.used = r.now
			return m.tex, false
		}
		if near == nil || abs(m.key.w-key.w)+abs(m.key.h-key.h) < abs(near.key.w-key.w)+abs(near.key.h-key.h) {
			near = m
		}
	}
	if near != nil && !b.wanted[key] && r.now.Sub(b.made[shape]) < bigMaskStale {
		near.used = r.now
		b.asked[key] = true
		return near.tex, true
	}
	pix := shape.Coverage(w, h)
	if len(pix) != w*h {
		return 0, false
	}
	g := r.GL
	m := &bigMask{key: key, tex: g.CreateTexture(), used: r.now}
	g.ActiveTexture(glTexture1)
	g.BindTexture(gl.TEXTURE_2D, m.tex)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, glLinear)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, glLinear)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	g.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	g.TexImage2D(gl.TEXTURE_2D, 0, glR8, int32(w), int32(h), glRed, gl.UNSIGNED_BYTE, pix)
	g.ActiveTexture(gl.TEXTURE0)
	// The batch may hold a quad reading whatever was on unit 1; flush binds it again.
	b.sizes[shape] = append(sizes, m)
	b.made[shape] = r.now
	return m.tex, false
}

func abs(v int32) int32 { return max(v, -v) }

// stretched notes that the frame drew a big mask stretched over box, in device pixels, so the frame to come at
// SharpAt draws it sharp.
func (r *Renderer) stretched(box geom.Rect) {
	// A pixel more each way holds the edge the snap to the pixel grid moved.
	area := geom.Rect{Min: box.Min.Sub(geom.Pt(1, 1)).Mul(1 / r.scale), Max: box.Max.Add(geom.Pt(1, 1)).Mul(1 / r.scale)}
	if r.Stretched.Empty() {
		r.Stretched, r.SharpAt = area, r.now.Add(bigMaskSettle)
		return
	}
	r.Stretched = r.Stretched.Union(area)
}

// evictBigMasks lets go of big masks the window has stopped drawing, and of the least recently drawn when they fill
// the budget.
func (r *Renderer) evictBigMasks() {
	total := 0
	var oldest *bigMask
	for shape, sizes := range r.bigs.sizes {
		kept := sizes[:0]
		for _, m := range sizes {
			if r.now.Sub(m.used) > imageIdle {
				r.GL.DeleteTexture(m.tex)
				continue
			}
			kept = append(kept, m)
			total += int(m.key.w) * int(m.key.h)
			if oldest == nil || m.used.Before(oldest.used) {
				oldest = m
			}
		}
		r.setSizes(shape, kept)
	}
	if total > bigMaskBudget && oldest != nil {
		// One a frame: a frame that fills the budget is rare, and the next lets go of the next oldest.
		r.dropBigMask(oldest)
	}
}

// setSizes keeps sizes as shape's big masks, or forgets shape when there are none.
func (r *Renderer) setSizes(shape paint.Shape, sizes []*bigMask) {
	if len(sizes) == 0 {
		delete(r.bigs.sizes, shape)
		delete(r.bigs.made, shape)
		return
	}
	r.bigs.sizes[shape] = sizes
}

func (r *Renderer) dropBigMask(m *bigMask) {
	r.GL.DeleteTexture(m.tex)
	sizes := r.bigs.sizes[m.key.shape]
	for i, s := range sizes {
		if s == m {
			r.setSizes(m.key.shape, append(sizes[:i:i], sizes[i+1:]...))
			return
		}
	}
}

// releaseBigMasks frees every big mask's texture.
func (r *Renderer) releaseBigMasks() {
	for _, sizes := range r.bigs.sizes {
		for _, m := range sizes {
			r.GL.DeleteTexture(m.tex)
		}
	}
	r.bigs = bigMasks{}
}

// rebase moves the times the renderer keeps on by d, as its clock changes from the wall's to a held one or back:
// when each big mask was made and drawn, and when what was drawn stretched turns sharp.
func (r *Renderer) rebase(d time.Duration) {
	for _, sizes := range r.bigs.sizes {
		for _, m := range sizes {
			m.used = m.used.Add(d)
		}
	}
	for shape, t := range r.bigs.made {
		r.bigs.made[shape] = t.Add(d)
	}
	if !r.SharpAt.IsZero() {
		r.SharpAt = r.SharpAt.Add(d)
	}
}
