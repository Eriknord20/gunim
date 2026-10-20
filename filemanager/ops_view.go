package filemanager

import (
	"fmt"
	"image/color"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

func registerOps(w *gunim.Window) {
	gunim.RegisterPatch(w, "browser", func(b *browser, s Ops, u *gunim.UI) { b.ops.set(s, u) })
	gunim.RegisterPatch(w, "browser", func(b *browser, t OpTick, u *gunim.UI) { b.ops.tick(t, u) })
	gunim.RegisterPatch(w, "browser", func(b *browser, n Notice, u *gunim.UI) {
		t := widget.Toast{Title: n.Title, Body: n.Body, Kind: toastKinds[n.Kind]}
		if n.Undo != 0 {
			t.Action = "Undo"
			t.OnClick = widget.Sends(UndoOp{ID: n.Undo})
		}
		t.Key, t.Check = n.Key, n.Check
		answer := func(i int) func(bool, *gunim.UI) gunim.Intent {
			return func(on bool, _ *gunim.UI) gunim.Intent { return NoticeAnswered{Key: n.Key, Button: i, Checked: on} }
		}
		for i, label := range n.Buttons {
			t.Buttons = append(t.Buttons, widget.ToastButton{Label: label, OnClick: answer(i)})
		}
		if len(n.Buttons) > 0 {
			t.OnDismiss = answer(-1)
		}
		b.toasts.Show(t, u)
	})
	gunim.RegisterPatch(w, "browser", func(b *browser, n NoticeGone, u *gunim.UI) { b.toasts.Close(n.Key, u) })
}

// toastKinds are the toasts' kinds, by a notice's kind.
var toastKinds = map[string]widget.ToastKind{
	"success": widget.ToastSuccess,
	"warning": widget.ToastWarning,
	"info":    widget.ToastInfo,
}

// The look of the cards that show the operations running.
var (
	// OpCardWidth is a card's width, and OpCardGap the room between cards.
	OpCardWidth = theme.Length("files.op.width", 360)
	OpCardGap   = theme.Length("files.op.gap", 10)
	// OpGraphHeight is the height of a card's speed graph, and OpBarHeight
	// that of the bar an operation without one shows.
	OpGraphHeight = theme.Length("files.op.graph", 40)
	OpBarHeight   = theme.Length("files.op.bar", 6)
	// OpSpeedSize is the size of the speed, the big words on a card.
	OpSpeedSize = theme.Length("files.op.speed", 22)
	// OpLeftSize is the size of the time left beside it.
	OpLeftSize = theme.Length("files.op.left", 16)
)

// opsKeep is how many cards of running operations stay open at once: an
// operation starting while as many are open folds the oldest, and the
// cards of operations that ended fold too.
const opsKeep = 2

// opsStack is the cards of the operations running, stacked over the
// bottom right corner, the newest on top. A card slides in when its
// operation starts and out when it ends, and the cards under it close up.
type opsStack struct {
	// cards is the cards of the operations running, the oldest first;
	// those leaving are in the tree only.
	cards []*opCard
	// height is the stack's height at its last layout.
	height float32
}

func newOpsStack() *opsStack { return &opsStack{} }

func (s *opsStack) card(id int) *opCard {
	for _, c := range s.cards {
		if c.id == id {
			return c
		}
	}
	return nil
}

func (s *opsStack) set(ops Ops, u *gunim.UI) {
	live := map[int]bool{}
	for _, v := range ops.Ops {
		live[v.ID] = true
		if c := s.card(v.ID); c != nil {
			c.set(v, u)
			continue
		}
		open := 0
		for _, c := range slices.Backward(s.cards) {
			if c.folded.Target() == 1 {
				continue
			}
			if c.badge.cheering {
				c.setFold(true, u)
				continue
			}
			open++
			if open >= opsKeep {
				c.setFold(true, u)
			}
		}
		c := newOpCard(v)
		s.cards = append(s.cards, c)
		u.Insert(s, c)
		if v.Ended {
			c.cheer(u)
		}
	}
	s.cards = slices.DeleteFunc(s.cards, func(c *opCard) bool {
		if live[c.id] {
			return false
		}
		u.Remove(c)
		return true
	})
	u.Invalidate()
}

func (s *opsStack) tick(t OpTick, u *gunim.UI) {
	if c := s.card(t.ID); c != nil {
		c.progress(t.OpProgress, u)
	}
}

// speed adds a sample to an operation's graph.
func (s *opsStack) speed(v OpSpeed, u *gunim.UI) {
	if c := s.card(v.ID); c != nil {
		c.graph.add(v.Done, v.Rate, u)
		u.Invalidate()
	}
}

// done shows an operation ended: one that went well cheers.
func (s *opsStack) done(d OpDone, u *gunim.UI) {
	if c := s.card(d.ID); c != nil && d.OK {
		c.cheer(u)
	}
}

// Children implements [gunim.Composite]: the cards come and go by
// Insert and Remove.
func (s *opsStack) Children() []gunim.Node { return nil }

// Layout implements [gunim.Node]: the newest card on top, each taking
// room as far as it has come in. A card coming or going keeps its place
// at the bottom of its room, so it never hangs out under the stack.
func (s *opsStack) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	w := min(OpCardWidth.Get(f.Theme), c.Max.W)
	gap := OpCardGap.Get(f.Theme)
	y := float32(0)
	for i := kids.Len() - 1; i >= 0; i-- {
		k := kids.At(i)
		size := k.Layout(gunim.Constraints{Min: geom.Sz(w, 0), Max: geom.Sz(w, c.Max.H)})
		in := float32(1)
		if card, ok := k.Node().(*opCard); ok {
			in = ease(card.appear.Value())
		}
		k.Place(geom.Pt(0, y-(size.H+gap)*(1-in)))
		y += (size.H + gap) * in
	}
	s.height = max(0, y-gap)
	return geom.Sz(w, s.height)
}

