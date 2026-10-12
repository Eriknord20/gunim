package markdown

import (
	"cmp"
	"image/color"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// Link travels when the reader clicks a link, unless the view's OnLink says otherwise.
type Link struct{ URL string }

func init() { gunim.RegisterType[Link]("markdown.link") }

// View shows a Markdown document wrapped to its width. The reader selects its text with the mouse, across
// paragraphs, and copies it with Ctrl+C; a double click selects a word and a triple click a paragraph.
type View struct {
	// Breaks makes a single line break in a paragraph break the line, as it does in a chat.
	Breaks bool
	// Ink colours the text, and the theme's [widget.Ink] when unset.
	Ink theme.Token[color.NRGBA]
	// OnLink turns a click on a link into an intent. Unset, the view sends [Link].
	OnLink func(url string) gunim.Intent
	// Group, when set, lets a selection run on from this view into the group's others, and Key names this view
	// in it.
	Group *Group
	Key   string

	src string
	// parsed is the source and the breaks the blocks come from.
	parsed struct {
		src    string
		breaks bool
	}
	blocks []block

	// laidFor is what paras and marks were laid out for.
	laidFor struct {
		width, size float32
		parsed      struct {
			src    string
			breaks bool
		}
	}
	paras []laidPara
	marks []mark
	// reach and markReach are the lowest any paragraph and any mark reaches, up to each one. Paragraphs come top
	// first, and marks are sorted so, which with these finds those that reach a band of the view by binary search.
	reach, markReach []float32
	size             geom.Size
	// plain is the text of the paragraphs, one after another with a line break between two.
	plain []rune

	// scrolls holds how far each code block is scrolled sideways, in order; codes counts them while laying out.
	scrolls []float32
	codes   int

	caret, anchor int
	held          bool
	// origin is where the view was last drawn, in the window's space, and drawnIn the frame, for its group.
	origin  geom.Point
	drawnIn uint64
	// hover is the link under the pointer, as a paragraph and a span, or -1.
	hover [2]int
}

// laidPara is a paragraph laid out: where it sits, its lines, the spans its pieces come from, and where its text
// starts in the view's.
type laidPara struct {
	at    geom.Point
	p     text.SpanParagraph
	spans []span
	// code marks a code block's text, whose fill is the block's. Its lines do not wrap: the block scrolls
	// sideways, to scrolls[scroll] of at most over, and shows its text within clip.
	code    bool
	scroll  int
	over    float32
	clip    geom.Rect
	base, n int
}

// shift returns how far a paragraph's text sits left of where it was laid out, as its code block scrolls.
func (v *View) shift(lp laidPara) float32 {
	if !lp.code || lp.scroll >= len(v.scrolls) {
		return 0
	}
	return v.scrolls[lp.scroll]
}

// mark is what a view draws besides its text.
type mark struct {
	kind markKind
	r    geom.Rect
	run  text.Run
	done bool
}

type markKind uint8

const (
	codeBox markKind = iota
	quoteBar
	bullet
	number
	checkBox
	ruleLine
	tableHead
	tableBox
)

// New returns a view of the Markdown src.
func New(src string) *View { return &View{src: src, hover: [2]int{-1, -1}} }

// SetText replaces the document, and drops the selection. Call it from a view's update function.
func (v *View) SetText(src string) {
	if src == v.src {
		return
	}
	v.src = src
	v.caret, v.anchor = 0, 0
}

// Text returns the document's Markdown.
func (v *View) Text() string { return v.src }

// Selection returns the selected runes' range in the text the view shows, start before end. In a group, it is the
// part of the group's selection in this view.
func (v *View) Selection() (start, end int) {
	if v.Group != nil {
		return v.Group.selection(v)
	}
	n := len(v.plain)
	return min(v.caret, v.anchor, n), min(max(v.caret, v.anchor), n)
}

// SelectedText returns the selected text, without its Markdown. In a group, it is all the group's selection, a line
// break between two views.
func (v *View) SelectedText() string {
	if v.Group != nil {
		return v.Group.text()
	}
	start, end := v.Selection()
	return string(v.plain[start:end])
}

// parse parses the source, unless it already is.
func (v *View) parse() {
	if v.parsed.src == v.src && v.parsed.breaks == v.Breaks && v.blocks != nil {
		return
	}
	v.parsed.src, v.parsed.breaks = v.src, v.Breaks
	v.blocks = parse(v.src, v.Breaks)
}

// lay lays the document out w wide, unless it already is.
func (v *View) lay(th *theme.Live, w float32) {
	v.parse()
	size := widget.TextSize.Get(th)
	if v.laidFor.width == w && v.laidFor.size == size && v.laidFor.parsed == v.parsed && v.paras != nil {
		return
	}
	v.laidFor.width, v.laidFor.size, v.laidFor.parsed = w, size, v.parsed
	v.paras, v.marks, v.plain, v.codes = v.paras[:0], v.marks[:0], v.plain[:0], 0
	l := layout{v: v, th: th, size: size}
	h := l.blocks(v.blocks, 0, 0, w)
	v.size = geom.Sz(w, h)
	v.scrolls = v.scrolls[:min(len(v.scrolls), v.codes)]
	for len(v.scrolls) < v.codes {
		v.scrolls = append(v.scrolls, 0)
	}
	for _, lp := range v.paras {
		if lp.code {
			v.scrolls[lp.scroll] = min(v.scrolls[lp.scroll], lp.over)
		}
	}
	if v.paras == nil {
		v.paras = []laidPara{}
	}
	// A quote's bar or a table's box comes after what it holds: sorted by their tops, marks are found as paragraphs
	// are. Their order among themselves stays, for those drawn over each other.
	slices.SortStableFunc(v.marks, func(a, b mark) int { return cmp.Compare(a.r.Min.Y, b.r.Min.Y) })
	v.reach = reachOf(v.reach, len(v.paras), func(i int) float32 { return v.paras[i].at.Y + v.paras[i].p.Size.H })
	v.markReach = reachOf(v.markReach, len(v.marks), func(i int) float32 { return v.marks[i].r.Max.Y })
}

// reachOf returns, in out, the lowest any of n things reaches up to each one, bottom(i) being where thing i ends.
func reachOf(out []float32, n int, bottom func(i int) float32) []float32 {
	out = out[:0]
	low := float32(math.Inf(-1))
	for i := range n {
		low = max(low, bottom(i))
		out = append(out, low)
	}
	return out
}

// within returns the run of n things, from and up to to, that may reach the band from y0 to y1: those after any
// that end above it, and before any that start below it. reach is from reachOf, and top(i) where thing i starts,
// which never goes up from one to the next.
func within(reach []float32, n int, top func(i int) float32, y0, y1 float32) (from, to int) {
	from = sort.Search(n, func(i int) bool { return reach[i] >= y0 })
	to = from + sort.Search(n-from, func(i int) bool { return top(from+i) > y1 })
	return from, to
}

// parasIn returns the run of paragraphs that may reach the band from y0 to y1.
func (v *View) parasIn(y0, y1 float32) (from, to int) {
	return within(v.reach, len(v.paras), func(i int) float32 { return v.paras[i].at.Y }, y0, y1)
}

// linesIn returns the run of lp's lines that reach the band from y0 to y1, in the view's space.
func linesIn(lp laidPara, y0, y1 float32) []text.SpanLine {
	ls := lp.p.Lines
	from := sort.Search(len(ls), func(i int) bool { return lp.at.Y+ls[i].Top+ls[i].Height >= y0 })
	to := from + sort.Search(len(ls)-from, func(i int) bool { return lp.at.Y+ls[from+i].Top > y1 })
	return ls[from:to]
}

// layout lays a view's blocks out.
type layout struct {
	v    *View
	th   *theme.Live
	size float32
}

// gap is the room between blocks.
func (l layout) gap() float32 { return l.size * 0.6 }

// blocks lays bs out from y, between x and x+w, and returns where they end.
func (l layout) blocks(bs []block, x, y, w float32) float32 {
	for i, b := range bs {
		if i > 0 {
			y += l.gap()
		}
		y = l.block(b, x, y, w)
	}
	return y
}

func (l layout) block(b block, x, y, w float32) float32 {
	switch b.kind {
	case paragraph:
		return l.para(b.spans, x, y, w, l.size, 0, '\n')
	case heading:
		scale := map[int]float32{1: 1.45, 2: 1.25, 3: 1.1}[b.level]
		if scale == 0 {
			scale = 1
		}
		return l.para(b.spans, x, y, w, l.size*scale, bold, '\n')
	case code:
		pad := l.size * 0.6
		end := l.para([]span{{text: b.text, style: mono}}, x+pad, y+pad, 0, l.size, 0, '\n')
		box := geom.Rc(x, y, w, end-y+pad)
		lp := &l.v.paras[len(l.v.paras)-1]
		lp.code, lp.scroll, lp.clip = true, l.v.codes, box.Inset(geom.Insets{Left: pad / 2, Right: pad / 2})
		lp.over = max(0, lp.p.Size.W-(w-2*pad))
		l.v.codes++
		l.v.marks = append(l.v.marks, mark{kind: codeBox, r: box})
		return end + pad
	case table:
		return l.table(b, x, y, w)
	case quote:
		indent := l.size
		end := l.blocks(b.kids, x+indent, y, w-indent)
		l.v.marks = append(l.v.marks, mark{kind: quoteBar, r: geom.Rc(x+2, y, 3, end-y)})
		return end
	case list:
		return l.list(b, x, y, w)
	case rule:
		l.v.marks = append(l.v.marks, mark{kind: ruleLine, r: geom.Rc(x, y+l.size*0.4, w, 1)})
		return y + l.size*0.8
	}
	return y
}

// list lays a list out, with each item's bullet, number or box beside its first line.
func (l layout) list(b block, x, y, w float32) float32 {
	indent := l.size * 1.6
	for i, it := range b.items {
		if i > 0 {
			y += l.size * 0.25
		}
		first := len(l.v.paras)
		end := l.blocks(it.blocks, x+indent, y, w-indent)
		line := l.size * 1.3
		if first < len(l.v.paras) && len(l.v.paras[first].p.Lines) > 0 {
			line = l.v.paras[first].p.Lines[0].Height
		}
		mid := y + line/2
		switch {
		case it.task != noTask:
			s := l.size
			l.v.marks = append(l.v.marks, mark{kind: checkBox, r: geom.Rc(x+indent-s-6, mid-s/2, s, s), done: it.task == doneTask})
		case b.ordered:
			run := widget.Font.Get(l.th).Shape(strconv.Itoa(b.start+i)+".", l.size)
			l.v.marks = append(l.v.marks, mark{kind: number, r: geom.Rc(x+indent-6-run.Advance, mid-run.Height()/2, run.Advance, run.Height()), run: run})
		default:
			d := l.size * 0.36
			l.v.marks = append(l.v.marks, mark{kind: bullet, r: geom.Rc(x+indent-8-d, mid-d/2, d, d)})
		}
		y = max(end, y+line)
	}
	return y
}

// table lays a table out: each column as wide as its widest cell, narrowed where the table would run wider than w,
// the header bold on a fill, and lines between the rows and the columns.
func (l layout) table(b block, x, y, w float32) float32 {
	pad := l.size * 0.5
	cols := len(b.aligns)
	for _, row := range b.rows {
		cols = max(cols, len(row))
	}
	if cols == 0 {
		return y
	}
	widths := make([]float32, cols)
	for r, row := range b.rows {
		for c, cell := range row {
			_, ts := l.spans(cell, l.size, header(r))
			widths[c] = max(widths[c], text.LayoutSpans(ts, text.Style{}, 0).Size.W+2*pad)
		}
	}
	widths = fitColumns(widths, w, 4*pad)
	var total float32
	for _, cw := range widths {
		total += cw
	}
	top := y
	for r, row := range b.rows {
		rowTop, bottom := y, y+l.size
		cx := x
		for c := range cols {
			var cell []span
			if c < len(row) {
				cell = row[c]
			}
			sep := '\t'
			if c == 0 {
				sep = '\n'
			}
			inner := widths[c] - 2*pad
			bottom = max(bottom, l.para(cell, cx+pad, rowTop+pad, inner, l.size, header(r), sep))
			lp := &l.v.paras[len(l.v.paras)-1]
			if c < len(b.aligns) {
				switch b.aligns[c] {
				case alignCenter:
					lp.at.X += (inner - lp.p.Size.W) / 2
				case alignEnd:
					lp.at.X += inner - lp.p.Size.W
				case alignStart:
				}
			}
			cx += widths[c]
		}
		y = bottom + pad
		if r == 0 {
			l.v.marks = append(l.v.marks, mark{kind: tableHead, r: geom.Rc(x, rowTop, total, y-rowTop)})
		} else {
			l.v.marks = append(l.v.marks, mark{kind: ruleLine, r: geom.Rc(x, rowTop, total, 1)})
		}
	}
	cx := x
	for _, cw := range widths[:cols-1] {
		cx += cw
		l.v.marks = append(l.v.marks, mark{kind: ruleLine, r: geom.Rc(cx, top, 1, y-top)})
	}
	l.v.marks = append(l.v.marks, mark{kind: tableBox, r: geom.Rc(x, top, total, y-top)})
	return y
}

// header returns the style a table's row r adds to its cells: bold for the header.
func header(r int) style {
	if r == 0 {
		return bold
	}
	return 0
}

// fitColumns narrows the widest of widths, as little as it can, so they add up to no more than w, and none to
// less than least.
func fitColumns(widths []float32, w, least float32) []float32 {
	var total, widest float32
	for _, cw := range widths {
		total += cw
		widest = max(widest, cw)
	}
	if total <= w {
		return widths
	}
	// Find the cap on a column's width that brings the total to w.
	lo, hi := float32(0), widest
	for range 30 {
		mid := (lo + hi) / 2
		var sum float32
		for _, cw := range widths {
			sum += min(cw, mid)
		}
		if sum > w {
			hi = mid
		} else {
			lo = mid
		}
	}
	out := make([]float32, len(widths))
	for i, cw := range widths {
		out[i] = max(min(cw, lo), least)
	}
	return out
}

// spans returns spans with extra style on top of each one's own, and the text spans that set them in size.
func (l layout) spans(spans []span, size float32, extra style) ([]span, []text.Span) {
	laid := make([]span, len(spans))
	ts := make([]text.Span, len(spans))
	for i, s := range spans {
		s.style |= extra
		laid[i] = s
		sz := size
		if s.style&mono != 0 {
			sz = size * 0.92
		}
		ts[i] = text.Span{Text: s.text, Face: faceFor(s.style).Get(l.th), Size: sz}
	}
	return laid, ts
}

// para lays spans out as a paragraph at x, y, w wide, or unwrapped for a w of 0, in size and with extra style on
// top of each span's own. In the view's text, sep comes before it: a line break, or a tab between table cells.
func (l layout) para(spans []span, x, y, w, size float32, extra style, sep rune) float32 {
	laid, ts := l.spans(spans, size, extra)
	p := text.LayoutSpans(ts, text.Style{}, w)
	v := l.v
	base := len(v.plain)
	if len(v.paras) > 0 {
		base++
		v.plain = append(v.plain, sep)
	}
	n := 0
	for _, s := range spans {
		rs := []rune(s.text)
		v.plain = append(v.plain, rs...)
		n += len(rs)
	}
	v.paras = append(v.paras, laidPara{at: geom.Pt(x, y), p: p, spans: laid, base: base, n: n})
	return y + p.Size.H
}

// faceFor returns the face a style sets text in.
func faceFor(st style) theme.Token[*text.Face] {
	switch {
	case st&mono != 0:
		return widget.MonoFont
	case st&bold != 0 && st&italic != 0:
		return widget.BoldItalicFont
	case st&bold != 0:
		return widget.BoldFont
	case st&italic != 0:
		return widget.ItalicFont
	}
	return widget.Font
}

// Layout implements [gunim.Node].
func (v *View) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	v.lay(f.Theme, c.Max.W)
	return c.Constrain(v.size)
}

