//go:build js && wasm

package web

import (
	"strings"
	"syscall/js"
	"time"
	"unicode/utf8"

	"github.com/marrasen/gunim/input"
)

// fieldText returns s as a textarea holds it, with every line ending a single \n.
func fieldText(s string) string {
	if !strings.Contains(s, "\r") {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// fieldWidth returns how many UTF-16 code units the rune r at s[i] takes in a
// textarea. The \r of a \r\n takes none.
func fieldWidth(s string, i int, r rune) int {
	switch {
	case r == '\r' && i+1 < len(s) && s[i+1] == '\n':
		return 0
	case r > 0xffff:
		return 2
	}
	return 1
}

// DOM selections count UTF-16 code units in the field's text, and Gunim text state
// counts UTF-8 bytes in its own. utf16Offset goes from the second to the first, and
// byteOffset back.
func utf16Offset(s string, at int) int {
	at = max(0, min(at, len(s)))
	n := 0
	for i, r := range s[:at] {
		n += fieldWidth(s, i, r)
	}
	return n
}
func byteOffset(s string, at int) int {
	n := 0
	for i, r := range s {
		if n >= at {
			return i
		}
		n += fieldWidth(s, i, r)
	}
	return len(s)
}

// differ returns how many bytes two texts share at the start and at the end, whole
// characters only, and with the two never overlapping in either text.
func differ(was, now string) (start, end int) {
	for start < len(was) && start < len(now) && was[start] == now[start] {
		start++
	}
	for start > 0 && (start < len(was) && !utf8.RuneStart(was[start]) || start < len(now) && !utf8.RuneStart(now[start])) {
		start--
	}
	for end < len(was)-start && end < len(now)-start && was[len(was)-1-end] == now[len(now)-1-end] {
		end++
	}
	for end > 0 && !utf8.RuneStart(was[len(was)-end]) {
		end--
	}
	return start, end
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
	if state.Secret && (d.secretAway || !t.Equal(js.Global().Get("document").Get("activeElement"))) {
		return
	}
	d.syncingText = true
	defer func() { d.syncingText = false }()
	if want := fieldText(state.Text); t.Get("value").String() != want {
		t.Set("value", want)
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

// domEdited sends what the browser did to the mirror, as an edit of only what
// changed, so a line ending the field holds differently from the text stays as it
// is.
func (d *Driver) domEdited(composing bool) {
	if d.syncingText || d.secretAway && d.text.Equal(d.secret) {
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
	now := d.text.Get("value").String()
	anchor, caret := d.text.Get("selectionStart").Int(), d.text.Get("selectionEnd").Int()
	if d.text.Get("selectionDirection").String() == "backward" {
		anchor, caret = caret, anchor
	}
	was := fieldText(old.Text)
	front, back := differ(was, now)
	from := byteOffset(old.Text, utf16Offset(was, front))
	to := byteOffset(old.Text, utf16Offset(was, len(was)-back))
	with := now[front : len(now)-back]
	next := old.Text[:from] + with + old.Text[to:]
	place := func(units int) int { return old.Start + byteOffset(next, units) }
	// A caret the engine puts inside a \r\n shows before it in the field, where it
	// has not moved.
	sel := old.Selection
	moved := utf16Offset(old.Text, old.Selection[0]-old.Start) != anchor || utf16Offset(old.Text, old.Selection[1]-old.Start) != caret
	if was != now || moved {
		sel = [2]int{place(anchor), place(caret)}
	}
	comp := [2]int{sel[1], sel[1]}
	if composing {
		a, b := min(d.compStart, len(now)), len(now)-len(d.compSuffix)
		comp = [2]int{place(utf16Offset(now, a)), place(utf16Offset(now, max(a, b)))}
	}
	// Equal ends are no composition, wherever they stand.
	sameComposing := old.Composing == comp || (old.Composing[0] == old.Composing[1] && comp[0] == comp[1])
	if was == now && !moved && sameComposing {
		return
	}
	edit := input.TextEdit{Replace: [2]int{old.Start + from, old.Start + to}, With: with, Selection: sel, Composing: comp, Time: time.Now()}
	d.mu.Lock()
	w.editSeq++
	edit.Seq = w.editSeq
	after, ok := old.Apply(edit)
	if ok {
		w.state = &after
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
	// Switching windows blurs it and leaves it the active element, so what it holds
	// then is no edit of the user's: secretAway says so until focus comes back.
	d.on(d.secret, "blur", true, func(js.Value) {
		d.secretAway = true
		d.syncingText = true
		d.secret.Set("value", "")
		d.syncingText = false
	})
	d.on(d.secret, "focus", true, func(js.Value) {
		d.secretAway = false
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
