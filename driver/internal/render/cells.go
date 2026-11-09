package render

import (
	"image/color"
	"math"
	"unsafe"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
)

// A grid of character cells is drawn by a program of its own: one quad
// over the rows, whose fragments look their cell up in a texture that
// holds each cell's background, foreground and pattern. A row costs the
// GPU twelve bytes a cell, where drawing its cells as rectangles costs
// hundreds.

// The texture units the cells program reads, past the draw program's.
const (
	cellsUnit    = gl.TEXTURE0 + 4
	patternsUnit = gl.TEXTURE0 + 5
)

// cellsWidth and cellsRows are the size of the texture rows of cells are
// uploaded into, three texels a cell, row after row through a frame.
const (
	cellsWidth = 4096
	cellsRows  = 512
)

// patternsWidth is the width of the texture of patterns, which holds
// them side by side in rows.
const patternsWidth = 2048

// cellsState is a renderer's means of drawing cells.
type cellsState struct {
	prog         program
	failed       bool
	vao, vbo     uint32
	tex          uint32
	texW         int
	row          int
	uCell, uBase int32
	uPer         int32
	// pats is the texture of patterns, the Patterns it holds, how many
	// of their masks are uploaded, and how many it has room for.
	pats     uint32
	patsOf   *paint.Patterns
	patsSent int
	patsRoom int
	patsPer  int
	pending  *paint.CellsOp
	firstRow int
	rows     int
	upload   []byte
	verts    [16]float32

	// The glyphs of the run of rows queued: table holds where each is in
	// the atlas and in its cell, two texels a glyph, uploaded after the
	// rows, and index numbers them by their keys. mode is 0 for
	// greyscale glyphs, 1 or 2 for glyphs on subpixels that run red to
	// blue or blue to red. later are the glyphs that reach outside their
	// cells, drawn as text over the cells.
	table []byte
	index map[glyphKey]uint16
	mode  int
	later []*paint.TextOp
	// placed is the row being added's glyphs, found before the row
	// joins a run.
	placed                                         []placedGlyph
	uMode, uGamma, uContrast, uTable, uTexW, uCols int32
	// inCells and spilled count the glyphs drawn each way, for tests.
	inCells, spilled int
}

// placedGlyph is where a cell's glyph is: its key and slot in the
// atlas, and its corner from the cell's in device pixels, which may lie
// in the cells either side, or fits false for one drawn as text.
type placedGlyph struct {
	key    glyphKey
	slot   glyphSlot
	dx, dy int
	fits   bool
}

const cellsVertex = `
in vec2 a_pos;
in vec2 a_px;
out vec2 v_px;
void main() {
	gl_Position = vec4(a_pos, 0.0, 1.0);
	v_px = a_px;
}
`

