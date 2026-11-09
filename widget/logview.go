package widget

import (
	"image/color"
	"strconv"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// LogView tokens.
var (
	// LogViewHeight is a log view's height, LogViewSize its words', and
	// LogViewLine a line's height as a share of LogViewSize.
	LogViewHeight = theme.Length("logview.height", 440)
	LogViewSize   = theme.Length("logview.size", 12.5)
	LogViewLine   = theme.Length("logview.line", 1.55)
	// LogViewPadding is the room round its lines, and LogViewLeadGap the
	// room after a line's lead.
	LogViewPadding = theme.Length("logview.padding", 12)
	LogViewLeadGap = theme.Length("logview.lead.gap", 12)
	// LogViewLead colours a line's lead, such as its time.
	LogViewLead = theme.Foreground("logview.lead", color.NRGBA{R: 0x8a, G: 0x93, B: 0xa6, A: 0xff})
)

// LogLine is one line of a [LogView]: a faint Lead, such as the time it
// was written, then its Text in Ink, the theme's [Ink] when unset.
type LogLine struct {
	Lead, Text string
	Ink        theme.Token[color.NRGBA]
}

// LogView shows lines as they are written, as a connection's log or a
// build's output, in the theme's mono face in a field's box, or in none
// when Plain, newest at the bottom.
//
// A line written while the newest is in view grows in at the bottom,
// and the view glides down to keep it there; scrolled up, the view
// stays on the lines it shows as more come. It scrolls as a [Scroll]
// does: the wheel, a finger and the keys glide it with a spring, and a
// thin bar shows while it moves. Only the lines in view are built, so a
// long log costs what a short one does.
//
// The app gives it the lines with SetLines from its view's update.
type LogView struct {
	// Plain leaves the field's box out, and the room inside it: the
	// lines are the whole of the view, for a log that is a page of its
	// own.
	Plain bool
	// OnSeen runs from the view's layout, with the window's UI, as the
	// newest line comes into view: the end SetLines was given, so an app
	// can count the lines not read yet. A non-nil result is sent to the
	// application as the view's intent.
	OnSeen func(end int64, u *gunim.UI) gunim.Intent

	list  *VirtualList
	lines map[Key]LogLine
	// end is the number past the newest line, and seen the end OnSeen
	// was last told; listAt is where the lines start.
	end, seen int64
	listAt    geom.Point
}

// NewLogView returns an empty log view.
func NewLogView() *LogView {
	v := &LogView{lines: map[Key]LogLine{}}
	v.list = NewVirtualList(func(k Key) gunim.Node { return &logRow{v: v, key: k} })
	v.list.StickToEnd = true
	v.list.Spacing = noSpacing
	return v
}

// noSpacing sets a log's lines against each other.
var noSpacing = theme.Length("logview.spacing", 0)

// End returns the number past the newest line, as SetLines was given.
func (v *LogView) End() int64 { return v.end }

// SetLines shows lines, oldest first, the newest numbered end-1, and
// sends no intent. The numbers say which lines are new: a line kept from
// before stays as it is, and a new one grows in. u may be nil.
func (v *LogView) SetLines(lines []LogLine, end int64, u *gunim.UI) {
	first := end - int64(len(lines))
	keys := make([]Key, len(lines))
	next := make(map[Key]LogLine, len(lines))
	for i, l := range lines {
		k := Key(strconv.FormatInt(first+int64(i), 10))
		keys[i], next[k] = k, l
	}
	v.lines, v.end = next, end
	v.list.SetKeys(keys, u)
}

// Children implements [gunim.Composite].
func (v *LogView) Children() []gunim.Node { return []gunim.Node{v.list} }

// Layout implements [gunim.Node]: the lines in a box LogViewHeight
// tall, LogViewPadding in from its edges.
func (v *LogView) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	th := f.Theme
	size := c.Constrain(geom.Sz(c.Max.W, LogViewHeight.Get(th)))
	pad := LogViewPadding.Get(th)
	if v.Plain {
		pad = 0
	}
	inner := geom.Sz(max(size.W-2*pad, 0), max(size.H-2*pad, 0))
	k := kids.At(0)
	k.Layout(gunim.Tight(inner))
	v.listAt = geom.Pt(pad, pad)
	k.Place(v.listAt)
	if v.OnSeen != nil && v.end > v.seen && v.list.AtEnd() {
		v.seen = v.end
		if in := v.OnSeen(v.end, f.UI()); in != nil {
			f.Send(v, in)
		}
	}
	return size
}

// Paint implements [gunim.Node]: a field's box, then the lines.
func (v *LogView) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	if v.Plain {
		kids.At(0).Paint(p)
		return
	}
	th := f.Theme
	r := geom.Rect{Max: box.Point()}
	radius := FieldRadius.Get(th)
	p.RRect(r, radius, paint.Solid(FieldFill.Get(th)))
	p.RRectStroke(r.Inset(geom.Uniform(0.5)), radius, paint.Fill{}, paint.Stroke{Width: 1, Color: FieldBorder.Get(th)})
	kids.At(0).Paint(p)
}

// Access implements [gunim.Accessible]: a list of lines.
func (v *LogView) Access() access.Info { return access.Info{Role: access.RoleList, Name: "Log"} }

// logRow is one line of a log view.
type logRow struct {
	v    *LogView
	key  Key
	lead shapedText
	text shapedText
}

// Layout implements [gunim.Node]: one line.
func (r *logRow) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	return c.Constrain(geom.Sz(c.Max.W, LogViewSize.Get(th)*LogViewLine.Get(th)))
}

// Paint implements [gunim.Node]: the lead faint, then the words in the
// line's colour, faded where they are cut.
func (r *logRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	l, ok := r.v.lines[r.key]
	if !ok {
		return
	}
	th := f.Theme
	face, size := faceIn(MonoFont, th), LogViewSize.Get(th)
	x := float32(0)
	if l.Lead != "" {
		run := r.lead.shape(face, l.Lead, size)
		run.Paint(p, geom.Pt(0, box.H/2-run.Height()/2), LogViewLead.Get(th))
		x = run.Advance + LogViewLeadGap.Get(th)
	}
	ink := Ink.Get(th)
	if l.Ink.Key() != "" {
		ink = l.Ink.Get(th)
	}
	run := r.text.shape(face, l.Text, size)
	run.PaintFaded(p, geom.Pt(x, box.H/2-run.Height()/2), ink, max(box.W-x, 0), TextFade.Get(th))
}

// Access implements [gunim.Accessible]: the line's words.
func (r *logRow) Access() access.Info {
	l := r.v.lines[r.key]
	name := l.Text
	if l.Lead != "" {
		name = l.Lead + " " + l.Text
	}
	return access.Info{Role: access.RoleLabel, Name: name}
}
