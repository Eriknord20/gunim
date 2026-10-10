package syntax

import (
	"strings"
	"unicode"
)

// PowerShell highlights PowerShell, Windows PowerShell's language and
// PowerShell 7's. The first word of a command is what runs, a cmdlet or
// a program, and takes a function's colour. Keywords and operators such
// as -eq are known by any case, as PowerShell reads them. Variables,
// type names in brackets, strings with what expands inside them,
// here-strings, numbers and comments have colours of their own, and
// the code inside $( ) is read again as code.
func PowerShell(src string) []Token {
	s := &source{rs: []rune(src)}
	(&powerShell{source: s, typeEnd: -1}).run(0, len(s.rs))
	return s.out
}

// powerShell is a place in PowerShell code: where it ends, and what
// the next word means there.
type powerShell struct {
	*source
	end int
	// cmd says the next word is a command, and args that none is, as
	// in an attribute's arguments.
	cmd, args bool
	// typeEnd is where the ] of the last type's name stands, as one can
	// follow another: [Parameter()][string].
	typeEnd int
	// name says the next word names something, in nameKind: a
	// function or a class being declared.
	name     bool
	nameKind Kind
}

// psKeywords are PowerShell's keywords.
var psKeywords = words(`begin break catch class clean configuration continue data do dynamicparam else elseif end
	enum exit filter finally for foreach function hidden if in inlinescript param parallel process return sequence
	static switch throw trap try until using while workflow`)

// psOperators are the operators PowerShell spells as a dash and a word,
// such as -eq, without the dash.
var psOperators = func() map[string]bool {
	set := words(`and or not xor band bor bxor bnot shl shr is isnot as f join`)
	for _, op := range strings.Fields(`eq ne gt ge lt le like notlike match notmatch contains notcontains in notin
		replace split`) {
		set[op], set["c"+op], set["i"+op] = true, true, true
	}
	return set
}()

// run reads from i up to end.
func (p *powerShell) run(i, end int) {
	p.end = end
	p.cmd = !p.args
	for i < end {
		c, n := p.rs[i], p.at(i+1, end)
		switch {
		case c == '\n':
			p.cmd = !p.args
			i++
		case blank(c):
			i++
		case c == '#':
			e := p.lineEnd(i, end)
			p.add(i, e, Comment)
			i = e
		case c == '<' && n == '#':
			e := i + 2
			for e < end && (p.rs[e] != '#' || p.at(e+1, end) != '>') {
				e++
			}
			e = min(e+2, end)
			p.add(i, e, Comment)
			i = e
		case c == '`':
			i = min(i+2, end) // an escape, or the line carrying on
		case singleQuote(c):
			i = p.single(i)
			p.cmd = false
		case doubleQuote(c):
			i = p.double(i)
			p.cmd = false
		case c == '@' && (singleQuote(n) || doubleQuote(n)) && p.lineBreakAfter(i+2):
			i = p.here(i)
			p.cmd = false
		case c == '@' && nameStart(n):
			// A splatted variable.
			e := i + 1
			for e < end && nameRune(p.rs[e]) {
				e++
			}
			p.add(i, e, Variable)
			i = e
			p.cmd = false
		case c == '$':
			i = p.dollar(i)
			p.cmd = false
		case c == '[' && p.typeAt(i):
			i = p.typeName(i)
			p.cmd = false
		case c == '-' && unicode.IsLetter(n):
			i = p.dash(i)
		case c == '-' && n == '-' && unicode.IsLetter(p.at(i+2, end)):
			// A native program's long option, as --version.
			i = p.wordEnd(i)
			p.cmd = false
		case isDigit(c) || c == '.' && isDigit(n):
			i = p.number(i)
		case nameStart(c) || p.cmd && strings.ContainsRune(`.\/~`, c) && !blank(n) && n != 0:
			i = p.bare(i)
		default:
			i = p.punct(i)
		}
	}
}

