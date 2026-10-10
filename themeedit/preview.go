package themeedit

import (
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// A demo is what the preview plays to show a motion.
type demo uint8

const (
	// demoSlide slides a chip across a track and back: for a motion
	// the editor knows no better way to show.
	demoSlide demo = iota
	// demoCaret jumps the terminal's cursor along its line.
	demoCaret
	// demoQuick flips the switch and the check, and back.
	demoQuick
	// demoSettle slides a panel in from the side, and out.
	demoSettle
	// demoBounce pops a toast in, and fades it out.
	demoBounce
	// demoSwitch fades the preview to the base theme, and back.
	demoSwitch
)

// demos are the demos of the motions gunim declares, by key. The rest
// play demoSlide.
var demos = map[string]demo{
	widget.Caret.Key():    demoCaret,
	widget.Quick.Key():    demoQuick,
	widget.RingFade.Key(): demoQuick,
	widget.Settle.Key():   demoSettle,
	widget.Reflow.Key():   demoSettle,
	widget.Bounce.Key():   demoBounce,
	theme.Switch.Key():    demoSwitch,
}

// hold is how long a demo rests at its end before it goes back, past
// the time its motion takes.
const hold = 900 * time.Millisecond

// previewPanel is the Preview: a heading, and under it a small window
// in the theme as edited, where a motion plays as it changes. It wears
// the edits; the controls beside it do not. Above the controls, as in
// a narrow editor, a button beside the heading folds it away.
type previewPanel struct {
	heading *widget.Label
	caption *widget.Label
	toggle  *widget.IconButton
	themed  *widget.Themed
	scene   *scene
	fold    *widget.Fold
	// narrow says the panel is above the controls, and short that the
	// editor is short too. shut says the preview is folded away there,
	// chose that the user folded or opened it, which the editor leaves
	// as it is from then on, and playing that a motion opened it for as
	// long as it plays.
	narrow, short, shut bool
	chose, playing      bool
	// stops cancels the steps of the demo playing.
	stops []func()
}

func newPreviewPanel(content gunim.Node, spec *specimen, title string, th theme.Theme) *previewPanel {
	p := &previewPanel{heading: widget.NewLabel("Preview"), caption: widget.NewLabel("")}
	p.heading.Face = widget.BoldFont
	p.caption.Color, p.caption.NoWrap, p.caption.MaxLines = widget.Placeholder, true, 1
	p.scene = newScene(content, spec, title)
	p.themed = widget.NewThemed(p.scene, th)
	p.fold = widget.NewFold(p.themed, true)
	p.toggle = widget.NewIconButton(icon.ChevronUp, "Hide the preview")
	p.toggle.IconSize = IconSize
	p.toggle.OnClick = func(u *gunim.UI) gunim.Intent {
		p.chose, p.playing = true, false
		p.setShut(!p.shut, u)
		return nil
	}
	return p
}

// setShut folds the preview away, or opens it.
func (p *previewPanel) setShut(on bool, u *gunim.UI) {
	p.shut = on
	p.toggle.Icon, p.toggle.Tooltip = icon.ChevronUp, "Hide the preview"
	if on {
		p.toggle.Icon, p.toggle.Tooltip = icon.ChevronDown, "Show the preview"
	}
	p.fold.SetOpen(!on || !p.narrow, u)
}

// setNarrow puts the panel above the controls, or beside them, where
// it never folds. Above the controls in a short editor it starts folded
// away, until the user opens it.
func (p *previewPanel) setNarrow(narrow, short bool, u *gunim.UI) {
	if p.narrow == narrow && p.short == short {
		return
	}
	p.narrow, p.short = narrow, short
	p.toggle.Disabled = !narrow
	if !p.chose && !p.playing {
		p.shut = short
	}
	p.setShut(p.shut, u)
}

// use dresses the preview in th, at once.
func (p *previewPanel) use(th theme.Theme) {
	p.themed.Use(th.With(theme.Set(theme.Switch, Instant)))
}

// play plays d with spring s, saying it shows label. base and edited
// are the themes a theme switch goes between.
func (p *previewPanel) play(d demo, s anim.Spring, label string, base, edited theme.Theme, u *gunim.UI) {
	for _, stop := range p.stops {
		stop()
	}
	p.stops = p.stops[:0]
	if p.shut && p.narrow {
		// The preview opens to play, and folds away again after.
		p.playing = true
		p.setShut(false, u)
	}
	p.caption.Text = "Playing " + label
	back := time.Duration(float64(s.Response)*float64(time.Second)) + hold
	later := func(d time.Duration, fn func(u *gunim.UI)) {
		if u != nil {
			p.stops = append(p.stops, u.After(d, fn))
		}
	}
	sc := p.scene
	switch d {
	case demoCaret:
		sc.spec.term.jump()
		back = 4 * jumpEvery
	case demoQuick:
		sc.spec.pulse(u)
		later(back, sc.spec.pulse)
	case demoSettle:
		sc.panel.Jump(0)
		sc.panel.Animate(1, s)
		later(back, func(*gunim.UI) { sc.panel.Animate(0, s) })
	case demoBounce:
		sc.toast.Jump(0)
		sc.toast.Animate(1, s)
		sc.fade.Jump(1)
		later(back+hold, func(*gunim.UI) { sc.fade.Animate(0, widget.Settle.Default()) })
		back += hold
	case demoSwitch:
		switched := func(th theme.Theme) theme.Theme { return th.With(theme.Set(theme.Switch, s)) }
		p.themed.Use(switched(base))
		later(back, func(*gunim.UI) { p.themed.Use(switched(edited)) })
		back *= 2
	case demoSlide:
		sc.chipOn.Jump(1)
		sc.chip.Jump(0)
		sc.chip.Animate(1, s)
		later(back, func(*gunim.UI) { sc.chip.Animate(0, s) })
		later(2*back, func(*gunim.UI) { sc.chipOn.Animate(0, widget.Settle.Default()) })
		back *= 2
	}
	later(back+hold, func(u *gunim.UI) {
		p.caption.Text = ""
		if p.playing {
			p.playing = false
			p.setShut(true, u)
		}
		u.Invalidate()
	})
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (p *previewPanel) Children() []gunim.Node {
	return []gunim.Node{p.heading, p.caption, p.toggle, p.fold}
}

// Layout implements [gunim.Node]: the heading with the caption after it
// and the fold button at the right, and the window under them, as tall
// as it needs, or as there is room for.
func (p *previewPanel) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	th := f.Theme
	w := c.Max.W
	gap := widget.Gap.Get(th)
	ts := kids.At(2).Layout(gunim.Constraints{})
	hs := kids.At(0).Layout(gunim.Constraints{Max: geom.Sz(w, 0)})
	cs := kids.At(1).Layout(gunim.Constraints{Max: geom.Sz(max(0, w-hs.W-ts.W-3*gap), 0)})
	top := max(hs.H, ts.H)
	kids.At(0).Place(geom.Pt(0, (top-hs.H)/2))
	kids.At(1).Place(geom.Pt(hs.W+gap, (top-cs.H)/2))
	kids.At(2).Place(geom.Pt(w-ts.W, (top-ts.H)/2))
	y := top + HeadingGap.Get(th)
	maxH := float32(0)
	if c.Max.H > 0 {
		maxH = max(1, c.Max.H-y)
	}
	// The fold measures the window at its full height: the window keeps
	// to the room itself.
	p.scene.maxH = maxH
	fs := kids.At(3).Layout(gunim.Constraints{Min: geom.Sz(w, 0), Max: geom.Sz(w, maxH)})
	kids.At(3).Place(geom.Pt(0, y))
	return c.Constrain(geom.Sz(w, y+fs.H))
}

// Paint implements [gunim.Node].
func (p *previewPanel) Paint(pt *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for i := range kids.Len() {
		if i == 2 && !p.narrow {
			continue
		}
		kids.At(i).Paint(pt)
	}
}

// scene is the window the preview draws: a title bar, the content under
// it, and the demos over it, clipped to its round corners.
type scene struct {
	anim.Group
	content gunim.Node
	// spec is the default content, or nil where the program gave its
	// own.
	spec *specimen
	// side is the panel that slides in, and note the toast that pops.
	side, note *widget.Card
	// panel is how far the panel is in, toast how far the toast has
	// popped and fade how much of it shows, and chip where the chip is
	// along its track and chipOn how much of the track shows.
	panel, toast, fade, chip, chipOn *anim.Float
	// name is the window's name in its title bar, or empty for none,
	// and title it shaped.
	name           string
	title          text.Run
	titleFace      *text.Face
	titleSize      float32
	sideAt, noteAt geom.Rect
	bar            float32
	// maxH is the most height the panel has room for, or zero for no
	// bound.
	maxH float32
}

func newScene(content gunim.Node, spec *specimen, name string) *scene {
	s := &scene{content: content, spec: spec, name: name,
		panel: anim.NewFloat(0), toast: anim.NewFloat(0), fade: anim.NewFloat(0), chip: anim.NewFloat(0), chipOn: anim.NewFloat(0)}
	s.Add(s.panel, s.toast, s.fade, s.chip, s.chipOn)
	head := widget.NewLabel("Details")
	head.Face = widget.BoldFont
	sub := widget.NewLabel("Edited a minute ago")
	sub.Color = widget.Placeholder
	more := widget.NewLabel("Three items, one of them done.")
	side := widget.Column(head, sub, more)
	side.Cross = widget.CrossStretch
	s.side = widget.NewCard(side)
	saved := widget.Row(widget.NewIcon(icon.Check, ""), widget.NewLabel("Saved"))
	saved.Cross = widget.CrossCenter
	s.note = widget.NewCard(saved)
	s.note.Fill = widget.MenuFill
	return s
}

// Children implements [gunim.Composite].
func (s *scene) Children() []gunim.Node { return []gunim.Node{s.content, s.side, s.note} }

// Layout implements [gunim.Node]. Given a height, the scene keeps to
// it, and what does not fit is cut off.
func (s *scene) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	th := f.Theme
	w := c.Max.W
	s.bar = PreviewTitleHeight.Get(th)
	face, size := widget.Font.Get(th), widget.TextSize.Get(th)*0.9
	if face != s.titleFace || size != s.titleSize {
		s.titleFace, s.titleSize = face, size
		s.title = face.Shape(s.name, size)
	}
	cs := kids.At(0).Layout(gunim.Constraints{Min: geom.Sz(w, 0), Max: geom.Sz(w, 0)})
	kids.At(0).Place(geom.Pt(0, s.bar))
	h := s.bar + cs.H
	if s.maxH > 0 {
		h = min(h, s.maxH)
	}
	// The panel takes the right of the window under the title bar, two
	// fifths of it, and the toast sits in the middle.
	m := widget.Margin.Get(th)
	pw := max(160, w*0.42)
	ps := kids.At(1).Layout(gunim.Constraints{Min: geom.Sz(pw, 0), Max: geom.Sz(pw, 0)})
	s.sideAt = geom.Rc(w-pw-m.Right, s.bar+m.Top, pw, ps.H)
	kids.At(1).Place(s.sideAt.Min)
	ns := kids.At(2).Layout(gunim.Constraints{})
	s.noteAt = geom.Rc((w-ns.W)/2, (h+s.bar-ns.H)/2, ns.W, ns.H)
	kids.At(2).Place(s.noteAt.Min)
	return c.Constrain(geom.Sz(w, h))
}

