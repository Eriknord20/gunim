// Package viewer shows a file's contents for reading: code in its
// language's colours, Markdown rendered, a picture, plain text, or the
// bytes of anything else in hex. It is given the bytes, read whole or
// in part, and the file's name, and reads nothing itself, follows no
// link, loads nothing a document refers to, and runs nothing.
//
// A View is a node of its own: in a dialog over a window, or in a
// narrow preview with [Options.Compact].
package viewer

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"  // GIF pictures
	_ "image/jpeg" // JPEG pictures
	_ "image/png"  // PNG pictures
	"strings"
	"unicode/utf8"

	_ "golang.org/x/image/bmp"  // BMP pictures
	_ "golang.org/x/image/tiff" // TIFF pictures
	_ "golang.org/x/image/webp" // WebP pictures

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/markdown"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/syntax"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// Kind is how a file is shown.
type Kind uint8

// The kinds of view.
const (
	// Text is plain text, in one colour.
	Text Kind = iota
	// Code is text in a language a highlighter reads.
	Code
	// Markdown is a Markdown document, rendered.
	Markdown
	// Picture is an image.
	Picture
	// Binary is anything else, shown as its bytes in hex.
	Binary
)

// The most a view takes on.
const (
	// MostPixels is the most pixels a picture may have to be decoded,
	// which keeps a small file that claims a vast picture from taking
	// the memory.
	MostPixels = 64 << 20
	// MostSide is the most pixels a picture is shown at along a side; a
	// larger one is scaled down first.
	MostSide = 4096
	// MostHex is how many bytes of a binary file the hex shows.
	MostHex = 64 << 10
)

// Options says how a view shows its file.
type Options struct {
	// Cut says the bytes are the start of the file only, which the view
	// says.
	Cut bool
	// Line is the line to show first, counted from 1, and 0 for the
	// top.
	Line int
	// OnLink turns a click on a link in a Markdown document into an
	// intent. Unset, a link does nothing: a document may point anywhere,
	// and whoever opens one must check where first.
	OnLink func(url string) gunim.Intent
	// Compact is for a narrow pane that scrolls the view itself: no line
	// numbers, no choice of how Markdown shows, and the view as tall as
	// what it shows.
	Compact bool
}

// KindOf returns how a file called name holding data is shown: by what
// its name says, and by whether data reads as text.
func KindOf(name string, data []byte) Kind {
	if isPicture(name) {
		return Picture
	}
	if !textual(data) {
		return Binary
	}
	if isMarkdown(name) {
		return Markdown
	}
	if syntax.ForName(name) != nil {
		return Code
	}
	return Text
}

// isPicture reports whether name is that of a picture the view decodes.
func isPicture(name string) bool {
	switch ext(name) {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".tif", ".tiff", ".webp":
		return true
	}
	return false
}

// isMarkdown reports whether name is that of a Markdown document.
func isMarkdown(name string) bool {
	switch ext(name) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return true
	}
	return false
}

// ext is name's end from its last dot, in lower case.
func ext(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return strings.ToLower(name[i:])
	}
	return ""
}

// textual reports whether data reads as text: UTF-8 with no NUL byte,
// allowing a character cut in two at its end.
func textual(data []byte) bool {
	head := data[:min(len(data), 8000)]
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	for cut := 0; cut < 4 && len(data) > 0; cut++ {
		if utf8.Valid(data[:len(data)-cut]) {
			return true
		}
	}
	return len(data) == 0
}

// Readable returns data as text fit to show: one kind of line break,
// and each control character but a tab drawn as its picture, as ␛ for
// an escape, so nothing in it acts on whatever shows it.
func Readable(data []byte) string {
	s := strings.ToValidUTF8(string(data), "�")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20:
			return 0x2400 + r
		case r == 0x7f:
			return '␡'
		case r >= 0x80 && r < 0xa0:
			return '�'
		}
		return r
	}, s)
}

