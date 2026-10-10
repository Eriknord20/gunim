package viewer

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/markdown"
	"github.com/marrasen/gunim/widget"
)

func TestAFileIsShownByItsNameAndWhatItHolds(t *testing.T) {
	for _, c := range []struct {
		name string
		data string
		want Kind
	}{
		{"main.go", "package main", Code},
		{"README.md", "# Hi", Markdown},
		{"notes.txt", "hello", Text},
		{"a.png", "whatever", Picture},
		{"x.bin", "a\x00b", Binary},
		{"bad.go", "\xff\xfe\xfd\xfc\xfb", Binary},
		{"cut.txt", "h\xc3", Text}, // a character cut in two at the end
		{"empty", "", Text},
	} {
		if got := KindOf(c.name, []byte(c.data)); got != c.want {
			t.Errorf("KindOf(%q, %q) = %d, want %d", c.name, c.data, got, c.want)
		}
	}
}

// Control characters show as their pictures, so nothing in a file acts
// on whatever shows it.
func TestTextIsMadeReadable(t *testing.T) {
	got := Readable([]byte("a\r\nb\x1b[31mc\x07\td\x7f\xc2\x9be"))
	if want := "a\nb␛[31mc␇\td␡�e"; got != want {
		t.Fatalf("Readable gave %q, want %q", got, want)
	}
}

func TestBytesShowInHex(t *testing.T) {
	got := Hex([]byte("ABC\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0cZ"))
	lines := strings.Split(got, "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "00000000  41 42 43 00 ") || !strings.HasSuffix(lines[0], " ABC.............") ||
		!strings.HasPrefix(lines[1], "00000010  5a ") || !strings.HasSuffix(lines[1], " Z") {
		t.Fatalf("the hex is\n%s", got)
	}
	if n := strings.Count(Hex(make([]byte, MostHex+100)), "\n") + 1; n != MostHex/16 {
		t.Fatalf("the hex of a large file has %d lines, want %d", n, MostHex/16)
	}
}

// A picture that says it is vast is refused before it is decoded.
func TestAVastPictureIsRefused(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	// The IHDR chunk's width and height, and its checksum, rewritten.
	binary.BigEndian.PutUint32(data[16:], 100000)
	binary.BigEndian.PutUint32(data[20:], 100000)
	binary.BigEndian.PutUint32(data[29:], crc32.ChecksumIEEE(data[12:29]))
	if _, err := Decode(data); err == nil || !strings.Contains(err.Error(), "more than can be shown") {
		t.Fatalf("a vast picture gave %v", err)
	}
	b.Reset()
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 30, 20))); err != nil {
		t.Fatal(err)
	}
	m, err := Decode(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if w, h := m.Size(); w != 30 || h != 20 {
		t.Fatalf("the picture is %d by %d", w, h)
	}
}

// Markdown shows rendered, and its source at a choice; a link does
// nothing unless the view is told what to do with one.
func TestMarkdownShowsRenderedOrAsItsSource(t *testing.T) {
	v := New("README.md", []byte("# Title\n\n[x](https://example.com)"), Options{})
	w := gunimtest.New(t, geom.Sz(600, 400), v)
	w.Frame(time.Second / 60)
	if v.head == nil || len(v.bodies) != 2 || v.body != v.bodies[0] {
		t.Fatal("the document does not show rendered, with a choice")
	}
	v.head.OnChange(1, nil)
	if _, ok := v.body.(*widget.CodeEditor); !ok {
		t.Fatalf("chosen, the source shows as %T", v.body)
	}
	if got := New("a.md", []byte("x"), Options{Compact: true}); got.head != nil {
		t.Fatal("a compact view offers a choice")
	}
	type linked struct{ url string }
	v = New("a.md", []byte("x"), Options{Compact: true, OnLink: func(url string) gunim.Intent { return linked{url} }})
	md, ok := v.body.(*markdown.View)
	if !ok || md.Text() != "x" {
		t.Fatalf("a compact document shows as %T", v.body)
	}
	if got := md.OnLink("https://example.com"); got != (linked{"https://example.com"}) {
		t.Fatalf("a link sent %#v", got)
	}
	plain, ok := New("a.md", []byte("x"), Options{Compact: true}).body.(*markdown.View)
	if !ok {
		t.Fatal("a compact document is not rendered")
	}
	if got := plain.OnLink("file:///etc/passwd"); got != nil {
		t.Fatalf("with nothing told, a link sent %#v", got)
	}
}

// A compact view is as tall as what it shows, and a full one fills its
// room, with the note on a cut file at the bottom.
func TestAViewFillsItsRoomOrIsAsTallAsItsText(t *testing.T) {
	v := New("main.go", []byte("package main\n\nfunc main() {}\n"), Options{Compact: true})
	w := gunimtest.New(t, geom.Sz(400, 600), widget.Column(v))
	w.Frame(time.Second / 60)
	if c, ok := v.body.(*widget.CodeEditor); !ok || c.Numbers {
		t.Fatal("a compact view of code numbers its lines, or shows no code")
	}
	full := New("main.go", []byte("package main"), Options{Cut: true})
	w2 := gunimtest.New(t, geom.Sz(400, 300), full)
	w2.Frame(time.Second / 60)
	if full.note == nil || !strings.Contains(full.note.Text, "first 12 bytes") {
		t.Fatal("a cut file does not say so")
	}
}
