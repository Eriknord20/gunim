package widget

import (
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// editor is the editing behind TextField and TextArea: the text, the
// caret and selection, the input method's composition, and every edit
// and key. What differs between the two is how the text is laid out,
// which each supplies as a navigator.
type editor struct {
	text          []rune
	caret, anchor int
	multiline     bool
	// secret keeps the text off the clipboard.
	secret bool
	// tabs keeps typed and pasted tabs, as code needs; otherwise a tab
	// becomes a space.
	tabs bool
	// readOnly keeps the text as it is: the caret moves and the text
	// selects and copies, and every edit is skipped.
	readOnly bool

	// hintX is the caret's x as an arrow key left it. Where text of two
	// directions meets, one rune index has two places on screen, and
	// hintX says which. hinted is false once the caret moves otherwise.
	hintX  float32
	hinted bool
	// goalX is the x Up and Down aim for, kept across lines of different
	// lengths so the caret comes back to its column.
	goalX float32
	goal  bool

	// preedit is the input method's composition, and preSel the part of
	// it the input method highlights, in runes; both ends are its caret
	// when nothing is highlighted.
	preedit []rune
	preSel  [2]int

	// blink blinks the caret while the widget has the keyboard.
	blink blinker

	// undo holds the steps of edits made, and redo the steps undone, the latest last.
	undo, redo []step
	// last is the kind of the latest edit, which the next one of the same kind joins in one step, lastEnd where
	// the caret was after it, lastAt when it was, and lastSpace whether it typed a space, which ends a word.
	last      editKind
	lastEnd   int
	lastAt    time.Time
	lastSpace bool

	// pasting is set while a paste goes in, which is a step of undo of its own.
	pasting bool
	// pasteImage, when set, takes a picture on the clipboard in place of its text, and reports whether it did.
	pasteImage func(u *gunim.UI) bool

	// changed is called after every edit.
	changed func(u *gunim.UI)
	// wording is set while a finger held still on a word goes on to
	// drag, selecting a word at a time; words is the word it first
	// selected.
	wording bool
	words   [2]int
	// handles are the popups of the selection's two handles while they
	// show, and handleAt where each hangs; see handles.go.
	handles  [2]*gunim.Popup
	handleAt [2]geom.Rect
	// menu is the edit menu's popup while it is open, and menuItems the
	// menu in it.
	menu      *gunim.Popup
	menuItems *Menu
	// edited is set by typing, deleting and composing, and cleared by
	// the widget's next layout. The caret jumps after an edit, so it
	// keeps up with the text; it glides when it only moves.
	edited bool

	// mark is a rune whose byte offset is known, kept up through edits,
	// so TextState counts bytes from near the selection.
	mark byteMark
	// change and taken record what changed since the layout last read
	// the text; see shownChange.
	change textChange
	taken  taken
	// version counts changes to the text, and drawn is the text with
	// the composition in it; see shown.
	version uint64
	drawn   drawn
	// said is the text as a string, made at the version before saidAt, so a screen reader listening reads the text
	// without it copied every frame.
	said   string
	saidAt uint64
}

// textString returns the text as a string, made again only once the text has changed.
func (e *editor) textString() string {
	if e.saidAt != e.version+1 {
		e.said, e.saidAt = string(e.text), e.version+1
	}
	return e.said
}

// step is one step of undo or redo: the edits that make it, in the
// order made, and the selection before them.
type step struct {
	edits         []textEdit
	caret, anchor int
}

// textEdit is one change to the text: at rune at, old became new.
type textEdit struct {
	at       int
	old, new []rune
}

// editKind is what an edit did, for grouping edits into steps of undo.
type editKind uint8

const (
	otherEdit editKind = iota
	typing
	deleting
)

// Undo history limits: at most maxUndo steps, and edits further apart than undoPause make steps of their own.
const (
	maxUndo   = 500
	undoPause = 2 * time.Second
)

// remember saves an edit of kind that replaces start to end with with, before it is made, as a new step of undo
// unless it carries on the step before: typing on, or deleting on, with nothing else in between. A step keeps only
// the runes its edits changed, so it costs what the edits do.
func (e *editor) remember(kind editKind, start, end int, with []rune) {
	joins := kind != otherEdit && kind == e.last && time.Since(e.lastAt) < undoPause &&
		(kind == deleting && (end == e.lastEnd || start == e.lastEnd) ||
			kind == typing && start == e.lastEnd && (!e.lastSpace || unicode.IsSpace(with[0])))
	if !joins || len(e.undo) == 0 {
		e.undo = append(e.undo, step{caret: e.caret, anchor: e.anchor})
		if len(e.undo) > maxUndo {
			e.undo = slices.Delete(e.undo, 0, len(e.undo)-maxUndo)
		}
	}
	top := &e.undo[len(e.undo)-1]
	top.edits = append(top.edits, textEdit{at: start, old: slices.Clone(e.text[start:end]), new: slices.Clone(with)})
	e.redo = e.redo[:0]
	e.last, e.lastAt = kind, time.Now()
	e.lastSpace = kind == typing && unicode.IsSpace(with[0])
}

// undoEdit puts the text back as it was before the latest step, and redoEdit puts back the step undone last.
func (e *editor) undoEdit(u *gunim.UI) { e.travel(&e.undo, &e.redo, u) }
func (e *editor) redoEdit(u *gunim.UI) { e.travel(&e.redo, &e.undo, u) }

// travel takes back the latest step of from, and saves on to the step
// that makes it again.
func (e *editor) travel(from, to *[]step, u *gunim.UI) {
	if len(*from) == 0 || e.readOnly {
		return
	}
	s := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	back := step{edits: make([]textEdit, len(s.edits)), caret: e.caret, anchor: e.anchor}
	for i, ed := range s.edits {
		back.edits[len(s.edits)-1-i] = textEdit{at: ed.at, old: ed.new, new: ed.old}
	}
	*to = append(*to, back)
	for _, ed := range back.edits {
		e.splice(ed.at, ed.at+len(ed.old), ed.new)
	}
	e.caret, e.anchor = s.caret, s.anchor
	e.hinted, e.goal = false, false
	e.last = otherEdit
	e.edited = true
	if e.changed != nil {
		e.changed(u)
	}
}

// forget drops the history, for text set from outside.
func (e *editor) forget() {
	e.undo, e.redo, e.last = nil, nil, otherEdit
}

// setText puts rs in place of the whole text, for text set from
// outside.
func (e *editor) setText(rs []rune) {
	e.splice(0, len(e.text), rs)
}

// splice puts with in place of runes start to end of the text. It
// changes the text in place, so an edit costs what it changes and a
// move of the runes after it, and keeps up the byte offset TextState
// starts from and the record of what changed for the next layout.
func (e *editor) splice(start, end int, with []rune) {
	e.mark.splice(e.text, start, end, with)
	e.change.splice(len(e.text), start, end)
	e.version++
	e.text = slices.Replace(e.text, start, end, with...)
}

// byteMark is a rune of the text and the byte it starts at, from which
// the bytes to a rune nearby are counted.
type byteMark struct{ rune, byte int }

// splice moves the mark for runes start to end of text becoming with.
func (m *byteMark) splice(text []rune, start, end int, with []rune) {
	switch {
	case m.rune <= start:
	case m.rune >= end:
		m.rune += len(with) - (end - start)
		m.byte += byteLen(with) - byteLen(text[start:end])
	default:
		m.byte -= byteLen(text[start:m.rune])
		m.rune = start
	}
}

// byteOf returns the byte rune i of text starts at, counted from the
// mark, which then moves to i.
func (m *byteMark) byteOf(text []rune, i int) int {
	i = max(0, min(i, len(text)))
	if m.rune > len(text) {
		m.rune, m.byte = 0, 0
	}
	if i >= m.rune {
		m.byte += byteLen(text[m.rune:i])
	} else {
		m.byte -= byteLen(text[i:m.rune])
	}
	m.rune = i
	return m.byte
}

// textChange records what has changed in the text since a layout last
// read it: head and tail count the runes at its start and end that
// stayed as they were, and was is how long it was then. Set says
// something changed.
type textChange struct {
	head, tail, was int
	set             bool
}

// splice records runes start to end of a text n long becoming others.
func (c *textChange) splice(n, start, end int) {
	if !c.set {
		*c = textChange{head: n, tail: n, was: n, set: true}
	}
	c.head = min(c.head, start)
	c.tail = min(c.tail, n-end)
}

// shownChange is what changed in the text as drawn, with the
// composition in it, since the layout last asked: the runes at its
// start and end that stayed, and how long it was. It reports false when
// nothing changed, and all of it as changed the first time it is asked.
func (e *editor) shownChange() (head, tail, was int, changed bool) {
	start, end := e.Selection()
	t := e.taken
	c := e.change
	same := len(e.preedit) == 0 || !c.set && t.ok && start == t.start && end == t.end
	if t.ok && !c.set && same && slices.Equal(t.pre, e.preedit) {
		return 0, 0, 0, false
	}
	n := len(e.text)
	if !c.set {
		c = textChange{head: n, tail: n, was: n}
	}
	head, tail = c.head, c.tail
	was = c.was
	if len(t.pre) > 0 {
		head, tail = min(head, t.start), min(tail, c.was-t.end)
		was += len(t.pre) - (t.end - t.start)
	}
	if len(e.preedit) > 0 {
		head, tail = min(head, start), min(tail, n-end)
	}
	if !t.ok {
		head, tail = 0, 0
	}
	e.change = textChange{}
	e.taken = taken{pre: append(t.pre[:0], e.preedit...), start: start, end: end, ok: true}
	return head, tail, was, true
}

// taken is the composition as the layout last read it, and the
// selection it was drawn over.
type taken struct {
	pre        []rune
	start, end int
	ok         bool
}

// navigator is what the editor needs from laid-out text.
type navigator interface {
	// caretX returns the x of a caret before rune i on its line.
	caretX(i int) float32
	// beside steps the caret one place on screen, as text.Run.Beside
	// does, crossing to the next or previous line at an edge.
	beside(i int, x float32, right bool) (int, float32)
	// lineStart and lineEnd return the ends of the line holding i.
	lineStart(i int) int
	lineEnd(i int) int
	// vertical moves n lines down, or up when n is negative, from i,
	// toward x. It reports false for text of one line.
	vertical(i int, x float32, n int) (int, bool)
	// page is how many lines a page holds.
	page() int
}

// Selection returns the selected runes' range, start before end.
func (e *editor) Selection() (start, end int) {
	return min(e.caret, e.anchor), max(e.caret, e.anchor)
}

// shown returns the text as drawn, with any composition in place of the
// selection, and where the composition starts.
// It builds the text with a composition once, and again only when the
// text or the composition changes.
func (e *editor) shown() (runes []rune, at int) {
	start, end := e.Selection()
	if len(e.preedit) == 0 {
		return e.text, start
	}
	d := &e.drawn
	if d.version != e.version || d.start != start || d.end != end || !slices.Equal(d.pre, e.preedit) {
		d.version, d.start, d.end = e.version, start, end
		d.pre = append(d.pre[:0], e.preedit...)
		d.text = append(d.text[:0], e.text[:start]...)
		d.text = append(d.text, e.preedit...)
		d.text = append(d.text, e.text[end:]...)
	}
	return d.text, start
}

// drawn is the text with a composition in it, as shown last built it,
// and the text's version, the selection and the composition it was
// built from.
type drawn struct {
	text, pre  []rune
	version    uint64
	start, end int
}

// drawnCaret returns the caret and anchor as drawn: the input method's,
// inside its composition, while one is in progress.
func (e *editor) drawnCaret() (caret, anchor int) {
	if len(e.preedit) > 0 {
		at, _ := e.Selection()
		return at + e.preSel[1], at + e.preSel[0]
	}
	return e.caret, e.anchor
}

// caretX returns where the caret is drawn on its line.
func (e *editor) caretX(n navigator) float32 {
	if e.hinted {
		return e.hintX
	}
	return n.caretX(e.caret)
}

// set moves the caret to i, keeping the anchor when extend is true.
func (e *editor) set(i int, extend bool) {
	e.caret = max(0, min(i, len(e.text)))
	if !extend {
		e.anchor = e.caret
	}
	e.hinted, e.goal = false, false
}

// textWindow is how many runes either side of the selection a long
// text shows the input method.
const textWindow = 2048

// TextState implements [gunim.TextEditor] for the widgets built on the
// editor. It returns the text as the input method sees it: as drawn, with
// any composition in place, and cut to textWindow runes either side of
// the selection. It counts bytes from a mark kept near the selection, so
// it costs the same in a long text.
func (e *editor) TextState() input.TextState {
	start, end := e.Selection()
	pre := e.preedit
	if len(pre) == 0 {
		start, end = 0, 0
	}
	n := len(e.text) - (end - start) + len(pre)
	caret, anchor := e.drawnCaret()
	lo := max(0, min(caret, anchor)-textWindow)
	hi := min(n, max(caret, anchor)+textWindow)
	// b returns the byte rune i of the text as drawn starts at.
	b := func(i int) int {
		switch {
		case len(pre) == 0:
			return e.mark.byteOf(e.text, i)
		case i <= start:
			return e.mark.byteOf(e.text, i)
		case i <= start+len(pre):
			return e.mark.byteOf(e.text, start) + byteLen(pre[:i-start])
		}
		return e.mark.byteOf(e.text, i-len(pre)+end-start) - e.mark.byteOf(e.text, end) +
			e.mark.byteOf(e.text, start) + byteLen(pre)
	}
	// The runes lo to hi of the text as drawn.
	rs := make([]rune, 0, hi-lo)
	rs = append(rs, e.text[min(lo, start):min(hi, start)]...)
	rs = append(rs, pre[max(0, min(lo-start, len(pre))):max(0, min(hi-start, len(pre)))]...)
	after := len(pre) - (end - start)
	rs = append(rs, e.text[max(lo-after, end):max(hi-after, end)]...)
	s := input.TextState{
		Text:      string(rs),
		Start:     b(lo),
		Multiline: e.multiline,
		Secret:    e.secret,
	}
	s.Selection = [2]int{b(anchor), b(caret)}
	if len(pre) > 0 {
		s.Composing = [2]int{b(start), b(start + len(pre))}
	} else {
		s.Composing = [2]int{s.Selection[1], s.Selection[1]}
	}
	return s
}

// commit puts typed or composed text in, as [input.TextInput] brings it,
// in place of the selection, which a composition is drawn over. It
// changes the text in place, so a key costs no more in a long text.
func (e *editor) commit(s string, u *gunim.UI) {
	if e.readOnly {
		return
	}
	e.preedit, e.preSel = nil, [2]int{}
	e.edited = true
	e.insert(s, u)
}

// compose takes the input method's latest composition, whose selection
// arrives in bytes. An empty one only ends the composition: the text
// and its selection stay as they were, as a desktop input method sends
// it when a composition is cancelled and just before one commits.
func (e *editor) compose(c input.Composing) {
	if e.readOnly {
		return
	}
	if c.Text == "" {
		if len(e.preedit) > 0 {
			e.preedit, e.preSel = nil, [2]int{}
			e.edited = true
		}
		return
	}
	e.preedit = []rune(c.Text)
	runeAt := func(b int) int {
		b = max(0, min(b, len(c.Text)))
		return utf8.RuneCountInString(c.Text[:b])
	}
	e.preSel = [2]int{runeAt(c.Selected[0]), runeAt(c.Selected[1])}
	e.hinted, e.goal = false, false
	e.edited = true
}

// edit makes an input method's edit. The composition is drawn over
// the text, in place of the selection, and stays out of the text
// itself: a word the input method takes up again to compose stays in
// the text until the composition commits. So only what the text itself
// gains or loses is a change and a step of undo, and it reaches the
// text as one replace of the runes that differ.
func (e *editor) edit(t input.TextEdit, u *gunim.UI) {
	if e.readOnly {
		return
	}
	rs, _ := e.shown()
	a, b := runeOfByte(rs, t.Replace[0]), runeOfByte(rs, t.Replace[1])
	if a < 0 || b < a {
		return
	}
	with := []rune(t.With)
	next := make([]rune, 0, len(rs)-(b-a)+len(with))
	next = append(next, rs[:a]...)
	next = append(next, with...)
	next = append(next, rs[b:]...)
	at := func(i int) int { return max(0, runeOfByte(next, i)) }
	c0, c1 := at(min(t.Composing[0], t.Composing[1])), at(max(t.Composing[0], t.Composing[1]))
	anchor, caret := at(t.Selection[0]), at(t.Selection[1])
	var pre []rune
	// clip moves a place in next into the composition.
	clip := func(i int) int { return max(0, min(i-c0, len(pre))) }
	if c0 < c1 {
		pre = slices.Clone(next[c0:c1])
		before, after := next[:c0], next[c1:]
		if len(before)+len(after) <= len(e.text) &&
			slices.Equal(e.text[:len(before)], before) && slices.Equal(e.text[len(e.text)-len(after):], after) {
			// The text around the composition is as it was: the
			// composition covers the runes between.
			e.anchor, e.caret = len(before), len(e.text)-len(after)
			e.preedit, e.preSel = pre, [2]int{clip(anchor), clip(caret)}
			e.hinted, e.goal = false, false
			e.edited = true
			return
		}
		// The edit changed the text around it too: that change goes in,
		// and the composition covers nothing.
		next = append(slices.Clone(before), after...)
	}

	// The runes that differ between the text and next.
	p := 0
	for p < len(e.text) && p < len(next) && e.text[p] == next[p] {
		p++
	}
	q := 0
	for q < len(e.text)-p && q < len(next)-p && e.text[len(e.text)-1-q] == next[len(next)-1-q] {
		q++
	}
	// Where a run of the same rune makes two places fit, as typing "l"
	// after "he" in "helo", the change goes where the edit put it, so a
	// word typed mid-text undoes as one step.
	for p > a && e.text[len(e.text)-q-1] == next[len(next)-q-1] {
		p--
		q++
	}
	added := next[p : len(next)-q]
	kept := added
	if len(added) > 0 {
		kept = []rune(e.clean(string(added)))
	}
	if p+q != len(e.text) || len(kept) > 0 {
		e.preedit = nil
		e.replace(p, len(e.text)-q, kept, u)
	}
	// Places in next, moved to the text as cleaning left it.
	moved := func(i int) int {
		switch {
		case i <= p:
			return i
		case i >= p+len(added):
			return i - len(added) + len(kept)
		}
		return min(i, p+len(kept))
	}
	if c0 < c1 {
		e.set(moved(c0), false)
		e.preedit, e.preSel = pre, [2]int{clip(anchor), clip(caret)}
	} else {
		e.preedit = nil
		e.set(moved(anchor), false)
		e.set(moved(caret), true)
	}
	e.edited = true
}

// byteLen returns how many bytes rs takes as UTF-8.
func byteLen(rs []rune) int {
	n := 0
	for _, r := range rs {
		n += runeBytes(r)
	}
	return n
}

// runeBytes returns how many bytes r takes as UTF-8, where an invalid
// rune becomes the replacement character.
func runeBytes(r rune) int {
	if n := utf8.RuneLen(r); n > 0 {
		return n
	}
	return utf8.RuneLen(utf8.RuneError)
}

// runeOfByte returns the rune that byte b of rs as UTF-8 starts, len(rs)
// for its end, and -1 for a byte past it. A byte inside a rune counts
// as that rune's start.
func runeOfByte(rs []rune, b int) int {
	if b < 0 {
		return -1
	}
	n := 0
	for i, r := range rs {
		if n >= b {
			return i
		}
		n += runeBytes(r)
		if n > b {
			return i
		}
	}
	if b == n {
		return len(rs)
	}
	return -1
}

// The edit menu's items, in order.
const (
	editCut = iota
	editCopy
	editPaste
	editSelectAll
)

// contextPress takes a press of the secondary button at rune i, at in
// owner's space. A right click outside the selection puts the caret
// there, and one inside it keeps it, and the edit menu opens. A finger
// held still selects the word under it, as a phone's text field does,
// and may go on to drag over more words; the menu opens as it lifts.
func (e *editor) contextPress(owner gunim.Node, i int, at geom.Point, touch bool, u *gunim.UI) {
	if touch {
		e.closeMenu()
		e.press(i, 2, false)
		e.words = [2]int{min(e.anchor, e.caret), max(e.anchor, e.caret)}
		e.wording = true
		return
	}
	start, end := e.Selection()
	if start == end || i < start || i > end {
		e.set(i, false)
	}
	e.openMenu(owner, at, u)
}

// dragWords takes a finger held on a word moving on to rune i: the
// selection runs from the first word to the word at i, whole words at a
// time, either way.
func (e *editor) dragWords(i int) {
	ws, we := wordAt(e.text, i)
	if ws == we {
		ws, we = i, i
	}
	a, b := e.words[0], e.words[1]
	if ws < a {
		e.anchor, e.caret = b, ws
	} else {
		e.anchor, e.caret = a, max(we, b)
	}
	e.hinted, e.goal = false, false
}

// endWords ends a finger's choosing of words as it lifts at at, in
// host's space, opening the edit menu there and the handles at the
// selection's ends, unless it was called off at [input.Away], and
// reports whether one was going on.
func (e *editor) endWords(host textHost, at geom.Point, u *gunim.UI) bool {
	if !e.wording {
		return false
	}
	e.wording = false
	if at == input.Away {
		// Called off, as by a second finger coming down to pinch.
		return true
	}
	e.showHandles(host, u)
	e.openMenu(host, at, u)
	return true
}

// openMenu opens the edit menu at at, in owner's space: Cut, Copy,
// Paste and Select all, in the words of the theme it is drawn with
// (EditCut and the rest), with only Copy and Select all for text that
// stays as it is. Cut and Copy are dimmed with nothing selected, or for
// a secret.
func (e *editor) openMenu(owner gunim.Node, at geom.Point, u *gunim.UI) {
	e.closeMenu()
	start, end := e.Selection()
	none := start == end || e.secret
	th := u.Theme()
	m := NewMenu([]MenuItem{
		{Label: EditCut.Get(th), Disabled: none || e.readOnly},
		{Label: EditCopy.Get(th), Disabled: none},
		{Label: EditPaste.Get(th), Disabled: e.readOnly},
		{Label: EditSelectAll.Get(th)},
	})
	m.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		e.closeMenu()
		start, end := e.Selection()
		switch i {
		case editCut:
			e.clipboard(input.KeyX, start, end, u)
		case editCopy:
			e.clipboard(input.KeyC, start, end, u)
		case editPaste:
			e.clipboard(input.KeyV, start, end, u)
		case editSelectAll:
			e.clipboard(input.KeyA, start, end, u)
			// With everything selected, the menu stays for what to do
			// with it, and handles chosen by finger move to its ends.
			if host, ok := owner.(textHost); ok && e.handles[0] != nil {
				e.placeHandles(host)
			}
			e.openMenu(owner, at, u)
		}
		u.Invalidate()
		return nil
	}
	e.menuItems = m
	u.Cue(gunim.CueOpen, owner)
	e.menu = u.OpenPopup(owner, m, gunim.PopupOptions{
		Anchor:  geom.Rect{Min: at, Max: at},
		Max:     geom.Sz(600, 480),
		Dismiss: dismissed(owner, func(*gunim.UI) { e.closeMenu() }),
	})
}