// Paint implements [gunim.Node].
func (s *opsStack) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

// ease is t kept between 0 and 1.
func ease(t float32) float32 { return min(max(t, 0), 1) }

// opCard is one operation: what it does, how fast, a graph of its speed
// across how far it has got, and buttons to pause, fold and stop it.
// Folded, it is one line with a ring for how far it has got.
type opCard struct {
	anim.Group
	id   int
	view OpView
	// appear runs to 1 as the card comes in, and back as it leaves;
	// folded runs to 1 as it folds to one line.
	appear, folded *anim.Float
	// done is how far the operation has got, as drawn: it glides on at
	// the operation's pace between reports.
	done glide

	icon                     *widget.Icon
	title, from, big, left   *widget.Label
	moved, file, note        *widget.Label
	pause, fold, cancel      *widget.IconButton
	close, slimClose         *widget.IconButton
	undo, slimUndo           *widget.Link
	badge                    *doneBadge
	graph                    *opGraph
	slimTitle, slimNote      *widget.Label
	unfold, slimCancel       *widget.IconButton
	slimBadge                *doneBadge
	ring                     *opRing
	fromRow, noteRow, pauses *shownIf
	undos, slimUndos         *shownIf
	running, ended           *shownIf
	slimRunning, slimEnded   *shownIf
	full, slim               gunim.Node
}

