package theme

import "testing"

var (
	body  = FontSize("test.text.body", 14)
	title = FontSize("test.text.title", 20)
)

func TestScaleTextScalesEveryFontSize(t *testing.T) {
	th := Make("app", Set(title, 24), Set(pad, 10))
	big := ScaleText(th, 1.5)
	if got := body.In(big); got != 21 {
		t.Errorf("body = %v, want its default 14 scaled to 21", got)
	}
	if got := title.In(big); got != 36 {
		t.Errorf("title = %v, want the theme's 24 scaled to 36", got)
	}
	if got := pad.In(big); got != 10 {
		t.Errorf("pad = %v, want 10: a plain length keeps its size", got)
	}
	if got := title.In(th); got != 24 {
		t.Errorf("the theme scaled from has title %v, want 24 left as it was", got)
	}
	if big.Name != "app" {
		t.Errorf("the scaled theme is named %q, want app", big.Name)
	}
}

func TestAFontSizeIsALength(t *testing.T) {
	info, ok := Lookup("test.text.body")
	if !ok || info.Kind != KindLength {
		t.Fatalf("Lookup = %+v, %v; want a length", info, ok)
	}
}
