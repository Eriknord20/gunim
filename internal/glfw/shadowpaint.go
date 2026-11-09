// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

// gunim change: this file is gunim's. It paints the pixels of the shadow and border win32_shadow_windows.go shows
// round a chromeless window. It has no Windows calls, so it is tested and timed on every system.

package glfw

import "math"

// shadowLook is what a drawn shadow is painted for: a window of winW by winH pixels at k times 96 dpi, with corners
// of radius corner in logical pixels, drawn scale times its size about its middle, active or not, and a border edge
// pixels wide of colour bc, premultiplied, 0 to 255.
type shadowLook struct {
	winW, winH int32
	k          float64
	corner     float64
	scale      float32
	active     bool
	edge       float64
	bc         [4]float64
}

// shadowMargins are how far the shadow reaches past the window's sides, top and bottom, in pixels, at k times 96 dpi.
func shadowMargins(k float64) (side, top, bottom int32) {
	return int32(50*k + 0.5), int32(31*k + 0.5), int32(73*k + 0.5)
}

// paintShadow paints the shadow and border into px, a premultiplied bitmap w pixels wide, with the window's top left
// at side, top: a rectangle offset downwards and blurred, outside the window, and the border in the window's edge,
// which the window leaves clear. It returns how far in from the window's left and top edges the bitmap draws.
//
// It runs on every frame of a window's coming and going, as the window shrinks, so it does no more than a few sums a
// pixel: everything that needs Erf or Exp is worked out once a column and once a row.
func paintShadow(px []uint32, w, h, side, top int32, l shadowLook) (inX, inY int32) {
	k := l.k
	// Fitted to the shadow Windows 11 draws round an active window, and a weaker, shorter one round an inactive one
	sigma, drop, inset, radius, strength := 22.7*k, 21.3*k, 2*k, l.corner*k, 0.366
	if !l.active {
		sigma, strength = 18.7*k, 0.25
	}
	winW, winH := l.winW, l.winH
	// The window's rectangle in the bitmap
	x0, y0, x1, y1 := side, top, side+winW, top+winH
	// As the window comes and goes it is drawn smaller about its middle, and so are its corners and border
	sc := float64(l.scale)
	if sc <= 0 {
		sc = 1
	}
	mx, my := float64(x0+x1)/2, float64(y0+y1)/2
	hw, hh := float64(winW)*sc/2, float64(winH)*sc/2
	fx0, fy0, fx1, fy1 := mx-hw, my-hh, mx+hw, my+hh
	radius, edge := min(radius*sc, hw, hh), l.edge*sc
	bc := l.bc
	x0, y0, x1, y1 = int32(math.Ceil(fx0)), int32(math.Ceil(fy0)), int32(fx1), int32(fy1)
	// A blurred rectangle is the product of a blurred edge across and a blurred edge down
	s2 := sigma * math.Sqrt2
	blur := func(p, a, b float64) float64 { return 0.5 * (math.Erf((p-a)/s2) - math.Erf((p-b)/s2)) }
	across := make([]float64, w)
	for x := range across {
		across[x] = blur(float64(x)+0.5, fx0+inset, fx1-inset)
	}
	// Rows and columns this far inside the window are covered by it whole
	reach := int32(max(radius, edge)) + 2
	inX, inY = min(x0-side+reach+1, winW/2), min(y0-top+reach+1, winH/2)
	// The shadow is cast by the window's rounded shape, the blurred rectangle less what its corners cut away
	cut := newCornerCut(fx0+inset, fy0+drop, fx1-inset, fy1+drop, max(radius-inset, 0), sigma, w, h)
	shade := func(x, y int32, square float64) float64 {
		return max(square-strength*cut.at(x, y), 0)
	}
	paintRow := func(y int32) {
		down := strength * blur(float64(y)+0.5, fy0+drop, fy1+drop)
		row := px[y*w : (y+1)*w]
		pixel := func(x int32) uint32 {
			cx, cy := float64(x)+0.5, float64(y)+0.5
			outer := min(max(0.5-sdRRect(cx, cy, fx0, fy0, fx1, fy1, radius), 0), 1)
			inner := min(max(0.5-sdRRect(cx, cy, fx0+edge, fy0+edge, fx1-edge, fy1-edge, max(radius-edge, 0)), 0), 1)
			if inner >= 1 {
				return 0
			}
			// The border lies between the window's edge and its content
			border := (outer - inner) * bc[3] / 0xff
			a := border
			if outer < 1 {
				a += shade(x, y, down*across[x]) * (1 - outer)
			}
			r := uint32(bc[0]*border + 0.5)
			g := uint32(bc[1]*border + 0.5)
			b := uint32(bc[2]*border + 0.5)
			return uint32(a*255+0.5)<<24 | r<<16 | g<<8 | b
		}
		// Outside the window's rectangle there is only shadow, and inside it, only near its edge and corners, the
		// window's shape and border
		shadow := func(x int32) uint32 { return uint32(shade(x, y, down*across[x])*255+0.5) << 24 }
		if y < y0-1 || y > y1 {
			for x := range row {
				row[x] = shadow(int32(x))
			}
			return
		}
		middle := y >= y0+reach && y < y1-reach
		for x := int32(0); x < w; x++ {
			switch {
			case x < x0-1 || x > x1:
				row[x] = shadow(x)
			case !middle || x < x0+reach || x >= x1-reach:
				row[x] = pixel(x)
			default:
				// The window covers the rest of the row up to its far edge
				end := min(x1-reach, w)
				clear(row[x:end])
				x = end - 1
			}
		}
	}
	for y := range h {
		paintRow(y)
	}
	return inX, inY
}