func newOpCard(v OpView) *opCard {
	c := &opCard{id: v.ID, appear: anim.NewFloat(0), folded: anim.NewFloat(0)}
	c.Add(c.appear, c.folded)
	small := func(ink theme.Token[color.NRGBA]) *widget.Label {
		l := widget.NewLabel("")
		l.Size, l.Color, l.MaxLines = SmallText, ink, 1
		return l
	}
	dismiss := func(*gunim.UI) gunim.Intent { return DismissOp{ID: c.id} }
	undo := func(*gunim.UI) gunim.Intent { return UndoOp{ID: c.id} }
	c.icon = widget.NewIcon(opIcon(v.Kind), "")
	c.icon.Color = widget.Accent
	c.title = widget.NewLabel("")
	c.title.Face, c.title.MaxLines = widget.BoldFont, 1
	c.from, c.moved, c.file, c.note = small(Faint), small(Faint), small(Faint), small(Faint)
	c.note.MaxLines = 2
	c.big = widget.NewLabel("")
	c.big.Face, c.big.Size, c.big.MaxLines = widget.BoldFont, OpSpeedSize, 1
	c.left = widget.NewLabel("")
	c.left.Size, c.left.Color, c.left.MaxLines = OpLeftSize, Faint, 1
	c.pause = widget.NewIconButton(icon.Pause, "Pause")
	c.pause.OnClick = func(*gunim.UI) gunim.Intent { return PauseOp{ID: c.id, Resume: c.view.Paused} }
	c.fold = widget.NewIconButton(icon.ChevronDown, "Fold")
	c.fold.OnClick = func(u *gunim.UI) gunim.Intent { c.setFold(true, u); return nil }
	c.cancel = widget.NewIconButton(icon.X, "Cancel")
	c.cancel.OnClick = widget.Sends(CancelOp{ID: v.ID})
	c.undo = widget.NewLink("Undo")
	c.undo.OnClick = undo
	c.close = widget.NewIconButton(icon.X, "Close")
	c.close.OnClick = dismiss
	c.graph = newOpGraph(&c.done)
	c.pauses = &shownIf{child: c.pause}
	runButtons := widget.Row(c.pauses, c.fold, c.cancel)
	runButtons.Gap = smallGap
	c.undos = &shownIf{child: c.undo}
	endButtons := widget.Row(c.undos, c.close)
	endButtons.Cross, endButtons.Gap = widget.CrossCenter, cardGap
	c.running, c.ended = &shownIf{child: runButtons, on: true}, &shownIf{child: endButtons}
	c.badge = newDoneBadge(c.icon)
	head := widget.Row(c.badge, c.title, c.running, c.ended).Grow(c.title, 1)
	head.Cross = widget.CrossCenter
	head.Gap = cardGap
	speed := widget.Row(c.big, c.left).Grow(c.big, 1)
	speed.Cross = widget.CrossEnd
	foot := widget.Row(c.file, c.moved).Grow(c.file, 1)
	foot.Gap = cardGap
	c.fromRow = &shownIf{child: c.from, top: -2}
	c.noteRow = &shownIf{child: c.note, top: 0}
	full := widget.Column(head, c.fromRow, speed, c.graph, foot, c.noteRow)
	full.Cross, full.Gap = widget.CrossStretch, cardGap
	c.full = &padded{child: full, pad: geom.Insets{Left: 14, Top: 10, Right: 10, Bottom: 12}}

	c.ring = &opRing{done: &c.done}
	c.slimTitle = widget.NewLabel("")
	c.slimTitle.MaxLines = 1
	c.slimNote = small(Faint)
	c.unfold = widget.NewIconButton(icon.ChevronUp, "Open")
	c.unfold.OnClick = func(u *gunim.UI) gunim.Intent { c.setFold(false, u); return nil }
	c.slimCancel = widget.NewIconButton(icon.X, "Cancel")
	c.slimCancel.OnClick = widget.Sends(CancelOp{ID: v.ID})
	c.slimUndo = widget.NewLink("Undo")
	c.slimUndo.OnClick = undo
	c.slimClose = widget.NewIconButton(icon.X, "Close")
	c.slimClose.OnClick = dismiss
	slimRun := widget.Row(c.unfold, c.slimCancel)
	slimRun.Gap = smallGap
	c.slimUndos = &shownIf{child: c.slimUndo}
	slimEnd := widget.Row(c.slimUndos, c.slimClose)
	slimEnd.Cross, slimEnd.Gap = widget.CrossCenter, cardGap
	c.slimRunning, c.slimEnded = &shownIf{child: slimRun, on: true}, &shownIf{child: slimEnd}
	c.slimBadge = newDoneBadge(c.ring)
	slim := widget.Row(c.slimBadge, c.slimTitle, c.slimNote, c.slimRunning, c.slimEnded).Grow(c.slimTitle, 1)
	slim.Cross, slim.Gap = widget.CrossCenter, cardGap
	c.slim = &padded{child: slim, pad: geom.Insets{Left: 12, Top: 6, Right: 6, Bottom: 6}}
	c.set(v, nil)
	return c
}

// cardGap is the room between the parts of a card.
var cardGap = theme.Length("files.op.inner", 8)

// opIcons are the icons of the operations, by kind.
var opIcons = map[OpKind]*icon.Icon{OpMove: icon.FolderInput, OpTrash: icon.Trash2, OpDelete: icon.Trash2,
	OpZip: icon.FileArchive, OpExtract: icon.FolderOutput, OpUndo: icon.Undo2}

