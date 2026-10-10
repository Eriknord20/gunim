package themeedit

import (
	"fmt"
	"image/color"
	"math"
	"reflect"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// A control edits one value in a row.
type control interface {
	// node returns what the row shows at its right.
	node() gunim.Node
	// below returns what the row shows under its words and control,
	// such as a spring's sliders, or nil.
	below() gunim.Node
	// show shows v, telling no one.
	show(v any, u *gunim.UI)
	// stops returns the nodes Tab stops at in the control, in order.
	stops() []gunim.Node
}

// row is one token's row: its name, what it does, its control, and a
// button that resets it, which shows only while the value is changed.
type row struct {
	key     string
	ctl     control
	reset   *widget.IconButton
	line    *line
	changed bool
}

// newRow returns a row for the token info describes, as field sets it
// out. A dense row, as All values has, shows the token's key under its
// name in place of a sentence.
func (e *Editor) newRow(info theme.Info, f Field, dense bool) *row {
	r := &row{key: info.Key}
	label := f.Label
	if label == "" {
		label = Words(info.Key)
	}
	edit := func(v any, commit bool, src control, u *gunim.UI) { _ = e.edit(info.Key, v, commit, src, u) }
	play := func(u *gunim.UI) { e.Play(info.Key, u) }
	r.ctl = newControl(info, f, label, e.choices[info.Key], dense, edit, play)
	r.ctl.show(e.value(info.Key), nil)

	title := widget.NewLabel(label)
	detail := widget.NewLabel(f.Detail)
	detail.Color = widget.Placeholder
	if dense {
		detail.Text = info.Key
		detail.Face, detail.Size, detail.Selectable = widget.MonoFont, KeySize, true
	}
	r.reset = widget.NewIconButton(icon.RotateCcw, "Reset "+label+" to "+e.baseName())
	r.reset.IconSize = IconSize
	r.reset.OnClick = func(u *gunim.UI) gunim.Intent {
		e.Reset(info.Key, u)
		return nil
	}
	r.line = &line{title: title, detail: detail, control: r.ctl.node(), reset: r.reset, below: r.ctl.below()}
	r.mark(e.over.Has(info.Key), nil)
	return r
}

// mark shows whether the row's value is changed: a dot before its name
// and its reset button, or neither.
func (r *row) mark(on bool, u *gunim.UI) {
	r.changed = on
	r.line.changed = on
	r.reset.Disabled = !on
	u.Invalidate()
}

// stops returns the nodes Tab stops at in the row, in order.
func (r *row) stops() []gunim.Node {
	out := r.ctl.stops()
	if r.changed {
		out = append(out, r.reset)
	}
	return out
}

// line lays a row out as a settings page does: its name, with what it
// does under it in the faint ink, at the left, and its control at the
// right, centred on each other. Where the words would have less than
// [TextRoom], the control goes under them. The reset button keeps its
// room at the far right whether it shows or not, so nothing moves as a
// value changes, and a changed row has a dot just after its name.
type line struct {
	title, detail *widget.Label
	control       gunim.Node
	reset         *widget.IconButton
	// below, when set, goes under the rest across the whole row.
	below gunim.Node
	// changed shows the dot and the reset button.
	changed bool
	// titleAt is where the title's words were placed, for the dot.
	titleAt geom.Rect
}

// Children implements [gunim.Composite].
func (l *line) Children() []gunim.Node {
	kids := []gunim.Node{l.title, l.detail, l.control, l.reset}
	if l.below != nil {
		kids = append(kids, l.below)
	}
	return kids
}

// Layout implements [gunim.Node].
func (l *line) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	th := f.Theme
	w := c.Max.W
	gap := widget.Gap.Get(th)
	title, detail, ctl, reset := kids.At(0), kids.At(1), kids.At(2), kids.At(3)
	rs := reset.Layout(gunim.Constraints{})
	slot := rs.W + gap
	cs := ctl.Layout(gunim.Constraints{Max: geom.Sz(max(0, w-slot), 0)})
	// The title leaves room for the dot after its words.
	dot := DotGap.Get(th) + DotSize.Get(th)
	var titleW float32
	names := func(width float32) (float32, float32) {
		ts := title.Layout(gunim.Constraints{Max: geom.Sz(max(0, width-dot), 0)})
		titleW = ts.W
		h := ts.H
		if l.detail.Text != "" {
			ds := detail.Layout(gunim.Constraints{Max: geom.Sz(width, 0)})
			h += NamesGap.Get(th) + ds.H
		} else {
			detail.Layout(gunim.Tight(geom.Size{}))
		}
		return ts.H, h
	}
	var h float32
	if room := w - cs.W - ControlGap.Get(th) - slot; room >= TextRoom.Get(th) {
		th1, nh := names(room)
		h = max(nh, cs.H, rs.H)
		y := (h - nh) / 2
		title.Place(geom.Pt(0, y))
		detail.Place(geom.Pt(0, y+th1+NamesGap.Get(th)))
		l.titleAt = geom.Rc(0, y, titleW, th1)
		ctl.Place(geom.Pt(w-slot-cs.W, (h-cs.H)/2))
		reset.Place(geom.Pt(w-rs.W, (h-rs.H)/2))
	} else {
		// The words take the whole row but the reset button's room, and
		// the control goes under them, from the left.
		th1, nh := names(max(0, w-slot))
		title.Place(geom.Point{})
		detail.Place(geom.Pt(0, th1+NamesGap.Get(th)))
		l.titleAt = geom.Rc(0, 0, titleW, th1)
		reset.Place(geom.Pt(w-rs.W, (th1-rs.H)/2))
		top := nh + gap
		ctl.Place(geom.Pt(0, top))
		h = top + cs.H
	}
	if l.below != nil {
		b := kids.At(4)
		bs := b.Layout(gunim.Constraints{Min: geom.Sz(w, 0), Max: geom.Sz(w, 0)})
		b.Place(geom.Pt(0, h))
		h += bs.H
	}
	return c.Constrain(geom.Sz(w, h))
}

