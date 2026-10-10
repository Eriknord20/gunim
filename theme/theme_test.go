package theme

import (
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim/anim"
)

var (
	fill = Color("test.fill", color.NRGBA{A: 0xff})
	pad  = Length("test.pad", 8)
)

func step(l *Live, n int) {
	for range n {
		l.Step(time.Second / 60)
	}
}

func TestGetGivesTheDefaultOutsideATheme(t *testing.T) {
	if got := pad.Get(nil); got != 8 {
		t.Fatalf("with no Live, pad = %v, want the default 8", got)
	}
	if got := pad.Get(NewLive(Make("empty"))); got != 8 {
		t.Fatalf("in a theme that leaves pad out, pad = %v, want 8", got)
	}
}

func TestGetGivesTheThemesValue(t *testing.T) {
	l := NewLive(Make("roomy", Set(pad, 20)))
	if got := pad.Get(l); got != 20 {
		t.Fatalf("pad = %v, want the theme's 20", got)
	}
}

func TestUseAnimatesToTheNewTheme(t *testing.T) {
	l := NewLive(Make("tight"))
	if pad.Get(l) != 8 {
		t.Fatal("setup: pad should start at 8")
	}
	l.Use(Make("roomy", Set(pad, 20)))
	step(l, 3)
	if got := pad.Get(l); got <= 8 || got >= 20 {
		t.Fatalf("three frames into the switch, pad = %v, want between 8 and 20", got)
	}
	if !l.Step(time.Second / 60) {
		t.Fatal("Step reports nothing moving in the middle of a switch")
	}
	step(l, 300)
	if got := pad.Get(l); got != 20 {
		t.Fatalf("once settled, pad = %v, want 20", got)
	}
	if l.Step(time.Second / 60) {
		t.Fatal("Step still reports movement after the switch settled")
	}
}

func TestSwitchingBackReturnsToTheDefault(t *testing.T) {
	l := NewLive(Make("roomy", Set(pad, 20)))
	pad.Get(l)
	l.Use(Make("plain"))
	step(l, 300)
	if got := pad.Get(l); got != 8 {
		t.Fatalf("pad = %v, want its default 8 in a theme that leaves it out", got)
	}
}

func TestATokenFirstReadAfterASwitchStartsAtRest(t *testing.T) {
	l := NewLive(Make("plain"))
	l.Use(Make("red", Set(fill, color.NRGBA{R: 0xff, A: 0xff})))
	if got := fill.Get(l); got != (color.NRGBA{R: 0xff, A: 0xff}) {
		t.Fatalf("fill = %v, want the new theme's red at once", got)
	}
	if l.Step(time.Second / 60) {
		t.Fatal("a token read after the switch is animating")
	}
}

func TestTheNewThemeChoosesTheSwitchMotion(t *testing.T) {
	instant := anim.Spring{Response: 0.001, Damping: 1}
	slow := NewLive(Make("a"))
	fast := NewLive(Make("a"))
	pad.Get(slow)
	pad.Get(fast)
	slow.Use(Make("b", Set(pad, 20)))
	fast.Use(Make("b", Set(pad, 20), Set(Switch, instant)))
	step(slow, 2)
	step(fast, 2)
	if s, f := pad.Get(slow), pad.Get(fast); f != 20 || s >= f {
		t.Fatalf("after two frames pad is %v with the default switch and %v with an instant one", s, f)
	}
}

func TestAKeyKeepsOneType(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("declaring test.pad again as a colour did not panic")
		}
	}()
	Color("test.pad", color.NRGBA{})
}

var ink = Foreground("test.ink", color.NRGBA{R: 0xee, G: 0xee, B: 0xee, A: 0xff})

func TestAForegroundFadesThroughASwitch(t *testing.T) {
	dark := color.NRGBA{R: 0x11, G: 0x11, B: 0x11, A: 0xff}
	l := NewLive(Make("dark"))
	if got := ink.Get(l); got.A != 0xff {
		t.Fatalf("setup: ink %v, want opaque", got)
	}
	l.Use(Make("light", Set(ink, dark), Set(Switch, anim.Spring{Response: 0.5, Damping: 1})))

	// Somewhere in the switch the ink is out of sight, and it is never
	// a grey between the two.
	minAlpha := uint8(0xff)
	for range 120 {
		l.Step(time.Second / 120)
		c := ink.Get(l)
		minAlpha = min(minAlpha, c.A)
		if c.A > 0x20 && c.R != 0xee && c.R != 0x11 {
			t.Fatalf("ink %v is visible part way between its two colours", c)
		}
	}
	if minAlpha > 0x08 {
		t.Fatalf("ink stayed at least %d opaque through the switch, want it faded out", minAlpha)
	}
	step(l, 300)
	if got := ink.Get(l); got != dark {
		t.Fatalf("once settled, ink is %v, want %v", got, dark)
	}
}

