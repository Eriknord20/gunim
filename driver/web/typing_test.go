//go:build js && wasm

package web_test

import (
	"context"
	"strings"
	"syscall/js"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver/web"
	"github.com/marrasen/gunim/widget"
)

// editable is a text widget under test.
type editable interface {
	gunim.Node
	Text() string
	Selection() (start, end int)
}

// typist does what a browser does with keys, to the driver's real listeners and
// through a real engine, with nothing between the steps: a frame can be owing all
// the way, as it is for a fast typist. Dispatching an event returns once every
// goroutine has gone idle, as the browser's own events do.
type typist struct {
	t     *testing.T
	d     *web.Driver
	field editable
}

// show starts a program with root as its window's content, and returns the driver.
func show(t *testing.T, root gunim.Node) *web.Driver {
	t.Helper()
	d, err := web.Open()
	if err != nil {
		t.Fatal(err)
	}
	d.AfterLastWindow()
	ctx, cancel := context.WithCancel(context.Background())
	opened := make(chan *gunim.Window, 1)
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		err := gunim.Main(ctx, func(a *gunim.App) error {
			w, err := a.NewWindow(gunim.WindowOptions{Root: widget.NewSurface(), SystemFrame: true})
			if err != nil {
				return err
			}
			w.RegisterTheme(widget.Dark())
			gunim.RegisterView(w, "field", func(struct{}) gunim.Node { return root }, func(gunim.Node, struct{}, *gunim.UI) {})
			if err := w.Client().Mount(gunim.Root, "field", "field", struct{}{}); err != nil {
				return err
			}
			opened <- w
			for range w.Client().Intents() {
			}
			return nil
		})
		if err != nil {
			t.Error(err)
		}
	}()
	w := <-opened
	t.Cleanup(func() {
		w.Close()
		cancel()
		<-ended
	})
	return d
}

func eventOf(class, kind string, init map[string]any) js.Value {
	init["bubbles"], init["cancelable"] = true, true
	return js.Global().Get(class).New(kind, init)
}

func activeElement() js.Value { return js.Global().Get("document").Get("activeElement") }

// click presses and lets go at a point on the canvas, as a mouse does.
func click(d *web.Driver, x, y float64) {
	r := d.Canvas().Call("getBoundingClientRect")
	at := func(buttons int) map[string]any {
		return map[string]any{
			"pointerId": 1, "pointerType": "mouse", "button": 0, "buttons": buttons,
			"clientX": r.Get("left").Float() + x, "clientY": r.Get("top").Float() + y,
		}
	}
	d.Canvas().Call("dispatchEvent", eventOf("PointerEvent", "pointerdown", at(1)))
	d.Canvas().Call("dispatchEvent", eventOf("PointerEvent", "pointerup", at(0)))
}

// focusField clicks the field and waits for the driver to give the mirror the
// keyboard, once the engine has drawn its first frame.
func focusField(t *testing.T, d *web.Driver, field editable) {
	t.Helper()
	for give := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		click(d, 40, 20)
		if d.Mirror().Equal(activeElement()) {
			return
		}
		if time.Now().After(give) {
			t.Fatalf("the field never took the keyboard: %v has it", activeElement().Get("tagName"))
		}
	}
}

// key sends a key as a browser does, with its default action if nothing cancels it.
func (k *typist) key(name, code string, mods ...string) {
	k.t.Helper()
	init := func() map[string]any {
		m := map[string]any{"key": name, "code": code}
		for _, mod := range mods {
			m[mod] = true
		}
		return m
	}
	target := activeElement()
	if target.Call("dispatchEvent", eventOf("KeyboardEvent", "keydown", init())).Bool() {
		k.defaultAction(name, mods)
	}
	activeElement().Call("dispatchEvent", eventOf("KeyboardEvent", "keyup", init()))
}

// defaultAction types a key's character into the field that has focus now. Other
// keys here are the engine's, so a browser left to act on them is a failure.
func (k *typist) defaultAction(name string, mods []string) {
	k.t.Helper()
	if len([]rune(name)) != 1 || len(mods) != 0 {
		k.t.Errorf("the browser would act on %q %v itself", name, mods)
		return
	}
	k.insert(name)
}

// insert is the browser putting text in the mirror, from a key or a soft keyboard.
func (k *typist) insert(text string) {
	m := activeElement()
	before := eventOf("InputEvent", "beforeinput", map[string]any{"inputType": "insertText", "data": text})
	if !m.Call("dispatchEvent", before).Bool() {
		return
	}
	m.Call("setRangeText", text, m.Get("selectionStart"), m.Get("selectionEnd"), "end")
	m.Call("dispatchEvent", eventOf("InputEvent", "input", map[string]any{"inputType": "insertText", "data": text}))
}

// typed sends each character of s as a key.
func (k *typist) typed(s string) {
	k.t.Helper()
	for _, r := range s {
		k.key(string(r), "Key"+strings.ToUpper(string(r)))
		k.check("after " + string(r))
	}
}

// check says the mirror and the field hold the same text and caret.
func (k *typist) check(step string) {
	k.t.Helper()
	m := activeElement()
	if !m.Equal(k.d.Mirror()) {
		k.t.Fatalf("%s: the mirror lost focus to %v", step, m.Get("tagName"))
	}
	if got, want := m.Get("value").String(), k.field.Text(); got != want {
		k.t.Fatalf("%s: the mirror holds %q and the field %q", step, got, want)
	}
	a, b := k.field.Selection()
	if ga, gb := m.Get("selectionStart").Int(), m.Get("selectionEnd").Int(); ga != a || gb != b {
		k.t.Fatalf("%s: the mirror selects %d-%d and the field %d-%d, in %q", step, ga, gb, a, b, k.field.Text())
	}
}