// Paint implements [gunim.Node].
func (v *View) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	v.lay(th, box.W)
	if v.Group != nil {
		v.Group.drawn(v, f.Number(), p.Transform().Apply(geom.Point{}))
	}
	ink := widget.Ink.Get(th)
	if v.Ink.Key() != "" {
		ink = v.Ink.Get(th)
	}
	// What the clips around the view let through, in its own space: a
	// paragraph, line or mark outside it is not drawn, as a long document
	// in a scroll shows a screenful of its thousands of lines. They are
	// found by binary search, so a frame costs what the screenful does.
	shown, cull := p.Visible()
	if !cull {
		shown = geom.Rect{Min: geom.Pt(0, float32(math.Inf(-1))), Max: geom.Pt(0, float32(math.Inf(1)))}
	}
	m0, m1 := within(v.markReach, len(v.marks), func(i int) float32 { return v.marks[i].r.Min.Y }, shown.Min.Y, shown.Max.Y)
	for _, m := range v.marks[m0:m1] {
		switch m.kind {
		case codeBox:
			p.RRect(m.r, 6, paint.Solid(widget.CodeFill.Get(th)))
		case tableHead:
			p.RRect(m.r, 0, paint.Solid(widget.CodeFill.Get(th)))
		default:
			// Every other mark is drawn with the text itself.
		}
	}
	start, end := v.Selection()
	sel := paint.Solid(widget.Selection.Get(th))
	from, to := v.parasIn(shown.Min.Y, shown.Max.Y)
	for i := from; i < to; i++ {
		lp := v.paras[i]
		if r := (geom.Rect{Min: lp.at, Max: lp.at.Add(lp.p.Size.Point())}); r.Max.Y < shown.Min.Y || r.Min.Y > shown.Max.Y {
			continue
		}
		func() {
			if lp.code {
				defer p.Layer(paint.LayerOpts{Bounds: lp.clip, Opacity: 1, Clip: true})()
				lp.at.X -= v.shift(lp)
			}
			if start != end && end >= lp.base && start <= lp.base+lp.n {
				lp.p.Select(start-lp.base, end-lp.base, func(r geom.Rect) { p.RRect(r.Add(lp.at), 3, sel) })
			}
			for _, l := range linesIn(lp, shown.Min.Y, shown.Max.Y) {
				if !lp.code {
					v.paintCodeFills(p, th, lp, l.Pieces)
				}
				for _, pc := range l.Pieces {
					v.paintPiece(p, th, lp, i, pc, ink)
				}
			}
		}()
		if lp.over > 0 {
			v.paintScrollBar(p, th, lp)
		}
	}
	for _, m := range v.marks[m0:m1] {
		v.paintMark(p, th, m, ink)
	}
}

