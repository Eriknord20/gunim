package syntax

import (
	"fmt"
	"strings"
	"testing"
)

// kinds writes each token of src as text=kind, for comparing.
func kinds(src string, toks []Token) string {
	rs := []rune(src)
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		out = append(out, fmt.Sprintf("%s=%s", string(rs[t.Start:t.End]), t.Kind))
	}
	return strings.Join(out, " ")
}

func TestGoColoursWhatTheCompilerReads(t *testing.T) {
	src := "package main\n\n// Hello says hi.\nfunc Hello(n int) string {\n\treturn fmt.Sprint(len(\"hé\"), n+1, 'x', nil)\n}\n\ntype Point struct{ X float64 }\n"
	got := kinds(src, Go(src))
	want := "package=keyword // Hello says hi.=comment func=keyword Hello=function int=type string=type " +
		"return=keyword Sprint=function len=builtin \"hé\"=string +=operator 1=number 'x'=string nil=builtin " +
		"type=keyword Point=type struct=keyword float64=type"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestGoCountsRunesPastWideCharacters(t *testing.T) {
	src := "s := \"åäö\" // ✓ done\nx := 1"
	toks := Go(src)
	got := kinds(src, toks)
	if want := ":==operator \"åäö\"=string // ✓ done=comment :==operator 1=number"; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestGoCarriesOnPastHalfTypedSource(t *testing.T) {
	for _, src := range []string{
		"func f( {",
		"s := \"never closed",
		"/* never closed\nfunc",
		"r := `raw\r\nstring` + 1",
		"x := 1 @ 2",
		"",
	} {
		toks := Go(src)
		n := len([]rune(src))
		end := 0
		for _, tk := range toks {
			if tk.Start < end || tk.End <= tk.Start || tk.End > n {
				t.Fatalf("%q: token %+v overlaps, is empty, or runs past %d", src, tk, n)
			}
			end = tk.End
		}
	}
	src := "r := `raw\r\nstring` + 1"
	if got := kinds(src, Go(src)); got != ":==operator `raw\r\nstring`=string +=operator 1=number" {
		t.Fatalf("a raw string with a carriage return reads %q", got)
	}
}
