package eq

import (
	"fmt"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugui"
	"github.com/marrasen/gunim/audio/plug/tonal"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// full is the EQ as the plugin's pane shows it. Along the top: a switch
// between the graph and the list of bands; the button that matches the
// track to a target, pink noise or the slope music commonly takes, with
// a bar of how far it has read the track, the menu beside it that picks
// the target, and a button to stop it, gliding in while it reads; and
// the knobs: the target's knee, where it has one, and those that work
// on every band, the amount, the Q, the threshold, and the gain.
// Under them, the graph with the band picked's row under it, or the
// list of every band's row, crossfading as the switch turns.
type full struct {
	anim.Group
	p      *plugin
	host   plug.Host
	panel  *panel
	g      *graph
	views  *widget.Segmented
	match  *widget.Button
	target *widget.MenuButton
	stop   *widget.IconButton
	knobs  []*plugui.Knob
	row    *bandRow
	list   *bandList
	scroll *widget.Scroll
	// tuner tunes how the graph shows behind the list, and bar is the
	// pane of glass the controls along the top stand on.
	tuner *plugui.Tuner
	bar   *glassPane
	// listed is how far the list shows, from 0 for the graph to 1, and
	// stopIn how far the stop button is in.
	listed, stopIn *anim.Float
	// listening says the track is being read for Auto, progress how far,
	// and shimmer runs along the bar as it reads; stopPoll stops looking
	// how far it has got, and calm puts the button's words back after
	// it tells how it went.
	listening bool
	// narrow says the bar has too little room for the match button's
	// words, which it says shorter.
	narrow   bool
	progress float32
	shimmer  float32
	stopPoll func()
	calm     func()
	size     geom.Size
	// matchAt, rowAt and barAt are where the match button, the band's
	// row and the bar along the top are.
	matchAt, rowAt, barAt geom.Rect
}

// matchWords are the match button's words at rest, matchShort its
// words at rest where the bar is narrow, matchedWords its words once
// done, and matchTip its tooltip: each for the target to.
func matchWords(to tonal.Target) string   { return "Match to " + to.Name }
func matchShort(to tonal.Target) string   { return to.Short }
func matchedWords(to tonal.Target) string { return "Matched to " + to.Name }
func matchTip(to tonal.Target) string {
	return "Read the whole track as it reaches the EQ, and shape the bands so it sounds as even as " + to.Name +
		". The bands it makes take the place of these; Undo brings them back."
}

// targetW is how wide the menu that picks the target stands.
const targetW = 32

// to is the target Auto brings the track to.
func (v *full) to() tonal.Target {
	return tonal.TargetAt(v.host.Param(TargetID)).At(v.host.Param(KneeID))
}

// rest says whether the match button's words are its words at rest.
func (v *full) rest() bool {
	return v.match.Label == matchWords(v.to()) || v.match.Label == matchShort(v.to())
}

// targetItems are the targets, the one picked ticked.
func (v *full) targetItems() []widget.MenuItem {
	at := v.to()
	items := []widget.MenuItem{{Label: "Match to", Caption: true}}
	for _, t := range tonal.Targets {
		items = append(items, widget.MenuItem{Label: strings.ToUpper(t.Name[:1]) + t.Name[1:], Hint: t.Hint,
			Checked: t.Short == at.Short})
	}
	return items
}

func newFull(p *plugin, h plug.Host) *full {
	pn := newPanel(p, h)
	v := &full{p: p, host: h, panel: pn, g: pn.graph, listed: anim.NewFloat(0), stopIn: anim.NewFloat(0),
		tuner: plugui.NewTuner("Tune how the graph shows behind the bands")}
	v.Add(v.listed, v.stopIn)
	v.bar = &glassPane{radius: 12}
	v.views = widget.NewSegmented("Graph", "Bands")
	v.views.Tooltip = "Show the graph, with the band picked under it, or every band with all its knobs"
	v.views.OnChange = func(i int, u *gunim.UI) gunim.Intent {
		v.listed.Animate(float32(i), widget.Settle.Get(u.Theme()))
		if i == 0 {
			v.tuner.SetOpen(false, u)
		}
		v.list.show(u)
		u.Invalidate()
		return nil
	}
	v.match = widget.NewButton(matchWords(v.to()))
	v.match.Tooltip = matchTip(v.to())
	v.match.OnClick = func(u *gunim.UI) gunim.Intent {
		v.listen(u)
		return nil
	}
	v.target = widget.NewMenuButton("", v.targetItems())
	v.target.Icon, v.target.Tooltip = icon.ChevronDown, "Pick what to match the track to"
	v.target.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		// The caption first, then the targets.
		if k := i - 1; k >= 0 && k < len(tonal.Targets) {
			h.Begin(TargetID, u)
			h.Set(TargetID, float64(k), u)
			h.End(TargetID, u)
			v.retarget(u)
		}
		return nil
	}
	v.stop = widget.NewIconButton(icon.Square, "Stop reading the track")
	v.stop.OnClick = func(u *gunim.UI) gunim.Intent {
		v.halt(u)
		return nil
	}
	tips := map[uint32]string{
		AmountID:    "How far every bell and shelf lifts or lowers, their dynamics too, from none to twice",
		QID:         "Every bell, shelf and notch narrower, over 100%, or wider, under it; the cuts stay as they are",
		ThresholdID: "Every dynamic band's threshold, moved up, so it turns down later, or down, so it turns down sooner",
		GainID:      "The gain on the sound the EQ passes, to keep it as loud as it came",
		KneeID:      "Where the music slope stops running level and starts to fall 6 dB an octave",
	}
	for _, id := range []uint32{KneeID, AmountID, QID, ThresholdID, GainID} {
		prm, _ := Descriptor.Param(id)
		k := plugui.NewKnob(h, prm)
		k.Compact, k.Tooltip = true, tips[id]
		v.knobs = append(v.knobs, k)
	}
	v.knobs[0].Disabled = !v.to().Kneed
	v.row = newBandRow(v.g, false)
	v.row.glass = true
	v.list = newBandList(v.g)
	v.scroll = widget.NewScroll(v.list)
	// Its edges never fade, which would draw the rows apart from the
	// graph, and their glass would blur nothing.
	v.scroll.NoFade = true
	v.g.onChange = v.changed
	return v
}