// closeMenu closes the edit menu, if it is open.
func (e *editor) closeMenu() {
	if e.menu != nil {
		e.menu.Close()
		e.menu, e.menuItems = nil, nil
	}
}

// press places the caret for a click at rune i: selecting a word on a
// double click and everything on a triple, and extending with Shift.
func (e *editor) press(i, clicks int, shift bool) {
	if clicks < 2 {
		e.set(i, shift)
		return
	}
	e.anchor, e.caret = clickRange(e.text, i, clicks)
	e.hinted, e.goal = false, false
}

// clickRange returns what a click at rune i of rs selects: nothing on a
// single click, a word on a double and everything on a triple.
func clickRange(rs []rune, i, clicks int) (anchor, caret int) {
	switch {
	case clicks >= 3:
		return 0, len(rs)
	case clicks == 2:
		return wordAt(rs, i)
	}
	return i, i
}

// wordAt returns the word at rune i: the one i is in, or the one it ends,
// for a click just after a word. On a space, with no word ending there,
// it returns i alone, so a double click or a long press on the space
// after a text's last word selects nothing past it.
func wordAt(rs []rune, i int) (start, end int) {
	i = max(0, min(i, len(rs)))
	j := i
	if j == len(rs) || unicode.IsSpace(rs[j]) {
		if j == 0 || unicode.IsSpace(rs[j-1]) {
			return i, i
		}
		j--
	}
	start, end = j, j
	for start > 0 && !unicode.IsSpace(rs[start-1]) {
		start--
	}
	for end < len(rs) && !unicode.IsSpace(rs[end]) {
		end++
	}
	return start, end
}

