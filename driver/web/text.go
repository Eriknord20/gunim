//go:build js && wasm

package web

import (
	"strings"
	"syscall/js"
	"time"
	"unicode/utf16"

	"github.com/marrasen/gunim/input"
)

// DOM selections count UTF-16 code units; Gunim text state counts UTF-8 bytes.
func utf16Offset(s string, at int) int {
	at = max(0, min(at, len(s)))
	return len(utf16.Encode([]rune(s[:at])))
}
func byteOffset(s string, at int) int {
	n := 0
	for i, r := range s {
		if n >= at {
			return i
		}
		if r > 0xffff {
			n += 2
		} else {
			n++
		}
	}
	return len(s)
}

func (d *Driver) syncText(w *Window) {
	d.mu.Lock()
	if d.typing != w || (w.state != nil && w.stateSeq < w.editSeq) {
		d.mu.Unlock()
		return
	}
	composing := d.composing
	if w.state == nil {
		d.mu.Unlock()
		if !composing {
			d.clearMirror()
		}
		return
	}
	state := *w.state
	d.mu.Unlock()
	if composing {
		return
	}
	d.useMirror(state.Secret)
	t := d.text
	// A secret is only there while it has focus: see listenText.
	if state.Secret && !t.Equal(js.Global().Get("document").Get("activeElement")) {
		return
	}
	d.syncingText = true
	defer func() { d.syncingText = false }()
	if t.Get("value").String() != state.Text {
		t.Set("value", state.Text)
	}
	a, b := utf16Offset(state.Text, state.Selection[0]-state.Start), utf16Offset(state.Text, state.Selection[1]-state.Start)
	direction := "forward"
	if a > b {
		a, b = b, a
		direction = "backward"
	}
	if t.Get("selectionStart").Int() != a || t.Get("selectionEnd").Int() != b || t.Get("selectionDirection").String() != direction {
		t.Call("setSelectionRange", a, b, direction)
	}
}

// clearMirror readies the mirror for a window with no text state, which takes
// typing as it comes: the plain field, holding nothing of the field before.
func (d *Driver) clearMirror() {
	d.useMirror(false)
	d.syncingText = true
	d.text.Set("value", "")
	d.syncingText = false
}