func TestAScopeOverridesSomeTokensAndInheritsTheRest(t *testing.T) {
	window := NewLive(Make("plain"))
	scope := NewLive(Make("callout", Set(pad, 30)))
	scope.Under(window)
	if got := pad.Get(scope); got != 30 {
		t.Fatalf("pad in the scope is %v, want its own 30", got)
	}
	if got := ink.Get(scope); got != ink.Get(window) {
		t.Fatalf("ink in the scope is %v, want the window's %v", got, ink.Get(window))
	}

	// A window switch reaches the inherited token inside the scope at
	// the same moment, and leaves the scope's own alone.
	grey := color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff}
	window.Use(Make("grey", Set(ink, grey), Set(pad, 12)))
	for range 5 {
		window.Step(time.Second / 60)
		scope.Step(time.Second / 60)
		if ink.Get(scope) != ink.Get(window) {
			t.Fatal("the scope's inherited ink lags the window's")
		}
	}
	if got := pad.Get(scope); got != 30 {
		t.Fatalf("pad in the scope moved to %v with the window's switch", got)
	}
}

func TestAScopeSwitchGlidesToAndFromTheThemeAround(t *testing.T) {
	window := NewLive(Make("roomy", Set(pad, 20)))
	scope := NewLive(Make("plain"))
	scope.Under(window)
	if pad.Get(scope) != 20 {
		t.Fatal("setup: the scope should inherit 20")
	}

	// Setting the token glides it out from the window's value.
	scope.Use(Make("tight", Set(pad, 4)))
	scope.Step(time.Second / 60)
	if got := pad.Get(scope); got >= 20 || got <= 4 {
		t.Fatalf("a frame into setting pad, it is %v, want between 20 and 4", got)
	}
	step(scope, 300)
	if got := pad.Get(scope); got != 4 {
		t.Fatalf("once settled, pad is %v, want 4", got)
	}

	// Leaving it out again glides it back to the window's.
	scope.Use(Make("plain"))
	scope.Step(time.Second / 60)
	if got := pad.Get(scope); got <= 4 || got >= 20 {
		t.Fatalf("a frame into dropping pad, it is %v, want between 4 and 20", got)
	}
	step(scope, 300)
	if got := pad.Get(scope); got != 20 {
		t.Fatalf("once settled, pad is %v, want the window's 20", got)
	}
}

func TestChoiceSwitchesWholeHalfway(t *testing.T) {
	shape := Choice("test.shape", "round")
	l := NewLive(Make("round"))
	if got := shape.Get(l); got != "round" {
		t.Fatalf("shape = %q, want the default round", got)
	}
	l.Use(Make("square", Set(shape, "square")))
	step(l, 1)
	if got := shape.Get(l); got != "round" {
		t.Fatalf("a frame into the switch, shape = %q, want round still", got)
	}
	step(l, 300)
	if got := shape.Get(l); got != "square" {
		t.Fatalf("once settled, shape = %q, want square", got)
	}
}

func TestDeclaredListsTokensWithTheirDefaults(t *testing.T) {
	tok := Length("test.declared", 7)
	if got, ok := Declared()[tok.Key()]; !ok || got != float32(7) {
		t.Fatalf("Declared()[%q] = %v, %v, want 7", tok.Key(), got, ok)
	}
	th := Make("t", Set(tok, 9))
	if !th.Has(tok.Key()) || th.Has("test.nothing") {
		t.Fatal("Has does not match what the theme sets")
	}
}

// Using the theme a scope wears already leaves its text alone: a
// foreground fades through on a switch, and a switch to the same colour
// would dip the text and bring it back.
func TestUsingTheSameThemeAgainLeavesTheTextAlone(t *testing.T) {
	ink := Foreground("test.same.ink", color.NRGBA{R: 200, G: 208, B: 218, A: 255})
	th := Make("dark", Set(ink, color.NRGBA{R: 10, G: 20, B: 30, A: 255}))
	l := NewLive(th)
	_ = ink.Get(l)
	step(l, 300)
	want := ink.Get(l)
	for range 3 {
		l.Use(th.With())
		for f := range 10 {
			step(l, 1)
			if got := ink.Get(l); got != want {
				t.Fatalf("frame %d after using the same theme again, the ink is %v, want %v", f, got, want)
			}
		}
	}
}