// key handles a key press. It reports false for a key the widget or its
// ancestors should have: a shortcut, Tab, and Enter in a single line.
func (e *editor) key(k input.KeyPress, u *gunim.UI, n navigator) bool {
	// A press that typed text is typing, whatever modifiers it holds:
	// AltGr holds Control and Alt. Its text arrives as TextInput.
	if k.Typed {
		return true
	}
	ctrl := k.Mods.Has(input.ModControl) || k.Mods.Has(input.ModSuper)
	shift := k.Mods.Has(input.ModShift)
	start, end := e.Selection()
	switch k.Key {
	case input.KeyLeft, input.KeyRight:
		right := k.Key == input.KeyRight
		switch {
		case ctrl && right:
			e.set(wordEnd(e.text, e.caret+1), shift)
		case ctrl:
			e.set(wordStart(e.text, e.caret-1), shift)
		case start != end && !shift:
			if right {
				e.set(end, false)
			} else {
				e.set(start, false)
			}
		default:
			i, x := n.beside(e.caret, e.caretX(n), right)
			e.set(i, shift)
			e.hintX, e.hinted = x, true
		}
	case input.KeyUp, input.KeyDown, input.KeyPageUp, input.KeyPageDown:
		lines := 1
		if k.Key == input.KeyPageUp || k.Key == input.KeyPageDown {
			lines = n.page()
		}
		if k.Key == input.KeyUp || k.Key == input.KeyPageUp {
			lines = -lines
		}
		if !e.goal {
			e.goalX = e.caretX(n)
		}
		i, ok := n.vertical(e.caret, e.goalX, lines)
		if !ok {
			return false
		}
		goalX := e.goalX
		e.set(i, shift)
		e.goalX, e.goal = goalX, true
	case input.KeyHome:
		if ctrl {
			e.set(0, shift)
		} else {
			e.set(n.lineStart(e.caret), shift)
		}
	case input.KeyEnd:
		if ctrl {
			e.set(len(e.text), shift)
		} else {
			e.set(n.lineEnd(e.caret), shift)
		}
	case input.KeyBackspace:
		switch {
		case start != end:
			e.replace(start, end, nil, u)
		case ctrl:
			e.replace(wordStart(e.text, e.caret-1), e.caret, nil, u)
		case e.caret > 0:
			e.replace(e.caret-1, e.caret, nil, u)
		}
	case input.KeyDelete:
		switch {
		case start != end:
			e.replace(start, end, nil, u)
		case ctrl:
			e.replace(e.caret, wordEnd(e.text, e.caret+1), nil, u)
		case e.caret < len(e.text):
			e.replace(e.caret, e.caret+1, nil, u)
		}
	case input.KeyEnter, input.KeyKPEnter:
		if !e.multiline {
			return false
		}
		e.insert("\n", u)
	case input.KeyTab:
		return false // focus moves on
	case input.KeyEscape:
		return false // for whatever the field sits in, to close
	case input.KeyA, input.KeyC, input.KeyX, input.KeyV:
		if !ctrl {
			return true // typing: the letter arrives as TextInput
		}
		if shift {
			return false // Ctrl+Shift+A, C, X and V are shortcuts for someone else
		}
		e.clipboard(k.Key, start, end, u)
	case input.KeyZ, input.KeyY:
		if !ctrl {
			return true
		}
		if k.Key == input.KeyY || shift {
			e.redoEdit(u)
		} else {
			e.undoEdit(u)
		}
	default:
		// Keys with a modifier are shortcuts for someone else, as are the
		// function keys; the rest are typing, which arrives as TextInput.
		return !ctrl && !k.Mods.Has(input.ModAlt) && !functionKey(k.Key)
	}
	return true
}