// useMirror moves typing to the password field or back, taking focus along, and
// leaves nothing behind in the one it left.
func (d *Driver) useMirror(secret bool) {
	to := d.plain
	if secret {
		to = d.secret
	}
	if to.Equal(d.text) {
		return
	}
	focused := d.text.Equal(js.Global().Get("document").Get("activeElement"))
	d.syncingText = true
	d.text.Set("value", "")
	d.syncingText = false
	d.text = to
	if focused {
		to.Call("focus", map[string]any{"preventScroll": true})
	}
}
func (d *Driver) domEdited(composing bool) {
	if d.syncingText {
		return
	}
	w := d.typingWindow()
	if w == nil {
		return
	}
	d.mu.Lock()
	if w.state == nil {
		d.mu.Unlock()
		// A window that gives no text state takes what is typed as it comes.
		if !composing {
			if text := d.text.Get("value").String(); text != "" {
				d.syncingText = true
				d.text.Set("value", "")
				d.syncingText = false
				w.in.Push(input.TextInput{Text: text, Time: time.Now()})
			}
		}
		return
	}
	old := *w.state
	d.mu.Unlock()
	text := d.text.Get("value").String()
	a := old.Start + byteOffset(text, d.text.Get("selectionStart").Int())
	b := old.Start + byteOffset(text, d.text.Get("selectionEnd").Int())
	if d.text.Get("selectionDirection").String() == "backward" {
		a, b = b, a
	}
	comp := [2]int{b, b}
	if composing {
		end := len(text) - len(d.compSuffix)
		comp = [2]int{old.Start + min(d.compStart, len(text)), old.Start + max(d.compStart, end)}
	}
	// Equal ends are no composition, wherever they stand.
	sameComposing := old.Composing == comp || (old.Composing[0] == old.Composing[1] && comp[0] == comp[1])
	if old.Text == text && old.Selection == [2]int{a, b} && sameComposing {
		return
	}
	edit := input.TextEdit{Replace: [2]int{old.Start, old.End()}, With: text, Selection: [2]int{a, b}, Composing: comp, Time: time.Now()}
	d.mu.Lock()
	w.editSeq++
	edit.Seq = w.editSeq
	next, ok := old.Apply(edit)
	if ok {
		w.state = &next
	}
	d.mu.Unlock()
	if ok {
		w.in.Push(edit)
	}
}
func (d *Driver) listenText(doc js.Value) {
	for _, t := range []js.Value{d.plain, d.secret} {
		d.on(t, "compositionstart", false, func(js.Value) {
			w := d.typingWindow()
			if w == nil {
				return
			}
			text := t.Get("value").String()
			d.compStart = byteOffset(text, t.Get("selectionStart").Int())
			end := byteOffset(text, t.Get("selectionEnd").Int())
			d.compSuffix = text[end:]
			d.composing = true
		})
		d.on(t, "compositionend", false, func(js.Value) { d.composing = false; d.domEdited(false) })
		d.on(t, "beforeinput", false, func(e js.Value) {
			w := d.typingWindow()
			if w == nil {
				return
			}
			d.mu.Lock()
			hasState := w.state != nil
			single := hasState && !w.state.Multiline
			d.mu.Unlock()
			switch kind := e.Get("inputType").String(); {
			case single && (kind == "insertLineBreak" || kind == "insertParagraph"):
				e.Call("preventDefault")
				w.in.Push(input.KeyPress{Key: input.KeyEnter, Time: time.Now()})
			case hasState && kind == "insertText" && !d.composing && e.Get("cancelable").Bool() && w.in.Waiting():
				// The engine is held up, as waiting on the clipboard, with input it has
				// yet to read, so the mirror is out of date. The text follows that input
				// in order, and the mirror catches up with the engine.
				e.Call("preventDefault")
				if data := e.Get("data"); data.Type() == js.TypeString {
					w.in.Push(input.TextInput{Text: data.String(), Time: time.Now()})
				}
			}
		})
		d.on(t, "input", false, func(e js.Value) {
			is := e.Get("isComposing")
			d.domEdited(d.composing || (!is.IsUndefined() && is.Bool()))
		})
		d.on(t, "paste", false, func(e js.Value) {
			c := e.Get("clipboardData")
			if !c.IsNull() && !c.IsUndefined() {
				d.clipboard = c.Call("getData", "text/plain").String()
			}
		})
	}
	d.on(doc, "selectionchange", true, func(js.Value) {
		if !d.syncingText && !d.composing && doc.Get("activeElement").Equal(d.text) {
			d.domEdited(false)
		}
	})
	// A secret stays only while it has focus, and cannot be copied out.
	d.on(d.secret, "blur", true, func(js.Value) {
		d.syncingText = true
		d.secret.Set("value", "")
		d.syncingText = false
	})
	d.on(d.secret, "focus", true, func(js.Value) {
		if w := d.typingWindow(); w != nil {
			d.syncText(w)
		}
	})
	for _, name := range []string{"copy", "cut"} {
		d.on(d.secret, name, false, func(e js.Value) { e.Call("preventDefault") })
	}
}

// editKeyOwnedByDOM says the browser handles a key in the mirror itself: composing,
// and cut, copy and paste. With no text state there is nothing in the mirror to cut
// or copy, so a window taking typing as it comes, as a terminal does, hears those.
func (d *Driver) editKeyOwnedByDOM(e js.Value) bool {
	is := e.Get("isComposing")
	if (!is.IsUndefined() && is.Bool()) || e.Get("key").String() == "Process" || e.Get("key").String() == "Unidentified" {
		return true
	}
	if altGr(e) {
		return false
	}
	if !e.Get("ctrlKey").Bool() && !e.Get("metaKey").Bool() {
		return false
	}
	switch strings.ToLower(e.Get("key").String()) {
	case "v":
		return true
	case "x", "c":
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.typing != nil && d.typing.state != nil
	}
	return false
}
