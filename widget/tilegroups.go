package widget

import (
	"math"
	"slices"
	"sort"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// tileLayout is where a grid's tiles go: in rows of cols tiles, step apart, from left and pad, each group of them
// starting a new row under a header head tall; or, with just, in justified rows, each tile its own width.
type tileLayout struct {
	n          int
	cols       int
	left, pad  float32
	gap        float32
	step, size geom.Size
	// span is how wide the rows are, from left.
	span float32
	// starts holds each group's first tile, the first 0, and rowBase each group's first row, counted over all
	// groups, and the count of rows after the last; head is a header's height, nought for tiles in one run with no
	// headers.
	starts  []int
	rowBase []int
	head    float32
	// just holds the rows, for tiles in justified rows; nil for rows of cols.
	just *tileRows
}

// newTileLayout lays n tiles out in groups from starts, nil for one run.
func newTileLayout(n, cols int, left, pad, gap float32, step, size geom.Size, starts []int, head float32) tileLayout {
	l := tileLayout{n: n, cols: max(cols, 1), left: left, pad: pad, gap: gap, step: step, size: size}
	l.span = float32(l.cols)*step.W - gap
	l.starts, l.head = groupStarts(n, starts, head)
	row := 0
	for k := range l.starts {
		l.rowBase = append(l.rowBase, row)
		row += l.rowsOf(k)
	}
	l.rowBase = append(l.rowBase, row)
	return l
}

// groupStarts returns the groups' first tiles of n from starts, the first 0, and their headers' height, nought
// with no groups.
func groupStarts(n int, starts []int, head float32) (firsts []int, headH float32) {
	for _, s := range starts {
		if s >= 0 && s < n && (len(firsts) == 0 || s > firsts[len(firsts)-1]) {
			firsts = append(firsts, s)
		}
	}
	if len(firsts) == 0 || firsts[0] != 0 {
		firsts = append([]int{0}, firsts...)
	}
	if len(starts) > 0 {
		headH = head
	}
	return firsts, headH
}

// laid reports whether the layout was laid out, rather than the zero one before the first layout.
func (l tileLayout) laid() bool { return l.cols != 0 || l.just != nil }

// same reports whether o puts every tile where l does.
func (l tileLayout) same(o tileLayout) bool {
	if l.just != nil || o.just != nil {
		return l.just == o.just
	}
	return l.step == o.step && l.cols == o.cols && l.pad == o.pad
}

// end returns group k's end, past its last tile.
func (l tileLayout) end(k int) int {
	if k+1 < len(l.starts) {
		return l.starts[k+1]
	}
	return l.n
}

// rowsOf returns how many rows group k takes.
func (l tileLayout) rowsOf(k int) int {
	if l.just != nil {
		return l.rowBase[k+1] - l.rowBase[k]
	}
	return (l.end(k) - l.starts[k] + l.cols - 1) / l.cols
}

// rows returns how many rows the tiles take, over all groups.
func (l tileLayout) rows() int { return l.rowBase[len(l.rowBase)-1] }

// groupOf returns the group tile i is in.
func (l tileLayout) groupOf(i int) int {
	return max(0, sort.Search(len(l.starts), func(k int) bool { return l.starts[k] > i })-1)
}

// groupOfRow returns the group row r is in.
func (l tileLayout) groupOfRow(r int) int {
	return min(len(l.starts)-1, max(0, sort.Search(len(l.starts), func(k int) bool { return l.rowBase[k] > r })-1))
}

// headerY returns where group k's header starts.
func (l tileLayout) headerY(k int) float32 {
	if l.just != nil {
		return l.just.headY[k]
	}
	return l.pad + float32(k)*l.head + float32(l.rowBase[k])*l.step.H
}

// rowY returns where row r starts.
func (l tileLayout) rowY(r int) float32 {
	if l.just != nil {
		return l.just.top[r]
	}
	return l.pad + float32(l.groupOfRow(r)+1)*l.head + float32(r)*l.step.H
}

// rowH returns how tall row r's tiles are.
func (l tileLayout) rowH(r int) float32 {
	if l.just != nil {
		return l.just.h[r]
	}
	return l.size.H
}

// rowCol returns tile i's row and column.
func (l tileLayout) rowCol(i int) (row, col int) {
	if j := l.just; j != nil {
		r := sort.Search(len(j.first)-1, func(r int) bool { return j.first[r+1] > i })
		return r, i - j.first[min(r, len(j.first)-1)]
	}
	k := l.groupOf(i)
	local := i - l.starts[k]
	return l.rowBase[k] + local/l.cols, local % l.cols
}

// at returns the tile at row r and column c, or -1.
func (l tileLayout) at(r, c int) int {
	if j := l.just; j != nil {
		if r < 0 || r >= l.rows() || c < 0 || j.first[r]+c >= j.first[r+1] {
			return -1
		}
		return j.first[r] + c
	}
	if r < 0 || r >= l.rows() || c < 0 || c >= l.cols {
		return -1
	}
	k := l.groupOfRow(r)
	i := l.starts[k] + (r-l.rowBase[k])*l.cols + c
	if i >= l.end(k) {
		return -1
	}
	return i
}

// rowFirst and rowLast return the first and last tiles of row r.
func (l tileLayout) rowFirst(r int) int { return l.at(r, 0) }

func (l tileLayout) rowLast(r int) int {
	if l.just != nil {
		return l.just.first[r+1] - 1
	}
	k := l.groupOfRow(r)
	return min(l.end(k), l.starts[k]+(r-l.rowBase[k]+1)*l.cols) - 1
}

// target returns where tile i belongs, in the content's space.
func (l tileLayout) target(i int) geom.Rect {
	if j := l.just; j != nil {
		if i < 0 || i >= l.n {
			return geom.Rect{}
		}
		r, _ := l.rowCol(i)
		return geom.Rc(j.x[i], j.top[r], j.w[i], j.h[r])
	}
	r, c := l.rowCol(i)
	return geom.Rc(l.left+float32(c)*l.step.W, l.rowY(r), l.size.W, l.size.H)
}

// rowAt returns the row at height y in the content, the nearest where y is above or below them all, and whether y
// falls in the row's tiles rather than a gap or a header.
func (l tileLayout) rowAt(y float32) (int, bool) {
	if l.rows() == 0 {
		return 0, false
	}
	// The group whose header starts at or above y.
	k := max(0, sort.Search(len(l.starts), func(k int) bool { return l.headerY(k) > y })-1)
	rel := y - l.headerY(k) - l.head
	if rel < 0 {
		return l.rowBase[k], false
	}
	if j := l.just; j != nil {
		lo, hi := l.rowBase[k], l.rowBase[k+1]
		r := max(lo, lo+sort.Search(hi-lo, func(d int) bool { return j.top[lo+d] > y })-1)
		return r, y <= j.top[r]+j.h[r]
	}
	local := int(math.Floor(float64(rel / l.step.H)))
	if local >= l.rowsOf(k) {
		return l.rowBase[k+1] - 1, false
	}
	return l.rowBase[k] + local, rel-float32(local)*l.step.H <= l.size.H
}

// content returns the content's height.
func (l tileLayout) content(gap float32) float32 {
	if l.just != nil {
		return max(0, l.just.bottom+l.pad-gap)
	}
	groups := float32(0)
	if l.head > 0 {
		groups = float32(len(l.starts)) * l.head
	}
	return max(0, 2*l.pad+groups+float32(l.rows())*l.step.H-gap)
}

// headerCell holds a group's header, gliding to its place as the tiles do, and fading in.
type headerCell struct {
	anim.Group
	child gunim.Node
	y     *anim.Float
	fade  *anim.Float
}

func newHeaderCell(child gunim.Node, y float32) *headerCell {
	h := &headerCell{child: child, y: anim.NewFloat(y), fade: anim.NewFloat(0)}
	h.Add(h.y, h.fade)
	return h
}

// Children implements [gunim.Composite].
func (h *headerCell) Children() []gunim.Node { return []gunim.Node{h.child} }

// Layout implements [gunim.Node].
func (h *headerCell) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	kid.Layout(gunim.Tight(cs.Max))
	kid.Place(geom.Point{})
	return cs.Max
}