const cellsFragment = `
uniform sampler2D u_cells;
uniform sampler2D u_patterns;
uniform sampler2D u_atlas;
uniform sampler2D u_lcd;
uniform vec2 u_cell;
uniform int u_base;
uniform int u_per;
uniform int u_mode;
uniform vec4 u_gamma;
uniform float u_contrast;
uniform int u_table;
uniform int u_texw;
uniform int u_cols;
in vec2 v_px;

int u16(float lo, float hi) { return int(lo * 255.0 + 0.5) + 256 * int(hi * 255.0 + 0.5); }

// coverage returns the coverage of each channel of the glyph pixel at,
// for text in colour c, enhanced and corrected for gamma as the draw
// program's glyphs are.
vec3 coverage(ivec2 at, vec4 c) {
	vec4 g = u_gamma;
	float k = u_contrast;
	if (u_mode == 0) {
		float a = texelFetch(u_atlas, at, 0).r;
		k *= clamp(4.0 * (0.75 - dot(c.rgb, vec3(0.30, 0.59, 0.11))), 0.0, 1.0);
		a = a * (k + 1.0) / (a * k + 1.0);
		float f = dot(c.rgb, vec3(0.25, 0.5, 0.25));
		a = clamp(a + a * (1.0 - a) * ((g.x * f + g.y) * a + (g.z * f + g.w)), 0.0, 1.0);
		return vec3(a * c.a);
	}
	vec3 m = texelFetch(u_lcd, at, 0).rgb;
	if (u_mode == 2) {
		m = m.bgr;
	}
	m = m * (k + 1.0) / (m * k + 1.0);
	return clamp(m + m * (1.0 - m) * ((g.x * c.rgb + g.y) * m + (g.z * c.rgb + g.w)), 0.0, 1.0) * c.a;
}

void main() {
	vec2 c = floor(v_px / u_cell);
	ivec2 cell = ivec2(c);
	ivec2 inside = ivec2(clamp(floor(v_px - c * u_cell), vec2(0.0), u_cell - 1.0));
	int x = cell.x * 3;
	int y = u_base + cell.y;
	vec4 bg = texelFetch(u_cells, ivec2(x, y), 0);
	vec4 fg = texelFetch(u_cells, ivec2(x + 1, y), 0);
	vec4 pt = texelFetch(u_cells, ivec2(x + 2, y), 0);
	int id = u16(pt.r, pt.g);
	float a = 0.0;
	if (id > 0) {
		int k = id - 1;
		ivec2 cw = ivec2(u_cell);
		a = texelFetch(u_patterns, ivec2((k % u_per) * cw.x + inside.x, (k / u_per) * cw.y + inside.y), 0).r;
	}
	vec4 f = vec4(fg.rgb * fg.a, fg.a) * a;
	vec3 col = f.rgb + bg.rgb * bg.a * (1.0 - f.a);
	vec3 cover = vec3(f.a + bg.a * (1.0 - f.a));
	// The glyphs of this cell and the cells either side, which may
	// reach into it, in the order text draws them: left to right.
	for (int d = -1; d <= 1; d++) {
		int nx = cell.x + d;
		if (nx < 0 || nx >= u_cols) {
			continue;
		}
		vec4 np = texelFetch(u_cells, ivec2(nx * 3 + 2, y), 0);
		int gl = u16(np.b, np.a);
		if (gl == 0) {
			continue;
		}
		int t = (gl - 1) * 2;
		vec4 e0 = texelFetch(u_cells, ivec2(t % u_texw, u_table + t / u_texw), 0);
		vec4 e1 = texelFetch(u_cells, ivec2((t + 1) % u_texw, u_table + (t + 1) / u_texw), 0);
		ivec2 size = ivec2(int(e1.r * 255.0 + 0.5), int(e1.g * 255.0 + 0.5));
		ivec2 off = ivec2(int(e1.b * 255.0 + 0.5), int(e1.a * 255.0 + 0.5)) - 128;
		ivec2 p = inside - ivec2(d * int(u_cell.x), 0) - off;
		if (p.x >= 0 && p.y >= 0 && p.x < size.x && p.y < size.y) {
			vec4 nfg = d == 0 ? fg : texelFetch(u_cells, ivec2(nx * 3 + 1, y), 0);
			vec3 m = coverage(ivec2(u16(e0.r, e0.g), u16(e0.b, e0.a)) + p, nfg);
			col = nfg.rgb * m + col * (1.0 - m);
			cover = m + cover * (1.0 - m);
		}
	}
	float alpha = max(cover.r, max(cover.g, cover.b));
	fragColor = vec4(col, alpha);
#ifdef DUAL
	fragCover = vec4(cover, alpha);
#endif
}
`

