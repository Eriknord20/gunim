package widget

import (
	"strconv"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// block is a row of a fixed height.
type block struct {
	key Key
	h   float32
}

func (b *block) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Constrain(geom.Sz(c.Min.W, b.h))
}

func (b *block) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(Ink.Default()))
}

func keys(n int) []Key {
	out := make([]Key, n)
	for i := range out {
		out[i] = Key(strconv.Itoa(i))
	}
	return out
}

// shownKeys is the state of a view holding a virtual list.
type shownKeys struct{ Keys []Key }

// newVirtual mounts a 300 by 400 virtual list of the given keys, whose
// rows are height tall, and returns it with its window.
func newVirtual(t *testing.T, ks []Key, height float32) (*gunim.Window, *VirtualList, func(int)) {
	t.Helper()
	blocks := map[Key]*block{}
	l := NewVirtualList(func(k Key) gunim.Node {
		b := &block{key: k, h: height}
		blocks[k] = b
		return b
	})
	w := gunimtest.New(t, geom.Sz(300, 400), nil)
	gunim.RegisterView(w, "v", func(shownKeys) gunim.Node { return l },
		func(_ gunim.Node, s shownKeys, u *gunim.UI) { l.SetKeys(s.Keys, u) })
	if err := w.Client().Mount(gunim.Root, "v", "v", shownKeys{ks}); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(2)
	return w, l, run
}

func TestVirtualListBuildsOnlyWhatIsInView(t *testing.T) {
	w, l, run := newVirtual(t, keys(100_000), 40)
	// 400 of view and 300 beyond it below, at 46 a row.
	if b := l.Built(); b < 10 || b > 30 {
		t.Fatalf("%d rows built for a view of about 9, want a few dozen", b)
	}
	if l.Len() != 100_000 {
		t.Fatalf("Len %d, want 100000", l.Len())
	}
	for range 50 {
		w.Input(input.Scroll{Pos: geom.Pt(100, 100), Delta: geom.Pt(0, -4000)})
		run(2)
	}
	run(120)
	if o := l.Offset(); o < 150_000 {
		t.Fatalf("scrolled to %v, want far down", o)
	}
	if b := l.Built(); b < 10 || b > 40 {
		t.Fatalf("%d rows built after scrolling, want a few dozen", b)
	}
	for k := range l.live {
		i := keyIndex(t, k)
		if y := float32(i) * 46; y < l.Offset()-400 || y > l.Offset()+800 {
			t.Fatalf("row %d, at %v, is built far from the view at %v", i, y, l.Offset())
		}
	}
}

func TestVirtualListAnimatesARemovedRowInView(t *testing.T) {
	w, l, run := newVirtual(t, keys(50), 40)
	r := l.live["2"]
	if err := w.Client().Update("v", shownKeys{append(keys(2), keys(50)[3:]...)}); err != nil {
		t.Fatal(err)
	}
	run(3)
	if _, ok := l.live["2"]; !ok || r.height.Value() >= 40 || r.height.Value() <= 0 {
		t.Fatalf("row 2 is not collapsing: height %v", r.height.Value())
	}
	run(120)
	if _, ok := l.live["2"]; ok || l.Len() != 49 {
		t.Fatalf("row 2 stayed, or the list holds %d", l.Len())
	}
	// Row 3 has closed the gap.
	if y := l.live["3"].y.Value(); y != 2*46 {
		t.Fatalf("row 3 at %v, want 92", y)
	}
}

func TestVirtualListKeepsTheViewStillAsRowsAboveAreMeasured(t *testing.T) {
	// Rows are 80 tall, where the list assumes 40, so every row built
	// above the view moves the rows below it.
	_, l, run := newVirtual(t, keys(1000), 80)
	l.ScrollTo(20_000, Quick.Default())
	run(120)
	// Take the first row in view, then scroll up, which builds rows
	// above it. It should move down the screen by the scroll alone.
	first, before := firstInView(l)
	l.ScrollTo(l.Offset()-300, Quick.Default())
	run(120)
	after := l.live[first].y.Value() - l.Offset()
	if d := after - before; d < 299 || d > 301 {
		t.Fatalf("row %s moved %v down the screen for a scroll of 300", first, d)
	}
	// And the rows in view sit a measured row apart.
	a, b := l.live[first], l.live[nextKey(t, first)]
	if gap := b.y.Value() - a.y.Value(); gap != 86 {
		t.Fatalf("rows in view are %v apart, want 86", gap)
	}
}