// cornerSteps is how many rows of each corner's rounding the corner cut sums.
const cornerSteps = 12

// cornerBands is how many rows the corner cut sums: the top corners' and then the bottom's.
const cornerBands = 2 * cornerSteps

// cornerCut is how much less of the shadow falls at each pixel than a square rectangle's, for the blurred rectangle
// x0, y0, x1, y1 with corners rounded by r, blurred by sigma: the rows the corners round, each blurred across by the
// width its corner cuts away, and summed under the blur down. It is 0 away from the corners.
//
// Each of those rows adds its weight at the pixel's row times its cut at the pixel's column, so both are worked out
// once, for the rows and columns near a corner: rows holds each pixel row's weights, and cols each column's cuts,
// cornerBands to a row or column. from and to are the rows of the sum that weigh anything at each pixel row.
type cornerCut struct {
	none       bool
	nearX      []bool
	nearY      []bool
	rows, cols []float64
	from, to   []uint8
}

// cornerFaint is a weight too small to change a pixel.
const cornerFaint = 1e-9

func newCornerCut(x0, y0, x1, y1, r, sigma float64, w, h int32) *cornerCut {
	c := &cornerCut{none: r <= 0}
	if c.none {
		return c
	}
	reach := 4 * sigma
	c.nearX, c.nearY = make([]bool, w), make([]bool, h)
	for x := range c.nearX {
		p := float64(x) + 0.5
		c.nearX[x] = p <= x0+r+reach || p >= x1-r-reach
	}
	for y := range c.nearY {
		p := float64(y) + 0.5
		c.nearY[y] = p <= y0+r+reach || p >= y1-r-reach
	}
	// Each row of the sum: where it lies, its height, and how far in its corner cuts
	var bandY, bandDy, bandOff [cornerBands]float64
	i := 0
	for _, band := range [2][2]float64{{y0, y0 + r}, {y1 - r, y1}} {
		dy := (band[1] - band[0]) / cornerSteps
		for s := range cornerSteps {
			y := band[0] + (float64(s)+0.5)*dy
			in := min(y-y0, y1-y)
			bandY[i], bandDy[i], bandOff[i] = y, dy, r-math.Sqrt(max(r*r-(r-in)*(r-in), 0))
			i++
		}
	}
	s2 := sigma * math.Sqrt2
	edge := func(p, a, b float64) float64 { return 0.5 * (math.Erf((p-a)/s2) - math.Erf((p-b)/s2)) }
	c.cols = make([]float64, cornerBands*int(w))
	for x, near := range c.nearX {
		if !near {
			continue
		}
		p := float64(x) + 0.5
		full := edge(p, x0, x1)
		col := c.cols[x*cornerBands : (x+1)*cornerBands]
		for i := range col {
			col[i] = full - edge(p, x0+bandOff[i], x1-bandOff[i])
		}
	}
	c.rows = make([]float64, cornerBands*int(h))
	c.from, c.to = make([]uint8, h), make([]uint8, h)
	norm := 1 / (sigma * math.Sqrt(2*math.Pi))
	for y, near := range c.nearY {
		if !near {
			continue
		}
		p := float64(y) + 0.5
		row := c.rows[y*cornerBands : (y+1)*cornerBands]
		from, to := cornerBands, 0
		for i := range row {
			t := (bandY[i] - p) / sigma
			row[i] = math.Exp(-t*t/2) * norm * bandDy[i]
			if row[i] > cornerFaint {
				from, to = min(from, i), i+1
			}
		}
		c.from[y], c.to[y] = uint8(from), uint8(max(to, from))
	}
	return c
}

// at is the cut at the pixel x, y.
func (c *cornerCut) at(x, y int32) float64 {
	if c.none || !c.nearX[x] || !c.nearY[y] {
		return 0
	}
	from, to := int(c.from[y]), int(c.to[y])
	row := c.rows[int(y)*cornerBands:][from:to]
	col := c.cols[int(x)*cornerBands:][from:to]
	var sum float64
	for i, wt := range row {
		sum += wt * col[i]
	}
	return sum
}

// sdRRect is the signed distance from (px, py) to the rectangle x0, y0, x1, y1 with corners rounded by r: negative
// inside.
func sdRRect(px, py, x0, y0, x1, y1, r float64) float64 {
	hx, hy := (x1-x0)/2, (y1-y0)/2
	r = min(r, hx, hy)
	qx := math.Abs(px-(x0+x1)/2) - hx + r
	qy := math.Abs(py-(y0+y1)/2) - hy + r
	return math.Hypot(max(qx, 0), max(qy, 0)) + min(max(qx, qy), 0) - r
}
