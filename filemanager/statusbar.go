package filemanager

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

func registerStatus(w *gunim.Window) {
	gunim.RegisterPatch(w, "browser", func(b *browser, s Status, u *gunim.UI) { b.status.set(s, u) })
}

// statusBar is the line along the bottom: how many items there are and
// how many are selected, the program's note, and the free space of the
// volume.
type statusBar struct {
	left, note, right *widget.Label
	// apart keeps the note off the free space, while there is a note.
	apart *widget.Spacer
	row   *widget.Flex
	views *viewBar
}

func newStatusBar() *statusBar {
	s := &statusBar{left: widget.NewLabel(""), note: widget.NewLabel(""), right: widget.NewLabel(""),
		apart: widget.NewSpacer(), views: newViewBar()}
	for _, l := range []*widget.Label{s.left, s.note, s.right} {
		l.Size, l.Color, l.MaxLines = SmallText, Faint, 1
	}
	s.row = widget.Row(s.left, s.note, s.apart, s.right, s.views).Grow(s.left, 1)
	s.row.Cross = widget.CrossCenter
	return s
}

func (s *statusBar) set(st Status, u *gunim.UI) {
	s.left.Text = st.Left
	s.note.Text = st.Note
	s.apart.Size.W = 0
	if st.Note != "" {
		s.apart.Size.W = 12
	}
	s.right.Text = st.Right
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (s *statusBar) Children() []gunim.Node { return []gunim.Node{s.row} }

// Layout implements [gunim.Node].
func (s *statusBar) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	kid.Layout(gunim.Tight(geom.Sz(c.Max.W-24, 26)))
	kid.Place(geom.Pt(12, 0))
	return geom.Sz(c.Max.W, 26)
}

// Paint implements [gunim.Node].
func (s *statusBar) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(SidebarFill.Get(f.Theme)))
	p.RRect(geom.Rc(0, 0, box.W, 1), 0, paint.Solid(widget.SplitLine.Get(f.Theme)))
	kids.At(0).Paint(p)
}