func (k *typist) wants(text string, caret int) {
	k.t.Helper()
	if got := k.field.Text(); got != text {
		k.t.Fatalf("the field holds %q, want %q", got, text)
	}
	if a, b := k.field.Selection(); a != caret || b != caret {
		k.t.Fatalf("the selection is %d-%d in %q, want a caret at %d", a, b, text, caret)
	}
}

func typistFor(t *testing.T, field editable) *typist {
	d := show(t, field)
	focusField(t, d, field)
	return &typist{t: t, d: d, field: field}
}

func TestFastKeysAgreeWithTheField(t *testing.T) {
	field := widget.NewTextField()
	k := typistFor(t, field)
	const (
		backspace = "Backspace"
		left      = "ArrowLeft"
	)
	reset := func() {
		k.key("a", "KeyA", "ctrlKey")
		k.key("Delete", "Delete")
		k.check("after emptying the field")
		k.wants("", 0)
	}
	for _, c := range []struct {
		name string
		do   func()
		text string
		at   int
	}{
		{"backspace then a letter", func() {
			k.typed("abc")
			k.key(backspace, backspace)
			k.check("after backspace")
			k.typed("d")
		}, "abd", 3},
		{"left then a letter", func() {
			k.typed("hello")
			k.key(left, left)
			k.check("after left")
			k.typed("x")
		}, "hellxo", 5},
		{"left twice, delete, then a letter", func() {
			k.typed("hello")
			k.key(left, left)
			k.key(left, left)
			k.key("Delete", "Delete")
			k.check("after delete")
			k.typed("x")
		}, "helxo", 4},
		{"home, end and a word", func() {
			k.typed("mid")
			k.key("Home", "Home")
			k.typed("a")
			k.key("End", "End")
			k.typed("z")
		}, "amidz", 5},
		{"a selection a key made is replaced", func() {
			k.typed("abc")
			k.key(left, left, "shiftKey")
			k.key(left, left, "shiftKey")
			k.check("after selecting")
			k.typed("x")
		}, "ax", 2},
		{"select all then type", func() {
			k.typed("abc")
			k.key("a", "KeyA", "ctrlKey")
			k.check("after select all")
			k.typed("x")
		}, "x", 1},
		{"undo then type", func() {
			k.typed("abc")
			k.key("z", "KeyZ", "ctrlKey")
			k.check("after undo")
			k.typed("x")
		}, "x", 1},
		{"a held backspace between letters", func() {
			k.typed("abcd")
			for range 3 {
				k.key(backspace, backspace)
				k.check("after a repeat")
			}
			k.typed("e")
			k.key(backspace, backspace)
			k.typed("f")
		}, "af", 2},
		{"a soft keyboard's text and a key's backspace", func() {
			// As Gboard sends them: text with no key of its own, and a backspace
			// with no place.
			for _, s := range []string{"a", "b", "c"} {
				k.insert(s)
				k.check("after " + s)
			}
			k.key(backspace, "")
			k.check("after backspace")
			k.insert("d")
		}, "abd", 3},
		{"a composition starts on what a key left", func() {
			k.typed("abc")
			k.key(left, left)
			m := k.d.Mirror()
			m.Call("dispatchEvent", eventOf("CompositionEvent", "compositionstart", map[string]any{"data": ""}))
			m.Call("setRangeText", "日", m.Get("selectionStart"), m.Get("selectionEnd"), "end")
			m.Call("dispatchEvent", eventOf("InputEvent", "input", map[string]any{"inputType": "insertCompositionText", "data": "日", "isComposing": true}))
			m.Call("dispatchEvent", eventOf("CompositionEvent", "compositionend", map[string]any{"data": "日"}))
			k.check("after the composition")
		}, "ab日c", 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			k.t = t
			reset()
			c.do()
			k.check("at the end")
			k.wants(c.text, c.at)
			// The same with the frames left to run, as for a slow typist.
			time.Sleep(50 * time.Millisecond)
			k.check("after the frames")
			k.wants(c.text, c.at)
		})
	}
}

func TestAKeyJustAfterAClickTypesAtTheClick(t *testing.T) {
	field := widget.NewTextField()
	k := typistFor(t, field)
	k.typed("abcdef")
	// A click far to the left puts the caret at the start.
	click(k.d, 0, 20)
	k.check("after the click")
	k.typed("x")
	k.wants("xabcdef", 1)
}

func TestLongTextEditsTheStretchTheMirrorHolds(t *testing.T) {
	field := widget.NewTextArea()
	long := strings.Repeat("0123456789\r\n", 1000)
	field.SetText(long, nil)
	d := show(t, field)
	focusField(t, d, field)
	k := &typist{t: t, d: d, field: field}
	k.key("Home", "Home", "ctrlKey")
	k.key("End", "End", "ctrlKey")
	m := d.Mirror()
	if strings.Contains(m.Get("value").String(), "\r") {
		t.Error("the mirror holds a \\r")
	}
	k.key("a", "KeyA")
	k.key("Backspace", "Backspace")
	k.key("b", "KeyB")
	if got := field.Text(); got != long+"b" {
		t.Fatalf("the text ends %q, %d runes, want %d", got[len(got)-20:], len(got), len(long)+1)
	}
}
