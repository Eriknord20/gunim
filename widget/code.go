package widget

import (
	"image/color"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/syntax"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// Code editor tokens: its text, its fills, and the colour of each kind
// of token a highlighter reports.
var (
	// CodeSize is the code's font size, and CodeLineHeight each line's
	// height as a multiple of the font's.
	CodeSize       = theme.Length("code.size", 13)
	CodeLineHeight = theme.Number("code.line.height", 1.45)
	// CodeTab is how many columns apart tab stops are.
	CodeTab = theme.Number("code.tab", 4)
	// CodePadding is the room around the code, and CodeRadius the
	// editor's corners.
	CodePadding = theme.Length("code.padding", 10)
	CodeRadius  = theme.Length("code.radius", 8)
	// CodeFill is behind the code, CodeGutterFill behind the line
	// numbers, CodeGutterInk the numbers, and CodeLineFill the band on
	// the caret's line.
	CodeEditorFill = theme.Color("code.fill", color.NRGBA{R: 0x16, G: 0x19, B: 0x20, A: 0xff})
	CodeGutterFill = theme.Color("code.gutter.fill", color.NRGBA{R: 0x1a, G: 0x1d, B: 0x25, A: 0xff})
	CodeGutterInk  = theme.Foreground("code.gutter.ink", color.NRGBA{R: 0x5c, G: 0x63, B: 0x70, A: 0xff})
	CodeLineFill   = theme.Color("code.line", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x09})
	// CodeScroll carries the code when it scrolls, damped so it stops
	// at the ends without swinging past them.
	CodeScroll = theme.Spring("code.scroll", anim.Spring{Response: 0.18, Damping: 1})
	// CodeProblem marks a line a [CodeMark] is on, and writes its
	// message.
	CodeProblem = theme.Color("code.problem", color.NRGBA{R: 0xff, G: 0x6b, B: 0x6b, A: 0xff})

	SyntaxKeyword  = theme.Foreground("syntax.keyword", color.NRGBA{R: 0xc6, G: 0x78, B: 0xdd, A: 0xff})
	SyntaxBuiltin  = theme.Foreground("syntax.builtin", color.NRGBA{R: 0x56, G: 0xb6, B: 0xc2, A: 0xff})
	SyntaxType     = theme.Foreground("syntax.type", color.NRGBA{R: 0xe5, G: 0xc0, B: 0x7b, A: 0xff})
	SyntaxFunction = theme.Foreground("syntax.function", color.NRGBA{R: 0x61, G: 0xaf, B: 0xef, A: 0xff})
	SyntaxString   = theme.Foreground("syntax.string", color.NRGBA{R: 0x98, G: 0xc3, B: 0x79, A: 0xff})
	SyntaxNumber   = theme.Foreground("syntax.number", color.NRGBA{R: 0xd1, G: 0x9a, B: 0x66, A: 0xff})
	SyntaxComment  = theme.Foreground("syntax.comment", color.NRGBA{R: 0x7f, G: 0x84, B: 0x8e, A: 0xff})
	SyntaxOperator = theme.Foreground("syntax.operator", color.NRGBA{R: 0x9d, G: 0xa5, B: 0xb4, A: 0xff})
	SyntaxVariable = theme.Foreground("syntax.variable", color.NRGBA{R: 0xe0, G: 0x6c, B: 0x75, A: 0xff})
)

// syntaxInk returns the colour token for a kind of token.
func syntaxInk(k syntax.Kind) theme.Token[color.NRGBA] {
	switch k {
	case syntax.Keyword:
		return SyntaxKeyword
	case syntax.Builtin:
		return SyntaxBuiltin
	case syntax.Type:
		return SyntaxType
	case syntax.Function:
		return SyntaxFunction
	case syntax.String:
		return SyntaxString
	case syntax.Number:
		return SyntaxNumber
	case syntax.Comment:
		return SyntaxComment
	case syntax.Operator:
		return SyntaxOperator
	case syntax.Variable:
		return SyntaxVariable
	}
	return Ink
}

// A CodeMark points at a place in the code with a message, such as a
// compiler's error. Line and Col count from 1, as compilers write them.
type CodeMark struct {
	Line, Col int
	Message   string
}

