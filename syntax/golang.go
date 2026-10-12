package syntax

import (
	"go/scanner"
	"go/token"
	"strings"
	"unicode/utf8"
)

// Go highlights Go source. It reads the source with go/scanner, so it
// colours what the compiler would read, and carries on past mistakes.
func Go(src string) []Token {
	b := []byte(src)
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(b))
	var s scanner.Scanner
	s.Init(file, b, func(token.Position, string) {}, scanner.ScanComments)

	// Scan everything first: a name's kind can depend on the token
	// after it, as a call's does.
	type scanned struct {
		start, end int
		tok        token.Token
		lit        string
	}
	var all []scanned
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.SEMICOLON && lit == "\n" || tok == token.ILLEGAL {
			// A semicolon the scanner put in at a line's end, or a
			// character it cannot read.
			continue
		}
		start := file.Offset(pos)
		all = append(all, scanned{start: start, end: tokenEnd(src, start, tok, lit), tok: tok, lit: lit})
	}

	var out []Token
	runes := runeCounter{src: src}
	for i, t := range all {
		kind := Plain
		switch {
		case t.tok == token.COMMENT:
			kind = Comment
		case t.tok == token.STRING || t.tok == token.CHAR:
			kind = String
		case t.tok == token.INT || t.tok == token.FLOAT || t.tok == token.IMAG:
			kind = Number
		case t.tok.IsKeyword():
			kind = Keyword
		case t.tok == token.IDENT:
			kind = goName(t.lit, i > 0 && all[i-1].tok == token.TYPE,
				i+1 < len(all) && all[i+1].tok == token.LPAREN)
		case t.tok.IsOperator() && !delimiter(t.tok):
			kind = Operator
		}
		if kind == Plain {
			continue
		}
		out = append(out, Token{Start: runes.at(t.start), End: runes.at(t.end), Kind: kind})
	}
	return out
}

// tokenEnd returns the byte offset a token that starts at start ends
// at. The scanner hands comments and raw strings back with carriage
// returns taken out, so their ends come from the source itself.
func tokenEnd(src string, start int, tok token.Token, lit string) int {
	switch {
	case tok == token.COMMENT && strings.HasPrefix(src[start:], "//"):
		if n := strings.IndexByte(src[start:], '\n'); n >= 0 {
			return start + n
		}
		return len(src)
	case tok == token.COMMENT:
		if n := strings.Index(src[start+2:], "*/"); n >= 0 {
			return start + 2 + n + 2
		}
		return len(src)
	case tok == token.STRING && strings.HasPrefix(src[start:], "`"):
		if n := strings.IndexByte(src[start+1:], '`'); n >= 0 {
			return start + 1 + n + 1
		}
		return len(src)
	case lit != "":
		return min(start+len(lit), len(src))
	}
	return min(start+len(tok.String()), len(src))
}

// goName returns the kind of a name: a predeclared type or function,
// a type being declared, a function being called or declared, or plain.
func goName(name string, afterType, beforeParen bool) Kind {
	switch {
	case goTypes[name]:
		return Type
	case goBuiltins[name]:
		return Builtin
	case afterType:
		return Type
	case beforeParen:
		return Function
	}
	return Plain
}

// delimiter reports whether tok is punctuation that only separates,
// which stays plain.
func delimiter(tok token.Token) bool {
	switch tok {
	case token.LPAREN, token.RPAREN, token.LBRACK, token.RBRACK, token.LBRACE, token.RBRACE,
		token.COMMA, token.SEMICOLON, token.PERIOD, token.COLON:
		return true
	default:
		// Every other token is plain text.
	}
	return false
}

// goTypes are Go's predeclared types.
var goTypes = map[string]bool{
	"any": true, "bool": true, "byte": true, "comparable": true, "complex64": true, "complex128": true,
	"error": true, "float32": true, "float64": true, "int": true, "int8": true, "int16": true, "int32": true,
	"int64": true, "rune": true, "string": true, "uint": true, "uint8": true, "uint16": true, "uint32": true,
	"uint64": true, "uintptr": true,
}

// goBuiltins are Go's predeclared functions and constants.
var goBuiltins = map[string]bool{
	"append": true, "cap": true, "clear": true, "close": true, "complex": true, "copy": true, "delete": true,
	"imag": true, "len": true, "make": true, "max": true, "min": true, "new": true, "panic": true,
	"print": true, "println": true, "real": true, "recover": true,
	"true": true, "false": true, "iota": true, "nil": true,
}

// runeCounter turns byte offsets into rune offsets, for offsets asked
// for in order.
type runeCounter struct {
	src         string
	byte, runes int
}

// at returns the rune offset of byte offset b. Asked for an offset
// before the last one, it counts again from the start.
func (c *runeCounter) at(b int) int {
	if b < c.byte {
		c.byte, c.runes = 0, 0
	}
	c.runes += utf8.RuneCountInString(c.src[c.byte:b])
	c.byte = b
	return c.runes
}
