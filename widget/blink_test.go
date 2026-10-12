package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

func TestTheCaretBlinksForAsLongAsItHasTheKeyboard(t *testing.T) {
	wr := newWriter(t, 400)
	b := &wr.area.blink
	if b.value() != 1 {
		t.Fatalf("caret at %v on arriving, want lit", b.value())
	}
	// Half a blink later, the caret is out.
	wr.run(35)
	if v := b.value(); v != 0 {
		t.Fatalf("caret at %v half a blink in, want out", v)
	}
	// A key lights it at once.
	wr.typeText("a")
	if v := b.value(); v != 1 {
		t.Fatalf("caret at %v after a key, want lit", v)
	}
	// Long after the last key it still blinks.
	lit, dark := false, false
	wr.run(60 * 60)
	for range 90 {
		wr.run(1)
		if b.value() == 1 {
			lit = true
		} else {
			dark = true
		}
	}
	if !lit || !dark {
		t.Fatalf("a minute after the last key the caret was lit %v and dark %v, want it still blinking", lit, dark)
	}
}

func TestTheCaretHidesWhileTheWindowIsInactive(t *testing.T) {
	wr := newWriter(t, 400)
	b := &wr.area.blink
	wr.w.Input(input.WindowFocusLost{})
	wr.run(1)
	if v := b.value(); v != 0 {
		t.Fatalf("caret at %v with the window inactive, want hidden", v)
	}
	if b.stop != nil {
		t.Fatal("the caret still blinks with the window inactive")
	}
	wr.w.Input(input.WindowFocusGained{})
	wr.run(1)
	if v := b.value(); v != 1 {
		t.Fatalf("caret at %v with the window back, want lit", v)
	}
	if b.stop == nil {
		t.Fatal("the caret does not blink again with the window back")
	}
}

// A caret that gets the keyboard in a window without it, as in a dialog
// that opens in a window behind another, stays hidden until the window
// has the keyboard.
func TestACaretFocusedInAWindowWithoutTheKeyboardStaysHidden(t *testing.T) {
	wr := newWriter(t, 400)
	b := &wr.area.blink
	wr.w.Input(input.WindowFocusLost{})
	wr.run(1)
	// Focused again, as by a dialog opening, with the window still
	// without the keyboard.
	wr.w.Input(input.PointerDown{Pos: geom.Pt(20, 20), Clicks: 1})
	wr.w.Input(input.PointerUp{Pos: geom.Pt(20, 20)})
	wr.run(1)
	if v := b.value(); v != 0 || b.stop != nil {
		t.Fatalf("caret at %v, blinking %v, focused in a window without the keyboard", v, b.stop != nil)
	}
	wr.w.Input(input.WindowFocusGained{})
	wr.run(1)
	if v := b.value(); v != 1 || b.stop == nil {
		t.Fatalf("caret at %v, blinking %v, with the keyboard back", v, b.stop != nil)
	}
}

func TestTheCaretFadesInAFewStepsAtAnyRefreshRate(t *testing.T) {
	wr := newWriter(t, 400)
	b := &wr.area.blink
	// Frames at 240 a second: a blink's fades take few of them, and the caret passes through levels between lit
	// and dark on the way.
	levels := map[float32]bool{}
	changes := 0
	last := b.value()
	for range 240 * 3 {
		wr.w.Frame(time.Second / 240)
		if v := b.value(); v != last {
			changes++
			levels[v] = true
			last = v
		}
	}
	if len(levels) < 4 {
		t.Fatalf("the caret took %d levels, want it to fade through some between lit and dark", len(levels))
	}
	// Three seconds hold at most six fades, of fadeSteps changes each.
	if most := 3 * 2 * fadeSteps; changes > most {
		t.Fatalf("the caret changed %d times in three seconds, want at most %d, however fast the frames", changes, most)
	}
}
