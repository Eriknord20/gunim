package plugui

import (
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// ChipText is a chip's words, as the gain under its plugin's name:
// smaller and quieter than the name, so the name leads, and in figures
// of one width, so they hold still as they change.
type ChipText struct {
	Text string
}

// The chip's words' size, and how strong their ink is.
const (
	ChipTextSize = 11
	ChipTextInk  = 0.6
)

// NewChipText returns a chip's words, s.
func NewChipText(s string) *ChipText { return &ChipText{Text: s} }

// Layout implements [gunim.Node]: as wide as the words, cut short where
// there is less room.
func (t *ChipText) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	run := audioui.Shaped(t.Text, ChipTextSize, false, true)
	return c.Constrain(geom.Sz(run.Advance, run.Height()))
}

// Paint implements [gunim.Node].
func (t *ChipText) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	run := audioui.Shaped(t.Text, ChipTextSize, false, true)
	end := p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: 1, Clip: true})
	defer end()
	run.Paint(p, geom.Pt(0, (box.H-run.Height())/2), audioui.Faded(widget.Ink.Get(f.Theme), ChipTextInk))
}

// Access implements [gunim.Accessible]: the words, as a label.
func (t *ChipText) Access() access.Info { return access.Info{Role: access.RoleLabel, Name: t.Text} }

// ChipScale is how far a chip draws v, from -1 to 1, where full fills
// it: by its square root, so the small changes of mastering show
// plainly, and a large one still fits.
func ChipScale(v, full float64) float32 {
	t := math.Sqrt(min(math.Abs(v)/full, 1))
	if v < 0 {
		t = -t
	}
	return float32(t)
}
