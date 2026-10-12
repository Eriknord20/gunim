// Package icc reads the ICC profiles of displays and builds the table
// that converts sRGB colours to a display's own.
//
// It reads matrix/TRC profiles (an "RGB" profile with rXYZ, gXYZ, bXYZ
// and rTRC, gTRC, bTRC tags), which is what almost every display
// profile is, and what most profiles made with a measuring device carry
// beside their tables. A profile with only A2B/B2A tables is reported
// as unsupported.
package icc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode/utf16"
)

// ErrUnsupported is returned for a profile with no matrix and curves
// for an RGB display: one with only lookup tables, or of another kind.
var ErrUnsupported = errors.New("icc: profile has no RGB matrix and curves")

// XYZ is a colour in CIE XYZ.
type XYZ [3]float64

// Profile is the part of a display profile the conversion needs.
type Profile struct {
	// Description is the profile's name, from its desc tag.
	Description string
	// Version is the major version: 2 or 4.
	Version int
	// Class, ColorSpace and PCS are the header's signatures, such as
	// "mntr", "RGB " and "XYZ ".
	Class, ColorSpace, PCS string
	// Matrix holds the red, green and blue colorants, which take linear
	// display RGB to XYZ in the D50 connection space, as columns.
	Matrix [3][3]float64
	// TRC is each channel's curve, from display RGB to linear.
	TRC [3]Curve
	// White is the media white point, where the profile has one.
	White XYZ
	// HasMatrix says the profile has the matrix and curves; HasLUT that
	// it has an A2B0 or B2A0 table.
	HasMatrix, HasLUT bool
}

// D50 is the ICC connection space's white.
var D50 = XYZ{0.9642, 1.0, 0.8249}

// sRGBMatrix takes linear sRGB to XYZ D50, Bradford adapted from D65,
// as the colorants of the standard sRGB profile.
var sRGBMatrix = [3][3]float64{
	{0.4360747, 0.3850649, 0.1430804},
	{0.2225045, 0.7168786, 0.0606169},
	{0.0139322, 0.0971045, 0.7141733},
}

// Parse reads an ICC profile. It returns [ErrUnsupported], wrapped, for
// a profile that is not an RGB matrix/TRC one, with what was read so
// far, so the description can still be shown.
func Parse(b []byte) (*Profile, error) {
	if len(b) < 132 {
		return nil, errors.New("icc: too short for a profile")
	}
	if string(b[36:40]) != "acsp" {
		return nil, errors.New("icc: not a profile: no acsp signature")
	}
	if size := binary.BigEndian.Uint32(b[0:4]); int(size) < len(b) && size >= 132 {
		b = b[:size]
	}
	p := &Profile{
		Version:    int(b[8]),
		Class:      string(b[12:16]),
		ColorSpace: string(b[16:20]),
		PCS:        string(b[20:24]),
	}
	n := binary.BigEndian.Uint32(b[128:132])
	if n > 1000 || 132+int(n)*12 > len(b) {
		return nil, errors.New("icc: tag table runs past the end")
	}
	tags := map[string][]byte{}
	for i := range int(n) {
		e := b[132+i*12:]
		sig := string(e[0:4])
		off, size := binary.BigEndian.Uint32(e[4:8]), binary.BigEndian.Uint32(e[8:12])
		if uint64(off)+uint64(size) > uint64(len(b)) || size < 8 {
			return nil, fmt.Errorf("icc: tag %q runs past the end", sig)
		}
		tags[sig] = b[off : off+size]
	}

	if d, ok := tags["desc"]; ok {
		p.Description = description(d)
	}
	if w, ok := tags["wtpt"]; ok {
		if xyz, err := readXYZ(w); err == nil {
			p.White = xyz
		}
	}
	_, a2b := tags["A2B0"]
	_, b2a := tags["B2A0"]
	p.HasLUT = a2b || b2a

	if p.ColorSpace != "RGB " {
		return p, fmt.Errorf("%w: its colour space is %q", ErrUnsupported, p.ColorSpace)
	}
	names := [3][2]string{{"rXYZ", "rTRC"}, {"gXYZ", "gTRC"}, {"bXYZ", "bTRC"}}
	for i, nm := range names {
		x, okX := tags[nm[0]]
		t, okT := tags[nm[1]]
		if !okX || !okT {
			what := "no matrix and curves"
			if p.HasLUT {
				what = "only lookup tables"
			}
			return p, fmt.Errorf("%w: it has %s", ErrUnsupported, what)
		}
		xyz, err := readXYZ(x)
		if err != nil {
			return p, err
		}
		for row := range 3 {
			p.Matrix[row][i] = xyz[row]
		}
		if p.TRC[i], err = readCurve(t); err != nil {
			return p, fmt.Errorf("icc: %s: %w", nm[1], err)
		}
	}
	if _, ok := invert3(p.Matrix); !ok {
		return p, fmt.Errorf("%w: its colorants are not independent", ErrUnsupported)
	}
	p.HasMatrix = true
	p.adaptToD50()
	return p, nil
}

