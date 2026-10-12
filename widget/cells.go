package widget

import (
	"image/color"
	"math"
	"slices"
	"time"
	"unsafe"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// CellGrid shows characters in a grid of equal cells, each with its own
// colours and style, as a terminal does. It fills the space it is given
// and reports, through Fit, how many cells fit there.
//
// The grid keeps its cells; the application sets them a row at a time
// with SetRow, from a view's update function. Each row keeps what it
// drew, and draws again only once it changes, so a screen where one
// line changed costs one line. A screen that changes whole, as a
// scrolling one does, stays cheap: characters take their font's glyphs
// directly, placed at their cells, without the shaping a paragraph
// needs.
//
// The cursor glides to a new cell along its row, and jumps to another
// row.
type CellGrid struct {
	anim.Group
	// Faces are the faces for regular, bold, italic and bold italic
	// text, in that order. They default to Go Mono.
	Faces [4]*text.Face
	// Size is the font size in logical pixels; zero takes the theme's
	// [TextSize].
	Size float32
	// Background fills the grid where a cell's own background is
	// clear. It defaults to the theme's [Background].
	Background theme.Token[color.NRGBA]
	// Foreground colours a cell whose own colour is clear. It defaults
	// to the theme's [Ink].
	Foreground theme.Token[color.NRGBA]
	// Pointer names the pointer's shape at p, in the grid's space, in
	// place of the I-beam: a hand over a link, say. CursorInherit
	// leaves it to the nodes around the grid.
	Pointer func(p geom.Point) input.Cursor

	cols, rows int
	lines      []cellRow
	// ink is the colour the rows were drawn with for cells that name none.
	ink    color.NRGBA
	cursor Cursor
	at     *anim.Point
	// lit fades the cursor through a blink.
	lit *anim.Float

	// metrics are what the last layout measured the cells by.
	metrics cellMetrics
	// fitCols and fitRows are how many cells fit the last layout's box.
	fitCols, fitRows int
	glyphs           map[glyphKey]cellGlyph
	// boxes are the rectangles each box-drawing or block character
	// fills, by the character and the cells' size in device pixels,
	// worked out once: an animation in half blocks draws the same one
	// in every cell of every frame. marks is a row's lines under and
	// through characters, kept for the next row.
	boxes map[boxKey][]boxRect
	marks []cellFill
	// pats are the masks of the box-drawing and block characters drawn
	// in the cells' own pass, at the cells' size, and patOf each
	// character's number among them, or 0 for one drawn as rectangles.
	pats  *paint.Patterns
	patOf map[rune]uint16
	// run is what the grid drew last frame, and painted what it drew it
	// from, so a frame in which nothing of the grid changed hands the
	// drawing on rather than drawing thousands of cells again.
	run     paint.Run
	painted paintKey
}

// paintKey is everything outside the rows that goes into drawing the
// grid.
type paintKey struct {
	t       paint.Transform
	scale   float32
	box     geom.Size
	bg, ink color.NRGBA
	cursor  Cursor
	lit     float32
	at      geom.Point
	metrics cellMetrics
	// set is true in every key made, so the zero key, before the first
	// frame, matches none.
	set bool
}

// boxKey names a character drawn in code at one size.
type boxKey struct {
	r    rune
	w, h int
}

// Cell is one character cell of a [CellGrid]. The zero Cell is blank.
type Cell struct {
	// Rune is the character, and Marks the combining marks drawn over
	// it, such as an accent. Zero and a space draw nothing.
	Rune  rune
	Marks string
	// FG and BG are the character's colour and the cell's. A clear FG
	// takes the grid's Foreground, and a clear BG shows the grid's
	// Background.
	FG, BG color.NRGBA
	Style  CellStyle
	// Wide marks a character two cells wide, such as a Chinese one. It
	// covers the cell after it, whose content is left undrawn.
	Wide bool
}

// CellStyle is a set of styles for a cell's character.
type CellStyle uint8

// The styles a cell can have.
const (
	CellBold CellStyle = 1 << iota
	CellItalic
	CellUnderline
	CellStrike
)

// CursorShape is how a [CellGrid] draws its cursor.
type CursorShape uint8

// The cursor's shapes.
const (
	// CursorBlock fills the cell, and shows the character in it in the
	// cell's background colour.
	CursorBlock CursorShape = iota
	// CursorBar is a thin line at the cell's left edge.
	CursorBar
	// CursorUnderline is a thin line along the cell's bottom.
	CursorUnderline
	// CursorOutline is the cell's outline, as terminals draw the cursor
	// while their window is in the background.
	CursorOutline
)

// Cursor is where a [CellGrid] shows its cursor, and how.
type Cursor struct {
	Col, Row int
	Shape    CursorShape
	Visible  bool
	// Blinked marks the off half of a blink: the cursor fades out, and
	// back in once Blinked clears, where Visible hides it at once.
	Blinked bool
	// Color is the cursor's colour; clear takes the colour of the
	// character under it.
	Color color.NRGBA
}

// cellRow is one row of cells, and what it drew.
type cellRow struct {
	cells []Cell
	drawn rowPaint
	// paint is how the row's cells are drawn in the cells' pass, and
	// spare the slice the frame before drew from, which a new drawing
	// takes, so the frame's last ops keep theirs to compare with.
	paint, spare []paint.CellPaint
	stale        bool
}

// rowPaint is a row's drawing, in the row's own space: backgrounds,
// glyphs grouped by colour, and lines under and through characters.
type rowPaint struct {
	fills []cellFill
	runs  []glyphRun
}

type cellFill struct {
	r geom.Rect
	c color.NRGBA
}

type glyphRun struct {
	c      color.NRGBA
	glyphs []paint.Glyph
	// lo and hi are the run's left and right edges.
	lo, hi float32
}

type cellMetrics struct {
	size, scale    float32
	faces          [4]*text.Face
	w, h, ascent   float32
	line, strikeAt float32
}

type glyphKey struct {
	r     rune
	style CellStyle
}

type cellGlyph struct {
	g       paint.Glyph
	advance float32
	ok      bool
}

// NewCellGrid returns an empty grid.
func NewCellGrid() *CellGrid {
	g := &CellGrid{Background: Background, Foreground: Ink, at: anim.NewPoint(geom.Point{})}
	g.lit = anim.NewFloat(1)
	g.Add(g.at, g.lit)
	return g
}

// Resize sets how many columns and rows the grid holds, keeping the
// cells that still fit.
func (g *CellGrid) Resize(cols, rows int) {
	if cols == g.cols && rows == g.rows {
		return
	}
	lines := make([]cellRow, rows)
	for y := range lines {
		lines[y].cells = make([]Cell, cols)
		lines[y].stale = true
		if y < len(g.lines) {
			copy(lines[y].cells, g.lines[y].cells)
		}
	}
	g.cols, g.rows, g.lines = cols, rows, lines
}

// GridSize returns how many columns and rows the grid holds.
func (g *CellGrid) GridSize() (cols, rows int) { return g.cols, g.rows }

// SetRow sets row y's cells from cells, as far as the row reaches, and
// clears the rest of it. A row whose cells stay the same keeps what it
// drew.
func (g *CellGrid) SetRow(y int, cells []Cell) {
	if y < 0 || y >= g.rows {
		return
	}
	line := &g.lines[y]
	n := min(len(cells), g.cols)
	if slices.Equal(line.cells[:n], cells[:n]) && isBlank(line.cells[n:]) {
		return
	}
	copy(line.cells, cells[:n])
	clear(line.cells[n:])
	line.stale = true
}

func isBlank(cells []Cell) bool {
	for _, c := range cells {
		if c != (Cell{}) {
			return false
		}
	}
	return true
}

// SetCursor places the cursor.
func (g *CellGrid) SetCursor(c Cursor) { g.cursor = c }

// Fit returns how many columns and rows fit the space the grid was last
// laid out in.
func (g *CellGrid) Fit() (cols, rows int) { return g.fitCols, g.fitRows }

// CellSize returns the size of one cell, as last laid out.
func (g *CellGrid) CellSize() geom.Size { return geom.Sz(g.metrics.w, g.metrics.h) }

// CellAt returns the cell under p, in the grid's space, clamped to the
// grid.
func (g *CellGrid) CellAt(p geom.Point) (col, row int) {
	if g.metrics.w <= 0 || g.metrics.h <= 0 {
		return 0, 0
	}
	col = max(0, min(int(p.X/g.metrics.w), g.cols-1))
	row = max(0, min(int(p.Y/g.metrics.h), g.rows-1))
	return col, row
}

func (g *CellGrid) face(s CellStyle) *text.Face {
	i := 0
	if s&CellBold != 0 {
		i |= 1
	}
	if s&CellItalic != 0 {
		i |= 2
	}
	if g.Faces[i] != nil {
		return g.Faces[i]
	}
	if g.Faces[0] != nil {
		return g.Faces[0]
	}
	return text.GoMono(i&1 != 0, i&2 != 0)
}

// measure sets the cells' size for a font size and a display scale.
// Sizes snap to whole device pixels, so every cell's edge falls on a
// pixel and each glyph lands on the same place in its cell.
func (g *CellGrid) measure(size, scale float32) {
	if g.metrics.size == size && g.metrics.scale == scale && g.metrics.faces == g.Faces {
		return
	}
	snap := func(v float32) float32 { return float32(math.Round(float64(v*scale))) / scale }
	face := g.face(0)
	ascent, descent, gap := face.Metrics(size)
	_, advance, ok := face.Glyph('M', size)
	if !ok {
		advance = size * 0.6
	}
	m := cellMetrics{size: size, scale: scale, faces: g.Faces}
	m.w = max(snap(advance), 1/scale)
	m.ascent = snap(ascent + gap/2)
	m.h = max(snap(ascent+descent+gap), 1/scale)
	m.line = max(snap(size/14), 1/scale)
	m.strikeAt = snap(m.ascent - ascent*0.3)
	g.metrics = m
	g.glyphs = map[glyphKey]cellGlyph{}
	for y := range g.lines {
		g.lines[y].stale = true
	}
}

// Layout implements [gunim.Node]. The grid fills the space it is given,
// or, given none, the space its cells need.
func (g *CellGrid) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	size := g.Size
	if size <= 0 {
		size = TextSize.Get(f.Theme)
	}
	g.measure(size, max(f.Scale, 1))
	m := g.metrics
	own := c.Max
	if own.W <= 0 {
		own.W = float32(g.cols) * m.w
	}
	if own.H <= 0 {
		own.H = float32(g.rows) * m.h
	}
	own = c.Constrain(own)
	g.fitCols, g.fitRows = max(1, int(own.W/m.w)), max(1, int(own.H/m.h))

	// A blink fades the cursor out and back.
	lit := float32(1)
	if g.cursor.Blinked {
		lit = 0
	}
	to := geom.Pt(float32(g.cursor.Col)*m.w, float32(g.cursor.Row)*m.h)
	// A hidden cursor has nothing to show moving: it takes its place and
	// its light at once, so a program that hides it, as an animation
	// does, draws no frames for a cursor nobody sees.
	if !g.cursor.Visible {
		if g.lit.Target() != lit || g.lit.Active() {
			g.lit.Jump(lit)
		}
		if g.at.Target() != to || g.at.Active() {
			g.at.Jump(to)
		}
		return own
	}
	// A tween, which never overshoots, so the fade never flickers back.
	g.lit.Animate(lit, anim.Tween{Duration: 150 * time.Millisecond})

	// The cursor glides along its row and jumps to another.
	if to.Y == g.at.Target().Y && to != g.at.Target() {
		g.at.Animate(to, Caret.Get(f.Theme))
	} else if to.Y != g.at.Target().Y {
		g.at.Jump(to)
	}
	return own
}

