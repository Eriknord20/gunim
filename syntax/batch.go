package syntax

import (
	"strings"
	"unicode"
)

// Batch highlights what is typed at the Windows Command Prompt, cmd,
// and the batch files it runs. The first word of a command is what
// runs, a command cmd knows itself or a program, and words such as if,
// for and goto are keywords, all in any case. Variables, as %PATH%,
// %~dp0, %%i and !delayed!, labels, strings, REM and :: comments, and
// the pipes and redirections between commands have colours of their
// own.
func Batch(src string) []Token {
	s := &source{rs: []rune(src)}
	(&batch{source: s}).run(0, len(s.rs))
	return s.out
}

// batch is a place in cmd code: where it ends, and what the next word
// means there.
type batch struct {
	*source
	end int
	// lineStart says nothing but blanks came before on the line, and cmd
	// that the next word is a command.
	lineStart, cmd bool
	// cond is how far along an if's condition is, and forLoop says a for
	// is being read up to its do; forLine says one was on this line, so
	// %i names its variable.
	cond             ifPart
	forLoop, forLine bool
	// label says the next word is a label, after goto. setting says the
	// next word is a variable set says, and assigning that an = sets it.
	label, setting, assigning bool
	// echoing says the rest of the command is text, after echo.
	echoing bool
	// closed says the last thing read was a ), for an else after it.
	closed bool
}

// ifPart is how far along an if's condition is.
type ifPart uint8

const (
	// noIf is outside a condition.
	noIf ifPart = iota
	// ifStart is just after if, /i or not.
	ifStart
	// ifOperand is before the word exist, defined or errorlevel test.
	ifOperand
	// ifCompare is after a comparison's left side, before its operator.
	ifCompare
	// ifRight is before a comparison's right side.
	ifRight
)

// batchBuiltins are the commands cmd runs itself.
var batchBuiltins = words(`assoc break cd chdir cls color copy date del dir echo endlocal erase exit ftype md mkdir
	mklink move path pause popd prompt pushd rd ren rename rmdir set setlocal shift start time title type ver verify
	vol`)

// batchCompare are the comparisons an if can make in words.
var batchCompare = words(`equ neq lss leq gtr geq`)

// run reads from i up to end.
func (b *batch) run(i, end int) {
	b.end = end
	b.newLine()
	for i < end {
		c := b.rs[i]
		switch {
		case c == '\n':
			b.newLine()
			i++
		case blank(c) || c == ',' || c == ';' && !b.echoing:
			i++
		case b.lineStart && c == '@':
			b.add(i, i+1, Operator)
			i++
		case b.lineStart && c == ':' && b.at(i+1, end) == ':':
			e := b.lineEnd(i, end)
			b.add(i, e, Comment)
			i = e
		case b.lineStart && c == ':':
			e := b.wordEnd(i + 1)
			b.add(i, e, Function)
			i = e
			b.lineStart = false
		case c == '^':
			i = min(i+2, end) // an escape, or the line carrying on
			b.lineStart = false
		case c == '"':
			i = b.quoted(i)
			b.after(false)
		case c == '%' || c == '!':
			if e := b.variable(i); e > i {
				i = e
				b.after(false)
				break
			}
			i = b.word(i)
		case strings.ContainsRune("&|<>()", c):
			i = b.operator(i)
		case c == '=':
			i = b.equals(i)
		default:
			i = b.word(i)
		}
	}
}

// newLine notes that a line starts: a command comes first on it.
func (b *batch) newLine() {
	b.lineStart, b.cmd = true, true
	b.cond, b.forLoop, b.forLine = noIf, false, false
	b.label, b.setting, b.assigning, b.echoing, b.closed = false, false, false, false, false
}

// newCommand notes that a command comes next on the line.
func (b *batch) newCommand() {
	b.cmd, b.lineStart = true, false
	b.label, b.setting, b.assigning, b.echoing = false, false, false, false
}

