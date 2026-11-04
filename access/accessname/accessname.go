// Package accessname finds controls in a window's accessibility tree by
// their names, as a person reading the window would: "Reinforce", or
// "Snare top > Solo" for a Solo inside a Snare top. A test or a script
// that drives a window from outside finds what to act on with it.
package accessname

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
)

// A Query is a name to find.
type Query struct {
	// Path is the names, in lower case, from the outermost: its last
	// the control's own, the ones before it its ancestors', in order. A
	// name ending in "*" matches the names that start with what is
	// before the "*".
	Path []string
	// Nth is which match the query means, from 1.
	Nth int
	// Loose lets names that hold the query's names match, where no name
	// matches whole.
	Loose bool
}

// Exact is a query for name, names split by ">", each matching whole,
// ignoring case, or by its start where it ends in "*".
func Exact(name string) Query {
	q := Query{Nth: 1}
	for seg := range strings.SplitSeq(name, ">") {
		q.Path = append(q.Path, strings.ToLower(strings.TrimSpace(seg)))
	}
	return q
}

// Parse reads a target a person typed, as Exact does, but loose, and with
// "#n" after the last name for its nth match.
func Parse(target string) Query {
	nth := 1
	if i := strings.LastIndex(target, "#"); i > 0 {
		if n, err := strconv.Atoi(target[i+1:]); err == nil && n > 0 {
			target, nth = target[:i], n
		}
	}
	q := Exact(target)
	q.Nth, q.Loose = nth, true
	return q
}

// Find returns the query's match among trees, searched in order, and the
// index of the tree it is in, or nil and -1. A loose query counts names
// that match whole first, and names that hold its names only where none
// do.
func Find(trees []*access.Tree, q Query) (n *access.Node, tree int) {
	passes := []bool{true}
	if q.Loose {
		passes = append(passes, false)
	}
	for _, whole := range passes {
		k := 0
		for i, t := range trees {
			if t == nil {
				continue
			}
			for _, m := range Matches(t.Root, q.Path, whole) {
				k++
				if k == q.Nth {
					return m, i
				}
			}
		}
		if k > 0 {
			// Names match whole, too few of them: names that hold the
			// query's name don't make up the count.
			return nil, -1
		}
	}
	return nil, -1
}

// Matches returns the nodes under root, depth first, that show and whose
// names, with their ancestors', match path: its last name the node's,
// the names before it ancestors' in order. whole says the names match
// whole, not in part; case is ignored. A leaf shows where its middle
// lies inside every node around it, up to its window or popup: a row
// scrolled out of its list, or a header's button past a folded row's
// foot, is hidden. A node with children and no actions shows where any
// part of it does, as a list taller than its scroll; one with actions
// keeps the middle rule, so a click on it lands on it.
func Matches(root *access.Node, path []string, whole bool) []*access.Node {
	if len(path) == 0 {
		return nil
	}
	var out []*access.Node
	var above []string
	var walk func(n *access.Node, room geom.Rect)
	walk = func(n *access.Node, room geom.Rect) {
		if n.Role == access.RoleWindow {
			// A popup shows past its window's edges.
			room = n.Bounds
		}
		name := strings.ToLower(n.Name)
		// A window, named by its title, is where controls show, not one
		// to act on: a name that is also the title means the button.
		shows := n.Role != access.RoleWindow && !n.Bounds.Empty()
		if len(n.Children) > 0 && len(n.Actions) == 0 {
			shows = shows && !meet(room, n.Bounds).Empty()
		} else {
			shows = shows && room.Contains(n.Bounds.Center())
		}
		if shows && nameIs(name, path[len(path)-1], whole) && hasPath(above, path[:len(path)-1], whole) {
			out = append(out, n)
		}
		if !n.Bounds.Empty() {
			room = meet(room, n.Bounds)
		}
		above = append(above, name)
		for _, k := range n.Children {
			walk(k, room)
		}
		above = above[:len(above)-1]
	}
	walk(root, root.Bounds)
	return out
}

// meet is where a and b overlap, empty where they do not.
func meet(a, b geom.Rect) geom.Rect {
	r := geom.Rect{
		Min: geom.Pt(max(a.Min.X, b.Min.X), max(a.Min.Y, b.Min.Y)),
		Max: geom.Pt(min(a.Max.X, b.Max.X), min(a.Max.Y, b.Max.Y)),
	}
	if r.Empty() {
		return geom.Rect{}
	}
	return r
}

// hasPath reports whether names, from the root down, hold each of path
// in order.
func hasPath(names, path []string, whole bool) bool {
	for _, n := range names {
		if len(path) > 0 && nameIs(n, path[0], whole) {
			path = path[1:]
		}
	}
	return len(path) == 0
}

// nameIs reports whether name, in lower case, is want: whole, or by its
// start where want ends in "*", or, not whole, in part. An empty name, or
// an empty want, matches nothing.
func nameIs(name, want string, whole bool) bool {
	if name == "" || want == "" {
		return false
	}
	if start, ok := strings.CutSuffix(want, "*"); ok {
		return strings.HasPrefix(name, start)
	}
	if whole {
		return name == want
	}
	return strings.Contains(name, want)
}

// Text is the named nodes under n, one a line, indented by depth: their
// role, name, value and where they show.
func Text(n *access.Node) string {
	var b strings.Builder
	var walk func(n *access.Node, depth int)
	walk = func(n *access.Node, depth int) {
		if n.Name != "" || n.Value != "" {
			r := n.Bounds
			fmt.Fprintf(&b, "%s%s %q", strings.Repeat("  ", depth), n.Role, n.Name)
			if n.Value != "" {
				fmt.Fprintf(&b, " = %q", n.Value)
			}
			fmt.Fprintf(&b, " at %.0f,%.0f %.0fx%.0f\n", r.Min.X, r.Min.Y, r.Size().W, r.Size().H)
			depth++
		}
		for _, k := range n.Children {
			walk(k, depth)
		}
	}
	walk(n, 0)
	return b.String()
}
