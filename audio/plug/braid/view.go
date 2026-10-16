package braid

import (
	"image/color"
	"math"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugui"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The ribbons' colours: the left channel's and the right's, each as its
// front shows; an inverted channel shows its back, darker.
var (
	BraidLeft  = theme.Color("plug.braid.left", color.NRGBA{R: 0x4f, G: 0xc3, B: 0xf7, A: 0xff})
	BraidRight = theme.Color("plug.braid.right", color.NRGBA{R: 0xff, G: 0x8a, B: 0x65, A: 0xff})
)

// back returns the back of a ribbon of colour c: darker.
func back(c color.NRGBA) color.NRGBA {
	return audioui.Mix(c, color.NRGBA{A: 0xff}, 0.35)
}

// meter is a pair of levels, left and right, of a sound as it is
// heard, taken a frame of the window at a time.
type meter struct {
	l      plug.Listener
	levels *audioui.Levels
	// since is the time since the last chunk of sound, which the levels
	// take it over.
	since time.Duration
	// heard says sound came in over the last step.
	heard bool
}

func newMeter(s plug.Sound) *meter {
	return &meter{l: plug.Listener{Sound: s}, levels: audioui.NewLevels()}
}

// step takes what was heard over dt, and says the levels still move.
// Between the chunks the sound comes in, the levels hold.
func (m *meter) step(dt time.Duration) bool {
	m.since += dt
	frames, rate, playing := m.l.Hear(8192, dt)
	m.heard = len(frames) > 0 || playing
	if len(frames) > 0 {
		m.levels.Take(frames, rate, m.since)
		m.since = 0
		return true
	}
	if playing {
		return true
	}
	m.since = 0
	m.levels.Quiet(dt)
	return m.levels.Peak[0] > -89 || m.levels.Peak[1] > -89 || m.levels.RMS[0] > -89 || m.levels.RMS[1] > -89
}

// chipText is what a chip says at s: the gain, then Ø where a side is
// inverted and M where the sound is folded to mono.
func chipText(s Settings) string {
	var b strings.Builder
	b.WriteString(gainText(s.Gain))
	if s.InvertL || s.InvertR {
		b.WriteString(" Ø")
	}
	if s.Mono != MonoOff {
		b.WriteString(" M")
	}
	return b.String()
}

// chip shows the gain and the marks, in a plugin's chip in a chain, and,
// beside them, the level that comes out, left over right.
type chip struct {
	set   Settings
	label *plugui.ChipText
	out   *meter
	size  geom.Size
}

func newChip(h plug.Host) *chip {
	s := SettingsOf(h)
	_, out := h.Sound()
	return &chip{set: s, label: plugui.NewChipText(chipText(s)), out: newMeter(out)}
}

// Children implements [gunim.Composite].
func (c *chip) Children() []gunim.Node { return []gunim.Node{c.label} }

// chipBars is how wide the chip's bars are.
const chipBars = 40

// Layout implements [gunim.Node]: the words, and the bars at the right.
func (c *chip) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	c.size = cs.Max
	kids.At(0).Layout(gunim.Constraints{Max: geom.Sz(max(c.size.W-chipBars-6, 0), c.size.H)})
	kids.At(0).Place(geom.Point{})
	return c.size
}

// Paint implements [gunim.Node]: each channel's RMS over its peak,
// fainter, coral where it reaches full scale.
func (c *chip) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
	ink, ground, over := widget.Ink.Get(f.Theme), audioui.Ground.Get(f.Theme), audioui.Over.Get(f.Theme)
	x, h := box.W-chipBars, float32(3)
	top := (box.H - 2*h - 2) / 2
	lv := c.out.levels
	for ch := range 2 {
		y := top + float32(ch)*(h+2)
		p.RRect(geom.Rc(x, y, chipBars, h), h/2, paint.Solid(audioui.Faded(ground, 0.85)))
		if pk := audioui.MeterAt(lv.Peak[ch]); lv.Peak[ch] > -59 {
			p.RRect(geom.Rc(x, y, chipBars*pk, h), h/2, paint.Solid(audioui.Faded(ink, 0.3)))
		}
		if lv.RMS[ch] > -59 {
			c := ink
			if lv.Peak[ch] > -0.1 {
				c = over
			}
			p.RRect(geom.Rc(x, y, chipBars*audioui.MeterAt(lv.RMS[ch]), h), h/2, paint.Solid(audioui.Faded(c, 0.85)))
		}
	}
}

// Step implements [gunim.Animator].
func (c *chip) Step(dt time.Duration) bool { return c.out.step(dt) }

