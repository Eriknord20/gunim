package widget

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/input"
)

// TypeAheadPause is how long a pause in typing at a list starts the search again, as in Windows' lists.
const TypeAheadPause = time.Second

// TypeAhead gathers the text typed at a list into what to find, as a file manager does: text typed close together
// adds up, and a pause of TypeAheadPause starts again.
type TypeAhead struct {
	text string
	last time.Time
}

// Type adds s, typed at t, and returns the text to find.
func (a *TypeAhead) Type(s string, t time.Time) string {
	if !a.Typing(t) {
		a.text = ""
	}
	a.text += s
	a.last = t
	return a.text
}

// Typing reports whether a search is under way at t, so a Space typed then is part of it.
func (a *TypeAhead) Typing(t time.Time) bool { return a.text != "" && t.Sub(a.last) < TypeAheadPause }

// Reset forgets the text typed.
func (a *TypeAhead) Reset() { a.text = "" }

// keeps reports whether a list that finds what is typed, as on says, keeps press e from the window's keys: a Space
// while the search is under way, which goes into it.
func (a *TypeAhead) keeps(e input.KeyPress, u *gunim.UI, on func(text string, u *gunim.UI) gunim.Intent) bool {
	return on != nil && e.Key == input.KeySpace && e.Mods == 0 && a.Typing(u.Now())
}

// take works e for a list at from that sends on(text) with the text to find, and reports whether it took e: text
// typed, and a Space while the search is under way, which the text that follows carries. A press of the pointer
// starts the search again.
func (a *TypeAhead) take(e input.Event, u *gunim.UI, from gunim.Node, on func(text string, u *gunim.UI) gunim.Intent) bool {
	if on == nil {
		return false
	}
	switch e := e.(type) {
	case input.TextInput:
		if v := on(a.Type(e.Text, u.Now()), u); v != nil {
			u.Send(from, v)
		}
		return true
	case input.KeyPress:
		return e.Key == input.KeySpace && e.Mods == 0 && a.Typing(u.Now())
	case input.PointerDown:
		a.Reset()
	}
	return false
}

// FindTyped returns the item text finds among n items named by name, or -1: the first whose name starts with text,
// ignoring case, from the item at from on and round to the start. One letter typed over and over steps from item to
// item that starts with it, beginning after from.
func FindTyped(text string, n, from int, name func(i int) string) int {
	if text == "" || n == 0 {
		return -1
	}
	want := strings.ToLower(text)
	start := from
	if r, _ := utf8.DecodeRuneInString(want); strings.Count(want, string(r)) == utf8.RuneCountInString(want) {
		want = string(r)
		start = from + 1
	}
	start = max(start, 0)
	for k := range n {
		i := (start + k) % n
		if strings.HasPrefix(strings.ToLower(name(i)), want) {
			return i
		}
	}
	return -1
}