// Paint implements [gunim.Node].
func (s *scene) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	r := geom.Rect{Max: box.Point()}
	radius := PreviewRadius.Get(th)
	border := widget.DialogBorder.Get(th)
	shadow := widget.DialogShadow.Get(th)
	func() {
		defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: 1, Clip: true, Radius: radius})()
		p.RRect(r, 0, paint.Solid(widget.Background.Get(th)))
		// The title bar, with three dots for the window's buttons and the
		// window's name in the middle.
		p.RRect(geom.Rc(0, 0, box.W, s.bar), 0, paint.Solid(widget.CardFill.Get(th)))
		p.RRect(geom.Rc(0, s.bar-1, box.W, 1), 0, paint.Solid(border))
		d := s.bar / 4
		for i := range 3 {
			x := s.bar/2 - d/2 + float32(i)*(d+d*0.8)
			p.RRect(geom.Rc(x, (s.bar-d)/2, d, d), d/2, paint.Solid(border))
		}
		s.title.Paint(p, geom.Pt((box.W-s.title.Advance)/2, (s.bar-s.title.Height())/2), widget.Placeholder.Get(th))
		kids.At(0).Paint(p)

		// The panel, sliding in from past the right edge.
		if in := s.panel.Value(); in > 0.001 {
			dx := (1 - in) * (s.sideAt.Size().W + widget.Margin.Get(th).Right + 8)
			func() {
				defer p.Push(paint.Translate(geom.Pt(dx, 0)))()
				p.ShadowRRect(s.sideAt, widget.CardRadius.Get(th), paint.Solid(widget.CardFill.Get(th)),
					paint.Shadow{Offset: geom.Pt(0, 4), Blur: 14, Color: shadow})
				kids.At(1).Paint(p)
			}()
		}
		// The toast, popping up from its middle.
		if op := clamp01(s.fade.Value()); op > 0.001 {
			sc := max(0, s.toast.Value())
			mid := geom.Pt(s.noteAt.Min.X+s.noteAt.Size().W/2, s.noteAt.Min.Y+s.noteAt.Size().H/2)
			func() {
				defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: op})()
				defer p.Push(paint.Scale(sc, mid))()
				p.ShadowRRect(s.noteAt, widget.CardRadius.Get(th), paint.Solid(widget.MenuFill.Get(th)),
					paint.Shadow{Offset: geom.Pt(0, 4), Blur: 14, Color: shadow})
				kids.At(2).Paint(p)
			}()
		}
		// The chip on its track, along the foot.
		if on := clamp01(s.chipOn.Value()); on > 0.001 {
			s.paintChip(p, th, box, on)
		}
	}()
	p.RRectStroke(r, radius, paint.Fill{}, paint.Stroke{Width: 1, Color: border})
}