// CodeEditor edits source code: a monospaced face, tab stops, line
// numbers, and each token in its kind's colour, from Highlight.
//
// It edits as [TextArea] does, with undo, the clipboard and input
// methods, and adds what code needs. Tab and Shift+Tab indent and
// outdent, Enter keeps the line's indent and adds one after an opening
// bracket, a closing brace takes one back, and Home goes to the first
// character of the line before its start. Lines stay whole and the
// code scrolls both ways.
//
// The caret glides between places, a band follows it from line to
// line, and a mark from [CodeEditor.SetMarks] fades in on its line
// with its message after the code.
type CodeEditor struct {
	anim.Group
	editor

	// Highlight finds the tokens to colour. Nil shows the code in one
	// colour.
	Highlight syntax.Highlighter
	// OnChange runs on the UI goroutine after each edit, with the code; a
	// non-nil result is sent to the application as the editor's intent.
	OnChange func(code string, u *gunim.UI) gunim.Intent
	// OnCommit, when set, runs when Enter is pressed, as OnChange does,
	// and Shift+Enter starts a line instead.
	OnCommit func(code string, u *gunim.UI) gunim.Intent
	// MaxRows, when above zero, makes the editor as tall as its code up
	// to that many lines, growing and shrinking as lines come and go,
	// and scrolling past them, rather than filling the room it is given.
	MaxRows int
	// Label names the editor for screen readers.
	Label string
	// Numbers shows line numbers down the left. NewCodeEditor sets it.
	Numbers bool

	focus   *anim.Float
	caretAt *anim.Point
	band    *anim.Float
	// rows carries how many lines tall the editor is under MaxRows.
	rows    *anim.Float
	scrollX *anim.Float
	scrollY *anim.Float
	marks   []*codeMark

	held bool

	// The layout: the face, sizes and highlighter it is for, and the
	// lines, kept up as the code changes.
	laidFace    *text.Face
	laidSize    float32
	laidTab     float32
	laidColours uintptr
	lines       []codeLine
	shapes      shapeCache
	numbers     map[int]text.Run
	lineH       float32
	glyphH      float32
	tabW        float32
	gutterW     float32
	pad         float32
	widest      float32
	view        geom.Size
	followed    int
	// followedIn is the view's size when the caret was last followed:
	// a view that changes size keeps the caret in it.
	followedIn geom.Size
	laid       bool
}

// codeMark is a mark as shown, fading in as it arrives and out as it
// goes.
type codeMark struct {
	CodeMark
	shown   *anim.Float
	leaving bool
}

// codeLine is one line of the code, laid out.
type codeLine struct {
	// start and end are the line's runes in the code, its newline left
	// out.
	start, end int
	// toks are the tokens on the line, counted from its start and cut
	// to it, and cont says a token from a line before runs into it.
	// stale says the tokens changed since the line was laid out.
	toks  []syntax.Token
	cont  bool
	stale bool
	// text and spans are what the line was laid out from, to tell
	// whether it needs laying out again.
	text  string
	spans []codeSpan
	segs  []codeSeg
	width float32
}

// codeSpan is a run of a line in one kind, or one tab.
type codeSpan struct {
	start, end int
	kind       syntax.Kind
	tab        bool
}

// codeSeg is a span laid out: where it sits on the line, and its
// shaped text.
type codeSeg struct {
	codeSpan
	x, w float32
	run  text.Run
}

// NewCodeEditor returns an empty code editor that highlights Go.
func NewCodeEditor() *CodeEditor {
	c := &CodeEditor{
		Highlight: syntax.Go,
		focus:     anim.NewFloat(0),
		caretAt:   anim.NewPoint(geom.Point{}),
		band:      anim.NewFloat(0),
		rows:      anim.NewFloat(0),
		scrollX:   anim.NewFloat(0),
		scrollY:   anim.NewFloat(0),
		numbers:   map[int]text.Run{},
	}
	c.multiline, c.tabs, c.Numbers = true, true, true
	c.Add(c.focus, c.caretAt, c.band, c.scrollX, c.scrollY, c.rows)
	c.changed = func(u *gunim.UI) {
		if c.OnChange != nil {
			send(u, c, c.OnChange(c.Text(), u))
		}
	}
	return c
}

// Text returns the code.
func (c *CodeEditor) Text() string { return c.textString() }

// SetText replaces the code, puts the caret at its start, scrolls to
// the top at once and forgets the history, for a file opened afresh. It
// sends no intent; u may be nil, as before the editor is laid out.
func (c *CodeEditor) SetText(s string, u *gunim.UI) {
	c.setText([]rune(s))
	c.set(0, false)
	c.forget()
	c.scrollX.Jump(0)
	c.scrollY.Jump(0)
	c.edited = true
	if u != nil {
		u.Invalidate()
	}
}

// Replace puts s in place of the code as one step of undo, keeping the
// caret on the same line and column, as formatting the code does. It
// replaces read only code too, as a program's output, and keeps no
// history for it.
func (c *CodeEditor) Replace(s string, u *gunim.UI) {
	if s == string(c.text) {
		return
	}
	line, col := c.lineCol(c.caret)
	readOnly := c.readOnly
	c.readOnly = false
	c.last = otherEdit
	c.replace(0, len(c.text), []rune(s), u)
	c.readOnly = readOnly
	if readOnly {
		c.forget()
	}
	c.set(c.offset(line, col), false)
}