// paintScrollBar draws a thin bar along the bottom of a code block that scrolls sideways, showing how far it is.
func (v *View) paintScrollBar(p *paint.Painter, th *theme.Live, lp laidPara) {
	track := geom.Rc(lp.clip.Min.X, lp.clip.Max.Y-5, lp.clip.Size().W, 3)
	content := lp.clip.Size().W + lp.over
	w := max(track.Size().W*track.Size().W/content, 24)
	x := track.Min.X + (track.Size().W-w)*v.shift(lp)/lp.over
	p.RRect(geom.Rc(x, track.Min.Y, w, track.Size().H), 1.5, paint.Solid(widget.QuoteBar.Get(th)))
}

// paintCodeFills draws the fill behind the inline code on a line of pieces, one for each run of pieces of a code span.
func (v *View) paintCodeFills(p *paint.Painter, th *theme.Live, lp laidPara, pieces []text.Piece) {
	for i := 0; i < len(pieces); {
		pc := pieces[i]
		j := i + 1
		for j < len(pieces) && pieces[j].Span == pc.Span {
			j++
		}
		if lp.spans[pc.Span].style&mono != 0 {
			last := pieces[j-1]
			x0, x1 := pc.At.X+lp.at.X, last.At.X+last.Run.Advance+lp.at.X
			p.RRect(geom.Rc(x0-2, pc.At.Y+lp.at.Y, x1-x0+4, pc.Run.Height()), 3, paint.Solid(widget.CodeFill.Get(th)))
		}
		i = j
	}
}

