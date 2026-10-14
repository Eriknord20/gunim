package eq

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugui"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// dot is a band as the graph shows it: its settings gliding, how far
// it has grown in, and how lit it is.
type dot struct {
	// logHz is the band's frequency as an octave count, gain its gain,
	// logQ its Q as an octave count: each glides to the band's.
	logHz, gain, logQ *anim.Float
	in, on, hot       *anim.Float
	gone              bool
	band              audio.Band
	// down is how far a dynamic band turns itself down as it plays, in
	// decibels, below zero, and heard the level it hears, each gliding
	// to what the plugin playing says.
	down, heard *anim.Float
}

// value returns the band as it shows now, mid-glide.
func (d *dot) value() audio.Band {
	b := d.band
	b.Freq = float32(math.Exp2(float64(d.logHz.Value())))
	b.Gain = d.gain.Value()
	b.Q = float32(math.Exp2(float64(d.logQ.Value())))
	b.On = true
	return b
}

// playing returns the band as it plays now: its gain scaled by amount,
// and turned down as far as it turns itself down, and its Q scaled by
// qScale.
func (d *dot) playing(amount, qScale float32) audio.Band {
	b := d.value()
	if hasGain(b.Kind) {
		b.Gain = b.Gain*amount + d.down.Value()
	}
	return b.Scaled(float64(qScale))
}

// weight is how much of the band shows: grown in, and on.
func (d *dot) weight() float32 { return min(max(d.in.Value(), 0), 1) * min(max(d.on.Value(), 0), 1) }

// graph is the EQ's bands on a graph: frequencies from eqLow to eqHigh
// across, on a scale of octaves, and gains of eqRange decibels each
// way. It draws the curve the bands make together and a numbered point
// for each band. A point drags, its frequency across and its gain up
// and down, finer with Shift. With Alt, a drag up narrows it and down
// widens it, or steepens and eases a cut. The wheel passes the graph
// by, to scroll the pane it is in. A double-click on a point turns its band off and
// on, and one elsewhere adds a band. A right-click on a point opens its
// menu: its kind, its slope, on or off, and away. Points glide to their
// bands, grow in as they come and shrink as they go.
//
// It takes the keyboard by Tab. The keys 1 to 8 pick a band. Left and
// Right move the band picked down and up by a semitone, and Up and Down
// its gain by half a decibel, finer with Shift; Page Up and Page Down
// narrow and widen it. Space turns it off and on, Delete takes it away,
// and the Menu key or Shift+F10 opens its menu. Insert adds a band, and
// Escape lets the band picked go.
//
// Each change is a gesture on the plugin's state through the host: a
// drag from press to release, and every other change on its own.
type graph struct {
	anim.Group
	p    *plugin
	host plug.Host
	// bands are the bands as the window has them, which the graph edits
	// and hands to the plugin.
	bands  []audio.Band
	bypass bool
	dots   map[int]*dot
	// sel is the ID of the band picked, and hot of the one under the
	// pointer; zero for none.
	sel, hot int
	// dragging is the band being dragged, and grab where on its point
	// it was taken. widening says the drag changes the band's width,
	// as Alt pressed it, from where it was at the press: from; grab is
	// then where the press was.
	dragging int
	grab     geom.Point
	widening bool
	from     audio.Band
	size     geom.Size
	menu     *widget.ContextMenu
	picks    []func(*gunim.UI)
	curve    []float32
	// bypassAmt dims the curve while the EQ is bypassed, ring is the
	// focus ring's strength, and amount the amount, as a ratio.
	bypassAmt, ring, amount *anim.Float
	// qScale is how far every band's Q is scaled, and shift how far the
	// dynamic bands' thresholds are moved, each gliding.
	qScale, shift *anim.Float
	// laid says the graph has been laid out once: before, springs jump.
	laid bool
	spec spectrum
	// onChange, where set, hears that the bands, or the band picked,
	// may have changed.
	onChange func(u *gunim.UI)
	// behind is how far the list of bands is over the graph, from 0 to
	// 1, and lit the band whose row the pointer is on there, which the
	// graph shows filled, as it does the band picked.
	behind float32
	lit    int
	// held is the band whose row has a control held, which stays lit
	// while it is, wherever the pointer goes.
	held int
	// tipBand is the band the readout beside a point shows, and tipIn how
	// far it is in: it fades and rises in as the pointer comes over a
	// point, and away as it leaves.
	tipBand int
	tipIn   *anim.Float
}

// The spectrum behind the bands: specPoints frequencies across, the
// last specFrames frames heard of each. specTilt lifts it this many
// decibels an octave above 1 kHz, and lowers it below, so music, which
// has less in its higher pitches, reads about level, as analyzers in
// studios show it; specTop and specBottom are the levels it runs
// between, after the tilt.
const (
	specPoints          = 240
	specFrames          = 4096
	specTilt            = 4.5
	specTop, specBottom = 0, -96
)

// spectrum is the sound before the EQ and after it, as the graph draws
// it behind the bands: measured as the sound is heard, its levels rising
// at once and falling slower, as a meter's do.
type spectrum struct {
	in, out plug.Sound
	at      int64
	// still is how long the sound has brought nothing new, up to
	// plug.StillAfter, and playing says the sound plays.
	still     time.Duration
	playing   bool
	meter     *audioui.Spectrometer
	frames    []float32
	freqs     []float32
	heard     []float32
	before    []float32
	heardSm   []float32
	beforeSm  []float32
	smoothed  []float32
	smoothedB []float32
}

func newSpectrum(h plug.Host) spectrum {
	s := spectrum{meter: audioui.NewSpectrometer(specFrames), frames: make([]float32, 2*specFrames), still: plug.StillAfter}
	s.in, s.out = h.Sound()
	s.freqs = make([]float32, specPoints)
	for i := range s.freqs {
		s.freqs[i] = float32(eqLow * math.Pow(eqHigh/eqLow, float64(i)/(specPoints-1)))
	}
	for _, p := range []*[]float32{&s.heard, &s.before, &s.heardSm, &s.beforeSm, &s.smoothed, &s.smoothedB} {
		*p = make([]float32, specPoints)
		for i := range *p {
			(*p)[i] = specBottom - 20
		}
	}
	return s
}