// SetReadOnly makes the code read only, or editable again.
func (c *CodeEditor) SetReadOnly(on bool) { c.readOnly = on }

// ReadOnly reports whether the code is read only.
func (c *CodeEditor) ReadOnly() bool { return c.readOnly }

// GoTo puts the caret at line and column, counted from 1, and scrolls
// it into view.
func (c *CodeEditor) GoTo(line, col int, u *gunim.UI) {
	c.set(c.offset(line-1, col-1), false)
	u.Invalidate()
}

// SetMarks shows marks on their lines. A mark already shown stays; one
// that is new fades in, and one left out fades away.
func (c *CodeEditor) SetMarks(marks []CodeMark, u *gunim.UI) {
	th := u.Theme()
	for _, m := range c.marks {
		if !slices.Contains(marks, m.CodeMark) {
			m.leaving = true
			m.shown.Animate(0, Settle.Get(th))
		}
	}
	for _, want := range marks {
		i := slices.IndexFunc(c.marks, func(m *codeMark) bool { return m.CodeMark == want })
		if i >= 0 {
			c.marks[i].leaving = false
			c.marks[i].shown.Animate(1, Quick.Get(th))
			continue
		}
		m := &codeMark{CodeMark: want, shown: anim.NewFloat(0)}
		m.shown.Animate(1, Quick.Get(th))
		c.marks = append(c.marks, m)
	}
	u.Invalidate()
}

// Marks returns the marks shown, those fading away left out.
func (c *CodeEditor) Marks() []CodeMark {
	var out []CodeMark
	for _, m := range c.marks {
		if !m.leaving {
			out = append(out, m.CodeMark)
		}
	}
	return out
}

// lineCol returns the line and column, from 0, of rune i of the code.
func (c *CodeEditor) lineCol(i int) (line, col int) {
	i = max(0, min(i, len(c.text)))
	start := 0
	for j, r := range c.text[:i] {
		if r == '\n' {
			line++
			start = j + 1
		}
	}
	return line, i - start
}

// offset returns the rune of the code at line and column, from 0, kept
// within the code and within the line.
func (c *CodeEditor) offset(line, col int) int {
	start := 0
	for l := 0; l < line; l++ {
		n := slices.Index(c.text[start:], '\n')
		if n < 0 {
			return len(c.text)
		}
		start += n + 1
	}
	end := start
	for end < len(c.text) && c.text[end] != '\n' {
		end++
	}
	return start + max(0, min(col, end-start))
}

// Focusable implements [gunim.Focusable].
func (c *CodeEditor) Focusable() bool { return true }

// TakesText implements [gunim.TextTaker].
func (c *CodeEditor) TakesText() bool { return !c.readOnly }

// TextCaret implements [gunim.CaretReporter].
func (c *CodeEditor) TextCaret() geom.Rect {
	at := c.caretAt.Value().Add(c.origin())
	return geom.Rc(at.X, at.Y+(c.lineH-c.glyphH)/2, 1.5, c.glyphH)
}

// caretRect returns where a caret before rune i would stand, in the
// editor's space.
func (c *CodeEditor) caretRect(i int) geom.Rect {
	at := geom.Pt(c.xOf(i), float32(c.lineOf(i))*c.lineH).Add(c.origin())
	return geom.Rc(at.X, at.Y+(c.lineH-c.glyphH)/2, 1.5, c.glyphH)
}

// hostIndex returns the rune a press at p in the editor's space is before.
func (c *CodeEditor) hostIndex(p geom.Point, _ *gunim.UI) int { return c.indexAt(p) }

// Step implements [gunim.Animator].
func (c *CodeEditor) Step(dt time.Duration) bool {
	moving := c.Group.Step(dt)
	kept := c.marks[:0]
	for _, m := range c.marks {
		if m.shown.Step(dt) {
			moving = true
		}
		if m.leaving && !m.shown.Active() && m.shown.Value() <= 0 {
			continue
		}
		kept = append(kept, m)
	}
	clear(c.marks[len(kept):])
	c.marks = kept
	return moving
}

// origin returns where the code's top-left sits in the editor, as
// scrolled now.
func (c *CodeEditor) origin() geom.Point {
	return geom.Pt(c.gutterW+c.pad-c.scrollX.Value(), c.pad-c.scrollY.Value())
}