// Paint implements [gunim.Node].
func (g *CellGrid) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	m := g.metrics
	bg, ink := g.Background.Get(f.Theme), g.Foreground.Get(f.Theme)
	key := paintKey{
		t: p.Transform(), scale: f.Scale, box: box, bg: bg, ink: ink,
		cursor: g.cursor, lit: g.lit.Value(), at: g.at.Value(), metrics: m, set: true,
	}
	mark := p.Mark()
	if key == g.painted && !g.staleIn(box) && p.Again(g.run) {
		g.run = p.RunFrom(mark)
		return
	}
	defer func() { g.run, g.painted = p.RunFrom(mark), key }()
	// The cells keep to the box: a grid squeezed before its program has
	// answered the new size, or one whose program keeps its own width,
	// cuts off the columns and the row that do not fit. An opaque layer
	// that clips to a rectangle clips in place, with no offscreen pass.
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: 1, Clip: true})()
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(bg))
	defer p.Push(paint.Translate(pixelSnap(p.Transform(), f.Scale)))()
	// Redraw every row when the ink changes, as it does through a theme switch
	if ink != g.ink {
		g.ink = ink
		for y := range g.lines {
			g.lines[y].stale = true
		}
	}
	for y := range g.lines {
		top := float32(y) * m.h
		if top >= box.H {
			break
		}
		line := &g.lines[y]
		if line.stale {
			line.paint, line.spare = line.spare[:0], line.paint
			line.drawn, line.paint = g.drawRow(line.cells, ink, line.drawn, line.paint)
			line.stale = false
		}
	}
	// Every row's cells first, in one pass, so the rows go to the GPU
	// together; then what is drawn over them.
	id := uintptr(unsafe.Pointer(g))
	for y := range g.lines {
		top := float32(y) * m.h
		if top >= box.H {
			break
		}
		p.Cells(geom.Pt(0, top), geom.Sz(m.w, m.h), g.lines[y].paint, g.pats, id, y, m.size, m.ascent)
	}
	for y := range g.lines {
		top := float32(y) * m.h
		if top >= box.H {
			break
		}
		g.paintRow(p, g.lines[y].drawn, top)
	}
	g.paintCursor(p, ink, bg)
}

