package widget

import (
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// Icon tokens.
var (
	// IconSize is the width and height of an icon.
	IconSize = theme.Length("icon.size", 16)
	// IconStroke is the width of an icon's strokes on its 24-unit grid.
	IconStroke = theme.Length("icon.stroke", 2)
	// IconGap is the space between an icon and the text beside it.
	IconGap = theme.Length("icon.gap", 6)
	// IconPadding is the space around the icon of a button that shows an icon alone at a size of its own.
	IconPadding = theme.Length("icon.padding", 3)
)

// spinTime is how long a spinning icon takes to turn once.
const spinTime = time.Second

// Icon shows a vector icon from package icon, tinted by a colour token. It can draw itself on, stroke by stroke,
// and spin, as a loader does.
type Icon struct {
	anim.Group
	// Icon is the icon shown.
	Icon *icon.Icon
	// Size and Color default to the theme's [IconSize] and [Ink].
	Size  theme.Token[float32]
	Color theme.Token[color.NRGBA]
	// Name says what the icon means, for a screen reader. An icon without one is decoration.
	Name string
	// Spin turns the icon about its centre, once a second.
	Spin bool

	drawn *anim.Float
	since time.Time
	// painted says the icon was painted since the last step; see Step.
	painted bool
}

// NewIcon returns a node showing ic, which a screen reader calls name.
func NewIcon(ic *icon.Icon, name string) *Icon {
	i := &Icon{Icon: ic, Name: name, Size: IconSize, Color: Ink, drawn: anim.NewFloat(1)}
	i.Add(i.drawn)
	return i
}

// DrawOn draws the icon on from nothing, its strokes in order, over d.
func (i *Icon) DrawOn(d time.Duration) {
	i.drawn.Jump(0)
	i.drawn.Animate(1, anim.Tween{Duration: d, Ease: anim.EaseInOut})
}

// Step implements [gunim.Animator]. A spinning icon keeps drawing while it is painted: one on a page out of sight
// lets the window rest, and turns on where it would be once it is painted again. The engine's step of no time,
// after a frame is painted, leaves the mark for the next frame's.
func (i *Icon) Step(dt time.Duration) bool {
	spin := i.Spin && i.painted
	if dt > 0 {
		i.painted = false
	}
	return i.Group.Step(dt) || spin
}

// size is the icon's size in th.
func (i *Icon) size(th *theme.Live) float32 {
	if i.Size.Key() != "" {
		return i.Size.Get(th)
	}
	return IconSize.Get(th)
}

// Layout implements [gunim.Node].
func (i *Icon) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	s := i.size(f.Theme)
	return c.Constrain(geom.Sz(s, s))
}

// Paint implements [gunim.Node]: the icon is centred in its box.
func (i *Icon) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	i.painted = true
	s := i.size(f.Theme)
	r := geom.Rc((box.W-s)/2, (box.H-s)/2, s, s)
	if i.Spin {
		if i.since.IsZero() {
			i.since = f.Now
		}
		turn := float32(f.Now.Sub(i.since)%spinTime) / float32(spinTime)
		defer p.Push(paint.Rotate(2*math.Pi*turn, r.Center()))()
	} else {
		i.since = time.Time{}
	}
	c := Ink.Get(f.Theme)
	if i.Color.Key() != "" {
		c = i.Color.Get(f.Theme)
	}
	drawIcon(p, f.Theme, i.Icon, r, c, i.drawn.Value())
}

// Access implements [gunim.Accessible]. An icon without a name says nothing of its own.
func (i *Icon) Access() access.Info {
	if i.Name == "" {
		return access.Info{Role: access.RoleGroup}
	}
	return access.Info{Role: access.RoleImage, Name: i.Name}
}

// drawIcon draws ic into r, tinted c, with the theme's stroke width, drawn on as far as progress.
func drawIcon(p *paint.Painter, th *theme.Live, ic *icon.Icon, r geom.Rect, c color.NRGBA, progress float32) {
	if ic == nil || progress <= 0 {
		return
	}
	p.Mask(icon.Stroke{Icon: ic, Width: IconStroke.Get(th), Progress: min(progress, 1)}, r, c)
}

// paintSmallIcon draws ic into r, tinted c, with strokes as thick on screen as at [IconSize], for a mark smaller
// than an icon.
func paintSmallIcon(p *paint.Painter, th *theme.Live, ic *icon.Icon, r geom.Rect, c color.NRGBA) {
	w := r.Size().W
	if ic == nil || w <= 0 {
		return
	}
	p.Mask(icon.Stroke{Icon: ic, Width: IconStroke.Get(th) * IconSize.Get(th) / w, Progress: 1}, r, c)
}

// PaintIcon draws ic in r, in c, as widgets draw their icons, for a node of an application's own.
func PaintIcon(p *paint.Painter, th *theme.Live, ic *icon.Icon, r geom.Rect, c color.NRGBA) {
	drawIcon(p, th, ic, r, c, 1)
}
