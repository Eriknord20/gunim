package widget

import (
	"math"
	"slices"
	"sort"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
)

// The bounds of a justified row's scale, from the target height, and of a tile's aspect.
const (
	// minRowScale and maxRowScale bound how far a row of several tiles shrinks or grows from the target height to
	// fill the width. A row that would grow further stops at maxRowScale, short of the right edge.
	minRowScale = 0.6
	maxRowScale = 1.5
	// maxAspect bounds a tile's aspect, from 1/maxAspect to maxAspect.
	maxAspect = 20
)

// Reshape tells the grid that Aspect gives some tiles new shapes, such as a picture's real aspect learned after its
// tile showed with a guess. The next layout fills the rows anew, and the tiles glide to their new places and sizes as
// they do after a Reorder, the tile the keyboard is on, or the first in view, holding its place on screen. Call it
// too when Aspect is a new func. It does nothing to a grid of tiles of one size.
func (g *TileGrid) Reshape(u *gunim.UI) {
	g.shapes++
	u.Invalidate()
}

// tileRows is where the tiles of a grid in justified rows go, from Aspect: each row's first tile and its top and
// height, and each tile's left and width.
type tileRows struct {
	// first holds each row's first tile, and n after the last row.
	first  []int
	top, h []float32
	x, w   []float32
	// headY holds each group's header's top, and bottom where the next row would start.
	headY  []float32
	bottom float32
	// key is what the rows were laid out from.
	key rowsKey
}

// rowsKey is what a justified layout depends on, so that one is laid out again only when that changes.
type rowsKey struct {
	n, shapes                      int
	width, pad, gap, head, caption float32
	size                           geom.Size
	starts                         []int
}

func (k rowsKey) same(o rowsKey) bool {
	return k.n == o.n && k.shapes == o.shapes && k.width == o.width && k.pad == o.pad && k.gap == o.gap &&
		k.head == o.head && k.caption == o.caption && k.size == o.size && slices.Equal(k.starts, o.starts)
}

// newJustifiedLayout lays the tiles out as key says, in rows across key.width from key.pad, each tile as wide as
// aspect makes it at its row's height. A row gathers tiles at the target height, size.H less the caption, until the
// next would pass the right edge; then it shrinks to take that tile too, or grows to fill the width without it,
// whichever changes it less, between minRowScale and maxRowScale. A tile too wide for a row alone takes a row alone,
// shrunk to fit. A group's last row keeps the target height, from the left.
func newJustifiedLayout(key rowsKey, aspect func(i int) float32) tileLayout {
	n, width, pad, gap, caption := key.n, max(key.width, 1), key.pad, key.gap, key.caption
	l := tileLayout{n: n, left: pad, pad: pad, gap: gap, size: key.size, span: width}
	l.starts, l.head = groupStarts(n, key.starts, key.head)
	j := &tileRows{x: make([]float32, n), w: make([]float32, n), key: key}
	pic := max(1, key.size.H-caption)
	own := max(key.size.W, 1) / pic
	for i := range n {
		a := aspect(i)
		if !(a > 0) || math.IsInf(float64(a), 0) {
			a = own
		}
		j.w[i] = min(max(a, 1/float32(maxAspect)), maxAspect) * pic
	}
	y := pad
	for k := range l.starts {
		l.rowBase = append(l.rowBase, len(j.first))
		j.headY = append(j.headY, y)
		y += l.head
		for a, end := l.starts[k], l.end(k); a < end; {
			b, scale, full := fillRow(j.w, a, end, width, gap)
			x := pad
			for i := a; i < b; i++ {
				w := j.w[i] * scale
				j.x[i], j.w[i] = x, w
				x += w + gap
			}
			if full {
				// The last tile's right edge is the row's, whatever the sum came to.
				j.w[b-1] = pad + width - j.x[b-1]
			}
			h := pic*scale + caption
			j.first, j.top, j.h = append(j.first, a), append(j.top, y), append(j.h, h)
			y += h + gap
			a = b
		}
	}
	l.rowBase = append(l.rowBase, len(j.first))
	j.first = append(j.first, n)
	j.bottom = y
	l.just = j
	return l
}