// paintPiece draws one piece of paragraph i's text.
func (v *View) paintPiece(p *paint.Painter, th *theme.Live, lp laidPara, i int, pc text.Piece, ink color.NRGBA) {
	s := lp.spans[pc.Span]
	at := pc.At.Add(lp.at)
	box := geom.Rc(at.X, at.Y, pc.Run.Advance, pc.Run.Height())
	c := ink
	if s.url != "" {
		c = widget.LinkInk.Get(th)
	}
	pc.Run.Paint(p, at, c)
	if s.style&strike != 0 {
		p.RRect(geom.Rc(box.Min.X, at.Y+pc.Run.Ascent*0.62, box.Size().W, 1), 0, paint.Solid(c))
	}
	if s.url != "" && v.hover == [2]int{i, pc.Span} {
		p.RRect(geom.Rc(box.Min.X, at.Y+pc.Run.Ascent+1.5, box.Size().W, 1), 0, paint.Solid(c))
	}
}

// paintMark draws what goes beside the text: bullets, numbers, boxes, the bars beside quotes and rules.
func (v *View) paintMark(p *paint.Painter, th *theme.Live, m mark, ink color.NRGBA) {
	switch m.kind {
	case quoteBar:
		p.RRect(m.r, 1.5, paint.Solid(widget.QuoteBar.Get(th)))
	case ruleLine:
		p.RRect(m.r, 0, paint.Solid(widget.QuoteBar.Get(th)))
	case tableBox:
		p.RRectStroke(m.r, 4, paint.Fill{}, paint.Stroke{Width: 1, Color: widget.QuoteBar.Get(th)})
	case bullet:
		p.RRect(m.r, m.r.Size().W/2, paint.Solid(ink))
	case number:
		m.run.Paint(p, m.r.Min, ink)
	case checkBox:
		radius := m.r.Size().W * 0.25
		if !m.done {
			p.RRectStroke(m.r.Inset(geom.Uniform(0.75)), radius, paint.Fill{}, paint.Stroke{Width: 1.5, Color: widget.FieldBorder.Get(th)})
			return
		}
		p.RRect(m.r, radius, paint.Solid(widget.Accent.Get(th)))
		tick := widget.BoldFont.Get(th).Shape("✓", m.r.Size().H*0.85)
		c := m.r.Center()
		tick.Paint(p, geom.Pt(c.X-tick.Advance/2, c.Y-tick.Height()/2), widget.ButtonStrongInk.Get(th))
	case codeBox, tableHead:
	}
}