// changed shows the band picked in the row, and every band in the list.
func (v *full) changed(u *gunim.UI) {
	b, i := v.g.band(v.g.sel)
	v.row.show(b, i, u)
	v.list.show(u)
	v.matchable()
}

// matchable greys the match button where there is no track to read.
func (v *full) matchable() {
	if v.listening {
		return
	}
	v.match.Disabled = !v.p.canRead()
	v.target.Disabled = false
	v.knobs[0].Disabled = !v.to().Kneed
	v.match.Tooltip = matchTip(v.to())
	if v.match.Disabled {
		v.match.Tooltip = "The host gives the EQ no track to read"
	}
}

// retarget words the match button and ticks the menu for the target as
// it is set.
func (v *full) retarget(u *gunim.UI) {
	v.target.SetItems(v.targetItems())
	if v.rest() || !v.listening && v.calm == nil {
		v.match.Label = matchWords(v.to())
	}
	v.matchable()
	u.Invalidate()
}

// listen starts reading the track, for Auto, and follows it.
func (v *full) listen(u *gunim.UI) {
	if v.listening || !v.p.canRead() {
		return
	}
	if v.calm != nil {
		v.calm()
		v.calm = nil
	}
	v.listening, v.progress = true, 0
	v.match.Active, v.target.Disabled = true, true
	v.stopIn.Animate(1, widget.Bounce.Get(u.Theme()))
	v.p.startReading()
	v.watchReading(u)
}

// halt stops reading the track.
func (v *full) halt(u *gunim.UI) {
	if !v.listening {
		return
	}
	v.p.stopReading()
	if v.stopPoll != nil {
		v.stopPoll()
	}
	v.done(matchWords(v.to()), u)
}

// readPoll is how often Auto looks how far the reading has got, and
// tellFor how long the button tells how it went.
const (
	readPoll = 50 * time.Millisecond
	tellFor  = 2500 * time.Millisecond
)

// watchReading looks how far the reading has got: while it reads, it
// shows how far and looks again a moment later; once the track is read,
// it shapes the bands to bring it to the target, as one step to undo.
func (v *full) watchReading(u *gunim.UI) {
	if !v.listening {
		return
	}
	levels, reading, progress, err := v.p.readState()
	switch {
	case reading:
		v.progress = float32(progress)
		v.match.Label = fmt.Sprintf("Reading the track… %.0f%%", 100*progress)
		if v.narrow {
			v.match.Label = fmt.Sprintf("Reading… %.0f%%", 100*progress)
		}
		v.stopPoll = u.After(readPoll, v.watchReading)
		u.Invalidate()
		return
	case err != nil:
		v.match.Tooltip = "Could not read the track: " + err.Error()
		v.done("Could not read the track", u)
	case len(levels) == len(autoFreqs):
		bands := autoBands(levels, v.to())
		v.g.edit("Auto EQ", true, u, func() bool {
			v.g.bands, v.g.sel = bands, 0
			return true
		})
		u.Cue(gunim.CueSelect, v)
		v.done(matchedWords(v.to()), u)
	default:
		v.done(matchWords(v.to()), u)
	}
}