// Paint implements [gunim.Node].
func (l *line) Paint(p *paint.Painter, f gunim.Frame, _ geom.Size, kids gunim.Children) {
	th := f.Theme
	for i := range kids.Len() {
		if i == 3 && !l.changed {
			continue
		}
		kids.At(i).Paint(p)
	}
	if l.changed {
		d := DotSize.Get(th)
		x := l.titleAt.Max.X + DotGap.Get(th)
		y := l.titleAt.Min.Y + l.titleAt.Size().H/2 - d/2
		p.RRect(geom.Rc(x, y, d, d), d/2, paint.Solid(widget.Accent.Get(th)))
	}
}

// editFunc sets a row's value from the control src, which already
// shows it.
type editFunc func(v any, commit bool, src control, u *gunim.UI)

// newControl returns the control for a value of info's kind, or one of
// presets where there are presets. A dense row, as All values has,
// shows a length or a number with a range as a field, where a row of
// the Basics shows a slider.
func newControl(info theme.Info, f Field, label string, choices []Preset, dense bool, edit editFunc, play func(*gunim.UI)) control {
	switch info.Kind {
	case theme.KindSpring:
		presets, custom := springPresets, true
		if len(f.Presets) > 0 {
			presets, custom = f.Presets, false
		}
		return newSpringControl(label, presets, custom, edit, play)
	case theme.KindColor, theme.KindForeground:
		if len(f.Presets) == 0 {
			return newColorControl(label, edit)
		}
	case theme.KindLength, theme.KindNumber:
		if len(f.Presets) == 0 {
			return newNumberControl(info, f, label, dense, edit)
		}
	case theme.KindInsets:
		if len(f.Presets) == 0 {
			return newInsetsControl(label, dense, edit)
		}
	case theme.KindChoice, theme.KindOther:
	}
	if len(f.Presets) > 0 {
		return newPresets(f.Presets, label, false, edit)
	}
	if len(choices) > 0 {
		return newChoiceControl(choices, label, edit)
	}
	return newTextControl()
}

