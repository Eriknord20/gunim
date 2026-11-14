package audioui

import (
	"image/color"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/theme"
)

func TestAContrastThemeSetsEveryColourTheLightThemeDoes(t *testing.T) {
	c := gunim.ContrastColors{Window: color.NRGBA{A: 0xff}, WindowText: color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
		Highlight: color.NRGBA{R: 0x8e, G: 0xe3, B: 0xf0, A: 0xff}, HighlightText: color.NRGBA{R: 0x26, G: 0x3b, B: 0x50, A: 0xff}}
	th := theme.Make("contrast", Contrast(c)...)
	for _, key := range theme.Make("light", Light...).Keys() {
		v, _ := theme.Make("light", Light...).Value(key)
		if _, colour := v.(color.NRGBA); colour && !th.Has(key) {
			t.Errorf("a contrast theme leaves %q as it was", key)
		}
	}
}