// done ends the reading, the button saying words for a moment.
func (v *full) done(words string, u *gunim.UI) {
	v.listening, v.match.Active, v.target.Disabled = false, false, false
	v.stopIn.Animate(0, widget.Settle.Get(u.Theme()))
	v.match.Label = words
	if words != matchWords(v.to()) {
		v.calm = u.After(tellFor, func(u *gunim.UI) {
			v.match.Label, v.calm = matchWords(v.to()), nil
			v.matchable()
			u.Invalidate()
		})
	}
	u.Invalidate()
}

// Step implements [gunim.Animator]: the shimmer runs along the bar
// while the track is read.
func (v *full) Step(dt time.Duration) bool {
	moving := v.Group.Step(dt)
	if v.listening {
		v.shimmer += float32(dt.Seconds()) * 0.8
		v.shimmer -= float32(int(v.shimmer))
		return true
	}
	return moving
}

// FollowState implements [plug.StateFollower]: the graph, the row and
// the list show the bands.
func (v *full) FollowState(u *gunim.UI) {
	v.panel.FollowState(u)
	v.changed(u)
}

// FollowDocument implements [plug.DocumentFollower]: the match button
// can read the track, or not.
func (v *full) FollowDocument(u *gunim.UI) {
	v.matchable()
	u.Invalidate()
}

// Follow implements [plug.Follower].
func (v *full) Follow(id uint32, val float64, u *gunim.UI) {
	v.panel.Follow(id, val, u)
	if id == TargetID {
		v.retarget(u)
	}
	for _, k := range v.knobs {
		if k.Param().ID == id {
			k.Follow(val, u)
		}
	}
}

// Children implements [gunim.Composite].
func (v *full) Children() []gunim.Node {
	// The bar's glass before what stands on it, which the pointer reaches
	// first.
	// The tuner's card last, over the rest.
	kids := []gunim.Node{v.panel, v.row, v.scroll, v.bar, v.views, v.match, v.stop, v.target}
	for _, k := range v.knobs {
		kids = append(kids, k)
	}
	return append(kids, v.tuner.Button, v.tuner.Card)
}

// firstKnob is where the knobs start among the view's children; the
// tuner's button and card follow them.
const firstKnob = 8

// barH is how tall the things on the bar along the top are, barInset
// how far in from the view's edges the bar stands, and barPadX and
// barPadY the room on it round them; gap is the room between parts, and
// rowInset how far the band picked's row stands in from the graph's
// edges.
const (
	barH     = plugui.CompactH
	barInset = 12
	barPadX  = 12
	barPadY  = 4
	gap      = 10
	rowInset = 16
	// rowFoot is how far up from the graph's foot the row stands, clear
	// of the frequencies written along it.
	rowFoot = 30
)

