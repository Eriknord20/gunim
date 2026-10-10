package syntax

import (
	"strings"
	"unicode"
)

// lang is a language a table describes: how its comments, strings and
// numbers are written, and the words it gives meaning to. One scanner
// reads every language so described, well enough to colour it, though
// it knows none of their grammars.
type lang struct {
	// line starts a comment to the end of the line; block are comments
	// between a start and an end.
	line  []string
	block [][2]string
	// quotes are the characters a string is quoted in, and triple says
	// a quote three times over starts a string that runs over lines, as
	// Python's does. raw are quotes no backslash escapes in.
	quotes string
	raw    string
	triple bool
	// keywords, builtins and types are the language's words, and
	// caseless says they are known in any case, as SQL's are.
	keywords, builtins, types map[string]bool
	caseless                  bool
	// sigils start a variable's name, as $ does in PHP and Perl.
	sigils string
	// calls colours a name just before a ( as a function's, and caps a
	// name starting with a capital as a type's, as is the custom in Go,
	// Java and their like.
	calls, caps bool
	// nameRunes are what a name holds besides letters, digits and _.
	nameRunes string
}

// highlight reads src as l describes.
func (l *lang) highlight(src string) []Token {
	s := &source{rs: []rune(src)}
	rs := s.rs
	end := len(rs)
	word := func(r rune) bool { return nameRune(r) || strings.ContainsRune(l.nameRunes, r) }
	for i := 0; i < end; {
		c := rs[i]
		if e := l.comment(s, i, end); e > i {
			s.add(i, e, Comment)
			i = e
			continue
		}
		switch {
		case strings.ContainsRune(l.quotes, c):
			e := l.str(s, i, end)
			s.add(i, e, String)
			i = e
		case strings.ContainsRune(l.sigils, c) && i+1 < end && nameStart(rs[i+1]):
			e := i + 1
			for e < end && word(rs[e]) {
				e++
			}
			s.add(i, e, Variable)
			i = e
		case isDigit(c) || c == '.' && i+1 < end && isDigit(rs[i+1]):
			if i > 0 && word(rs[i-1]) {
				i++
				break
			}
			e := numberEnd(rs, i, end)
			s.add(i, e, Number)
			i = e
		case nameStart(c) || c == '#' && strings.ContainsRune(l.nameRunes, c) && i+1 < end && nameStart(rs[i+1]):
			e := i + 1
			for e < end && word(rs[e]) {
				e++
			}
			w := string(rs[i:e])
			key := w
			if l.caseless {
				key = strings.ToLower(w)
			}
			next := e
			for next < end && (rs[next] == ' ' || rs[next] == '\t') {
				next++
			}
			switch {
			case l.keywords[key]:
				s.add(i, e, Keyword)
			case l.types[key]:
				s.add(i, e, Type)
			case l.builtins[key]:
				s.add(i, e, Builtin)
			case l.calls && next < end && rs[next] == '(':
				s.add(i, e, Function)
			case l.caps && unicode.IsUpper(c) && e-i > 1:
				s.add(i, e, Type)
			}
			i = e
		case strings.ContainsRune("+-*/%=<>!&|^~?:", c):
			e := i
			for e < end && e-i < 3 && strings.ContainsRune("+-*/%=<>!&|^~?:", rs[e]) {
				e++
			}
			s.add(i, e, Operator)
			i = e
		default:
			i++
		}
	}
	return s.out
}

// comment returns where a comment starting at i ends, or i where none
// starts there.
func (l *lang) comment(s *source, i, end int) int {
	for _, b := range l.block {
		if s.has(i, end, b[0]) {
			for j := i + len([]rune(b[0])); j < end; j++ {
				if s.has(j, end, b[1]) {
					return j + len([]rune(b[1]))
				}
			}
			return end
		}
	}
	for _, p := range l.line {
		if s.has(i, end, p) {
			return s.lineEnd(i, end)
		}
	}
	return i
}