// opIcon is the icon of an operation of kind.
func opIcon(kind OpKind) *icon.Icon {
	if ic, ok := opIcons[kind]; ok {
		return ic
	}
	return icon.Copy
}

func (c *opCard) set(v OpView, u *gunim.UI) {
	c.view = v
	title := v.Title
	if v.To != "" {
		title = strings.TrimSuffix(title, " to "+v.To)
	}
	c.title.Text, c.slimTitle.Text = title, title
	c.icon.Icon = opIcon(v.Kind)
	switch {
	case v.From != "" && v.To != "":
		c.from.Text = v.From + " → " + v.To
	case v.From != "":
		c.from.Text = "From " + v.From
	case v.To != "":
		c.from.Text = "To " + v.To
	}
	c.fromRow.on = v.From != "" || v.To != ""
	c.pauses.on = v.CanPause
	c.undos.on, c.slimUndos.on = v.Undo, v.Undo
	c.progress(v.OpProgress, u)
	if v.Ended && u != nil {
		c.cheer(u)
	}
}

// smallFiles is the size an operation's items are on the whole under
// which its card says how many items go a second, not how many bytes.
const smallFiles = 1 << 20

// progress takes how far the operation has got.
func (c *opCard) progress(p OpProgress, u *gunim.UI) {
	c.view.OpProgress = p
	waiting := p.Waiting != ""
	c.done.to(float64(p.Done), p.Unknown, p.Paused || waiting)
	c.graph.unknown = p.Unknown
	c.graph.paused = p.Paused || waiting
	c.graph.bar = p.BytesTotal == 0
	if !p.Paused {
		c.graph.speed = p.Speed
	}
	cheered := c.badge.cheering
	// Many items are counted: how many are done is what the user waits on.
	many := p.ItemsTotal > 1
	var big, left, file, moved, note string
	switch {
	case cheered:
		big = "Done"
	case p.Paused:
		big = "Paused"
	case waiting:
		big = "Waiting"
	case p.Unknown:
		big = "Counting…"
	case many:
		big = count(p.Items) + " of " + count(p.ItemsTotal)
	case p.BytesTotal > 0 && p.Speed > 0:
		big = humanBytes(int64(p.Speed)) + "/s"
	case p.Done > 0:
		big = percent(p.Done)
	default:
		big = "Starting…"
	}
	switch {
	case cheered:
		if c.view.Took >= 1 {
			left = "took " + duration(c.view.Took)
		}
	case !p.Paused && !waiting && !p.Unknown && p.Left >= 1:
		left = timeLeft(p.Left)
	case many && !p.Unknown:
		left = percent(p.Done)
	}
	if !p.Unknown {
		switch {
		case cheered:
		case many:
			file = p.File
		default:
			file = percent(p.Done)
		}
		if p.BytesTotal > 0 {
			moved = humanBytes(p.Bytes) + " of " + humanBytes(p.BytesTotal)
			if many && !cheered && !p.Paused && !waiting {
				if rate := c.rate(p); rate != "" {
					moved += " · " + rate
				}
			}
		}
	}
	switch {
	case cheered:
		c.note.Text = c.view.Note
	case waiting:
		c.note.Text = p.Waiting
	}
	switch {
	case cheered:
	case p.Paused:
		note = "Paused"
	case waiting:
		note = "Waiting"
	case p.Unknown:
		note = "Counting…"
	default:
		note = percent(p.Done)
		if left != "" && left != note {
			note += " · " + left
		}
	}
	c.big.Text, c.left.Text, c.moved.Text, c.file.Text = big, left, moved, file
	c.noteRow.on = c.note.Text != "" && (cheered || waiting)
	c.slimNote.Text = note
	c.pause.Icon, c.pause.Tooltip = icon.Pause, "Pause"
	if p.Paused {
		c.pause.Icon, c.pause.Tooltip = icon.Play, "Resume"
	}
	if u != nil {
		u.Invalidate()
	}
}

// rate says how fast the operation goes: in items a second where its
// items are small, else in bytes.
func (c *opCard) rate(p OpProgress) string {
	if p.ItemSpeed > 0 && p.BytesTotal/int64(p.ItemsTotal) < smallFiles {
		return fmt.Sprintf("%.0f items/s", p.ItemSpeed)
	}
	if p.Speed > 0 {
		return humanBytes(int64(p.Speed)) + "/s"
	}
	return ""
}