// Access implements [gunim.Accessible]: the chip reads as its words.
func (c *chip) Access() access.Info { return c.label.Access() }

// Follow implements [plug.Follower].
func (c *chip) Follow(id uint32, v float64, u *gunim.UI) {
	c.set = c.set.with(id, v)
	if t := chipText(c.set); t != c.label.Text {
		c.label.Text = t
		u.Invalidate()
	}
}

// gainTaper turns the gain knob most of its way between -24 and +24 dB,
// where a gain is mostly set: a tenth of the way below, from off, and a
// tenth above, to +48 dB.
var gainTaper = func() *plugui.Taper {
	g := param(GainID)
	// The bends, as decibels and how far round.
	db := [...]float64{g.Min, -24, 24, g.Max}
	at := [...]float64{0, 0.1, 0.9, 1}
	bend := func(x float64, from, to [4]float64) float64 {
		for i := 1; i < len(from); i++ {
			if x <= from[i] || i == len(from)-1 {
				u := (x - from[i-1]) / (from[i] - from[i-1])
				return to[i-1] + (to[i]-to[i-1])*min(max(u, 0), 1)
			}
		}
		return to[len(to)-1]
	}
	return &plugui.Taper{
		To:   func(n float64) float64 { return bend(g.Plain(n), db, at) },
		From: func(t float64) float64 { return g.Normal(bend(t, at, db)) },
	}
}()

// look is the ribbons as they show: each value glides to what the
// settings make it, so a change moves the ribbons into their new shape.
type look struct {
	anim.Group
	// weight is how wide each way through is, as the stereo matrix at
	// 0 dB has it, without its sign: LL, RL, LR, RR.
	weight [4]*anim.Float
	// flip is how far each channel coming in is turned over, and drop
	// how far it is dropped, as Left only drops the right.
	flip, drop [2]*anim.Float
	// sum is how far the sound is folded to mono, and gain how wide the
	// gain leaves the ribbons, as a factor.
	sum, gain *anim.Float
}

func newLook(s Settings) *look {
	l := &look{sum: anim.NewFloat(0), gain: anim.NewFloat(0)}
	for i := range l.weight {
		l.weight[i] = anim.NewFloat(0)
		l.Add(l.weight[i])
	}
	for c := range 2 {
		l.flip[c], l.drop[c] = anim.NewFloat(0), anim.NewFloat(0)
		l.Add(l.flip[c], l.drop[c])
	}
	l.Add(l.sum, l.gain)
	l.aim(s, nil)
	return l
}

// gainWidth is how wide the gain leaves a ribbon, as a factor: half as
// fast as the sound, so a boost of 48 dB does not fill the panel, and
// none at all at off.
func gainWidth(db float64) float32 {
	if db <= GainOff {
		return 0
	}
	return float32(min(math.Pow(10, db/40), 2.4))
}

// aim sets each value gliding to what s makes it; with no UI, as the
// look is made, it is there at once.
func (l *look) aim(s Settings, u *gunim.UI) {
	flat := s
	flat.Gain = 0
	m := flat.Matrix()
	set := func(f *anim.Float, v float32) {
		if u == nil {
			f.Jump(v)
			return
		}
		f.Animate(v, widget.Quick.Get(u.Theme()))
	}
	for i, w := range [4]float64{m.LL, m.RL, m.LR, m.RR} {
		set(l.weight[i], float32(math.Abs(w)))
	}
	set(l.flip[0], float32(onOffF(s.InvertL)))
	set(l.flip[1], float32(onOffF(s.InvertR)))
	set(l.drop[0], float32(onOffF(s.Mono == MonoRight)))
	set(l.drop[1], float32(onOffF(s.Mono == MonoLeft)))
	set(l.sum, float32(onOffF(s.Mono == MonoSum)))
	set(l.gain, gainWidth(s.Gain))
	if u != nil {
		u.Invalidate()
	}
}