// Handle implements [gunim.Handler].
func (c *CodeEditor) Handle(e input.Event, u *gunim.UI) bool {
	if c.blink.windowFocus(e, u) {
		return false
	}
	th := u.Theme()
	switch e := e.(type) {
	case input.FocusGained:
		c.focus.Animate(1, Quick.Get(th))
		c.blink.restart(u)
	case input.FocusLost:
		c.focus.Animate(0, Settle.Get(th))
		c.blink.halt()
		c.anchor = c.caret
		c.preedit = nil
		c.closeMenu()
		c.wording = false
		c.closeHandles()
	case input.PointerDown:
		if e.Button == input.ButtonSecondary {
			c.contextPress(c, c.indexAt(e.Pos), e.Pos, e.Touch, u)
			break
		}
		c.closeHandles()
		c.press(c.indexAt(e.Pos), e.Clicks, e.Mods.Has(input.ModShift))
		c.held = true
	case input.PointerMove:
		if c.wording {
			c.dragWords(c.indexAt(e.Pos))
			break
		}
		if !c.held {
			return false
		}
		c.set(c.indexAt(e.Pos), true)
	case input.PointerUp:
		c.held = false
		c.endWords(c, e.Pos, u)
	case input.Scroll:
		dx, dy := e.Delta.X, e.Delta.Y
		if e.Mods.Has(input.ModShift) && dx == 0 {
			dx, dy = dy, 0
		}
		x := max(0, min(c.scrollX.Target()-dx, c.maxScrollX()))
		y := max(0, min(c.scrollY.Target()-dy, c.maxScrollY()))
		if x == c.scrollX.Target() && y == c.scrollY.Target() {
			return false // at an end: for whatever scrolls outside
		}
		c.scrollX.Animate(x, CodeScroll.Get(th))
		c.scrollY.Animate(y, CodeScroll.Get(th))
	case input.TextInput:
		if c.readOnly {
			return true
		}
		c.typed(e.Text, u)
	case input.Composing:
		if c.readOnly {
			return true
		}
		c.compose(e)
	case input.TextEdit:
		if c.readOnly {
			return true
		}
		c.edit(e, u)
	case input.KeyPress:
		if c.submits(e) {
			send(u, c, c.OnCommit(c.Text(), u))
			return true
		}
		if !c.codeKey(e, u) && !c.key(e, u, codeNav{c}) {
			return false
		}
	default:
		return false
	}
	if _, lost := e.(input.FocusLost); !lost {
		c.blink.restart(u)
	}
	u.Invalidate()
	return true
}

// submits reports whether k is an Enter that runs OnCommit.
func (c *CodeEditor) submits(k input.KeyPress) bool {
	enter := k.Key == input.KeyEnter || k.Key == input.KeyKPEnter
	return enter && c.OnCommit != nil && !k.Mods.Has(input.ModShift) && len(c.preedit) == 0
}

// typed puts typed text in, taking an indent back for a closing brace
// typed at the start of a line.
func (c *CodeEditor) typed(s string, u *gunim.UI) {
	start, end := c.Selection()
	if s == "}" && start == end && len(c.preedit) == 0 {
		ls := c.lineStartOf(start)
		if before := c.text[ls:start]; len(before) > 0 && onlyIndent(before) && before[len(before)-1] == '\t' {
			c.replace(start-1, start, []rune("}"), u)
			return
		}
	}
	c.commit(s, u)
}

// codeKey takes the keys code edits differently, and reports whether
// it took k.
func (c *CodeEditor) codeKey(k input.KeyPress, u *gunim.UI) bool {
	ctrl := k.Mods.Has(input.ModControl) || k.Mods.Has(input.ModSuper)
	shift := k.Mods.Has(input.ModShift)
	switch {
	case k.Key == input.KeyTab && !ctrl && !k.Mods.Has(input.ModAlt):
		if c.readOnly {
			return false // focus moves on
		}
		start, end := c.Selection()
		switch {
		case shift:
			c.indent(false, u)
		case strings.ContainsRune(string(c.text[start:end]), '\n'):
			c.indent(true, u)
		default:
			c.insert("\t", u)
		}
		return true
	case (k.Key == input.KeyEnter || k.Key == input.KeyKPEnter) && !c.readOnly && len(c.preedit) == 0:
		start, _ := c.Selection()
		ls := c.lineStartOf(start)
		line := c.text[ls:start]
		n := 0
		for n < len(line) && (line[n] == '\t' || line[n] == ' ') {
			n++
		}
		indent := string(line[:n])
		if t := strings.TrimRightFunc(string(line), unicode.IsSpace); strings.HasSuffix(t, "{") ||
			strings.HasSuffix(t, "(") || strings.HasSuffix(t, "[") || strings.HasSuffix(t, ":") {
			indent += "\t"
		}
		c.insert("\n"+indent, u)
		return true
	case k.Key == input.KeyHome && !ctrl:
		ls := c.lineStartOf(c.caret)
		first := ls
		for first < len(c.text) && (c.text[first] == '\t' || c.text[first] == ' ') {
			first++
		}
		if c.caret == first {
			first = ls
		}
		c.set(first, shift)
		return true
	}
	return false
}