// measure measures what was heard last, before the EQ and after,
// where anything new was, and reports whether it did and whether the
// sound plays: as it does through the frames of the window between the
// chunks it comes in, until nothing new comes for plug.StillAfter.
func (s *spectrum) measure(dt time.Duration) (measured, playing bool) {
	if s.in == nil || s.out == nil {
		return false, false
	}
	n, at, rate := s.out.Heard(s.frames)
	if at == 0 || at == s.at {
		s.still = min(s.still+dt, plug.StillAfter)
		return false, at != 0 && s.still < plug.StillAfter
	}
	s.at, s.still = at, 0
	if n < specFrames || !s.meter.Measure(s.frames[:2*n], rate, s.freqs, s.heard) {
		return false, true
	}
	if n, _, _ := s.in.Heard(s.frames); n == specFrames {
		s.meter.Measure(s.frames, rate, s.freqs, s.before)
	}
	return true, true
}

// step moves the levels drawn a frame's way toward those measured,
// toward silence while nothing plays, and reports whether they still
// move.
func (s *spectrum) step(dt time.Duration) bool {
	// Between chunks, the levels ease on toward those last measured.
	measured, playing := s.measure(dt)
	s.playing = playing
	if !playing {
		for i := range s.heard {
			s.heard[i], s.before[i] = specBottom-20, specBottom-20
		}
	}
	sec := float32(dt.Seconds())
	settled := true
	ease := func(v *float32, to float32) {
		rate := float32(30)
		if to < *v {
			rate = 6
		}
		*v += (to - *v) * min(1, rate*sec)
		if math.Abs(float64(to-*v)) > 0.3 {
			settled = false
		}
	}
	for i, f := range s.freqs {
		tilt := float32(specTilt * math.Log2(float64(f)/1000))
		ease(&s.heardSm[i], s.heard[i]+tilt)
		ease(&s.beforeSm[i], s.before[i]+tilt)
	}
	return measured || playing || !settled
}

func newGraph(p *plugin, h plug.Host) *graph {
	g := &graph{p: p, host: h, dots: map[int]*dot{}, bypassAmt: anim.NewFloat(0), ring: anim.NewFloat(0),
		amount: anim.NewFloat(float32(h.Param(AmountID) / 100)), qScale: anim.NewFloat(float32(h.Param(QID) / 100)),
		shift: anim.NewFloat(float32(h.Param(ThresholdID))), spec: newSpectrum(h), tipIn: anim.NewFloat(0)}
	g.Add(g.bypassAmt, g.ring, g.amount, g.qScale, g.shift, g.tipIn)
	g.bands = p.Bands()
	g.bypass = h.Param(BypassID) > 0.5
	if g.bypass {
		g.bypassAmt.Jump(1)
	}
	g.glide(false, nil)
	g.menu = widget.NewContextMenu(g, nil)
	g.menu.Prepare = g.prepare
	g.menu.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		if i >= 0 && i < len(g.picks) && g.picks[i] != nil {
			g.picks[i](u)
		}
		return nil
	}
	return g
}

// The graph's scales: x from frequency and back, and y from gain and
// back.
func (g *graph) xOf(hz float64) float32 {
	return float32(math.Log(hz/eqLow) / math.Log(eqHigh/eqLow) * float64(g.size.W))
}

func (g *graph) hzAt(x float32) float64 {
	return eqLow * math.Pow(eqHigh/eqLow, float64(max(0, min(x/max(g.size.W, 1), 1))))
}

// gainH is how far from the middle a gain of eqRange reaches.
func (g *graph) gainH() float32 { return max(g.size.H/2-14, 1) }

func (g *graph) yOf(db float32) float32 { return g.size.H/2 - db/eqRange*g.gainH() }

func (g *graph) dbAt(y float32) float32 { return (g.size.H/2 - y) / g.gainH() * eqRange }

// dotAt returns where dot d is drawn: at its gain, gliding, which for a
// cut or a notch is 0 dB.
func (g *graph) dotAt(d *dot) geom.Point {
	b := d.value()
	return geom.Pt(g.xOf(float64(b.Freq)), g.yOf(b.Gain))
}

// hit returns the band whose point is drawn at p, nearest first, or
// zero.
func (g *graph) hit(p geom.Point, th *theme.Live) int {
	reach := DotReach.Get(th)
	best, bestD := 0, reach*reach
	for _, b := range g.bands {
		q := g.dotAt(g.dots[b.ID]).Sub(p)
		if dd := q.X*q.X + q.Y*q.Y; dd < bestD {
			best, bestD = b.ID, dd
		}
	}
	return best
}

// band returns the band with ID id, and its place, or nil and -1.
func (g *graph) band(id int) (b *audio.Band, place int) {
	for i := range g.bands {
		if g.bands[i].ID == id {
			return &g.bands[i], i
		}
	}
	return nil, -1
}

// number returns band id's number, as its point shows it.
func (g *graph) number(id int) int {
	_, i := g.band(id)
	return i + 1
}

// spring moves a toward v on s, or puts it there before the first
// layout.
func (g *graph) spring(a *anim.Float, v float32, s theme.Token[anim.Spring], u *gunim.UI) {
	if !g.laid {
		a.Jump(v)
		return
	}
	a.Animate(v, s.Get(u.Theme()))
}

// dot returns band b's dot, making it, grown in, where it is new.
func (g *graph) dot(b audio.Band, u *gunim.UI) *dot {
	d := g.dots[b.ID]
	if d == nil {
		d = &dot{
			logHz: anim.NewFloat(float32(math.Log2(float64(b.Freq)))), gain: anim.NewFloat(b.Gain),
			logQ: anim.NewFloat(float32(math.Log2(float64(max(b.Q, 0.025))))),
			in:   anim.NewFloat(1), on: anim.NewFloat(1), hot: anim.NewFloat(0), down: anim.NewFloat(0),
			heard: anim.NewFloat(specBottom - 20),
		}
		if !b.On {
			d.on.Jump(0)
		}
		if g.laid {
			d.in.Jump(0)
			d.in.Animate(1, widget.Bounce.Get(u.Theme()))
		}
		g.dots[b.ID] = d
		g.Add(d.logHz, d.gain, d.logQ, d.in, d.on, d.hot, d.down, d.heard)
	}
	if d.gone {
		// A band back again, as an undo brings it, before its point
		// has shrunk away.
		g.spring(d.in, 1, widget.Bounce, u)
	}
	d.band, d.gone = b, false
	return d
}

