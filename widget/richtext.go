package widget

import (
	"image/color"
	"slices"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// RichSpan is a piece of a [RichText] in a style of its own.
type RichSpan struct {
	Text string
	// Icon shows before the text, as tall as the text and in its colour. A span with an icon and no text shows
	// the icon alone. The icon wraps with the word it touches, and is part of the span's link.
	Icon *icon.Icon
	// Face, Size and Ink default to the theme's [Font], [TextSize] and
	// [Ink], and to [LinkInk] for a link.
	Face theme.Token[*text.Face]
	Size theme.Token[float32]
	Ink  theme.Token[color.NRGBA]
	// Mark, when set, colours the span's background.
	Mark      theme.Token[color.NRGBA]
	Underline bool
	// OnClick makes the span a link. It runs on the UI goroutine when the
	// span is clicked; a non-nil result is sent as the text's intent.
	OnClick func(u *gunim.UI) gunim.Intent
}

// RichText is text in several styles, wrapped to the width it is given:
// bold within a sentence, a word in code, a link.
type RichText struct {
	Spans []RichSpan
	Align text.Align
	// LineHeight scales the lines' spacing. Zero means 1.
	LineHeight float32

	laid  text.SpanParagraph
	key   []text.Span
	width float32
	// from holds the index in Spans of each span laid out, and icon whether that span is the icon's box.
	from []int
	icon []bool
	// hover is the link under the pointer, or -1.
	hover int
	click Clicker
}

// NewRichText returns text made of spans.
func NewRichText(spans ...RichSpan) *RichText { return &RichText{Spans: spans, hover: -1} }

func (r *RichText) paragraph(th *theme.Live, width float32) text.SpanParagraph {
	spans := make([]text.Span, 0, len(r.Spans))
	r.from, r.icon = r.from[:0], r.icon[:0]
	for i, s := range r.Spans {
		size := TextSize.Get(th)
		if s.Size.Key() != "" {
			size = s.Size.Get(th)
		}
		face := faceIn(s.Face, th)
		if s.Icon != nil {
			box := size
			if s.Text != "" {
				box += IconGap.Get(th)
			}
			spans = append(spans, text.Span{Face: face, Size: size, Box: box})
			r.from, r.icon = append(r.from, i), append(r.icon, true)
		}
		if s.Text != "" || s.Icon == nil {
			spans = append(spans, text.Span{Text: s.Text, Face: face, Size: size})
			r.from, r.icon = append(r.from, i), append(r.icon, false)
		}
	}
	if !slices.Equal(spans, r.key) || width != r.width {
		r.key, r.width = spans, width
		r.laid = text.LayoutSpans(spans, text.Style{Align: r.Align, LineHeight: r.LineHeight}, width)
	}
	return r.laid
}

// Layout implements [gunim.Node].
func (r *RichText) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	return c.Constrain(r.paragraph(f.Theme, c.Max.W).Size)
}

// span returns the index in Spans of a piece laid out, whether the piece is an icon, and false for a piece of
// no span.
func (r *RichText) span(pc text.Piece) (i int, isIcon, ok bool) {
	if pc.Span < 0 || pc.Span >= len(r.from) || r.from[pc.Span] >= len(r.Spans) {
		return 0, false, false
	}
	return r.from[pc.Span], r.icon[pc.Span], true
}

// Paint implements [gunim.Node].
func (r *RichText) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	laid := r.paragraph(th, box.W)
	// Lines the clips hide are left out, with a line's room kept above
	// and below for strokes past the box.
	shown, cull := p.Visible()
	for _, l := range laid.Lines {
		if cull && (l.Top+2*l.Height < shown.Min.Y || l.Top-l.Height > shown.Max.Y) {
			continue
		}
		for _, pc := range l.Pieces {
			i, isIcon, ok := r.span(pc)
			if !ok {
				continue
			}
			s := r.Spans[i]
			ink := Ink.Get(th)
			switch {
			case s.Ink.Key() != "":
				ink = s.Ink.Get(th)
			case s.OnClick != nil:
				ink = LinkInk.Get(th)
			}
			if s.Mark.Key() != "" {
				p.RRect(geom.Rc(pc.At.X, pc.At.Y, pc.Run.Advance, pc.Run.Height()), 2, paint.Solid(s.Mark.Get(th)))
			}
			if isIcon {
				size := pc.Run.Size
				drawIcon(p, th, s.Icon, geom.Rc(pc.At.X, pc.At.Y+(pc.Run.Height()-size)/2, size, size), ink, 1)
				continue
			}
			pc.Run.Paint(p, pc.At, ink)
			if s.Underline || s.OnClick != nil && i == r.hover {
				p.RRect(geom.Rc(pc.At.X, pc.At.Y+pc.Run.Ascent+1.5, pc.Run.Advance, 1), 0, paint.Solid(ink))
			}
		}
	}
}

// linkAt returns the link span under pt, or -1.
func (r *RichText) linkAt(pt geom.Point) int {
	for _, l := range r.laid.Lines {
		for _, pc := range l.Pieces {
			if i, _, ok := r.span(pc); ok && r.Spans[i].OnClick != nil &&
				geom.Rc(pc.At.X, pc.At.Y, pc.Run.Advance, pc.Run.Height()).Contains(pt) {
				return i
			}
		}
	}
	return -1
}

// Cursor implements [gunim.CursorShaper]: a hand over a link.
func (r *RichText) Cursor(pt geom.Point) input.Cursor {
	if r.linkAt(pt) >= 0 {
		return input.CursorHand
	}
	return input.CursorArrow
}

// Handle implements [gunim.Handler]: a click on a link sends it.
func (r *RichText) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		if at := r.linkAt(e.Pos); at != r.hover {
			r.hover = at
			u.Invalidate()
		}
	case input.PointerLeave:
		if r.hover >= 0 {
			r.hover = -1
			u.Invalidate()
		}
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if at := r.linkAt(e.Pos); at >= 0 {
			r.click.Press(e, at)
			return true
		}
	case input.PointerUp:
		// A click follows the link it lets go on, the one it pressed.
		if at := r.linkAt(e.Pos); r.click.Release(e, at) {
			send(u, r, r.Spans[at].OnClick(u))
			return true
		}
	}
	return false
}

// Access implements [gunim.Accessible]: the text, read as a label. An icon shown without text, as a link, is read
// by its name.
func (r *RichText) Access() access.Info {
	var b strings.Builder
	for _, s := range r.Spans {
		if s.Text == "" && s.Icon != nil && s.OnClick != nil {
			b.WriteString(iconName(s.Icon))
			continue
		}
		b.WriteString(s.Text)
	}
	return access.Info{Role: access.RoleLabel, Name: b.String()}
}