// Layout implements [gunim.Node].
func (v *full) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	v.size = c.Max
	size := v.size
	// The bar along the top, a pane of glass over the graph, in from its
	// edges: on it the switch at the left, the knobs at the right, and
	// the match button, and the stop button as far in as it is, before
	// the knobs.
	v.barAt = geom.Rc(barInset, barInset, max(size.W-2*barInset, 0), barH+2*barPadY)
	o := v.barAt.Min.Add(geom.Pt(barPadX, barPadY))
	right := v.barAt.Max.X - barPadX
	sw := kids.At(4).Layout(gunim.Constraints{Max: geom.Sz(220, 32)})
	kids.At(4).Place(o.Add(geom.Pt(0, (barH-sw.H)/2)))
	// The knee's knob, the first, only for a target with a knee: else it
	// takes no room, out of the view.
	from := 0
	if !v.to().Kneed {
		from = 1
		kids.At(firstKnob).Layout(gunim.Tight(geom.Size{}))
		kids.At(firstKnob).Place(geom.Pt(-1e4, -1e4))
	}
	knobsW := float32(len(v.knobs)-from) * plugui.CompactW
	for i := from; i < len(v.knobs); i++ {
		k := kids.At(firstKnob + i)
		k.Layout(gunim.Tight(geom.Sz(plugui.CompactW, plugui.CompactH)))
		k.Place(geom.Pt(right-knobsW+float32(i-from)*plugui.CompactW, o.Y))
	}
	x := right - knobsW - gap
	stopIn := min(max(v.stopIn.Value(), 0), 1)
	stopW := (32 + 6) * stopIn
	if stopIn < 0.01 && v.stopIn.Target() == 0 {
		kids.At(6).Layout(gunim.Tight(geom.Size{}))
	} else {
		kids.At(6).Layout(gunim.Tight(geom.Sz(32, 32)))
	}
	kids.At(6).Place(geom.Pt(x-32, o.Y+(barH-32)/2))
	// The match button, and the menu of targets after it, in the room
	// between the switch, with the tuner's button after it, and the
	// knobs: its words shorter where the room is short, and none of it
	// where it has none.
	room := x - stopW - targetW - 4 - (o.X + sw.W + gap + 32 + gap)
	if v.rest() {
		v.match.Label = matchWords(v.to())
		full := kids.At(5).Layout(gunim.Constraints{Max: geom.Sz(400, 32)})
		v.narrow = full.W > room
		if v.narrow {
			v.match.Label = matchShort(v.to())
		}
	}
	mw := kids.At(5).Layout(gunim.Constraints{Max: geom.Sz(400, 32)})
	tAt := geom.Pt(x-stopW-targetW, o.Y+(barH-32)/2)
	v.matchAt = geom.Rc(tAt.X-4-mw.W, o.Y+(barH-mw.H)/2, mw.W, mw.H)
	kids.At(5).Place(v.matchAt.Min)
	kids.At(7).Layout(gunim.Tight(geom.Sz(targetW, 32)))
	kids.At(7).Place(tAt)
	if mw.W > room {
		// No room: out of the view, where it is not drawn and the pointer
		// does not reach it.
		v.matchAt = geom.Rect{}
		kids.At(5).Place(geom.Pt(-1e4, -1e4))
		kids.At(7).Place(geom.Pt(-1e4, -1e4))
	}
	// The graph fills the view, under the bar too, the band picked's row
	// over it, on glass, centred near its foot; the list comes over it,
	// under the bar, the graph darkening behind it. The one out of sight
	// takes no room, so the pointer reaches only the one shown.
	listed := min(max(v.listed.Value(), 0), 1)
	rowH := float32(plugui.KnobH)
	v.g.behind = listed
	kids.At(0).Layout(gunim.Tight(size))
	kids.At(0).Place(geom.Point{})
	rowW := min(size.W-2*rowInset, v.row.shownWidth())
	if listed > 0.99 && v.listed.Target() == 1 {
		kids.At(1).Layout(gunim.Tight(geom.Size{}))
	} else {
		kids.At(1).Layout(gunim.Tight(geom.Sz(rowW, rowH)))
	}
	v.rowAt = geom.Rc((size.W-rowW)/2, max(size.H-rowH-rowFoot, 0), rowW, rowH)
	kids.At(1).Place(v.rowAt.Min)
	top := v.barAt.Max.Y + gap
	if listed < 0.01 && v.listed.Target() == 0 {
		kids.At(2).Layout(gunim.Tight(geom.Size{}))
	} else {
		kids.At(2).Layout(gunim.Tight(geom.Sz(size.W, max(size.H-top, 0))))
	}
	kids.At(2).Place(geom.Pt(0, top))
	// The tuner's button, after the switch, as the list shows; its card
	// under the bar, as it opens.
	tuneShown := !(listed < 0.01 && v.listed.Target() == 0)
	tb, tc := v.tunerAt()
	if tuneShown {
		kids.At(tb).Layout(gunim.Tight(geom.Sz(32, 32)))
	} else {
		kids.At(tb).Layout(gunim.Tight(geom.Size{}))
	}
	kids.At(tb).Place(o.Add(geom.Pt(sw.W+gap, (barH-32)/2)))
	kids.At(tc).Layout(gunim.Constraints{Max: geom.Sz(plugui.TunerW, plugui.TunerH)})
	kids.At(tc).Place(geom.Pt(o.X+sw.W+gap, v.barAt.Max.Y+4))
	kids.At(3).Layout(gunim.Tight(v.barAt.Size()))
	kids.At(3).Place(v.barAt.Min)
	return size
}