// singleQuote and doubleQuote report whether r opens or closes a
// string, as PowerShell takes typographic quotes too.
func singleQuote(r rune) bool {
	return r == '\'' || r == '‘' || r == '’' || r == '‚' || r == '‛'
}
func doubleQuote(r rune) bool { return r == '"' || r == '“' || r == '”' || r == '„' }

// lineBreakAfter reports whether only blanks stand between i and the
// end of its line, as a here-string's opening needs.
func (p *powerShell) lineBreakAfter(i int) bool {
	for ; i < p.end; i++ {
		switch c := p.rs[i]; {
		case c == '\n':
			return true
		case !blank(c):
			return false
		}
	}
	return false
}

// single colours the single-quoted string at i, in which two quotes
// stand for one, and returns where it ends.
func (p *powerShell) single(i int) int {
	j := i + 1
	for j < p.end {
		if singleQuote(p.rs[j]) {
			if singleQuote(p.at(j+1, p.end)) {
				j += 2
				continue
			}
			j++
			break
		}
		j++
	}
	p.add(i, j, String)
	return j
}

// double colours the double-quoted string at i, with what expands in
// it, and returns where it ends.
func (p *powerShell) double(i int) int {
	piece := i
	j := i + 1
	for j < p.end {
		c := p.rs[j]
		switch {
		case c == '`':
			j = min(j+2, p.end)
			continue
		case doubleQuote(c) && doubleQuote(p.at(j+1, p.end)):
			j += 2
			continue
		case doubleQuote(c):
			p.add(piece, j+1, String)
			return j + 1
		case c == '$' && p.expands(j):
			p.add(piece, j, String)
			j = p.dollar(j)
			piece = j
			continue
		}
		j++
	}
	p.add(piece, j, String)
	return j
}

// here colours the here-string at i, and returns where it ends: after
// the quote and @ that start a line of their own.
func (p *powerShell) here(i int) int {
	quote := p.rs[i+1]
	expand := doubleQuote(quote)
	j := p.lineEnd(i, p.end)
	for j < p.end {
		ls := j + 1
		k := ls
		for k < p.end && blank(p.rs[k]) {
			k++
		}
		if k+1 < p.end && (singleQuote(p.rs[k]) && !expand || doubleQuote(p.rs[k]) && expand) && p.rs[k+1] == '@' {
			p.hereBody(i, k+2, expand)
			return k + 2
		}
		j = p.lineEnd(ls, p.end)
	}
	p.hereBody(i, p.end, expand)
	return p.end
}

// hereBody colours a here-string from i up to end, with what expands
// in it when it is double-quoted.
func (p *powerShell) hereBody(i, end int, expand bool) {
	if !expand {
		p.add(i, end, String)
		return
	}
	piece := i
	for j := i; j < end; {
		switch c := p.rs[j]; {
		case c == '`':
			j += 2
			continue
		case c == '$' && p.expands(j):
			p.add(piece, j, String)
			j = p.dollar(j)
			piece = j
			continue
		}
		j++
	}
	p.add(piece, end, String)
}

// expands reports whether the $ at i starts something that expands.
func (p *powerShell) expands(i int) bool {
	n := p.at(i+1, p.end)
	return n == '(' || n == '{' || nameStart(n) || n == '$' || n == '?' || n == '^'
}

// dollar colours the variable or the subexpression at i, and returns
// where it ends.
func (p *powerShell) dollar(i int) int {
	n := p.at(i+1, p.end)
	switch {
	case n == '(':
		e, closed := p.match(i + 1)
		p.add(i, i+2, Operator)
		(&powerShell{source: p.source, typeEnd: -1}).run(i+2, e)
		if closed {
			p.add(e, e+1, Operator)
			e++
		}
		return e
	case n == '{':
		e := i + 2
		for e < p.end && p.rs[e] != '}' {
			e++
		}
		e = min(e+1, p.end)
		p.add(i, e, Variable)
		return e
	case nameStart(n):
		e := i + 1
		for e < p.end && nameRune(p.rs[e]) {
			e++
		}
		// A scope or a drive before the name, as in $env:Path.
		if p.at(e, p.end) == ':' && nameStart(p.at(e+1, p.end)) {
			for e++; e < p.end && nameRune(p.rs[e]); e++ {
			}
		}
		switch strings.ToLower(p.text(i+1, e)) {
		case "true", "false", "null":
			p.add(i, e, Builtin)
		default:
			p.add(i, e, Variable)
		}
		return e
	case n == '$' || n == '?' || n == '^':
		p.add(i, i+2, Variable)
		return i + 2
	}
	return i + 1
}