// index returns the rune of the view's text a caret put at pt sits before.
func (v *View) index(pt geom.Point) int {
	// The first paragraph that ends below pt: pt is in it, or above it.
	i := sort.Search(len(v.paras), func(i int) bool { return v.reach[i] > pt.Y })
	if i == len(v.paras) {
		return len(v.plain)
	}
	lp := v.paras[i]
	if pt.Y < lp.at.Y {
		return lp.base
	}
	// A table's cells sit side by side: the caret goes in the cell pt is over.
	if next, ok := v.cellRight(i, pt); ok {
		return next
	}
	return lp.base + lp.p.Index(pt.Sub(lp.at).Add(geom.Pt(v.shift(lp), 0)))
}

// cellRight returns where a caret at pt goes when pt is right of paragraph i, a table cell, over a later cell of
// its row: one of the paragraphs after it at the same height.
func (v *View) cellRight(i int, pt geom.Point) (int, bool) {
	lp := v.paras[i]
	best, found := laidPara{}, false
	for _, q := range v.paras[i+1:] {
		if q.at.Y != lp.at.Y {
			break
		}
		if pt.X >= q.at.X && (!found || q.at.X > best.at.X) {
			best, found = q, true
		}
	}
	if !found {
		return 0, false
	}
	return best.base + best.p.Index(pt.Sub(best.at)), true
}