// fillRow returns the end of the row that starts at tile a, before end, with w the tiles' widths at the target
// height, the scale that fits it to width, and whether it fills it.
func fillRow(w []float32, a, end int, width, gap float32) (b int, scale float32, full bool) {
	sum := float32(0)
	for b = a; b < end; b++ {
		if sum+w[b]+gap*float32(b-a) < width {
			sum += w[b]
			continue
		}
		// Tile b reaches the right edge: the row shrinks to take it, or grows to fill the width without it.
		down := (width - gap*float32(b-a)) / (sum + w[b])
		if b == a {
			return a + 1, down, true
		}
		up := (width - gap*float32(b-a-1)) / sum
		if down >= minRowScale && (up > maxRowScale || 1/down <= up) {
			return b + 1, down, true
		}
		if up > maxRowScale {
			return b, maxRowScale, false
		}
		return b, up, true
	}
	return end, 1, false
}

// atX returns the tile of row r whose place spans x, or -1 for a gap or past the row's ends.
func (l tileLayout) atX(r int, x float32) int {
	if j := l.just; j != nil {
		if r < 0 || r >= l.rows() {
			return -1
		}
		f, e := j.first[r], j.first[r+1]
		i := f + sort.Search(e-f, func(d int) bool { return j.x[f+d]+j.w[f+d] >= x })
		if i >= e || x < j.x[i] {
			return -1
		}
		return i
	}
	x -= l.left
	if x < 0 {
		return -1
	}
	c := int(x / l.step.W)
	if c >= l.cols || x-float32(c)*l.step.W > l.size.W {
		return -1
	}
	return l.at(r, c)
}

// touched returns the run of row r's tiles that the span from x0 to x1 touches, empty where it touches none.
func (l tileLayout) touched(r int, x0, x1 float32) (a, b int) {
	if j := l.just; j != nil {
		f, e := j.first[r], j.first[r+1]
		a = f + sort.Search(e-f, func(d int) bool { return j.x[f+d]+j.w[f+d] >= x0 })
		b = f + sort.Search(e-f, func(d int) bool { return j.x[f+d] > x1 })
		return a, max(a, b)
	}
	col := func(x float32, end bool) int {
		c := (x - l.left) / l.step.W
		if end {
			return min(l.cols-1, int(math.Floor(float64(c))))
		}
		// Past a tile's right edge, into the gap, the span has not reached the next.
		k := int(math.Floor(float64(c)))
		if x-l.left-float32(k)*l.step.W > l.size.W {
			k++
		}
		return max(0, k)
	}
	c0, c1 := col(x0, false), col(x1, true)
	a = l.at(r, c0)
	if c0 > c1 || a < 0 {
		return 0, 0
	}
	return a, min(l.rowLast(r), l.rowFirst(r)+c1) + 1
}

// rowsAway returns the tile d rows from tile i, no further than the first or last row: in justified rows the one
// whose middle is nearest tile i's, and otherwise the one in its column, or the row's last where it is shorter.
func (l tileLayout) rowsAway(i, d int) int {
	r, c := l.rowCol(i)
	r = min(max(r+d, 0), l.rows()-1)
	j := l.just
	if j == nil {
		return min(l.rowFirst(r)+c, l.rowLast(r))
	}
	mid := j.x[i] + j.w[i]/2
	best, far := j.first[r], float32(math.Inf(1))
	for t := j.first[r]; t < j.first[r+1]; t++ {
		if d := abs32(j.x[t] + j.w[t]/2 - mid); d < far {
			best, far = t, d
		}
	}
	return best
}

// rowStep returns how far one row is from the next, on average for justified rows.
func (l tileLayout) rowStep() float32 {
	if j := l.just; j != nil {
		if len(j.top) == 0 {
			return 0
		}
		return (j.bottom - j.top[0]) / float32(len(j.top))
	}
	return l.step.H
}