// glide glides each dot to its band, at once where springy is false,
// as while dragged, and lets the dots of bands gone shrink away.
func (g *graph) glide(springy bool, u *gunim.UI) {
	live := map[int]bool{}
	for _, b := range g.bands {
		live[b.ID] = true
		d := g.dot(b, u)
		to := []float32{float32(math.Log2(float64(b.Freq))), b.Gain, float32(math.Log2(float64(max(b.Q, 0.025))))}
		for i, a := range []*anim.Float{d.logHz, d.gain, d.logQ} {
			if springy {
				g.spring(a, to[i], widget.Settle, u)
			} else {
				a.Jump(to[i])
			}
		}
		on := float32(0)
		if b.On {
			on = 1
		}
		g.spring(d.on, on, widget.Quick, u)
	}
	for id, d := range g.dots {
		if !live[id] && !d.gone {
			d.gone = true
			g.spring(d.in, 0, widget.Settle, u)
		}
	}
	if b, _ := g.band(g.sel); b == nil {
		g.sel = 0
	}
	g.told(u)
}

// told tells the views round the graph that the bands, or the band
// picked, may have changed.
func (g *graph) told(u *gunim.UI) {
	if g.onChange != nil {
		g.onChange(u)
	}
}

// FollowState implements [plug.StateFollower]: the graph reads the
// plugin's bands again, and the points glide to them. A band being
// dragged keeps to the pointer, and the bands the drag makes win.
func (g *graph) FollowState(u *gunim.UI) {
	if g.dragging != 0 {
		return
	}
	g.bands = g.p.Bands()
	g.glide(true, u)
	u.Invalidate()
}

// Follow implements [plug.Follower]: the curve dims while the EQ is
// bypassed, and scales with the amount and the Q; the thresholds move.
func (g *graph) Follow(id uint32, v float64, u *gunim.UI) {
	switch id {
	case AmountID:
		g.spring(g.amount, float32(v/100), widget.Settle, u)
		u.Invalidate()
		return
	case QID:
		g.spring(g.qScale, float32(v/100), widget.Settle, u)
		u.Invalidate()
		return
	case ThresholdID:
		g.spring(g.shift, float32(v), widget.Settle, u)
		u.Invalidate()
		return
	}
	if id != BypassID {
		return
	}
	g.bypass = v > 0.5
	to := float32(0)
	if g.bypass {
		to = 1
	}
	g.spring(g.bypassAmt, to, widget.Settle, u)
	u.Invalidate()
}

// commit hands the bands to the plugin and tells the host they
// changed, within a gesture, and glides the dots to them.
func (g *graph) commit(springy bool, u *gunim.UI) {
	g.p.setBands(g.bands)
	g.host.Changed(u)
	g.glide(springy, u)
	u.Invalidate()
}

// edit is one gesture named name: change changes the bands, and
// reports whether it did.
func (g *graph) edit(name string, springy bool, u *gunim.UI, change func() bool) {
	g.host.BeginEdit(name, u)
	if change() {
		g.commit(springy, u)
	}
	g.host.EndEdit(u)
}

// add puts a band at p: a bell, or a cut near either end.
func (g *graph) add(p geom.Point, u *gunim.UI) {
	if len(g.bands) >= maxBands {
		u.Cue(gunim.CueError, g)
		return
	}
	hz := clampHz(g.hzAt(p.X))
	b := audio.Band{Kind: audio.Bell, Freq: hz, Gain: clampDB(g.dbAt(p.Y)), Q: 1, On: true}
	switch {
	case hz < 40:
		b.Kind, b.Gain, b.Q, b.Slope = audio.LowCut, 0, 0.707, 24
	case hz > 15000:
		b.Kind, b.Gain, b.Q, b.Slope = audio.HighCut, 0, 0.707, 24
	}
	g.addBand(b, u)
}

// addBand adds b, numbered after the others, and picks it.
func (g *graph) addBand(b audio.Band, u *gunim.UI) {
	if len(g.bands) >= maxBands {
		u.Cue(gunim.CueError, g)
		return
	}
	b.ID = 0
	for _, o := range g.bands {
		b.ID = max(b.ID, o.ID)
	}
	b.ID++
	u.Cue(gunim.CueToggleOn, g)
	g.edit("Add band", false, u, func() bool {
		g.bands = append(g.bands, b)
		g.sel = b.ID
		return true
	})
}

// freeFreq is a frequency for a band added without a place: the middle,
// in octaves, of the widest stretch the bands leave free between 40 Hz
// and 12 kHz.
func (g *graph) freeFreq() float32 {
	at := make([]float64, 0, 2+len(g.bands))
	at = append(at, math.Log2(40), math.Log2(12000))
	for _, b := range g.bands {
		at = append(at, math.Log2(float64(b.Freq)))
	}
	slices.Sort(at)
	best, wide := 1000.0, 0.0
	for i := 1; i < len(at); i++ {
		if w := at[i] - at[i-1]; w > wide && at[i] <= math.Log2(12000) {
			best, wide = math.Exp2((at[i]+at[i-1])/2), w
		}
	}
	return float32(math.Round(best))
}

// remove takes band id away.
func (g *graph) remove(id int, u *gunim.UI) {
	_, i := g.band(id)
	if i < 0 {
		return
	}
	u.Cue(gunim.CueToggleOff, g)
	g.edit(fmt.Sprintf("Remove band %d", i+1), false, u, func() bool {
		g.bands = slices.Delete(g.bands, i, i+1)
		return true
	})
}