// colorControl is a swatch with the colour in hex beside it, which
// opens a colour picker.
type colorControl struct {
	button *widget.ColorButton
}

func newColorControl(label string, edit editFunc) *colorControl {
	c := &colorControl{button: widget.NewColorButton(color.NRGBA{})}
	c.button.Label, c.button.Hex = label, true
	c.button.OnChange = func(v color.NRGBA, u *gunim.UI) gunim.Intent {
		edit(v, false, c, u)
		return nil
	}
	c.button.OnCommit = func(v color.NRGBA, u *gunim.UI) gunim.Intent {
		edit(v, true, c, u)
		return nil
	}
	return c
}

func (c *colorControl) node() gunim.Node    { return c.button }
func (c *colorControl) below() gunim.Node   { return nil }
func (c *colorControl) stops() []gunim.Node { return []gunim.Node{c.button} }

func (c *colorControl) show(v any, u *gunim.UI) {
	if col, ok := v.(color.NRGBA); ok {
		c.button.SetValue(col, u)
	}
}

// numberControl is a field for a length or a number, or, for a field
// of the Basics with a range, a slider with a small field for the value
// beside it. Either field can be dragged up and down to change the
// value.
type numberControl struct {
	field  *widget.NumberField
	slider *widget.Slider
	// whole says the value is a whole number, and view is what the
	// row shows.
	whole bool
	view  gunim.Node
}

func newNumberControl(info theme.Info, f Field, label string, dense bool, edit editFunc) *numberControl {
	def, _ := info.Default.(float32)
	c := &numberControl{whole: def == float32(math.Round(float64(def)))}
	c.field = widget.NewNumberField(-1e6, 1e6)
	if f.Max > f.Min {
		c.field.Min, c.field.Max = float64(f.Min), float64(f.Max)
	}
	// A whole number steps by ones; any other by hundredths, and drags
	// a little quicker than a hundredth every few pixels, so a value
	// such as a text size can be dragged across in a short way.
	c.field.Decimals, c.field.Increment = 2, 0.01
	if c.whole {
		c.field.Decimals, c.field.Increment = 0, 1
	} else if f.Max <= f.Min {
		c.field.DragRate = fractionRate
	}
	c.field.Tooltip, c.field.Placeholder = label, label
	// While the field is dragged the edits are on the way, as a
	// slider's are, and letting go keeps the one it ends at. A value
	// typed is kept as it reads.
	c.field.OnChange = func(v float64, u *gunim.UI) gunim.Intent {
		if c.slider != nil {
			c.slider.SetValue(float32(v), u)
		}
		edit(float32(v), !c.field.Dragging(), c, u)
		return nil
	}
	c.field.OnCommit = func(v float64, u *gunim.UI) gunim.Intent {
		edit(float32(v), true, c, u)
		return nil
	}
	if f.Max > f.Min && !dense {
		c.slider = widget.NewSlider(f.Min, f.Max)
		c.slider.Label = label
		if c.whole {
			c.slider.Snap = 1
		}
		c.slider.OnChange = func(v float32, u *gunim.UI) gunim.Intent {
			c.field.SetValue(float64(v), u)
			edit(v, false, c, u)
			return nil
		}
		c.slider.OnCommit = func(v float32, u *gunim.UI) gunim.Intent {
			edit(v, true, c, u)
			return nil
		}
		c.field.Height = CompactHeight
		r := widget.Row(&sized{child: c.slider, width: SliderWidth}, &sized{child: c.field, width: ValueWidth})
		r.Cross, r.Gap = widget.CrossCenter, NamesGap
		c.view = r
		return c
	}
	c.view = &sized{child: c.field, width: NumberWidth}
	if dense {
		c.field.Height = CompactHeight
		c.view = &sized{child: c.field, width: CompactWidth}
	}
	return c
}

// fractionRate is how far a field for a number that is not whole, and
// has no range, moves for each pixel it is dragged.
const fractionRate = 0.02

