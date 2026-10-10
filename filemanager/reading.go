package filemanager

import (
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"

	fileview "github.com/marrasen/gunim/viewer"
)

// The text viewer: a file that is no picture shown in a card over the
// window, in its language's colours, Markdown rendered, or its bytes in
// hex. Space or Escape closes it, and so does a click beside the card.

func registerReading(w *gunim.Window) {
	gunim.RegisterView(w, "reading", func(Reading) *reading { return newReading() }, (*reading).set)
}

// readingMargin is the room round the card, and readingWidth its widest.
const (
	readingMargin = 40
	readingWidth  = 980
	readingHead   = 48
)

// reading shows a file over the window.
type reading struct {
	anim.Group
	state Reading
	in    *anim.Float
	name  *widget.Label
	info  *widget.Label
	shut  *widget.IconButton
	body  gunim.Node
	view  *fileview.View
	// card is where the card was laid out, and closing says it is on
	// its way out.
	card    geom.Rect
	closing bool
}

func newReading() *reading {
	r := &reading{in: anim.NewFloat(0)}
	r.Add(r.in)
	r.name = widget.NewLabel("")
	r.name.Face, r.name.MaxLines = widget.BoldFont, 1
	r.info = widget.NewLabel("")
	r.info.Size, r.info.Color, r.info.MaxLines = SmallText, Faint, 1
	r.shut = widget.NewIconButton(icon.X, "Close")
	return r
}

// set takes the viewer's state: the file once it has been read.
func (r *reading) set(s Reading, u *gunim.UI) {
	first := r.state.Seq == 0
	was := r.state
	r.state = s
	r.name.Text = s.Name
	r.info.Text = s.Shown
	if s.Size != "" {
		r.info.Text = s.Size + "  ·  " + s.Shown
	}
	if first {
		r.shut.OnClick = func(u *gunim.UI) gunim.Intent { r.close(u); return nil }
		u.Insert(r, r.name)
		u.Insert(r, r.info)
		u.Insert(r, r.shut)
		u.Focus(r)
	}
	if first || was.Loading != s.Loading || was.Seq != s.Seq || was.Err != s.Err {
		if r.body != nil {
			u.Remove(r.body)
		}
		r.view = nil
		switch {
		case s.Err != "":
			l := widget.NewLabel(s.Err)
			l.Color, l.Selectable = ErrorInk, true
			r.body = l
		case s.Loading:
			l := widget.NewLabel("Reading…")
			l.Color = Faint
			r.body = l
		default:
			seq := s.Seq
			r.view = fileview.New(s.Name, s.Data, fileview.Options{Cut: s.Cut, OnLink: func(url string) gunim.Intent {
				return ReadingLink{Seq: seq, URL: url}
			}})
			r.body = r.view
		}
		u.Insert(r, r.body)
		u.Focus(r)
	}
	u.Invalidate()
}

// close takes the viewer away, and tells the application.
func (r *reading) close(u *gunim.UI) {
	if r.closing {
		return
	}
	r.closing = true
	u.Remove(r)
	u.Send(r, ReadingClosed{Seq: r.state.Seq})
}

// Focusable implements [gunim.Focusable].
func (r *reading) Focusable() bool { return !r.closing }

// Transition implements [gunim.Transitioner].
func (r *reading) Transition(p gunim.Presence, f gunim.Frame) bool {
	switch p {
	case gunim.Entering:
		r.in.Animate(1, Page.Get(f.Theme))
	case gunim.Exiting:
		r.in.Animate(0, widget.Settle.Get(f.Theme))
	case gunim.Present:
	}
	return !r.in.Active()
}

// Handle implements [gunim.Handler]: Escape and Space close it, and so
// does a click beside the card; what the card holds takes the rest.
func (r *reading) Handle(e input.Event, u *gunim.UI) bool {
	if r.closing {
		return false
	}
	switch e := e.(type) {
	case input.KeyPress:
		if e.Key == input.KeyEscape || e.Key == input.KeySpace && e.Mods == 0 {
			r.close(u)
			return true
		}
		return false
	case input.PointerDown:
		if !r.card.Contains(e.Pos) {
			r.close(u)
		}
		return true
	case input.PointerMove, input.PointerUp, input.Scroll:
		// Nothing under the card hears the pointer.
		return true
	}
	return false
}

// Layout implements [gunim.Node]: the card in the middle of the window,
// its name and the close button along its top, and the file below.
func (r *reading) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	w := max(0, min(readingWidth, box.W-2*readingMargin))
	h := max(0, box.H-2*readingMargin)
	r.card = geom.Rc((box.W-w)/2, (box.H-h)/2, w, h)
	const pad = 16
	for k := range kids.All {
		switch k.Node() {
		case gunim.Node(r.shut):
			s := k.Layout(gunim.Constraints{Max: geom.Sz(40, 40)})
			k.Place(geom.Pt(r.card.Max.X-pad-s.W+6, r.card.Min.Y+(readingHead-s.H)/2))
		case gunim.Node(r.name):
			s := k.Layout(gunim.Constraints{Max: geom.Sz(max(0, w*0.5-pad), 0)})
			k.Place(geom.Pt(r.card.Min.X+pad, r.card.Min.Y+(readingHead-s.H)/2))
		case gunim.Node(r.info):
			k.Layout(gunim.Constraints{Max: geom.Sz(max(0, w*0.5-pad-48), 0)})
		default:
			k.Layout(gunim.Tight(geom.Sz(max(0, w-2*pad), max(0, h-readingHead-pad))))
			k.Place(geom.Pt(r.card.Min.X+pad, r.card.Min.Y+readingHead))
		}
	}
	// The path and size at the right of the name, before the button.
	for k := range kids.All {
		if k.Node() == gunim.Node(r.info) {
			s := k.Size()
			k.Place(geom.Pt(r.card.Max.X-pad-44-s.W, r.card.Min.Y+(readingHead-s.H)/2))
		}
	}
	return box
}

// Paint implements [gunim.Node]: the window darkened, and the card
// rising into it as it opens.
func (r *reading) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	in := min(max(r.in.Value(), 0), 1)
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(color.NRGBA{A: uint8(0x99 * in)}))
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: in})()
	defer p.Push(paint.Scale(0.96+0.04*in, r.card.Center()))()
	p.ShadowRRect(r.card, 12, paint.Solid(widget.DialogFill.Get(f.Theme)),
		paint.Shadow{Offset: geom.Pt(0, 8), Blur: 32, Color: widget.DialogShadow.Get(f.Theme)})
	for k := range kids.All {
		k.Paint(p)
	}
}
