package paint

import "image/color"

// RGB is the opaque colour r, g, b.
func RGB(r, g, b uint8) color.NRGBA { return color.NRGBA{R: r, G: g, B: b, A: 0xff} }

// Faded is c at a share a of its own alpha, from 0 to 1.
func Faded(c color.NRGBA, a float32) color.NRGBA {
	c.A = uint8(float32(c.A)*min(max(a, 0), 1) + 0.5)
	return c
}