// firstInView returns the key of the topmost row in view, and its top
// on screen.
func firstInView(l *VirtualList) (first Key, top float32) {
	top = 1e9
	for k, r := range l.live {
		if y := r.y.Value() - l.Offset(); y >= 0 && y < top {
			first, top = k, y
		}
	}
	return first, top
}

func keyIndex(t *testing.T, k Key) int {
	t.Helper()
	i, err := strconv.Atoi(string(k))
	if err != nil {
		t.Fatalf("key %q is no number", k)
	}
	return i
}

func nextKey(t *testing.T, k Key) Key { return Key(strconv.Itoa(keyIndex(t, k) + 1)) }

func TestFenwickMatchesPlainSums(t *testing.T) {
	vals := []float32{3, 0, 5, 2, 0, 0, 7, 1, 4}
	var f fenwick
	f.reset(vals)
	f.add(4, 6)
	vals[4] += 6
	run := float64(0)
	for i, v := range vals {
		if got := f.sum(i); got != run {
			t.Fatalf("sum(%d) = %v, want %v", i, got, run)
		}
		run += float64(v)
	}
	for y := 0.0; y < run+2; y += 0.5 {
		want, acc := len(vals), 0.0
		for i, v := range vals {
			acc += float64(v)
			if acc > y {
				want = i
				break
			}
		}
		if got := f.find(y); got != want {
			t.Fatalf("find(%v) = %d, want %d", y, got, want)
		}
	}
}

func TestVirtualListGrowsAnAddedRowInView(t *testing.T) {
	w, l, run := newVirtual(t, keys(50), 40)
	ks := append([]Key{"new"}, keys(50)...)
	if err := w.Client().Update("v", shownKeys{ks}); err != nil {
		t.Fatal(err)
	}
	run(3)
	r, ok := l.live["new"]
	if !ok || r.instant || r.height.Value() <= 0 || r.height.Value() >= 40 {
		t.Fatalf("the new row is not growing in: built %v", ok)
	}
	run(120)
	if y := l.live["0"].y.Value(); y != 46 {
		t.Fatalf("row 0 at %v after the new row opened, want 46", y)
	}
}

// BenchmarkVirtualListFrame times a frame of a 100,000-row list
// scrolling, with rows built and dropped as they pass.
func BenchmarkVirtualListFrame(b *testing.B) {
	blocksT := &testing.T{}
	w, l, _ := newVirtual(blocksT, keys(100_000), 40)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		w.Input(input.Scroll{Pos: geom.Pt(100, 100), Delta: geom.Pt(0, -60)})
		w.Frame(time.Second / 60)
		_ = i
	}
	b.ReportMetric(float64(l.Built()), "rows/frame")
}

func TestVirtualListHoldsTheViewAsItemsArriveAbove(t *testing.T) {
	w, l, run := newVirtual(t, keys(1000), 40)
	l.ScrollTo(4600, Quick.Default())
	run(120)
	first, before := firstInView(l)
	ks := append([]Key{"a", "b", "c"}, keys(1000)...)
	if err := w.Client().Update("v", shownKeys{ks}); err != nil {
		t.Fatal(err)
	}
	run(60)
	if after := l.live[first].y.Value() - l.Offset(); after != before {
		t.Fatalf("row %s moved from %v to %v on screen as items arrived above", first, before, after)
	}
}

// scrollBy sends n wheel notches, up for negative n.
func scrollBy(w *gunim.Window, run func(int), n int) {
	step := float32(-120)
	if n < 0 {
		step, n = 120, -n
	}
	for range n {
		w.Input(input.Scroll{Pos: geom.Pt(100, 100), Delta: geom.Pt(0, step)})
		run(2)
	}
}

func TestVirtualListShowsAnItemAddedAtTheTopAfterScrollingBack(t *testing.T) {
	w, l, run := newVirtual(t, keys(1000), 40)
	scrollBy(w, run, 20)
	run(60)
	scrollBy(w, run, -30)
	run(120)
	if err := w.Client().Update("v", shownKeys{append([]Key{"new"}, keys(1000)...)}); err != nil {
		t.Fatal(err)
	}
	run(120)
	if r, ok := l.live["new"]; !ok || l.Offset() != 0 || r.y.Value() != 0 {
		t.Fatalf("the new row is built %v, the list at %v; want it at the top in view", ok, l.Offset())
	}
}