// str returns where the string quoted at i ends: past its closing
// quote, or at the end of its line where it has none, unless it is a
// triple-quoted one.
func (l *lang) str(s *source, i, end int) int {
	q := s.rs[i]
	if l.triple && s.has(i, end, string([]rune{q, q, q})) {
		shut := string([]rune{q, q, q})
		for j := i + 3; j < end; j++ {
			if s.rs[j] == '\\' {
				j++
				continue
			}
			if s.has(j, end, shut) {
				return j + 3
			}
		}
		return end
	}
	raw := strings.ContainsRune(l.raw, q)
	for j := i + 1; j < end; j++ {
		switch r := s.rs[j]; {
		case r == '\\' && !raw:
			j++
		case r == q:
			return j + 1
		case r == '\n' && q != '`':
			return j
		}
	}
	return end
}

// has reports whether the source at i starts with p.
func (s *source) has(i, end int, p string) bool {
	for _, r := range p {
		if i >= end || s.rs[i] != r {
			return false
		}
		i++
	}
	return true
}

// numberEnd returns where the number at i ends: digits, a point, an
// exponent, a base's letters and a type's suffix.
func numberEnd(rs []rune, i, end int) int {
	e := i
	for e < end {
		r := rs[e]
		switch {
		case nameRune(r), r == '.' && e+1 < end && isDigit(rs[e+1]):
		case (r == '+' || r == '-') && e > i && (rs[e-1] == 'e' || rs[e-1] == 'E') && !strings.HasPrefix(strings.ToLower(string(rs[i:e])), "0x"):
		default:
			return e
		}
		e++
	}
	return e
}

// The C family's words, which the languages below share in part.
const (
	cKeywords  = `break case continue default do else for goto if return switch while sizeof typedef struct union enum extern static const volatile register inline restrict`
	cTypes     = `void char short int long float double signed unsigned bool size_t int8_t int16_t int32_t int64_t uint8_t uint16_t uint32_t uint64_t`
	cppMore    = `class namespace template typename public private protected virtual override final new delete this try catch throw using operator friend explicit constexpr noexcept auto nullptr true false mutable static_cast dynamic_cast const_cast reinterpret_cast co_await co_return co_yield concept requires`
	javaWords  = `abstract assert break case catch class continue default do else enum extends final finally for if implements import instanceof interface native new package private protected public return static strictfp super switch synchronized this throw throws transient try volatile while var record sealed permits yield`
	javaTypes  = `boolean byte char short int long float double void String Object Integer Long Boolean`
	jsWords    = `break case catch class const continue debugger default delete do else export extends finally for function if import in instanceof let new return super switch this throw try typeof var void while with yield async await of static get set from as`
	jsBuiltins = `true false null undefined NaN Infinity console window document globalThis require module exports Promise JSON Math Object Array String Number Boolean Map Set Symbol Error`
)

