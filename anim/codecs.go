package anim

import (
	"image/color"
	"math"

	"github.com/marrasen/gunim/geom"
)

// These aliases name the values a widget animates, so a field
// declaration reads as `hover *anim.Float`.

// Float is an animated number: an opacity, an angle, a 0..1 progress.
type Float = Animated[float32]

// Point is an animated position.
type Point = Animated[geom.Point]

// Size is an animated width and height.
type Size = Animated[geom.Size]

// Rect is an animated rectangle.
type Rect = Animated[geom.Rect]

// Color is an animated colour.
type Color = Animated[color.NRGBA]

// FloatCodec animates a plain number: an opacity, an angle, a 0..1
// progress value driving everything else.
var FloatCodec = Codec[float32]{
	N:      1,
	Encode: func(v float32) (d [MaxScalars]float32) { d[0] = v; return d },
	Decode: func(s [MaxScalars]float32) float32 { return s[0] },
}

// PointCodec animates a position as two independent springs.
var PointCodec = Codec[geom.Point]{
	N: 2,
	Encode: func(v geom.Point) (d [MaxScalars]float32) {
		d[0], d[1] = v.X, v.Y
		return d
	},
	Decode: func(s [MaxScalars]float32) geom.Point {
		return geom.Point{X: s[0], Y: s[1]}
	},
}

// SizeCodec animates a width and height.
var SizeCodec = Codec[geom.Size]{
	N: 2,
	Encode: func(v geom.Size) (d [MaxScalars]float32) {
		d[0], d[1] = v.W, v.H
		return d
	},
	Decode: func(s [MaxScalars]float32) geom.Size {
		return geom.Size{W: s[0], H: s[1]}
	},
}

// RectCodec animates a rectangle's two corners, which is what you want
// for a focus ring or a selection that slides between targets.
var RectCodec = Codec[geom.Rect]{
	N: 4,
	Encode: func(v geom.Rect) (d [MaxScalars]float32) {
		d[0], d[1], d[2], d[3] = v.Min.X, v.Min.Y, v.Max.X, v.Max.Y
		return d
	},
	Decode: func(s [MaxScalars]float32) geom.Rect {
		return geom.Rect{Min: geom.Point{X: s[0], Y: s[1]}, Max: geom.Point{X: s[2], Y: s[3]}}
	},
}

// ColorCodec animates a colour through Oklab, a colour space built so
// equal steps look equal. A blend passes through the hues and
// lightnesses a person would expect, where blending the stored sRGB
// values dulls saturated colours to a muddy middle. Alpha blends on its
// own, straight.
var ColorCodec = Codec[color.NRGBA]{
	N: 4,
	Encode: func(v color.NRGBA) (d [MaxScalars]float32) {
		d[0], d[1], d[2] = toOklab(v)
		d[3] = float32(v.A)
		return d
	},
	Decode: func(s [MaxScalars]float32) color.NRGBA {
		r, g, b := fromOklab(s[0], s[1], s[2])
		return color.NRGBA{R: r, G: g, B: b, A: clampByte(s[3])}
	},
}

// SRGBCodec animates a colour channel by channel, on the stored sRGB
// values. It is cheaper than [ColorCodec] and dulls the middle of a
// blend between saturated colours.
var SRGBCodec = Codec[color.NRGBA]{
	N: 4,
	Encode: func(v color.NRGBA) (d [MaxScalars]float32) {
		d[0], d[1], d[2], d[3] = float32(v.R), float32(v.G), float32(v.B), float32(v.A)
		return d
	},
	Decode: func(s [MaxScalars]float32) color.NRGBA {
		return color.NRGBA{R: clampByte(s[0]), G: clampByte(s[1]), B: clampByte(s[2]), A: clampByte(s[3])}
	},
}

// toOklab converts an sRGB colour to Oklab's L, a and b.
func toOklab(c color.NRGBA) (l, a, b float32) {
	r, g, bl := toLinear(c.R), toLinear(c.G), toLinear(c.B)
	lm := cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*bl)
	mm := cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*bl)
	sm := cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*bl)
	return 0.2104542553*lm + 0.7936177850*mm - 0.0040720468*sm,
		1.9779984951*lm - 2.4285922050*mm + 0.4505937099*sm,
		0.0259040371*lm + 0.7827717662*mm - 0.8086757660*sm
}

// fromOklab converts Oklab back to sRGB, clamping what falls outside
// its gamut.
func fromOklab(l, a, b float32) (r, g, bl uint8) {
	lm := l + 0.3963377774*a + 0.2158037573*b
	mm := l - 0.1055613458*a - 0.0638541728*b
	sm := l - 0.0894841775*a - 1.2914855480*b
	lm, mm, sm = lm*lm*lm, mm*mm*mm, sm*sm*sm
	return fromLinear(4.0767416621*lm - 3.3077115913*mm + 0.2309699292*sm),
		fromLinear(-1.2684380046*lm + 2.6097574011*mm - 0.3413193965*sm),
		fromLinear(-0.0041960863*lm - 0.7034186147*mm + 1.7076147010*sm)
}

