package driver

import "github.com/marrasen/gunim/geom"

// An EdgeSwipeKeeper is a [Window] on a system that takes swipes in from the screen's edges as its own gestures, as
// Android's back gesture is. KeepEdgeSwipes keeps such swipes for the program within bands, in the window's units,
// in place of the bands the window kept before; an empty call gives the window's edges back to the system. Each
// window keeps its own bands while it is open and shown, and the system takes the union of them. The system may
// honour part of them: Android at most 200 dp of each side's height. On Android before 10, and with 3-button
// navigation, back is a button, and the call changes nothing. It may be called from any goroutine.
type EdgeSwipeKeeper interface {
	KeepEdgeSwipes(bands []geom.Rect)
}