// cheer shows the operation went well: the icon turns to a check, and
// the buttons that steer it give way to Undo, where it can be undone,
// and one to close the card.
func (c *opCard) cheer(u *gunim.UI) {
	if c.badge.cheering {
		return
	}
	c.done.to(1, false, false)
	c.done.finish()
	c.running.on, c.slimRunning.on = false, false
	c.ended.on, c.slimEnded.on = true, true
	c.badge.cheer(u)
	c.slimBadge.cheer(u)
	c.graph.cheer(u)
	p := c.view.OpProgress
	p.Done, p.Bytes, p.Items, p.Paused, p.Waiting = 1, p.BytesTotal, p.ItemsTotal, false, ""
	c.progress(p, u)
}

// setFold folds the card to one line, or opens it again.
func (c *opCard) setFold(on bool, u *gunim.UI) {
	to := float32(0)
	if on {
		to = 1
	}
	c.folded.Animate(to, Page.Get(u.Theme()))
	u.Invalidate()
}

// Step implements [gunim.Animator].
func (c *opCard) Step(dt time.Duration) bool {
	moving := c.Group.Step(dt)
	if c.done.step(dt) {
		moving = true
	}
	return moving
}

// Transition implements [gunim.Transitioner]: the card slides in as its
// operation starts, and out once it has ended.
func (c *opCard) Transition(p gunim.Presence, f gunim.Frame) bool {
	switch p {
	case gunim.Entering:
		c.appear.Animate(1, widget.Settle.Get(f.Theme))
	case gunim.Exiting:
		c.appear.Animate(0, widget.Quick.Get(f.Theme))
	case gunim.Present:
	}
	return !c.appear.Active()
}

// Children implements [gunim.Composite].
func (c *opCard) Children() []gunim.Node { return []gunim.Node{c.full, c.slim} }

// Layout implements [gunim.Node]: the card is as tall as it is open, the
// side folded away out of reach of the pointer.
func (c *opCard) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	t := ease(c.folded.Value())
	w := cs.Max.W
	full, slim := kids.At(0), kids.At(1)
	fs := full.Layout(gunim.Constraints{Min: geom.Sz(w, 0), Max: geom.Sz(w, cs.Max.H)})
	ss := slim.Layout(gunim.Constraints{Min: geom.Sz(w, 0), Max: geom.Sz(w, cs.Max.H)})
	away := geom.Pt(-1e5, 0)
	full.Place(geom.Point{})
	slim.Place(geom.Point{})
	switch {
	case t >= 0.999:
		full.Place(away)
	case t <= 0.001:
		slim.Place(away)
	}
	return geom.Sz(w, fs.H+(ss.H-fs.H)*t)
}

// Paint implements [gunim.Node]: the card comes in from the right as the
// toasts do, and fades between open and folded.
func (c *opCard) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	in := ease(c.appear.Value())
	if in <= 0.001 {
		return
	}
	r := geom.Rect{Max: box.Point()}
	radius := widget.CardRadius.Get(th)
	defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-24)), Opacity: in})()
	defer p.Push(paint.Translate(geom.Pt(48*(1-in), 0)))()
	p.ShadowRRect(r, radius, paint.Solid(widget.DialogFill.Get(th)),
		paint.Shadow{Offset: geom.Pt(0, 4), Blur: 16, Color: widget.DialogShadow.Get(th)})
	p.RRectStroke(r, radius, paint.Fill{}, paint.Stroke{Width: 1, Color: widget.DialogBorder.Get(th)})
	t := ease(c.folded.Value())
	defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: 1, Clip: true, Radius: radius})()
	// One side fades out before the other fades in, so their words never
	// lie over each other.
	if out := ease(1 - 2*t); out > 0.001 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: out})()
			kids.At(0).Paint(p)
		}()
	}
	if in := ease(2*t - 1); in > 0.001 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: in})()
			kids.At(1).Paint(p)
		}()
	}
}

// percent says d, from 0 to 1, as a whole percentage, which only says
// 100% once it is all done.
func percent(d float32) string {
	n := int(math.Floor(float64(d)*100 + 0.5))
	if n >= 100 && d < 1 {
		n = 99
	}
	return fmt.Sprintf("%d%%", n)
}