// toggle turns band id off, or on again.
func (g *graph) toggle(id int, u *gunim.UI) {
	b, i := g.band(id)
	if b == nil {
		return
	}
	name, cue := "Turn band %d off", gunim.CueToggleOff
	if !b.On {
		name, cue = "Turn band %d on", gunim.CueToggleOn
	}
	u.Cue(cue, g)
	g.edit(fmt.Sprintf(name, i+1), false, u, func() bool {
		b.On = !b.On
		return true
	})
}

// toggleDynamic makes band id dynamic, as its dynamics were or as they
// start, or static again.
func (g *graph) toggleDynamic(id int, u *gunim.UI) {
	b, i := g.band(id)
	if b == nil || !hasGain(b.Kind) {
		return
	}
	name, cue := "Band %d dynamic", gunim.CueToggleOn
	if b.Dynamic.On {
		name, cue = "Band %d static", gunim.CueToggleOff
	}
	u.Cue(cue, g)
	g.edit(fmt.Sprintf(name, i+1), false, u, func() bool {
		switch {
		case b.Dynamic.On:
			b.Dynamic.On = false
		case b.Dynamic == (audio.Dynamics{}):
			b.Dynamic = dynDefaults
		default:
			b.Dynamic.On = true
		}
		return true
	})
}

// setKind turns band id into a band of kind, keeping what it can.
func (g *graph) setKind(id int, kind audio.FilterKind, u *gunim.UI) {
	b, i := g.band(id)
	if b == nil || b.Kind == kind {
		return
	}
	g.edit(fmt.Sprintf("Band %d to %s", i+1, strings.ToLower(kindNames[kind])), true, u, func() bool {
		b.Kind = kind
		switch {
		case isCut(kind):
			b.Gain, b.Q, b.Slope = 0, 0.707, max(b.Slope, 24)
		case kind == audio.Notch:
			b.Gain, b.Q = 0, max(b.Q, 4)
		case kind == audio.LowShelf || kind == audio.HighShelf:
			b.Q = 0.707
		}
		return true
	})
}

// setSlope sets cut id's slope, in decibels an octave.
func (g *graph) setSlope(id, slope int, u *gunim.UI) {
	b, i := g.band(id)
	if b == nil || max(b.Slope, 12) == slope {
		return
	}
	g.edit(fmt.Sprintf("Band %d to %d dB an octave", i+1, slope), false, u, func() bool {
		b.Slope = slope
		return true
	})
}

// widthName names a change to band b's width, or a cut's slope, at
// place i.
func widthName(b *audio.Band, i int) string {
	if isCut(b.Kind) {
		return fmt.Sprintf("Change the slope of band %d", i+1)
	}
	return fmt.Sprintf("Change the width of band %d", i+1)
}

// widen narrows band b by notches, or for a cut steepens it by a step
// for each whole notch.
func widen(b *audio.Band, notches float32) {
	if isCut(b.Kind) {
		step := 12 * int(math.Round(float64(notches)))
		b.Slope = max(12, min(max(b.Slope, 12)+step, 48))
	} else {
		b.Q = clampQ(float64(b.Q) * math.Pow(1.15, float64(notches)))
	}
}

// widenPx is how far up a drag with Alt goes for a notch of widen.
const widenPx = 24

// nudge moves band id by key: its frequency by octaves, its
// gain by decibels and its width by notches. It is one gesture.
func (g *graph) nudge(id int, octaves float64, db, notches float32, u *gunim.UI) {
	b, i := g.band(id)
	if b == nil {
		return
	}
	name := fmt.Sprintf("Move band %d", i+1)
	if notches != 0 {
		name = widthName(b, i)
	}
	u.Cue(gunim.CueTick, g)
	g.edit(name, true, u, func() bool {
		was := *b
		b.Freq = clampHz(float64(b.Freq) * math.Exp2(octaves))
		if hasGain(b.Kind) {
			b.Gain = clampDB(b.Gain + db)
		}
		if notches != 0 {
			widen(b, notches)
		}
		return *b != was
	})
}

// DragsTouch implements [gunim.TouchDragger]: a finger on a point drags
// it.
func (g *graph) DragsTouch() bool { return g.dragging != 0 }

// Focusable implements [gunim.Focusable].
func (g *graph) Focusable() bool { return true }

// Handle implements [gunim.Handler].
func (g *graph) Handle(e input.Event, u *gunim.UI) bool {
	// What the pointer or the keys did may have picked another band.
	defer g.told(u)
	th := u.Theme()
	switch e := e.(type) {
	case input.PointerMove:
		if g.dragging != 0 {
			g.drag(e.Pos, e.Mods, u)
			break
		}
		g.hover(g.hit(e.Pos, th), u)
	case input.PointerLeave:
		if g.dragging == 0 {
			g.hover(0, u)
		}
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		id := g.hit(e.Pos, th)
		switch {
		case id != 0 && e.Clicks == 2:
			g.toggle(id, u)
		case id != 0:
			g.press(id, e.Pos, e.Mods.Has(input.ModAlt), u)
		case e.Clicks == 2:
			g.add(e.Pos, u)
		default:
			g.sel = 0
		}
	case input.PointerUp:
		g.release(u)
	case input.KeyPress:
		if !g.key(e, u) {
			return false
		}
	case input.FocusRing:
		to := float32(0)
		if e.On {
			to = 1
		}
		g.ring.Animate(to, widget.RingFade.Get(th))
	case input.FocusLost:
		g.ring.Animate(0, widget.RingFade.Get(th))
		g.release(u)
	default:
		return false
	}
	u.Invalidate()
	return true
}

// press takes band id's point at pos, to drag, and begins the drag's
// gesture. widening says the drag changes the band's width rather than
// moving it.
func (g *graph) press(id int, pos geom.Point, widening bool, u *gunim.UI) {
	g.release(u)
	g.sel, g.dragging, g.widening = id, id, widening
	g.grab = pos.Sub(g.dotAt(g.dots[id]))
	name := fmt.Sprintf("Move band %d", g.number(id))
	if b, i := g.band(id); widening && b != nil {
		g.grab, g.from = pos, *b
		name = widthName(b, i)
	}
	u.Cue(gunim.CueSelect, g)
	g.host.BeginEdit(name, u)
}

