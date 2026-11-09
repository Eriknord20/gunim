package anim

import (
	"image/color"
	"math"
	"testing"
)

var sinkNRGBA color.NRGBA

// BenchmarkMixColor blends two colours through Oklab, as a widget
// blending its idle and hover colours does each time it paints.
func BenchmarkMixColor(b *testing.B) {
	from := color.NRGBA{R: 0x20, G: 0x80, B: 0xc0, A: 0xff}
	to := color.NRGBA{R: 0xf0, G: 0x40, B: 0x10, A: 0xff}
	b.ReportAllocs()
	t := float32(0)
	for b.Loop() {
		sinkNRGBA = Mix(ColorCodec, from, to, t)
		t += 0.001
		if t > 1 {
			t = 0
		}
	}
}

// TestTheSRGBTablesMatchThePowerForm checks the tables give what the
// power form gives: every channel one way, and every float32 near each
// level's threshold the other, plus an even spread across 0..1. A run
// over every float32 from -1 to 1 matched too, but takes 20 seconds.
func TestTheSRGBTablesMatchThePowerForm(t *testing.T) {
	for c := range 256 {
		if got, want := toLinear(uint8(c)), srgbToLinear(uint8(c)); got != want {
			t.Fatalf("toLinear(%d) = %v, want %v", c, got, want)
		}
	}
	check := func(v float32) {
		if got, want := fromLinear(v), linearToSRGB(v); got != want {
			t.Fatalf("fromLinear(%v) = %d, want %d", v, got, want)
		}
	}
	for _, s := range srgbSteps {
		b := math.Float32bits(s)
		for d := range uint32(257) {
			check(math.Float32frombits(b + d))
			check(math.Float32frombits(b - d))
		}
	}
	for b := uint32(0); b <= math.Float32bits(1); b += 4093 {
		check(math.Float32frombits(b))
		check(-math.Float32frombits(b))
	}
	for _, v := range []float32{-2, 0, 1, 1.5, float32(math.Inf(1)), float32(math.Inf(-1))} {
		check(v)
	}
}
