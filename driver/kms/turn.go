//go:build linux && (arm64 || amd64)

package kms

import "github.com/marrasen/gunim/geom"

// turn is how far the picture is turned on the panel, clockwise: 0, 90,
// 180 or 270 degrees. The panel's own way up is 0.
type turn int

// valid reports whether t is one of the four turns.
func (t turn) valid() bool { return t == 0 || t == 90 || t == 180 || t == 270 }

// sideways says the picture lies across the panel, as landscape on a
// portrait panel does.
func (t turn) sideways() bool { return t == 90 || t == 270 }

// size returns the picture's size for a panel of w×h pixels.
func (t turn) size(w, h int) (pw, ph int) {
	if t.sideways() {
		return h, w
	}
	return w, h
}

// toPicture takes a point on a panel of w×h pixels, from its top left, to
// the picture's pixels from its top left.
//
// A picture turned 90° clockwise has its top along the panel's right edge,
// so its top left lies at the panel's top right.
func (t turn) toPicture(p geom.Point, w, h float32) geom.Point {
	switch t {
	case 90:
		return geom.Pt(p.Y, w-p.X)
	case 180:
		return geom.Pt(w-p.X, h-p.Y)
	case 270:
		return geom.Pt(h-p.Y, p.X)
	}
	return p
}

// toPanel is toPicture the other way: a point of the picture to the panel.
func (t turn) toPanel(p geom.Point, w, h float32) geom.Point {
	switch t {
	case 90:
		return geom.Pt(w-p.Y, p.X)
	case 180:
		return geom.Pt(w-p.X, h-p.Y)
	case 270:
		return geom.Pt(p.Y, h-p.X)
	}
	return p
}

// matrix returns the turn as a column-major 2×2 matrix on GL's clip space,
// whose y runs up: clockwise by t on the screen.
func (t turn) matrix() [4]float32 {
	switch t {
	case 90:
		// (x, y) to (y, -x).
		return [4]float32{0, -1, 1, 0}
	case 180:
		return [4]float32{-1, 0, 0, -1}
	case 270:
		// (x, y) to (-y, x).
		return [4]float32{0, 1, -1, 0}
	}
	return [4]float32{1, 0, 0, 1}
}