// cellsReady builds what drawing cells needs, once, and reports whether
// it can.
func (r *Renderer) cellsReady() bool {
	s := &r.cellsState
	if s.prog.id != 0 || s.failed {
		return !s.failed
	}
	g := r.GL
	header := "#version 150\n"
	if r.isES {
		header = "#version 300 es\nprecision highp float;\nprecision highp int;\n"
	}
	fragHeader := header
	if r.dual {
		fragHeader = "#version 330\n#define DUAL\n"
		if r.isES {
			fragHeader = "#version 300 es\n#extension GL_EXT_blend_func_extended : require\nprecision highp float;\nprecision highp int;\n#define DUAL\n"
		}
	}
	v, err := compile(g, gl.VERTEX_SHADER, header+cellsVertex)
	if err != nil {
		s.failed = true
		return false
	}
	defer g.DeleteShader(v)
	f, err := compile(g, gl.FRAGMENT_SHADER, fragHeader+drawOut+cellsFragment)
	if err != nil {
		s.failed = true
		return false
	}
	defer g.DeleteShader(f)
	id := g.CreateProgram()
	g.AttachShader(id, v)
	g.AttachShader(id, f)
	g.BindAttribLocation(id, 0, "a_pos")
	g.BindAttribLocation(id, 1, "a_px")
	g.LinkProgram(id)
	if g.GetProgrami(id, gl.LINK_STATUS) == gl.FALSE {
		g.DeleteProgram(id)
		s.failed = true
		return false
	}
	s.prog = program{id: id}
	g.UseProgram(id)
	g.Uniform1i(g.GetUniformLocation(id, "u_cells"), 4)
	g.Uniform1i(g.GetUniformLocation(id, "u_patterns"), 5)
	g.Uniform1i(g.GetUniformLocation(id, "u_atlas"), 0)
	g.Uniform1i(g.GetUniformLocation(id, "u_lcd"), 2)
	s.uMode = g.GetUniformLocation(id, "u_mode")
	s.uGamma = g.GetUniformLocation(id, "u_gamma")
	s.uContrast = g.GetUniformLocation(id, "u_contrast")
	s.uTable = g.GetUniformLocation(id, "u_table")
	s.uTexW = g.GetUniformLocation(id, "u_texw")
	s.uCols = g.GetUniformLocation(id, "u_cols")
	s.uCell = g.GetUniformLocation(id, "u_cell")
	s.uBase = g.GetUniformLocation(id, "u_base")
	s.uPer = g.GetUniformLocation(id, "u_per")

	s.vao = g.CreateVertexArray()
	g.BindVertexArray(s.vao)
	s.vbo = g.CreateBuffer()
	g.BindBuffer(gl.ARRAY_BUFFER, s.vbo)
	g.EnableVertexAttribArray(0)
	g.VertexAttribPointer(0, 2, gl.FLOAT, false, 16, 0)
	g.EnableVertexAttribArray(1)
	g.VertexAttribPointer(1, 2, gl.FLOAT, false, 16, 8)
	g.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, r.ibo)
	r.bindDraw()
	return true
}

// cells queues a row of cells, drawn with the rows before it that it
// follows, or draws what is queued first.
func (r *Renderer) cells(op *paint.CellsOp) {
	if len(op.Cells) == 0 || op.Patterns == nil {
		return
	}
	cw, ch := int(math.Round(float64(op.Size.W*r.scale))), int(math.Round(float64(op.Size.H*r.scale)))
	if cw <= 0 || ch <= 0 || cw != op.Patterns.W || ch != op.Patterns.H {
		return
	}
	end := geom.Pt(op.At.X+op.Size.W*float32(len(op.Cells)), op.At.Y+op.Size.H)
	if !r.cull.Empty() && r.outside(r.deviceRect(op.Transform, geom.Rect{Min: op.At, Max: end})) {
		return
	}
	if !r.cellsReady() {
		return
	}
	s := &r.cellsState
	plain := op.Transform.A == 1 && op.Transform.B == 0 && op.Transform.D == 0 && op.Transform.E == 1
	mode := 0
	if r.subpixels && plain && r.depth == 0 {
		mode = 1
		if r.textRendering.Smoothing == text.SubpixelBGR {
			mode = 2
		}
	}
	// Found first: finding a glyph can start the atlas again, which
	// draws what is queued.
	r.placeGlyphs(op, cw, ch, plain, mode)
	// Rows join the run before them only with nothing drawn in between,
	// which would end up beneath them, and with room for their glyphs.
	if p := s.pending; p == nil || len(r.verts) > 0 || !follows(p, op, s.rows) || s.mode != mode ||
		s.firstRow+s.rows+1+s.tableRows(len(op.Cells)) > cellsRows {
		r.flush()
		r.startCells(op)
		s.mode = mode
	}
	r.addRow(op)
	s.rows++
}

// follows reports whether op is the row after the rows of p queued.
func follows(p, op *paint.CellsOp, rows int) bool {
	return p.Grid == op.Grid && op.Row == p.Row+rows && p.Patterns == op.Patterns && p.Size == op.Size &&
		p.Transform == op.Transform && p.At.X == op.At.X && len(p.Cells) == len(op.Cells)
}