// staleIn reports whether a row the grid draws in box has changed since
// it was last drawn.
func (g *CellGrid) staleIn(box geom.Size) bool {
	for y := range g.lines {
		if float32(y)*g.metrics.h >= box.H {
			break
		}
		if g.lines[y].stale {
			return true
		}
	}
	return false
}

// drawRow works out what a row of cells draws, reusing the room of the
// row's last drawing, was.
func (g *CellGrid) drawRow(cells []Cell, ink color.NRGBA, was rowPaint, cp []paint.CellPaint) (rowPaint, []paint.CellPaint) {
	m := g.metrics
	g.readyPatterns()
	out := rowPaint{fills: was.fills[:0], runs: was.runs[:0]}
	put := func(x0, x1, y0, y1 float32, c color.NRGBA) {
		if n := len(out.fills); n > 0 {
			last := &out.fills[n-1]
			if last.c == c && last.r.Max.X == x0 && last.r.Min.Y == y0 && last.r.Max.Y == y1 {
				last.r.Max.X = x1
				return
			}
		}
		out.fills = append(out.fills, cellFill{r: geom.Rect{Min: geom.Pt(x0, y0), Max: geom.Pt(x1, y1)}, c: c})
	}
	lines := g.marks[:0]
	for x := 0; x < len(cells); x++ {
		c := cells[x]
		span := 1
		if c.Wide {
			span = 2
		}
		x0 := float32(x) * m.w
		x1 := x0 + float32(span)*m.w
		fg := c.FG
		if fg.A == 0 {
			fg = ink
		}
		cell := paint.CellPaint{BG: c.BG, FG: fg}
		if span == 1 {
			cell.Pattern = g.pattern(c.Rune)
		}
		cp = append(cp, cell)
		if span == 2 {
			cp = append(cp, paint.CellPaint{BG: c.BG})
		}
		// A character with a pattern is drawn in the cells' pass.
		if cell.Pattern == 0 {
			if drawn, ok := g.boxDrawn(lines, c.Rune, x0, x1, fg); ok {
				lines = drawn
			} else if c.Rune != 0 && c.Rune != ' ' {
				if gly, ok := g.cellGlyph(c.Rune, c.Style, x1-x0); ok && c.Marks == "" {
					// Drawn with the cells.
					cp[len(cp)-span].Text, cp[len(cp)-span].Glyph = true, gly
				} else {
					g.place(&out, c.Rune, c.Style, fg, x0, x1)
					for _, mark := range c.Marks {
						g.place(&out, mark, c.Style, fg, x0, x1)
					}
				}
			}
		}
		if c.Style&CellUnderline != 0 {
			lines = append(lines, cellFill{r: geom.Rect{Min: geom.Pt(x0, m.ascent+m.line), Max: geom.Pt(x1, m.ascent+2*m.line)}, c: fg})
		}
		if c.Style&CellStrike != 0 {
			lines = append(lines, cellFill{r: geom.Rect{Min: geom.Pt(x0, m.strikeAt), Max: geom.Pt(x1, m.strikeAt+m.line)}, c: fg})
		}
		x += span - 1
	}
	for _, l := range lines {
		put(l.r.Min.X, l.r.Max.X, l.r.Min.Y, l.r.Max.Y, l.c)
	}
	g.marks = lines
	for len(cp) < g.cols {
		cp = append(cp, paint.CellPaint{})
	}
	return out, cp[:g.cols]
}