// The languages described by tables.
var (
	langC = &lang{line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"'`,
		keywords: words(cKeywords + ` #include #define #if #ifdef #ifndef #endif #else #elif #pragma`), types: words(cTypes),
		builtins: words(`NULL true false`), calls: true, nameRunes: "#"}
	langCpp = &lang{line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"'`,
		keywords: words(cKeywords + " " + cppMore + ` #include #define #if #ifdef #ifndef #endif #else #elif #pragma`),
		types:    words(cTypes + ` string vector map set wstring`), calls: true, caps: true, nameRunes: "#"}
	langJava = &lang{line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"'`,
		keywords: words(javaWords), types: words(javaTypes), builtins: words(`true false null`), calls: true, caps: true}
	langCSharp = &lang{line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"'`,
		keywords: words(javaWords + ` namespace using foreach in out ref params base internal readonly sealed override virtual async await get set value event delegate operator implicit explicit checked unchecked fixed unsafe lock goto is as typeof sizeof stackalloc when where`),
		types:    words(`bool byte sbyte char decimal double float int uint long ulong short ushort object string void dynamic var`),
		builtins: words(`true false null`), calls: true, caps: true}
	langKotlin = &lang{line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"'`,
		keywords: words(`as break class continue do else for fun if in interface is object package return super this throw try typealias typeof val var when while by catch constructor finally get import init set where data sealed enum open override private protected public internal abstract companion lateinit suspend`),
		types:    words(`Int Long Short Byte Float Double Boolean Char String Unit Any Nothing`), builtins: words(`true false null`), calls: true, caps: true}
	langScala = &lang{line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"'`,
		keywords: words(`abstract case catch class def do else extends final finally for forSome if implicit import lazy match new object override package private protected return sealed super this throw trait try type val var while with yield given using enum then export`),
		builtins: words(`true false null`), calls: true, caps: true}
	langSwift = &lang{line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"`,
		keywords: words(`associatedtype class deinit enum extension fileprivate func import init inout internal let open operator private protocol public rethrows static struct subscript typealias var break case continue default defer do else fallthrough for guard if in repeat return switch where while as catch is throw throws try async await self Self super`),
		types:    words(`Int Double Float String Bool Character Array Dictionary Set Optional Any`), builtins: words(`true false nil`), calls: true, caps: true}
	langRust = &lang{line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"`,
		keywords: words(`as async await break const continue crate dyn else enum extern fn for if impl in let loop match mod move mut pub ref return self Self static struct super trait type unsafe use where while`),
		types:    words(`i8 i16 i32 i64 i128 isize u8 u16 u32 u64 u128 usize f32 f64 bool char str String Vec Option Result Box`),
		builtins: words(`true false Some None Ok Err`), calls: true, caps: true, nameRunes: "!"}
	langJS = &lang{line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: "\"'`",
		keywords: words(jsWords), builtins: words(jsBuiltins), calls: true}
	langTS = &lang{line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: "\"'`",
		keywords: words(jsWords + ` interface type enum implements namespace declare abstract private protected public readonly keyof infer is satisfies`),
		types:    words(`string number boolean any unknown never void object bigint symbol`), builtins: words(jsBuiltins), calls: true, caps: true}
	langPython = &lang{line: []string{"#"}, quotes: `"'`, triple: true,
		keywords: words(`and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield match case`),
		builtins: words(`True False None self print len range open str int float list dict set tuple bool type isinstance super object Exception`), calls: true}
	langRuby = &lang{line: []string{"#"}, block: [][2]string{{"=begin", "=end"}}, quotes: `"'`, raw: `'`,
		keywords: words(`alias and begin break case class def defined? do else elsif end ensure for if in module next not or redo rescue retry return self super then undef unless until when while yield require require_relative attr_accessor attr_reader private protected public`),
		builtins: words(`true false nil puts print`), sigils: "@$", calls: true, caps: true, nameRunes: "?!"}
	langPHP = &lang{line: []string{"//", "#"}, block: [][2]string{{"/*", "*/"}}, quotes: `"'`, raw: `'`,
		keywords: words(`abstract and array as break callable case catch class clone const continue declare default do echo else elseif empty enddeclare endfor endforeach endif endswitch endwhile extends final finally fn for foreach function global goto if implements include include_once instanceof insteadof interface isset list match namespace new or print private protected public readonly require require_once return static switch throw trait try unset use var while xor yield`),
		builtins: words(`true false null TRUE FALSE NULL`), sigils: "$", calls: true}
	langPerl = &lang{line: []string{"#"}, quotes: `"'`, raw: `'`,
		keywords: words(`my our local sub if elsif else unless while until for foreach do last next redo return use no package require eval`),
		builtins: words(`print printf open close die warn chomp push pop shift unshift keys values defined scalar`), sigils: "$@%", calls: true}
	langLua = &lang{line: []string{"--"}, block: [][2]string{{"--[[", "]]"}}, quotes: `"'`,
		keywords: words(`and break do else elseif end for function goto if in local not or repeat return then until while`),
		builtins: words(`true false nil print pairs ipairs require table string math`), calls: true}
	langSQL = &lang{line: []string{"--"}, block: [][2]string{{"/*", "*/"}}, quotes: `'"`, caseless: true,
		keywords: words(`select from where and or not insert into values update set delete create table drop alter add column index view join inner left right outer full on as group by order having limit offset union all distinct case when then else end is null like in between exists primary key foreign references default unique check constraint begin commit rollback transaction with returning if`),
		types:    words(`int integer bigint smallint text varchar char boolean bool date time timestamp float real double numeric decimal blob json jsonb uuid serial`),
		builtins: words(`count sum avg min max coalesce now true false`)}
	langCSS = &lang{block: [][2]string{{"/*", "*/"}}, quotes: `"'`, nameRunes: "-",
		keywords: words(`@media @import @font-face @keyframes @supports @layer !important`), sigils: "@"}
	langShellish = &lang{line: []string{"#"}, quotes: `"'`, sigils: "$"}
	langHCL      = &lang{line: []string{"#", "//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"`,
		keywords: words(`resource data variable output module provider locals terraform for in if`), builtins: words(`true false null`), calls: true}
	langProto = &lang{line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"'`,
		keywords: words(`syntax package import option message enum service rpc returns repeated optional required oneof map reserved extend stream`),
		types:    words(`double float int32 int64 uint32 uint64 sint32 sint64 fixed32 fixed64 sfixed32 sfixed64 bool string bytes`), caps: true}
	langDart = &lang{line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"'`,
		keywords: words(`abstract as assert async await break case catch class const continue default deferred do dynamic else enum export extends extension external factory final finally for get if implements import in is late library mixin new on operator part required rethrow return set show static super switch sync this throw try typedef var void while with yield`),
		types:    words(`int double num String bool List Map Set Future Stream`), builtins: words(`true false null`), calls: true, caps: true}
)

// ForName returns the highlighter for a file called name, by its name
// or the end of it, or nil for a kind of file it does not know.
func ForName(name string) Highlighter {
	base := name
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	lower := strings.ToLower(base)
	switch lower {
	case "makefile", "gnumakefile":
		return Makefile
	case "dockerfile", "containerfile":
		return Dockerfile
	case ".bashrc", ".bash_profile", ".profile", ".zshrc", ".zprofile", ".envrc", ".bash_aliases":
		return Bash
	case "gemfile", "rakefile":
		return langRuby.highlight
	case ".gitconfig", ".editorconfig", ".gitmodules":
		return INI
	case ".gitignore", ".dockerignore", ".env":
		return langShellish.highlight
	case "go.mod", "go.sum", "go.work":
		return langShellish.highlight
	}
	if strings.HasPrefix(lower, "dockerfile.") {
		return Dockerfile
	}
	ext := ""
	if i := strings.LastIndexByte(lower, '.'); i > 0 {
		ext = lower[i:]
	}
	switch ext {
	case ".go":
		return Go
	case ".sh", ".bash", ".zsh", ".ksh":
		return Bash
	case ".ps1", ".psm1", ".psd1":
		return PowerShell
	case ".bat", ".cmd":
		return Batch
	case ".c", ".h":
		return langC.highlight
	case ".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx", ".ino", ".m", ".mm":
		return langCpp.highlight
	case ".java":
		return langJava.highlight
	case ".cs":
		return langCSharp.highlight
	case ".kt", ".kts":
		return langKotlin.highlight
	case ".scala", ".sc":
		return langScala.highlight
	case ".swift":
		return langSwift.highlight
	case ".rs":
		return langRust.highlight
	case ".js", ".mjs", ".cjs", ".jsx":
		return langJS.highlight
	case ".ts", ".tsx", ".mts", ".cts":
		return langTS.highlight
	case ".py", ".pyw", ".pyi":
		return langPython.highlight
	case ".rb":
		return langRuby.highlight
	case ".php":
		return langPHP.highlight
	case ".pl", ".pm":
		return langPerl.highlight
	case ".lua":
		return langLua.highlight
	case ".sql":
		return langSQL.highlight
	case ".css", ".scss", ".less":
		return langCSS.highlight
	case ".tf", ".hcl", ".tfvars":
		return langHCL.highlight
	case ".proto":
		return langProto.highlight
	case ".dart":
		return langDart.highlight
	case ".json", ".jsonc", ".json5", ".jsonl", ".ndjson", ".geojson":
		return JSON
	case ".yml", ".yaml":
		return YAML
	case ".toml", ".ini", ".cfg", ".conf", ".properties", ".desktop", ".service", ".reg":
		return INI
	case ".xml", ".html", ".htm", ".xhtml", ".svg", ".xaml", ".csproj", ".vbproj", ".props", ".targets", ".plist", ".vue":
		return XML
	case ".diff", ".patch":
		return Diff
	case ".mk":
		return Makefile
	}
	return nil
}

// JSON highlights JSON: a member's name as a function's, values' strings,
// numbers, and true, false and null.
func JSON(src string) []Token {
	s := &source{rs: []rune(src)}
	rs, end := s.rs, len(s.rs)
	for i := 0; i < end; {
		c := rs[i]
		switch {
		case c == '"':
			e := (&lang{quotes: `"`}).str(s, i, end)
			k := String
			j := e
			for j < end && unicode.IsSpace(rs[j]) {
				j++
			}
			if j < end && rs[j] == ':' {
				k = Function
			}
			s.add(i, e, k)
			i = e
		case c == '/' && i+1 < end && (rs[i+1] == '/' || rs[i+1] == '*'):
			// JSON with comments, as .jsonc and settings files are.
			e := langJS.comment(s, i, end)
			s.add(i, e, Comment)
			i = max(e, i+1)
		case isDigit(c) || c == '-' && i+1 < end && isDigit(rs[i+1]):
			e := numberEnd(rs, i+1, end)
			s.add(i, e, Number)
			i = e
		case nameStart(c):
			e := i
			for e < end && nameRune(rs[e]) {
				e++
			}
			if w := string(rs[i:e]); w == "true" || w == "false" || w == "null" {
				s.add(i, e, Builtin)
			}
			i = e
		default:
			i++
		}
	}
	return s.out
}

// YAML highlights YAML: comments, a key before its colon, strings,
// numbers, and its words such as true and null.
func YAML(src string) []Token {
	s := &source{rs: []rune(src)}
	rs, end := s.rs, len(s.rs)
	q := &lang{quotes: `"'`, raw: `'`}
	for ls := 0; ls < end; {
		le := s.lineEnd(ls, end)
		i := ls
		for i < le && (rs[i] == ' ' || rs[i] == '\t' || rs[i] == '-') {
			i++
		}
		// A key: up to a colon before a blank or the line's end.
		if i < le && rs[i] != '#' && rs[i] != '"' && rs[i] != '\'' {
			for k := i; k < le; k++ {
				if rs[k] == '#' && k > i && blank(rs[k-1]) {
					break
				}
				if rs[k] == ':' && (k+1 == le || blank(rs[k+1])) {
					s.add(i, k, Function)
					i = k + 1
					break
				}
			}
		}
		for i < le {
			c := rs[i]
			switch {
			case c == '#' && (i == ls || blank(rs[i-1])):
				s.add(i, le, Comment)
				i = le
			case c == '"' || c == '\'':
				e := q.str(s, i, le)
				s.add(i, e, String)
				i = e
			case c == '&' || c == '*' || c == '!':
				e := i + 1
				for e < le && !blank(rs[e]) && rs[e] != ',' {
					e++
				}
				s.add(i, e, Variable)
				i = e
			default:
				e := i
				for e < le && !blank(rs[e]) && rs[e] != ',' && rs[e] != ']' && rs[e] != '}' {
					e++
				}
				if e == i {
					i++
					continue
				}
				switch w := strings.ToLower(string(rs[i:e])); {
				case w == "true" || w == "false" || w == "null" || w == "yes" || w == "no" || w == "~":
					s.add(i, e, Builtin)
				case number(strings.TrimPrefix(w, "-")):
					s.add(i, e, Number)
				}
				i = e
			}
		}
		ls = le + 1
	}
	return s.out
}

// INI highlights INI and TOML files and their like: a [section] as a
// keyword, a key before its = or :, comments, strings and numbers.
func INI(src string) []Token {
	s := &source{rs: []rune(src)}
	rs, end := s.rs, len(s.rs)
	q := &lang{quotes: `"'`, triple: true}
	for ls := 0; ls < end; {
		le := s.lineEnd(ls, end)
		i := ls
		for i < le && blank(rs[i]) {
			i++
		}
		switch {
		case i < le && (rs[i] == '#' || rs[i] == ';'):
			s.add(i, le, Comment)
		case i < le && rs[i] == '[':
			e := i
			for e < le && rs[e] != ']' {
				e++
			}
			s.add(i, min(e+1, le), Keyword)
		case i < le:
			k := i
			for k < le && rs[k] != '=' && rs[k] != ':' {
				k++
			}
			if k == le {
				break
			}
			ke := k
			for ke > i && blank(rs[ke-1]) {
				ke--
			}
			s.add(i, ke, Function)
			s.add(k, k+1, Operator)
			for j := k + 1; j < le; {
				c := rs[j]
				switch {
				case c == '"' || c == '\'':
					e := q.str(s, j, end)
					s.add(j, e, String)
					if e > le {
						le = s.lineEnd(e, end)
					}
					j = e
				case c == '#' || c == ';':
					s.add(j, le, Comment)
					j = le
				case isDigit(c) && (j == k+1 || !nameRune(rs[j-1])):
					e := numberEnd(rs, j, le)
					s.add(j, e, Number)
					j = e
				case nameStart(c):
					e := j
					for e < le && nameRune(rs[e]) {
						e++
					}
					if w := strings.ToLower(string(rs[j:e])); w == "true" || w == "false" || w == "yes" || w == "no" || w == "on" || w == "off" {
						s.add(j, e, Builtin)
					}
					j = e
				default:
					j++
				}
			}
		}
		ls = le + 1
	}
	return s.out
}

// XML highlights XML and HTML: tags' names as keywords, attributes'
// names as types and their values as strings, comments, and entities.
func XML(src string) []Token {
	s := &source{rs: []rune(src)}
	rs, end := s.rs, len(s.rs)
	q := &lang{quotes: `"'`, raw: `"'`}
	for i := 0; i < end; {
		switch {
		case s.has(i, end, "<!--"):
			e := (&lang{block: [][2]string{{"<!--", "-->"}}}).comment(s, i, end)
			s.add(i, e, Comment)
			i = e
		case s.has(i, end, "<![CDATA["):
			e := (&lang{block: [][2]string{{"<![CDATA[", "]]>"}}}).comment(s, i, end)
			s.add(i, e, String)
			i = e
		case rs[i] == '<':
			j := i + 1
			for j < end && (rs[j] == '/' || rs[j] == '?' || rs[j] == '!') {
				j++
			}
			e := j
			for e < end && (nameRune(rs[e]) || rs[e] == ':' || rs[e] == '-' || rs[e] == '.') {
				e++
			}
			s.add(i, j, Operator)
			s.add(j, e, Keyword)
			// Attributes up to the tag's end.
			for i = e; i < end && rs[i] != '>' && rs[i] != '<'; {
				c := rs[i]
				switch {
				case c == '"' || c == '\'':
					e := q.str(s, i, end)
					s.add(i, e, String)
					i = e
				case nameStart(c):
					e := i
					for e < end && (nameRune(rs[e]) || rs[e] == ':' || rs[e] == '-' || rs[e] == '.') {
						e++
					}
					s.add(i, e, Type)
					i = e
				default:
					i++
				}
			}
			if i < end && rs[i] == '>' {
				start := i
				if i > 0 && (rs[i-1] == '/' || rs[i-1] == '?') {
					start--
				}
				s.add(start, i+1, Operator)
				i++
			}
		case rs[i] == '&':
			e := i + 1
			for e < end && e-i < 12 && (nameRune(rs[e]) || rs[e] == '#') {
				e++
			}
			if e < end && rs[e] == ';' {
				s.add(i, e+1, Builtin)
				i = e + 1
				break
			}
			i++
		default:
			i++
		}
	}
	return s.out
}

// Diff highlights a diff: lines added as strings, lines taken away as
// variables, which themes colour green and red, the hunks' heads as
// keywords, and the files' heads as comments.
func Diff(src string) []Token {
	s := &source{rs: []rune(src)}
	rs, end := s.rs, len(s.rs)
	for ls := 0; ls < end; {
		le := s.lineEnd(ls, end)
		switch {
		case s.has(ls, le, "+++"), s.has(ls, le, "---"), s.has(ls, le, "diff "), s.has(ls, le, "index "):
			s.add(ls, le, Comment)
		case s.has(ls, le, "@@"):
			s.add(ls, le, Keyword)
		case ls < le && rs[ls] == '+':
			s.add(ls, le, String)
		case ls < le && rs[ls] == '-':
			s.add(ls, le, Variable)
		}
		ls = le + 1
	}
	return s.out
}

// Makefile highlights a makefile: comments, a rule's targets as
// functions, variables as $(NAME), and a variable being set.
func Makefile(src string) []Token {
	s := &source{rs: []rune(src)}
	rs, end := s.rs, len(s.rs)
	for ls := 0; ls < end; {
		le := s.lineEnd(ls, end)
		i := ls
		if i < le && rs[i] != '\t' {
			// A target before its colon, or a name before its =.
			for k := i; k < le; k++ {
				if rs[k] == '#' || rs[k] == '$' {
					break
				}
				if rs[k] == '=' || rs[k] == ':' && (k+1 >= le || rs[k+1] != '=') {
					kind := Function
					if rs[k] == '=' || k > i && strings.ContainsRune("?+:", rs[k-1]) && k+1 < le && rs[k+1] == '=' {
						kind = Variable
					}
					if rs[k] == ':' && k+1 < le && rs[k+1] == '=' {
						kind = Variable
					}
					name := strings.TrimRight(string(rs[i:k]), " \t?+:")
					if name != "" && !strings.ContainsAny(name, " \t") || kind == Function {
						s.add(i, i+len([]rune(name)), kind)
					}
					break
				}
			}
		}
		for i < le {
			switch c := rs[i]; {
			case c == '#':
				s.add(i, le, Comment)
				i = le
			case c == '$' && i+1 < le && (rs[i+1] == '(' || rs[i+1] == '{'):
				shut := ')'
				if rs[i+1] == '{' {
					shut = '}'
				}
				e := i + 2
				for e < le && rs[e] != shut {
					e++
				}
				e = min(e+1, le)
				if e > i && len(s.out) > 0 && s.out[len(s.out)-1].End > i {
					i = e
					break
				}
				s.add(i, e, Variable)
				i = e
			default:
				i++
			}
		}
		ls = le + 1
	}
	return s.out
}

// dockerWords are a Dockerfile's instructions.
var dockerWords = words(`FROM RUN CMD LABEL MAINTAINER EXPOSE ENV ADD COPY ENTRYPOINT VOLUME USER WORKDIR ARG ONBUILD STOPSIGNAL HEALTHCHECK SHELL AS`)

// Dockerfile highlights a Dockerfile: its instructions as keywords,
// comments, strings and variables.
func Dockerfile(src string) []Token {
	s := &source{rs: []rune(src)}
	rs, end := s.rs, len(s.rs)
	q := &lang{quotes: `"'`}
	for ls := 0; ls < end; {
		le := s.lineEnd(ls, end)
		i := ls
		for i < le && blank(rs[i]) {
			i++
		}
		if i < le && rs[i] == '#' {
			s.add(i, le, Comment)
			ls = le + 1
			continue
		}
		for i < le {
			c := rs[i]
			switch {
			case c == '"' || c == '\'':
				e := q.str(s, i, le)
				s.add(i, e, String)
				i = e
			case c == '$' && i+1 < le && (rs[i+1] == '{' || nameStart(rs[i+1])):
				e := i + 1
				if rs[e] == '{' {
					for e < le && rs[e] != '}' {
						e++
					}
					e = min(e+1, le)
				} else {
					for e < le && nameRune(rs[e]) {
						e++
					}
				}
				s.add(i, e, Variable)
				i = e
			case nameStart(c):
				e := i
				for e < le && nameRune(rs[e]) {
					e++
				}
				if dockerWords[strings.ToUpper(string(rs[i:e]))] && (i == ls || blank(rs[i-1])) {
					s.add(i, e, Keyword)
				}
				i = e
			default:
				i++
			}
		}
		ls = le + 1
	}
	return s.out
}
