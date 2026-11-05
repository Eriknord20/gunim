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

// Held keeps a window at the place and size of the frame it shows, from the
// moment it is told a new size until a frame at that size comes. A window always
// presents after it changes size. Until then, laying it at the new size would
// draw its old frame whole a first time, only to draw it again a moment later,
// and a popup animating its size would pay that every frame.
//
// The zero Held holds nothing.
type Held struct {
	on   bool
	rect geom.Rect
}

// Resized notes the window has a new size, and lay at was with the frame it shows.
func (h *Held) Resized(was geom.Rect) {
	if !h.on {
		h.on, h.rect = true, was
	}
}

// Rect returns where a frame lays the window, which is at now: its place and size
// as told. frame says the frame draws a new frame of the window, which ends the
// hold.
func (h *Held) Rect(now geom.Rect, frame bool) geom.Rect {
	if !h.on {
		return now
	}
	if frame {
		h.on = false
		return now
	}
	return h.rect
}
