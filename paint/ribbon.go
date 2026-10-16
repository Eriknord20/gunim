package paint

import (
	"image/color"
	"math"
	"slices"

	"github.com/marrasen/gunim/geom"
)

// RibbonPoint is a point along a ribbon's middle line: where it is, how
// wide the ribbon is there, and its colour there.
type RibbonPoint struct {
	At geom.Point
	// Width is the ribbon's width across, in the op's own space. Zero
	// pinches the ribbon to nothing, as a ribbon seen edge on.
	Width float32
	Color color.NRGBA
}

// RibbonOp draws a ribbon through Points: a band that runs along the
// line through them, as wide as each point's Width, and coloured by each
// point's Color, both changing evenly from one point to the next. The
// colour changes premultiplied by its alpha, so a point of alpha 0 fades
// the ribbon out without darkening it. Its edges are smooth, and its
// ends cut square across.
type RibbonOp struct {
	Points    []RibbonPoint
	Transform Transform
	// Blend is how the ribbon meets what is beneath it, set from the
	// blend in force as the op is recorded; see [Painter.Blend].
	Blend Blend
}

func (*RibbonOp) isOp() {}

// RibbonMiter is how far out a ribbon's corner reaches at most, at a
// sharp bend, as a multiple of its half width there. A bend sharper than
// that is cut short, and the ribbon is a little thinner at it.
const RibbonMiter = 2

// Ribbon records a ribbon through pts, a line the caller has sampled
// finely enough to look smooth: a curve of a few dozen to a couple of
// hundred points. A ribbon of fewer than two points draws nothing.
//
// The op holds its own copy of pts, so the caller may change its slice
// once Ribbon returns. The copy is made into a buffer the painter keeps
// from frame to frame, so a ribbon drawn every frame allocates nothing
// once the buffer has grown to the frame's points.
func (p *Painter) Ribbon(pts []RibbonPoint) {
	if len(pts) < 2 {
		return
	}
	op := p.ribbons.take()
	*op = RibbonOp{Points: p.copyPoints(pts), Transform: p.at(), Blend: p.blendNow()}
	p.record(op, ribbonBounds(pts, op.Transform))
}

// takeRibbon takes a RibbonOp from the painter's blocks, a copy of op
// with its points copied into this frame's point buffer.
func (p *Painter) takeRibbon(op *RibbonOp, pts []RibbonPoint) *RibbonOp {
	v := p.ribbons.take()
	*v = *op
	v.Points = p.copyPoints(pts)
	return v
}

// copyPoints copies pts to the end of this frame's point buffer and
// returns the copy, as copyItems does a scene's items.
func (p *Painter) copyPoints(pts []RibbonPoint) []RibbonPoint {
	at := len(p.points)
	p.points = append(p.points, pts...)
	return p.points[at:len(p.points):len(p.points)]
}

// ribbonFeather is how far a driver draws a ribbon past its edges and
// ends, in device pixels, for their smoothing. Bounds take a device
// pixel as no more than a logical one, as it is at a scale of 1 and up.
const ribbonFeather = 1

// ribbonBounds is the box a ribbon through pts, drawn through t, can
// touch, in its own space: the box round its points, grown by the
// furthest its corners reach out from them. A driver draws the width
// scaled by the square root of t's determinant, the mean of its two
// scales; the box grows by that reach taken back into the op's space
// through t's least scale, which a turn or an uneven scale leaves as
// the one that shrinks a distance most. record grows it further.
func ribbonBounds(pts []RibbonPoint, t Transform) geom.Rect {
	b := geom.Rect{Min: pts[0].At, Max: pts[0].At}
	var widest float32
	for _, q := range pts {
		b.Min = geom.Pt(min(b.Min.X, q.At.X), min(b.Min.Y, q.At.Y))
		b.Max = geom.Pt(max(b.Max.X, q.At.X), max(b.Max.Y, q.At.Y))
		widest = max(widest, q.Width)
	}
	det := math.Abs(float64(t.A*t.E - t.B*t.D))
	sum := float64(t.A*t.A + t.B*t.B + t.D*t.D + t.E*t.E)
	most := math.Sqrt((sum + math.Sqrt(max(sum*sum-4*det*det, 0))) / 2)
	if det == 0 || most == 0 {
		// Folded flat, the ribbon has no width to draw.
		return b
	}
	least := det / most
	reach := (float64(widest)/2*math.Sqrt(det) + ribbonFeather) * RibbonMiter
	return grow(b, float32(reach/least))
}

// grow returns r grown by d on every side.
func grow(r geom.Rect, d float32) geom.Rect {
	return geom.Rect{Min: geom.Pt(r.Min.X-d, r.Min.Y-d), Max: geom.Pt(r.Max.X+d, r.Max.Y+d)}
}

// sameRibbon reports whether two ribbon ops draw the same.
func sameRibbon(a, b *RibbonOp) bool {
	return a.Transform == b.Transform && a.Blend == b.Blend && slices.Equal(a.Points, b.Points)
}
