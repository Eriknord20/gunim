package syntax

import (
	"math/rand"
	"testing"
)

func TestBashColoursWhatTheShellReads(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"cd ~/src && ls -la | grep foo > out.txt 2>&1",
			"cd=builtin &&=operator ls=function |=operator grep=function >=operator 2=number >&=operator 1=number"},
		{"export PATH=\"$HOME/bin:$PATH\"",
			"export=builtin PATH=variable ==operator \"=string $HOME=variable /bin:=string $PATH=variable \"=string"},
		{"FOO=1 make -j4 # build",
			"FOO=variable ==operator make=function # build=comment"},
		{"for f in *.go; do echo \"${f%.go}\" $(basename \"$f\"); done",
			"for=keyword f=variable in=keyword do=keyword echo=builtin \"=string ${f%.go}=variable \"=string " +
				"$(=operator basename=function \"=string $f=variable \"=string )=operator done=keyword"},
		{"if [[ -f x ]]; then sudo -E apt install -y git; fi",
			"if=keyword [[=keyword ]]=keyword then=keyword sudo=function apt=function fi=keyword"},
		{"case $1 in\n  start|go) run ;;\n  *) echo no ;;\nesac",
			"case=keyword $1=variable in=keyword |=operator run=function ;;=operator echo=builtin ;;=operator esac=keyword"},
		{"arr=(a b c); echo ${#arr[@]} $((n + 1)) 'it'",
			"arr=variable ==operator echo=builtin ${#arr[@]}=variable $((=operator n=variable +=operator 1=number " +
				"))=operator 'it'=string"},
		{"foo() { echo hi; }", "foo=function echo=builtin"},
		{"git commit -m \"at `date`\"",
			"git=function \"at =string `=operator date=function `=operator \"=string"},
	} {
		if got := kinds(c.src, Bash(c.src)); got != c.want {
			t.Errorf("%q\n got  %s\n want %s", c.src, got, c.want)
		}
	}
}

func TestBashReadsHereDocuments(t *testing.T) {
	src := "cat <<EOF > conf\nname=$USER\nEOF\necho done\ncat <<'X'\n$HOME\nX\nls"
	got := kinds(src, Bash(src))
	want := "cat=function <<=operator EOF=string >=operator name==string $USER=variable \n=string EOF=string " +
		"echo=builtin cat=function <<=operator 'X'=string $HOME\n=string X=string ls=function"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestPowerShellColoursWhatItReads(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"Get-ChildItem -Path $env:USERPROFILE -Recurse | Where-Object { $_.Length -GT 1MB }",
			"Get-ChildItem=function $env:USERPROFILE=variable |=operator Where-Object=function $_=variable -GT=operator 1MB=number"},
		{"$x = [int]\"5\" + 0x1F; if ($x -eq $null) { Write-Host \"x is $($x * 2)\" } else { exit 1 }",
			"$x=variable ==operator int=type \"5\"=string +=operator 0x1F=number if=keyword $x=variable -eq=operator " +
				"$null=builtin Write-Host=function \"x is =string $(=operator $x=variable *=operator 2=number )=operator " +
				"\"=string else=keyword exit=keyword 1=number"},
		{"function Get-Thing([Parameter(Mandatory)][string[]]$Name) {\n  # note\n  return $Name.ToUpper()\n}",
			"function=keyword Get-Thing=function Parameter=type string[]=type $Name=variable # note=comment " +
				"return=keyword $Name=variable ToUpper=function"},
		{"$s = @'\nit's $raw\n'@\n[Math]::Round(1.5)",
			"$s=variable ==operator @'\nit's $raw\n'@=string Math=type Round=function 1.5=number"},
		{"& 'C:\\Program Files\\x.exe' --flag; .\\build.ps1 -Release; 1..10 | % { $_ }",
			"&=operator 'C:\\Program Files\\x.exe'=string .\\build.ps1=function 1=number ..=operator 10=number " +
				"|=operator %=function $_=variable"},
		{"<# block #> $list += 'it''s'; $True; 7z x a.zip",
			"<# block #>=comment $list=variable +==operator 'it''s'=string $True=builtin 7z=function"},
	} {
		if got := kinds(c.src, PowerShell(c.src)); got != c.want {
			t.Errorf("%q\n got  %s\n want %s", c.src, got, c.want)
		}
	}
}

