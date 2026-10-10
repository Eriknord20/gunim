package paint

import (
	"fmt"
	"image/color"
	"reflect"

	"github.com/marrasen/gunim/geom"
)

// A Shape is artwork a driver rasterizes into a coverage mask and tints with a colour, such as an icon. It must be
// comparable, as a driver keys the masks it keeps by the shape and the size in pixels.
type Shape interface {
	// Coverage returns w*h bytes, row by row from the top, of the shape drawn to fill w by h device pixels: 0 outside,
	// 255 inside.
	Coverage(w, h int) []byte
	// Settled reports whether the shape stays as it is, so a driver may keep its coverage for later frames.
	Settled() bool
}

// MaskOp draws a Shape's coverage into Rect, tinted by Color, or by
// Gradient where it is set, in the same space as Rect.
type MaskOp struct {
	Shape     Shape
	Rect      geom.Rect
	Color     color.NRGBA
	Gradient  *Gradient
	Transform Transform
	// Blend is how the tinted mask meets what is beneath it, set from
	// the blend in force as the op is recorded; see [Painter.Blend].
	Blend Blend
}

func (*MaskOp) isOp() {}

// takeMask takes a MaskOp from the painter's blocks, as takeRRect does
// a shape, so a frame of hundreds of icons and sprites allocates none.
func (p *Painter) takeMask(op MaskOp) *MaskOp {
	v := p.masks.take()
	*v = op
	return v
}

// Mask records s drawn into r and tinted by c. A driver snaps r to whole device pixels while the transform in force
// only moves it.
func (p *Painter) Mask(s Shape, r geom.Rect, c color.NRGBA) {
	if s == nil || c.A == 0 || r.Empty() {
		return
	}
	if !reflect.TypeOf(s).Comparable() {
		panic(fmt.Sprintf("paint: Mask of %T, a shape that is not comparable", s))
	}
	p.record(p.takeMask(MaskOp{Shape: s, Rect: r, Color: c, Transform: p.at(), Blend: p.blendNow()}), r)
}

// MaskFill records s drawn into r and coloured by f, which may be a
// gradient, as a shape such as a hat or a lock of hair shades from
// light to dark. The gradient's points are in the same space as r.
func (p *Painter) MaskFill(s Shape, r geom.Rect, f Fill) {
	if f.Gradient == nil {
		p.Mask(s, r, f.Solid)
		return
	}
	if s == nil || r.Empty() {
		return
	}
	if !reflect.TypeOf(s).Comparable() {
		panic(fmt.Sprintf("paint: Mask of %T, a shape that is not comparable", s))
	}
	p.record(p.takeMask(MaskOp{Shape: s, Rect: r, Gradient: f.Gradient, Transform: p.at(), Blend: p.blendNow()}), r)
}
