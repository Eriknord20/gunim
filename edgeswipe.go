package gunim

import (
	"slices"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

// An EdgeSwipeKeeper is a node that keeps swipes in from the screen's edges for itself, where the system would take
// them as its own gestures: on Android, a game steered by dragging keeps a band down the left and right edges, so a
// drag begun at the edge steers the game, and back takes the swipes elsewhere. EdgeSwipes returns the bands, in the
// node's own space at size, the size it was laid out at; none gives the edges back.
//
// The engine asks after each frame, while the node is in the tree and drawn, and hands the window the bands of
// every such node, moved into the window's space. They follow the node: as it moves or resizes, as on rotation, the
// bands move with it, and as it leaves the tree or the window hides, the edges go back to the system. A node whose
// bands change between frames asks for one with [UI.Invalidate].
//
// Android honours at most 200 dp of each side's height, and a band reaches the edge to keep its swipes. On Android
// before 10, and with 3-button navigation, back is a button, and the bands change nothing; a desktop takes no
// swipes from the edges, and the bands change nothing there either.
type EdgeSwipeKeeper interface {
	Node
	EdgeSwipes(size geom.Size) []geom.Rect
}

// noteEdgeSwipeNode notes s while its node can keep edge swipes, so a frame walks the tree for them only while it
// holds one. [UI.forget] lets it go.
func (u *UI) noteEdgeSwipeNode(s *state) {
	if _, ok := s.node.(EdgeSwipeKeeper); !ok {
		return
	}
	if u.edgeSwipeNodes == nil {
		u.edgeSwipeNodes = map[*state]struct{}{}
	}
	u.edgeSwipeNodes[s] = struct{}{}
}

// keepEdgeSwipes hands the window, and each popup, the bands the nodes drawn in it keep from the system's edge
// gestures, where they changed since the last it handed. It runs after each frame, which settles what is shown and
// where, and as the window hides, which gives every edge back.
func (u *UI) keepEdgeSwipes() {
	out := u.w.hidden || u.goingAway
	u.edgeSwipes = u.sendEdgeSwipes(u.w.dw, u.root, u.edgeSwipes, out)
	for _, p := range u.popups {
		if p.dw != nil && !p.closing {
			p.edgeSwipes = u.sendEdgeSwipes(p.dw, p.root, p.edgeSwipes, out)
		}
	}
}

// sendEdgeSwipes hands dw the bands the nodes under root keep, or none where out is set, if they differ from last,
// what dw was handed before, and returns them.
func (u *UI) sendEdgeSwipes(dw driver.Window, root *state, last []geom.Rect, out bool) []geom.Rect {
	var bands []geom.Rect
	if !out && len(u.edgeSwipeNodes) > 0 {
		bands = u.gatherEdgeSwipes(root, nil)
	}
	if slices.Equal(bands, last) {
		return last
	}
	if k, ok := dw.(driver.EdgeSwipeKeeper); ok {
		k.KeepEdgeSwipes(bands)
	}
	return bands
}

// gatherEdgeSwipes adds the bands s and the nodes beneath it keep, in the window's space, to bands, skipping what
// the last frame left undrawn and what is leaving.
func (u *UI) gatherEdgeSwipes(s *state, bands []geom.Rect) []geom.Rect {
	if s.presence == Exiting || s.drawn != u.seq {
		return bands
	}
	if k, ok := s.node.(EdgeSwipeKeeper); ok {
		for _, r := range k.EdgeSwipes(s.size) {
			if !r.Empty() {
				bands = append(bands, s.screenRect(r))
			}
		}
	}
	for _, k := range s.kids {
		bands = u.gatherEdgeSwipes(k, bands)
	}
	return bands
}
