package widget

import (
	"image/color"
	"slices"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/theme"
)

// blackContrast is Windows' "Night sky" contrast theme, near enough.
var blackContrast = gunim.ContrastColors{
	Window:        color.NRGBA{A: 0xff},
	WindowText:    color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
	Highlight:     color.NRGBA{R: 0x8e, G: 0xe3, B: 0xf0, A: 0xff},
	HighlightText: color.NRGBA{R: 0x26, G: 0x3b, B: 0x50, A: 0xff},
	ButtonFace:    color.NRGBA{R: 0x20, G: 0x20, B: 0x20, A: 0xff},
	ButtonText:    color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
	GrayText:      color.NRGBA{R: 0xa6, G: 0xa6, B: 0xa6, A: 0xff},
	HotLight:      color.NRGBA{R: 0x75, G: 0xe9, B: 0xfc, A: 0xff},
}

func TestForLookScalesTextAndStopsMotion(t *testing.T) {
	th := ForLook(Light(), gunim.SystemLook{TextScale: 1.5, LessMotion: true})
	if got, want := TextSize.In(th), TextSize.In(Light())*1.5; got != want {
		t.Errorf("text size = %v, want %v", got, want)
	}
	if got, want := HeadingSize.In(th), HeadingSize.In(Light())*1.5; got != want {
		t.Errorf("heading size = %v, want %v", got, want)
	}
	if got, want := ButtonHeight.In(th), ButtonHeight.In(Light())*1.5; got != want {
		t.Errorf("button height = %v, want %v, grown with the text it holds", got, want)
	}
	if got := ButtonRadius.In(th); got != ButtonRadius.In(Light()) {
		t.Errorf("button radius = %v, want it left at %v", got, ButtonRadius.In(Light()))
	}
	if got := theme.Scale.In(th); got != 0 {
		t.Errorf("motion scale = %v, want 0 for fewer animations", got)
	}
	if got := Accent.In(th); got != Accent.In(Light()) {
		t.Errorf("accent = %v without a contrast theme, want the light theme's", got)
	}
}

func TestForLookLeavesATheme(t *testing.T) {
	th := ForLook(Dark(), gunim.SystemLook{TextScale: 1})
	if got := TextSize.In(th); got != TextSize.Default() {
		t.Errorf("text size = %v at a scale of 1, want %v", got, TextSize.Default())
	}
	if got := theme.Scale.In(th); got != 1 {
		t.Errorf("motion scale = %v with animations on, want 1", got)
	}
}

func TestAContrastThemeColoursEveryColourWithItsOwn(t *testing.T) {
	th := ForLook(Dark(), gunim.SystemLook{TextScale: 1, Contrast: &blackContrast})
	c := blackContrast
	scrim := c.Window
	scrim.A = 0xa0
	own := []color.NRGBA{c.Window, c.WindowText, c.Highlight, c.HighlightText, c.ButtonFace, c.ButtonText,
		c.GrayText, c.HotLight, {}, scrim}
	light := Light()
	var missing []string
	for key, def := range theme.Declared() {
		if _, isColour := def.(color.NRGBA); !isColour || !light.Has(key) {
			continue
		}
		if !th.Has(key) {
			missing = append(missing, key)
		}
	}
	slices.Sort(missing)
	for _, key := range missing {
		t.Errorf("a contrast theme leaves %q as it was; give it one of the theme's colours", key)
	}
	for _, tok := range []theme.Token[color.NRGBA]{Ink, Background, Accent, LinkInk, Placeholder, ButtonFill,
		ButtonPrimaryFill, CheckMark, MenuShadow, Scrim} {
		if v := tok.In(th); !slices.Contains(own, v) {
			t.Errorf("%s = %v, which is none of the contrast theme's colours", tok.Key(), v)
		}
	}
	if got := Ink.In(th); got != c.WindowText {
		t.Errorf("ink = %v, want the window text colour %v", got, c.WindowText)
	}
	if got := LinkInk.In(th); got != c.HotLight {
		t.Errorf("a link = %v, want the hot-tracking colour %v", got, c.HotLight)
	}
	if got := MenuShadow.In(th); got.A != 0 {
		t.Errorf("a menu casts a shadow of %v, want none", got)
	}
}
