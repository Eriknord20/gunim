package audioui

import (
	"image/color"
	"math"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// A light theme colours the audio pieces' text and guides, through widget's Ink alone.
func TestALightThemeColoursTheAudioPieces(t *testing.T) {
	light := theme.NewLive(widget.Light())
	ink := widget.Ink.Get(light)
	if ink == widget.Ink.Default() {
		t.Fatal("the light theme leaves widget.Ink at its dark default")
	}
	gain := float32(0)
	f := NewFader(func() float32 { return gain }, nil)
	var p paint.Painter
	f.Paint(&p, gunim.Frame{Scale: 1, Theme: light}, geom.Sz(24, 200), gunim.Children{})
	for _, op := range p.Ops() {
		if r, ok := op.(*paint.RRectOp); ok {
			c := r.Fill.Solid
			if c.R == ink.R && c.G == ink.G && c.B == ink.B && c.A > 0 {
				return
			}
		}
	}
	t.Fatalf("under the light theme the fader drew nothing in its ink %v", ink)
}

// contrastRatio is the contrast ratio of a and b, from 1 to 21, by WCAG's relative luminance.
func contrastRatio(a, b color.NRGBA) float64 {
	lum := func(c color.NRGBA) float64 {
		ch := func(v uint8) float64 {
			x := float64(v) / 255
			if x <= 0.03928 {
				return x / 12.92
			}
			return math.Pow((x+0.055)/1.055, 2.4)
		}
		return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
	}
	hi, lo := lum(a), lum(b)
	if lo > hi {
		hi, lo = lo, hi
	}
	return (hi + 0.05) / (lo + 0.05)
}

// Text in widget's Ink, and what plays, read on the ground behind them in the dark theme and in a light one made with
// Light.
func TestTheAudioPiecesReadOnTheirGroundInBothThemes(t *testing.T) {
	for name, th := range map[string]theme.Theme{
		"dark":  widget.Dark(),
		"light": widget.Light().With(Light...),
	} {
		live := theme.NewLive(th)
		ground := Ground.Get(live)
		if c := contrastRatio(widget.Ink.Get(live), ground); c < 4.5 {
			t.Errorf("%s: the ink reads at %.1f:1 on the ground, want 4.5:1 or more", name, c)
		}
		for _, tok := range []theme.Token[color.NRGBA]{Sound, Near, Over, Spread, Short} {
			if c := contrastRatio(tok.Get(live), ground); c < 3 {
				t.Errorf("%s: %s reads at %.1f:1 on the ground, want 3:1 or more", name, tok.Key(), c)
			}
		}
	}
}
