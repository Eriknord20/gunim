// Package graph shows boxes joined by wires, on a plane the user pans
// and zooms: the modules of a synth, the routes of a mixer, the steps
// of a pipeline. Each box has inputs down its left edge and outputs
// down its right. A drag from a port draws a wire that snaps to a port
// it may join; a drag on a box moves it; a click picks a box or a
// wire, and Delete takes what is picked out.
//
// The package has two halves, as an application has. The [Graph] is
// plain data the application keeps: its boxes, their ports and the
// wires between them. The [View] shows it, and says what the user asks
// for as a [Change], which the application carries out, with
// Graph.Apply for the plain ones or in its own way, and then shows the
// graph again.
package graph

import (
	"image/color"
	"slices"

	"github.com/marrasen/gunim/geom"
)

// A Shape is how a port is drawn.
type Shape uint8

// The shapes of ports.
const (
	// Ring is a circle, as for sound.
	Ring Shape = iota
	// Diamond is a square on its corner, as for a control.
	Diamond
	// Bolt is a lightning bolt, as for triggers.
	Bolt
)

// A Port is where a wire joins a box.
type Port struct {
	// ID names the port among its box's inputs, or its outputs.
	ID string
	// Name is what the port says beside it.
	Name string
	// Kind is what the port carries, as "sound" or "control": a wire
	// joins an output to an input of the same kind, unless the view's
	// Accepts says otherwise.
	Kind string
	// Shape is how the port is drawn, and Color its colour; the zero
	// colour is the theme's PortInk.
	Shape Shape
	Color color.NRGBA
}

// A Node is a box on the plane.
type Node struct {
	// ID names the box.
	ID string
	// Title is the box's name, in its head, and Sub what it says under
	// the title, faintly.
	Title, Sub string
	// Color marks the box: a bar along its head, its glow, and its
	// wires' colour where a port has none.
	Color color.NRGBA
	// At is where the box's top left corner stands on the plane.
	At geom.Point
	// Width is how wide the box is, 0 for the theme's NodeWidth, and
	// Body how tall the room under its head is for what the view's
	// Content shows in it, 0 for none.
	Width, Body float32
	// Ins are the box's inputs, down its left edge, and Outs its
	// outputs, down its right.
	Ins, Outs []Port
	// Glow lights the box in its colour, 0 to 1, as a level rises.
	Glow float32
	// Dim draws the box faint, as one bypassed or muted.
	Dim bool
}

// An End is one end of a wire: a box, and a port of it.
type End struct {
	Node, Port string
}

// A Wire joins an output, From, to an input, To.
type Wire struct {
	// ID names the wire.
	ID       string
	From, To End
	// Color is the wire's colour; the zero colour is its output's.
	Color color.NRGBA
	// Thin draws the wire thinner, and Dashed in dashes, as for a
	// control rather than sound.
	Thin, Dashed bool
	// Flow runs dots along the wire, from 0 for none to 1, faster and
	// brighter as it rises, as the sound along it grows louder.
	Flow float32
	// Dim draws the wire faint, as one bypassed.
	Dim bool
}

// A Graph is the boxes and the wires a view shows.
type Graph struct {
	Nodes []Node
	Wires []Wire
}

// Node returns box id, or nil.
func (g *Graph) Node(id string) *Node {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i]
		}
	}
	return nil
}

// Wire returns wire id, or nil.
func (g *Graph) Wire(id string) *Wire {
	for i := range g.Wires {
		if g.Wires[i].ID == id {
			return &g.Wires[i]
		}
	}
	return nil
}

// Port returns the port e names, an output where out says, else an
// input; false where there is none.
func (g *Graph) Port(e End, out bool) (Port, bool) {
	n := g.Node(e.Node)
	if n == nil {
		return Port{}, false
	}
	ps := n.Ins
	if out {
		ps = n.Outs
	}
	for _, p := range ps {
		if p.ID == e.Port {
			return p, true
		}
	}
	return Port{}, false
}

// Joins reports whether a wire joins output from to input to.
func (g *Graph) Joins(from, to End) bool {
	return slices.ContainsFunc(g.Wires, func(w Wire) bool { return w.From == from && w.To == to })
}

