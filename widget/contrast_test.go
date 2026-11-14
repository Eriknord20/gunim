package widget

import (
	"image/color"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// desertContrast is Windows' "Desert" contrast theme, a light one, near enough.
var desertContrast = gunim.ContrastColors{
	Window:        color.NRGBA{R: 0xff, G: 0xfa, B: 0xef, A: 0xff},
	WindowText:    color.NRGBA{R: 0x3d, G: 0x3d, B: 0x3d, A: 0xff},
	Highlight:     color.NRGBA{R: 0x90, G: 0x39, B: 0x09, A: 0xff},
	HighlightText: color.NRGBA{R: 0xff, G: 0xf5, B: 0xe3, A: 0xff},
	ButtonFace:    color.NRGBA{R: 0xff, G: 0xfa, B: 0xef, A: 0xff},
	ButtonText:    color.NRGBA{R: 0x20, G: 0x20, B: 0x20, A: 0xff},
	GrayText:      color.NRGBA{R: 0x67, G: 0x67, B: 0x67, A: 0xff},
	HotLight:      color.NRGBA{R: 0x1c, G: 0x5e, B: 0x75, A: 0xff},
}

// contrastThemes are a dark and a light contrast theme on the dark and the light base.
func contrastThemes() map[string]theme.Theme {
	return map[string]theme.Theme{
		"Night sky": ForLook(Dark(), gunim.SystemLook{TextScale: 1, Contrast: &blackContrast}),
		"Desert":    ForLook(Light(), gunim.SystemLook{TextScale: 1, Contrast: &desertContrast}),
	}
}

func TestEverySolidHighlightFillCarriesTheHighlightText(t *testing.T) {
	g := contrastSorted(blackContrast)
	solid := map[string]bool{}
	for _, p := range g.solid {
		solid[p.fill.Key()] = true
	}
	solid["nav.selected"] = true
	marks := map[string]bool{}
	for _, m := range g.marks {
		marks[m.Key()] = true
	}
	th := contrastThemes()["Night sky"]
	for key, def := range theme.Declared() {
		if _, ok := def.(color.NRGBA); !ok {
			continue
		}
		if v, _ := th.Value(key); v == blackContrast.Highlight && !solid[key] && !marks[key] {
			t.Errorf("a contrast theme fills %q with the highlight, but nothing says what is drawn on it: "+
				"pair it with an ink, or list it as a mark", key)
		}
	}
	for _, p := range g.solid {
		if got := p.fill.In(th); got != blackContrast.Highlight {
			t.Errorf("%s = %v, want the highlight", p.fill.Key(), got)
		}
		if got := p.ink.In(th); got != blackContrast.HighlightText {
			t.Errorf("%s, drawn on %s, = %v, want the highlight text", p.ink.Key(), p.fill.Key(), got)
		}
	}
	// The navigation list's tokens, where they are in this build.
	if _, ok := theme.Lookup("nav.selected"); ok {
		if v, _ := th.Value("nav.selected.ink"); v != blackContrast.HighlightText {
			t.Errorf("the chosen row of a navigation list is drawn in %v, want the highlight text", v)
		}
	}
}

// luminance is c's relative luminance, as WCAG measures it.
func luminance(c color.NRGBA) float64 {
	ch := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
}

// ratio is the contrast ratio of a and b, from 1 to 21.
func ratio(a, b color.NRGBA) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

// onGround is c laid over the opaque ground.
func onGround(c, ground color.NRGBA) color.NRGBA {
	a := float64(c.A) / 255
	mix := func(f, g uint8) uint8 { return uint8(math.Round(float64(f)*a + float64(g)*(1-a))) }
	return color.NRGBA{R: mix(c.R, ground.R), G: mix(c.G, ground.G), B: mix(c.B, ground.B), A: 0xff}
}

func TestTheWindowTextStaysReadableOnAContrastThemesTints(t *testing.T) {
	for _, c := range []gunim.ContrastColors{blackContrast, desertContrast} {
		g := contrastSorted(c)
		th := ForLook(Dark(), gunim.SystemLook{TextScale: 1, Contrast: &c})
		for _, tok := range g.tints {
			under := onGround(tok.In(th), c.Window)
			if r := ratio(c.WindowText, under); r < 4.5 {
				t.Errorf("the window text %v on %s, %v over %v, has a contrast of %.1f, want 4.5 or more",
					c.WindowText, tok.Key(), tok.In(th), c.Window, r)
			}
		}
		if r := ratio(c.HighlightText, c.Highlight); r < 4.5 {
			t.Errorf("the highlight text on the highlight has a contrast of %.1f", r)
		}
	}
}

// textColours returns the colours of the text ops among ops whose tops lie between top and bottom.
func textColours(ops []paint.Op, top, bottom float32) []color.NRGBA {
	var out []color.NRGBA
	for _, op := range ops {
		if o, ok := op.(*paint.TextOp); ok && o.Transform.F >= top && o.Transform.F < bottom {
			out = append(out, o.Color)
		}
	}
	return out
}

// settledMenu lays a menu of two items with hints out in th, with the second one hot and the highlight at rest.
func settledMenu(th theme.Theme) (*Menu, geom.Size, *theme.Live) {
	live := theme.NewLive(th)
	m := NewMenu([]MenuItem{{Label: "Open", Hint: "Ctrl+O"}, {Label: "Save", Hint: "Ctrl+S"}})
	f := gunim.Frame{Scale: 1, Theme: live}
	size := m.Layout(gunim.Loose(geom.Sz(300, 400)), f, gunim.Children{})
	m.Highlight(1)
	for range 120 {
		m.Step(time.Second / 60)
		live.Step(time.Second / 60)
		size = m.Layout(gunim.Loose(geom.Sz(300, 400)), f, gunim.Children{})
	}
	return m, size, live
}

func TestAMenusHotRowTakesTheHighlightTextInAContrastTheme(t *testing.T) {
	m, size, live := settledMenu(contrastThemes()["Night sky"])
	ops := paintedIn(m, size, live)
	hot := textColours(ops, m.rowY(1), m.rowY(1)+m.row)
	if len(hot) != 2 || hot[0] != blackContrast.HighlightText || hot[1] != blackContrast.HighlightText {
		t.Fatalf("the hot row's hint and label are drawn in %v, want both in the highlight text %v", hot,
			blackContrast.HighlightText)
	}
	if cold := textColours(ops, m.rowY(0), m.rowY(0)+m.row); slices.Contains(cold, blackContrast.HighlightText) {
		t.Fatalf("the row that is not hot is drawn in %v, want the window text", cold)
	}

	// Without a contrast theme, the hot row keeps the ink and the hint's colour.
	m, size, live = settledMenu(Dark())
	ops = paintedIn(m, size, live)
	hot = textColours(ops, m.rowY(1), m.rowY(1)+m.row)
	if !slices.Contains(hot, Ink.Default()) || !slices.Contains(hot, MenuHint.Default()) {
		t.Fatalf("in the dark theme the hot row is drawn in %v, want the ink and the hint's colour", hot)
	}
}

func TestAPlainButtonTakesTheButtonTextInAContrastTheme(t *testing.T) {
	desert := desertContrast
	desert.ButtonText = color.NRGBA{R: 0x00, G: 0x40, B: 0x00, A: 0xff}
	for _, c := range []struct {
		th   theme.Theme
		want color.NRGBA
	}{
		{ForLook(Light(), gunim.SystemLook{TextScale: 1, Contrast: &desert}), desert.ButtonText},
		{Light(), Ink.In(Light())},
	} {
		live := theme.NewLive(c.th)
		b := NewButton("Restore")
		size := b.Layout(gunim.Loose(geom.Sz(300, 100)), gunim.Frame{Scale: 1, Theme: live}, gunim.Children{})
		got := textColours(paintedIn(b, size, live), -1, size.H)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("the label is drawn in %v, want %v", got, c.want)
		}
	}
}

