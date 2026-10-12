package markdown

import (
	"strings"

	"github.com/marrasen/gunim/geom"
)

// Group lets a selection run across several views, such as the messages of a chat. Each view joins with its Group
// and Key fields. A drag that starts in one view selects on into the others it reaches, and Ctrl+C copies all of
// it, the views between that are not built too.
type Group struct {
	// Keys returns the keys from one view to another, both included, in order. It decides which views a
	// selection covers, including views that are not built.
	Keys func(from, to string) []string
	// Text returns the text, without its Markdown, of the view key when it is not built.
	Text func(key string) string

	views map[string]*View
	// frame is the latest frame a view of the group was drawn in.
	frame uint64
	// anchor is where the selection started and caret where it has got to; on says there is one.
	anchor, caret point
	on            bool
}

// point is a place in a group's text: a view and a rune in it.
type point struct {
	key string
	at  int
}

// NewGroup returns a group whose views come in the order keys gives, and whose views that are not built read as
// text gives.
func NewGroup(keys func(from, to string) []string, text func(key string) string) *Group {
	return &Group{Keys: keys, Text: text, views: map[string]*View{}}
}

// drawn records that v was drawn in frame at origin, in the window's space, and forgets views not drawn lately.
func (g *Group) drawn(v *View, frame uint64, origin geom.Point) {
	v.origin, v.drawnIn = origin, frame
	g.views[v.Key] = v
	if frame > g.frame {
		g.frame = frame
		for k, o := range g.views {
			if o.drawnIn+1 < frame {
				delete(g.views, k)
			}
		}
	}
}

// ends returns the selection's ends, the first first.
func (g *Group) ends() (first, last point) {
	if g.anchor.key == g.caret.key {
		if g.anchor.at <= g.caret.at {
			return g.anchor, g.caret
		}
		return g.caret, g.anchor
	}
	keys := g.Keys(g.anchor.key, g.caret.key)
	if len(keys) > 0 && keys[0] == g.anchor.key {
		return g.anchor, g.caret
	}
	return g.caret, g.anchor
}

// covered returns the keys the selection covers, in order.
func (g *Group) covered() []string {
	first, last := g.ends()
	if first.key == last.key {
		return []string{first.key}
	}
	keys := g.Keys(first.key, last.key)
	if len(keys) == 0 || keys[0] != first.key {
		return []string{first.key, last.key}
	}
	return keys
}

// selection returns the part of v's text the selection covers.
func (g *Group) selection(v *View) (start, end int) {
	if !g.on {
		return 0, 0
	}
	n := len(v.plain)
	first, last := g.ends()
	switch {
	case v.Key == first.key && first.key == last.key:
		return min(first.at, n), min(last.at, n)
	case v.Key == first.key:
		return min(first.at, n), n
	case v.Key == last.key:
		return 0, min(last.at, n)
	}
	for _, k := range g.covered() {
		if k == v.Key {
			return 0, n
		}
	}
	return 0, 0
}

// text returns the selected text, a line break between two views.
func (g *Group) text() string {
	if !g.on {
		return ""
	}
	var parts []string
	for _, k := range g.covered() {
		if v, ok := g.views[k]; ok {
			if s, e := g.selection(v); s < e {
				parts = append(parts, string(v.plain[s:e]))
			}
			continue
		}
		if g.Text != nil {
			parts = append(parts, g.Text(k))
		}
	}
	return strings.Join(parts, "\n")
}

// at returns the place in the group's views under p, in the window's space: in the view drawn there, or at the
// end of the nearest one above it, or the start of the first when there is none above.
func (g *Group) at(p geom.Point) (point, bool) {
	var above, below *View
	for _, v := range g.views {
		if v.drawnIn != g.frame {
			continue
		}
		top, bottom := v.origin.Y, v.origin.Y+v.size.H
		switch {
		case p.Y >= top && p.Y < bottom:
			return point{v.Key, v.index(p.Sub(v.origin))}, true
		case bottom <= p.Y && (above == nil || v.origin.Y > above.origin.Y):
			above = v
		case top > p.Y && (below == nil || v.origin.Y < below.origin.Y):
			below = v
		}
	}
	switch {
	case above != nil:
		return point{above.Key, len(above.plain)}, true
	case below != nil:
		return point{below.Key, 0}, true
	}
	return point{}, false
}