// match finds the ) that closes the ( at i, past strings and what
// nests, and reports whether there is one: where there is none, it
// returns the end.
func (p *powerShell) match(i int) (int, bool) {
	depth := 0
	for j := i; j < p.end; j++ {
		switch c := p.rs[j]; {
		case c == '`':
			j++
		case singleQuote(c):
			for j++; j < p.end && !singleQuote(p.rs[j]); j++ {
			}
		case doubleQuote(c):
			for j++; j < p.end && !doubleQuote(p.rs[j]); j++ {
				if p.rs[j] == '`' {
					j++
				}
			}
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				return j, true
			}
		}
	}
	return p.end, false
}

// typeAt reports whether the [ at i starts a type's name, as [int] or
// [System.IO.Path] do, rather than an index into what comes before it.
func (p *powerShell) typeAt(i int) bool {
	if !nameStart(p.at(i+1, p.end)) {
		return false
	}
	if i == 0 {
		return true
	}
	b := p.rs[i-1]
	if b == ']' && i-1 == p.typeEnd {
		return true
	}
	return !nameRune(b) && b != ')' && b != ']' && b != '}' && !singleQuote(b) && !doubleQuote(b)
}

// typeName colours the type named in the brackets at i, and returns
// where the name ends. An attribute, as [Parameter(Mandatory)] is, has
// its arguments read as code after it.
func (p *powerShell) typeName(i int) int {
	j := i + 1
	depth := 0
	for j < p.end {
		c := p.rs[j]
		switch {
		case nameRune(c) || c == '.' || c == ',' || c == '`' || c == ' ' && depth > 0:
		case c == '[':
			depth++
		case c == ']' && depth > 0:
			depth--
		case c == '(' && depth == 0:
			// An attribute: its arguments name things, and run nothing.
			p.add(i+1, j, Type)
			e, closed := p.match(j)
			(&powerShell{source: p.source, args: true, typeEnd: -1}).run(j+1, e)
			if closed {
				e++
			}
			p.typeEnd = e
			return e
		default:
			p.add(i+1, j, Type)
			p.typeEnd = j
			return j
		}
		j++
	}
	p.add(i+1, j, Type)
	return j
}

// dash colours the dash and word at i, an operator such as -eq or a
// parameter's name, and returns where it ends.
func (p *powerShell) dash(i int) int {
	e := i + 1
	for e < p.end && (nameRune(p.rs[e]) || p.rs[e] == '-') {
		e++
	}
	if psOperators[strings.ToLower(p.text(i+1, e))] {
		p.add(i, e, Operator)
		p.cmd = false
	}
	return e
}

// number colours the number at i and returns where it ends. Digits
// that run on into letters are a word, as 7z is, read as one.
func (p *powerShell) number(i int) int {
	e := i
	if p.rs[i] == '0' && (p.at(i+1, p.end) == 'x' || p.at(i+1, p.end) == 'X') {
		for e = i + 2; e < p.end && unicode.Is(unicode.ASCII_Hex_Digit, p.rs[e]); e++ {
		}
	} else {
		for e < p.end && (isDigit(p.rs[e]) || p.rs[e] == '.' && isDigit(p.at(e+1, p.end))) {
			e++
		}
		if c := p.at(e, p.end); c == 'e' || c == 'E' {
			k := e + 1
			if c := p.at(k, p.end); c == '+' || c == '-' {
				k++
			}
			if isDigit(p.at(k, p.end)) {
				for e = k; e < p.end && isDigit(p.rs[e]); e++ {
				}
			}
		}
	}
	// A type suffix, and a multiplier such as KB.
	for _, suffix := range []string{"kb", "mb", "gb", "tb", "pb", "ul", "l", "d", "u", "y", "n", "s"} {
		if e+len(suffix) <= p.end && strings.EqualFold(p.text(e, e+len(suffix)), suffix) {
			e += len(suffix)
			break
		}
	}
	if c := p.at(e, p.end); nameRune(c) || c == '-' && nameStart(p.at(e+1, p.end)) {
		return p.bare(i)
	}
	p.add(i, e, Number)
	p.cmd = false
	return e
}

