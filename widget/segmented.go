package widget

import (
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// Segmented tokens.
var (
	// SegmentedHeight is a segmented control's height.
	SegmentedHeight = theme.Length("segmented.height", 28)
	// SegmentedPadding is the room either side of an option's icon and label.
	SegmentedPadding = theme.Length("segmented.padding", 12)
	// SegmentedTrack fills the track the options sit in, a shade apart from a card or the window behind it.
	SegmentedTrack = theme.Color("segmented.track", color.NRGBA{R: 0x12, G: 0x14, B: 0x19, A: 0xff})
)

// Segmented is a row of options in a rounded track, one of them chosen, with a pill that springs to the chosen one.
//
// A click on an option chooses it. With focus, Left and Right choose the option beside the chosen one, and Home and
// End the first and the last. Disabled, it fades faint and takes no clicks or keys. Every option is as wide as the widest,
// or, with Fit, as wide as its own label. Given less room, the options share it, and a label too long for its share ends
// in an ellipsis.
type Segmented struct {
	Control
	// Items are the options' labels and Icons their icons, in order: an icon, a label or both. The longer
	// of the two sets how many options there are.
	Items []string
	Icons []*icon.Icon
	// IconSize, when set, is the icons' size in place of [IconSize].
	IconSize theme.Token[float32]
	// Track, when set, fills the track in place of [SegmentedTrack].
	Track theme.Token[color.NRGBA]
	// Fit makes each option as wide as its own icon and label, in place of every option as wide as the widest: for
	// options of lengths far apart, such as filters with counts.
	Fit bool
	// OnChange runs on the UI goroutine when the user chooses an option; a non-nil result is sent to the
	// application as the control's intent.
	OnChange func(i int, u *gunim.UI) gunim.Intent

	selected int
	// pill is the pill's place, in options from the first.
	pill *anim.Float
	// hot is the option under the pointer, or -1.
	hot    int
	click  Clicker
	shaped []shapedText
	ell    shapedText
	// width is each option's width, and size the control's, from the last layout. With Fit, lefts holds each
	// option's left edge, and one more for the right edge of the last.
	width float32
	size  geom.Size
	lefts []float32
}

// NewSegmented returns a segmented control of labels, the first chosen.
func NewSegmented(labels ...string) *Segmented {
	s := &Segmented{Control: newControl(), Items: labels, pill: anim.NewFloat(0), hot: -1}
	s.Add(s.pill)
	return s
}

// Len returns how many options there are.
func (s *Segmented) Len() int { return max(len(s.Items), len(s.Icons)) }

// Selected returns the chosen option.
func (s *Segmented) Selected() int { return s.selected }

// SetSelected chooses option i and sends no intent. Once the control is laid out the pill springs to it; before
// that, or with a nil u, it jumps.
func (s *Segmented) SetSelected(i int, u *gunim.UI) {
	if i < 0 || i >= s.Len() || i == s.selected {
		return
	}
	s.selected = i
	if !s.laid || u == nil {
		s.pill.Jump(float32(i))
		return
	}
	s.pill.Animate(float32(i), Bounce.Get(u.Theme()))
	u.Invalidate()
}

// choose chooses option i and tells the application.
func (s *Segmented) choose(i int, u *gunim.UI) {
	if i < 0 || i >= s.Len() || i == s.selected {
		return
	}
	s.SetSelected(i, u)
	act(u, s, gunim.CueSelect, s.OnChange, i)
}

// at returns the option at x in the control's space, or -1.
func (s *Segmented) at(x float32) int {
	if s.Fit && len(s.lefts) == s.Len()+1 {
		for i := range s.Len() {
			if x >= s.lefts[i] && x < s.lefts[i+1] {
				return i
			}
		}
		return -1
	}
	if s.width <= 0 || x < 0 {
		return -1
	}
	if i := int(x / s.width); i < s.Len() {
		return i
	}
	return -1
}

// Handle implements [gunim.Handler].
func (s *Segmented) Handle(e input.Event, u *gunim.UI) bool {
	s.showTip(e, u, s)
	th := u.Theme()
	if s.Disabled {
		s.hot = -1
		return s.handleDisabled(e, u, nil)
	}
	switch e := e.(type) {
	case input.PointerEnter:
		s.hot = s.at(e.Pos.X)
	case input.PointerMove:
		s.hot = s.at(e.Pos.X)
	case input.PointerLeave:
		s.hot = -1
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		s.click.Press(e, s.at(e.Pos.X))
	case input.PointerUp:
		// A click chooses the option it lets go on, the one it pressed.
		if i := s.at(e.Pos.X); s.click.Release(e, i) && e.Pos.Y >= 0 && e.Pos.Y < s.size.H {
			s.choose(i, u)
		}
	case input.KeyPress:
		if e.Mods.Has(input.ModControl) || e.Mods.Has(input.ModAlt) {
			return false
		}
		switch e.Key {
		case input.KeyLeft:
			s.choose(s.selected-1, u)
		case input.KeyRight:
			s.choose(s.selected+1, u)
		case input.KeyHome:
			s.choose(0, u)
		case input.KeyEnd:
			s.choose(s.Len()-1, u)
		default:
			return false
		}
	default:
		if !s.ringFollows(e, th) {
			return false
		}
	}
	u.Invalidate()
	return true
}

// icon returns option i's icon, or nil.
func (s *Segmented) icon(i int) *icon.Icon {
	if i < 0 || i >= len(s.Icons) {
		return nil
	}
	return s.Icons[i]
}

// label returns option i's label, or "".
func (s *Segmented) label(i int) string {
	if i < 0 || i >= len(s.Items) {
		return ""
	}
	return s.Items[i]
}

// iconSize is the size the control draws its icons at.
func (s *Segmented) iconSize(th *theme.Live) float32 {
	if s.IconSize.Key() != "" {
		return s.IconSize.Get(th)
	}
	return IconSize.Get(th)
}

// content returns the width of option i's icon and label, side by side.
func (s *Segmented) content(i int, th *theme.Live) float32 {
	w := float32(0)
	if l := s.label(i); l != "" {
		w = s.shaped[i].shape(faceIn(Font, th), l, TextSize.Get(th)).Advance
	}
	if s.icon(i) != nil {
		if w > 0 {
			w += IconGap.Get(th)
		}
		w += s.iconSize(th)
	}
	return w
}

// Layout implements [gunim.Node].
func (s *Segmented) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	n := s.Len()
	if len(s.shaped) != n {
		s.shaped = make([]shapedText, n)
	}
	widest := float32(0)
	for i := range n {
		widest = max(widest, s.content(i, th))
	}
	s.width = widest + 2*SegmentedPadding.Get(th)
	if !s.laid {
		s.pill.Jump(float32(s.selected))
	}
	s.follow(th)
	if s.Fit {
		total := float32(0)
		for i := range n {
			total += s.content(i, th) + 2*SegmentedPadding.Get(th)
		}
		s.size = c.Constrain(geom.Sz(total, SegmentedHeight.Get(th)))
		s.fit(s.size.W, th)
		return s.size
	}
	s.size = c.Constrain(geom.Sz(s.width*float32(n), SegmentedHeight.Get(th)))
	// Given another width, the options share it: squeezed into less room,
	// or spread across more, never past the track.
	if n > 0 {
		s.width = s.size.W / float32(n)
	}
	return s.size
}

