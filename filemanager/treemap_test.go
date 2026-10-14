package filemanager

import (
	"math"
	"testing"

	"github.com/marrasen/gunim/geom"
)

// TestSquarifyFillsTheRoomInProportion checks each value gets its share
// of the room, the boxes fill it without overlapping, and none is far
// from square.
func TestSquarifyFillsTheRoomInProportion(t *testing.T) {
	values := []float64{6, 6, 4, 3, 2, 2, 1}
	r := geom.Rc(10, 20, 600, 400)
	rs := squarify(values, r)
	total := 24.0
	area := float64(600 * 400)
	sum := 0.0
	for i, b := range rs {
		a := float64(b.Size().W * b.Size().H)
		sum += a
		if want := values[i] / total * area; math.Abs(a-want) > 1 {
			t.Errorf("box %d has area %.1f, want %.1f", i, a, want)
		}
		if b.Min.X < r.Min.X-0.01 || b.Min.Y < r.Min.Y-0.01 || b.Max.X > r.Max.X+0.01 || b.Max.Y > r.Max.Y+0.01 {
			t.Errorf("box %d, %v, is outside %v", i, b, r)
		}
		for j := range i {
			o := rs[j]
			if min(b.Max.X, o.Max.X)-max(b.Min.X, o.Min.X) > 0.01 && min(b.Max.Y, o.Max.Y)-max(b.Min.Y, o.Min.Y) > 0.01 {
				t.Errorf("boxes %d and %d overlap: %v and %v", i, j, b, rs[j])
			}
		}
		if ratio := max(b.Size().W/b.Size().H, b.Size().H/b.Size().W); ratio > 4 {
			t.Errorf("box %d is %v, %.1f times longer than wide", i, b.Size(), ratio)
		}
	}
	if math.Abs(sum-area) > 2 {
		t.Errorf("the boxes take %.1f of %.1f", sum, area)
	}
}

// TestSquarifyLeavesOutWhatIsEmpty checks a value of nothing gets no box.
func TestSquarifyLeavesOutWhatIsEmpty(t *testing.T) {
	rs := squarify([]float64{5, 0}, geom.Rc(0, 0, 100, 100))
	if rs[1].Size().W*rs[1].Size().H != 0 {
		t.Errorf("an empty value got %v", rs[1])
	}
	if rs[0] != geom.Rc(0, 0, 100, 100) {
		t.Errorf("the one value got %v, want all of the room", rs[0])
	}
}

// TestLayoutMapNestsAFoldersItemsInItsBox checks a folder's items lie
// inside its box, under the strip with its name.
func TestLayoutMapNestsAFoldersItemsInItsBox(t *testing.T) {
	items := []UsageItem{
		{Key: "", Dir: true, Size: 100, Parent: -1},
		{Key: "a", Dir: true, Size: 60, Parent: 0},
		{Key: "a/x", Size: 40, Parent: 1},
		{Key: "a/y", Size: 20, Parent: 1},
		{Key: "b", Size: 40, Parent: 0},
	}
	bs := layoutMap(items, MeasureAll, geom.Rc(0, 0, 500, 300))
	a := bs[1]
	if !a.head {
		t.Fatalf("folder a's box %v has no strip for its name", a.r)
	}
	for _, i := range []int{2, 3} {
		r := bs[i].r
		if r.Min.Y < a.r.Min.Y+mapHead-0.01 || r.Min.X < a.r.Min.X || r.Max.X > a.r.Max.X || r.Max.Y > a.r.Max.Y {
			t.Errorf("item %s at %v is not inside folder a's box %v under its strip", items[i].Key, r, a.r)
		}
		if bs[i].depth != 2 {
			t.Errorf("item %s is %d deep, want 2", items[i].Key, bs[i].depth)
		}
	}
}