// timeLeft says secs as the time left.
func timeLeft(secs float64) string { return duration(secs) + " left" }

// duration says secs to the second under a minute, to the minute under
// an hour, and in hours and minutes beyond.
func duration(secs float64) string {
	s := int(math.Round(secs))
	switch {
	case s < 60:
		return fmt.Sprintf("%d s", s)
	case s < 3600:
		if m, r := s/60, s%60; m < 10 && r != 0 {
			return fmt.Sprintf("%d min %d s", m, r)
		}
		return fmt.Sprintf("%d min", (s+30)/60)
	default:
		return fmt.Sprintf("%d h %d min", s/3600, (s%3600+30)/60)
	}
}

// glide is how far an operation has got as drawn. Between reports it
// moves on at the pace the reports have come at, and it bends toward
// each report rather than leaping to it, so the progress moves evenly
// and never back.
type glide struct {
	shown, target float64
	// pace is how far a second the operation goes, from its reports,
	// and at when the last report came.
	pace float64
	at   time.Time
	// unknown and paused hold the glide still.
	unknown, paused bool
	started         bool
}

// glideCatch is how many times a second the glide closes the gap to the
// last report, over and above its pace.
const glideCatch = 2.5

// to takes a report: the operation has got v of the way.
func (g *glide) to(v float64, unknown, paused bool) {
	now := time.Now()
	g.unknown, g.paused = unknown, paused
	if unknown {
		return
	}
	if !g.started {
		g.started, g.shown, g.target, g.at = true, v, v, now
		return
	}
	if dt := now.Sub(g.at).Seconds(); dt > 0.02 && v > g.target && !paused {
		seen := (v - g.target) / dt
		if g.pace == 0 {
			g.pace = seen
		} else {
			g.pace += (seen - g.pace) * min(1, dt/1.5)
		}
	}
	if paused {
		g.pace = 0
	}
	g.target = max(g.target, v)
	g.at = now
}

// finish has the glide end at the target a little quicker.
func (g *glide) finish() { g.pace = max(g.pace, (g.target-g.shown)*3) }

// step moves the glide on by dt, and reports whether it is moving.
func (g *glide) step(dt time.Duration) bool {
	if g.unknown || !g.started || g.shown >= 1 {
		return false
	}
	s := dt.Seconds()
	gap := g.target - g.shown
	pace := g.pace
	if g.paused {
		pace = 0
	}
	v := max(0, pace+gap*glideCatch)
	// It may run on a little past the last report, as far as the pace
	// takes it in a fifth of a second, while reports are late.
	limit := g.target + pace*0.2
	g.shown = min(g.shown+v*s, limit, 1)
	return v > 1e-6 && g.shown < limit
}

// value is how far the operation has got as drawn.
func (g *glide) value() float32 { return float32(g.shown) }

// opGraph draws an operation's speed across how far it has got, as
// Windows does: the fill is how much is done, the line at its end is
// now, and a dashed line across says the speed now. An operation that
// moves no bytes it can count shows a plain bar.
type opGraph struct {
	anim.Group
	done *glide
	// samples are the speeds, by how far the operation had got.
	samples []speedAt
	// top is the speed at the top of the graph, gliding as it changes.
	top *anim.Float
	// green runs to 1 as the operation cheers.
	green *anim.Float
	// speed is the speed now; unknown and paused say the operation counts
	// or is paused; bar draws a bar in place of the graph.
	speed                float64
	unknown, paused, bar bool
	// spin counts the seconds while the operation counts.
	spin float64
}

type speedAt struct {
	done float32
	rate float64
}

func newOpGraph(done *glide) *opGraph {
	g := &opGraph{done: done, top: anim.NewFloat(0), green: anim.NewFloat(0)}
	g.Add(g.top, g.green)
	return g
}

// add takes a sample of the speed, once the operation had got done of
// the way.
func (g *opGraph) add(done float32, rate float64, u *gunim.UI) {
	if n := len(g.samples); n > 0 && done < g.samples[n-1].done {
		return
	}
	g.samples = append(g.samples, speedAt{done: done, rate: rate})
	most := 0.0
	for _, s := range g.samples {
		most = max(most, s.rate)
	}
	top := float32(most * 1.25)
	switch {
	case g.top.Target() == 0:
		g.top.Jump(top)
	case math.Abs(float64(top-g.top.Target())) > 0.05*float64(g.top.Target()):
		g.top.Animate(top, widget.Settle.Get(u.Theme()))
	}
}

