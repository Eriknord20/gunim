// Package text shapes strings into positioned glyphs and rasterizes
// those glyphs for a driver to draw.
//
// Shaping is the step that turns a string and a font into glyphs: it
// picks each glyph, applies kerning and ligatures, and places them.
// Layout goes further: it splits text into runs by direction, script
// and font, shapes each, and wraps the result into lines at a width.
// Both run on the UI goroutine during layout, and produce values that
// measure and paint themselves. Rasterizing turns one glyph into a
// coverage mask at one device size, and runs on a driver's render
// thread when a glyph first appears.
//
// All of it is pure Go, built on go-text/typesetting, the HarfBuzz port
// that Gio and Ebitengine also use.
package text

import (
	"bytes"
	"fmt"
	"image/color"
	"math"
	"sync"
	"unicode"

	"github.com/go-text/typesetting/bidi"
	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/math/fixed"
	"golang.org/x/text/unicode/norm"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// mu guards everything that touches go-text state: the shaper, the
// segmenter, the wrapper, and every parsed font, whose glyph caches
// fill as they are used. The UI goroutine shapes while render threads
// rasterize, and one layout can reach several fonts through fallback,
// so a single lock keeps that simple.
var (
	mu      sync.Mutex
	shaper  shaping.HarfbuzzShaper
	seg     shaping.Segmenter
	wrapper shaping.LineWrapper
	levels  bidi.Paragraph
	faces   []*Face
	byFont  = map[*font.Face]*Face{}
)

// A Face is a font, ready to shape and rasterize, with the faces it
// falls back to for characters it lacks. It is safe for concurrent use.
type Face struct {
	id   uint32
	face *font.Face
	upem float32
	// ascent, descent and gap are the font's line extents in font
	// units, all positive.
	ascent, descent, gap float32
	fallback             []*Face
	// zones are where hinted edges line up, measured on first use.
	zones    []zone
	zonesSet bool
	// colored says which glyphs draw in colours of their own, as they are asked about.
	colored map[uint32]bool
}

// Parse reads a TrueType or OpenType font.
func Parse(data []byte) (*Face, error) {
	ff, err := font.ParseTTF(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("text: %w", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return faceOf(ff), nil
}

// ParseCollection reads a font collection, a .ttc or .otc file, which
// holds several faces: the weights of a family, or the Chinese,
// Japanese and Korean cuts of one design. Windows ships most of its
// Chinese, Japanese and Korean fonts this way.
func ParseCollection(data []byte) ([]*Face, error) {
	ffs, err := font.ParseTTC(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("text: %w", err)
	}
	mu.Lock()
	defer mu.Unlock()
	out := make([]*Face, len(ffs))
	for i, ff := range ffs {
		out[i] = faceOf(ff)
	}
	return out, nil
}

// faceOf returns the Face for a parsed font, registering it on first
// sight. It runs with mu held.
func faceOf(ff *font.Face) *Face {
	if f, ok := byFont[ff]; ok {
		return f
	}
	f := &Face{face: ff, upem: float32(ff.Upem())}
	if ext, ok := ff.FontHExtents(); ok {
		f.ascent, f.descent, f.gap = ext.Ascender, -ext.Descender, ext.LineGap
	} else {
		f.ascent, f.descent = 0.8*f.upem, 0.2*f.upem
	}
	f.id = uint32(len(faces))
	faces = append(faces, f)
	byFont[ff] = f
	return f
}

// Default returns Go Regular, the face gunim uses when a widget names
// none.
func Default() *Face { return GoSans(false, false) }

var sansFaces = builtins{
	name:  "Go",
	fonts: [4][]byte{goregular.TTF, gobold.TTF, goitalic.TTF, gobolditalic.TTF},
}

// GoSans returns Go, the proportional face gunim sets text in, in the
// style asked for.
func GoSans(bold, italic bool) *Face { return sansFaces.get(bold, italic) }

// Lookup returns the face a [paint.Glyph] names. A driver calls it to
// rasterize the glyphs of a [paint.TextOp].
func Lookup(id uint32) (*Face, bool) {
	mu.Lock()
	defer mu.Unlock()
	if int(id) >= len(faces) {
		return nil, false
	}
	return faces[id], true
}

// Fallback sets the faces that draw the characters f lacks, in order of
// preference, and returns f. Go Regular covers Latin, Greek and
// Cyrillic; Hebrew, Arabic or CJK text needs a face that has them.
//
//	face := text.Default().Fallback(hebrew, arabic)
func (f *Face) Fallback(others ...*Face) *Face {
	mu.Lock()
	f.fallback = append([]*Face(nil), others...)
	mu.Unlock()
	return f
}

// fontmap resolves each character to the first face that has it, which
// is how go-text splits a run by font: the face, then its fallbacks,
// then the fonts installed on the system. It runs with mu held.
//
// A combining mark stays in its letter's face when that face has the mark, or the letter and mark as one character,
// so the shaper draws them together; last and lastFace are the letter and its face.
type fontmap struct {
	f        *Face
	last     rune
	lastFace *font.Face
}

// ResolveFace implements [shaping.Fontmap].
func (m *fontmap) ResolveFace(r rune) *font.Face {
	if m.lastFace != nil && joinsEmoji(r) {
		// The parts of an emoji sequence stay in the emoji's face, which draws the sequence as one picture.
		if _, ok := m.lastFace.NominalGlyph(r); ok {
			return m.lastFace
		}
	}
	if m.lastFace != nil && unicode.In(r, unicode.Mn, unicode.Me, unicode.Mc) {
		if _, ok := m.lastFace.NominalGlyph(r); ok {
			return m.lastFace
		}
		if c := []rune(norm.NFC.String(string([]rune{m.last, r}))); len(c) == 1 {
			if _, ok := m.lastFace.NominalGlyph(c[0]); ok {
				m.last = c[0]
				return m.lastFace
			}
		}
	}
	m.last, m.lastFace = r, m.resolve(r)
	return m.lastFace
}

// resolve is the first face that has r: for an emoji, the system's emoji font first.
func (m *fontmap) resolve(r rune) *font.Face {
	if isEmoji(r) {
		if ff := systemEmoji(r); ff != nil {
			return ff
		}
	}
	if _, ok := m.f.face.NominalGlyph(r); ok {
		return m.f.face
	}
	for _, fb := range m.f.fallback {
		if _, ok := fb.face.NominalGlyph(r); ok {
			return fb.face
		}
	}
	if ff := systemFace(r); ff != nil {
		return ff
	}
	return m.f.face
}

// A Run is one line of shaped text, laid out along a baseline that
// starts at the origin.
type Run struct {
	Face *Face
	// Size is the font size in logical pixels.
	Size float32
	// Glyphs are in visual order, left to right, positioned relative to
	// the start of the baseline with y growing downward.
	Glyphs []paint.Glyph
	// Advance is how far the pen moved: the run's width.
	Advance float32
	// Ascent and Descent are the line's extents above and below the
	// baseline, both positive.
	Ascent, Descent float32
	// Start and End are the runes of the text this line holds, as rune
	// indices into the whole string it was shaped or laid out from.
	Start, End int
	// carets holds the caret's x before each rune from Start to End,
	// End included.
	carets []float32
	// stops are every place a caret can sit on screen, by x. Where
	// text of two directions meets, one rune index has two places.
	stops []stop
}

// stop is one place a caret can sit: an x, and the rune index a caret
// there is before.
type stop struct {
	x float32
	i int
}

// CaretX returns the x of a caret placed before rune i, counted as
// Start and End are. In right-to-left text a rune's leading edge is its
// right side, so the caret sits there. i is clamped to the line.
func (r Run) CaretX(i int) float32 {
	if len(r.carets) == 0 {
		return 0
	}
	i = min(max(i, r.Start), r.End)
	return r.carets[i-r.Start]
}

// Index returns the rune index whose caret position is nearest x: where
// a click at x puts the caret.
func (r Run) Index(x float32) int {
	best, dist := r.Start, float32(math.Inf(1))
	for i, cx := range r.carets {
		if d := abs(cx - x); d < dist {
			best, dist = r.Start+i, d
		}
	}
	return best
}

// Beside returns the caret place next on screen to a caret before rune
// i at x: the nearest to the right when right is true, else to the
// left, as a rune index and an x. It returns i and x at the line's edge.
//
// In text of one direction that is the next or previous rune. In mixed
// text it follows the screen, so an arrow key moves the caret the way
// it points. Where text of two directions meets, one rune index has two
// places on screen; keep the x Beside returns to draw the caret where
// it went.
func (r Run) Beside(i int, x float32, right bool) (next int, nextX float32) {
	const eps = 0.01
	nextX = float32(math.Inf(1))
	if !right {
		nextX = float32(math.Inf(-1))
	}
	for _, s := range r.stops {
		if right && s.x > x+eps && s.x < nextX || !right && s.x < x-eps && s.x > nextX {
			nextX = s.x
		}
	}
	if math.IsInf(float64(nextX), 0) {
		return i, x
	}
	// Of the runes at that place, take the one nearest i in the text.
	next = -1
	for _, s := range r.stops {
		if abs(s.x-nextX) <= eps && (next < 0 || absInt(s.i-i) < absInt(next-i)) {
			next = s.i
		}
	}
	return next, nextX
}

// Hit returns the caret place nearest x, as a rune index and an x.
func (r Run) Hit(x float32) (i int, caretX float32) {
	if len(r.stops) == 0 {
		return r.Start, 0
	}
	best := r.stops[0]
	for _, s := range r.stops {
		if abs(s.x-x) < abs(best.x-x) {
			best = s
		}
	}
	return best.i, best.x
}

// Places reports whether a caret before rune i can sit at x.
func (r Run) Places(i int, x float32) bool {
	for _, s := range r.stops {
		if s.i == i && abs(s.x-x) <= 0.01 {
			return true
		}
	}
	return false
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// Height returns the line's height, ascent plus descent.
func (r Run) Height() float32 { return r.Ascent + r.Descent }

// Box returns the run's size: its advance by its height.
func (r Run) Box() geom.Size { return geom.Sz(r.Advance, r.Height()) }

// Paint draws the run with the top-left of its line box at topLeft.
func (r Run) Paint(p *paint.Painter, topLeft geom.Point, c color.NRGBA) {
	if len(r.Glyphs) == 0 {
		return
	}
	defer p.Push(paint.Translate(geom.Pt(topLeft.X, topLeft.Y+r.Ascent)))()
	p.Text(r.Glyphs, r.Size, c, geom.Rect{
		Min: geom.Pt(0, -r.Ascent),
		Max: geom.Pt(r.Advance, r.Descent),
	})
}

// Shape lays s out as one line at size logical pixels, with no
// wrapping. A line break in s is drawn as a space.
//
// Bidirectional text and fallback faces work as they do in [Face.Layout].
func (f *Face) Shape(s string, size float32) Run {
	runes := []rune(s)
	for i, r := range runes {
		if isLineBreak(r) {
			runes[i] = ' '
		}
	}
	mu.Lock()
	defer mu.Unlock()
	lines, _ := f.wrapLocked(runes, size, 0, shaping.WrapConfig{Direction: direction(runes)})
	if len(lines) == 0 {
		return f.emptyRun(size)
	}
	return f.lineRun(lines[0], size)
}

// Align places lines of different widths within a paragraph.
type Align uint8

const (
	// AlignStart puts lines against the edge a paragraph starts from:
	// the left for left-to-right text, the right for right-to-left.
	AlignStart Align = iota
	// AlignCenter centres each line.
	AlignCenter
	// AlignEnd puts lines against the edge a paragraph ends at.
	AlignEnd
)

// Style describes how [Face.Layout] sets a paragraph.
type Style struct {
	// Size is the font size in logical pixels.
	Size float32
	// LineHeight scales the font's own line spacing. Zero means 1.
	LineHeight float32
	Align      Align
	// MaxLines cuts the text after that many lines and ends the last
	// one with an ellipsis. Zero means no limit.
	MaxLines int
}

// A Paragraph is text laid out in lines.
type Paragraph struct {
	Lines []Line
	// LineHeight is the distance from one line's top to the next's.
	LineHeight float32
	// Size is the box the lines fill: the widest line by the height of
	// every line together.
	Size geom.Size
	// Truncated reports whether MaxLines cut text off.
	Truncated bool
}

// A Line is one line of a [Paragraph].
type Line struct {
	Run Run
	// At is the top-left of the line's box within the paragraph, after
	// alignment.
	At geom.Point
	// RightToLeft reports whether the line belongs to a right-to-left
	// paragraph.
	RightToLeft bool
}

// Caret returns where a caret before rune i goes: its line, and its x
// and the top of that line within the paragraph. i counts runes in the
// whole string. Where a wrapped line ends and the next begins at the
// same rune, the caret goes to the start of the later line.
func (p Paragraph) Caret(i int) (line int, at geom.Point) {
	for k, l := range p.Lines {
		if l.Run.Start <= i {
			line = k
		}
	}
	if len(p.Lines) == 0 {
		return 0, geom.Point{}
	}
	l := p.Lines[line]
	return line, geom.Pt(l.At.X+l.Run.CaretX(i), l.At.Y)
}

// Index returns the rune whose caret is nearest pt, in the paragraph's
// space: where a click at pt puts the caret.
func (p Paragraph) Index(pt geom.Point) int {
	if len(p.Lines) == 0 {
		return 0
	}
	line := 0
	if p.LineHeight > 0 {
		line = int(pt.Y / p.LineHeight)
	}
	l := p.Lines[max(0, min(line, len(p.Lines)-1))]
	return l.Run.Index(pt.X - l.At.X)
}

// Paint draws the paragraph with its top-left at topLeft.
func (p Paragraph) Paint(painter *paint.Painter, topLeft geom.Point, c color.NRGBA) {
	// The lines the clips hide are left out, a line's height of room
	// kept above and below for the strokes that reach past its box.
	shown, cull := painter.Visible()
	for _, l := range p.Lines {
		at := topLeft.Add(l.At)
		if h := l.Run.Height(); cull && (at.Y+2*h < shown.Min.Y || at.Y-h > shown.Max.Y) {
			continue
		}
		l.Run.Paint(painter, at, c)
	}
}

// Layout sets s as a paragraph in lines at most width logical pixels
// wide, breaking where Unicode's line breaking rules allow and within a
// word only when the word alone is wider than width. A path breaks after
// its slashes or backslashes and nowhere else in it. A width of zero or
// less sets each line of s unbroken.
//
// Each newline in s starts a paragraph of its own, whose direction comes
// from its first letter with a direction, so Hebrew or Arabic sets right
// to left while the text around it sets left to right. Characters the
// face lacks come from its [Face.Fallback] faces.
func (f *Face) Layout(s string, st Style, width float32) Paragraph {
	if st.LineHeight <= 0 {
		st.LineHeight = 1
	}
	scale := st.Size / f.upem
	ascent, descent := f.ascent*scale, f.descent*scale
	step := (f.ascent + f.descent + f.gap) * scale * st.LineHeight
	// Half the leading goes above the line and half below.
	halfLeading := (step - ascent - descent) / 2

	out := Paragraph{LineHeight: step}
	paras, seps := splitLines(s)
	base := 0 // the rune where the current paragraph starts
	mu.Lock()
	var ellipsis shaping.Output
	if st.MaxLines > 0 {
		ellipsis = f.shapeLocked([]rune("…"), st.Size, di.DirectionLTR)
	}
	for i, runes := range paras {
		if st.MaxLines > 0 && len(out.Lines) == st.MaxLines {
			out.Truncated = true
			break
		}
		dir := direction(runes)
		cfg := shaping.WrapConfig{Direction: dir}
		if st.MaxLines > 0 {
			cfg.TruncateAfterLines = st.MaxLines - len(out.Lines)
			cfg.Truncator = ellipsis
			cfg.TextContinues = i < len(paras)-1
		}
		lines, cut := f.wrapLocked(runes, st.Size, width, cfg)
		if cut > 0 {
			out.Truncated = true
		}
		if len(lines) == 0 {
			// An empty paragraph still takes a line.
			lines = []shaping.Line{nil}
		}
		for _, ln := range lines {
			run := f.lineRun(ln, st.Size)
			run.Ascent, run.Descent = ascent, descent
			run.Start, run.End = run.Start+base, run.End+base
			for k := range run.stops {
				run.stops[k].i += base
			}
			out.Lines = append(out.Lines, Line{
				Run:         run,
				At:          geom.Pt(0, float32(len(out.Lines))*step+halfLeading),
				RightToLeft: dir == di.DirectionRTL,
			})
		}
		if st.MaxLines > 0 && len(out.Lines) > st.MaxLines {
			out.Lines = out.Lines[:st.MaxLines]
			out.Truncated = true
		}
		base += len(runes) + seps[i]
	}
	mu.Unlock()

	for _, l := range out.Lines {
		out.Size.W = max(out.Size.W, l.Run.Advance)
	}
	out.Size.H = float32(len(out.Lines)) * step
	for i := range out.Lines {
		l := &out.Lines[i]
		spare := out.Size.W - l.Run.Advance
		switch {
		case st.Align == AlignCenter:
			l.At.X = spare / 2
		case (st.Align == AlignEnd) != l.RightToLeft:
			l.At.X = spare
		}
	}
	return out
}

// splitLines splits s at each line break into the runes of each line, and returns how many runes the break after
// each line takes: two for \r\n, one for \n, \r and the Unicode line and paragraph separators, none after the last.
func splitLines(s string) (lines [][]rune, seps []int) {
	start := 0
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if !isLineBreak(runes[i]) {
			continue
		}
		sep := 1
		if runes[i] == '\r' && i+1 < len(runes) && runes[i+1] == '\n' {
			sep = 2
		}
		lines, seps = append(lines, runes[start:i]), append(seps, sep)
		i += sep - 1
		start = i + 1
	}
	return append(lines, runes[start:]), append(seps, 0)
}

// isLineBreak reports whether r ends a line: a newline, a carriage return, a next line, or a line or paragraph
// separator.
func isLineBreak(r rune) bool {
	switch r {
	case '\n', '\r', '\u0085', ' ', ' ':
		return true
	}
	return false
}

// direction returns the direction of a paragraph: the direction of its
// first letter that has one, and left to right when none does. It runs
// with mu held.
func direction(runes []rune) di.Direction {
	if len(runes) == 0 {
		return di.DirectionLTR
	}
	// With no default, the bidi algorithm sets the paragraph's level
	// from its first strong character, and no run sits below it.
	runs := levels.Segment(runes, bidi.Neutral)
	rtl := true
	for i := range runs.NumRuns() {
		if runs.Run(i).Level%2 == 0 {
			rtl = false
			break
		}
	}
	if rtl {
		return di.DirectionRTL
	}
	return di.DirectionLTR
}

// shapeLocked shapes runes as a single run in f. It runs with mu held.
func (f *Face) shapeLocked(runes []rune, size float32, dir di.Direction) shaping.Output {
	return shape(shaping.Input{
		Text:      runes,
		RunEnd:    len(runes),
		Direction: dir,
		Face:      f.face,
		Size:      toFixed(size),
		Script:    language.Common,
		Language:  language.DefaultLanguage(),
	}, size)
}

// shape shapes in at size, as fractional as it is. The shaper sets text
// at the size rounded up to a whole pixel, so text at 13.5 pixels was
// spaced as wide as at 14, and text whose size animated was spaced for
// the size it was leaving until it all but arrived, when it jumped.
// Spacing is in proportion to the size: in is shaped at the whole size
// the shaper takes, and the result scaled to size, which is exact where
// in's own size keeps a sixty-fourth of a pixel. It runs with mu held.
func shape(in shaping.Input, size float32) shaping.Output {
	whole := float32(math.Ceil(float64(size)))
	if whole <= 0 || whole == size {
		return shaper.Shape(in)
	}
	in.Size = fixed.I(int(whole))
	out := shaper.Shape(in)
	k := float64(size) / float64(whole)
	scale := func(v *fixed.Int26_6) { *v = fixed.Int26_6(math.Round(float64(*v) * k)) }
	for i := range out.Glyphs {
		g := &out.Glyphs[i]
		// XAdvance and YAdvance, gone from the shaper's interface, are
		// left: Advance is the one read.
		for _, v := range []*fixed.Int26_6{&g.Width, &g.Height, &g.XBearing, &g.YBearing, &g.Advance, &g.XOffset, &g.YOffset} {
			scale(v)
		}
	}
	scale(&out.LineBounds.Ascent)
	scale(&out.LineBounds.Descent)
	scale(&out.LineBounds.Gap)
	out.Size = toFixed(size)
	out.RecalculateAll()
	return out
}

// wrapLocked splits one paragraph into runs, shapes them, and wraps them
// into lines no wider than width, or unbroken when width is zero or less.
// It also returns how many runes a line limit in cfg cut off. It runs
// with mu held.
func (f *Face) wrapLocked(runes []rune, size, width float32, cfg shaping.WrapConfig) (lines []shaping.Line, cut int) {
	if len(runes) == 0 {
		return nil, 0
	}
	fm := &fontmap{f: f}
	inputs := respace(seg.Split(shaping.Input{
		Text:      runes,
		RunEnd:    len(runes),
		Direction: cfg.Direction,
		Face:      f.face,
		Size:      toFixed(size),
		Language:  language.DefaultLanguage(),
	}, fm), fm)
	outs := make([]shaping.Output, len(inputs))
	for i, in := range inputs {
		outs[i] = shape(in, size)
		inkWidths(&outs[i], runes)
	}
	maxWidth := fixed.Int26_6(math.MaxInt32)
	if width > 0 {
		maxWidth = toFixed(width)
	}
	return wrapper.WrapParagraphF(cfg, maxWidth, breakText(runes), shaping.NewSliceIterator(outs))
}

// respace moves spaces out of runs set in a face other than their own.
// The segmenter leaves a space in the face of the text before it, so it
// starts no run of its own, and an emoji font sets a space as wide as an
// emoji: a space after an emoji would show as a wide gap. Each stretch
// of spaces goes to the face the fontmap gives a space. It runs with mu
// held.
func respace(inputs []shaping.Input, fm *fontmap) []shaping.Input {
	out := make([]shaping.Input, 0, len(inputs))
	for _, in := range inputs {
		start := in.RunStart
		for i := in.RunStart; i < in.RunEnd; {
			r := in.Text[i]
			if !unicode.IsSpace(r) {
				i++
				continue
			}
			face := fm.resolve(r)
			if face == in.Face {
				i++
				continue
			}
			j := i
			for j < in.RunEnd && unicode.IsSpace(in.Text[j]) {
				j++
			}
			if i > start {
				part := in
				part.RunStart, part.RunEnd = start, i
				out = append(out, part)
			}
			part := in
			part.RunStart, part.RunEnd, part.Face = i, j, face
			out = append(out, part)
			start, i = j, j
		}
		if start < in.RunEnd {
			part := in
			part.RunStart = start
			out = append(out, part)
		}
	}
	return out
}

// inkWidths gives each glyph that moves the pen and has no measured
// ink its advance for a width, unless it is a space. The wrapper takes a
// glyph of no width at a line's end for a space and drops its advance,
// and a colour glyph drawn in layers, as a COLRv1 emoji is, has no
// outline to measure: an emoji ending a line would lose its width, and
// the caret after it would sit before it.
func inkWidths(out *shaping.Output, runes []rune) {
	for i := range out.Glyphs {
		g := &out.Glyphs[i]
		if g.Width == 0 && g.Advance > 0 && g.TextIndex() < len(runes) && !unicode.IsSpace(runes[g.TextIndex()]) {
			g.Width = g.Advance
		}
	}
}

// lineRun lays a wrapped line's runs out left to right in visual order.
// Each run's glyphs are already in visual order. It runs with mu held.
func (f *Face) lineRun(ln shaping.Line, size float32) Run {
	run := f.emptyRun(size)
	order := make([]int, len(ln))
	for i, r := range ln {
		order[r.VisualIndex] = i
	}
	if len(ln) > 0 {
		run.Start, run.End = ln[0].Runes.Offset, ln[0].Runes.Offset
		for _, out := range ln {
			run.Start = min(run.Start, out.Runes.Offset)
			run.End = max(run.End, out.Runes.Offset+out.Runes.Count)
		}
	}
	carets := make([]float32, run.End-run.Start+1)
	set := make([]bool, len(carets))

	var pen float32
	for _, i := range order {
		out := ln[i]
		id := f.id
		if face, ok := byFont[out.Face]; ok {
			id = face.id
		}
		rtl := out.Direction.Progression() == di.TowardTopLeft
		for gi := 0; gi < len(out.Glyphs); {
			// A cluster is one or more glyphs drawn for one or more
			// runes; its width is shared out among its runes.
			g := out.Glyphs[gi]
			n := max(g.GlyphsCount(), 1)
			x0 := pen
			for _, cg := range out.Glyphs[gi:min(gi+n, len(out.Glyphs))] {
				run.Glyphs = append(run.Glyphs, paint.Glyph{
					ID:   uint32(cg.GlyphID),
					At:   geom.Pt(pen+fromFixed(cg.XOffset), -fromFixed(cg.YOffset)),
					Face: id,
				})
				pen += fromFixed(cg.Advance)
			}
			runes := max(g.RunesCount(), 1)
			for k := range runes + 1 {
				frac := float32(k) / float32(runes)
				edge := x0 + (pen-x0)*frac
				if rtl {
					edge = pen - (pen-x0)*frac
				}
				run.stops = append(run.stops, stop{x: edge, i: g.TextIndex() + k})
				idx := g.TextIndex() + k - run.Start
				if idx < 0 || idx >= len(carets) || (k == runes && set[idx]) {
					continue
				}
				carets[idx] = edge
				set[idx] = k < runes
			}
			gi += n
		}
	}
	run.Advance = pen
	run.carets = carets
	if len(run.stops) == 0 {
		run.stops = []stop{{x: 0, i: run.Start}}
	}
	return run
}

func (f *Face) emptyRun(size float32) Run {
	scale := size / f.upem
	return Run{
		Face: f, Size: size, Ascent: f.ascent * scale, Descent: f.descent * scale,
		carets: []float32{0}, stops: []stop{{}},
	}
}

func toFixed(v float32) fixed.Int26_6 { return fixed.Int26_6(math.Round(float64(v) * 64)) }

func fromFixed(v fixed.Int26_6) float32 { return float32(v) / 64 }