func onOffF(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// panel is the ribbons, the left channel's and the right's, running
// from the meter of the sound fed in to the meter of the sound that
// comes out, through the controls: each side's invert lamp and pan knob
// on its ribbon, and over them the mono selector and the gain knob.
type panel struct {
	anim.Group
	h       plug.Host
	set     Settings
	look    *look
	invert  [2]*plugui.Power
	pan     [2]*plugui.Knob
	mono    *plugui.Selector
	gain    *plugui.Knob
	in, out *meter
	// flow is how far the light on the ribbons has flowed, in seconds
	// of sound heard.
	flow float64
	// lay is where things stand, as last laid out, and pts the points
	// of the ribbon being drawn, kept from frame to frame.
	lay  layout
	pts  []paint.RibbonPoint
	size geom.Size
}

// The panel's channels' names, and their IDs by channel.
var (
	sideNames = [2]string{"L", "R"}
	invertIDs = [2]uint32{InvertLID, InvertRID}
	panIDs    = [2]uint32{PanLID, PanRID}
)

func newPanel(h plug.Host) *panel {
	s := SettingsOf(h)
	in, out := h.Sound()
	p := &panel{h: h, set: s, look: newLook(s), in: newMeter(in), out: newMeter(out)}
	p.Add(p.look)
	for c := range 2 {
		id := invertIDs[c]
		lamp := plugui.NewPower("invert of the " + [2]string{"left", "right"}[c] + " side")
		lamp.SetChecked(c == 0 && s.InvertL || c == 1 && s.InvertR, nil)
		lamp.OnChange = func(on bool, u *gunim.UI) gunim.Intent {
			h.Begin(id, u)
			h.Set(id, onOffF(on), u)
			h.End(id, u)
			p.take(id, onOffF(on), u)
			return nil
		}
		p.invert[c] = lamp
		k := plugui.NewKnob(h, param(panIDs[c]))
		k.Compact = true
		k.Tooltip = "Where the " + [2]string{"left", "right"}[c] + " channel plays, from left to right"
		k.OnTurn = func(v float64, u *gunim.UI) { p.take(panIDs[c], v, u) }
		p.pan[c] = k
	}
	p.mono = plugui.NewSelector("Mono", monoNames[:], []string{
		"Stereo, as it is",
		"Both sides as one, on both sides: how a mono speaker plays it",
		"The left side alone, on both sides; the right is dropped",
		"The right side alone, on both sides; the left is dropped",
	})
	p.mono.Columns = 2
	p.mono.SetSelected(s.Mono, nil)
	p.mono.OnPick = func(i int, u *gunim.UI) {
		h.Begin(MonoID, u)
		h.Set(MonoID, float64(i), u)
		h.End(MonoID, u)
		p.take(MonoID, float64(i), u)
	}
	p.gain = plugui.NewKnob(h, param(GainID))
	p.gain.Taper = gainTaper
	p.gain.Tooltip = "How loud the sound comes out; all the way down is silence"
	p.gain.OnTurn = func(v float64, u *gunim.UI) { p.take(GainID, v, u) }
	p.lock()
	return p
}

// lock greys the pan knobs where Left only or Right only leaves them
// nothing to pan.
func (p *panel) lock() {
	alone := p.set.Mono == MonoLeft || p.set.Mono == MonoRight
	p.pan[0].Disabled, p.pan[1].Disabled = alone, alone
}

// take takes parameter id's value v, as set here or elsewhere, and
// moves the ribbons to it.
func (p *panel) take(id uint32, v float64, u *gunim.UI) {
	p.set = p.set.with(id, v)
	p.lock()
	p.look.aim(p.set, u)
}

// Children implements [gunim.Composite].
func (p *panel) Children() []gunim.Node {
	return []gunim.Node{p.invert[0], p.invert[1], p.pan[0], p.pan[1], p.mono, p.gain}
}

// layout is where the panel's parts stand: the meters, the ribbons'
// ends, the stages along them, and their lanes.
type layout struct {
	// inX and outX are where the meters stand, top to bottom their
	// extent.
	inX, outX, top, bottom float32
	// x0 and x1 are where the ribbons start and end; invert, pan, mono
	// and gain where each stage is along them.
	x0, x1, invert, pan, mono, gain float32
	// lane is each channel's lane, and mid the middle between them.
	lane [2]float32
	mid  float32
}

// The panel's sizes: its edges, the row of controls over the ribbons,
// the room under them, and how wide a ribbon is.
const (
	edge     = 12
	rowTop   = 6
	foot     = 50
	ribbonW  = 16
	laneMin  = 64
	laneMost = 120
)

// Layout implements [gunim.Node]: the meters at either end, the stages
// spread between them, the lanes under the row of controls.
func (p *panel) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	p.size = c.Max
	l := &p.lay
	l.inX, l.outX = edge, p.size.W-edge-audioui.MeterW
	l.x0, l.x1 = l.inX+audioui.MeterW+8, l.outX-8
	along := func(t float32) float32 { return l.x0 + (l.x1-l.x0)*t }
	l.invert, l.pan, l.mono, l.gain = along(0.12), along(0.32), along(0.6), along(0.85)
	l.lane[0] = rowTop + plugui.KnobH + 24
	gap := min(max(p.size.H-l.lane[0]-foot, laneMin), laneMost)
	l.lane[1] = l.lane[0] + gap
	l.mid = (l.lane[0] + l.lane[1]) / 2
	l.top, l.bottom = l.lane[0]-30, l.lane[1]+30

	place := func(i int, sz geom.Size, at geom.Point) {
		kids.At(i).Layout(gunim.Tight(sz))
		kids.At(i).Place(at)
	}
	for ch := range 2 {
		place(ch, geom.Sz(plugui.PowerSize, plugui.PowerSize),
			geom.Pt(l.invert-plugui.PowerSize/2, l.lane[ch]-plugui.PowerSize/2))
		// The knob's cap, 4 + 14 down its box, on the lane.
		place(2+ch, geom.Sz(plugui.CompactW, plugui.CompactH), geom.Pt(l.pan-plugui.CompactW/2, l.lane[ch]-18))
	}
	ms := kids.At(4).Layout(gunim.Constraints{Max: geom.Sz(80, plugui.SelectorH)})
	kids.At(4).Place(geom.Pt(l.mono-ms.W/2, rowTop))
	place(5, geom.Sz(plugui.KnobW, plugui.KnobH), geom.Pt(l.gain-plugui.KnobW/2, rowTop))
	return p.size
}

