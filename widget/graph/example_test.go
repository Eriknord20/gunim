package graph_test

import (
	"fmt"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/widget/graph"
)

// An application keeps the graph and changes it; a View shows it and
// says what the user asks for, which Apply carries out where it is
// plain.
func Example() {
	g := graph.Graph{Nodes: []graph.Node{
		{ID: "osc", Title: "Oscillator", Outs: []graph.Port{{ID: "out", Kind: "sound"}}},
		{ID: "out", Title: "Speakers", At: geom.Pt(300, 0), Ins: []graph.Port{{ID: "in", Kind: "sound"}}},
	}}

	// The user draws a wire from the oscillator to the speakers, then
	// moves the speakers down. The View sends these as intents.
	g.Apply(graph.Connect{From: graph.End{Node: "osc", Port: "out"}, To: graph.End{Node: "out", Port: "in"}})
	g.Apply(graph.Move{To: map[string]geom.Point{"out": geom.Pt(300, 120)}})
	fmt.Println(len(g.Wires), g.Node("out").At)

	// The window's half shows it.
	v := graph.NewView()
	v.SetGraph(g, nil)
	fmt.Println(len(v.Graph().Nodes))

	// Output:
	// 1 {300 120}
	// 2
}