// startCells begins a run of rows with op, uploading any of its
// patterns not yet on the GPU.
func (r *Renderer) startCells(op *paint.CellsOp) {
	s := &r.cellsState
	g := r.GL
	if need := len(op.Cells) * 3; need > s.texW || s.tex == 0 {
		if s.tex != 0 {
			g.DeleteTexture(s.tex)
		}
		s.texW = max(need, cellsWidth)
		s.tex = r.cellsTexture(cellsUnit, glRGBA8, gl.RGBA, s.texW, cellsRows, 4)
		s.row = 0
	}
	if s.row >= cellsRows {
		s.row = 0
	}
	if s.row+2 > cellsRows {
		s.row = 0
	}
	s.pending, s.firstRow, s.rows = op, s.row, 0
	s.upload = s.upload[:0]
	s.table = s.table[:0]
	if s.index == nil {
		s.index = map[glyphKey]uint16{}
	}
	clear(s.index)
	r.sendPatterns(op.Patterns)
}

// sendPatterns uploads the masks of pats the GPU does not have yet.
func (r *Renderer) sendPatterns(pats *paint.Patterns) {
	s := &r.cellsState
	g := r.GL
	n := len(pats.Masks)
	if s.patsOf != pats || n > s.patsRoom {
		if s.pats != 0 {
			g.DeleteTexture(s.pats)
		}
		s.patsPer = max(patternsWidth/pats.W, 1)
		s.patsRoom = max(256, 2*n)
		rows := (s.patsRoom + s.patsPer - 1) / s.patsPer
		s.pats = r.cellsTexture(patternsUnit, glR8, glRed, s.patsPer*pats.W, rows*pats.H, 1)
		s.patsOf, s.patsSent = pats, 0
	}
	if s.patsSent == n {
		return
	}
	g.ActiveTexture(patternsUnit)
	g.BindTexture(gl.TEXTURE_2D, s.pats)
	g.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	for k := s.patsSent; k < n; k++ {
		m := pats.Masks[k]
		if len(m) != pats.W*pats.H {
			continue
		}
		x, y := (k%s.patsPer)*pats.W, (k/s.patsPer)*pats.H
		g.TexSubImage2D(gl.TEXTURE_2D, 0, int32(x), int32(y), int32(pats.W), int32(pats.H), glRed, gl.UNSIGNED_BYTE, m)
	}
	g.ActiveTexture(gl.TEXTURE0)
	s.patsSent = n
}

// cellsTexture makes a texture of w by h texels on unit, read texel by
// texel.
func (r *Renderer) cellsTexture(unit uint32, internal int32, format uint32, w, h, channels int) uint32 {
	g := r.GL
	tex := g.CreateTexture()
	g.ActiveTexture(unit)
	g.BindTexture(gl.TEXTURE_2D, tex)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	g.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	g.TexImage2D(gl.TEXTURE_2D, 0, internal, int32(w), int32(h), format, gl.UNSIGNED_BYTE, make([]byte, w*h*channels))
	g.ActiveTexture(gl.TEXTURE0)
	return tex
}

// tableRows is how many rows of the texture the glyph table takes, with
// room for n more glyphs.
func (s *cellsState) tableRows(n int) int {
	if s.texW == 0 {
		return 1
	}
	return (len(s.table)/4 + 2*n + s.texW - 1) / s.texW
}