// screenTop returns where row k sits on screen.
func screenTop(l *VirtualList, k Key) float32 { return l.live[k].y.Value() - l.Offset() }

func TestVirtualListNeverJumpsAsARemovedRowCloses(t *testing.T) {
	w, l, run := newVirtual(t, keys(1000), 40)
	// Row 20 part way under the top edge, as a wheel leaves it, and
	// row 21, just below it, removed.
	l.ScrollTo(46*20+6, Quick.Default())
	run(120)
	offset := l.Offset()
	ks := keys(1000)
	if err := w.Client().Update("v", shownKeys{append(ks[:21:21], ks[22:]...)}); err != nil {
		t.Fatal(err)
	}
	last := screenTop(l, "22")
	for range 120 {
		run(1)
		now := screenTop(l, "22")
		if now > last+0.01 {
			t.Fatalf("row 22 moved down the screen, from %v to %v, while the row above it closed", last, now)
		}
		// The closed row stops a hair above nothing, and that hair may
		// move the list as the row goes; a jump would be the spacing.
		if d := l.Offset() - offset; d > 0.5 || d < -0.5 {
			t.Fatalf("the list moved from %v to %v as a row in view closed", offset, l.Offset())
		}
		last = now
	}
	if want := float32(46*21) - l.Offset(); last-want > 0.01 || want-last > 0.01 {
		t.Fatalf("row 22 settled at %v on screen, want %v", last, want)
	}
}

func TestVirtualListHoldsEveryFrameStillAsItemsArriveAbove(t *testing.T) {
	w, l, run := newVirtual(t, keys(1000), 80)
	l.ScrollTo(4600, Quick.Default())
	run(120)
	first, before := firstInView(l)
	if err := w.Client().Update("v", shownKeys{append([]Key{"a", "b", "c"}, keys(1000)...)}); err != nil {
		t.Fatal(err)
	}
	for range 60 {
		run(1)
		if now := screenTop(l, first); now != before {
			t.Fatalf("row %s moved on screen from %v to %v", first, before, now)
		}
	}
	// Scrolling up builds the new rows, 80 tall where 40 is assumed.
	l.ScrollTo(l.Offset()-200, Quick.Default())
	prev := screenTop(l, first)
	for range 120 {
		run(1)
		now := screenTop(l, first)
		// The scroll's spring may overshoot a hair as it settles; a
		// row measured above the view would jump by tens of pixels.
		if now < prev-1 {
			t.Fatalf("row %s jumped up the screen, from %v to %v, while scrolling up", first, prev, now)
		}
		prev = now
	}
}

func TestVirtualListRowFindsOnlyBuiltRows(t *testing.T) {
	_, l, _ := newVirtual(t, keys(1000), 40)
	n, ok := l.Row("0")
	if b, isBlock := n.(*block); !ok || !isBlock || b.key != "0" {
		t.Fatalf("Row(0) = %v, %v, want the first row's block", n, ok)
	}
	if n, ok := l.Row("999"); ok {
		t.Fatalf("Row(999) = %v for a row out of view, want none", n)
	}
}

// newStuckVirtual is newVirtual for a list that sticks to its end.
func newStuckVirtual(t *testing.T, ks []Key, height float32) (*gunim.Window, *VirtualList, func(int)) {
	t.Helper()
	l := NewVirtualList(func(k Key) gunim.Node { return &block{key: k, h: height} })
	l.StickToEnd = true
	w := gunimtest.New(t, geom.Sz(300, 400), nil)
	gunim.RegisterView(w, "v", func(shownKeys) gunim.Node { return l },
		func(_ gunim.Node, s shownKeys, u *gunim.UI) { l.SetKeys(s.Keys, u) })
	if err := w.Client().Mount(gunim.Root, "v", "v", shownKeys{ks}); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(2)
	return w, l, run
}

// wantEnd fails unless the list's last row, key last and 60 tall, sits at the bottom of the 400 tall view.
func wantEnd(t *testing.T, l *VirtualList, last Key) {
	t.Helper()
	r, ok := l.live[last]
	if !ok {
		t.Fatalf("the last row, %s, is not built", last)
	}
	if bottom := r.y.Value() + 60 - l.Offset(); abs32(bottom-400) > 0.5 {
		t.Fatalf("the last row ends at %v in the view, want 400", bottom)
	}
}

func TestVirtualListThatSticksStartsAtTheEnd(t *testing.T) {
	// Rows are 60 tall against an estimate of 40, so the end moves as rows are measured.
	_, l, run := newStuckVirtual(t, keys(10_000), 60)
	run(10)
	wantEnd(t, l, "9999")
}

