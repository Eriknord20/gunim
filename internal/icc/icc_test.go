package icc

import (
	"errors"
	"math"
	"testing"
)

func roundTrip(t *testing.T, p *Profile) *Profile {
	t.Helper()
	got, err := Parse(Write(p))
	if err != nil {
		t.Fatalf("parse %q: %v", p.Description, err)
	}
	return got
}

func to8(v float32) int { return int(math.Round(float64(v) * 255)) }

func TestParseVersions(t *testing.T) {
	for _, v := range []int{2, 4} {
		p := DisplayP3()
		p.Version = v
		p.Description = "Wide Gamut Monitor ÅÄÖ"
		if v == 2 {
			p.Description = "Wide Gamut Monitor"
		}
		got := roundTrip(t, p)
		if got.Description != p.Description || got.Version != v || !got.HasMatrix {
			t.Errorf("v%d: got %q v%d matrix %v", v, got.Description, got.Version, got.HasMatrix)
		}
		for r := range 3 {
			for c := range 3 {
				if math.Abs(got.Matrix[r][c]-p.Matrix[r][c]) > 1e-4 {
					t.Errorf("v%d: matrix[%d][%d] = %v, want %v", v, r, c, got.Matrix[r][c], p.Matrix[r][c])
				}
			}
		}
	}
}

func TestSRGBIsIdentity(t *testing.T) {
	l := roundTrip(t, SRGB()).LUT(LUTSize)
	if !l.Identity(1.0 / 255) {
		t.Fatal("sRGB to sRGB moves colours by more than 1/255")
	}
	// Every 8-bit value comes back as itself.
	for v := range 256 {
		c := float32(v) / 255
		out := l.Apply([3]float32{c, c / 2, 1 - c})
		if math.Abs(float64(out[0]-c))*255 > 1 || math.Abs(float64(out[2]-(1-c)))*255 > 1 {
			t.Fatalf("%d: got %v", v, out)
		}
	}
}

func TestP3Red(t *testing.T) {
	l := roundTrip(t, DisplayP3()).LUT(LUTSize)
	if l.Identity(1.0 / 255) {
		t.Fatal("Display P3 taken as sRGB")
	}
	red := l.Apply([3]float32{1, 0, 0})
	if r, g, b := to8(red[0]), to8(red[1]), to8(red[2]); r != 234 || g != 51 || b != 35 {
		t.Errorf("sRGB red in Display P3 = %d,%d,%d, want 234,51,35", r, g, b)
	}
	green := l.Apply([3]float32{0, 1, 0})
	if r, g, b := to8(green[0]), to8(green[1]), to8(green[2]); r != 117 || g != 251 || b != 76 {
		t.Errorf("sRGB green in Display P3 = %d,%d,%d, want 117,251,76", r, g, b)
	}
	// Greys stay grey: both have a D65 white and the same curve.
	for _, v := range []float32{0, 0.2, 0.5, 0.8, 1} {
		out := l.Apply([3]float32{v, v, v})
		for c := range 3 {
			if math.Abs(float64(out[c]-v))*255 > 0.6 {
				t.Errorf("grey %v came out %v", v, out)
			}
		}
	}
}

func TestCurves(t *testing.T) {
	table := make([]float64, 1024)
	for i := range table {
		table[i] = math.Pow(float64(i)/1023, 2.2)
	}
	curves := map[string]Curve{
		"identity": Gamma(1),
		"gamma":    Gamma(2.2),
		"table":    {Table: table},
		"para1":    {Fn: 1, Params: [7]float64{2.2, 1.0, 0}},
		"para2":    {Fn: 2, Params: [7]float64{2.4, 0.95, 0.05, 0.001}},
		"para3":    sRGBCurve,
		"para4":    {Fn: 4, Params: [7]float64{2.4, 1 / 1.055, 0.055 / 1.055, 1 / 12.92, 0.04045, 0.001, 0.001}},
	}
	for name, c := range curves {
		for _, v := range []int{2, 4} {
			p := SRGB()
			p.Version = v
			p.TRC = [3]Curve{c, c, c}
			got := roundTrip(t, p)
			inv := got.TRC[0].inverse()
			for i := range 101 {
				x := float64(i) / 100
				if math.Abs(got.TRC[0].Eval(x)-c.Eval(x)) > 2e-4 {
					t.Fatalf("%s v%d: curve at %v = %v, want %v", name, v, x, got.TRC[0].Eval(x), c.Eval(x))
				}
				// Round trip through the inverse, where the curve rises.
				if y := c.Eval(x); y > c.Eval(0) && y < c.Eval(1) {
					if back := inv(y); math.Abs(back-x) > 1e-3 {
						t.Fatalf("%s v%d: inverse of curve(%v) = %v", name, v, x, back)
					}
				}
			}
		}
	}
}

func TestGamma22IsNotSRGB(t *testing.T) {
	p := SRGB()
	p.TRC = [3]Curve{Gamma(2.2), Gamma(2.2), Gamma(2.2)}
	if roundTrip(t, p).LUT(LUTSize).Identity(1.0 / 255) {
		t.Fatal("a gamma 2.2 display taken as sRGB")
	}
}

func TestLUTOnly(t *testing.T) {
	b := Write(DisplayP3())
	// Rename the matrix and curve tags so only a table seems left.
	for i := range 7 {
		e := b[132+12*i:]
		switch string(e[0:4]) {
		case "rXYZ":
			copy(e, "A2B0")
		case "gXYZ", "bXYZ", "rTRC", "gTRC", "bTRC":
			copy(e, "zzzz")
		}
	}
	p, err := Parse(b)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v, want ErrUnsupported", err)
	}
	if p == nil || p.Description != "Display P3" || !p.HasLUT {
		t.Fatalf("got %+v", p)
	}
}

func TestUnadaptedColorants(t *testing.T) {
	// Colorants left at D65, as some old profiles have them, with the
	// D65 white point: adapted, they give sRGB's.
	d65 := XYZ{0.9505, 1, 1.089}
	p := SRGB()
	p.Version = 2
	p.White = d65
	inv := bradford(D50, d65)
	p.Matrix = mul3(inv, sRGBMatrix)
	if !roundTrip(t, p).LUT(LUTSize).Identity(1.0 / 255) {
		t.Fatal("an sRGB profile with unadapted colorants is not taken as sRGB")
	}
}

func TestGarbage(t *testing.T) {
	for _, b := range [][]byte{nil, make([]byte, 200), Write(SRGB())[:140]} {
		if _, err := Parse(b); err == nil {
			t.Errorf("parsed %d bytes of garbage", len(b))
		}
	}
}