// adaptToD50 fixes a profile whose colorants were left at its own white
// rather than adapted to D50, as some old version 2 profiles are: the
// colorants' sum is its white, not D50. They are adapted with Bradford.
func (p *Profile) adaptToD50() {
	var sum XYZ
	for row := range 3 {
		sum[row] = p.Matrix[row][0] + p.Matrix[row][1] + p.Matrix[row][2]
	}
	if near(sum, D50, 0.01) || p.White == (XYZ{}) || !near(sum, p.White, 0.01) {
		return
	}
	a := bradford(p.White, D50)
	p.Matrix = mul3(a, p.Matrix)
}

func near(a, b XYZ, tol float64) bool {
	return math.Abs(a[0]-b[0]) <= tol && math.Abs(a[1]-b[1]) <= tol && math.Abs(a[2]-b[2]) <= tol
}

// bradford returns the matrix that adapts XYZ colours from white src
// to white dst.
func bradford(src, dst XYZ) [3][3]float64 {
	m := [3][3]float64{
		{0.8951, 0.2664, -0.1614},
		{-0.7502, 1.7135, 0.0367},
		{0.0389, -0.0685, 1.0296},
	}
	inv, _ := invert3(m)
	s, d := apply3(m, src), apply3(m, dst)
	var scale [3][3]float64
	for i := range 3 {
		scale[i][i] = d[i] / s[i]
	}
	return mul3(inv, mul3(scale, m))
}

// readXYZ reads an XYZType tag's first value.
func readXYZ(t []byte) (XYZ, error) {
	if len(t) < 20 || string(t[0:4]) != "XYZ " {
		return XYZ{}, errors.New("icc: not an XYZ tag")
	}
	return XYZ{s15(t[8:]), s15(t[12:]), s15(t[16:])}, nil
}

func s15(b []byte) float64 { return float64(int32(binary.BigEndian.Uint32(b))) / 65536 }

// description reads a desc tag: a version 2 textDescriptionType, or a
// version 4 multiLocalizedUnicodeType, taking English where there is a
// choice.
func description(t []byte) string {
	switch string(t[0:4]) {
	case "desc":
		if len(t) < 12 {
			return ""
		}
		n := int(binary.BigEndian.Uint32(t[8:12]))
		if n <= 0 || 12+n > len(t) {
			return ""
		}
		s := t[12 : 12+n]
		for i, c := range s {
			if c == 0 {
				s = s[:i]
				break
			}
		}
		return string(s)
	case "mluc":
		if len(t) < 16 {
			return ""
		}
		count, size := int(binary.BigEndian.Uint32(t[8:12])), int(binary.BigEndian.Uint32(t[12:16]))
		if size < 12 {
			return ""
		}
		pick := -1
		for i := range count {
			r := 16 + i*size
			if r+12 > len(t) {
				break
			}
			if pick < 0 || string(t[r:r+2]) == "en" {
				pick = r
				if string(t[r:r+2]) == "en" {
					break
				}
			}
		}
		if pick < 0 {
			return ""
		}
		n, off := int(binary.BigEndian.Uint32(t[pick+4:])), int(binary.BigEndian.Uint32(t[pick+8:]))
		if off+n > len(t) || n%2 != 0 {
			return ""
		}
		u := make([]uint16, n/2)
		for i := range u {
			u[i] = binary.BigEndian.Uint16(t[off+2*i:])
		}
		for len(u) > 0 && u[len(u)-1] == 0 {
			u = u[:len(u)-1]
		}
		return string(utf16.Decode(u))
	}
	return ""
}

// Curve is one channel's tone response, from a display value to linear
// light, both from 0 to 1.
type Curve struct {
	// Table holds the samples of a sampled curve, evenly spaced; when
	// empty, Fn and Params give the curve.
	Table []float64
	// Fn is the parametric function type, 0 to 4, and Params its g, a,
	// b, c, d, e and f, as ICC's parametricCurveType has them. A plain
	// gamma is type 0.
	Fn     int
	Params [7]float64
}