// wordEnd returns where the word at i ends, at a blank or what
// separates.
func (p *powerShell) wordEnd(i int) int {
	for i < p.end {
		c := p.rs[i]
		if blank(c) || c == '\n' || strings.ContainsRune("|;&(){}\"'$,<>`=", c) || singleQuote(c) || doubleQuote(c) {
			break
		}
		i++
	}
	return i
}

// bare colours the bare word at i for what it is where it stands: a
// keyword, a command, or a method's name, and returns where it ends.
func (p *powerShell) bare(i int) int {
	e := i
	if p.cmd {
		// A command: a cmdlet's name, or a program's path.
		e = p.wordEnd(i)
	} else {
		for e < p.end && (nameRune(p.rs[e]) || p.rs[e] == '-' && nameRune(p.at(e+1, p.end))) {
			e++
		}
	}
	if e == i {
		return i + 1 // a number's point, as in .5x
	}
	w := strings.ToLower(p.text(i, e))
	switch {
	case p.name:
		p.add(i, e, p.nameKind)
		p.name = false
	case psKeywords[w]:
		p.add(i, e, Keyword)
		switch w {
		case "function", "filter", "workflow", "configuration":
			p.name, p.nameKind = true, Function
		case "class", "enum":
			p.name, p.nameKind = true, Type
		case "return", "throw", "exit", "else", "try", "finally", "do", "begin", "process", "end", "clean":
			// A command may follow.
			return e
		}
		p.cmd = false
	case p.cmd:
		p.add(i, e, Function)
		p.cmd = false
	case p.at(e, p.end) == '(' && i > 0 && (p.rs[i-1] == '.' || p.rs[i-1] == ':'):
		p.add(i, e, Function)
	}
	return e
}

// punct reads the punctuation at i, an operator or what separates, and
// returns where it ends.
func (p *powerShell) punct(i int) int {
	c, n := p.rs[i], p.at(i+1, p.end)
	switch {
	case c == '|' || c == '&':
		w := 1
		if n == c {
			w = 2
		}
		p.add(i, i+w, Operator)
		p.cmd = !p.args
		return i + w
	case c == ';' || c == '(' || c == '{':
		p.cmd = !p.args
	case c == '@' && (n == '(' || n == '{'):
		p.cmd = !p.args
		return i + 2
	case c == ')' || c == '}' || c == ']':
		p.cmd = false
	case c == '.' && n == '.':
		p.add(i, i+2, Operator)
		return i + 2
	case c == '.' && p.cmd && (blank(n) || n == 0):
		p.add(i, i+1, Operator) // dot-sourcing
		return i + 1
	case strings.ContainsRune("+-*/%", c) && n == '=':
		p.add(i, i+2, Operator)
		p.cmd = !p.args
		return i + 2
	case c == '+' && n == '+', c == '-' && n == '-':
		p.add(i, i+2, Operator)
		return i + 2
	case (c == '%' || c == '?') && p.cmd && (blank(n) || n == '{'):
		// ForEach-Object and Where-Object, by their short names.
		p.add(i, i+1, Function)
		p.cmd = false
	case c == '=':
		p.add(i, i+1, Operator)
		p.cmd = !p.args
	case c == '>':
		w := 1
		if n == '>' {
			w = 2
		}
		p.add(i, i+w, Operator)
		return i + w
	case strings.ContainsRune("+-*/%!<", c):
		p.add(i, i+1, Operator)
	}
	return i + 1
}