// toLinear turns an sRGB channel into linear light. It reads a table
// made once with [srgbToLinear], since a channel has only 256 values.
func toLinear(c uint8) float32 { return linearOf[c] }

// fromLinear turns linear light back into an sRGB channel. It looks up
// the first level whose threshold v falls below, in place of raising v
// to a power, and gives exactly what [linearToSRGB] gives.
func fromLinear(v float32) uint8 {
	// Count the thresholds at or below v by binary search.
	lo, hi := 0, len(srgbSteps)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if srgbSteps[mid] <= v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return uint8(lo)
}

var (
	// linearOf holds srgbToLinear for every channel value.
	linearOf [256]float32
	// srgbSteps holds, for each level k from 1 to 255, the smallest
	// float32 that linearToSRGB turns into k or more, at index k-1.
	srgbSteps [255]float32
)

func init() {
	for c := range linearOf {
		linearOf[c] = srgbToLinear(uint8(c))
	}
	for k := range srgbSteps {
		srgbSteps[k] = firstReaching(uint8(k + 1))
	}
}

// firstReaching returns the smallest float32 in 0..1 that linearToSRGB
// turns into level k or more. It bisects on the bit patterns, which
// order positive float32s as their values.
func firstReaching(k uint8) float32 {
	lo, hi := uint32(0), math.Float32bits(1) // linearToSRGB(1) is 255
	for lo < hi {
		mid := lo + (hi-lo)/2
		if linearToSRGB(math.Float32frombits(mid)) >= k {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return math.Float32frombits(lo)
}

func srgbToLinear(c uint8) float32 {
	v := float64(c) / 255
	if v <= 0.04045 {
		return float32(v / 12.92)
	}
	return float32(math.Pow((v+0.055)/1.055, 2.4))
}

func linearToSRGB(v float32) uint8 {
	x := float64(max(0, min(v, 1)))
	if x <= 0.0031308 {
		x *= 12.92
	} else {
		x = 1.055*math.Pow(x, 1/2.4) - 0.055
	}
	return clampByte(float32(x * 255))
}

func cbrt(v float32) float32 { return float32(math.Cbrt(float64(v))) }

// NewFloat returns an animated number holding v, at rest.
func NewFloat(v float32) *Float { return New(v, FloatCodec) }

// NewPoint returns an animated position holding v, at rest.
func NewPoint(v geom.Point) *Point { return New(v, PointCodec) }

// NewSize returns an animated size holding v, at rest.
func NewSize(v geom.Size) *Size { return New(v, SizeCodec) }

// NewRect returns an animated rectangle holding v, at rest.
func NewRect(v geom.Rect) *Rect { return New(v, RectCodec) }

// NewColor returns an animated colour holding v, at rest.
func NewColor(v color.NRGBA) *Color { return New(v, ColorCodec) }

func clampByte(v float32) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	default:
		return uint8(v + 0.5)
	}
}

// Mix returns the value t of the way from a to b, blending each scalar
// the codec takes apart. t may leave 0..1, so a spring's overshoot
// carries through.
//
// It is how a widget turns an animated state into a look: animate the
// hover from 0 to 1, and Mix the theme's idle and hover colours by it
// each frame, so the colours can change underneath without the
// animation noticing.
func Mix[T any](c Codec[T], a, b T, t float32) T {
	// At either end the mix is an end: a widget at rest mixes its
	// colours at 0 or 1 every frame, and a colour's trip through Oklab
	// costs three cube roots.
	if t == 0 {
		return a
	}
	if t == 1 {
		return b
	}
	x, y := c.Encode(a), c.Encode(b)
	for i := range c.N {
		x[i] += (y[i] - x[i]) * t
	}
	return c.Decode(x)
}

// MixColor is t of the way from colour a to b, blended as [ColorCodec] blends.
func MixColor(a, b color.NRGBA, t float32) color.NRGBA { return Mix(ColorCodec, a, b, t) }

// OnOff is 1 for true and 0 for false, a state as a spring's target.
func OnOff(on bool) float32 {
	if on {
		return 1
	}
	return 0
}

// InsetsCodec animates the four sides of a padding or margin.
var InsetsCodec = Codec[geom.Insets]{
	N: 4,
	Encode: func(v geom.Insets) (d [MaxScalars]float32) {
		d[0], d[1], d[2], d[3] = v.Top, v.Right, v.Bottom, v.Left
		return d
	},
	Decode: func(s [MaxScalars]float32) geom.Insets {
		return geom.Insets{Top: s[0], Right: s[1], Bottom: s[2], Left: s[3]}
	},
}

// SpringCodec animates a spring's response and damping, so a theme can
// make every motion gradually snappier or softer.
var SpringCodec = Codec[Spring]{
	N: 2,
	Encode: func(v Spring) (d [MaxScalars]float32) {
		d[0], d[1] = v.Response, v.Damping
		return d
	},
	Decode: func(s [MaxScalars]float32) Spring {
		return Spring{Response: s[0], Damping: s[1]}
	},
}