// cheer turns the graph green.
func (g *opGraph) cheer(u *gunim.UI) {
	g.green.Animate(1, widget.Settle.Get(u.Theme()))
}

// Step implements [gunim.Animator].
func (g *opGraph) Step(dt time.Duration) bool {
	moving := g.Group.Step(dt)
	if g.unknown {
		g.spin += dt.Seconds()
		moving = true
	}
	return moving
}

// Layout implements [gunim.Node].
func (g *opGraph) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	h := OpGraphHeight.Get(f.Theme)
	if g.bar {
		h = OpBarHeight.Get(f.Theme)
	}
	return geom.Sz(c.Max.W, h)
}

// rateAt is the speed when the operation had got d of the way, between
// the samples either side.
func (g *opGraph) rateAt(d float32) float64 {
	n := len(g.samples)
	i, _ := slices.BinarySearchFunc(g.samples, d, func(s speedAt, d float32) int {
		switch {
		case s.done < d:
			return -1
		case s.done > d:
			return 1
		}
		return 0
	})
	switch {
	case i == 0:
		return g.samples[0].rate
	case i >= n:
		return g.samples[n-1].rate
	}
	a, b := g.samples[i-1], g.samples[i]
	if b.done <= a.done {
		return b.rate
	}
	t := float64((d - a.done) / (b.done - a.done))
	return a.rate + (b.rate-a.rate)*t
}

// Paint implements [gunim.Node].
func (g *opGraph) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink := anim.Mix(anim.ColorCodec, widget.Accent.Get(th), Success.Get(th), ease(g.green.Value()))
	track := widget.Ink.Get(th)
	track.A = 0x10
	radius := min(6, box.H/2)
	r := geom.Rect{Max: box.Point()}
	p.RRect(r, radius, paint.Solid(track))
	defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: 1, Clip: true, Radius: radius})()
	if g.unknown {
		// A soft band sweeps across while the operation counts.
		w := box.W * 0.3
		x := -w + (box.W+w)*float32(math.Mod(g.spin, 1.4)/1.4)
		edge := ink
		edge.A = 0
		mid := ink
		mid.A = 0x60
		p.RRect(geom.Rc(x, 0, w/2, box.H), 0, paint.Fill{Gradient: &paint.Gradient{From: geom.Pt(x, 0), To: geom.Pt(x+w/2, 0), Start: edge, End: mid}})
		p.RRect(geom.Rc(x+w/2, 0, w/2, box.H), 0, paint.Fill{Gradient: &paint.Gradient{From: geom.Pt(x+w/2, 0), To: geom.Pt(x+w, 0), Start: mid, End: edge}})
		return
	}
	now := box.W * g.done.value()
	if g.bar || len(g.samples) < 2 || g.top.Value() <= 0 {
		fill := ink
		if !g.bar {
			fill.A = 0x40
		}
		p.RRect(geom.Rc(0, 0, now, box.H), radius, paint.Solid(fill))
		return
	}
	top := float64(g.top.Value())
	yOf := func(v float64) float32 { return 2 + (box.H-2)*(1-float32(min(v/top, 1))) }
	hi, lo := ink, ink
	hi.A, lo.A = 0x80, 0x14
	grad := &paint.Gradient{From: geom.Pt(0, 0), To: geom.Pt(0, box.H), Start: hi, End: lo}
	// Columns a device pixel wide, whose edges fall on device pixels.
	dev := max(f.Scale, 1)
	col := 1 / dev
	var line []geom.Point
	for x := float32(0); x < now; x += col {
		next := min(x+col, now)
		y := yOf(g.rateAt((x + next) / 2 / box.W))
		p.RRect(geom.Rect{Min: geom.Pt(x, y), Max: geom.Pt(next, box.H)}, 0, paint.Fill{Gradient: grad})
	}
	for x := float32(0); ; x += 2 {
		x = min(x, now)
		line = append(line, geom.Pt(x, yOf(g.rateAt(x/box.W))))
		if x >= now {
			break
		}
	}
	for i := 1; i < len(line); i++ {
		a, b := line[i-1], line[i]
		if d := dist(a, b); d > 0 {
			angle := float32(math.Atan2(float64(b.Y-a.Y), float64(b.X-a.X)))
			func() {
				defer p.Push(paint.Rotate(angle, a))()
				p.RRect(geom.Rc(a.X, a.Y-0.75, d+0.5, 1.5), 0.75, paint.Solid(ink))
			}()
		}
	}
	// Now: a line up at how far it has got.
	if now < box.W-1 {
		p.RRect(geom.Rc(now-0.75, 0, 1.5, box.H), 0, paint.Solid(ink))
	}
	// The speed now, dashed across the whole width.
	if g.speed > 0 && !g.paused && g.green.Target() == 0 {
		dash := widget.Ink.Get(th)
		dash.A = 0x50
		y := yOf(g.speed)
		for x := float32(0); x < box.W; x += 8 {
			p.RRect(geom.Rc(x, y-0.5, 4, 1), 0, paint.Solid(dash))
		}
	}
}