// after notes that a word or what stands for one was read, which moves
// an if's condition along.
func (b *batch) after(keyword bool) {
	b.lineStart, b.closed = false, false
	if keyword {
		return
	}
	b.cmd = false
	switch b.cond {
	case ifStart:
		b.cond = ifCompare
	case ifOperand, ifRight:
		b.cond = noIf
		b.cmd = true // the command the condition guards
	case noIf, ifCompare:
	}
}

// wordEnd returns where the word at i ends.
func (b *batch) wordEnd(i int) int {
	for i < b.end {
		c := b.rs[i]
		if blank(c) || c == '\n' || strings.ContainsRune(`&|<>()"^%!,;=`, c) {
			break
		}
		i++
	}
	return i
}

// word colours the word at i for what it is where it stands, and
// returns where it ends.
func (b *batch) word(i int) int {
	e := b.wordEnd(i)
	if e == i {
		e = i + 1 // a % or ! that names nothing
	}
	w := strings.ToLower(b.text(i, e))
	// echo. and cd\ are echo and cd.
	name := w
	if k := strings.IndexAny(w, `.:/\[]+`); k > 0 && batchBuiltins[w[:k]] {
		name = w[:k]
	}
	switch {
	case b.cmd && name == "rem":
		le := b.lineEnd(i, b.end)
		b.add(i, le, Comment)
		return le
	case b.cmd && name == "if":
		b.add(i, e, Keyword)
		b.cond = ifStart
		b.after(true)
		b.cmd = false
	case b.cmd && name == "for":
		b.add(i, e, Keyword)
		b.forLoop, b.forLine = true, true
		b.after(true)
		b.cmd = false
	case b.cmd && name == "goto":
		b.add(i, e, Keyword)
		b.after(true)
		b.cmd, b.label = false, true
	case b.cmd && name == "call":
		b.add(i, e, Keyword)
		b.after(true)
	case (b.cmd || b.closed) && name == "else":
		b.add(i, e, Keyword)
		b.newCommand()
	case b.cmd:
		k := Function
		if batchBuiltins[name] {
			// Only the name, of echo.Text: the rest is the text.
			k, e = Builtin, i+len(name)
		}
		b.add(i, e, k)
		b.after(false)
		b.setting = name == "set"
		b.echoing = name == "echo"
	case b.label:
		b.add(i, e, Function)
		b.label = false
		b.after(false)
	case b.forLoop && (w == "in" || w == "do"):
		b.add(i, e, Keyword)
		b.after(true)
		if w == "do" {
			b.forLoop = false
			b.newCommand()
		}
	case b.cond == ifStart && (w == "/i" || w == "not"):
		if w == "not" {
			b.add(i, e, Keyword)
		}
	case b.cond == ifStart && (w == "exist" || w == "defined" || w == "errorlevel" || w == "cmdextversion"):
		b.add(i, e, Keyword)
		b.cond = ifOperand
	case b.cond == ifCompare && batchCompare[w]:
		b.add(i, e, Operator)
		b.cond = ifRight
	case b.setting && strings.HasPrefix(w, "/"):
		// set /a or /p, before the name.
	case b.setting:
		b.add(i, e, Variable)
		b.setting, b.assigning = false, true
		b.after(false)
	case !b.echoing && number(w):
		b.add(i, e, Number)
		b.after(false)
	default:
		b.after(false)
	}
	return e
}

// equals colours an = that sets a variable or compares, and returns
// where it ends.
func (b *batch) equals(i int) int {
	switch {
	case b.cond == ifCompare && b.at(i+1, b.end) == '=':
		b.add(i, i+2, Operator)
		b.cond = ifRight
		return i + 2
	case b.assigning:
		b.add(i, i+1, Operator)
		b.assigning, b.echoing = false, true // the value is text
	}
	return i + 1
}

// operator reads the operator at i and returns where it ends.
func (b *batch) operator(i int) int {
	c, n := b.rs[i], b.at(i+1, b.end)
	switch c {
	case '(':
		if !b.forLoop {
			b.newCommand()
		}
		return i + 1
	case ')':
		b.cmd, b.closed = false, true
		return i + 1
	case '&', '|':
		w := 1
		if n == c {
			w = 2
		}
		b.add(i, i+w, Operator)
		b.newCommand()
		return i + w
	}
	// < > >> >&
	w := 1
	if n == '>' || n == '&' {
		w = 2
	}
	b.add(i, i+w, Operator)
	return i + w
}