// release lets go of the point dragged, which ends the drag's gesture.
func (g *graph) release(u *gunim.UI) {
	if g.dragging == 0 {
		return
	}
	g.dragging = 0
	g.host.EndEdit(u)
}

// drag moves the band being dragged to p: its frequency across, and
// its gain up and down. Shift moves it finely. Where the drag is
// widening, it narrows the band as p goes up from the press instead.
func (g *graph) drag(p geom.Point, mods input.Mods, u *gunim.UI) {
	b, _ := g.band(g.dragging)
	if b == nil {
		g.release(u)
		return
	}
	if g.widening {
		up := g.grab.Y - p.Y
		if mods.Has(input.ModShift) {
			up *= 0.1
		}
		was := *b
		b.Q, b.Slope = g.from.Q, g.from.Slope
		widen(b, up/widenPx)
		if *b != was {
			g.commit(false, u)
		}
		return
	}
	at := p.Sub(g.grab)
	if mods.Has(input.ModShift) {
		// A tenth as far: from where the point is, toward the pointer.
		now := g.dotAt(g.dots[b.ID])
		at = now.Add(at.Sub(now).Mul(0.1))
	}
	was := *b
	b.Freq = clampHz(g.hzAt(at.X))
	if hasGain(b.Kind) {
		b.Gain = clampDB(g.dbAt(at.Y))
	}
	if *b != was {
		g.commit(false, u)
	}
}

// key acts on k, and reports whether k was one of the graph's keys.
func (g *graph) key(k input.KeyPress, u *gunim.UI) bool {
	if k.Mods.Has(input.ModControl) || k.Mods.Has(input.ModAlt) {
		return false
	}
	fine := k.Mods.Has(input.ModShift)
	semis, db := 1.0, float32(0.5)
	if fine {
		semis, db = 0.25, 0.1
	}
	if k.Key >= input.Key1 && k.Key <= input.Key8 {
		i := int(k.Key - input.Key1)
		if i >= len(g.bands) {
			return false
		}
		g.sel = g.bands[i].ID
		u.Cue(gunim.CueSelect, g)
		return true
	}
	switch k.Key {
	case input.KeyInsert:
		g.add(geom.Pt(g.xOf(1000), g.yOf(0)), u)
		return true
	case input.KeyEscape:
		if g.sel == 0 {
			return false
		}
		g.sel = 0
		return true
	case input.KeyLeft, input.KeyRight, input.KeyUp, input.KeyDown, input.KeyPageUp, input.KeyPageDown,
		input.KeySpace, input.KeyDelete, input.KeyBackspace, input.KeyMenu, input.KeyF10:
	default:
		return false
	}
	if k.Key == input.KeyF10 && !fine {
		return false
	}
	if g.sel == 0 {
		// The first key picks the first band, to act on with the next.
		if len(g.bands) == 0 || k.Key == input.KeyMenu || k.Key == input.KeyF10 {
			return false
		}
		g.sel = g.bands[0].ID
		u.Cue(gunim.CueSelect, g)
		return true
	}
	switch k.Key {
	case input.KeyLeft:
		g.nudge(g.sel, -semis/12, 0, 0, u)
	case input.KeyRight:
		g.nudge(g.sel, semis/12, 0, 0, u)
	case input.KeyUp:
		g.nudge(g.sel, 0, db, 0, u)
	case input.KeyDown:
		g.nudge(g.sel, 0, -db, 0, u)
	case input.KeyPageUp:
		g.nudge(g.sel, 0, 0, 1, u)
	case input.KeyPageDown:
		g.nudge(g.sel, 0, 0, -1, u)
	case input.KeySpace:
		g.toggle(g.sel, u)
	case input.KeyDelete, input.KeyBackspace:
		g.remove(g.sel, u)
	default:
		// The Menu key, or Shift+F10.
		g.menu.Open(g.dotAt(g.dots[g.sel]), u)
	}
	return true
}

func (g *graph) hover(id int, u *gunim.UI) {
	if id == g.hot {
		return
	}
	if d := g.dots[g.hot]; d != nil {
		d.hot.Animate(0, widget.Settle.Get(u.Theme()))
	}
	g.hot = id
	if d := g.dots[id]; d != nil {
		d.hot.Animate(1, widget.Quick.Get(u.Theme()))
	}
}

// prepare sets the menu for a right-click on a band's point: its kind,
// its slope, on or off, and away.
func (g *graph) prepare(at geom.Point, u *gunim.UI) bool {
	id := g.hit(at, u.Theme())
	b, _ := g.band(id)
	if b == nil {
		return false
	}
	g.sel = id
	var items []widget.MenuItem
	g.picks = g.picks[:0]
	item := func(it widget.MenuItem, do func(*gunim.UI)) {
		items = append(items, it)
		g.picks = append(g.picks, do)
	}
	for k, name := range kindNames {
		kind := audio.FilterKind(k)
		item(widget.MenuItem{Label: name, Checked: b.Kind == kind}, func(u *gunim.UI) { g.setKind(id, kind, u) })
	}
	if isCut(b.Kind) {
		for i, s := range []int{12, 24, 36, 48} {
			item(widget.MenuItem{Label: fmt.Sprintf("%d dB an octave", s), Checked: max(b.Slope, 12) == s, Break: i == 0},
				func(u *gunim.UI) { g.setSlope(id, s, u) })
		}
	}
	onText := "Turn off"
	if !b.On {
		onText = "Turn on"
	}
	item(widget.MenuItem{Label: onText, Icon: icon.Power, Break: true}, func(u *gunim.UI) { g.toggle(id, u) })
	item(widget.MenuItem{Label: "Delete band", Icon: icon.Trash2}, func(u *gunim.UI) { g.remove(id, u) })
	g.menu.SetItems(items)
	return true
}

// Step implements [gunim.Animator]: the dots glide, and those of bands
// gone are dropped once they have shrunk away.
func (g *graph) Step(dt time.Duration) bool {
	moving := g.Group.Step(dt)
	if g.spec.step(dt) {
		moving = true
	}
	g.followDown()
	g.followTip()
	for id, d := range g.dots {
		if d.gone && d.in.Value() < 0.01 && !d.in.Active() {
			g.Remove(d.logHz, d.gain, d.logQ, d.in, d.on, d.hot, d.down, d.heard)
			delete(g.dots, id)
		}
	}
	return moving
}

