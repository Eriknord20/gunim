package main

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/widget"
)

// transferRow is a file on its way up: its name, how it stands, and a bar for how much has gone. A failed one tries
// again when clicked, and its cross takes it away.
type transferRow struct {
	anim.Group
	t     Transfer
	hover *anim.Float
	// done carries the bar to how much has gone.
	done         *anim.Float
	name, status text.Run
	box          geom.Size
}

func newTransferRow(t Transfer) *transferRow {
	r := &transferRow{t: t, hover: anim.NewFloat(0), done: anim.NewFloat(0)}
	r.Add(r.hover, r.done)
	r.done.Jump(r.fraction())
	return r
}

func (r *transferRow) set(t Transfer, u *gunim.UI) {
	r.t = t
	r.done.Animate(r.fraction(), widget.Quick.Get(u.Theme()))
	u.Invalidate()
}

// fraction is how much of the file has gone, from 0 to 1.
func (r *transferRow) fraction() float32 {
	if r.t.State == TransferUploaded || r.t.Size == 0 {
		return map[bool]float32{false: 0, true: 1}[r.t.State == TransferUploaded]
	}
	return float32(r.t.Done) / float32(r.t.Size)
}

// transferText says how a transfer stands.
func transferText(t Transfer) string {
	switch t.State {
	case TransferEncrypting:
		return "Encrypting…"
	case TransferUploading:
		return sizeText(t.Done) + " of " + sizeText(t.Size)
	case TransferWaiting:
		return "Waiting for the connection"
	case TransferUploaded:
		return "Uploaded"
	case TransferFailed:
		if retryable(t) {
			return "Not uploaded: " + t.Reason + ". Click to try again."
		}
		return "Not uploaded: " + t.Reason + "."
	}
	return ""
}

// retryable reports whether a failed transfer can go again: one whose file was read, as it was sending.
func retryable(t Transfer) bool { return t.Size > 0 }

// Layout implements [gunim.Node].
func (r *transferRow) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	r.name = widget.Font.Get(th).Shape(r.t.Name, widget.TextSize.Get(th))
	r.box = geom.Sz(c.Max.W, 46)
	r.status = shapeFit(widget.Font.Get(th), transferText(r.t), SmallText.Get(th), r.box.W-42-48)
	return r.box
}

// cross is where the cross that takes a failed transfer away sits.
func (r *transferRow) cross() geom.Rect { return geom.Rc(r.box.W-36, r.box.H/2-12, 24, 24) }

// Paint implements [gunim.Node].
func (r *transferRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	if h := min(r.hover.Value(), 1); h > 0.01 {
		p.RRect(geom.Rect{Max: box.Point()}, 7, paint.Solid(fade(SidebarHot.Get(th), h)))
	}
	ic := fileIcon(FileEntry{Name: r.t.Name})
	widget.PaintIcon(p, th, ic, geom.Rc(12, box.H/2-9, 18, 18), Faint.Get(th))
	r.name.Paint(p, geom.Pt(42, 6), widget.Ink.Get(th))
	ink := Faint.Get(th)
	if r.t.State == TransferFailed {
		ink = ErrorInk.Get(th)
	}
	r.status.Paint(p, geom.Pt(42, 8+r.name.Height()), ink)
	switch r.t.State {
	case TransferUploading, TransferUploaded, TransferEncrypting:
		track := geom.Rc(42, box.H-4, box.W-42-12, 2)
		p.RRect(track, 1, paint.Solid(QuoteFill.Get(th)))
		w := track.Size().W * min(max(r.done.Value(), 0), 1)
		p.RRect(geom.Rc(track.Min.X, track.Min.Y, w, 2), 1, paint.Solid(widget.Accent.Get(th)))
	case TransferFailed:
		widget.PaintIcon(p, th, icon.X, r.cross().Inset(geom.Uniform(4)), Faint.Get(th))
	default:
		// A transfer still waiting draws no bar of its own.
	}
}

// Handle implements [gunim.Handler]: the row lights under the pointer; a click on a failed one tries again, and on
// its cross takes it away.
func (r *transferRow) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		if r.t.State == TransferFailed {
			r.hover.Animate(1, widget.Quick.Get(u.Theme()))
		}
	case input.PointerLeave:
		r.hover.Animate(0, widget.Settle.Get(u.Theme()))
	case input.PointerDown:
		if e.Button != input.ButtonPrimary || r.t.State != TransferFailed {
			return false
		}
		if r.cross().Contains(e.Pos) {
			u.Send(r, TransferDismissed{ID: r.t.ID})
		} else if retryable(r.t) {
			u.Send(r, TransferRetried{ID: r.t.ID})
		}
		return true
	}
	return false
}

// Cursor implements [gunim.CursorShaper].
func (r *transferRow) Cursor(geom.Point) input.Cursor {
	if r.t.State == TransferFailed {
		return input.CursorHand
	}
	return input.CursorArrow
}