func (e *editor) clipboard(k input.Key, start, end int, u *gunim.UI) {
	switch k {
	case input.KeyA:
		e.anchor, e.caret = 0, len(e.text)
		e.hinted, e.goal = false, false
	case input.KeyC:
		if start != end && !e.secret {
			u.SetClipboard(string(e.text[start:end]))
		}
	case input.KeyX:
		if start != end && !e.secret {
			u.SetClipboard(string(e.text[start:end]))
			e.last = otherEdit
			e.replace(start, end, nil, u)
		}
	case input.KeyV:
		if e.pasteImage != nil && e.pasteImage(u) {
			return
		}
		e.last = otherEdit
		e.paste(u.Clipboard(), u)
	default:
	}
}

// paste puts pasted text in place of the selection, as a step of undo of its own.
func (e *editor) paste(s string, u *gunim.UI) {
	e.pasting = true
	e.insert(s, u)
	e.pasting = false
}

// insert puts s in place of the selection.
func (e *editor) insert(s string, u *gunim.UI) {
	s = e.clean(s)
	if s == "" {
		return
	}
	start, end := e.Selection()
	e.replace(start, end, []rune(s), u)
}

// clean returns s as the text keeps it. A single line turns newlines
// and tabs into spaces; both keep printable text only.
func (e *editor) clean(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' && e.multiline, r == '\t' && e.tabs:
			return r
		case r == '\n' || r == '\t':
			return ' '
		case r == '\r' || unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
}