// placeGlyphs finds the glyphs of op's cells in the atlas, and where
// each lands in its cell, into placed. A glyph that reaches outside its
// cell, a colour one, or any under a transform that more than moves,
// is drawn as text.
func (r *Renderer) placeGlyphs(op *paint.CellsOp, cw, ch int, plain bool, mode int) {
	s := &r.cellsState
	for range 3 {
		epochs := [2]int{r.glyphs.epoch, r.lcdGlyphs.epoch}
		s.placed = s.placed[:0]
		raster := text.Raster{Hint: r.textRendering.Hinting == text.HintingLight, LCD: mode != 0}
		sizePx := op.TextSize * r.scale
		origin := op.Transform.Apply(op.At)
		x0, y0 := float32(math.Round(float64(origin.X*r.scale))), float32(math.Round(float64(origin.Y*r.scale)))
		var (
			face   *text.Face
			faceID uint32
		)
		for x, c := range op.Cells {
			if !c.Text {
				continue
			}
			pg := placedGlyph{}
			if gly := c.Glyph; plain && sizePx > 0 {
				if face == nil || gly.Face != faceID {
					face, _ = text.Lookup(gly.Face)
					faceID = gly.Face
				}
				if face != nil && !face.IsColor(gly.ID) {
					o := op.Transform.Apply(geom.Pt(op.At.X+float32(x)*op.Size.W+gly.At.X, op.At.Y+op.Baseline))
					ox, oy := o.X*r.scale, float32(math.Round(float64(o.Y*r.scale)))
					fx := float32(math.Floor(float64(ox)))
					sh := int(math.Round(float64(ox-fx) * subpixel))
					if sh == subpixel {
						fx, sh = fx+1, 0
					}
					pg.key = glyphKeyFor(faceID, gly.ID, sizePx, uint8(sh), raster)
					slot, ok := r.glyph(pg.key, func() text.Mask {
						return face.Rasterize(pg.key.id, sizePx, float32(pg.key.shift)/subpixel, pg.key.raster)
					})
					cx, cy := int(x0)+x*cw, int(y0)
					gx, gy := int(fx)+slot.off.X, int(oy)+slot.off.Y
					pg.slot, pg.dx, pg.dy = slot, gx-cx, gy-cy
					// Into the cells either side, which look for it, but not
					// the rows above and below, which may be drawn apart.
					pg.fits = ok && pg.dx >= -cw && pg.dx+slot.w <= 2*cw && pg.dy >= 0 && pg.dy+slot.h <= ch &&
						pg.dx >= -128 && pg.dx < 128 && pg.dy < 128 && slot.w < 256 && slot.h < 256
					if ok && slot.w == 0 {
						// Nothing to draw, as a space has.
						pg.fits, pg.slot = true, glyphSlot{}
					}
				}
			}
			s.placed = append(s.placed, pg)
		}
		if epochs == [2]int{r.glyphs.epoch, r.lcdGlyphs.epoch} {
			return
		}
		// The atlas started again part way: what was found before is gone.
	}
}

// addRow adds op's cells to the rows uploaded together when they are
// drawn, one upload a run of rows rather than one a row, with their
// glyphs from placed, and the glyphs that do not fit to later.
func (r *Renderer) addRow(op *paint.CellsOp) {
	s := &r.cellsState
	b := s.upload
	n := 0
	var spill map[color.NRGBA]*paint.TextOp
	for x, c := range op.Cells {
		var glyphAt uint16
		if c.Text {
			pg := s.placed[n]
			n++
			switch {
			case pg.fits && pg.slot.w > 0:
				i, ok := s.index[pg.key]
				if !ok {
					sl := pg.slot
					s.table = append(s.table, byte(sl.x), byte(sl.x>>8), byte(sl.y), byte(sl.y>>8),
						byte(sl.w), byte(sl.h), byte(pg.dx+128), byte(pg.dy+128))
					i = uint16(len(s.table) / 8)
					s.index[pg.key] = i
				}
				glyphAt = i
				s.inCells++
			case !pg.fits:
				s.spilled++
				if spill == nil {
					spill = map[color.NRGBA]*paint.TextOp{}
				}
				t := spill[c.FG]
				if t == nil {
					t = &paint.TextOp{Size: op.TextSize, Color: c.FG, Transform: op.Transform}
					spill[c.FG] = t
					s.later = append(s.later, t)
				}
				gly := c.Glyph
				gly.At = geom.Pt(op.At.X+float32(x)*op.Size.W+gly.At.X, op.At.Y+op.Baseline)
				t.Glyphs = append(t.Glyphs, gly)
			}
		}
		b = append(b, c.BG.R, c.BG.G, c.BG.B, c.BG.A, c.FG.R, c.FG.G, c.FG.B, c.FG.A,
			byte(c.Pattern), byte(c.Pattern>>8), byte(glyphAt), byte(glyphAt>>8))
	}
	s.upload = b
}