// Hex returns the first MostHex bytes of data as a hex dump: an offset,
// sixteen bytes, and those that print.
func Hex(data []byte) string {
	data = data[:min(len(data), MostHex)]
	var b strings.Builder
	for off := 0; off < len(data); off += 16 {
		row := data[off:min(off+16, len(data))]
		fmt.Fprintf(&b, "%08x  ", off)
		for i := range 16 {
			if i < len(row) {
				fmt.Fprintf(&b, "%02x ", row[i])
			} else {
				b.WriteString("   ")
			}
			if i == 7 {
				b.WriteByte(' ')
			}
		}
		b.WriteString(" ")
		for _, c := range row {
			if c < 0x20 || c > 0x7e {
				c = '.'
			}
			b.WriteByte(c)
		}
		b.WriteByte('\n')
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// Decode decodes a picture from data, refusing one of more than
// MostPixels, and scales it to MostSide at most along a side.
func Decode(data []byte) (*paint.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("reading the picture: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > MostPixels {
		return nil, fmt.Errorf("the picture is %d by %d pixels, more than can be shown", cfg.Width, cfg.Height)
	}
	m, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("reading the picture: %w", err)
	}
	return paint.NewImageFit(m, MostSide, MostSide), nil
}

// View shows a file.
type View struct {
	kind Kind
	// head chooses how Markdown shows, rendered or as its source, and
	// is nil where there is no choice.
	head *widget.Segmented
	// body is what shows, and bodies the views to choose between.
	body   gunim.Node
	bodies []gunim.Node
	// note says what the view leaves out, as the end of a file cut.
	note *widget.Label
	o    Options
}

// New returns a view of data, the contents of a file called name, or
// their start with o.Cut set.
func New(name string, data []byte, o Options) *View {
	v := &View{kind: KindOf(name, data), o: o}
	switch v.kind {
	case Picture:
		m, err := Decode(data)
		if err != nil {
			v.body = message(err.Error())
			break
		}
		img := widget.NewImage(m)
		img.Fit = widget.FitContain
		v.body = img
	case Markdown:
		md := markdown.New(Readable(data))
		md.OnLink = func(url string) gunim.Intent {
			if o.OnLink == nil {
				return nil
			}
			return o.OnLink(url)
		}
		if o.Compact {
			v.body = md
			break
		}
		rendered := widget.NewScroll(widget.NewPad(md))
		source := code(name, Readable(data), o)
		v.bodies = []gunim.Node{rendered, source}
		v.body = rendered
		v.head = widget.NewSegmented("Rendered", "Source")
		v.head.OnChange = func(i int, u *gunim.UI) gunim.Intent {
			v.body = v.bodies[i]
			u.Invalidate()
			return nil
		}
	case Binary:
		c := widget.NewCodeEditor()
		c.Highlight, c.Numbers, c.Label = nil, false, "The file's bytes"
		c.SetText(Hex(data), nil)
		c.SetReadOnly(true)
		v.body = c
	default:
		v.body = code(name, Readable(data), o)
	}
	switch {
	case v.kind == Binary && len(data) > MostHex:
		v.note = note(fmt.Sprintf("The first %s, in hex.", size(MostHex)))
	case v.kind == Binary:
		v.note = note("Not text: its bytes, in hex.")
	case o.Cut:
		v.note = note(fmt.Sprintf("The first %s of the file.", size(len(data))))
	}
	return v
}

// code is a read-only code editor holding text, in the colours of the
// language name says, at line o.Line.
func code(name, text string, o Options) *widget.CodeEditor {
	c := widget.NewCodeEditor()
	c.Highlight = syntax.ForName(name)
	c.Numbers = !o.Compact
	c.Label = name
	c.SetText(text, nil)
	c.SetReadOnly(true)
	return c
}

// message is a line saying why there is nothing to show.
func message(s string) *widget.Label {
	l := widget.NewLabel(s)
	l.Color = widget.Placeholder
	return l
}

// note is a faint line under the view.
func note(s string) *widget.Label {
	l := widget.NewLabel(s)
	l.Color, l.Size = widget.Placeholder, NoteSize
	return l
}

// size says n bytes the way people do.
func size(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

// NoteSize is the size of the line under a view.
var NoteSize = theme.Length("viewer.note.size", 12)

// Kind returns how the view shows its file.
func (v *View) Kind() Kind { return v.kind }

// GoTo shows line, counted from 1, of text or code, and does nothing
// for the rest.
func (v *View) GoTo(line int, u *gunim.UI) {
	if c, ok := v.body.(*widget.CodeEditor); ok && line > 0 {
		c.GoTo(line, 1, u)
	}
}

// Children implements [gunim.Composite]: every way the file can show,
// of which the one chosen is laid out and drawn.
func (v *View) Children() []gunim.Node {
	kids := []gunim.Node{}
	if v.head != nil {
		kids = append(kids, v.head)
	}
	if len(v.bodies) > 0 {
		kids = append(kids, v.bodies...)
	} else {
		kids = append(kids, v.body)
	}
	if v.note != nil {
		kids = append(kids, v.note)
	}
	return kids
}

// gap is the room between the view's parts.
const gap = 8

// Layout implements [gunim.Node]: the choice along the top, the note
// along the bottom, and what shows in the rest. Given no height, the
// view is as tall as what it shows.
func (v *View) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	w := cs.Max.W
	var head, note gunim.Child
	var hasHead, hasNote bool
	var top, bottom float32
	for k := range kids.All {
		switch k.Node() {
		case gunim.Node(v.head):
			head, hasHead = k, true
			top = k.Layout(gunim.Constraints{Max: geom.Sz(w, 0)}).H + gap
		case gunim.Node(v.note):
			note, hasNote = k, true
			bottom = k.Layout(gunim.Constraints{Max: geom.Sz(w, 0)}).H + gap
		}
	}
	bodyH := float32(0)
	for k := range kids.All {
		n := k.Node()
		if n == gunim.Node(v.head) || n == gunim.Node(v.note) {
			continue
		}
		if n != v.body {
			// Not chosen: nowhere.
			k.Layout(gunim.Tight(geom.Size{}))
			k.Place(geom.Point{})
			continue
		}
		var s geom.Size
		if cs.Max.H > 0 {
			s = k.Layout(gunim.Tight(geom.Sz(w, max(0, cs.Max.H-top-bottom))))
		} else {
			s = k.Layout(gunim.Constraints{Max: geom.Sz(w, 0)})
		}
		k.Place(geom.Pt(0, top))
		bodyH = s.H
	}
	h := top + bodyH + bottom
	if cs.Max.H > 0 {
		h = cs.Max.H
	}
	if hasHead {
		head.Place(geom.Pt(max(0, (w-head.Size().W)/2), 0))
	}
	if hasNote {
		note.Place(geom.Pt(0, h-bottom+gap))
	}
	return geom.Sz(w, h)
}

// Paint implements [gunim.Node]: what is chosen, and the parts round it.
func (v *View) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		if n := k.Node(); len(v.bodies) > 0 && n != v.body && n != gunim.Node(v.head) && n != gunim.Node(v.note) {
			continue
		}
		k.Paint(p)
	}
}
