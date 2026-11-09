package widget

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// FormGap is the room between a form's rows, and between a label and
// its field.
var FormGap = theme.Length("form.gap", 12)

// Form lines up labelled fields: each label beside its field, level
// with the field's first line, the labels in a column of their own as
// wide as the longest, the fields taking the rest. Put one in a [Dialog]'s Body, and Tab moves through
// its fields and the dialog's buttons in turn.
type Form struct {
	rows []formRow
}

type formRow struct {
	label *Label
	field gunim.Node
	// labelRight and fieldLeft are where the last layout put the
	// label's right edge and the field's left.
	labelRight, fieldLeft float32
}

// NewForm returns an empty form.
func NewForm() *Form { return &Form{} }

// Add adds a row: label beside field. Add rows before the form is
// mounted.
func (f *Form) Add(label string, field gunim.Node) *Form {
	f.rows = append(f.rows, formRow{label: NewLabel(label), field: field})
	return f
}

// Focusables returns the fields that take the keyboard, in order.
func (f *Form) Focusables() []gunim.Node {
	var out []gunim.Node
	for _, r := range f.rows {
		if fo, ok := r.field.(gunim.Focusable); ok && fo.Focusable() {
			out = append(out, r.field)
		}
	}
	return out
}

// Children implements [gunim.Composite]: each label, then its field.
func (f *Form) Children() []gunim.Node {
	out := make([]gunim.Node, 0, 2*len(f.rows))
	for _, r := range f.rows {
		out = append(out, r.label, r.field)
	}
	return out
}

// Layout implements [gunim.Node].
func (f *Form) Layout(c gunim.Constraints, fr gunim.Frame, kids gunim.Children) geom.Size {
	gap := FormGap.Get(fr.Theme)
	w := c.Max.W
	if w <= 0 {
		w = DialogWidth.Get(fr.Theme)
	}
	// The labels' column is as wide as the widest, and no more than
	// two fifths of the form.
	labelW := float32(0)
	for i := range f.rows {
		s := kids.At(2 * i).Layout(gunim.Constraints{Max: geom.Sz(w*0.4, 0)})
		labelW = max(labelW, s.W)
	}
	fieldW := max(0, w-labelW-gap)
	y := float32(0)
	for i := range f.rows {
		label, field := kids.At(2*i), kids.At(2*i+1)
		fs := field.Layout(gunim.Constraints{Min: geom.Sz(fieldW, 0), Max: geom.Sz(fieldW, 0)})
		ls := label.Layout(gunim.Constraints{Max: geom.Sz(labelW, 0)})
		h := max(fs.H, ls.H)
		field.Place(geom.Pt(labelW+gap, y+(h-fs.H)/2))
		// Labels line up at the right, against their fields: beside one control, at its middle, and beside a field
		// taller than any one control, as a column of them, level with its first line
		line := h
		if h > max(ControlHeight.Get(fr.Theme), FieldHeight.Get(fr.Theme), ls.H) {
			line = max(ControlHeight.Get(fr.Theme), ls.H)
		}
		label.Place(geom.Pt(labelW-ls.W, y+(line-ls.H)/2))
		f.rows[i].labelRight, f.rows[i].fieldLeft = labelW, labelW+gap
		y += h + gap
	}
	if len(f.rows) > 0 {
		y -= gap
	}
	return c.Constrain(geom.Sz(w, y))
}

// Paint implements [gunim.Node].
func (f *Form) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}
