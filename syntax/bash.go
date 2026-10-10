package syntax

import "strings"

// Bash highlights what is typed at bash and the shells like it, zsh and
// sh among them. It reads the line as the shell does: the first word of
// a command is what runs, so it takes a builtin's colour or a
// function's, and the words after it are its arguments. Keywords count
// only where a command could start, as the shell reads them. Variables,
// strings with what expands inside them, here documents, comments, and
// the pipes and redirections between commands have colours of their
// own, and the code inside $( ) and backquotes is read again as code.
func Bash(src string) []Token {
	s := &source{rs: []rune(src)}
	b := &bash{source: s}
	b.run(0, len(s.rs))
	return s.out
}

// bash is a place in shell code: where it ends, and what the next word
// means there.
type bash struct {
	*source
	end int
	// args says no word is a command, as in an array's parentheses.
	args bool
	// cmd says the next word is a command, and prefixed that a command
	// such as sudo was before it, so an option comes first. declaring
	// says the command sets variables, as export does.
	cmd, prefixed, declaring bool
	// name says the next word names something, in nameKind: a loop's
	// variable, or a function being declared.
	name     bool
	nameKind Kind
	// wantIn says in is a keyword next, after for, select or case, and
	// casing that it is case's.
	wantIn, casing bool
	// cases counts the case statements open, and pattern says the next
	// words are a case's pattern, up to its ).
	cases   int
	pattern bool
	// test says a [[ is open, for its ]].
	test bool
	// redirect says the next word is where a redirection goes, and
	// heredoc that it ends a here document.
	redirect, heredoc, tabs bool
	// docs are the here documents asked for on this line, to read from
	// the next.
	docs []hereDoc
}

// hereDoc is a here document asked for: the line that ends it, whether
// tabs before that line are let pass, and whether what is in it
// expands.
type hereDoc struct {
	delim        string
	tabs, expand bool
}

// bashKeywords are the shell's reserved words.
var bashKeywords = words(`if then else elif fi case esac for select while until do done in function time coproc
	[[ ]] !`)

// bashBuiltins are the commands bash runs itself.
var bashBuiltins = words(`alias bg bind break builtin caller cd command compgen complete compopt continue declare
	dirs disown echo enable eval exec exit export false fc fg getopts hash help history jobs kill let local logout
	mapfile popd printf pushd pwd read readarray readonly return set shift shopt source suspend test times trap true
	type typeset ulimit umask unalias unset wait . : [`)

// bashDeclares are the builtins that set the variables named after
// them.
var bashDeclares = words(`export local declare readonly typeset`)

// bashPrefixes are commands that run the command after them.
var bashPrefixes = words(`sudo doas exec command builtin nohup nice time env xargs watch`)

// run reads from i up to end.
func (b *bash) run(i, end int) {
	b.end = end
	b.cmd = !b.args
	for i < end {
		c := b.rs[i]
		switch {
		case c == '\n':
			i++
			b.newCommand()
			b.cmd = !b.args && !b.pattern
			b.wantIn, b.name, b.heredoc = false, false, false
			i = b.hereDocs(i)
		case blank(c):
			i++
		case c == '\\' && b.at(i+1, end) == '\n':
			i += 2 // the line carries on
		case c == '#':
			e := b.lineEnd(i, end)
			b.add(i, e, Comment)
			i = e
		case strings.ContainsRune("|&;()<>", c):
			i = b.operator(i)
		default:
			i = b.word(i)
		}
	}
}

// newCommand notes that a command starts next.
func (b *bash) newCommand() {
	b.cmd = !b.args
	b.prefixed, b.redirect, b.declaring = false, false, false
}