// indent adds a tab before each line the selection touches, or takes
// one indent off each, as one step of undo, and selects the lines.
func (c *CodeEditor) indent(in bool, u *gunim.UI) {
	start, end := c.Selection()
	from := c.lineStartOf(start)
	if end > start && end > 0 && c.text[end-1] == '\n' {
		end-- // a selection that ends at a line's start leaves that line be
	}
	to := end
	for to < len(c.text) && c.text[to] != '\n' {
		to++
	}
	lines := strings.Split(string(c.text[from:to]), "\n")
	for i, l := range lines {
		switch {
		case in && l != "":
			lines[i] = "\t" + l
		case !in && strings.HasPrefix(l, "\t"):
			lines[i] = l[1:]
		case !in:
			n := 0
			for n < len(l) && n < 4 && l[n] == ' ' {
				n++
			}
			lines[i] = l[n:]
		}
	}
	block := []rune(strings.Join(lines, "\n"))
	if string(block) == string(c.text[from:to]) {
		return
	}
	c.last = otherEdit
	c.replace(from, to, block, u)
	c.anchor, c.caret = from, from+len(block)
}

// lineStartOf returns where the line holding rune i starts.
func (c *CodeEditor) lineStartOf(i int) int {
	for i > 0 && c.text[i-1] != '\n' {
		i--
	}
	return i
}

// onlyIndent reports whether rs is all spaces and tabs.
func onlyIndent(rs []rune) bool {
	for _, r := range rs {
		if r != ' ' && r != '\t' {
			return false
		}
	}
	return true
}

// indexAt returns the rune a pointer at p, in the editor's space, puts
// the caret before.
func (c *CodeEditor) indexAt(p geom.Point) int {
	if len(c.lines) == 0 {
		return 0
	}
	at := p.Sub(c.origin())
	line := max(0, min(int(math.Floor(float64(at.Y/c.lineH))), len(c.lines)-1))
	return c.hit(line, at.X)
}

// hit returns the rune nearest x on line.
func (c *CodeEditor) hit(line int, x float32) int {
	l := c.laidLine(line)
	for _, s := range l.segs {
		if x >= s.x+s.w {
			continue
		}
		if s.tab {
			if x < s.x+s.w/2 {
				return l.start + s.start
			}
			return l.start + s.end
		}
		i, _ := s.run.Hit(x - s.x)
		return l.start + s.start + i
	}
	return l.end
}

// lineOf returns the line holding rune i of the code as laid out.
func (c *CodeEditor) lineOf(i int) int {
	n, _ := slices.BinarySearchFunc(c.lines, i, func(l codeLine, i int) int {
		switch {
		case l.end < i:
			return -1
		case l.start > i:
			return 1
		}
		return 0
	})
	return max(0, min(n, len(c.lines)-1))
}

// xOf returns where a caret before rune i sits across its line.
func (c *CodeEditor) xOf(i int) float32 {
	if len(c.lines) == 0 {
		return 0
	}
	l := c.laidLine(c.lineOf(i))
	local := i - l.start
	for _, s := range l.segs {
		if local >= s.end {
			continue
		}
		if s.tab || local <= s.start {
			return s.x
		}
		return s.x + s.run.CaretX(local-s.start)
	}
	return l.width
}

// codeNav navigates a code editor's lines.
type codeNav struct{ c *CodeEditor }

func (n codeNav) caretX(i int) float32 { return n.c.xOf(i) }

func (n codeNav) beside(i int, x float32, right bool) (int, float32) {
	switch {
	case right && i < len(n.c.text):
		i++
	case !right && i > 0:
		i--
	default:
		return i, x
	}
	return i, n.c.xOf(i)
}

func (n codeNav) lineStart(i int) int {
	if len(n.c.lines) == 0 {
		return 0
	}
	return n.c.lines[n.c.lineOf(i)].start
}

func (n codeNav) lineEnd(i int) int {
	if len(n.c.lines) == 0 {
		return len(n.c.text)
	}
	return n.c.lines[n.c.lineOf(i)].end
}

func (n codeNav) vertical(i int, x float32, lines int) (int, bool) {
	if len(n.c.lines) == 0 {
		return i, true
	}
	to := n.c.lineOf(i) + lines
	switch {
	case to < 0:
		return 0, true
	case to >= len(n.c.lines):
		return len(n.c.text), true
	}
	return n.c.hit(to, x), true
}

