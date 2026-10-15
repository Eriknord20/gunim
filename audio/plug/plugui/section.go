package plugui

import (
	"strings"

	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// Section is a part of a plugin's controls, on a card of glass: its
// name, after a power lamp where it can be switched off; its knobs,
// greyed while it is off; and after them any other controls it has, as
// a selector or a button.
type Section struct {
	Name   string
	On     uint32
	Switch *Power
	Knobs  []*Knob
	extras []extra
	size   geom.Size
}

// extra is a control of a section's other than its knobs, and how wide
// it stands.
type extra struct {
	n gunim.Node
	w float32
}

// The sections' look: the room round and between their controls, the
// height of the name over them, and the room between sections.
const (
	SectionPad  = 8
	SectionHead = 26
	SectionGap  = 10
	SectionH    = SectionHead + KnobH + SectionPad
)

// NewSection returns the section name of d's parameters ids, as knobs
// working them through h; on, where not 0, is the parameter its switch
// works.
func NewSection(h plug.Host, d *plug.Descriptor, name string, on uint32, ids ...uint32) *Section {
	s := &Section{Name: name, On: on}
	for _, id := range ids {
		p, _ := d.Param(id)
		s.Knobs = append(s.Knobs, NewKnob(h, p))
	}
	if on != 0 {
		s.Switch = NewPower(strings.ToLower(name))
		s.Switch.SetChecked(h.Param(on) > 0.5, nil)
		s.Switch.OnChange = func(v bool, u *gunim.UI) gunim.Intent {
			h.Begin(on, u)
			h.Set(on, float64(onOff(v)), u)
			h.End(on, u)
			s.dim(v)
			return nil
		}
		s.dim(h.Param(on) > 0.5)
	}
	return s
}

// Add puts n after the section's knobs, w wide.
func (s *Section) Add(n gunim.Node, w float32) *Section {
	s.extras = append(s.extras, extra{n: n, w: w})
	return s
}

// dim greys the knobs while the section is off.
func (s *Section) dim(on bool) {
	for _, k := range s.Knobs {
		k.Disabled = !on
	}
}

// Width is how wide the section is: its controls', or its name's,
// after the power lamp, where that is wider.
func (s *Section) Width() float32 {
	w := float32(len(s.Knobs))*KnobW + 2*SectionPad
	for _, e := range s.extras {
		w += e.w + SectionPad
	}
	return max(w, s.nameX()+audioui.Shaped(s.Name, sectionText, true, false).Advance+SectionPad)
}

// sectionText is how big the section's name is.
const sectionText = 10

// nameX is where the section's name starts: after its power lamp,
// where it has one.
func (s *Section) nameX() float32 {
	if s.Switch != nil {
		return SectionPad + PowerSize
	}
	return SectionPad
}

// Follow takes a parameter set elsewhere, where it is the section's.
func (s *Section) Follow(id uint32, v float64, u *gunim.UI) {
	if id == s.On && s.Switch != nil {
		s.Switch.SetChecked(v > 0.5, u)
		s.dim(v > 0.5)
		u.Invalidate()
	}
	for _, k := range s.Knobs {
		if k.Param().ID == id {
			k.Follow(v, u)
		}
	}
}

// Children implements [gunim.Composite].
func (s *Section) Children() []gunim.Node {
	kids := make([]gunim.Node, 0, len(s.Knobs)+len(s.extras)+1)
	for _, k := range s.Knobs {
		kids = append(kids, k)
	}
	for _, e := range s.extras {
		kids = append(kids, e.n)
	}
	if s.Switch != nil {
		kids = append(kids, s.Switch)
	}
	return kids
}

// Layout implements [gunim.Node]: the knobs in a row under the name,
// the other controls after them, each in the middle of the knobs'
// height, and the switch at the top right.
func (s *Section) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	s.size = c.Constrain(geom.Sz(s.Width(), SectionH))
	x := float32(SectionPad)
	for i := range s.Knobs {
		kids.At(i).Layout(gunim.Tight(geom.Sz(KnobW, KnobH)))
		kids.At(i).Place(geom.Pt(x, SectionHead))
		x += KnobW
	}
	for i, e := range s.extras {
		x += SectionPad
		k := kids.At(len(s.Knobs) + i)
		sz := k.Layout(gunim.Constraints{Min: geom.Sz(e.w, 0), Max: geom.Sz(e.w, KnobH)})
		k.Place(geom.Pt(x, SectionHead+(KnobH-sz.H)/2))
		x += e.w
	}
	if s.Switch != nil {
		k := kids.At(kids.Len() - 1)
		k.Layout(gunim.Tight(geom.Sz(PowerSize, PowerSize)))
		k.Place(geom.Pt(SectionPad-3, (SectionHead-PowerSize)/2+2))
	}
	return s.size
}