// operator reads the operator at i, and returns where it ends.
func (b *bash) operator(i int) int {
	c, n := b.rs[i], b.at(i+1, b.end)
	switch c {
	case '(':
		if n == '(' && (b.cmd || b.name) {
			// (( arithmetic )), or a for loop's
			b.name, b.wantIn = false, false
			e, closed := b.match(i, '(', ')')
			inner := e - 1
			if !closed || b.at(inner, b.end) != ')' {
				inner = e
			}
			b.add(i, i+2, Operator)
			b.arith(i+2, inner)
			if closed {
				b.add(inner, e+1, Operator)
				e++
			}
			b.cmd = false
			return e
		}
		if !b.pattern {
			b.newCommand()
		}
		return i + 1
	case ')':
		if b.pattern {
			b.pattern = false
			b.newCommand()
		} else {
			b.cmd = false
		}
		return i + 1
	case '<', '>':
		if n == '(' {
			// <( ) and >( ) run a command in place of a file.
			return b.inside(i, i+2, '(', ')')
		}
		switch op := b.text(i, min(i+3, b.end)); {
		case strings.HasPrefix(op, "<<<"):
			b.add(i, i+3, Operator)
			b.redirect = true
			return i + 3
		case strings.HasPrefix(op, "<<-"), strings.HasPrefix(op, "<<"):
			w := 2
			b.tabs = strings.HasPrefix(op, "<<-")
			if b.tabs {
				w = 3
			}
			b.add(i, i+w, Operator)
			b.heredoc = true
			return i + w
		case strings.HasPrefix(op, "<&"), strings.HasPrefix(op, ">&"), strings.HasPrefix(op, ">>"),
			strings.HasPrefix(op, ">|"), strings.HasPrefix(op, "<>"):
			b.add(i, i+2, Operator)
			b.redirect = true
			return i + 2
		}
		b.add(i, i+1, Operator)
		b.redirect = true
		return i + 1
	case '|':
		w := 1
		if n == '|' || n == '&' {
			w = 2
		}
		b.add(i, i+w, Operator)
		if !b.pattern {
			b.newCommand()
		}
		return i + w
	case '&':
		switch n {
		case '&':
			b.add(i, i+2, Operator)
			b.newCommand()
			return i + 2
		case '>':
			w := 2
			if b.at(i+2, b.end) == '>' {
				w = 3
			}
			b.add(i, i+w, Operator)
			b.redirect = true
			return i + w
		}
		b.add(i, i+1, Operator)
		b.newCommand()
		return i + 1
	}
	// ;
	if n == ';' || n == '&' {
		w := 2
		if n == ';' && b.at(i+2, b.end) == '&' {
			w = 3
		}
		b.add(i, i+w, Operator)
		b.newCommand()
		if b.cases > 0 {
			b.pattern, b.cmd = true, false
		}
		return i + w
	}
	b.newCommand()
	return i + 1
}

// word reads the word at i, colouring what is in it and what it is,
// and returns where it ends.
func (b *bash) word(i int) int {
	start := i
	literal, assigned := true, false
	for i < b.end {
		c := b.rs[i]
		if blank(c) || c == '\n' || strings.ContainsRune("|&;()<>", c) {
			break
		}
		switch c {
		case '\\':
			i = min(i+2, b.end)
			literal = false
		case '\'':
			e := i + 1
			for e < b.end && b.rs[e] != '\'' {
				e++
			}
			e = min(e+1, b.end)
			b.add(i, e, String)
			i, literal = e, false
		case '"':
			i, literal = b.quoted(i), false
		case '$':
			e := b.dollar(i, false)
			if e > i+1 {
				literal = false
			}
			i = e
		case '`':
			i, literal = b.inside(i, i+1, '`', '`'), false
		case '=':
			if literal && !assigned && (b.cmd || b.declaring) && !b.heredoc && !b.redirect && assignable(b.text(start, i)) {
				name := i
				if b.rs[i-1] == '+' {
					name--
				}
				b.add(start, name, Variable)
				b.add(name, i+1, Operator)
				assigned = true
				i++
				if b.at(i, b.end) == '(' {
					// An array: words, none of them a command.
					e, closed := b.match(i, '(', ')')
					(&bash{source: b.source, args: true}).run(i+1, e)
					if closed {
						e++
					}
					return e
				}
				start = i
				continue
			}
			i++
		default:
			i++
		}
	}
	if assigned {
		return i // the command, if any, comes next
	}
	b.means(start, i, literal)
	return i
}

