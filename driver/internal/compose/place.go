package compose

import (
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

// Place returns where a popup of the given size goes by anchor, in the
// surface's logical pixels: below it from its left edge, or above it where
// there is more room there or above asks for it and it fits, slid sideways
// into room, the part of the surface a popup may use. over puts the popup
// on the anchor's top left instead.
func Place(anchor geom.Rect, size geom.Size, over, above bool, room geom.Rect) geom.Point {
	if over {
		return anchor.Min
	}
	below, overhead := room.Max.Y-anchor.Max.Y, anchor.Min.Y-room.Min.Y
	y := anchor.Max.Y
	if above && overhead >= size.H || size.H > below && overhead > below {
		y = anchor.Min.Y - size.H
	}
	x := max(room.Min.X, min(anchor.Min.X, room.Max.X-size.W))
	return geom.Pt(x, y)
}

// PopupRoom returns the room from anchor, in the surface's logical pixels,
// to the edges of room, the part of the surface a popup may use.
func PopupRoom(anchor, room geom.Rect) driver.Room {
	return driver.Room{
		Below: room.Max.Y - anchor.Max.Y,
		Above: anchor.Min.Y - room.Min.Y,
		Left:  anchor.Min.X - room.Min.X,
		Right: room.Max.X - anchor.Min.X,
	}
}
