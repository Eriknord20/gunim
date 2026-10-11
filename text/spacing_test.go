package text

import (
	"math"
	"testing"
)

// Text is spaced in proportion to its size, a fraction of a pixel
// included. The shaper spaces text for the size rounded up to a whole
// pixel: text at 13.5 was as wide as at 14, and text shrinking from 14
// to 13 kept the width of 14 until the last moment, then jumped.
func TestTextIsSpacedInProportionToItsSize(t *testing.T) {
	const s = "The quick brown fox jumps over the lazy dog"
	f := Default()
	width := func(size float32) float32 { return f.Layout(s, Style{Size: size}, 0).Lines[0].Run.Advance }
	at14 := width(14)
	for _, size := range []float32{13, 13.007, 13.5, 13.99, 12.5, 7.25} {
		want := at14 * size / 14
		// Each glyph's advance is rounded to a sixty-fourth of a pixel.
		if got := width(size); math.Abs(float64(got-want)) > float64(len(s))/64 {
			t.Errorf("at %v pixels the line is %v wide, want %v, in proportion to %v at 14", size, got, want, at14)
		}
	}
	if a, b := width(13.007), width(13.008); a > b || b-a > 0.05 {
		t.Errorf("from 13.007 to 13.008 pixels the line goes from %v to %v wide", a, b)
	}
	if got := f.Shape(s, 13.5).Advance; math.Abs(float64(got-width(13.5))) > 0.01 {
		t.Errorf("shaped at 13.5 the line is %v wide, and laid out %v", got, width(13.5))
	}
}