// Gamma returns a curve that is a plain power.
func Gamma(g float64) Curve { return Curve{Params: [7]float64{g}} }

// readCurve reads a curveType or parametricCurveType tag.
func readCurve(t []byte) (Curve, error) {
	switch string(t[0:4]) {
	case "curv":
		if len(t) < 12 {
			return Curve{}, errors.New("short curve")
		}
		n := int(binary.BigEndian.Uint32(t[8:12]))
		if 12+2*n > len(t) {
			return Curve{}, errors.New("curve runs past its tag")
		}
		switch n {
		case 0:
			return Gamma(1), nil
		case 1:
			return Gamma(float64(binary.BigEndian.Uint16(t[12:])) / 256), nil
		}
		c := Curve{Table: make([]float64, n)}
		for i := range n {
			c.Table[i] = float64(binary.BigEndian.Uint16(t[12+2*i:])) / 65535
		}
		return c, nil
	case "para":
		if len(t) < 12 {
			return Curve{}, errors.New("short parametric curve")
		}
		fn := int(binary.BigEndian.Uint16(t[8:10]))
		counts := [...]int{1, 3, 4, 5, 7}
		if fn >= len(counts) {
			return Curve{}, fmt.Errorf("unknown parametric curve type %d", fn)
		}
		if 12+4*counts[fn] > len(t) {
			return Curve{}, errors.New("parametric curve runs past its tag")
		}
		c := Curve{Fn: fn}
		for i := range counts[fn] {
			c.Params[i] = s15(t[12+4*i:])
		}
		return c, nil
	}
	return Curve{}, fmt.Errorf("unknown curve type %q", t[0:4])
}

// Eval returns the linear value for a display value x, clamped to 0..1.
func (c Curve) Eval(x float64) float64 {
	x = clamp01(x)
	if len(c.Table) > 0 {
		n := len(c.Table) - 1
		f := x * float64(n)
		i := min(int(f), n-1)
		return c.Table[i] + (c.Table[i+1]-c.Table[i])*(f-float64(i))
	}
	g, a, b, cc, d, e, f := c.Params[0], c.Params[1], c.Params[2], c.Params[3], c.Params[4], c.Params[5], c.Params[6]
	pow := func(v float64) float64 {
		if v <= 0 {
			return 0
		}
		return math.Pow(v, g)
	}
	var y float64
	switch c.Fn {
	case 0:
		y = pow(x)
	case 1:
		if x >= -b/a {
			y = pow(a*x + b)
		}
	case 2:
		y = cc
		if x >= -b/a {
			y = pow(a*x+b) + cc
		}
	case 3:
		y = cc * x
		if x >= d {
			y = pow(a*x + b)
		}
	case 4:
		y = cc*x + f
		if x >= d {
			y = pow(a*x+b) + e
		}
	}
	return clamp01(y)
}

// inverse returns a function that takes linear light back to the
// display value: the curve sampled finely, made monotone, and searched.
func (c Curve) inverse() func(float64) float64 {
	const n = 4096
	ys := make([]float64, n+1)
	for i := range ys {
		ys[i] = c.Eval(float64(i) / n)
		if i > 0 && ys[i] < ys[i-1] {
			ys[i] = ys[i-1]
		}
	}
	return func(y float64) float64 {
		if y <= ys[0] {
			return 0
		}
		if y >= ys[n] {
			return 1
		}
		lo, hi := 0, n
		for hi-lo > 1 {
			m := (lo + hi) / 2
			if ys[m] <= y {
				lo = m
			} else {
				hi = m
			}
		}
		t := 0.0
		if d := ys[hi] - ys[lo]; d > 0 {
			t = (y - ys[lo]) / d
		}
		return (float64(lo) + t) / n
	}
}

// SRGBToLinear is sRGB's transfer function, from an encoded value to
// linear light.
func SRGBToLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

// LUT is a table that takes sRGB colours to a display's: N steps along
// each axis, from 0 to 1, of RGB triples, red varying fastest, then
// green, then blue.
type LUT struct {
	N   int
	RGB []float32
}

// LUTSize is the number of steps along each axis of the tables
// [Profile.LUT] builds.
const LUTSize = 33