// replace swaps runes start to end for with, and leaves the caret after
// it.
func (e *editor) replace(start, end int, with []rune, u *gunim.UI) {
	if start < 0 || end > len(e.text) || start > end || e.readOnly {
		return
	}
	kind := otherEdit
	switch {
	case e.pasting:
	case start == end && len(with) == 1:
		kind = typing
	case len(with) == 0 && end-start == 1:
		kind = deleting
	}
	e.remember(kind, start, end, with)
	e.closeHandles()
	e.splice(start, end, with)
	e.set(start+len(with), false)
	e.lastEnd = e.caret
	e.edited = true
	if e.changed != nil {
		e.changed(u)
	}
}

// wordStart returns where the word at or before i starts, skipping
// spaces before it.
func wordStart(rs []rune, i int) int {
	i = max(0, min(i, len(rs)))
	for i > 0 && unicode.IsSpace(rs[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(rs[i-1]) {
		i--
	}
	return i
}

// wordEnd returns where the word at or after i ends, skipping spaces
// before it.
func wordEnd(rs []rune, i int) int {
	i = max(0, min(i, len(rs)))
	for i < len(rs) && unicode.IsSpace(rs[i]) {
		i++
	}
	for i < len(rs) && !unicode.IsSpace(rs[i]) {
		i++
	}
	return i
}

// aim sends a to v: at once after an edit, gliding with m otherwise.
func (e *editor) aim(a interface {
	Jump(float32)
	Animate(float32, anim.Motion)
}, v float32, m anim.Motion) {
	if e.edited {
		a.Jump(v)
		return
	}
	a.Animate(v, m)
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// functionKey reports whether k is one of F1 to F24.
func functionKey(k input.Key) bool {
	return k >= input.KeyF1 && k <= input.KeyF12 || k >= input.KeyF13 && k <= input.KeyF24
}
