//go:build linux || windows || darwin

package render

import (
	"math"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
)

// settledMost is the most device pixels a settled mask is kept at in the atlas, across or down; a bigger one has a
// texture of its own, see bigmasks.go.
const settledMost = 256

// mask queues a shape's coverage, tinted by the op's colour, through the greyscale glyph atlas.
//
// A settled shape is rasterized once per device size and kept; one still changing is rasterized every frame into
// the scratch strip. Under a plain translation the mask snaps to whole device pixels. Under a scale or
// rotation it keeps its resting size and the quad carries the transform, as glyphs do.
func (r *Renderer) mask(op *paint.MaskOp) {
	if op.Shape == nil || op.Color.A == 0 && op.Gradient == nil {
		return
	}
	t := op.Transform
	size := op.Rect.Size()
	w, h := int(math.Round(float64(size.W*r.scale))), int(math.Round(float64(size.H*r.scale)))
	if w <= 0 || h <= 0 {
		return
	}
	o := t.Apply(op.Rect.Min)
	ox, oy := o.X*r.scale, o.Y*r.scale
	if t.A == 1 && t.B == 0 && t.D == 0 && t.E == 1 {
		ox, oy = float32(math.Round(float64(ox))), float32(math.Round(float64(oy)))
	}
	if op.Shape.Settled() && max(w, h) > settledMost {
		// A big mask has a texture of its own, so it neither overflows the atlas nor empties it of the text in
		// every window.
		tex, stretched := r.bigMask(op.Shape, w, h)
		if tex == 0 {
			return
		}
		if stretched {
			r.stretched(r.deviceRect(t, op.Rect))
		}
		r.uses(tex)
		l := r.maskLook(op, w, h)
		l.radius = 4
		r.quad(corners(geom.Rect{Max: geom.Pt(float32(w), float32(h))}, geom.Rect{Max: geom.Pt(1, 1)}),
			paint.Transform{A: t.A, B: t.B, C: ox, D: t.D, E: t.E, F: oy}, 1, &l)
		return
	}
	var slot glyphSlot
	if op.Shape.Settled() {
		key := glyphKey{shape: op.Shape, w: int32(w), h: int32(h)}
		s, ok := r.glyph(key, func() text.Mask {
			pix := op.Shape.Coverage(w, h)
			if len(pix) != w*h {
				return text.Mask{}
			}
			return text.Mask{Pix: pix, W: w, H: h}
		})
		if !ok {
			return
		}
		slot = s
	} else {
		// A mask too big for the strip is drawn smaller and stretched.
		sw, sh := w, h
		if big := max(w, h); big > scratchRows-1 {
			sw, sh = max(1, w*(scratchRows-1)/big), max(1, h*(scratchRows-1)/big)
		}
		pix := op.Shape.Coverage(sw, sh)
		if len(pix) != sw*sh {
			return
		}
		slot = r.scratch(pix, sw, sh)
	}
	r.uses(0)
	q := geom.Rect{Max: geom.Pt(float32(w), float32(h))}
	uv := geom.Rect{
		Min: geom.Pt(float32(slot.x)/atlasSize, float32(slot.y)/atlasSize),
		Max: geom.Pt(float32(slot.x+slot.w)/atlasSize, float32(slot.y+slot.h)/atlasSize),
	}
	l := r.maskLook(op, w, h)
	r.quad(corners(q, uv), paint.Transform{A: t.A, B: t.B, C: ox, D: t.D, E: t.E, F: oy}, 1, &l)
}

// scratch copies a mask w by h pixels into the scratch strip for this frame alone and returns its place. A full
// strip draws what is queued and starts again from its left.
func (r *Renderer) scratch(pix []byte, w, h int) glyphSlot {
	if r.scratchX+w+1 > atlasSize {
		r.flush()
		r.scratchX = 0
	}
	slot := glyphSlot{x: r.scratchX, y: atlasSize - scratchRows, w: w, h: h}
	r.scratchX += w + 1
	g := r.GL
	g.ActiveTexture(gl.TEXTURE0)
	g.BindTexture(gl.TEXTURE_2D, r.glyphs.tex)
	g.TexSubImage2D(gl.TEXTURE_2D, 0, int32(slot.x), int32(slot.y), int32(w), int32(h), glRed, gl.UNSIGNED_BYTE, pix)
	r.scratches++
	return slot
}

// maskLook is the look of op's mask, drawn w by h device pixels.
func (r *Renderer) maskLook(op *paint.MaskOp, w, h int) look {
	l := look{kind: kindGlyph, color0: rgba(op.Color), add: op.Blend == paint.BlendAdd}
	if gr := op.Gradient; gr != nil {
		// A corner's point is in device pixels of the mask; the
		// gradient's are in the space of its rectangle.
		size := op.Rect.Size()
		fx, fy := size.W/float32(w), size.H/float32(h)
		l.grad, l.mode = gr, r.rampMode(gr)
		l.toGrad = func(p geom.Point) geom.Point {
			return geom.Pt(op.Rect.Min.X+p.X*fx, op.Rect.Min.Y+p.Y*fy)
		}
	}
	return l
}