// LUT builds the table that converts sRGB to the profile's display RGB,
// with n steps along each axis. The profile must have its matrix.
//
// Each step goes from sRGB to linear light, to XYZ D50, through the
// inverse of the display's matrix to linear display RGB, which is
// clamped to the display's gamut, and through the inverse of each
// channel's curve.
func (p *Profile) LUT(n int) *LUT {
	toDisplay, _ := invert3(p.Matrix)
	m := mul3(toDisplay, sRGBMatrix)
	var inv [3]func(float64) float64
	for i := range inv {
		inv[i] = p.TRC[i].inverse()
	}
	lin := make([]float64, n)
	for i := range lin {
		lin[i] = SRGBToLinear(float64(i) / float64(n-1))
	}
	l := &LUT{N: n, RGB: make([]float32, 0, n*n*n*3)}
	for b := range n {
		for g := range n {
			for r := range n {
				d := apply3(m, XYZ{lin[r], lin[g], lin[b]})
				for c := range 3 {
					l.RGB = append(l.RGB, float32(inv[c](clamp01(d[c]))))
				}
			}
		}
	}
	return l
}

// Identity reports whether the table moves no colour by more than tol,
// such as half a step of an 8-bit channel: it is the profile of an sRGB
// display, or one so close that converting would change nothing seen.
func (l *LUT) Identity(tol float64) bool {
	n := l.N
	i := 0
	for b := range n {
		for g := range n {
			for r := range n {
				for c, v := range [3]int{r, g, b} {
					if math.Abs(float64(l.RGB[i+c])-float64(v)/float64(n-1)) > tol {
						return false
					}
				}
				i += 3
			}
		}
	}
	return true
}

// Apply converts an sRGB colour through the table, interpolating
// between its steps as the GPU does.
func (l *LUT) Apply(rgb [3]float32) [3]float32 {
	n := l.N
	var lo [3]int
	var f [3]float32
	for c := range 3 {
		v := min(max(rgb[c], 0), 1) * float32(n-1)
		lo[c] = min(int(v), n-2)
		f[c] = v - float32(lo[c])
	}
	at := func(r, g, b int) [3]float32 {
		i := ((b*n+g)*n + r) * 3
		return [3]float32{l.RGB[i], l.RGB[i+1], l.RGB[i+2]}
	}
	var out [3]float32
	for corner := range 8 {
		dr, dg, db := corner&1, corner>>1&1, corner>>2&1
		w := pick(f[0], dr) * pick(f[1], dg) * pick(f[2], db)
		if w == 0 {
			continue
		}
		v := at(lo[0]+dr, lo[1]+dg, lo[2]+db)
		for c := range 3 {
			out[c] += w * v[c]
		}
	}
	return out
}

func pick(f float32, one int) float32 {
	if one == 1 {
		return f
	}
	return 1 - f
}

func clamp01(v float64) float64 { return min(max(v, 0), 1) }

func apply3(m [3][3]float64, v XYZ) XYZ {
	return XYZ{
		m[0][0]*v[0] + m[0][1]*v[1] + m[0][2]*v[2],
		m[1][0]*v[0] + m[1][1]*v[1] + m[1][2]*v[2],
		m[2][0]*v[0] + m[2][1]*v[1] + m[2][2]*v[2],
	}
}

func mul3(a, b [3][3]float64) [3][3]float64 {
	var out [3][3]float64
	for i := range 3 {
		for j := range 3 {
			for k := range 3 {
				out[i][j] += a[i][k] * b[k][j]
			}
		}
	}
	return out
}

func invert3(m [3][3]float64) ([3][3]float64, bool) {
	det := m[0][0]*(m[1][1]*m[2][2]-m[1][2]*m[2][1]) -
		m[0][1]*(m[1][0]*m[2][2]-m[1][2]*m[2][0]) +
		m[0][2]*(m[1][0]*m[2][1]-m[1][1]*m[2][0])
	if math.Abs(det) < 1e-9 {
		return [3][3]float64{}, false
	}
	inv := [3][3]float64{
		{m[1][1]*m[2][2] - m[1][2]*m[2][1], m[0][2]*m[2][1] - m[0][1]*m[2][2], m[0][1]*m[1][2] - m[0][2]*m[1][1]},
		{m[1][2]*m[2][0] - m[1][0]*m[2][2], m[0][0]*m[2][2] - m[0][2]*m[2][0], m[0][2]*m[1][0] - m[0][0]*m[1][2]},
		{m[1][0]*m[2][1] - m[1][1]*m[2][0], m[0][1]*m[2][0] - m[0][0]*m[2][1], m[0][0]*m[1][1] - m[0][1]*m[1][0]},
	}
	for i := range 3 {
		for j := range 3 {
			inv[i][j] /= det
		}
	}
	return inv, true
}