// Paint implements [gunim.Node]: the meters, In and Out, under their
// names; the ribbons; the controls over them.
func (p *panel) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	ink := widget.Ink.Get(th)
	l := p.lay
	for i, m := range []*meter{p.in, p.out} {
		x := []float32{l.inX, l.outX}[i]
		name := audioui.Shaped([]string{"IN", "OUT"}[i], 9, true, false)
		name.Paint(pt, geom.Pt(x+(audioui.MeterW-name.Advance)/2, l.bottom+4), audioui.Faded(ink, 0.6))
		audioui.PaintMeter(pt, th, geom.Rect{Min: geom.Pt(x, l.top), Max: geom.Pt(x+audioui.MeterW, l.bottom)}, m.levels)
	}
	// The gate the gain squeezes the ribbons through.
	pt.RRect(geom.Rc(l.gain-7, l.top-6, 14, l.bottom-l.top+12), 7, paint.Solid(audioui.Faded(ink, 0.06)))
	p.paintRibbons(pt, th)
	for ch := range 2 {
		name := audioui.Shaped("Ø "+sideNames[ch], 9, true, false)
		name.Paint(pt, geom.Pt(l.invert-name.Advance/2, l.lane[ch]-plugui.PowerSize/2-4-name.Height()), audioui.Faded(ink, 0.6))
	}
	for k := range kids.All {
		k.Paint(pt)
	}
}

// Step implements [gunim.Animator]: the ribbons glide, the meters
// move, and the light flows while sound comes in.
func (p *panel) Step(dt time.Duration) bool {
	moving := p.Group.Step(dt)
	in, out := p.in.step(dt), p.out.step(dt)
	if p.in.heard {
		p.flow += dt.Seconds()
	}
	return moving || in || out
}

// Follow implements [plug.Follower]: the controls glide to a value set
// elsewhere, unless held, and the ribbons with them.
func (p *panel) Follow(id uint32, v float64, u *gunim.UI) {
	switch id {
	case InvertLID, InvertRID:
		c := 0
		if id == InvertRID {
			c = 1
		}
		if on := v > 0.5; on != p.invert[c].Checked() {
			p.invert[c].SetChecked(on, u)
		}
	case PanLID:
		p.pan[0].Follow(v, u)
	case PanRID:
		p.pan[1].Follow(v, u)
	case MonoID:
		p.mono.SetSelected(monoOf(v), u)
	case GainID:
		p.gain.Follow(v, u)
	}
	p.take(id, v, u)
}

// smooth runs from 0 at u = 0 to 1 at u = 1, easing in and out, and
// stays there past either end.
func smooth(u float32) float32 {
	u = min(max(u, 0), 1)
	return u * u * (3 - 2*u)
}

// bell is 1 at x = at and falls smoothly to 0 at half either side of it.
func bell(x, at, half float32) float32 {
	d := min(float64(abs32(x-at)/half), 1)
	return float32(math.Cos(d*math.Pi)+1) / 2
}

func abs32(v float32) float32 { return float32(math.Abs(float64(v))) }

// The ribbons' shape: the half width of the twist an invert makes, the
// steps the ribbons are drawn in, how far the braid swings and how long
// its turns are, and how far the gain draws the lanes together.
const (
	twistHalf  = 22
	ribbonStep = 3
	braidSwing = 0.14
	braidTurn  = 70
	squeeze    = 0.32
)