// readyPatterns makes new patterns when the cells have changed size.
func (g *CellGrid) readyPatterns() {
	m := g.metrics
	w, h := int(math.Round(float64(m.w*m.scale))), int(math.Round(float64(m.h*m.scale)))
	if g.pats != nil && g.pats.W == w && g.pats.H == h {
		return
	}
	g.pats = &paint.Patterns{W: w, H: h}
	g.patOf = map[rune]uint16{}
}

// pattern is the number of r's mask among the grid's patterns, made
// the first time r is met, or 0 for a character drawn otherwise.
func (g *CellGrid) pattern(r rune) uint16 {
	if r < 0x2500 || r > 0x259f {
		return 0
	}
	if n, ok := g.patOf[r]; ok {
		return n
	}
	w, h := g.pats.W, g.pats.H
	rects := boxDrawing(r, w, h)
	var n uint16
	if len(rects) > 0 && len(g.pats.Masks) < 0xffff {
		mask := make([]byte, w*h)
		for _, b := range rects {
			for y := max(b.y0, 0); y < min(b.y1, h); y++ {
				for x := max(b.x0, 0); x < min(b.x1, w); x++ {
					// Over what is there, as the rectangles drew.
					a := int(mask[y*w+x])
					mask[y*w+x] = uint8(a + int(b.alpha)*(255-a)/255)
				}
			}
		}
		g.pats.Masks = append(g.pats.Masks, mask)
		n = uint16(len(g.pats.Masks))
	}
	g.patOf[r] = n
	return n
}