// flushCells draws the rows of cells queued, if any.
func (r *Renderer) flushCells() {
	s := &r.cellsState
	op := s.pending
	if op == nil || s.rows == 0 {
		s.pending = nil
		return
	}
	s.pending = nil
	g := r.GL
	g.ActiveTexture(cellsUnit)
	g.BindTexture(gl.TEXTURE_2D, s.tex)
	g.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	g.TexSubImage2D(gl.TEXTURE_2D, 0, 0, int32(s.firstRow), int32(len(op.Cells)*3), int32(s.rows), gl.RGBA, gl.UNSIGNED_BYTE, s.upload)
	tableRow := s.firstRow + s.rows
	if n := len(s.table) / 4; n > 0 {
		// The table, row after row of the texture, after the cells'.
		for at, row := 0, tableRow; at < n; at, row = at+s.texW, row+1 {
			w := min(s.texW, n-at)
			g.TexSubImage2D(gl.TEXTURE_2D, 0, 0, int32(row), int32(w), 1, gl.RGBA, gl.UNSIGNED_BYTE, s.table[at*4:(at+w)*4])
		}
	}
	if s.mode != 0 {
		g.ActiveTexture(glTexture2)
		g.BindTexture(gl.TEXTURE_2D, r.lcdGlyphs.tex)
	}
	w := op.Size.W * float32(len(op.Cells))
	h := op.Size.H * float32(s.rows)
	local := [4]geom.Point{op.At, geom.Pt(op.At.X+w, op.At.Y), geom.Pt(op.At.X+w, op.At.Y+h), geom.Pt(op.At.X, op.At.Y+h)}
	sx, sy := 2*r.scale/float32(r.fbW), 2*r.scale/float32(r.fbH)
	for i, l := range local {
		p := op.Transform.Apply(l)
		s.verts[i*4+0] = p.X*sx - 1
		s.verts[i*4+1] = 1 - p.Y*sy
		s.verts[i*4+2] = (l.X - op.At.X) * r.scale
		s.verts[i*4+3] = (l.Y - op.At.Y) * r.scale
	}
	g.UseProgram(s.prog.id)
	g.BindVertexArray(s.vao)
	g.BindBuffer(gl.ARRAY_BUFFER, s.vbo)
	g.BufferData(gl.ARRAY_BUFFER, floatBytes(s.verts[:]), gl.STREAM_DRAW)
	g.ActiveTexture(cellsUnit)
	g.BindTexture(gl.TEXTURE_2D, s.tex)
	g.ActiveTexture(patternsUnit)
	g.BindTexture(gl.TEXTURE_2D, s.pats)
	g.Uniform2fv(s.uCell, []float32{float32(op.Patterns.W), float32(op.Patterns.H)})
	g.Uniform1i(s.uBase, int32(s.firstRow))
	g.Uniform1i(s.uPer, int32(s.patsPer))
	g.Uniform1i(s.uMode, int32(s.mode))
	g.Uniform4fv(s.uGamma, r.gamma[:])
	contrast := float32(greyContrast)
	if s.mode != 0 {
		contrast = lcdContrast
	}
	g.Uniform1fv(s.uContrast, []float32{contrast})
	g.Uniform1i(s.uTable, int32(tableRow))
	g.Uniform1i(s.uTexW, int32(s.texW))
	g.Uniform1i(s.uCols, int32(len(op.Cells)))
	g.DrawElements(gl.TRIANGLES, 6, glUnsignedShort, 0)
	r.draws++
	if framesDebug {
		r.Stats.Flushes++
		r.Stats.Quads++
		r.Stats.Bytes += len(s.upload) + len(s.verts)*4
	}
	s.row = tableRow + s.tableRows(0)
	r.bindDraw()
	// The glyphs that reach outside their cells, over the cells.
	later := s.later
	s.later = nil
	for _, t := range later {
		r.text(t)
	}
}

// releaseCells frees what drawing cells made.
func (r *Renderer) releaseCells() {
	s := &r.cellsState
	g := r.GL
	if s.prog.id != 0 {
		g.DeleteProgram(s.prog.id)
		g.DeleteBuffer(s.vbo)
		g.DeleteVertexArray(s.vao)
	}
	if s.tex != 0 {
		g.DeleteTexture(s.tex)
	}
	if s.pats != 0 {
		g.DeleteTexture(s.pats)
	}
	*s = cellsState{}
}

// deviceRect is the device-pixel box b covers under t.
func (r *Renderer) deviceRect(t paint.Transform, b geom.Rect) geom.Rect {
	lo, hi := t.Apply(b.Min), t.Apply(b.Min)
	for _, c := range [...]geom.Point{{X: b.Max.X, Y: b.Min.Y}, b.Max, {X: b.Min.X, Y: b.Max.Y}} {
		p := t.Apply(c)
		lo = geom.Pt(min(lo.X, p.X), min(lo.Y, p.Y))
		hi = geom.Pt(max(hi.X, p.X), max(hi.Y, p.Y))
	}
	return geom.Rect{Min: lo.Mul(r.scale), Max: hi.Mul(r.scale)}
}

// floatBytes is fs as the bytes it is made of.
func floatBytes(fs []float32) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(&fs[0])), len(fs)*4)
}