// linkAt returns the link under pt, as a paragraph and a span, or -1s.
func (v *View) linkAt(pt geom.Point) [2]int {
	from, to := v.parasIn(pt.Y, pt.Y)
	for i := from; i < to; i++ {
		lp := v.paras[i]
		for _, l := range linesIn(lp, pt.Y, pt.Y) {
			for _, pc := range l.Pieces {
				if lp.spans[pc.Span].url == "" {
					continue
				}
				at := pc.At.Add(lp.at).Sub(geom.Pt(v.shift(lp), 0))
				if geom.Rc(at.X, at.Y, pc.Run.Advance, pc.Run.Height()).Contains(pt) {
					return [2]int{i, pc.Span}
				}
			}
		}
	}
	return [2]int{-1, -1}
}

// Focusable implements [gunim.Focusable]: a click gives the view the keyboard, so Ctrl+C reaches it.
func (v *View) Focusable() bool { return true }

// SkipsTab implements [gunim.TabSkipper].
func (v *View) SkipsTab() {}

// DragHeld implements [gunim.DragHolder], so a scroll view scrolls while a selection is dragged past its edge.
func (v *View) DragHeld() bool { return v.held }

// Cursor implements [gunim.CursorShaper]: a hand over a link, and a text cursor over text.
func (v *View) Cursor(pt geom.Point) input.Cursor {
	if v.linkAt(pt)[0] >= 0 {
		return input.CursorHand
	}
	return input.CursorText
}