func (c *numberControl) node() gunim.Node  { return c.view }
func (c *numberControl) below() gunim.Node { return nil }

func (c *numberControl) stops() []gunim.Node {
	if c.slider != nil {
		return []gunim.Node{c.slider, c.field}
	}
	return []gunim.Node{c.field}
}

func (c *numberControl) show(v any, u *gunim.UI) {
	f, ok := v.(float32)
	if !ok {
		return
	}
	if c.slider != nil {
		c.slider.SetValue(f, u)
	}
	c.field.SetValue(float64(f), u)
}

// insetsControl is four small fields, top, right, bottom and left, each
// with its letter before it.
type insetsControl struct {
	sides [4]*widget.NumberField
	row   *widget.Flex
}

func newInsetsControl(label string, dense bool, edit editFunc) *insetsControl {
	c := &insetsControl{}
	kids := make([]gunim.Node, 0, 8)
	for i, name := range []string{"Top", "Right", "Bottom", "Left"} {
		n := widget.NewNumberField(-1e4, 1e4)
		n.Decimals = 0
		n.Tooltip = label + ": " + name
		if dense {
			n.Height = CompactHeight
		}
		c.sides[i] = n
		n.OnChange = func(_ float64, u *gunim.UI) gunim.Intent {
			edit(c.value(), !n.Dragging(), c, u)
			return nil
		}
		n.OnCommit = func(_ float64, u *gunim.UI) gunim.Intent {
			edit(c.value(), true, c, u)
			return nil
		}
		letter := widget.NewLabel(name[:1])
		letter.Color, letter.Size = widget.Placeholder, KeySize
		pair := widget.Row(letter, &sized{child: n, width: SideWidth})
		pair.Cross, pair.Gap = widget.CrossCenter, NamesGap
		kids = append(kids, pair)
	}
	c.row = widget.Row(kids...)
	c.row.Cross = widget.CrossCenter
	return c
}

// value returns the insets the fields hold.
func (c *insetsControl) value() geom.Insets {
	f := func(i int) float32 { return float32(c.sides[i].Value()) }
	return geom.Insets{Top: f(0), Right: f(1), Bottom: f(2), Left: f(3)}
}

func (c *insetsControl) node() gunim.Node  { return c.row }
func (c *insetsControl) below() gunim.Node { return nil }

func (c *insetsControl) stops() []gunim.Node {
	return []gunim.Node{c.sides[0], c.sides[1], c.sides[2], c.sides[3]}
}

func (c *insetsControl) show(v any, u *gunim.UI) {
	in, ok := v.(geom.Insets)
	if !ok {
		return
	}
	// A side that is not whole shows its tenths.
	for i, s := range []float32{in.Top, in.Right, in.Bottom, in.Left} {
		c.sides[i].Decimals = 0
		if s != float32(math.Round(float64(s))) {
			c.sides[i].Decimals = 1
		}
		c.sides[i].SetValue(float64(s), u)
	}
}

// presetControl picks one of a field's presets, or shows Custom for a
// value none of them has.
type presetControl struct {
	presets []Preset
	seg     *widget.Segmented
	// always offers Custom whatever the value, as a spring's presets
	// do, so the user can reach its sliders; custom says Custom is
	// chosen.
	always, custom bool
	// onCustom runs as the user chooses Custom.
	onCustom func(u *gunim.UI)
}

func newPresets(presets []Preset, label string, always bool, edit editFunc) *presetControl {
	c := &presetControl{presets: presets, seg: widget.NewSegmented(), always: always}
	c.seg.Tooltip, c.seg.Fit = label, true
	c.seg.OnChange = func(i int, u *gunim.UI) gunim.Intent {
		if i < len(c.presets) {
			c.setCustom(false)
			edit(c.presets[i].Value, true, c, u)
			return nil
		}
		c.setCustom(true)
		if c.onCustom != nil {
			c.onCustom(u)
		}
		return nil
	}
	c.setCustom(false)
	return c
}

