package widget

import (
	"cmp"
	"slices"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// Stack tokens.
var (
	// StackEdge is how far in from a [Stack]'s left edge a finger starts the swipe that goes back.
	StackEdge = theme.Length("stack.edge", 20)
	// StackBack is the name a screen reader gives the action of a [Stack] that goes back.
	StackBack = theme.Choice("stack.back", "Back")
)

// stackBelow is how far the screen under the top one sits to the left, as a share of the width, while the top one
// covers it: it slides in under the one going, a third as fast, and fades up from the scrim.
const stackBelow = 0.3

// stackFling is the speed, in widths a second, past which a swipe let go goes back however far it went.
const stackFling = 0.8

// Stack shows screens one on top of another, the top one in front, as a phone's settings go deeper and come back.
//
// Push slides a screen in from the right over the one before, and Pop slides the top one off to the right, back to
// the one below. A finger that lands within StackEdge of the left edge and drags right pulls the top screen off as it
// moves, and the screen below slides in after it. Let go, the screen springs on with the finger's speed: back into
// place if it went less than a third of the way slowly, and off otherwise. Screens come and go on Settle. The screens
// below the top one keep their state, as what was typed in them.
//
// Escape, Alt+Left and a mouse's back button go back too, as the swipe does, when what has the keyboard leaves
// them. Going back by any of these runs OnPop; Pop,
// the app's own, does not.
type Stack struct {
	anim.Group
	// OnPop runs as the user takes the top screen off: by the swipe, a key, or a screen reader.
	OnPop func(u *gunim.UI) gunim.Intent

	screens []*stackFrame
	// gone are screens on their way off, which the engine holds until they have slid away.
	gone []*stackFrame
	// drag follows a swipe from the edge: whether one is under way, where it started and the finger's trail.
	drag struct {
		on, armed bool
		from      float32
		start     float32
		trail     anim.Trail
	}
	// width is the stack's width, and band the width of the band along its left edge that the swipe starts in, at
	// the last layout.
	width, band float32
	laid        bool
	// backName is StackBack in the theme of the last layout.
	backName string
	// z is the order the next screen pushed takes.
	z uint64
}

// NewStack returns a stack showing first, which arrives with no motion.
func NewStack(first gunim.Node) *Stack {
	s := &Stack{}
	if first != nil {
		s.screens = []*stackFrame{newStackFrame(first, 0)}
	}
	return s
}

// Top returns the screen in front, or nil.
func (s *Stack) Top() gunim.Node {
	if f := s.front(); f != nil {
		return f.child
	}
	return nil
}

// Len returns how many screens the stack holds.
func (s *Stack) Len() int { return len(s.screens) }

// front is the frame in front, or nil.
func (s *Stack) front() *stackFrame {
	if len(s.screens) == 0 {
		return nil
	}
	return s.screens[len(s.screens)-1]
}

// Push slides screen in over the top one, and sends no intent. Before the stack is mounted, or with a nil u, it
// shows at once. A Push in the frame of a Pop replaces the screen popped: as a PIN pad gives way to what its PIN
// opens, the screen popped stays where it is, and the new one slides in over it.
func (s *Stack) Push(screen gunim.Node, u *gunim.UI) {
	if screen == nil {
		return
	}
	f := newStackFrame(screen, 1)
	s.z++
	f.z = s.z
	s.endDrag(u)
	inside := u != nil && u.HasFocus(s)
	s.screens = append(s.screens, f)
	if u == nil || !s.laid {
		f.off.Jump(0)
		return
	}
	// A screen popped in this same frame, as a PIN pad dropped once its PIN is right, is replaced: it stays where it
	// is, and the new screen slides in over it, as over any other.
	for _, g := range s.gone {
		if g.replaced == nil && g.poppedAt.Equal(u.Now()) {
			g.off.Jump(g.off.Value())
			g.replaced = func() bool {
				return !slices.Contains(s.screens, f) || f.off.Value() < 0.001 && !f.off.Active()
			}
		}
	}
	u.Insert(s, f)
	u.Cue(gunim.CueOpen, f)
	f.off.Animate(0, Settle.Get(u.Theme()))
	// The keyboard follows into the new screen, where it was in the stack.
	f.focus = inside
	u.Invalidate()
}

// Pop slides the top screen off, back to the one below, and sends no intent. It reports false, doing nothing, when
// only one screen is left. Before the stack is mounted, or with a nil u, the screen goes at once.
func (s *Stack) Pop(u *gunim.UI) bool {
	if len(s.screens) < 2 {
		return false
	}
	s.endDrag(u)
	f := s.front()
	s.screens = s.screens[:len(s.screens)-1]
	if u == nil || !s.laid {
		return true
	}
	f.poppedAt = u.Now()
	s.gone = append(s.gone, f)
	u.Cue(gunim.CueClose, f)
	moveFocus := u.HasFocus(f)
	u.Remove(f)
	if moveFocus {
		s.front().focus = true
	}
	u.Invalidate()
	return true
}

// back goes back for the user, and runs OnPop.
func (s *Stack) back(u *gunim.UI) bool {
	if !s.Pop(u) {
		return false
	}
	act0(u, s, gunim.CueClose, s.OnPop)
	return true
}

// Children implements [gunim.Composite]: every screen, bottom first, then those going.
func (s *Stack) Children() []gunim.Node {
	out := make([]gunim.Node, 0, len(s.screens))
	for _, f := range s.screens {
		out = append(out, f)
	}
	return out
}

// ClaimsPointer implements [gunim.PointerClaimer]: the band along the left edge, for the swipe back, while there is a
// screen to go back to.
func (s *Stack) ClaimsPointer(p geom.Point) bool {
	return len(s.screens) > 1 && p.X >= 0 && p.X < s.band
}

// DragsTouch implements [gunim.TouchDragger]: a finger that landed in the edge band drags the top screen.
func (s *Stack) DragsTouch() bool { return s.drag.armed }

// Handle implements [gunim.Handler]: the swipe from the edge, and the keys that go back.
func (s *Stack) Handle(e input.Event, u *gunim.UI) bool {
	th := u.Theme()
	switch e := e.(type) {
	case input.PointerDown:
		if e.Button != input.ButtonPrimary || len(s.screens) < 2 || e.Pos.X < 0 || e.Pos.X >= s.band {
			return false
		}
		s.drag.armed, s.drag.on = true, false
		s.drag.start = e.Pos.X
		s.drag.from = s.front().off.Value()
		s.drag.trail = anim.Trail{}
		s.drag.trail.Add(geom.Pt(e.Pos.X, 0), e.Time)
		return true
	case input.PointerMove:
		if !s.drag.armed || s.width <= 0 {
			return false
		}
		s.drag.on = true
		s.drag.trail.Add(geom.Pt(e.Pos.X, 0), e.Time)
		s.front().off.Jump(min(max(s.drag.from+(e.Pos.X-s.drag.start)/s.width, 0), 1))
		u.Invalidate()
		return true
	case input.PointerUp:
		if !s.drag.armed || e.Button != input.ButtonPrimary {
			return false
		}
		s.drag.armed = false
		top := s.front()
		v := s.drag.trail.Velocity(e.Time).X / max(s.width, 1)
		off := top.off.Value()
		// A press let go at Away was taken from the finger, as by a long press or the window losing it: the swipe
		// is called off, and the screen springs back.
		goes := s.drag.on && !away(e.Pos) && (off >= 1.0/3 || v > stackFling) && v > -stackFling
		s.drag.on = false
		// The screen springs on with the finger's speed, whichever way it goes.
		anim.Fling(top.off, v, anim.Decay{})
		if goes {
			top.off.Retarget(1, Settle.Get(th))
			s.back(u)
		} else {
			top.off.Retarget(0, Settle.Get(th))
		}
		u.Invalidate()
		return true
	case input.KeyPress:
		if e.Key == input.KeyLeft && e.Mods == input.ModAlt || e.Key == input.KeyEscape && e.Mods == 0 {
			return s.back(u)
		}
	case input.HistoryStep:
		if !e.Forward {
			return s.back(u)
		}
	}
	return false
}

// endDrag stops a swipe under way, the top screen springing back into place.
func (s *Stack) endDrag(u *gunim.UI) {
	if !s.drag.armed {
		return
	}
	s.drag.armed, s.drag.on = false, false
	if top := s.front(); top != nil && u != nil {
		top.off.Retarget(0, Settle.Get(u.Theme()))
	}
}

// Layout implements [gunim.Node]: every screen drawn this frame at the stack's size, each placed where it slides.
func (s *Stack) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	if !s.laid {
		s.laid = true
		for _, fr := range s.screens {
			fr.off.Jump(0)
		}
	}
	size := fillOr(c, geom.Sz(400, 300))
	s.width = size.W
	s.band = min(StackEdge.Get(f.Theme), size.W/4)
	s.backName = StackBack.Get(f.Theme)
	live := map[*stackFrame]bool{}
	tight := gunim.Tight(size)
	for k := range kids.All {
		fr, ok := k.Node().(*stackFrame)
		if !ok {
			continue
		}
		live[fr] = true
		if s.shows(fr) {
			k.Layout(tight)
			k.Place(geom.Point{})
		}
	}
	// Those the engine has let go of are gone for good.
	s.gone = slices.DeleteFunc(s.gone, func(g *stackFrame) bool { return !live[g] })
	if top := s.front(); top != nil && top.focus {
		top.focus = false
		if u := f.UI(); u != nil {
			u.FocusFirstLaidOut(top)
		}
	}
	return size
}

