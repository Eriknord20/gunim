package widget

import (
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// shapes are the aspects the justified grids' tiles take in turn.
var shapes = []float32{1.5, 0.67, 1, 2, 1.33, 0.75, 3, 1}

// justStage puts a grid of n tiles in justified rows in a frame 400 by 300: tiles 90 by 80 at the target height, a
// caption 20 tall under a picture 60 tall, in rows 372 wide from x 14, 8 apart. Tile i takes aspects[i%len].
func justStage(t *testing.T, n int, aspects []float32) (g *TileGrid, w *gunim.Window, run func(int)) {
	t.Helper()
	g = NewTileGrid(geom.Sz(90, 80))
	g.Caption = 20
	g.Aspect = func(i int) float32 { return aspects[i%len(aspects)] }
	g.Tile = func(int) gunim.Node { return &blank{} }
	g.OnSelect = func(sel [][2]int, cursor int, u *gunim.UI) gunim.Intent { return tilesPicked{sel, cursor} }
	g.OnView = func(first, count int, u *gunim.UI) gunim.Intent { return tilesInView{first, count} }
	g.n = n
	w, run = stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	run(2)
	return g, w, run
}

// checkRows checks every row of g's layout: from x 14, 8 apart, each tile its aspect at the row's picture height, a
// row filling the width to 386 within a pixel, but a group's last row at most the target height, 80: short of the
// right edge at 80, or filling it shrunk to take the group's last tile.
func checkRows(t *testing.T, g *TileGrid, aspect func(int) float32) {
	t.Helper()
	l := g.lay
	if l.just == nil {
		t.Fatal("the grid is not in justified rows")
	}
	last := map[int]bool{}
	for k := range l.starts {
		last[l.rowBase[k+1]-1] = true
	}
	for r := range l.rows() {
		f, e := l.rowFirst(r), l.rowLast(r)
		h := g.target(f).Size().H
		for i := f; i <= e; i++ {
			rc := g.target(i)
			if rc.Size().H != h {
				t.Fatalf("row %d: tile %d is %v tall, the row %v", r, i, rc.Size().H, h)
			}
			if i == f && rc.Min.X != 14 {
				t.Fatalf("row %d starts at x %v, want 14", r, rc.Min.X)
			}
			if i > f {
				if gap := rc.Min.X - g.target(i-1).Max.X; abs32(gap-8) > 0.01 {
					t.Fatalf("row %d: %v between tiles %d and %d, want 8", r, gap, i-1, i)
				}
			}
			if want := aspect(i) * (h - 20); abs32(rc.Size().W-want) > 0.05 {
				t.Fatalf("row %d: tile %d is %v wide, want %v for its aspect", r, i, rc.Size().W, want)
			}
		}
		right := g.target(e).Max.X
		if last[r] {
			if h > 80 || right > 386.5 || (h < 80 && abs32(right-386) > 0.5) {
				t.Fatalf("row %d, a group's last, is %v tall to x %v; want 80 tall, short of 386, or shrunk to fill it", r, h, right)
			}
			continue
		}
		if abs32(right-386) > 0.5 {
			t.Fatalf("row %d ends at x %v, want 386", r, right)
		}
		if s := (h - 20) / 60; s < minRowScale || s > maxRowScale {
			t.Fatalf("row %d is scaled %v", r, s)
		}
	}
}

func TestTileGridJustifiedRowsFillTheWidth(t *testing.T) {
	g, _, run := justStage(t, 40, shapes)
	checkRows(t, g, g.Aspect)
	if g.lay.rows() < 5 {
		t.Fatalf("40 tiles took %d rows", g.lay.rows())
	}
	if g.Columns() != 0 {
		t.Fatalf("justified rows have %d columns, want 0", g.Columns())
	}
	run(120)
	// The tiles settle where the layout puts them.
	for i, tc := range g.live {
		if tc.rect() != g.target(i) {
			t.Fatalf("tile %d settled at %v, want %v", i, tc.rect(), g.target(i))
		}
	}
}

func TestTileGridJustifiedGroupsStartNewRows(t *testing.T) {
	g, w, run := justStage(t, 30, shapes)
	g.Header = func(int) gunim.Node { return &blank{} }
	g.HeaderHeight = 30
	do(t, w, func(u *gunim.UI) { g.SetGroups([]int{0, 5, 12}, u) })
	run(120)
	checkRows(t, g, g.Aspect)
	l := g.lay
	for _, s := range []int{5, 12} {
		r, c := l.rowCol(s)
		if c != 0 || g.target(s).Min.X != 14 {
			t.Fatalf("group start %d is at column %d, x %v", s, c, g.target(s).Min.X)
		}
		// Under the row before it, a gap, and its header.
		if want := g.target(s-1).Max.Y + 8 + 30; abs32(l.rowY(r)-want) > 0.01 {
			t.Fatalf("group start %d's row is at %v, want %v", s, l.rowY(r), want)
		}
		if g.target(s-1).Size().H != 80 {
			t.Fatalf("tile %d, in a group's last row, is %v tall", s-1, g.target(s-1).Size().H)
		}
	}
	if got := g.placeAt(geom.Pt(30, g.target(5).Min.Y-10)); got != -1 {
		t.Fatalf("a header finds tile %d", got)
	}
}

func TestTileGridJustifiedTileAtAndTileRectAgree(t *testing.T) {
	g, w, run := justStage(t, 200, shapes)
	run(120)
	check := func() {
		t.Helper()
		for i := range g.live {
			r := g.TileRect(i)
			if r.Max.Y < 0 || r.Min.Y > 300 {
				continue
			}
			if got := g.TileAt(r.Center()); got != i {
				t.Fatalf("TileAt the middle of tile %d, at %v, gave %d", i, r, got)
			}
			// The caption band is the tile's too.
			if got := g.TileAt(geom.Pt(r.Center().X, r.Max.Y-5)); got != i {
				t.Fatalf("TileAt tile %d's caption gave %d", i, got)
			}
			if r.Max.X < 380 {
				if got := g.TileAt(geom.Pt(r.Max.X+4, r.Center().Y)); got != -1 {
					t.Fatalf("the gap right of tile %d finds tile %d", i, got)
				}
			}
			if got := g.TileAt(geom.Pt(r.Center().X, r.Max.Y+4)); got != -1 && r.Max.Y+4 < 300 {
				t.Fatalf("the gap under tile %d finds tile %d", i, got)
			}
		}
	}
	check()
	onTiles(t, w, func(u *gunim.UI) { g.JumpToTile(120, u) })
	run(2)
	if g.Offset() == 0 {
		t.Fatal("the grid did not jump")
	}
	check()
}

func TestTileGridJustifiedUpAndDownGoToTheNearestTile(t *testing.T) {
	g, w, run := justStage(t, 60, shapes)
	press(w, g.TileRect(0).Center(), 1, 0)
	run(1)
	// nearest is the tile of row r whose middle is nearest x, found from the tiles' places.
	nearest := func(r int, x float32) int {
		best := -1
		for i := range g.n {
			if rr, _ := g.lay.rowCol(i); rr == r && (best < 0 || abs32(g.target(i).Center().X-x) < abs32(g.target(best).Center().X-x)) {
				best = i
			}
		}
		return best
	}
	offColumn := 0
	for i := range g.n {
		r, c := g.lay.rowCol(i)
		for _, k := range []struct {
			key input.Key
			d   int
		}{{input.KeyDown, 1}, {input.KeyUp, -1}} {
			rr := min(max(r+k.d, 0), g.lay.rows()-1)
			want := nearest(rr, g.target(i).Center().X)
			if rr != r && want != g.lay.at(rr, c) {
				offColumn++
			}
			do(t, w, func(u *gunim.UI) { g.SetSelected([][2]int{{i, i + 1}}, i, u) })
			run(1)
			w.Input(input.KeyPress{Key: k.key, Time: time.Now()})
			run(1)
			if _, cur := g.Selected(); cur != want {
				t.Fatalf("%v from tile %d went to %d, want %d", k.key, i, cur, want)
			}
		}
	}
	if offColumn == 0 {
		t.Fatal("the nearest tile is always in the same column, so the test tests nothing")
	}
	// Left and Right go by index, across rows.
	r1 := g.lay.rowFirst(1)
	do(t, w, func(u *gunim.UI) { g.SetSelected([][2]int{{r1, r1 + 1}}, r1, u) })
	run(1)
	w.Input(input.KeyPress{Key: input.KeyLeft, Time: time.Now()})
	run(1)
	if _, cur := g.Selected(); cur != r1-1 {
		t.Fatalf("Left from the first of row 1 went to %d, want %d", cur, r1-1)
	}
	w.Input(input.KeyPress{Key: input.KeyEnd, Time: time.Now()})
	run(1)
	if _, cur := g.Selected(); cur != 59 {
		t.Fatalf("End went to %d", cur)
	}
	w.Input(input.KeyPress{Key: input.KeyPageUp, Time: time.Now()})
	run(1)
	if _, cur := g.Selected(); cur >= g.lay.rowFirst(g.lay.rows()-2) {
		t.Fatalf("Page Up from the last row went to %d, not up a page", cur)
	}
}

func TestTileGridJustifiedBuildsOnlyTheRowsInView(t *testing.T) {
	g, w, run := justStage(t, 10000, shapes)
	check := func() {
		t.Helper()
		if n := g.Built(); n == 0 || n > 80 {
			t.Fatalf("the grid built %d tiles of 10,000", n)
		}
		l, off := g.lay, g.Offset()
		r0, _ := l.rowAt(off)
		r1, _ := l.rowAt(off + 300)
		for i := range g.live {
			if r, _ := l.rowCol(i); r < r0-1 || r > r1+2 {
				t.Fatalf("tile %d, in row %d, is built with rows %d to %d in view", i, r, r0, r1)
			}
		}
	}
	check()
	if _, ok := g.live[0]; !ok {
		t.Fatal("tile 0 is not built")
	}
	onTiles(t, w, func(u *gunim.UI) { g.JumpToTile(5000, u) })
	run(2)
	if _, ok := g.live[5000]; !ok {
		t.Fatal("tile 5000 is not built after the jump")
	}
	check()
	press(w, g.TileRect(5000).Center(), 1, 0)
	w.Input(input.KeyPress{Key: input.KeyEnd, Time: time.Now()})
	run(90)
	if _, ok := g.live[9999]; !ok {
		t.Fatal("after End the last tile is not built")
	}
	check()
}

func TestTileGridJustifiedLaysOutManyTilesQuicklyAndOnce(t *testing.T) {
	calls := 0
	key := rowsKey{n: 200000, width: 1800, pad: 14, gap: 8, head: 40, caption: 24, size: geom.Sz(200, 180),
		starts: []int{0, 1000, 50000, 120000}}
	start := time.Now()
	l := newJustifiedLayout(key, func(i int) float32 { calls++; return shapes[i%len(shapes)] })
	if d := time.Since(start); d > 250*time.Millisecond {
		t.Fatalf("200,000 tiles took %v to lay out", d)
	}
	if calls != 200000 || l.rows() < 20000 {
		t.Fatalf("Aspect was asked %d times, for %d rows", calls, l.rows())
	}

	// The grid asks Aspect again only when the rows change.
	g, w, run := justStage(t, 10000, shapes)
	calls = 0
	g.Aspect = func(i int) float32 { calls++; return shapes[i%len(shapes)] }
	do(t, w, func(u *gunim.UI) { g.Reshape(u) })
	run(2)
	if calls != 10000 {
		t.Fatalf("Reshape asked Aspect %d times, want each tile once", calls)
	}
	g.ScrollTo(3000, Quick.Get(g.th))
	do(t, w, func(u *gunim.UI) { u.Invalidate() })
	run(30)
	if calls != 10000 || g.Offset() == 0 {
		t.Fatalf("scrolling to %v asked Aspect %d times in all", g.Offset(), calls)
	}
}

func TestTileGridJustifiedRowsBendAroundAWideTile(t *testing.T) {
	// A tile 60 wide and a panorama 1200 wide at the target height: shrunk to take it the row would be under
	// 0.6 times as tall, and grown to fill the width without it over 1.5, so the first tile stops at 1.5 and the
	// panorama takes a row alone, shrunk to the width.
	key := rowsKey{n: 3, width: 372, pad: 14, gap: 8, size: geom.Sz(60, 60)}
	l := newJustifiedLayout(key, func(i int) float32 { return []float32{1, 20, 1}[i] })
	if l.rows() != 3 {
		t.Fatalf("the tiles took %d rows, want 3", l.rows())
	}
	if r := l.target(0); r != geom.Rc(14, 14, 90, 90) {
		t.Fatalf("the first tile is at %v, want grown to 1.5 times", r)
	}
	if r := l.target(1); abs32(r.Min.X-14) > 0.01 || abs32(r.Max.X-386) > 0.01 || abs32(r.Size().H-372.0/20) > 0.01 {
		t.Fatalf("the panorama is at %v, want alone across the width", r)
	}
	if r := l.target(2); r.Size() != geom.Sz(60, 60) {
		t.Fatalf("the last tile is %v, want the target height", r.Size())
	}
	// No aspect falls back to the tile's picture shape, and a tiny one is kept to 1/20.
	l = newJustifiedLayout(key, func(i int) float32 { return []float32{0, 0.001, -1}[i] })
	if w := l.target(0).Size().W; w != 60 {
		t.Fatalf("a tile without an aspect is %v wide at the target, want 60", w)
	}
	if w := l.target(1).Size().W; w != 3 {
		t.Fatalf("a tile of aspect 0.001 is %v wide, want 3", w)
	}
}

func TestTileGridJustifiedTilesGlideToNewAspects(t *testing.T) {
	aspects := slices.Clone(shapes)
	g, w, run := justStage(t, 8, aspects)
	g.Aspect = func(i int) float32 { return aspects[i] }
	do(t, w, func(u *gunim.UI) { g.Reshape(u) })
	run(120)
	was := g.live[1].rect()
	aspects[0] = 0.5
	do(t, w, func(u *gunim.UI) { g.Reshape(u) })
	run(3)
	to := g.target(1)
	if to == was {
		t.Fatal("a new aspect for tile 0 left tile 1 where it was")
	}
	if now := g.live[1].rect(); now == was || now == to {
		t.Fatalf("three frames in, tile 1 is at %v, from %v to %v; want it on its way", now, was, to)
	}
	run(120)
	checkRows(t, g, g.Aspect)
	for i, tc := range g.live {
		if tc.rect() != g.target(i) {
			t.Fatalf("tile %d settled at %v, want %v", i, tc.rect(), g.target(i))
		}
	}
}

func TestTileGridSpringsBetweenColumnsAndJustifiedRows(t *testing.T) {
	g, w, run := tileStage(t, 20)
	run(60)
	was := g.live[5].rect()
	g.Aspect = func(i int) float32 { return shapes[i%len(shapes)] }
	do(t, w, func(u *gunim.UI) { u.Invalidate() })
	run(3)
	if now, to := g.live[5].rect(), g.target(5); now == was || now == to {
		t.Fatalf("three frames into justified rows, tile 5 is at %v, from %v to %v", now, was, to)
	}
	run(120)
	if g.live[5].rect() != g.target(5) {
		t.Fatal("tile 5 did not settle in its justified place")
	}
	g.Aspect = nil
	do(t, w, func(u *gunim.UI) { u.Invalidate() })
	run(120)
	if g.Columns() != 4 || g.live[5].rect() != was {
		t.Fatalf("back in %d columns tile 5 is at %v, want %v", g.Columns(), g.live[5].rect(), was)
	}
}

func TestTileGridJustifiedBandSelectsWhatItTouches(t *testing.T) {
	g, w, run := justStage(t, 7, shapes)
	run(120)
	if g.lay.content(8) > 280 {
		t.Fatalf("the tiles reach %v, with no room below them", g.lay.content(8))
	}
	from, to := geom.Pt(380, 290), g.TileRect(1).Center()
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	for _, p := range []geom.Point{{X: 300, Y: 200}, to} {
		w.Input(input.PointerMove{Pos: p, Time: time.Now()})
		run(1)
	}
	w.Input(input.PointerUp{Pos: to, Button: input.ButtonPrimary, Time: time.Now()})
	run(1)
	band := geom.Rect{Min: from, Max: to}.Normalized()
	var want [][2]int
	for i := range g.n {
		if r := g.target(i); r.Max.X >= band.Min.X && r.Min.X <= band.Max.X && r.Max.Y >= band.Min.Y && r.Min.Y <= band.Max.Y {
			want = addRun(want, i, i+1)
		}
	}
	if got := lastPicked(t, w).Sel; len(want) < 2 || !slices.Equal(got, want) {
		t.Fatalf("the band selected %v, want %v", got, want)
	}
}
