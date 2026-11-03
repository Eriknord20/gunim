package glfw

import (
	"math"
	"testing"
)

func TestARoundedShadowFallsAwayAtItsCornersAlone(t *testing.T) {
	// Pixel x, y has its middle at x+0.5, y+0.5.
	c := newCornerCut(100, 100, 900, 700, 39, 34, 1000, 800)
	if cut := c.at(500, 60); cut != 0 {
		t.Errorf("above the middle of the top edge, the corners cut %v, want 0", cut)
	}
	corner := c.at(105, 105)
	if corner <= 0 || corner >= 1 {
		t.Errorf("just inside the corner, the corners cut %v, want a part of the shadow", corner)
	}
	if far := c.at(80, 80); far <= 0 || far >= corner {
		t.Errorf("beyond the corner the corners cut %v, want less than the %v just inside it", far, corner)
	}
	if square := newCornerCut(100, 100, 900, 700, 0, 34, 1000, 800).at(105, 105); square != 0 {
		t.Errorf("square corners cut %v, want 0", square)
	}
}

// TestTheShadowPaintsAsItDidPixelByPixel checks the shadow painted with the corner cut worked out once a row and a
// column against the one that worked it out at every pixel, to within one step of 255 in each channel.
func TestTheShadowPaintsAsItDidPixelByPixel(t *testing.T) {
	looks := []shadowLook{
		{winW: 1040, winH: 700, k: 1, corner: 8, scale: 1, active: true},
		{winW: 1560, winH: 1050, k: 1.5, corner: 8, scale: 0.94, active: true, edge: 1.5, bc: [4]float64{30, 60, 90, 200}},
		{winW: 600, winH: 400, k: 2, corner: 13, scale: 0.97, active: false, edge: 2, bc: [4]float64{100, 100, 100, 255}},
		{winW: 60, winH: 40, k: 1.5, corner: 26, scale: 1, active: true},
		{winW: 800, winH: 500, k: 1, corner: 0, scale: 1, active: true},
	}
	for _, l := range looks {
		side, top, bottom := shadowMargins(l.k)
		w, h := l.winW+2*side, l.winH+top+bottom
		got, want := make([]uint32, w*h), make([]uint32, w*h)
		gx, gy := paintShadow(got, w, h, side, top, l)
		wx, wy := paintShadowPerPixel(want, w, h, side, top, l)
		if gx != wx || gy != wy {
			t.Errorf("%+v: draws %d, %d in from the edges, want %d, %d", l, gx, gy, wx, wy)
		}
		worst := 0
		for i := range got {
			for sh := 0; sh < 32; sh += 8 {
				d := int(got[i]>>sh&0xff) - int(want[i]>>sh&0xff)
				worst = max(worst, d, -d)
			}
		}
		if worst > 1 {
			t.Errorf("%+v: a channel is %d away from the shadow worked out pixel by pixel, want at most 1", l, worst)
		}
	}
}

func BenchmarkShadowPaint(b *testing.B) {
	l := shadowLook{winW: 1560, winH: 1050, k: 1.5, corner: 8, scale: 0.97, active: true}
	side, top, bottom := shadowMargins(l.k)
	w, h := l.winW+2*side, l.winH+top+bottom
	px := make([]uint32, w*h)
	b.Run("rows and columns", func(b *testing.B) {
		for b.Loop() {
			paintShadow(px, w, h, side, top, l)
		}
	})
	b.Run("per pixel", func(b *testing.B) {
		for b.Loop() {
			paintShadowPerPixel(px, w, h, side, top, l)
		}
	})
}

