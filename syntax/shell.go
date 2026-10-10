package syntax

import (
	"strings"
	"unicode"
)

// source is code read as runes, with the tokens found in it so far. The
// shells' highlighters read it a rune at a time, as their languages are
// read: each word means something by where it stands.
type source struct {
	rs  []rune
	out []Token
}

// add notes a token from rune start up to rune end, unless it is plain
// or empty.
func (s *source) add(start, end int, k Kind) {
	if end > start && k != Plain {
		s.out = append(s.out, Token{Start: start, End: end, Kind: k})
	}
}

// at returns rune i, or 0 at or past end.
func (s *source) at(i, end int) rune {
	if i >= 0 && i < end {
		return s.rs[i]
	}
	return 0
}

// lineEnd returns where the line holding rune i ends, its line break
// left out, or end when it runs that far.
func (s *source) lineEnd(i, end int) int {
	for i < end && s.rs[i] != '\n' {
		i++
	}
	return i
}

// text returns the source from start up to end.
func (s *source) text(start, end int) string { return string(s.rs[start:end]) }

// blank reports whether r is space between words on a line.
func blank(r rune) bool { return r == ' ' || r == '\t' || r == '\r' || r == '\f' || r == '\v' }

// nameStart and nameRune report whether r starts or carries on a
// variable's name, as the shells spell them.
func nameStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }
func nameRune(r rune) bool  { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// isDigit reports whether r is an ASCII digit.
func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// number reports whether w is a number on its own: digits, with a
// decimal point at most.
func number(w string) bool {
	if w == "" || w == "." {
		return false
	}
	dots := 0
	for _, r := range w {
		switch {
		case r == '.':
			dots++
		case !isDigit(r):
			return false
		}
	}
	return dots <= 1 && w[0] != '.' && w[len(w)-1] != '.'
}

// words makes a set of words from a list written out with spaces.
func words(list string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(list) {
		set[w] = true
	}
	return set
}