func TestRowsGrowWithTheTextUpToTheLargestSize(t *testing.T) {
	th := ForLook(Dark(), gunim.SystemLook{TextScale: 2.25})
	live := theme.NewLive(th)
	f := gunim.Frame{Scale: 1, Theme: live}

	b := NewButton("Restore the files")
	size := b.Layout(gunim.Loose(geom.Sz(600, 200)), f, gunim.Children{})
	run := faceIn(Font, live).Shape("Restore the files", TextSize.Get(live))
	line := run.Height()
	if size.H < line {
		t.Errorf("at 225%% a button is %v high for a line %v high", size.H, line)
	}
	if out := spills(paintedIn(b, size, live), size, 3); len(out) > 0 {
		t.Errorf("at 225%% the button draws outside its box: %v", out)
	}

	m := NewMenu([]MenuItem{{Label: "Open", Hint: "Ctrl+O"}, {Label: "Save", Hint: "Ctrl+S"}, {Label: "Quit"}})
	size = m.Layout(gunim.Loose(geom.Sz(600, 800)), f, gunim.Children{})
	if m.row < line {
		t.Errorf("at 225%% a menu row is %v high for a line %v high", m.row, line)
	}
	ops := paintedIn(m, size, live)
	for i := range 3 {
		for _, op := range ops {
			o, ok := op.(*paint.TextOp)
			if !ok {
				continue
			}
			// A text op sits on its baseline, the ascent below the line's top.
			top := o.Transform.F - run.Ascent
			if top < m.rowY(i)-1 || top >= m.rowY(i)+m.row {
				continue
			}
			if bottom := top + line; bottom > m.rowY(i)+m.row+0.5 {
				t.Errorf("at 225%% row %d's text runs to %v, past the row's bottom %v", i, bottom, m.rowY(i)+m.row)
			}
		}
	}
}
