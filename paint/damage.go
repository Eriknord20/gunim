package paint

import "github.com/marrasen/gunim/geom"

// MaxDamageRects is how many rectangles [Painter.DamageRects] returns
// at most. Each costs the renderer a pass over the frame's ops, culled
// a layer at a time, which costs far less than drawing what lies
// between them. A window of many views each with a part that moves as
// a song plays (a playhead, meters in three places, a lit step, a row
// following) changes in that many places: with 4, they merged into a
// box round most of the window. In Hej DAW! on a laptop at 125%, 12
// redrew 6.5% of the window a frame where 4 redrew 37%, and drawing the
// frame took half the time.
const MaxDamageRects = 12

const (
	// fewDamageRects is how many rectangles need no more thought than their sum
	// against the box round them.
	fewDamageRects = 4
	// manyDamageCover is how much of the box round them more than that may cover
	// and still each be worth a pass.
	manyDamageCover = 1.0 / 3
)

// damageSlack is how much area, in square logical pixels, two damaged
// rectangles may waste when they merge into one: two parts of a meter a
// few pixels apart are drawn as one.
const damageSlack = 32 * 32

// damage gathers the rectangles a frame changed in, merging those that
// touch or nearly do, and keeps at most [MaxDamageRects] of them.
type damage struct {
	rs []geom.Rect
}

// reset empties d, keeping its room.
func (d *damage) reset() { d.rs = d.rs[:0] }

// add adds r to d. A rectangle that touches one already there, or lies
// so near that the two cost little more drawn as one, merges with it,
// and the merged one is tried against the rest again.
func (d *damage) add(r geom.Rect) {
	if r.Empty() {
		return
	}
	for i := 0; i < len(d.rs); {
		q := d.rs[i]
		if !near(q, r) {
			i++
			continue
		}
		r = q.Union(r)
		d.rs[i] = d.rs[len(d.rs)-1]
		d.rs = d.rs[:len(d.rs)-1]
		i = 0
	}
	d.rs = append(d.rs, r)
	// Past twice the most, the cheapest pair merges, so a frame of many
	// scattered changes costs a bounded amount of merging.
	for len(d.rs) > 2*MaxDamageRects {
		d.mergeCheapest()
	}
}

// rects returns d's rectangles, at most [MaxDamageRects]. Each is a pass over the
// frame's ops, which small scattered parts repay many times over. Rectangles that
// are large for how many they are don't: 12 covering half the window took twice as
// long as the few round them, so those merge to [fewDamageRects]. And where they
// cover most of the rectangle round them all, it returns that one.
func (d *damage) rects() []geom.Rect {
	for len(d.rs) > MaxDamageRects {
		d.mergeCheapest()
	}
	if len(d.rs) < 2 {
		return d.rs
	}
	if len(d.rs) > fewDamageRects && d.covers() >= manyDamageCover {
		for len(d.rs) > fewDamageRects {
			d.mergeCheapest()
		}
	}
	if d.covers() >= 0.6 {
		var all geom.Rect
		for _, r := range d.rs {
			all = all.Union(r)
		}
		d.rs = append(d.rs[:0], all)
	}
	return d.rs
}

// covers is how much of the rectangle round d's rectangles they cover.
func (d *damage) covers() float32 {
	var all geom.Rect
	var sum float32
	for _, r := range d.rs {
		all = all.Union(r)
		sum += area(r)
	}
	if a := area(all); a > 0 {
		return sum / a
	}
	return 0
}

// mergeCheapest merges the two rectangles of d whose union adds the
// least area.
func (d *damage) mergeCheapest() {
	bi, bj, best := 0, 1, float32(-1)
	for i := range d.rs {
		for j := i + 1; j < len(d.rs); j++ {
			cost := area(d.rs[i].Union(d.rs[j])) - area(d.rs[i]) - area(d.rs[j])
			if best < 0 || cost < best {
				bi, bj, best = i, j, cost
			}
		}
	}
	d.rs[bi] = d.rs[bi].Union(d.rs[bj])
	d.rs[bj] = d.rs[len(d.rs)-1]
	d.rs = d.rs[:len(d.rs)-1]
}

// near reports whether a and b are better drawn as one rectangle: they
// overlap or touch, or the one round them both wastes no more than
// [damageSlack].
func near(a, b geom.Rect) bool {
	if a.Min.X <= b.Max.X && b.Min.X <= a.Max.X && a.Min.Y <= b.Max.Y && b.Min.Y <= a.Max.Y {
		return true
	}
	return area(a.Union(b))-area(a)-area(b) <= damageSlack
}

// area is r's area, 0 for an empty one.
func area(r geom.Rect) float32 {
	if r.Empty() {
		return 0
	}
	s := r.Size()
	return s.W * s.H
}