func TestVirtualListThatSticksFollowsNewRows(t *testing.T) {
	w, l, run := newStuckVirtual(t, keys(100), 60)
	if err := w.Client().Update("v", shownKeys{keys(103)}); err != nil {
		t.Fatal(err)
	}
	run(120)
	wantEnd(t, l, "102")
	if !l.AtEnd() {
		t.Fatal("AtEnd is false at the end")
	}
}

func TestVirtualListThatSticksStaysPutWhenScrolledUp(t *testing.T) {
	w, l, run := newStuckVirtual(t, keys(100), 60)
	run(10)
	w.Input(input.Scroll{Pos: geom.Pt(100, 100), Delta: geom.Pt(0, 500)})
	run(120)
	at := l.Offset()
	if l.AtEnd() {
		t.Fatalf("AtEnd at %v after scrolling up", at)
	}
	if err := w.Client().Update("v", shownKeys{keys(103)}); err != nil {
		t.Fatal(err)
	}
	run(120)
	if l.Offset() != at {
		t.Fatalf("offset moved from %v to %v as rows arrived below", at, l.Offset())
	}
	l.ScrollToEnd(Quick.Default())
	run(120)
	wantEnd(t, l, "102")
}

func TestVirtualListThatSticksLetsADragHoldIt(t *testing.T) {
	w, l, run := newStuckVirtual(t, keys(100), 60)
	l.DragScroll = true
	run(10)
	w.Input(input.PointerDown{Pos: geom.Pt(100, 200), Button: input.ButtonPrimary, Clicks: 1})
	run(2)
	at := l.Offset()
	if err := w.Client().Update("v", shownKeys{keys(103)}); err != nil {
		t.Fatal(err)
	}
	run(60)
	if l.Offset() != at {
		t.Fatalf("offset moved from %v to %v under a held drag", at, l.Offset())
	}
	w.Input(input.PointerUp{Pos: geom.Pt(100, 200), Button: input.ButtonPrimary})
	run(120)
}

func TestAListThatSticksCanOpenAtARow(t *testing.T) {
	l := NewVirtualList(func(k Key) gunim.Node { return &block{key: k, h: 60} })
	l.StickToEnd = true
	l.Estimate = 60
	l.OpenAt("50")
	w := gunimtest.New(t, geom.Sz(300, 400), nil)
	gunim.RegisterView(w, "v", func(shownKeys) gunim.Node { return l },
		func(_ gunim.Node, s shownKeys, u *gunim.UI) { l.SetKeys(s.Keys, u) })
	if err := w.Client().Mount(gunim.Root, "v", "v", shownKeys{keys(100)}); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		w.Frame(time.Second / 60)
	}
	if want := float32(50 * 66); abs32(l.Offset()-want) > 0.5 {
		t.Fatalf("offset %v, want row 50 at the top, %v", l.Offset(), want)
	}
	if l.AtEnd() {
		t.Fatal("the view opened at the end")
	}
	// Rows arriving below leave it where it is.
	if err := w.Client().Update("v", shownKeys{keys(103)}); err != nil {
		t.Fatal(err)
	}
	for range 60 {
		w.Frame(time.Second / 60)
	}
	if want := float32(50 * 66); abs32(l.Offset()-want) > 0.5 {
		t.Fatalf("offset %v after rows arrived, want it to stay at %v", l.Offset(), want)
	}
}

// olderAsked is the intent the tests' timelines send for older items.
type olderAsked struct{}