// means colours a word for what it is where it stands, and notes what
// the word after it is.
func (b *bash) means(start, end int, literal bool) {
	w := b.text(start, end)
	whole := func(k Kind) {
		if literal {
			b.add(start, end, k)
		}
	}
	switch {
	case b.heredoc:
		b.heredoc = false
		whole(String)
		b.docs = append(b.docs, hereDoc{delim: unquote(w), tabs: b.tabs, expand: literal})
	case b.redirect:
		b.redirect = false
		if literal && number(w) {
			b.add(start, end, Number)
		}
	case b.name:
		b.name = false
		whole(b.nameKind)
	case b.wantIn && literal && w == "in":
		b.add(start, end, Keyword)
		b.wantIn = false
		if b.casing {
			b.pattern, b.casing = true, false
		}
	case b.wantIn:
		// The word case looks at, or a for loop's list.
	case b.pattern && literal && w == "esac":
		b.add(start, end, Keyword)
		b.keyword(w)
	case literal && w == "{":
		b.newCommand()
	case literal && w == "}" && b.cmd:
		b.cmd = false
	case b.test && literal && w == "]]":
		b.add(start, end, Keyword)
		b.test, b.cmd = false, false
	case b.cmd && literal && bashKeywords[w]:
		b.add(start, end, Keyword)
		b.keyword(w)
	case b.cmd && b.prefixed && strings.HasPrefix(w, "-"):
		// An option of sudo's, before the command it runs.
	case b.cmd:
		b.cmd, b.prefixed = false, false
		switch {
		case !literal:
		case bashBuiltins[w]:
			b.add(start, end, Builtin)
		default:
			b.add(start, end, Function)
		}
		if literal && bashPrefixes[w] {
			b.cmd, b.prefixed = true, true
		}
		b.declaring = literal && bashDeclares[w]
	case literal && number(w):
		b.add(start, end, Number)
	}
}

// keyword notes what comes after keyword w.
func (b *bash) keyword(w string) {
	switch w {
	case "for", "select":
		b.name, b.nameKind, b.wantIn, b.cmd = true, Variable, true, false
	case "case":
		b.cases++
		b.wantIn, b.casing, b.cmd = true, true, false
	case "esac":
		b.cases = max(b.cases-1, 0)
		b.pattern, b.cmd = false, false
	case "function":
		b.name, b.nameKind, b.cmd = true, Function, false
	case "[[":
		b.test, b.cmd = true, false
	case "fi", "done", "]]", "in":
		b.cmd = false
	}
}

// assignable reports whether w names a variable, with an index or a +
// before the = that sets it.
func assignable(w string) bool {
	w = strings.TrimSuffix(w, "+")
	if i := strings.IndexByte(w, '['); i > 0 && strings.HasSuffix(w, "]") {
		w = w[:i]
	}
	if w == "" {
		return false
	}
	for i, r := range w {
		if i == 0 && !nameStart(r) || !nameRune(r) {
			return false
		}
	}
	return true
}