// followTip has the readout follow the band dragged, or under the
// pointer: in as there is one, away as there is none, keeping the band
// it showed as it fades.
func (g *graph) followTip() {
	id := g.dragging
	if id == 0 {
		id = g.hot
	}
	to := float32(0)
	if b, _ := g.band(id); b != nil {
		g.tipBand, to = id, 1
	}
	if g.tipIn.Target() != to {
		sp := anim.Spring{Response: 0.18, Damping: 0.9}
		if to == 0 {
			sp = anim.Spring{Response: 0.25, Damping: 1}
		}
		g.tipIn.Animate(to, sp)
	}
}

// followDown glides each dynamic band's dot to how far the band turns
// itself down at the frame heard, as the plugin playing kept it, and
// back up once the sound stops; and its threshold's level to the level
// it hears, down to silence once the sound stops.
func (g *graph) followDown() {
	for id, d := range g.dots {
		to, heard := float32(0), float32(specBottom-20)
		if g.spec.playing && d.band.Dynamic.On {
			if v, ok := g.host.Traced(bandTrace+uint32(id), g.spec.at-1); ok {
				to = v
			}
			if v, ok := g.host.Traced(heardTrace+uint32(id), g.spec.at-1); ok {
				heard = -v
			}
		}
		if d.down.Target() != to {
			d.down.Animate(to, anim.Snappy)
		}
		if d.heard.Target() != heard {
			d.heard.Animate(heard, anim.Snappy)
		}
	}
}

// Access implements [gunim.Accessible]: a group, named EQ, whose value
// is the band picked.
func (g *graph) Access() access.Info {
	v := fmt.Sprintf("%d bands", len(g.bands))
	if b, i := g.band(g.sel); b != nil {
		v = fmt.Sprintf("Band %d, %s, %s", i+1, kindNames[b.Kind], bandText(*b))
		if !b.On {
			v += ", off"
		}
	}
	return access.Info{Role: access.RoleGroup, Name: "EQ", Value: v}
}

// Layout implements [gunim.Node].
func (g *graph) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	g.size = c.Max
	g.laid = true
	return c.Max
}

// Paint implements [gunim.Node].
func (g *graph) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	whole := geom.Rect{Max: box.Point()}
	radius := widget.CardRadius.Get(th)
	p.RRect(whole, radius, paint.Solid(audioui.Ground.Get(th)))
	end := p.Layer(paint.LayerOpts{Bounds: whole, Opacity: 1, Clip: true, Radius: radius})
	g.paintGrid(p, th, box)
	g.paintSpectrum(p, th, box)
	// Behind the list: darkened, the curve and the points over the dark,
	// as bright as the look says.
	plugui.Darken(p, whole, g.behind)
	curve := p.Layer(paint.LayerOpts{Bounds: whole, Opacity: plugui.Lines(g.behind)})
	bypass := g.bypassAmt.Value()
	g.paintCurve(p, th, box, bypass)
	g.paintThresholds(p, th, bypass)
	g.paintDots(p, th, bypass)
	curve()
	if g.behind < 0.5 {
		g.paintReadout(p, th, box)
	}
	if len(g.bands) == 0 {
		size := ReadoutSize.Get(th)
		run := audioui.Shaped("Double-click to add a band", size, false, false)
		run.Paint(p, geom.Pt((box.W-run.Advance)/2, box.H/2-2*size), audioui.Faded(widget.Ink.Get(th), 0.55))
	}
	end()
	widget.GroupRing(p, whole, radius, g.ring.Value(), th)
}

// specY is where on the graph a level of the spectrum is.
func (g *graph) specY(db float32) float32 {
	t := (db - specBottom) / (specTop - specBottom)
	return g.size.H * (1 - max(0, min(t, 1)))
}

// paintSpectrum draws the sound after the EQ, filled, and before it as
// a pale line over the fill: where the EQ lifts, the fill rises above
// the line, and where it cuts, the line runs above the fill.
func (g *graph) paintSpectrum(p *paint.Painter, th *theme.Live, box geom.Size) {
	s := &g.spec
	heard, before := smooth(s.heardSm, s.smoothed), smooth(s.beforeSm, s.smoothedB)
	accent, ink := widget.Accent.Get(th), widget.Ink.Get(th)
	step := math.Pow(eqHigh/eqLow, 0.5/(specPoints-1))
	for i, f := range s.freqs {
		// Whole pixels, each column starting where the last ended, so
		// none overlap and the fill is even.
		x0 := float32(math.Round(float64(g.xOf(float64(f) / step))))
		x1 := float32(math.Round(float64(g.xOf(float64(f) * step))))
		if y := g.specY(heard[i]); x1 > x0 && y < box.H {
			p.RRect(geom.Rc(x0, y, x1-x0, box.H-y), 0, paint.Solid(audioui.Faded(accent, 0.18)))
		}
	}
	var prevHeard, prevBefore geom.Point
	for i, f := range s.freqs {
		x := g.xOf(float64(f))
		h, b := geom.Pt(x, g.specY(heard[i])), geom.Pt(x, g.specY(before[i]))
		if i > 0 {
			audioui.Segment(p, prevHeard, h, 1.5, audioui.Faded(accent, 0.6))
			audioui.Segment(p, prevBefore, b, 1.2, audioui.Faded(ink, 0.4))
		}
		prevHeard, prevBefore = h, b
	}
}

// smooth fills out with db evened across its neighbours, as a studio's
// analyzer evens a spectrum across a fraction of an octave, and returns
// it.
func smooth(db, out []float32) []float32 {
	for i := range db {
		sum, n := float32(0), float32(0)
		for j := max(i-2, 0); j <= min(i+2, len(db)-1); j++ {
			w := float32(3 - max(i-j, j-i))
			sum += db[j] * w
			n += w
		}
		out[i] = sum / n
	}
	return out
}

