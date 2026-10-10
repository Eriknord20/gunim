package syntax

import (
	"math/rand"
	"testing"
)

func TestForNamePicksByNameAndEnd(t *testing.T) {
	for name, want := range map[string]bool{
		"main.go": true, "x/Makefile": true, "Dockerfile": true, "a.PY": true, "b.tsx": true,
		"c.json": true, "d.yaml": true, "e.toml": true, "f.html": true, "g.patch": true, "notes.txt": false,
		"README": false, "C:\\src\\app.cs": true, ".bashrc": true,
	} {
		if got := ForName(name) != nil; got != want {
			t.Errorf("ForName(%q) found one: %v, want %v", name, got, want)
		}
	}
}

func TestTablesColourWhatTheLanguagesSay(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"a.py", "def f(x):  # hi\n    return \"\"\"doc\nmore\"\"\" + 1.5",
			"def=keyword f=function :=operator # hi=comment return=keyword \"\"\"doc\nmore\"\"\"=string +=operator 1.5=number"},
		{"a.js", "const s = `a ${b}`; // c\nconsole.log(0x1F)",
			"const=keyword ==operator `a ${b}`=string // c=comment console=builtin log=function 0x1F=number"},
		{"a.c", "#include <stdio.h>\nint main(void) { /* x */ return 0; }",
			"#include=keyword <=operator >=operator int=type main=function void=type /* x */=comment return=keyword 0=number"},
		{"q.sql", "SELECT name FROM users WHERE id = 1 -- one",
			"SELECT=keyword FROM=keyword WHERE=keyword ==operator 1=number -- one=comment"},
		{"a.json", `{"name": "x", "n": -2, "ok": true}`,
			`"name"=function "x"=string "n"=function -2=number "ok"=function true=builtin`},
		{"a.yaml", "name: kakel # app\nlist:\n  - true\n  - \"s\"",
			"name=function # app=comment list=function true=builtin \"s\"=string"},
		{"a.toml", "[server]\nport = 8080\nhost = \"x\" # c",
			"[server]=keyword port=function ==operator 8080=number host=function ==operator \"x\"=string # c=comment"},
		{"a.html", `<!-- c --><a href="x">t &amp; u</a>`,
			`<!-- c -->=comment <=operator a=keyword href=type "x"=string >=operator &amp;=builtin </=operator a=keyword >=operator`},
		{"a.diff", "--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new\n same",
			"--- a=comment +++ b=comment @@ -1 +1 @@=keyword -old=variable +new=string"},
		{"Makefile", "CC := gcc\nall: main.o\n\t$(CC) -o x # build",
			"CC=variable all=function $(CC)=variable # build=comment"},
		{"Dockerfile", "FROM alpine AS b\nRUN echo \"$HOME\" # c",
			"FROM=keyword AS=keyword RUN=keyword \"$HOME\"=string"},
	} {
		h := ForName(c.name)
		if got := kinds(c.src, h(c.src)); got != c.want {
			t.Errorf("%s %q\n got  %s\n want %s", c.name, c.src, got, c.want)
		}
	}
}

// Every table and special highlighter keeps its tokens in order, apart
// and inside the source, whatever it is given.
func TestEveryHighlighterCopesWithAnything(t *testing.T) {
	names := []string{"a.py", "a.js", "a.ts", "a.c", "a.cpp", "a.rs", "a.rb", "a.php", "a.pl", "a.lua", "a.sql",
		"a.css", "a.tf", "a.proto", "a.dart", "a.json", "a.yaml", "a.toml", "a.html", "a.diff", "Makefile", "Dockerfile",
		"a.java", "a.cs", "a.kt", "a.swift", "a.scala", ".gitignore"}
	const alphabet = "ab1 \t\n$%!{}()[]<>|&;'\"`\\#@=-+.:~^*,/éx-<!--*/@@"
	r := rand.New(rand.NewSource(7))
	for _, name := range names {
		h := ForName(name)
		for range 3000 {
			rs := make([]rune, r.Intn(40))
			for i := range rs {
				rs[i] = []rune(alphabet)[r.Intn(len([]rune(alphabet)))]
			}
			src := string(rs)
			end := 0
			for _, tk := range h(src) {
				if tk.Start < end || tk.End <= tk.Start || tk.End > len(rs) || tk.Kind == Plain {
					t.Fatalf("%s on %q: token %+v after %d", name, src, tk, end)
				}
				end = tk.End
			}
		}
	}
}