// layered returns the screens drawn this frame, bottom first: the highest one resting in its place, and every one
// above it, coming, going or swiped. Those under the one resting are hidden by it.
func (s *Stack) layered() []*stackFrame {
	all := make([]*stackFrame, 0, len(s.screens)+len(s.gone))
	all = append(append(all, s.screens...), s.gone...)
	slices.SortFunc(all, func(a, b *stackFrame) int { return cmp.Compare(a.z, b.z) })
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].off.Value() < 0.001 {
			return all[i:]
		}
	}
	return all
}

// shows reports whether fr is drawn this frame.
func (s *Stack) shows(fr *stackFrame) bool { return slices.Contains(s.layered(), fr) }

// Paint implements [gunim.Node]: the screens drawn, bottom first, each cut off where the one above it starts, so
// none shows through another as it slides. The one at the bottom slides left as it is uncovered, a third as fast,
// and lies under the scrim as far as it is covered. Where a screen draws nothing, the window's own background shows,
// which is drawn under every frame.
func (s *Stack) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	layers := s.layered()
	child := func(fr *stackFrame) (gunim.Child, bool) {
		for k := range kids.All {
			if k.Node() == gunim.Node(fr) {
				return k, true
			}
		}
		return gunim.Child{}, false
	}
	if len(layers) == 1 {
		if k, ok := child(layers[0]); ok {
			k.Paint(p)
		}
		return
	}
	// The screens slide past the stack's edges, and are cut off there; a clip to a rectangle costs no layer.
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Clip: true, Opacity: 1})()
	for i, fr := range layers {
		k, ok := child(fr)
		if !ok {
			continue
		}
		left, right := min(max(fr.off.Value(), 0), 1)*box.W, box.W
		for _, above := range layers[i+1:] {
			right = min(right, min(max(above.off.Value(), 0), 1)*box.W)
		}
		if i == 0 {
			left = 0
		}
		if right-left < 0.5 {
			continue
		}
		strip := geom.Rect{Min: geom.Pt(left, 0), Max: geom.Pt(right, box.H)}
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: strip, Clip: true, Opacity: 1})()
			if i > 0 {
				k.Paint(p)
				return
			}
			// The bottom one, as covered as the strip left of the screens over it is narrow.
			t := 1 - right/box.W
			func() {
				defer p.Push(paint.Translate(geom.Pt(-stackBelow*t*box.W, 0)))()
				k.Paint(p)
			}()
			if scrim := Scrim.Get(f.Theme); t > 0.001 {
				scrim.A = uint8(float32(scrim.A)*t + 0.5)
				p.RRect(strip, 0, paint.Solid(scrim))
			}
		}()
	}
}