// fit sets each option's left edge, with Fit, for a control w wide: each option as wide as its own content, every
// option's share of the room left over, or short of it, the same.
func (s *Segmented) fit(w float32, th *theme.Live) {
	n := s.Len()
	s.lefts = s.lefts[:0]
	total := float32(0)
	for i := range n {
		total += s.content(i, th) + 2*SegmentedPadding.Get(th)
	}
	extra := float32(0)
	if n > 0 {
		extra = (w - total) / float32(n)
	}
	x := float32(0)
	for i := range n {
		s.lefts = append(s.lefts, x)
		x += max(0, s.content(i, th)+2*SegmentedPadding.Get(th)+extra)
	}
	s.lefts = append(s.lefts, x)
}

// span returns option i's left edge and width, or, for a place between two options, as the pill is while it moves,
// the edge and width part way between theirs.
func (s *Segmented) span(at float32) (x, w float32) {
	if !s.Fit || len(s.lefts) != s.Len()+1 {
		return at * s.width, s.width
	}
	n := s.Len()
	i := int(max(0, min(at, float32(n-1))))
	j := min(i+1, n-1)
	t := at - float32(i)
	x0, w0 := s.lefts[i], s.lefts[i+1]-s.lefts[i]
	x1, w1 := s.lefts[j], s.lefts[j+1]-s.lefts[j]
	return x0 + (x1-x0)*t, w0 + (w1-w0)*t
}