// unquote is a here document's delimiter as written, its quotes and
// backslashes taken out.
func unquote(w string) string {
	return strings.Map(func(r rune) rune {
		if r == '\'' || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, w)
}

// quoted colours the double-quoted string at i, with what expands in
// it, and returns where it ends.
func (b *bash) quoted(i int) int {
	piece := i
	j := i + 1
	for j < b.end {
		c := b.rs[j]
		switch {
		case c == '\\':
			j = min(j+2, b.end)
			continue
		case c == '"':
			b.add(piece, j+1, String)
			return j + 1
		case c == '$' && b.expands(j):
			b.add(piece, j, String)
			j = b.dollar(j, true)
			piece = j
			continue
		case c == '`':
			b.add(piece, j, String)
			j = b.inside(j, j+1, '`', '`')
			piece = j
			continue
		}
		j++
	}
	b.add(piece, j, String)
	return j
}

// expands reports whether the $ at i starts something that expands.
func (b *bash) expands(i int) bool {
	n := b.at(i+1, b.end)
	return n == '{' || n == '(' || nameStart(n) || isDigit(n) || strings.ContainsRune("@*#?$!-", n)
}

// dollar colours what the $ at i expands, and returns where it ends:
// just past the $ when nothing follows it that expands.
func (b *bash) dollar(i int, inQuotes bool) int {
	n := b.at(i+1, b.end)
	switch {
	case n == '{':
		e, closed := b.match(i+1, '{', '}')
		if closed {
			e++
		}
		b.add(i, e, Variable)
		return e
	case n == '(' && b.at(i+2, b.end) == '(':
		e, closed := b.match(i+1, '(', ')')
		inner := e - 1
		if !closed || b.at(inner, b.end) != ')' {
			inner = e
		}
		b.add(i, i+3, Operator)
		b.arith(i+3, inner)
		if closed {
			b.add(inner, e+1, Operator)
			e++
		}
		return e
	case n == '(':
		return b.inside(i, i+2, '(', ')')
	case nameStart(n):
		e := i + 2
		for e < b.end && nameRune(b.rs[e]) {
			e++
		}
		b.add(i, e, Variable)
		return e
	case isDigit(n) || strings.ContainsRune("@*#?$!-", n):
		b.add(i, i+2, Variable)
		return i + 2
	case n == '\'' && !inQuotes:
		// $'...' takes backslash escapes.
		e := i + 2
		for e < b.end && b.rs[e] != '\'' {
			if b.rs[e] == '\\' {
				e++
			}
			e++
		}
		e = min(e+1, b.end)
		b.add(i, e, String)
		return e
	case n == '"' && !inQuotes:
		return b.quoted(i + 1)
	}
	return i + 1
}

// inside colours code within an opening, from i to body, and its
// closing, shut, as operators, and reads the code between them again,
// and returns where it ends.
func (b *bash) inside(i, body int, open, shut rune) int {
	var e int
	var closed bool
	if open == shut {
		e = body
		for e < b.end && b.rs[e] != shut {
			if b.rs[e] == '\\' {
				e++
			}
			e++
		}
		e = min(e, b.end)
		closed = e < b.end
	} else {
		e, closed = b.match(body-1, open, shut)
	}
	b.add(i, body, Operator)
	(&bash{source: b.source}).run(body, e)
	if closed {
		b.add(e, e+1, Operator)
		return e + 1
	}
	return e
}

// match finds the close that matches the open at i, past strings and
// what nests, and reports whether there is one: where there is none,
// it returns the end.
func (b *bash) match(i int, open, shut rune) (int, bool) {
	depth := 0
	for j := i; j < b.end; j++ {
		switch c := b.rs[j]; c {
		case '\\':
			j++
		case '\'':
			for j++; j < b.end && b.rs[j] != '\''; j++ {
			}
		case '"':
			for j++; j < b.end && b.rs[j] != '"'; j++ {
				if b.rs[j] == '\\' {
					j++
				}
			}
		case open:
			depth++
		case shut:
			depth--
			if depth == 0 {
				return j, true
			}
		}
	}
	return b.end, false
}

// arith colours arithmetic from i up to end: numbers, variables and
// operators.
func (b *bash) arith(i, end int) {
	for i < end {
		c := b.rs[i]
		switch {
		case isDigit(c):
			e := i
			for e < end && nameRune(b.rs[e]) {
				e++
			}
			b.add(i, e, Number)
			i = e
		case nameStart(c):
			e := i
			for e < end && nameRune(b.rs[e]) {
				e++
			}
			b.add(i, e, Variable)
			i = e
		case c == '$':
			saved := b.end
			b.end = end
			i = b.dollar(i, true)
			b.end = saved
		case strings.ContainsRune("+-*/%<>=!&|^~?:", c):
			e := i
			for e < end && strings.ContainsRune("+-*/%<>=!&|^~?:", b.rs[e]) {
				e++
			}
			b.add(i, e, Operator)
			i = e
		default:
			i++
		}
	}
}

// hereDocs colours the here documents asked for on the line before i,
// and returns where the code carries on after them.
func (b *bash) hereDocs(i int) int {
	for _, d := range b.docs {
		body := i
		for i < b.end {
			e := b.lineEnd(i, b.end)
			line := strings.TrimSuffix(b.text(i, e), "\r")
			if d.tabs {
				line = strings.TrimLeft(line, "\t")
			}
			if line == d.delim {
				b.docBody(body, i, d.expand)
				b.add(i, e, String)
				i = min(e+1, b.end)
				body = -1
				break
			}
			i = min(e+1, b.end)
		}
		if body >= 0 {
			// Not ended yet: the rest is the document.
			b.docBody(body, b.end, d.expand)
			i = b.end
		}
	}
	b.docs = b.docs[:0]
	return i
}

// docBody colours a here document's text, with what expands in it
// when its delimiter was not quoted.
func (b *bash) docBody(i, end int, expand bool) {
	if !expand {
		b.add(i, end, String)
		return
	}
	saved := b.end
	b.end = end
	piece := i
	for i < end {
		switch c := b.rs[i]; {
		case c == '\\':
			i += 2
			continue
		case c == '$' && b.expands(i):
			b.add(piece, i, String)
			i = b.dollar(i, true)
			piece = i
			continue
		}
		i++
	}
	b.add(piece, min(i, end), String)
	b.end = saved
}