// Handle implements [gunim.Handler]: a click on a link sends it, the mouse selects, Ctrl+C copies and Ctrl+A
// selects everything.
func (v *View) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.FocusLost:
		v.anchor, v.held = v.caret, false
		// The view with the keyboard is where the group's selection started.
		if g := v.Group; g != nil && g.anchor.key == v.Key {
			g.on = false
		}
	case input.PointerMove:
		if at := v.linkAt(e.Pos); at != v.hover {
			v.hover = at
			u.Invalidate()
		}
		if !v.held {
			return false
		}
		v.caret = v.index(e.Pos)
		if g := v.Group; g != nil {
			if at, ok := g.at(v.origin.Add(e.Pos)); ok {
				g.caret = at
			}
		}
	case input.Scroll:
		return v.scrollCode(e, u)
	case input.PointerLeave:
		if v.hover[0] >= 0 {
			v.hover = [2]int{-1, -1}
			u.Invalidate()
		}
		return false
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if at := v.linkAt(e.Pos); at[0] >= 0 && e.Clicks < 2 && !e.Mods.Has(input.ModShift) {
			url := v.paras[at[0]].spans[at[1]].url
			var in gunim.Intent = Link{URL: url}
			if v.OnLink != nil {
				in = v.OnLink(url)
			}
			u.Send(v, in)
			return true
		}
		i := v.index(e.Pos)
		switch {
		case e.Clicks < 2 && e.Mods.Has(input.ModShift):
			v.caret = i
		case e.Clicks == 2:
			v.anchor, v.caret = wordAround(v.plain, i)
		case e.Clicks >= 3:
			v.anchor, v.caret = lineAround(v.plain, i)
		default:
			v.anchor, v.caret = i, i
		}
		v.held = true
		if g := v.Group; g != nil {
			if !g.on || e.Clicks >= 2 || !e.Mods.Has(input.ModShift) {
				g.anchor = point{v.Key, v.anchor}
			}
			g.caret, g.on = point{v.Key, v.caret}, true
		}
	case input.PointerUp:
		if !v.held {
			return false
		}
		v.held = false
	case input.KeyPress:
		if e.Typed || !e.Mods.Has(input.ModControl) && !e.Mods.Has(input.ModSuper) {
			return false
		}
		switch e.Key {
		case input.KeyC:
			if picked := v.SelectedText(); picked != "" {
				u.SetClipboard(picked)
			}
		case input.KeyA:
			v.anchor, v.caret = 0, len(v.plain)
			if g := v.Group; g != nil {
				g.anchor, g.caret, g.on = point{v.Key, 0}, point{v.Key, len(v.plain)}, true
			}
		default:
			return false
		}
	default:
		return false
	}
	u.Invalidate()
	return true
}

// scrollCode scrolls the code block under the pointer sideways, for a sideways scroll or Shift with the wheel,
// and reports false when there is none or it is at its end, for whatever scrolls outside.
func (v *View) scrollCode(e input.Scroll, u *gunim.UI) bool {
	dx := e.Delta.X
	if dx == 0 && e.Mods.Has(input.ModShift) {
		dx = e.Delta.Y
	}
	if dx == 0 {
		return false
	}
	for _, lp := range v.paras {
		if !lp.code || lp.over <= 0 || !lp.clip.Contains(e.Pos) {
			continue
		}
		at := &v.scrolls[lp.scroll]
		to := max(0, min(*at-dx, lp.over))
		if to == *at {
			return false
		}
		*at = to
		u.Invalidate()
		return true
	}
	return false
}

// wordAround returns the word at rune i of rs.
func wordAround(rs []rune, i int) (start, end int) {
	i = max(0, min(i, len(rs)))
	start, end = i, i
	for start > 0 && !unicode.IsSpace(rs[start-1]) {
		start--
	}
	for end < len(rs) && !unicode.IsSpace(rs[end]) {
		end++
	}
	return start, end
}

// lineAround returns the line, which is a paragraph or a line of one broken by hand, that holds rune i of rs.
func lineAround(rs []rune, i int) (start, end int) {
	i = max(0, min(i, len(rs)))
	start, end = i, i
	for start > 0 && rs[start-1] != '\n' {
		start--
	}
	for end < len(rs) && rs[end] != '\n' {
		end++
	}
	return start, end
}

// Access implements [gunim.Accessible]: the text, without its Markdown, read as a label.
func (v *View) Access() access.Info {
	return access.Info{Role: access.RoleLabel, Name: strings.TrimSpace(Plain(v.src))}
}