// paintShadowPerPixel is paintShadow as it was, working the corner cut out at every pixel: what paintShadow is
// checked against.
func paintShadowPerPixel(px []uint32, W, H, side, top int32, l shadowLook) (inX, inY int32) {
	k := l.k
	sigma, drop, inset, radius, strength := 22.7*k, 21.3*k, 2*k, l.corner*k, 0.366
	if !l.active {
		sigma, strength = 18.7*k, 0.25
	}
	winW, winH := l.winW, l.winH
	x0, y0, x1, y1 := side, top, side+winW, top+winH
	edge, bc := l.edge, l.bc
	sc := float64(l.scale)
	if sc <= 0 {
		sc = 1
	}
	mx, my := float64(x0+x1)/2, float64(y0+y1)/2
	hw, hh := float64(winW)*sc/2, float64(winH)*sc/2
	fx0, fy0, fx1, fy1 := mx-hw, my-hh, mx+hw, my+hh
	radius, edge = min(radius*sc, hw, hh), edge*sc
	x0, y0, x1, y1 = int32(math.Ceil(fx0)), int32(math.Ceil(fy0)), int32(fx1), int32(fy1)
	blur := func(p, a, b float64) float64 {
		return 0.5 * (math.Erf((p-a)/(sigma*math.Sqrt2)) - math.Erf((p-b)/(sigma*math.Sqrt2)))
	}
	across := make([]float64, W)
	for x := range across {
		across[x] = blur(float64(x)+0.5, fx0+inset, fx1-inset)
	}
	reach := int32(max(radius, edge)) + 2
	inX, inY = min(x0-side+reach+1, winW/2), min(y0-top+reach+1, winH/2)
	cast := roundedShade{x0: fx0 + inset, y0: fy0 + drop, x1: fx1 - inset, y1: fy1 + drop, r: max(radius-inset, 0), sigma: sigma}
	shade := func(x int32, y int32, square float64) float64 {
		return max(square-strength*cast.cut(float64(x)+0.5, float64(y)+0.5), 0)
	}
	for y := int32(0); y < H; y++ {
		down := strength * blur(float64(y)+0.5, fy0+drop, fy1+drop)
		row := px[y*W : (y+1)*W]
		pixel := func(x int32) uint32 {
			cx, cy := float64(x)+0.5, float64(y)+0.5
			outer := min(max(0.5-sdRRect(cx, cy, fx0, fy0, fx1, fy1, radius), 0), 1)
			inner := min(max(0.5-sdRRect(cx, cy, fx0+edge, fy0+edge, fx1-edge, fy1-edge, max(radius-edge, 0)), 0), 1)
			if inner >= 1 {
				return 0
			}
			border := (outer - inner) * bc[3] / 0xff
			a := border + shade(x, y, down*across[x])*(1-outer)
			r := uint32(bc[0]*border + 0.5)
			g := uint32(bc[1]*border + 0.5)
			b := uint32(bc[2]*border + 0.5)
			return uint32(a*255+0.5)<<24 | r<<16 | g<<8 | b
		}
		shadow := func(x int32) uint32 { return uint32(shade(x, y, down*across[x])*255+0.5) << 24 }
		if y < y0-1 || y > y1 {
			for x := range row {
				row[x] = shadow(int32(x))
			}
			continue
		}
		middle := y >= y0+reach && y < y1-reach
		for x := int32(0); x < W; x++ {
			switch {
			case x < x0-1 || x > x1:
				row[x] = shadow(x)
			case !middle || x < x0+reach || x >= x1-reach:
				row[x] = pixel(x)
			default:
				row[x] = 0
			}
		}
	}
	return inX, inY
}

// roundedShade is the blurred rectangle x0, y0, x1, y1, its corners rounded by r, blurred by sigma, as the shadow
// worked it out before; see cornerCut.
type roundedShade struct{ x0, y0, x1, y1, r, sigma float64 }

func (c roundedShade) cut(px, py float64) float64 {
	r, reach := c.r, 4*c.sigma
	if r <= 0 || px > c.x0+r+reach && px < c.x1-r-reach || py > c.y0+r+reach && py < c.y1-r-reach {
		return 0
	}
	const steps = 12
	s2 := c.sigma * math.Sqrt2
	edge := func(p, a, b float64) float64 { return 0.5 * (math.Erf((p-a)/s2) - math.Erf((p-b)/s2)) }
	full := edge(px, c.x0, c.x1)
	var sum float64
	for _, band := range [2][2]float64{{c.y0, c.y0 + r}, {c.y1 - r, c.y1}} {
		dy := (band[1] - band[0]) / steps
		for i := range steps {
			y := band[0] + (float64(i)+0.5)*dy
			in := min(y-c.y0, c.y1-y)
			off := r - math.Sqrt(max(r*r-(r-in)*(r-in), 0))
			t := (y - py) / c.sigma
			w := math.Exp(-t*t/2) / (c.sigma * math.Sqrt(2*math.Pi)) * dy
			sum += w * (full - edge(px, c.x0+off, c.x1-off))
		}
	}
	return sum
}