// quoted colours the string at i, with the variables in it, and
// returns where it ends: at its closing quote, or the end of its line.
// set "name=value" names its variable in the string.
func (b *batch) quoted(i int) int {
	piece := i
	j := i + 1
	if b.setting {
		if eq := strings.IndexRune(b.text(j, b.lineEnd(j, b.end)), '='); eq > 0 && !strings.ContainsRune(b.text(j, j+eq), '"') {
			b.add(i, j, String)
			b.add(j, j+eq, Variable)
			b.add(j+eq, j+eq+1, Operator)
			j += eq + 1
			piece = j
			b.setting = false
		}
	}
	for j < b.end && b.rs[j] != '\n' {
		c := b.rs[j]
		if c == '"' {
			b.add(piece, j+1, String)
			return j + 1
		}
		if c == '%' || c == '!' {
			if e := b.varEnd(j); e > j {
				b.add(piece, j, String)
				b.add(j, e, Variable)
				j, piece = e, e
				continue
			}
		}
		j++
	}
	b.add(piece, j, String)
	return j
}

// variable colours the variable at i, a % or a !, and returns where it
// ends, or i when it names none.
func (b *batch) variable(i int) int {
	e := b.varEnd(i)
	b.add(i, e, Variable)
	return e
}

// varEnd returns where the variable at i, a % or a !, ends, or i when
// it names none.
func (b *batch) varEnd(i int) int {
	c, n := b.rs[i], b.at(i+1, b.end)
	if c == '!' {
		// !name! where delayed expansion is on.
		for j := i + 1; j < b.end; j++ {
			switch r := b.rs[j]; {
			case r == '!' && j > i+1:
				return j + 1
			case r == '!' || blank(r) || r == '\n' || r == '"':
				return i
			}
		}
		return i
	}
	switch {
	case n == '%':
		// %%i, or %%~nxi, a for loop's variable in a batch file.
		j := b.modifiers(i + 2)
		if unicode.IsLetter(b.at(j, b.end)) {
			return j + 1
		}
		return i
	case n == '~':
		// %~dp0, %~1: an argument, as a path's parts.
		j := b.modifiers(i + 1)
		if isDigit(b.at(j, b.end)) || b.forLine && unicode.IsLetter(b.at(j, b.end)) {
			return j + 1
		}
		return i
	case isDigit(n) || n == '*':
		return i + 2
	}
	// %name%, or %name:old=new% and %name:~0,4%.
	for j := i + 1; j < b.end; j++ {
		r := b.rs[j]
		if r == '%' && j > i+1 {
			return j + 1
		}
		if r == '%' || r == '\n' || r == '"' {
			break
		}
		if r == ':' {
			// What follows the colon may hold spaces.
			for k := j + 1; k < b.end && b.rs[k] != '\n'; k++ {
				if b.rs[k] == '%' {
					return k + 1
				}
			}
			break
		}
		if blank(r) {
			break
		}
	}
	if b.forLine && unicode.IsLetter(n) {
		// %i, a for loop's variable typed at the prompt.
		return i + 2
	}
	return i
}

// modifiers returns where the ~ and the letters that pick a path's
// parts end, from i, before a variable's own letter.
func (b *batch) modifiers(i int) int {
	if b.at(i, b.end) != '~' {
		return i
	}
	j := i + 1
	for j+1 < b.end && (strings.ContainsRune("fdpnxsatz", unicode.ToLower(b.rs[j])) || b.rs[j] == '$') &&
		(unicode.IsLetter(b.rs[j+1]) || isDigit(b.rs[j+1]) || b.rs[j+1] == '$' || b.rs[j+1] == ':') {
		if b.rs[j] == '$' {
			// %~$PATH:1 looks along a variable's list.
			for j++; j < b.end && b.rs[j] != ':' && b.rs[j] != '\n'; j++ {
			}
		}
		j++
	}
	return j
}