// Clone returns a deep copy of g.
func (g *Graph) Clone() Graph {
	c := Graph{Nodes: slices.Clone(g.Nodes), Wires: slices.Clone(g.Wires)}
	for i := range c.Nodes {
		c.Nodes[i].Ins = slices.Clone(c.Nodes[i].Ins)
		c.Nodes[i].Outs = slices.Clone(c.Nodes[i].Outs)
	}
	return c
}

// Apply carries out c in the plainest way, and reports whether it
// changed g: Move moves the boxes; Connect adds a wire, named from its
// ends, where none joins them; Replug moves a wire's end; Delete takes
// out the wires and the boxes named, and the wires to those boxes.
// Select, Open, LinkOnNode and Refused change nothing. An application
// whose wires mean more carries out Connect, Replug and Delete in its
// own way.
func (g *Graph) Apply(c Change) bool {
	switch c := c.(type) {
	case Move:
		changed := false
		for id, at := range c.To {
			if n := g.Node(id); n != nil && n.At != at {
				n.At, changed = at, true
			}
		}
		return changed
	case Connect:
		if g.Joins(c.From, c.To) {
			return false
		}
		g.Wires = append(g.Wires, Wire{ID: WireID(c.From, c.To), From: c.From, To: c.To})
		return true
	case Replug:
		w := g.Wire(c.Wire)
		if w == nil || (w.From == c.From && w.To == c.To) || g.Joins(c.From, c.To) {
			return false
		}
		w.From, w.To = c.From, c.To
		return true
	case Delete:
		before := len(g.Nodes) + len(g.Wires)
		gone := map[string]bool{}
		for _, id := range c.Nodes {
			gone[id] = true
		}
		g.Nodes = slices.DeleteFunc(g.Nodes, func(n Node) bool { return gone[n.ID] })
		g.Wires = slices.DeleteFunc(g.Wires, func(w Wire) bool {
			return gone[w.From.Node] || gone[w.To.Node] || slices.Contains(c.Wires, w.ID)
		})
		return len(g.Nodes)+len(g.Wires) != before
	}
	return false
}

// WireID is the ID Apply names a wire from output from to input to.
func WireID(from, to End) string {
	return from.Node + "." + from.Port + ">" + to.Node + "." + to.Port
}

// A Change is what the user asks a [View] for. The view sends it to
// the application as an intent, or hands it to its OnChange.
type Change interface{ change() }

type (
	// Move puts boxes at new places on the plane: the user dragged
	// them. It comes once, as the drag ends; the boxes follow the
	// pointer in the view meanwhile.
	Move struct{ To map[string]geom.Point }
	// Connect asks for a wire from output From to input To: the user
	// drew one from a port and let it go on another, which the view's
	// Accepts let it join.
	Connect struct{ From, To End }
	// Replug asks for wire Wire to run from From to To: the user took
	// one of its ends to another port.
	Replug struct {
		Wire     string
		From, To End
	}
	// Delete asks to take boxes and wires out: the user pressed Delete
	// with them picked.
	Delete struct {
		Nodes, Wires []string
	}
	// Select says what the user picked: boxes, or a wire, or nothing.
	Select struct {
		Nodes []string
		Wire  string
	}
	// Open asks to open a box, as its settings: the user double
	// clicked it.
	Open struct{ Node string }
	// LinkOnNode says a wire drawn from From was let go on the body of
	// box Node, at At on the plane, short of any port: the application
	// may offer what it could join there, as a menu.
	LinkOnNode struct {
		From End
		// Out says From is an output.
		Out  bool
		Node string
		At   geom.Point
	}
	// Refused says a wire was let go on a port Accepts would not let it
	// join, and why, for the application to say.
	Refused struct {
		From, To End
		Why      string
	}
)

func (Move) change()       {}
func (Connect) change()    {}
func (Replug) change()     {}
func (Delete) change()     {}
func (Select) change()     {}
func (Open) change()       {}
func (LinkOnNode) change() {}
func (Refused) change()    {}
