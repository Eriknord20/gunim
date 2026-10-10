package widget

import (
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// faceIn returns t's face in th, and [Font]'s when t is the zero token.
func faceIn(t theme.Token[*text.Face], th *theme.Live) *text.Face {
	if t.Key() == "" {
		t = Font
	}
	if f := t.Get(th); f != nil {
		return f
	}
	return text.Default()
}

// shapedText holds a string's shaped form and shapes it again only
// when the string, face or size changes, so a widget can measure and
// paint its text every frame at no cost.
type shapedText struct {
	face *text.Face
	s    string
	size float32
	run  text.Run
	ok   bool
}

// shape returns s shaped in face at size logical pixels.
func (l *shapedText) shape(face *text.Face, s string, size float32) text.Run {
	if !l.ok || l.face != face || l.s != s || l.size != size {
		l.face, l.s, l.size, l.run, l.ok = face, s, size, face.Shape(s, size), true
	}
	return l.run
}

// laidText holds a string's laid-out form and lays it out again only
// when the string, face, style or width changes.
type laidText struct {
	face  *text.Face
	s     string
	style text.Style
	width float32
	p     text.Paragraph
	ok    bool
}

// layout returns s laid out in face at width.
func (pr *laidText) layout(face *text.Face, s string, st text.Style, width float32) text.Paragraph {
	if !pr.ok || pr.face != face || pr.s != s || pr.style != st || pr.width != width {
		pr.face, pr.s, pr.style, pr.width, pr.ok = face, s, st, width, true
		pr.p = face.Layout(s, st, width)
	}
	return pr.p
}

// wrap returns s laid out in face at size, wrapped at width and cut after lines lines.
func (pr *laidText) wrap(face *text.Face, s string, size, width float32, lines int) text.Paragraph {
	return pr.layout(face, s, text.Style{Size: size, MaxLines: lines}, width)
}

// CutRun returns run cut short to fit in room, ending in an ellipsis,
// or run as it is when it fits.
func CutRun(run text.Run, room float32) text.Run {
	if run.Advance <= room || run.Face == nil {
		return run
	}
	return cutRunWith(run, run.Face.Shape("…", run.Size), room)
}

// cutRunWith returns run cut to the glyphs that fit in room with ell, an
// ellipsis shaped in the same face and size, after them. Where even the
// ellipsis is wider than room, it returns run with no glyphs.
func cutRunWith(run, ell text.Run, room float32) text.Run {
	avail := room - ell.Advance
	if avail < 0 {
		run.Glyphs, run.Advance = nil, 0
		return run
	}
	n := 0
	for n < len(run.Glyphs) {
		end := run.Advance
		if n+1 < len(run.Glyphs) {
			end = run.Glyphs[n+1].At.X
		}
		if end > avail {
			break
		}
		n++
	}
	if n == len(run.Glyphs) {
		return run
	}
	pen := float32(0)
	if n > 0 {
		pen = run.Glyphs[n].At.X
	}
	out := run
	out.Glyphs = make([]paint.Glyph, 0, n+len(ell.Glyphs))
	out.Glyphs = append(out.Glyphs, run.Glyphs[:n]...)
	for _, gl := range ell.Glyphs {
		gl.At.X += pen
		out.Glyphs = append(out.Glyphs, gl)
	}
	out.Advance = pen + ell.Advance
	return out
}

// fitRun returns run whole where it fits in room, and otherwise cut with
// an ellipsis, shaped into ell in run's face and size.
func fitRun(run text.Run, ell *shapedText, room float32) text.Run {
	if run.Advance <= room || run.Face == nil {
		return run
	}
	return cutRunWith(run, ell.shape(run.Face, "…", run.Size), room)
}
