package dock_test

import (
	"fmt"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/widget/dock"
)

// An application keeps the layout and changes it; a Dock in each window
// shows its part and says what the user asks for, which Apply carries
// out.
func Example() {
	// The application's half: three views as tabs of one group.
	l := dock.NewLayout(true, "editor", "terminal", "notes")

	// The user drags notes to the right edge of the editor's pane, then
	// picks the terminal's tab. The Dock sends these as intents.
	l.Apply(dock.Move{ID: "notes", To: dock.Target{At: "editor", Side: dock.Right}})
	l.Apply(dock.Show{ID: "terminal"})
	fmt.Println(l.Shown(0))

	// Out of every window: a window of its own.
	win, _ := l.ToWindow("notes", nil)
	fmt.Println(l.Shown(0), l.Shown(win))

	// The window's half: a dock showing window 0, holding a view for
	// each item, which the application sends each new layout.
	d := dock.NewDock(0)
	d.Content = newView
	d.SetLayout(l, nil)
	fmt.Println(d.Shown())

	// Output:
	// [terminal notes]
	// [terminal] [notes]
	// [terminal]
}

// newView stands for the application's view of an item.
func newView(string) gunim.Node { return nil }