// setCustom chooses Custom, or not, and offers it after the presets
// where it is chosen or always offered.
func (c *presetControl) setCustom(on bool) {
	c.custom = on
	items := make([]string, 0, len(c.presets)+1)
	for _, p := range c.presets {
		items = append(items, p.Label)
	}
	if on || c.always {
		items = append(items, "Custom")
	}
	c.seg.Items = items
}

func (c *presetControl) node() gunim.Node    { return c.seg }
func (c *presetControl) below() gunim.Node   { return nil }
func (c *presetControl) stops() []gunim.Node { return []gunim.Node{c.seg} }

// show shows v: its preset, or Custom for a value none has. Custom
// chosen by the user stays chosen while the value is one of its own.
func (c *presetControl) show(v any, u *gunim.UI) {
	i := presetOf(c.presets, v)
	if c.custom && c.always {
		i = -1
	}
	c.setCustom(i < 0)
	if i < 0 {
		i = len(c.presets)
	}
	c.seg.SetSelected(i, u)
}

// presetOf returns the preset of value v, or -1.
func presetOf(presets []Preset, v any) int {
	for i, p := range presets {
		if same(p.Value, v) {
			return i
		}
	}
	return -1
}

// choiceControl picks one of the values a program offers for a token,
// such as a font.
type choiceControl struct {
	choices []Preset
	drop    *widget.Dropdown
}

func newChoiceControl(choices []Preset, label string, edit editFunc) *choiceControl {
	items := make([]string, 0, len(choices))
	for _, p := range choices {
		items = append(items, p.Label)
	}
	c := &choiceControl{choices: choices, drop: widget.NewDropdown(widget.Labels(items...))}
	c.drop.Label = label
	c.drop.OnChange = func(i int, u *gunim.UI) gunim.Intent {
		edit(c.choices[i].Value, true, c, u)
		return nil
	}
	return c
}

func (c *choiceControl) node() gunim.Node    { return c.drop }
func (c *choiceControl) below() gunim.Node   { return nil }
func (c *choiceControl) stops() []gunim.Node { return []gunim.Node{c.drop} }

func (c *choiceControl) show(v any, u *gunim.UI) { c.drop.SetSelected(presetOf(c.choices, v), u) }

// textControl shows a value the editor cannot change, as text.
type textControl struct{ label *widget.Label }

func newTextControl() *textControl {
	l := widget.NewLabel("")
	l.Color, l.MaxLines, l.NoWrap = widget.Placeholder, 1, true
	return &textControl{label: l}
}

func (c *textControl) node() gunim.Node    { return c.label }
func (c *textControl) below() gunim.Node   { return nil }
func (c *textControl) stops() []gunim.Node { return nil }

func (c *textControl) show(v any, u *gunim.UI) {
	c.label.Text = describe(v)
	u.Invalidate()
}

// describe writes a value as text: its own words, or its type.
func describe(v any) string {
	switch v := v.(type) {
	case fmt.Stringer:
		return v.String()
	case *text.Face:
		return "A font"
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return "None"
	}
	switch k := rv.Kind(); k {
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return fmt.Sprint(v)
	default:
		return "A " + rv.Type().String()
	}
}

// sized lays its child out at a width from a token.
type sized struct {
	child gunim.Node
	width theme.Token[float32]
}

// Children implements [gunim.Composite].
func (s *sized) Children() []gunim.Node { return []gunim.Node{s.child} }

// Layout implements [gunim.Node].
func (s *sized) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	w := s.width.Get(f.Theme)
	if c.Max.W > 0 {
		w = min(w, c.Max.W)
	}
	cs := gunim.Constraints{Min: geom.Sz(w, c.Min.H), Max: geom.Sz(w, c.Max.H)}
	size := kids.At(0).Layout(cs)
	kids.At(0).Place(geom.Point{})
	return c.Constrain(geom.Sz(w, size.H))
}

// Paint implements [gunim.Node].
func (s *sized) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}