// paintRibbons draws each channel's ribbon from the meter in to its pan
// knob, and from there a strand to each side out, as wide as the share
// that goes there: under the strands the right channel's, over them the
// left's.
func (p *panel) paintRibbons(pt *paint.Painter, th *theme.Live) {
	l, lk := p.lay, p.look
	if l.x1-l.x0 < 4*twistHalf {
		return
	}
	front := [2]color.NRGBA{BraidLeft.Get(th), BraidRight.Get(th)}
	level := [2]float32{}
	for c := range 2 {
		level[c] = audioui.MeterAt(p.in.levels.RMS[c])
	}
	gain := max(lk.gain.Value(), 0)
	// light is how lit a ribbon of channel c is at x: brighter with the
	// sound, with bands of light flowing along it.
	light := func(col color.NRGBA, c int, x float32) color.NRGBA {
		wave := 0.5 + 0.5*math.Sin(2*math.Pi*(float64(x)/48-p.flow*1.2))
		b := 0.72 + 0.28*level[c]*float32(wave)
		return audioui.Mix(color.NRGBA{A: 0xff}, col, b)
	}
	// side is channel c's colour past its invert: its back as far as it
	// is turned over.
	side := func(c int) color.NRGBA { return audioui.Mix(front[c], back(front[c]), lk.flip[c].Value()) }

	for _, c := range [2]int{1, 0} {
		// The ribbon in, the twist at the lamp, and, as far as it is
		// dropped, its end drawn back toward the meter.
		flip, drop := min(max(lk.flip[c].Value(), 0), 1), min(max(lk.drop[c].Value(), 0), 1)
		end := l.pan + (l.x0-l.pan)*drop
		p.pts = p.pts[:0]
		for x := l.x0; x <= l.pan; x += ribbonStep {
			u := (x - (l.invert - twistHalf)) / (2 * twistHalf)
			w := float32(ribbonW)
			col := front[c]
			if u > 0 && u < 1 {
				w *= 1 - flip*(1-abs32(float32(math.Cos(float64(u)*math.Pi))))
			}
			col = audioui.Mix(col, side(c), smooth((u-0.45)/0.1))
			w *= smooth((end - x) / 24)
			p.pts = append(p.pts, paint.RibbonPoint{At: geom.Pt(x, l.lane[c]), Width: w, Color: light(col, c, x)})
		}
		pt.Ribbon(p.pts)
	}

	// The strands out, by the matrix's order: LL, RL, LR, RR.
	type strand struct{ from, to int }
	strands := [4]strand{{0, 0}, {1, 0}, {0, 1}, {1, 1}}
	for _, i := range [4]int{3, 1, 2, 0} {
		s := strands[i]
		w := max(lk.weight[i].Value(), 0)
		if w*ribbonW < 0.05 {
			continue
		}
		col := side(s.from)
		phase := float64(i) * math.Pi / 2
		bendEnd := l.pan + (l.mono-l.pan)*0.8
		sum := lk.sum.Value()
		p.pts = p.pts[:0]
		// The two strands to a side lie side by side, the left
		// channel's over the right's, so together they are as wide as
		// both.
		beside := lk.weight[i^1].Value() * ribbonW / 2
		if s.from == 0 {
			beside = -beside
		}
		for x := l.pan; x <= l.x1+ribbonStep/2; x += ribbonStep {
			x := min(x, l.x1)
			bent := smooth((x - l.pan) / (bendEnd - l.pan))
			g := 1 + (gain-1)*smooth((x-(l.gain-24))/48)
			y := l.lane[s.from] + (l.lane[s.to]-l.lane[s.from])*bent + beside*bent*g
			// The braid: the strands drawn together and twined round
			// the middle as far as the sound is summed.
			swing := braidSwing * (l.lane[1] - l.lane[0]) * float32(math.Sin(2*math.Pi*float64(x-l.mono)/braidTurn+phase))
			b := sum * bell(x, l.mono, (l.mono-l.pan)*0.9)
			y += (l.mid + swing - y) * b
			// The gain: the lanes drawn in, and the ribbons as wide as
			// it leaves them past it.
			y += (l.mid - y) * squeeze * bell(x, l.gain, 60)
			p.pts = append(p.pts, paint.RibbonPoint{At: geom.Pt(x, y), Width: ribbonW * w * g, Color: light(col, s.from, x)})
		}
		pt.Ribbon(p.pts)
	}
}
