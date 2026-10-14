package filemanager

import "github.com/marrasen/gunim/geom"

// squarify lays values out in r as rectangles whose areas keep to the
// values, each as near a square as can be: the squarified treemap of
// Bruls, Huizing and van Wijk. The values must be largest first; a value
// of 0 or less gets an empty rectangle.
func squarify(values []float64, r geom.Rect) []geom.Rect {
	out := make([]geom.Rect, len(values))
	total := 0.0
	n := len(values)
	for i, v := range values {
		if v <= 0 {
			n = i
			break
		}
		total += v
	}
	if n == 0 || total <= 0 || r.Size().W <= 0 || r.Size().H <= 0 {
		return out
	}
	// Each value as the area it takes.
	scale := float64(r.Size().W) * float64(r.Size().H) / total
	areas := make([]float64, n)
	for i := range n {
		areas[i] = values[i] * scale
	}
	free := r
	i := 0
	for i < n {
		w, h := float64(free.Size().W), float64(free.Size().H)
		side := min(w, h)
		// Take values into the row along the shorter side while the worst
		// of their shapes gets no worse.
		j, sum := i+1, areas[i]
		worst := worstRatio(areas[i:j], sum, side)
		for j < n {
			next := worstRatio(areas[i:j+1], sum+areas[j], side)
			if next > worst {
				break
			}
			sum += areas[j]
			worst = next
			j++
		}
		if j == n {
			// The last row takes all the room left, which its areas add up
			// to, so rounding leaves no gap.
			sum = w * h
		}
		thick := sum / side
		at := 0.0
		for k := i; k < j; k++ {
			along := areas[k] / thick
			if k == j-1 {
				along = side - at
			}
			if w >= h {
				// A column at the left.
				out[k] = geom.Rc(free.Min.X, free.Min.Y+float32(at), float32(thick), float32(along))
			} else {
				// A row at the top.
				out[k] = geom.Rc(free.Min.X+float32(at), free.Min.Y, float32(along), float32(thick))
			}
			at += along
		}
		if w >= h {
			free.Min.X = min(free.Max.X, free.Min.X+float32(thick))
		} else {
			free.Min.Y = min(free.Max.Y, free.Min.Y+float32(thick))
		}
		i = j
	}
	return out
}

// worstRatio is the worst aspect ratio of the areas laid in a row along a
// side of length side, sum being their total.
func worstRatio(areas []float64, sum, side float64) float64 {
	if sum <= 0 || side <= 0 {
		return 0
	}
	hi, lo := areas[0], areas[0]
	for _, a := range areas {
		hi, lo = max(hi, a), min(lo, a)
	}
	s2, w2 := sum*sum, side*side
	return max(w2*hi/s2, s2/(w2*lo))
}

// The room a folder's box keeps round what it holds: a strip for its
// name along the top, where the box has the room, and a margin.
const (
	mapHead   = 18
	mapMargin = 3
	// mapLeast is the least width and height a box is drawn at, and
	// mapHeadRoom the least a folder's box needs for its name's strip.
	mapLeast    = 2
	mapHeadRoom = 56
)

// mapBox is where a treemap draws one item, and how deep it is under the
// folder in focus.
type mapBox struct {
	r     geom.Rect
	depth int
	// head says the box has a strip for its name along the top.
	head bool
}

// layoutMap lays the items out in r as a treemap sized by m, nested: each
// folder's items inside its box. The items are as [Usage] has them, a
// folder before its items, its items largest first. An item too small to
// draw gets a zero box.
func layoutMap(items []UsageItem, m Measure, r geom.Rect) []mapBox {
	out := make([]mapBox, len(items))
	if len(items) == 0 {
		return out
	}
	kids := make([][]int, len(items))
	for i, it := range items {
		if it.Parent >= 0 && int(it.Parent) < len(items) {
			kids[it.Parent] = append(kids[it.Parent], i)
		}
	}
	var place func(i, depth int, r geom.Rect)
	place = func(i, depth int, r geom.Rect) {
		if r.Size().W < mapLeast || r.Size().H < mapLeast {
			return
		}
		b := mapBox{r: r, depth: depth}
		inner := r
		if depth > 0 && len(kids[i]) > 0 {
			b.head = r.Size().W >= mapHeadRoom && r.Size().H >= mapHeadRoom
			top := float32(mapMargin)
			if b.head {
				top = mapHead
			}
			inner = geom.Rect{Min: geom.Pt(r.Min.X+mapMargin, r.Min.Y+top), Max: geom.Pt(r.Max.X-mapMargin, r.Max.Y-mapMargin)}
		}
		out[i] = b
		ks := kids[i]
		if len(ks) == 0 || inner.Size().W < mapLeast || inner.Size().H < mapLeast {
			return
		}
		values := make([]float64, len(ks))
		for k, j := range ks {
			values[k] = float64(measureOf(items[j], m))
		}
		// Largest first, as squarify wants: the items come so already, but
		// for the items put together, which come last.
		order := make([]int, len(ks))
		for k := range order {
			order[k] = k
		}
		sortedByValue(order, values)
		vs := make([]float64, len(ks))
		for k, o := range order {
			vs[k] = values[o]
		}
		rs := squarify(vs, inner)
		for k, o := range order {
			place(ks[o], depth+1, rs[k])
		}
	}
	place(0, 0, r)
	return out
}

// sortedByValue sorts order, indices into values, largest value first,
// keeping the order of equal ones.
func sortedByValue(order []int, values []float64) {
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && values[order[j]] > values[order[j-1]]; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
}

// measureOf is what m sizes it by.
func measureOf(it UsageItem, m Measure) int64 {
	switch m {
	case MeasureDevice:
		return it.Size - it.Online
	case MeasureOnline:
		return it.Online
	case MeasureAll:
	}
	return it.Size
}
