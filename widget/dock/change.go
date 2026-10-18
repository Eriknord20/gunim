package dock

import "github.com/marrasen/gunim/geom"

// A Change is what the user asks a [Dock] for. The dock sends it to the
// application as an intent, or hands it to its OnChange. Apply carries
// out those that change the layout alone; the rest are the
// application's.
type Change interface{ change() }

type (
	// Show brings a tab to the front: the user picked it.
	Show struct{ ID string }
	// Move moves a node, with all it holds, as Layout.Move does: the
	// user dragged its tab there.
	Move struct {
		ID string
		To Target
	}
	// Resize sets a split's share of the room: the user let go of its
	// divider.
	Resize struct {
		ID    string
		Share float32
	}
	// Close asks to close a tab and all it holds: the user pressed its ×
	// or clicked it with the middle button. The application decides, and
	// Apply takes it out of the layout.
	Close struct{ ID string }
	// Detach asks for a window of its own for a node: the user let its
	// tab go outside every window. At is where the new window's top left
	// corner goes, in the space of window From, and Size how large it
	// is. The application opens the window and calls Layout.ToWindow.
	Detach struct {
		ID   string
		From int
		At   geom.Point
		Size geom.Size
	}
	// New asks for a new tab in group Group of window Win: the user
	// pressed the + after its tabs. The application makes the item and
	// adds it.
	New struct {
		Group string
		Win   int
	}
)

func (Show) change()   {}
func (Move) change()   {}
func (Resize) change() {}
func (Close) change()  {}
func (Detach) change() {}
func (New) change()    {}

// Apply carries out c where it changes the layout alone, Show, Move,
// Resize and Close, and reports whether it changed l. Detach and New
// are the application's to carry out.
func (l *Layout) Apply(c Change) bool {
	switch c := c.(type) {
	case Show:
		n := l.Node(c.ID)
		if n == nil || l.Shows(c.ID) {
			return false
		}
		l.Show(c.ID)
		return true
	case Move:
		return l.Move(c.ID, c.To)
	case Resize:
		n := l.Node(c.ID)
		if n == nil || n.Share == c.Share {
			return false
		}
		return l.SetShare(c.ID, c.Share)
	case Close:
		return l.Remove(c.ID)
	}
	return false
}