// boxDrawn adds to out what a box-drawing or block character fills in
// the cells from x0 to x1, drawn to the cell rather than taken from the
// font, so lines meet across cells. It reports false, adding nothing,
// for any other character.
func (g *CellGrid) boxDrawn(out []cellFill, r rune, x0, x1 float32, c color.NRGBA) ([]cellFill, bool) {
	if r < 0x2500 || r > 0x259f {
		return out, false
	}
	m := g.metrics
	s := m.scale
	w, h := int(math.Round(float64((x1-x0)*s))), int(math.Round(float64(m.h*s)))
	key := boxKey{r, w, h}
	rects, seen := g.boxes[key]
	if !seen {
		rects = boxDrawing(r, w, h)
		if g.boxes == nil {
			g.boxes = map[boxKey][]boxRect{}
		}
		g.boxes[key] = rects
	}
	if len(rects) == 0 {
		return out, false
	}
	for _, b := range rects {
		ink := c
		ink.A = uint8(int(c.A) * int(b.alpha) / 255)
		out = append(out, cellFill{c: ink, r: geom.Rect{
			Min: geom.Pt(x0+float32(b.x0)/s, float32(b.y0)/s),
			Max: geom.Pt(x0+float32(b.x1)/s, float32(b.y1)/s),
		}})
	}
	return out, true
}

// cellGlyph returns r's glyph as a cell w wide draws it: from its left
// edge, centred where it is narrower, as a fallback font's may be.
func (g *CellGrid) cellGlyph(r rune, style CellStyle, w float32) (paint.Glyph, bool) {
	key := glyphKey{r, style & (CellBold | CellItalic)}
	cg, seen := g.glyphs[key]
	if !seen {
		cg.g, cg.advance, cg.ok = g.face(key.style).Glyph(r, g.metrics.size)
		g.glyphs[key] = cg
	}
	if !cg.ok {
		return paint.Glyph{}, false
	}
	gly := cg.g
	gly.At = geom.Pt(0, 0)
	if cg.advance > 0 && cg.advance < w {
		gly.At.X = (w - cg.advance) / 2
	}
	return gly, true
}

// place adds r's glyph to the run of its colour, centred in the cells
// from x0 to x1 when it is narrower, as a fallback font's may be.
func (g *CellGrid) place(out *rowPaint, r rune, style CellStyle, c color.NRGBA, x0, x1 float32) {
	key := glyphKey{r, style & (CellBold | CellItalic)}
	cg, seen := g.glyphs[key]
	if !seen {
		cg.g, cg.advance, cg.ok = g.face(key.style).Glyph(r, g.metrics.size)
		g.glyphs[key] = cg
	}
	if !cg.ok {
		return
	}
	at := x0
	if cg.advance > 0 && cg.advance < x1-x0 {
		at += (x1 - x0 - cg.advance) / 2
	}
	glyph := cg.g
	glyph.At = geom.Pt(at, 0)
	i := slices.IndexFunc(out.runs, func(run glyphRun) bool { return run.c == c })
	if i < 0 {
		out.runs = append(out.runs, glyphRun{c: c, lo: x0, hi: x1})
		i = len(out.runs) - 1
	}
	run := &out.runs[i]
	run.glyphs = append(run.glyphs, glyph)
	run.lo, run.hi = min(run.lo, x0), max(run.hi, x1)
}