func (n codeNav) page() int {
	if n.c.lineH <= 0 {
		return 1
	}
	return max(1, int(n.c.view.H/n.c.lineH)-1)
}

// maxScrollX and maxScrollY are how far the code scrolls.
func (c *CodeEditor) maxScrollX() float32 {
	return max(0, c.widest+c.tabW-c.view.W)
}

func (c *CodeEditor) maxScrollY() float32 {
	return max(0, float32(len(c.lines))*c.lineH-c.view.H)
}

// relayout lays out the lines the code's changes touched, and colours
// them and the lines a change of colour carries on to.
func (c *CodeEditor) relayout(th *theme.Live) {
	face := faceIn(MonoFont, th)
	size := CodeSize.Get(th)
	tab := CodeTab.Get(th)
	shown, _ := c.shown()
	head, tail, was, changed := c.shownChange()
	colours := highlighterID(c.Highlight)
	if !c.laid || c.laidFace != face || c.laidSize != size || c.laidTab != tab || c.laidColours != colours {
		if c.laidFace != face || c.laidSize != size || c.laidTab != tab {
			c.shapes.clear()
			clear(c.numbers)
			space := face.Shape(" ", size)
			c.glyphH = space.Height()
			c.tabW = space.Advance * max(tab, 1)
		}
		c.lines = c.lines[:0]
		head, tail, was, changed = 0, 0, 0, true
	}
	c.laidFace, c.laidSize, c.laidTab, c.laidColours = face, size, tab, colours
	c.lineH = float32(math.Ceil(float64(c.glyphH * CodeLineHeight.Get(th))))
	if !changed {
		return
	}

	from, to := c.splitLines(shown, head, tail, was)
	first, end := c.colour(shown, from, to)
	// The new lines are laid out now. A line whose text stayed and whose
	// colours changed keeps its width, which colours leave as it is, and
	// is laid out when it is next shown, so a comment opened above many
	// lines costs only the lines in view.
	for i := first; i < end; i++ {
		if i >= from && i < to {
			c.layLine(&c.lines[i], face, size)
		} else {
			c.lines[i].stale = true
		}
	}
	c.widest = 0
	for _, l := range c.lines {
		c.widest = max(c.widest, l.width)
	}
	c.gutterW = 0
	if c.Numbers {
		digits := len(strconv.Itoa(max(len(c.lines), 99)))
		c.gutterW = float32(digits)*c.tabW/max(tab, 1) + 2*CodePadding.Get(th)
	}
}

// layLine lays out l from its text and tokens, and reuses what it had
// where neither changed.
func (c *CodeEditor) layLine(l *codeLine, face *text.Face, size float32) {
	rs := []rune(l.text)
	var spans []codeSpan
	// Spans: the tokens on the line, the plain text between them, and
	// each tab on its own.
	add := func(s, e int, k syntax.Kind) {
		for s < e {
			t := s
			for t < e && rs[t] != '\t' {
				t++
			}
			if t > s {
				spans = append(spans, codeSpan{start: s, end: t, kind: k})
			}
			if t < e {
				spans = append(spans, codeSpan{start: t, end: t + 1, tab: true})
				t++
			}
			s = t
		}
	}
	at := 0
	for _, tk := range l.toks {
		if tk.Start > at {
			add(at, tk.Start, syntax.Plain)
		}
		add(tk.Start, tk.End, tk.Kind)
		at = tk.End
	}
	add(at, len(rs), syntax.Plain)

	l.stale = false
	if l.segs != nil && slices.Equal(l.spans, spans) {
		return
	}
	l.spans, l.segs = spans, nil
	x := float32(0)
	for _, sp := range spans {
		seg := codeSeg{codeSpan: sp, x: x}
		if sp.tab {
			seg.w = c.tabW - float32(math.Mod(float64(x), float64(c.tabW)))
		} else {
			seg.run = c.shapes.shape(face, l.text[byteAt(l.text, sp.start):byteAt(l.text, sp.end)], size)
			seg.w = seg.run.Advance
		}
		l.segs = append(l.segs, seg)
		x += seg.w
	}
	l.width = x
}

// laidLine returns line i, laid out again first if its colours changed.
func (c *CodeEditor) laidLine(i int) *codeLine {
	l := &c.lines[i]
	if l.stale {
		c.layLine(l, c.laidFace, c.laidSize)
		c.widest = max(c.widest, l.width)
	}
	return l
}

// byteAt returns the byte offset of rune i of s.
func byteAt(s string, i int) int {
	n := 0
	for b := range s {
		if n == i {
			return b
		}
		n++
	}
	return len(s)
}