// opRing is a folded card's ring, round as far as the operation has got.
type opRing struct {
	done *glide
}

// Layout implements [gunim.Node].
func (r *opRing) Layout(gunim.Constraints, gunim.Frame, gunim.Children) geom.Size {
	return geom.Sz(18, 18)
}

// Paint implements [gunim.Node].
func (r *opRing) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	track := widget.Ink.Get(th)
	track.A = 0x24
	accent := widget.Accent.Get(th)
	if r.done.value() >= 1 {
		accent = Success.Get(th)
	}
	mid := geom.Pt(box.W/2, box.H/2)
	rad := box.W/2 - 1.5
	const n = 64
	from, to := 0.0, float64(r.done.value())
	if r.done.unknown {
		// A quarter of the ring goes round while the operation counts.
		from = math.Mod(float64(time.Now().UnixMilli())/1200, 1)
		to = from + 0.25
	}
	for i := range n {
		a0, a1 := float64(i)/n, float64(i+1)/n
		c := track
		if a := a0 + 0.5/n; (a >= from && a < to) || a+1 < to {
			c = accent
		}
		pt := func(t float64) geom.Point {
			ang := -math.Pi/2 + 2*math.Pi*t
			return mid.Add(geom.Pt(rad*float32(math.Cos(ang)), rad*float32(math.Sin(ang))))
		}
		a, b := pt(a0), pt(a1)
		angle := float32(math.Atan2(float64(b.Y-a.Y), float64(b.X-a.X)))
		func() {
			defer p.Push(paint.Rotate(angle, a))()
			p.RRect(geom.Rc(a.X, a.Y-1.25, dist(a, b)+0.5, 2.5), 1.25, paint.Solid(c))
		}()
	}
}

// shownIf shows its child while on, and takes no room while off.
type shownIf struct {
	child gunim.Node
	on    bool
	// top is room over the child while it shows.
	top float32
}

// Children implements [gunim.Composite].
func (s *shownIf) Children() []gunim.Node { return []gunim.Node{s.child} }

// Layout implements [gunim.Node].
func (s *shownIf) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	size := k.Layout(c)
	if !s.on {
		k.Place(geom.Pt(-1e5, 0))
		return geom.Size{}
	}
	k.Place(geom.Pt(0, s.top))
	return geom.Sz(size.W, size.H+s.top)
}

// Paint implements [gunim.Node].
func (s *shownIf) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	if s.on {
		kids.At(0).Paint(p)
	}
}

// padded pads its child.
type padded struct {
	child gunim.Node
	pad   geom.Insets
}

// Children implements [gunim.Composite].
func (b *padded) Children() []gunim.Node { return []gunim.Node{b.child} }

// Layout implements [gunim.Node].
func (b *padded) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	w := c.Max.W - b.pad.Left - b.pad.Right
	s := k.Layout(gunim.Constraints{Min: geom.Sz(w, 0), Max: geom.Sz(w, max(0, c.Max.H-b.pad.Top-b.pad.Bottom))})
	k.Place(geom.Pt(b.pad.Left, b.pad.Top))
	return geom.Sz(c.Max.W, s.H+b.pad.Top+b.pad.Bottom)
}

// Paint implements [gunim.Node].
func (b *padded) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}
