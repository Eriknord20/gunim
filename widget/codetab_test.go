package widget

import (
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// codeForm is a code editor as one field of a form, between a text
// field above and a button below.
type codeForm struct {
	code    *CodeEditor
	field   *TextField
	save    *Button
	w       *gunim.Window
	run     func(int)
	focused func() gunim.Node
}

// codeFormStage shows a codeForm, with the keyboard on the field.
func codeFormStage(t *testing.T, movesOn bool) codeForm {
	t.Helper()
	field, code, save := NewTextField(), NewCodeEditor(), NewButton("Save")
	code.Highlight = nil
	code.MaxRows = 3
	code.TabMovesOn = movesOn
	code.SetText("ls", nil)
	w, run := stage(t, &frame{child: Column(field, code, save), size: geom.Sz(600, 300)})
	focused := focusProbe(t, w, run)
	click(w, 20, 10)
	run(1)
	if f := focused(); f != field {
		t.Fatalf("a click on the field put the keyboard on %T", f)
	}
	return codeForm{code, field, save, w, run, focused}
}

// With TabMovesOn, Tab and Shift+Tab move through the editor as through
// any field, and as the keyboard leaves it either way, its focus fades
// out on every frame.
func TestCodeTabMovesOnLeavesTabToFocus(t *testing.T) {
	form := codeFormStage(t, true)
	code, field, save, w, run, focused := form.code, form.field, form.save, form.w, form.run, form.focused
	// A fade from 1 to 0 takes many frames; a frame that moves the
	// focus further than this is a jump.
	const fadeStep = 0.25
	press := func(mods input.Mods, want gunim.Node) {
		t.Helper()
		w.Input(input.KeyPress{Key: input.KeyTab, Mods: mods})
		run(1)
		if f := focused(); f != want {
			t.Fatalf("Tab with %v put the keyboard on %T, want %T", mods, f, want)
		}
	}
	for _, way := range []struct {
		name       string
		mods       input.Mods
		from, onto gunim.Node
	}{
		{"Tab", 0, field, save},
		{"Shift+Tab", input.ModShift, save, field},
	} {
		if f := focused(); f != way.from {
			t.Fatalf("%s: the keyboard is on %T, want %T", way.name, f, way.from)
		}
		press(way.mods, code)
		run(60)
		last := code.focus.Value()
		if last != 1 {
			t.Fatalf("%s: the editor's focus rests at %v with the keyboard on it, want 1", way.name, last)
		}
		// From the frame the key goes in, the focus falls on every frame,
		// by no more than a step a fade takes, until it rests at 0.
		w.Input(input.KeyPress{Key: input.KeyTab, Mods: way.mods})
		frames := 0
		for ; last > 0; frames++ {
			if frames > 120 {
				t.Fatalf("%s: the editor's focus is still at %v after %d frames", way.name, last, frames)
			}
			run(1)
			v := code.focus.Value()
			if v >= last || v < 0 || last-v > fadeStep {
				t.Fatalf("%s: frame %d took the editor's focus from %v to %v, want a fall of at most %v", way.name, frames, last, v, fadeStep)
			}
			last = v
		}
		if f := focused(); f != way.onto {
			t.Fatalf("%s: the keyboard left the editor for %T, want %T", way.name, f, way.onto)
		}
	}
	if got := code.Text(); got != "ls" {
		t.Fatalf("Tab through the editor changed its code to %q", got)
	}
}

// Without TabMovesOn, Tab stays in the editor and indents.
func TestCodeTabIndentsWithoutTabMovesOn(t *testing.T) {
	form := codeFormStage(t, false)
	code, w, run, focused := form.code, form.w, form.run, form.focused
	w.Input(input.KeyPress{Key: input.KeyTab})
	run(1)
	if f := focused(); f != code {
		t.Fatalf("Tab from the field put the keyboard on %T, want the editor", f)
	}
	typeKeys(w, run, input.ModControl, input.KeyHome)
	typeKeys(w, run, 0, input.KeyTab)
	if f := focused(); f != code {
		t.Fatalf("Tab in the editor moved the keyboard to %T", f)
	}
	if got := code.Text(); got != "\tls" {
		t.Fatalf("Tab in the editor made %q, want a tab typed", got)
	}
	typeKeys(w, run, input.ModShift, input.KeyTab)
	if got, f := code.Text(), focused(); got != "ls" || f != code {
		t.Fatalf("Shift+Tab in the editor made %q with the keyboard on %T, want the tab taken back", got, f)
	}
}

// With TabMovesOn, a tab typed or pasted becomes a space.
func TestCodeTabMovesOnTurnsTabsToSpaces(t *testing.T) {
	c, w, run := codeStage(t, "")
	c.TabMovesOn = true
	w.Input(input.TextInput{Text: "a\tb"})
	run(1)
	if got := c.Text(); got != "a b" {
		t.Fatalf("typing a tab made %q", got)
	}
	if err := w.Offscreen().SetClipboard("\n\tc\td"); err != nil {
		t.Fatal(err)
	}
	typeKeys(w, run, input.ModControl, input.KeyV)
	if got := c.Text(); got != "a b\n c d" {
		t.Fatalf("pasting tabs made %q", got)
	}
}

// With TabMovesOn, Enter keeps the line's indent and adds none after an
// opening bracket or a colon, and a closing brace takes none back.
func TestCodeTabMovesOnEnterAddsNoIndent(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"if x {", "if x {\n"},
		{"  case a:", "  case a:\n  "},
		{"\tf(", "\tf(\n\t"},
		{"  [", "  [\n  "},
	} {
		c, w, run := codeStage(t, tc.src)
		c.TabMovesOn = true
		typeKeys(w, run, input.ModControl, input.KeyEnd)
		typeKeys(w, run, 0, input.KeyEnter)
		if got := c.Text(); got != tc.want {
			t.Errorf("Enter after %q made %q, want %q", tc.src, got, tc.want)
		}
	}
	c, w, run := codeStage(t, "\tx")
	c.TabMovesOn = true
	typeKeys(w, run, input.ModControl, input.KeyEnd)
	typeKeys(w, run, 0, input.KeyEnter)
	w.Input(input.TextInput{Text: "}"})
	run(1)
	if got := c.Text(); got != "\tx\n\t}" {
		t.Fatalf("a closing brace made %q, want the indent kept", got)
	}
}

// With TabMovesOn, Paste from the right-click menu turns tabs to spaces
// too, as it goes through the editor's own paste.
func TestCodeTabMovesOnMenuPasteTurnsTabsToSpaces(t *testing.T) {
	c, w, run := codeStage(t, "")
	c.TabMovesOn = true
	if err := w.Offscreen().SetClipboard("a\tb"); err != nil {
		t.Fatal(err)
	}
	rightClick(w, 300, 150)
	run(2)
	if c.menuItems == nil {
		t.Fatal("a right click opened no edit menu")
	}
	c.menuItems.OnPick(editPaste, stageUI(t, w, run))
	run(2)
	if got := c.Text(); got != "a b" {
		t.Fatalf("Paste from the menu made %q, want the tab a space", got)
	}
}