func TestBatchColoursWhatCmdReads(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"@echo off\nset PATH=%PATH%;C:\\bin",
			"@=operator echo=builtin set=builtin PATH=variable ==operator %PATH%=variable"},
		{"if \"%1\"==\"\" goto :usage\nIF NOT EXIST \"%~dp0out\" mkdir \"%~dp0out\"",
			"if=keyword \"=string %1=variable \"=string ===operator \"\"=string goto=keyword :usage=function " +
				"IF=keyword NOT=keyword EXIST=keyword \"=string %~dp0=variable out\"=string mkdir=builtin " +
				"\"=string %~dp0=variable out\"=string"},
		{"for /f \"tokens=*\" %%a in ('dir /b') do echo %%a & echo !count!",
			"for=keyword \"tokens=*\"=string %%a=variable in=keyword do=keyword echo=builtin %%a=variable " +
				"&=operator echo=builtin !count!=variable"},
		{"for %i in (*.txt) do type %i > nul 2>&1",
			"for=keyword %i=variable in=keyword do=keyword type=builtin %i=variable >=operator 2=number >&=operator 1=number"},
		{":usage\nrem show help\n:: a comment\necho.Usage: x ^& y\nif %errorlevel% neq 0 (exit /b 1) else (echo ok)",
			":usage=function rem show help=comment :: a comment=comment echo=builtin if=keyword %errorlevel%=variable " +
				"neq=operator 0=number exit=builtin 1=number else=keyword echo=builtin"},
		{"set \"name=hello world\" && git commit -m \"%name:world=you%\"",
			"set=builtin \"=string name=variable ==operator hello world\"=string &&=operator git=function " +
				"\"=string %name:world=you%=variable \"=string"},
	} {
		if got := kinds(c.src, Batch(c.src)); got != c.want {
			t.Errorf("%q\n got  %s\n want %s", c.src, got, c.want)
		}
	}
}

// TestShellsCopeWithAnything checks every highlighter's tokens stay in
// order, apart and inside the source, whatever it is given: each prefix
// of real code, as it is typed, and random runs of the characters the
// languages give meaning to.
func TestShellsCopeWithAnything(t *testing.T) {
	samples := []string{
		"for f in *.go; do echo \"${f%.go}\" $(basename \"$f\" `x`); done\ncat <<-EOF\n\tbody $x\n\tEOF\n",
		"case $1 in (a|b) x=$((1+2)) ;; esac; arr=(1 2) <(ls) &>> log",
		"$s = @\"\nhi $($x.Name)\n\"@; [List[string]]::new() | % { $_ -replace 'a','b' } <# c #>",
		"if \"%~1\"==\"\" (for /f %%i in ('x') do set \"v=%%~nxi\") else echo !v! %v:~0,2% ^\n& rem",
	}
	all := []Highlighter{Bash, PowerShell, Batch, Go}
	check := func(h Highlighter, src string) {
		n := len([]rune(src))
		end := 0
		for _, tk := range h(src) {
			if tk.Start < end || tk.End <= tk.Start || tk.End > n || tk.Kind == Plain {
				t.Fatalf("source %q: token %+v after %d of %d", src, tk, end, n)
			}
			end = tk.End
		}
	}
	for _, s := range samples {
		rs := []rune(s)
		for i := range len(rs) + 1 {
			for _, h := range all {
				check(h, string(rs[:i]))
			}
		}
	}
	const alphabet = "ab1 \t\n$%!{}()[]<>|&;'\"`\\#@=-+.:~^*,/éx"
	r := rand.New(rand.NewSource(1))
	for range 20000 {
		rs := make([]rune, r.Intn(24))
		for i := range rs {
			rs[i] = []rune(alphabet)[r.Intn(len([]rune(alphabet)))]
		}
		for _, h := range all {
			check(h, string(rs))
		}
	}
}