// Layout implements [gunim.Node]. The editor fills the room it is
// given; asked for no particular height, it is as tall as its code.
func (c *CodeEditor) Layout(cs gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	c.relayout(th)
	c.pad = CodePadding.Get(th)
	own := cs.Max
	if own.W <= 0 {
		own.W = AreaWidth.Get(th)
	}
	switch {
	case c.MaxRows > 0:
		want := float32(min(max(len(c.lines), 1), c.MaxRows))
		if !c.laid {
			c.rows.Jump(want)
		} else if want != c.rows.Target() {
			c.rows.Animate(want, Quick.Get(th))
		}
		own.H = c.rows.Value()*c.lineH + 2*c.pad
	case own.H <= 0:
		own.H = float32(max(len(c.lines), 1))*c.lineH + 2*c.pad
	}
	own = cs.Constrain(own)
	c.view = geom.Sz(max(0, own.W-c.gutterW-2*c.pad), max(0, own.H-2*c.pad))

	caret, _ := c.drawnCaret()
	line := c.lineOf(caret)
	at := geom.Pt(c.xOf(caret), float32(line)*c.lineH)
	motion := Caret.Get(th)
	if c.edited || !c.laid {
		c.caretAt.Jump(at)
	} else {
		c.caretAt.Animate(at, motion)
	}
	// The band moves with the caret, on the same motion.
	if !c.laid {
		c.band.Jump(at.Y)
	} else {
		c.band.Animate(at.Y, motion)
	}

	// Follow the caret when it has moved, the code has changed, or the
	// view has changed size, so the wheel can scroll away from it.
	sx, sy := c.scrollX.Target(), c.scrollY.Target()
	if c.edited || caret != c.followed || c.view != c.followedIn {
		margin := c.tabW
		switch {
		case at.X-margin < sx:
			sx = at.X - margin
		case at.X+margin > sx+c.view.W:
			sx = at.X + margin - c.view.W
		}
		switch {
		case at.Y < sy:
			sy = at.Y
		case at.Y+c.lineH > sy+c.view.H:
			sy = at.Y + c.lineH - c.view.H
		}
	}
	c.followed, c.followedIn = caret, c.view
	sx = max(0, min(sx, c.maxScrollX()))
	sy = max(0, min(sy, c.maxScrollY()))
	if !c.laid {
		c.scrollX.Jump(sx)
		c.scrollY.Jump(sy)
	} else {
		c.aim(c.scrollX, sx, CodeScroll.Get(th))
		c.aim(c.scrollY, sy, CodeScroll.Get(th))
	}
	c.edited = false
	c.laid = true
	c.placeHandles(c)
	return own
}

