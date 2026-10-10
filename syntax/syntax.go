// Package syntax splits source code into tokens for colouring. A
// highlighter reads source and returns the runs worth a colour of their
// own, as rune offsets, so an editor can paint each in its kind's
// colour and leave the text between them plain.
//
// Go reads Go. Bash, PowerShell and Batch read what is typed at a
// shell: bash and the shells like it, PowerShell, and the Windows
// Command Prompt. A highlighter for another language is a function with
// the same signature.
package syntax

// Kind is what a token is, which picks its colour.
type Kind uint8

// The kinds a highlighter reports.
const (
	// Plain is text with no colour of its own: names, and the
	// punctuation between them.
	Plain Kind = iota
	// Keyword is a word of the language, such as func or return.
	Keyword
	// Builtin is a name the language declares, such as len or nil.
	Builtin
	// Type names a type: a predeclared one, such as int, or one being
	// declared.
	Type
	// Function names a function where it is called or declared.
	Function
	// String is a string or character literal.
	String
	// Number is a numeric literal.
	Number
	// Comment is a comment, with its markers.
	Comment
	// Operator is an operator, such as + or :=, or a shell's pipe.
	Operator
	// Variable is a variable where a shell reads or sets it, such as
	// $HOME or %PATH%.
	Variable
)

// String names the kind.
func (k Kind) String() string {
	switch k {
	case Plain:
		return "plain"
	case Keyword:
		return "keyword"
	case Builtin:
		return "builtin"
	case Type:
		return "type"
	case Function:
		return "function"
	case String:
		return "string"
	case Number:
		return "number"
	case Comment:
		return "comment"
	case Operator:
		return "operator"
	case Variable:
		return "variable"
	}
	return "invalid"
}

// A Token is a run of source of one kind, from rune Start up to rune
// End.
type Token struct {
	Start, End int
	Kind       Kind
}

// A Highlighter returns src's tokens, in order and apart, leaving out
// the plain text between them. It copes with source that is half
// typed: whatever it cannot read stays plain.
type Highlighter func(src string) []Token