// Access implements [gunim.Accessible]: a group that can go back while it holds more than one screen.
func (s *Stack) Access() access.Info {
	info := access.Info{Role: access.RoleGroup}
	if len(s.screens) > 1 {
		info.Description = s.backName
		if info.Description == "" {
			info.Description = StackBack.Default()
		}
		info.Actions = []string{access.ActionClose}
	}
	return info
}

// AccessAct implements [gunim.AccessActor]: close goes back.
func (s *Stack) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionClose {
		return false
	}
	return s.back(u)
}

// stackFrame holds one screen of a [Stack], slid off to the right by off widths.
type stackFrame struct {
	anim.Group
	child gunim.Node
	off   *anim.Float
	// focus says the keyboard is to go into the screen once it is laid out.
	focus bool
	// z orders the screens, those going among them: a screen pushed lies over every one pushed before it.
	z uint64
	// poppedAt is the frame's time a screen was popped at; replaced, where set, says a screen popped in the frame
	// a new one was pushed stays where it is under the new one, until it reports the new one has covered it.
	poppedAt time.Time
	replaced func() bool
}

func newStackFrame(child gunim.Node, off float32) *stackFrame {
	f := &stackFrame{child: child, off: anim.NewFloat(off)}
	f.Add(f.off)
	return f
}

// Transition implements [gunim.Transitioner]: a screen going slides off to the right on Settle, and the engine
// holds it until it has. One replaced stays where it is until the screen that replaced it has covered it.
func (f *stackFrame) Transition(p gunim.Presence, fr gunim.Frame) bool {
	if p == gunim.Exiting && f.replaced != nil {
		return f.replaced()
	}
	if p == gunim.Exiting {
		f.off.Animate(1, Settle.Get(fr.Theme))
		return f.off.Value() >= 0.999 || !f.off.Active()
	}
	return true
}

// Children implements [gunim.Composite].
func (f *stackFrame) Children() []gunim.Node { return []gunim.Node{f.child} }

// Layout implements [gunim.Node]: the screen, slid off by its share of the width.
func (f *stackFrame) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	size := k.Layout(c)
	k.Place(geom.Pt(min(max(f.off.Value(), 0), 1)*size.W, 0))
	return size
}

// Paint implements [gunim.Node]. The screen it slides over is cut off where it starts, so what it leaves clear shows
// the window's background; see [Stack.Paint].
func (f *stackFrame) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	if f.off.Value() >= 0.999 || kids.Len() == 0 {
		return
	}
	kids.At(0).Paint(p)
}