// paintGrid draws the lines of the octaves' landmarks and of every 6
// dB, labelled.
func (g *graph) paintGrid(p *paint.Painter, th *theme.Live, box geom.Size) {
	ink, size := widget.Ink.Get(th), LabelSize.Get(th)
	for _, hz := range []float64{50, 100, 200, 500, 1000, 2000, 5000, 10000} {
		x := g.xOf(hz)
		p.RRect(geom.Rc(x, 0, 1, box.H), 0, paint.Solid(audioui.Faded(ink, 0.06)))
		label := fmt.Sprintf("%.0f", hz)
		if hz >= 1000 {
			label = fmt.Sprintf("%.0fk", hz/1000)
		}
		audioui.Shaped(label, size, false, false).Paint(p, geom.Pt(x+4, box.H-1.6*size), audioui.Faded(ink, 0.35))
	}
	for db := -eqRange + 6; db < eqRange; db += 6 {
		y := g.yOf(float32(db))
		a := float32(0.05)
		label := fmt.Sprintf("%+d", db)
		if db == 0 {
			a, label = 0.14, "0"
		}
		p.RRect(geom.Rc(0, y, box.W, 1), 0, paint.Solid(audioui.Faded(ink, a)))
		run := audioui.Shaped(label, size, false, false)
		run.Paint(p, geom.Pt(box.W-run.Advance-6, y-1.3*size), audioui.Faded(ink, 0.35))
	}
}

// paintCurve draws the curve the bands make together, the band picked
// or under the pointer filled in its colour, dimmed while bypassed.
func (g *graph) paintCurve(p *paint.Painter, th *theme.Live, box geom.Size, bypass float32) {
	n := max(int(box.W/3), 2)
	if len(g.curve) != n+1 {
		g.curve = make([]float32, n+1)
	}
	focus := g.lit
	if focus == 0 {
		focus = g.hot
	}
	if focus == 0 {
		focus = g.sel
	}
	var own []float32
	focused := g.dots[focus]
	if focused != nil && focused.weight() > 0.001 {
		own = make([]float32, n+1)
	}
	for i := range n + 1 {
		hz := g.hzAt(box.W * float32(i) / float32(n))
		var db float32
		for _, d := range g.dots {
			w := d.weight()
			if w <= 0.001 {
				continue
			}
			r := float32(d.playing(g.amount.Value(), g.qScale.Value()).Response(hz)) * w
			db += r
			if d == focused && own != nil {
				own[i] = r
			}
		}
		g.curve[i] = db
	}
	if own != nil {
		// The band picked, filled from the line of 0 dB.
		c := bandColor(th, g.place(focus))
		zero := g.yOf(0)
		w := box.W / float32(n)
		for i, r := range own {
			y := g.yOf(max(-eqRange-6, min(r, eqRange+6)))
			top, h := min(y, zero), float32(math.Abs(float64(y-zero)))
			p.RRect(geom.Rc(box.W*float32(i)/float32(n)-w/2, top, w+0.5, h), 0, paint.Solid(audioui.Faded(c, 0.16*(1-bypass))))
		}
	}
	line := audioui.Mix(widget.Ink.Get(th), widget.Accent.Get(th), 0.35)
	alpha := 1 - 0.65*bypass
	width := CurveWidth.Get(th)
	var prev geom.Point
	for i, db := range g.curve {
		// Bypassed, the curve flattens as it dims.
		db *= 1 - bypass
		pt := geom.Pt(box.W*float32(i)/float32(n), g.yOf(max(-eqRange-8, min(db, eqRange+8))))
		if i > 0 {
			audioui.Segment(p, prev, pt, 3*width, audioui.Faded(line, 0.12*alpha))
			audioui.Segment(p, prev, pt, width, audioui.Faded(line, alpha))
		}
		prev = pt
	}
}

// place returns where band id is among the bands, for its colour and
// its number.
func (g *graph) place(id int) int {
	if _, i := g.band(id); i >= 0 {
		return i
	}
	return 0
}

// paintDots draws each band's point: numbered, in its colour, hollow
// while off, larger under the pointer, ringed while picked.
// paintThresholds draws each dynamic band's threshold, moved by the
// shift, on the spectrum's scale, across the pitches the band hears: a
// bell's about its frequency, as wide as its Q, and a shelf's from its
// frequency out to the edge; and, as the sound plays, the level the
// band hears there, a fainter line, lit up to the threshold where it is
// over, which is where the band turns down. The band picked, or under
// the pointer, stands out, its threshold written by it; the others are
// fainter, and unwritten, so many do not crowd.
func (g *graph) paintThresholds(p *paint.Painter, th *theme.Live, bypass float32) {
	ground, ink := audioui.Ground.Get(th), widget.Ink.Get(th)
	for i, b := range g.bands {
		d := g.dots[b.ID]
		if d == nil || !b.Dynamic.On || !hasGain(b.Kind) {
			continue
		}
		w := d.weight()
		if w < 0.01 {
			continue
		}
		focus := g.focusOf(b.ID)
		strength := w * (0.45 + 0.55*focus)
		c := audioui.Mix(bandColor(th, i), audioui.Mix(ground, ink, 0.5), 0.7*bypass)
		from, to := g.heardSpan(d.playing(g.amount.Value(), g.qScale.Value()))
		db := b.Dynamic.Threshold + g.shift.Value()
		y := g.specY(db)
		x0, x1 := g.xOf(from), g.xOf(to)
		if heard := d.heard.Value(); heard > specBottom {
			hy := g.specY(heard)
			if hy < y {
				p.RRect(geom.Rc(x0, hy, x1-x0, y-hy), 0, paint.Solid(audioui.Faded(c, 0.22*strength)))
			}
			p.RRect(geom.Rc(x0, hy-0.5, x1-x0, 1), 0.5, paint.Solid(audioui.Faded(c, 0.45*strength)))
		}
		p.RRect(geom.Rc(x0, y-0.75, x1-x0, 1.5), 0.75, paint.Solid(audioui.Faded(c, 0.8*strength)))
		for _, x := range []float32{x0, x1} {
			if x > 1 && x < g.size.W-1 {
				p.RRect(geom.Rc(x-0.75, y-4, 1.5, 8), 0.75, paint.Solid(audioui.Faded(c, 0.8*strength)))
			}
		}
		if focus > 0.01 {
			label := audioui.Shaped(fmt.Sprintf("threshold %.0f dB", db), 9, false, true)
			lx := min(max((x0+x1-label.Advance)/2, x0), g.size.W-label.Advance-4)
			label.Paint(p, geom.Pt(lx, y-13), audioui.Faded(c, 0.9*w*focus))
		}
	}
}