func TestVirtualListLoadsOlderItemsAtTheTopWithoutMoving(t *testing.T) {
	l := NewVirtualList(func(k Key) gunim.Node { return &block{key: k, h: 40} })
	l.StickToEnd, l.HoldOnPrepend = true, true
	l.OnReachStart = func(u *gunim.UI) gunim.Intent { return olderAsked{} }
	w := gunimtest.New(t, geom.Sz(300, 400), nil)
	gunim.RegisterView(w, "v", func(shownKeys) gunim.Node { return l },
		func(_ gunim.Node, s shownKeys, u *gunim.UI) { l.SetKeys(s.Keys, u) })
	recent := keys(60)[30:]
	if err := w.Client().Mount(gunim.Root, "v", "v", shownKeys{recent}); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(5)
	asked := func() int {
		n := 0
		for {
			select {
			case e := <-w.Client().Intents():
				if e.Intent == (olderAsked{}) {
					n++
				}
			default:
				return n
			}
		}
	}
	if n := asked(); n != 0 {
		t.Fatalf("at the end, the list asked for older items %d times", n)
	}
	// Scroll to the very top.
	scrollBy(w, run, -40)
	run(90)
	if l.Offset() != 0 {
		t.Fatalf("the list is at %v, want its top", l.Offset())
	}
	if n := asked(); n != 1 {
		t.Fatalf("at the top, the list asked for older items %d times, want once", n)
	}
	first := screenTop(l, "30")
	if err := w.Client().Update("v", shownKeys{keys(60)}); err != nil {
		t.Fatal(err)
	}
	run(60)
	if _, built := l.live["30"]; !built {
		t.Fatal("the first row went out of view as older items arrived above it")
	}
	if after := screenTop(l, "30"); after != first {
		t.Fatalf("the first row moved from %v to %v on screen as older items arrived above it", first, after)
	}
	if _, built := l.live["29"]; built && l.live["29"].fade.Value() < 1 {
		t.Fatal("an older item above the view grows in")
	}
	// Scrolling up to the new top asks again.
	scrollBy(w, run, -40)
	run(90)
	if n := asked(); n != 1 {
		t.Fatalf("back at the top, the list asked %d times, want once more", n)
	}
}

// mountTimeline mounts a 300 by 400 list of 40-pixel rows that sticks to its end and asks for older items, holding
// its view as they arrive when hold is set, and returns a way to count the asks.
func mountTimeline(t *testing.T, hold bool, ks []Key) (w *gunim.Window, list *VirtualList, run func(int), built func() int) {
	t.Helper()
	list = NewVirtualList(func(k Key) gunim.Node { return &block{key: k, h: 40} })
	list.StickToEnd, list.HoldOnPrepend = true, hold
	list.OnReachStart = func(u *gunim.UI) gunim.Intent { return olderAsked{} }
	w = gunimtest.New(t, geom.Sz(300, 400), nil)
	gunim.RegisterView(w, "v", func(shownKeys) gunim.Node { return list },
		func(_ gunim.Node, s shownKeys, u *gunim.UI) { list.SetKeys(s.Keys, u) })
	if err := w.Client().Mount(gunim.Root, "v", "v", shownKeys{ks}); err != nil {
		t.Fatal(err)
	}
	run = func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(5)
	asked := func() int {
		n := 0
		for {
			select {
			case e := <-w.Client().Intents():
				if e.Intent == (olderAsked{}) {
					n++
				}
			default:
				return n
			}
		}
	}
	return w, list, run, asked
}

func TestVirtualListHoldsTheRowReadAsADayHeadingMovesUp(t *testing.T) {
	// The heading over the day's first shown message is keyed by it, so the older page brings a new one.
	w, l, run, _ := mountTimeline(t, true, append([]Key{"day:30"}, keys(60)[30:]...))
	scrollBy(w, run, -40)
	run(90)
	before := screenTop(l, "30")
	older := append(append(append([]Key{"day:0"}, keys(20)...), "day:20"), keys(60)[20:]...)
	if err := w.Client().Update("v", shownKeys{older}); err != nil {
		t.Fatal(err)
	}
	run(60)
	if _, built := l.live["30"]; !built {
		t.Fatal("the row read went out of view")
	}
	if after := screenTop(l, "30"); after != before {
		t.Fatalf("the row read moved from %v to %v on screen", before, after)
	}
}

func TestVirtualListShorterThanItsViewAsksForOlderItems(t *testing.T) {
	_, _, run, asked := mountTimeline(t, true, keys(5))
	run(3)
	if n := asked(); n != 1 {
		t.Fatalf("a list shorter than its view asked for older items %d times, want once", n)
	}
}

func TestVirtualListWithoutHoldingAsksAgainAfterOlderItems(t *testing.T) {
	w, _, run, asked := mountTimeline(t, false, keys(60)[40:])
	scrollBy(w, run, -40)
	run(60)
	if n := asked(); n != 1 {
		t.Fatalf("at the top the list asked %d times, want once", n)
	}
	if err := w.Client().Update("v", shownKeys{keys(60)[20:]}); err != nil {
		t.Fatal(err)
	}
	run(10)
	scrollBy(w, run, -40)
	run(60)
	if n := asked(); n != 1 {
		t.Fatalf("back at the top after older items came, the list asked %d times, want once more", n)
	}
}