// paintChip draws a track across the middle of the window, on a card as
// a toast is, and the chip on it, at strength on.
func (s *scene) paintChip(p *paint.Painter, th *theme.Live, box geom.Size, on float32) {
	m := widget.Margin.Get(th)
	h := widget.ControlHeight.Get(th)
	pad := widget.Gap.Get(th)
	card := geom.Rc(m.Left, (box.H+s.bar)/2-h/2-pad, box.W-m.Left-m.Right, h+2*pad)
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: on})()
	p.ShadowRRect(card, widget.CardRadius.Get(th), paint.Solid(widget.MenuFill.Get(th)),
		paint.Shadow{Offset: geom.Pt(0, 4), Blur: 14, Color: widget.DialogShadow.Get(th)})
	track := card.Inset(geom.Uniform(pad))
	p.RRect(track, h/2, paint.Solid(widget.FieldFill.Get(th)))
	cw := h * 2
	x := track.Min.X + 3 + s.chip.Value()*(track.Size().W-cw-6)
	p.RRect(geom.Rc(x, track.Min.Y+3, cw, h-6), (h-6)/2, paint.Solid(widget.Accent.Get(th)))
}

// Access implements [gunim.Accessible].
func (s *scene) Access() access.Info {
	return access.Info{Role: access.RoleGroup, Name: "Preview of the theme as edited"}
}

// clamp01 returns v held between 0 and 1.
func clamp01(v float32) float32 { return max(0, min(v, 1)) }