// Paint implements [gunim.Node].
func (h *headerCell) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	fade := min(max(h.fade.Value(), 0), 1)
	if fade <= 0.001 {
		return
	}
	if fade < 0.999 {
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: fade})()
	}
	kids.At(0).Paint(p)
}

// SetGroups splits the tiles into groups, each starting a new row under a header Header builds, HeaderHeight tall,
// such as the photos of a shoot taken close together: starts holds each group's first tile, ascending, from 0. Nil
// puts the tiles back in one run with no headers. The tiles spring to their new places and the headers fade in.
func (g *TileGrid) SetGroups(starts []int, u *gunim.UI) {
	if slices.Equal(starts, g.groups) && (starts == nil) == (g.groups == nil) {
		return
	}
	g.groups = slices.Clone(starts)
	g.regroup = true
	u.Invalidate()
}

// placeHeaders builds the headers of the groups in view, from first to last of the tiles, as wide as the rows, and lays
// them out; those gone out of view, or of groups that changed, go.
func (g *TileGrid) placeHeaders(first, last int, offset float32, kids gunim.Children, motion, settle anim.Spring) {
	children := map[*headerCell]gunim.Child{}
	for kid := range kids.All {
		if h, ok := kid.Node().(*headerCell); ok {
			children[h] = kid
		}
	}
	drop := func(k int) {
		kids.Drop(g.headers[k])
		delete(g.headers, k)
	}
	l := g.lay
	if g.regroup || l.head <= 0 || g.Header == nil || g.n == 0 {
		for k := range g.headers {
			drop(k)
		}
		g.regroup = false
		if l.head <= 0 || g.Header == nil || g.n == 0 {
			return
		}
	}
	k0, k1 := l.groupOf(first), l.groupOf(max(first, last))
	for k := range g.headers {
		if k < k0 || k > k1 {
			drop(k)
		}
	}
	for k := k0; k <= k1; k++ {
		y := l.headerY(k)
		h, ok := g.headers[k]
		if !ok {
			h = newHeaderCell(g.Header(k), y)
			h.fade.Animate(1, g.pacedSpring(settle))
			g.headers[k] = h
			children[h] = kids.Build(h)
		}
		h.y.Animate(y, motion)
		kid := children[h]
		kid.Layout(gunim.Tight(geom.Sz(max(0, l.span), l.head)))
		kid.Place(geom.Pt(l.left, h.y.Value()-offset))
	}
}
