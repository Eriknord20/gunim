package filemanager

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/input"

	fileview "github.com/marrasen/gunim/viewer"
)

// Space on a file that is no picture shows it in the text viewer, in
// its language's colours; Escape closes it and gives the keyboard back
// to the listing.
func TestSpaceShowsAFileInTheTextViewer(t *testing.T) {
	h := newDndHarness(t, "main.go", "sub/")
	h.choose("main.go")
	h.do(Command{Name: CmdViewer})
	var r *reading
	h.until("the file shows", func() bool {
		r, _ = h.focused().(*reading)
		return r != nil && !r.state.Loading && r.view != nil
	})
	if r.state.Name != "main.go" || string(r.state.Data) != "dir/main.go" && !strings.HasSuffix(string(r.state.Data), "main.go") {
		t.Fatalf("the viewer shows %q holding %q", r.state.Name, r.state.Data)
	}
	if r.view.Kind() != fileview.Code {
		t.Fatalf("a Go file shows as kind %d", r.view.Kind())
	}
	h.w.Input(input.KeyPress{Key: input.KeyEscape})
	h.until("the viewer closes", func() bool { return !h.a.reading.open })

	// Space on a folder opens it.
	h.choose("sub")
	h.do(Command{Name: CmdViewer})
	h.until("the folder opens", func() bool { return strings.HasSuffix(h.a.nav.path, "sub") })
	if h.a.reading.open {
		t.Fatal("Space on a folder opened the text viewer")
	}
}

// Only a file is read: not a pipe, which could make the read wait for
// ever. More than the most is cut, and said to be.
func TestTheTextViewerReadsFilesAlone(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	data, cut, err := readStart(LocalFS(), big, 40)
	if err != nil || len(data) != 40 || !cut {
		t.Fatalf("a large file read %d bytes, cut %v, %v", len(data), cut, err)
	}
	data, cut, err = readStart(LocalFS(), big, 100)
	if err != nil || len(data) != 100 || cut {
		t.Fatalf("a file of the most read %d bytes, cut %v, %v", len(data), cut, err)
	}
}

// A link in a document opens when it is a web address, and nothing
// else does: a scheme could start a program.
func TestOnlyWebLinksOpenFromADocument(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	h := newHarnessWith(t, func(o *Options) {
		o.Log = func(l string) {
			mu.Lock()
			lines = append(lines, l)
			mu.Unlock()
		}
	}, "a.txt")
	urls := []string{"file:///etc/passwd", "ms-msdt:/id", "javascript:alert(1)", "C:\\Windows\\x.exe"}
	for _, url := range urls {
		h.do(ReadingLink{URL: url})
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != len(urls) {
		t.Fatalf("the log has %q", lines)
	}
	for i, l := range lines {
		if !strings.Contains(l, "Only web links open from here") || !strings.Contains(l, urls[i]) {
			t.Fatalf("for %q the log says %q", urls[i], l)
		}
	}
}

// The preview shows the start of a file in its language's colours, and
// Markdown rendered.
func TestThePreviewColoursCodeAndRendersMarkdown(t *testing.T) {
	code := newPreviewPage(Preview{Seq: 1, Title: "main.go", Path: "/x/main.go", Text: "package main\n"}, nil)
	md := newPreviewPage(Preview{Seq: 1, Title: "README.md", Path: "/x/README.md", Text: "# Hi\n"}, nil)
	kinds := func(pg *previewPage) []fileview.Kind {
		var out []fileview.Kind
		var walk func(n gunim.Node)
		walk = func(n gunim.Node) {
			switch n := n.(type) {
			case *fileview.View:
				out = append(out, n.Kind())
			case *textBox:
				walk(n.child)
			case gunim.Composite:
				for _, k := range n.Children() {
					walk(k)
				}
			}
		}
		walk(pg.scroll)
		return out
	}
	if got := kinds(code); len(got) != 1 || got[0] != fileview.Code {
		t.Fatalf("a Go file previews as %v", got)
	}
	if got := kinds(md); len(got) != 1 || got[0] != fileview.Markdown {
		t.Fatalf("a Markdown file previews as %v", got)
	}
}
