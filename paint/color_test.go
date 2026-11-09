package paint

import (
	"image/color"
	"testing"
)

func TestFadedTakesAShareOfTheColoursOwnAlpha(t *testing.T) {
	c := color.NRGBA{R: 10, G: 20, B: 30, A: 200}
	for _, v := range []struct {
		a    float32
		want uint8
	}{{-1, 0}, {0, 0}, {0.5, 100}, {1, 200}, {2, 200}} {
		got := Faded(c, v.a)
		if got.A != v.want || got.R != 10 || got.G != 20 || got.B != 30 {
			t.Fatalf("Faded(%v, %v) = %v, want alpha %d and the colour as it was", c, v.a, got, v.want)
		}
	}
	if got := Faded(RGB(1, 2, 3), 1); got != (color.NRGBA{R: 1, G: 2, B: 3, A: 255}) {
		t.Fatalf("an opaque colour at 1 came back as %v", got)
	}
}
