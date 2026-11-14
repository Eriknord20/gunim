package widget

import (
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// Themed gives its child a theme of its own: a sidebar, a callout, a
// dialog that stands apart. The tokens its theme sets hold inside it;
// the rest come from the theme around it and move with it.
type Themed struct {
	kids []gunim.Node
	live *theme.Live
}

// NewThemed returns child wearing th.
func NewThemed(child gunim.Node, th theme.Theme) *Themed {
	return &Themed{kids: []gunim.Node{child}, live: theme.NewLive(th)}
}

// Use switches the subtree to th, animating every value it changes,
// including tokens th stops or starts setting. Call it from a view's
// update function.
func (t *Themed) Use(th theme.Theme) { t.live.Use(th) }

// ThemeScope implements [gunim.ThemeScope].
func (t *Themed) ThemeScope() *theme.Live { return t.live }

// Children implements [gunim.Composite].
func (t *Themed) Children() []gunim.Node { return t.kids }

// Step implements [gunim.Animator].
func (t *Themed) Step(dt time.Duration) bool { return t.live.Step(dt) }

// Layout implements [gunim.Node].
func (t *Themed) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	s := kid.Layout(c)
	kid.Place(geom.Point{})
	return s
}

// Paint implements [gunim.Node].
func (t *Themed) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}