// focusOf is how far band id stands out among the thresholds: fully
// picked, and as far as the pointer is on its point otherwise.
func (g *graph) focusOf(id int) float32 {
	if id == g.sel {
		return 1
	}
	if d := g.dots[id]; d != nil {
		return min(max(d.hot.Value(), 0), 1)
	}
	return 0
}

// heardSpan returns the pitches band b hears, in hertz: a shelf's from
// its frequency out to the graph's edge, and about another's frequency
// as wide as its Q, half the band on each side.
func (g *graph) heardSpan(b audio.Band) (from, to float64) {
	hz := float64(b.Freq)
	switch b.Kind {
	case audio.LowShelf:
		return eqLow, hz
	case audio.HighShelf:
		return hz, eqHigh
	case audio.Bell, audio.Notch, audio.LowCut, audio.HighCut:
	}
	// A bandwidth in octaves from the Q.
	half := math.Asinh(1/(2*float64(max(b.Q, 0.05)))) / math.Ln2
	return max(hz*math.Exp2(-half), eqLow), min(hz*math.Exp2(half), eqHigh)
}

func (g *graph) paintDots(p *paint.Painter, th *theme.Live, bypass float32) {
	for i, b := range g.bands {
		if d := g.dots[b.ID]; d != nil {
			g.paintDot(p, th, d, i, b.ID == g.sel, bypass)
		}
	}
	// Points of bands gone, shrinking away.
	for _, d := range g.dots {
		if d.gone {
			g.paintDot(p, th, d, 0, false, bypass)
		}
	}
}

func (g *graph) paintDot(p *paint.Painter, th *theme.Live, d *dot, i int, picked bool, bypass float32) {
	grow := max(d.in.Value(), 0)
	if grow < 0.01 {
		return
	}
	ground, ink := audioui.Ground.Get(th), widget.Ink.Get(th)
	c := audioui.Mix(bandColor(th, i), audioui.Mix(ground, ink, 0.5), 0.7*bypass)
	at := g.dotAt(d)
	base := DotRadius.Get(th)
	r := (base + base/3*d.hot.Value()) * grow
	if down := d.down.Value(); down < -0.1 && grow > 0.5 {
		// A dynamic band turning itself down: a line from its point to
		// where it plays now, and a mark there.
		y := g.yOf(d.gain.Value()*g.amount.Value() + down)
		audioui.Segment(p, geom.Pt(at.X, at.Y+r), geom.Pt(at.X, y), 1.5, audioui.Faded(c, 0.7))
		p.RRect(geom.Rc(at.X-4, y-1, 8, 2), 1, paint.Solid(c))
	}
	if picked {
		ring := geom.Rc(at.X-r-5, at.Y-r-5, 2*r+10, 2*r+10)
		p.RRectStroke(ring, r+5, paint.Fill{}, paint.Stroke{Width: 2, Color: audioui.Faded(c, 0.6)})
	}
	on := min(max(d.on.Value(), 0), 1)
	circle := geom.Rc(at.X-r, at.Y-r, 2*r, 2*r)
	p.ShadowRRect(circle, r, paint.Solid(audioui.Mix(ground, c, 0.25+0.75*on)),
		paint.Shadow{Blur: 10 + 8*d.hot.Value(), Color: audioui.Faded(c, 0.5*on)})
	p.RRectStroke(circle, r, paint.Fill{}, paint.Stroke{Width: 1.5, Color: c})
	if grow > 0.6 {
		size := LabelSize.Get(th) + 1
		run := audioui.Shaped(strconv.Itoa(i+1), size, true, false)
		run.Paint(p, geom.Pt(at.X-run.Advance/2, at.Y-0.7*size), audioui.Mix(c, ground, on))
	}
}

// paintReadout draws the settings of the band dragged, or under the
// pointer, beside its point.
func (g *graph) paintReadout(p *paint.Painter, th *theme.Live, box geom.Size) {
	// The band it shows: the one dragged, or under the pointer, or, as it
	// fades away, the one it showed last.
	id := g.tipBand
	b, i := g.band(id)
	tip := min(max(g.tipIn.Value(), 0), 1)
	if b == nil || g.dots[id] == nil || tip < 0.01 {
		return
	}
	defer p.Push(paint.Translate(geom.Pt(0, (1-tip)*8)))()
	end := p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: tip})
	defer end()
	text := bandText(*b)
	if !b.On {
		text += "  ·  off"
	}
	size := ReadoutSize.Get(th)
	run := audioui.Shaped(text, size, true, false)
	at := g.dotAt(g.dots[id])
	w, h := run.Advance+20, 2*size+2
	x := max(4, min(at.X-w/2, box.W-w-4))
	y := at.Y - 46
	if y < 4 {
		y = at.Y + 24
	}
	c := bandColor(th, i)
	p.RRect(geom.Rc(x, y, w, h), h/2, paint.Solid(audioui.Faded(audioui.Ground.Get(th), 0.92)))
	p.RRectStroke(geom.Rc(x, y, w, h), h/2, paint.Fill{}, paint.Stroke{Width: 1, Color: audioui.Faded(c, 0.7)})
	run.Paint(p, geom.Pt(x+10, y+(h-size)/2), widget.Ink.Get(th))
}

// panel is the graph with its points' menu, as a panel shows it.
type panel struct {
	*widget.ContextMenu
	graph *graph
}

func newPanel(p *plugin, h plug.Host) *panel {
	g := newGraph(p, h)
	return &panel{ContextMenu: g.menu, graph: g}
}

// FollowState implements [plug.StateFollower].
func (v *panel) FollowState(u *gunim.UI) { v.graph.FollowState(u) }

// Follow implements [plug.Follower].
func (v *panel) Follow(id uint32, val float64, u *gunim.UI) { v.graph.Follow(id, val, u) }
