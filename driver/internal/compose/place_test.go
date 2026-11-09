package compose

import (
	"testing"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

func TestPlace(t *testing.T) {
	room := geom.Rect{Max: geom.Pt(400, 300)}
	tests := []struct {
		name        string
		anchor      geom.Rect
		size        geom.Size
		over, above bool
		room        geom.Rect
		want        geom.Point
	}{
		{name: "below, from its left edge", anchor: geom.Rc(50, 60, 40, 20), size: geom.Sz(100, 100), want: geom.Pt(50, 80)},
		{name: "exactly fits below", anchor: geom.Rc(50, 180, 40, 20), size: geom.Sz(100, 100), want: geom.Pt(50, 200)},
		{name: "above where there is more room there", anchor: geom.Rc(50, 250, 40, 20), size: geom.Sz(100, 100), want: geom.Pt(50, 150)},
		{name: "below where it fits neither way and there is more room below", anchor: geom.Rc(50, 40, 40, 20), size: geom.Sz(100, 280), want: geom.Pt(50, 60)},
		{name: "above where it asks and fits", anchor: geom.Rc(50, 150, 40, 20), size: geom.Sz(100, 100), above: true, want: geom.Pt(50, 50)},
		{name: "below where it asks for above and does not fit", anchor: geom.Rc(50, 30, 40, 20), size: geom.Sz(100, 100), above: true, want: geom.Pt(50, 50)},
		{name: "slid left onto the surface", anchor: geom.Rc(350, 60, 40, 20), size: geom.Sz(100, 100), want: geom.Pt(300, 80)},
		{name: "wider than the surface keeps its left edge on it", anchor: geom.Rc(50, 60, 40, 20), size: geom.Sz(500, 100), want: geom.Pt(0, 80)},
		{name: "over goes on the anchor, wherever that is", anchor: geom.Rc(390, 290, 40, 20), size: geom.Sz(100, 100), over: true, want: geom.Pt(390, 290)},
		{
			name: "the room left above a keyboard turns it upward", anchor: geom.Rc(50, 100, 40, 20), size: geom.Sz(100, 100),
			room: geom.Rect{Max: geom.Pt(400, 140)}, want: geom.Pt(50, 0),
		},
		{
			name: "room with insets slides it inside them", anchor: geom.Rc(0, 60, 40, 20), size: geom.Sz(100, 100),
			room: geom.Rect{Min: geom.Pt(30, 20), Max: geom.Pt(370, 280)}, want: geom.Pt(30, 80),
		},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			r := c.room
			if r.Empty() {
				r = room
			}
			if got := Place(c.anchor, c.size, c.over, c.above, r); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestPopupRoom(t *testing.T) {
	got := PopupRoom(geom.Rc(50, 60, 40, 20), geom.Rect{Min: geom.Pt(10, 20), Max: geom.Pt(400, 300)})
	want := driver.Room{Below: 220, Above: 40, Left: 40, Right: 350}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestHeldKeepsTheWindowUntilItsFrameComes(t *testing.T) {
	var h Held
	was := geom.Rect{Min: geom.Pt(1, 2), Max: geom.Pt(11, 12)}
	now := geom.Rect{Min: geom.Pt(3, 4), Max: geom.Pt(33, 44)}
	if got := h.Rect(was, false); got != was {
		t.Fatalf("holding nothing, got %v", got)
	}
	h.Resized(was)
	if got := h.Rect(now, false); got != was {
		t.Fatalf("with no frame, got %v, want %v", got, was)
	}
	// Told again before its frame comes, it still shows the frame it showed.
	h.Resized(now)
	if got := h.Rect(geom.Rect{Max: geom.Pt(50, 50)}, false); got != was {
		t.Fatalf("told twice, got %v, want %v", got, was)
	}
	if got := h.Rect(now, true); got != now {
		t.Fatalf("with its frame, got %v, want %v", got, now)
	}
	if got := h.Rect(now, false); got != now {
		t.Fatalf("after its frame, got %v, want %v", got, now)
	}
}