// Paint implements [gunim.Node].
func (c *CodeEditor) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	focus := min(max(c.focus.Value(), 0), 1)
	radius := CodeRadius.Get(th)
	all := geom.Rect{Max: box.Point()}
	p.RRect(all, radius, paint.Solid(CodeEditorFill.Get(th)))
	defer p.Layer(paint.LayerOpts{Bounds: all, Opacity: 1, Clip: true, Radius: radius})()

	o := c.origin()
	first := max(0, int((c.scrollY.Value()-c.pad)/max(c.lineH, 1)))
	last := min(len(c.lines), int((c.scrollY.Value()+box.H)/max(c.lineH, 1))+1)

	// The band on the caret's line, stronger while the editor has the
	// keyboard. Read only text, such as a program's output, goes
	// without.
	if !c.readOnly {
		band := CodeLineFill.Get(th)
		band.A = uint8(float32(band.A) * (0.5 + 0.5*focus))
		p.RRect(geom.Rc(c.gutterW, o.Y+c.band.Value(), box.W-c.gutterW, c.lineH), 0, paint.Solid(band))
	}

	// Marks: a tint on their lines.
	problem := CodeProblem.Get(th)
	for _, m := range c.marks {
		if m.Line < 1 || m.Line > len(c.lines) {
			continue
		}
		y := o.Y + float32(m.Line-1)*c.lineH
		p.RRect(geom.Rc(c.gutterW, y, box.W-c.gutterW, c.lineH), 0, paint.Solid(fadedBy(problem, 0.13*m.shown.Value())))
	}

	// The selection.
	caret, anchor := c.drawnCaret()
	if start, end := min(caret, anchor), max(caret, anchor); start != end {
		sel := Selection.Get(th)
		sel.A = uint8(float32(sel.A) * (0.4 + 0.6*focus))
		for li := c.lineOf(start); li < len(c.lines) && c.lines[li].start <= end; li++ {
			l := c.lines[li]
			if li < first || li >= last {
				continue
			}
			x0 := c.xOf(max(start, l.start))
			x1 := c.xOf(min(end, l.end))
			if end > l.end {
				x1 += c.tabW / 2
			}
			p.RRect(geom.Rc(o.X+x0, o.Y+float32(li)*c.lineH, x1-x0, c.lineH), 2, paint.Solid(sel))
		}
	}

	// The code, the lines in view and the pieces of them in view.
	lift := (c.lineH - c.glyphH) / 2
	for li := first; li < last; li++ {
		l := c.laidLine(li)
		y := o.Y + float32(li)*c.lineH + lift
		for _, s := range l.segs {
			if s.tab || o.X+s.x+s.w < c.gutterW || o.X+s.x > box.W {
				continue
			}
			s.run.Paint(p, geom.Pt(o.X+s.x, y), syntaxInk(s.kind).Get(th))
		}
	}

	// Each mark's message, after its line's code, sliding in as it
	// fades in.
	for _, m := range c.marks {
		if m.Line < 1 || m.Line > len(c.lines) || m.Line-1 < first || m.Line-1 >= last {
			continue
		}
		shown := min(max(m.shown.Value(), 0), 1)
		l := c.lines[m.Line-1]
		run := c.shape(m.Message, th)
		x := o.X + l.width + 2*c.tabW + 16*(1-shown)
		run.Paint(p, geom.Pt(x, o.Y+float32(m.Line-1)*c.lineH+lift), fadedBy(problem, shown))
	}

	// The input method's composition, underlined.
	if len(c.preedit) > 0 {
		_, at := c.shown()
		li := c.lineOf(at)
		x0, x1 := c.xOf(at), c.xOf(at+len(c.preedit))
		p.RRect(geom.Rc(o.X+x0, o.Y+float32(li)*c.lineH+lift+c.glyphH, x1-x0, 1), 0, paint.Solid(Ink.Get(th)))
	}

	// The caret, blinking while the editor has the keyboard.
	if focus > 0.01 && !c.readOnly {
		col := Accent.Get(th)
		col.A = uint8(float32(col.A) * focus * c.blink.value())
		at := c.caretAt.Value().Add(o)
		p.RRect(geom.Rc(at.X-0.75, at.Y+lift, 1.5, c.glyphH), 0.75, paint.Solid(col))
	}

	// The gutter over the code's left edge, and its numbers: the
	// caret's line in the ink, a marked line in the problem colour.
	if c.Numbers {
		c.paintGutter(p, th, box, o, first, last, focus)
	}
	if focus > 0 {
		edge := Accent.Get(th)
		edge.A = uint8(float32(edge.A) * focus * 0.8)
		p.RRectStroke(all.Inset(geom.Uniform(0.5)), radius, paint.Fill{}, paint.Stroke{Width: 1, Color: edge})
	}
}

// paintGutter draws the line numbers of lines first to last.
func (c *CodeEditor) paintGutter(p *paint.Painter, th *theme.Live, box geom.Size, o geom.Point, first, last int, focus float32) {
	lift := (c.lineH - c.glyphH) / 2
	problem := CodeProblem.Get(th)
	caret, _ := c.drawnCaret()
	p.RRect(geom.Rc(0, 0, c.gutterW, box.H), 0, paint.Solid(CodeGutterFill.Get(th)))
	caretLine := c.lineOf(caret)
	gutterInk := CodeGutterInk.Get(th)
	ink := Ink.Get(th)
	for li := first; li < last; li++ {
		run, ok := c.numbers[li+1]
		if !ok {
			run = c.laidFace.Shape(strconv.Itoa(li+1), c.laidSize)
			c.numbers[li+1] = run
		}
		col := gutterInk
		if li == caretLine {
			col = anim.Mix(anim.ColorCodec, gutterInk, ink, 0.4+0.6*focus)
		}
		for _, m := range c.marks {
			if m.Line == li+1 {
				col = anim.Mix(anim.ColorCodec, col, problem, min(max(m.shown.Value(), 0), 1))
			}
		}
		x := c.gutterW - c.pad - run.Advance
		run.Paint(p, geom.Pt(x, o.Y+float32(li)*c.lineH+lift), col)
	}
}

// shape returns s shaped in the code's face, from the cache.
func (c *CodeEditor) shape(s string, th *theme.Live) text.Run {
	return c.shapes.shape(faceIn(MonoFont, th), s, CodeSize.Get(th))
}

// fadedBy is col with its alpha scaled by a, from 0 to 1.
func fadedBy(col color.NRGBA, a float32) color.NRGBA {
	col.A = uint8(float32(col.A) * min(max(a, 0), 1))
	return col
}