// Paint implements [gunim.Node]: the bar along the top, the graph and
// its row faded out as the list fades in, and the bar of how far the
// track is read under the match button.
func (v *full) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	listed := min(max(v.listed.Value(), 0), 1)
	fade := func(i int, alpha float32, dy float32) {
		if alpha < 0.01 {
			return
		}
		defer p.Push(paint.Translate(geom.Pt(0, dy)))()
		if alpha > 0.999 {
			// Whole, it draws in place: the glass of the list's rows
			// blurs the graph drawn before them.
			kids.At(i).Paint(p)
			return
		}
		end := p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}.Inset(geom.Uniform(-16)), Opacity: alpha})
		kids.At(i).Paint(p)
		end()
	}
	// The graph stays, darkened as the list comes over it.
	kids.At(0).Paint(p)
	fade(1, 1-listed, -listed*12)
	fade(2, listed, (1-listed)*12)
	if b, _ := v.g.band(v.g.sel); b == nil && listed < 0.99 {
		v.paintHint(p, th, box, (1-listed)*(1-min(max(v.row.in.Value(), 0), 1)))
	}
	// The bar's glass, then what stands on it.
	kids.At(3).Paint(p)
	kids.At(4).Paint(p)
	if v.matchAt.Size().W > 0 {
		kids.At(5).Paint(p)
		kids.At(7).Paint(p)
	}
	fade(6, min(max(v.stopIn.Value(), 0), 1), 0)
	for i := range v.knobs {
		if i > 0 || v.to().Kneed {
			kids.At(firstKnob + i).Paint(p)
		}
	}
	tb, tc := v.tunerAt()
	fade(tb, listed, 0)
	// The tuner's card, over the rest.
	kids.At(tc).Paint(p)
}

// tunerAt returns where the tuner's button and card are among the
// view's children.
func (v *full) tunerAt() (button, card int) {
	return firstKnob + len(v.knobs), firstKnob + len(v.knobs) + 1
}

// glassPane is a pane of glass over a display, as the bar along the top
// is: it blurs what is drawn behind it, and a click on it reaches nothing
// under it.
type glassPane struct{ radius float32 }

// Layout implements [gunim.Node].
func (*glassPane) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Max
}

// Paint implements [gunim.Node].
func (g *glassPane) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	plugui.PaintGlass(p, f.Theme, geom.Rect{Max: box.Point()}, g.radius)
}

// The hint shown where the band's row stands while no band is picked,
// a line each.
var hintLines = []string{
	"Pick a band to change it, or double-click the graph to add one",
	"Drag a point to move it, scroll on it to change its width, and right-click it for more",
}

// paintHint draws how to pick a band, centred on a card of glass where
// the band's row stands, as far in as t, fading as the row comes in.
func (v *full) paintHint(p *paint.Painter, th *theme.Live, box geom.Size, t float32) {
	if t < 0.01 {
		return
	}
	size := ReadoutSize.Get(th)
	ink := widget.Ink.Get(th)
	card, runs := v.hintCard(size, box)
	at := card.Center()
	defer p.Push(paint.Translate(geom.Pt(0, (1-t)*8)))()
	end := p.Layer(paint.LayerOpts{Bounds: card.Inset(geom.Uniform(-8)), Opacity: t})
	defer end()
	plugui.PaintGlass(p, th, card, 12)
	for i, run := range runs {
		alpha := float32(0.85)
		if i > 0 {
			alpha = 0.55
		}
		// Cut short where the card is narrower than the words.
		room := card.Size().W - 2*hintPad
		x := at.X - min(run.Advance, room)/2
		paintFit(p, hintLines[i], size, i == 0, geom.Pt(x, card.Min.Y+hintPad+float32(i)*(size+6)), room,
			audioui.Faded(ink, alpha))
	}
}

// hintCard returns where the hint's card is, centred where the band's
// row stands, in a view box big, its words size big; and the words.
func (v *full) hintCard(size float32, box geom.Size) (geom.Rect, []text.Run) {
	runs := make([]text.Run, len(hintLines))
	w := float32(0)
	for i, line := range hintLines {
		runs[i] = audioui.Shaped(line, size, i == 0, false)
		w = max(w, runs[i].Advance)
	}
	w = min(w+2*hintPad, box.W-2*rowInset)
	h := float32(len(runs))*(size+6) + 2*hintPad - 6
	at := v.rowAt.Center()
	return geom.Rc(at.X-w/2, at.Y-h/2, w, h), runs
}

// hintPad is the room round the hint's words on its card.
const hintPad = 14