// Paint implements [gunim.Node].
func (s *Section) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	ink := widget.Ink.Get(th)
	PaintGlass(p, th, geom.Rect{Max: box.Point()}, widget.CardRadius.Get(th))
	name := audioui.Shaped(s.Name, sectionText, true, false)
	name.Paint(p, geom.Pt(s.nameX(), (SectionHead-name.Height())/2+2), audioui.Faded(ink, 0.55))
	for k := range kids.All {
		k.Paint(p)
	}
}

// PlaceSections places sections along the foot of a view size big, in
// from its edges by inset: in rows as many as fit, each row centred,
// reaching as near the edges as half the inset. It tells place where
// each goes, and returns where the first row's top is.
func PlaceSections(size geom.Size, inset float32, sections []*Section, place func(i int, r geom.Rect)) float32 {
	widths := make([]float32, len(sections))
	for i, s := range sections {
		widths[i] = s.Width()
	}
	rows := balancedRows(widths, size.W-inset)
	top := size.H - inset - (float32(len(rows))*(SectionH+SectionGap) - SectionGap)
	y := top
	for _, r := range rows {
		x := (size.W - rowWidth(widths[r[0]:r[1]])) / 2
		for i := r[0]; i < r[1]; i++ {
			place(i, geom.Rc(x, y, widths[i], SectionH))
			x += widths[i] + SectionGap
		}
		y += SectionH + SectionGap
	}
	return top
}

// rowWidth is how wide sections of widths stand in a row.
func rowWidth(widths []float32) float32 {
	w := float32(0)
	for i, v := range widths {
		if i > 0 {
			w += SectionGap
		}
		w += v
	}
	return w
}

// balancedRows splits sections of widths, in order, into as few rows as
// fit in room, each from and to an index, and of those, the rows whose
// widest is least: as even as they go, so no section stands alone under
// a full row where it need not.
func balancedRows(widths []float32, room float32) [][2]int {
	n := len(widths)
	if n == 0 {
		return [][2]int{{0, 0}}
	}
	// How many rows: as many as filling each in turn takes.
	count, w := 1, float32(0)
	for i, v := range widths {
		if i > 0 && w+SectionGap+v > room {
			count, w = count+1, v
			continue
		}
		if i > 0 {
			w += SectionGap
		}
		w += v
	}
	// best[k][i] is the least widest row splitting the first i sections
	// into k rows, and cut[k][i] where its last row starts.
	inf := float32(math.Inf(1))
	best := make([][]float32, count+1)
	cut := make([][]int, count+1)
	for k := range best {
		best[k], cut[k] = make([]float32, n+1), make([]int, n+1)
		for i := range best[k] {
			best[k][i] = inf
		}
	}
	best[0][0] = 0
	for k := 1; k <= count; k++ {
		for i := 1; i <= n; i++ {
			for j := k - 1; j < i; j++ {
				if v := max(best[k-1][j], rowWidth(widths[j:i])); v < best[k][i] {
					best[k][i], cut[k][i] = v, j
				}
			}
		}
	}
	rows := make([][2]int, count)
	for k, i := count, n; k > 0; k-- {
		rows[k-1] = [2]int{cut[k][i], i}
		i = cut[k][i]
	}
	return rows
}