// Paint implements [gunim.Node].
func (s *Segmented) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	defer s.faint(p, box)()
	th := f.Theme
	h := box.H
	track := geom.Rect{Max: box.Point()}
	s.paintRing(p, track, h/2, th)
	fill := SegmentedTrack.Get(th)
	if s.Track.Key() != "" {
		fill = s.Track.Get(th)
	}
	p.RRect(track, h/2, paint.Solid(fill))
	// The options share the box Paint is given, which a parent may have
	// squeezed since layout.
	n := s.Len()
	if n == 0 {
		return
	}
	s.width = box.W / float32(n)
	if s.Fit {
		s.fit(box.W, th)
	}
	at := s.pill.Value()
	px, pw := s.span(at)
	p.RRect(geom.Rc(px+2, 2, max(0, pw-4), max(0, h-4)), max(0, h-4)/2, paint.Solid(Accent.Get(th)))

	faint, strong := Placeholder.Get(th), ButtonStrongInk.Get(th)
	for i := range n {
		// The option the pill is on reads on the pill; the one under the pointer lights up.
		near := 1 - min(max(at-float32(i), float32(i)-at), 1)
		ink := faint
		if i == s.hot {
			ink = Ink.Get(th)
		}
		ink = anim.Mix(anim.ColorCodec, ink, strong, near)
		left, width := s.span(float32(i))
		pad := min(SegmentedPadding.Get(th), width/4)
		room := width - 2*pad
		x := left + pad + max(0, room-s.content(i, th))/2
		if ic := s.icon(i); ic != nil {
			size := s.iconSize(th)
			if size > room {
				continue
			}
			drawIcon(p, th, ic, geom.Rc(x, (h-size)/2, size, size), ink, 1)
			x += size + IconGap.Get(th)
			room -= size + IconGap.Get(th)
		}
		if l := s.label(i); l != "" {
			run := fitRun(s.shaped[i].run, &s.ell, room)
			run.Paint(p, geom.Pt(x, (h-run.Height())/2), ink)
		}
	}
}

// Access implements [gunim.Accessible]: a group with a button for each option, the chosen one checked.
func (s *Segmented) Access() access.Info {
	info := access.Info{Role: access.RoleGroup, Name: s.Tooltip, State: s.accessState(), Active: s.selected + 1}
	for i := range s.Len() {
		name := s.label(i)
		if name == "" {
			name = iconName(s.icon(i))
		}
		x, w := s.span(float32(i))
		part := access.Info{Role: access.RoleButton, Name: name, State: access.StateCheckable | s.accessState(),
			Actions: []string{access.ActionPress}, Bounds: geom.Rc(x, 0, w, s.size.H)}
		if i == s.selected {
			part.State |= access.StateChecked
		}
		info.Parts = append(info.Parts, part)
	}
	return info
}

// AccessAct implements [gunim.AccessActor]: pressing an option chooses it.
func (s *Segmented) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || r.Part < 0 || r.Part >= s.Len() || s.Disabled {
		return false
	}
	s.choose(r.Part, u)
	return true
}