// paintRow draws a row's drawing with its top at top.
func (g *CellGrid) paintRow(p *paint.Painter, d rowPaint, top float32) {
	m := g.metrics
	defer p.Push(paint.Translate(geom.Pt(0, top)))()
	for _, fill := range d.fills {
		p.RRect(fill.r, 0, paint.Solid(fill.c))
	}
	if len(d.runs) == 0 {
		return
	}
	defer p.Push(paint.Translate(geom.Pt(0, m.ascent)))()
	for _, run := range d.runs {
		p.Text(run.glyphs, m.size, run.c, geom.Rect{
			Min: geom.Pt(run.lo, -m.ascent),
			Max: geom.Pt(run.hi, m.h-m.ascent),
		})
	}
}

// paintCursor draws the cursor where it has glided to.
func (g *CellGrid) paintCursor(p *paint.Painter, ink, background color.NRGBA) {
	cur := g.cursor
	if !cur.Visible || cur.Row < 0 || cur.Row >= g.rows || cur.Col < 0 || cur.Col >= g.cols {
		return
	}
	lit := min(max(g.lit.Value(), 0), 1)
	if lit < 0.01 {
		return
	}
	m := g.metrics
	under := g.lines[cur.Row].cells[cur.Col]
	col := cur.Color
	if col.A == 0 {
		col = under.FG
		if col.A == 0 {
			col = ink
		}
	}
	w := m.w
	if under.Wide {
		w *= 2
	}
	at := g.at.Value()
	box := geom.Rect{Min: at, Max: geom.Pt(at.X+w, at.Y+m.h)}
	if lit < 1 {
		defer p.Layer(paint.LayerOpts{Bounds: box, Opacity: lit})()
	}
	switch cur.Shape {
	case CursorBar:
		box.Max.X = box.Min.X + 2*m.line
		p.RRect(box, 0, paint.Solid(col))
	case CursorUnderline:
		box.Min.Y = box.Max.Y - 2*m.line
		p.RRect(box, 0, paint.Solid(col))
	case CursorOutline:
		// The outline runs inside the cell, half a line in from its edge.
		half := m.line / 2
		box = geom.Rect{Min: geom.Pt(box.Min.X+half, box.Min.Y+half), Max: geom.Pt(box.Max.X-half, box.Max.Y-half)}
		p.RRectStroke(box, 0, paint.Fill{}, paint.Stroke{Width: m.line, Color: col})
	case CursorBlock:
		p.RRect(box, 0, paint.Solid(col))
		// The character under a block shows in the cell's background.
		if under.Rune == 0 || under.Rune == ' ' {
			return
		}
		bg := under.BG
		if bg.A == 0 {
			bg = background
		}
		var d rowPaint
		g.place(&d, under.Rune, under.Style, bg, 0, w)
		defer p.Push(paint.Translate(geom.Pt(at.X, at.Y+m.ascent)))()
		for _, run := range d.runs {
			p.Text(run.glyphs, m.size, run.c, geom.Rect{Min: geom.Pt(0, -m.ascent), Max: geom.Pt(w, m.h-m.ascent)})
		}
	}
}

// onPixels is the scroll offset v rounded to whole device pixels at
// scale device pixels to a logical one, for placing what scrolls. Moved
// by a whole number of pixels, everything scrolled moves alike: text,
// which the renderer puts on whole pixels, and icons and pictures, which
// it puts where they are. Moved by fractions, the text stepped on
// different frames from the pictures beside it, and the gap between
// them wavered by a pixel as the view slid.
func onPixels(v, scale float32) float32 {
	if scale <= 0 {
		return v
	}
	return float32(math.Round(float64(v*scale))) / scale
}

// pixelSnap is how far to move the grid for its corner to sit on a
// device pixel, under transform t at scale device pixels to a logical
// one. Every cell is a whole number of device pixels, so with the corner
// on one, every cell's edge is too. With the corner between pixels,
// the two cells either side of each edge would each cover part of the
// same pixel, and the background would show through the seam: a grid
// of faint lines across a picture drawn in half blocks.
//
// A grid drawn scaled, as while it zooms, keeps its place.
func pixelSnap(t paint.Transform, scale float32) geom.Point {
	if scale <= 0 || t.A != 1 || t.E != 1 || t.B != 0 || t.D != 0 {
		return geom.Point{}
	}
	snap := func(v float32) float32 { return float32(math.Round(float64(v*scale)))/scale - v }
	return geom.Pt(snap(t.C), snap(t.F))
}
