package widget

import (
	"image/color"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// grab holds a node and keeps the UI its frames are laid out in, for a test to call setters with.
type grab struct {
	child gunim.Node
	ui    *gunim.UI
}

func (g *grab) Children() []gunim.Node { return []gunim.Node{g.child} }

func (g *grab) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	g.ui = f.UI()
	k := kids.At(0)
	s := k.Layout(c)
	k.Place(geom.Point{})
	return s
}

func (g *grab) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// touchStage mounts root under the touch theme in a window that keeps time by its frames, and returns the window,
// the UI and a function that runs n frames of 1/60 s.
func touchStage(t *testing.T, root gunim.Node, size geom.Size) (*gunim.Window, *grab, func(int)) {
	t.Helper()
	g := &grab{child: NewThemed(root, Dark().With(Touch()...))}
	w, run := stage(t, &frame{child: g, size: size})
	w.TimeByFrames(true)
	run(1)
	return w, g, run
}

// touchDown and touchUp land a finger at p and lift it, as a touch driver sends them: a move to the finger before
// it presses, and the pointer leaving as it lifts.
func touchDown(w *gunim.Window, p geom.Point) {
	w.Input(input.PointerMove{Pos: p, Touch: true, Time: time.Now()})
	w.Input(input.PointerDown{Pos: p, Button: input.ButtonPrimary, Clicks: 1, Touch: true, Time: time.Now()})
}

func touchUp(w *gunim.Window, p geom.Point) {
	w.Input(input.PointerUp{Pos: p, Button: input.ButtonPrimary, Touch: true, Time: time.Now()})
	w.Input(input.PointerLeave{Time: time.Now()})
}

// The touch theme sets text in Inter, makes the targets at least 48 tall and turns tooltips off.
func TestTheTouchThemeFollowsTheDesignLanguage(t *testing.T) {
	th := theme.NewLive(Dark().With(Touch()...))
	if Font.Get(th) != text.Inter(false) || BoldFont.Get(th) != text.Inter(true) {
		t.Fatal("the touch theme sets text in another face than Inter")
	}
	for _, tok := range []theme.Token[float32]{ButtonHeight, FieldHeight, ControlHeight, MenuRowHeight, TabHeight,
		SegmentedHeight, ChipHeight, NavRowHeight, PinKeyLeast, KeyboardKey} {
		if v := tok.Get(th); v < 48 {
			t.Errorf("%s is %v under the touch theme, want 48 or more", tok.Key(), v)
		}
	}
	if Tooltips.Get(th) {
		t.Error("the touch theme leaves tooltips on")
	}
	if got := [4]float32{FigureSize.Get(th), HeadingSize.Get(th), TextSize.Get(th), CaptionSize.Get(th)}; got != [4]float32{40, 24, 17, 14} {
		t.Errorf("the four sizes of text are %v, want 40, 24, 17 and 14", got)
	}
}

// A finger lights and squashes a button on the frame it lands, and a glow spreads from it; lifted, the light fades.
func TestAFingerLightsAButtonOnTheFrameItLands(t *testing.T) {
	b := NewButton("Water now")
	w, _, run := touchStage(t, Row(b), geom.Sz(400, 100))
	at := geom.Pt(20, 20)
	touchDown(w, at)
	run(1)
	if b.hover.Value() <= 0 || b.squash.Value() <= 0 || b.touch.shine.Value() <= 0 {
		t.Fatalf("one frame after the finger landed: light %v, squash %v, glow %v; want all on their way up",
			b.hover.Value(), b.squash.Value(), b.touch.shine.Value())
	}
	if b.touch.at != at {
		t.Fatalf("the glow spreads from %v, want where the finger landed, %v", b.touch.at, at)
	}
	touchUp(w, at)
	run(60)
	if b.hover.Value() > 0.01 || b.touch.shine.Value() > 0.01 {
		t.Fatalf("lifted, the light is at %v and the glow at %v, want both gone", b.hover.Value(), b.touch.shine.Value())
	}
}

// A mouse's press squashes a button, as before, and spreads no glow.
func TestAMousePressSpreadsNoGlow(t *testing.T) {
	b := NewButton("Water now")
	w, _, run := touchStage(t, Row(b), geom.Sz(400, 100))
	w.Input(input.PointerDown{Pos: geom.Pt(20, 20), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	run(3)
	if b.squash.Value() <= 0 || b.touch.shine.Target() != 0 {
		t.Fatalf("a mouse press: squash %v, glow heading to %v; want a squash and no glow", b.squash.Value(), b.touch.shine.Target())
	}
}

// longColumn returns a column of buttons too tall for a window of 300, and its first button.
func longColumn() (gunim.Node, *Button) {
	kids := make([]gunim.Node, 0, 20)
	var first *Button
	for i := range 20 {
		b := NewButton("Row")
		if i == 0 {
			first = b
		}
		kids = append(kids, b)
	}
	return NewScroll(Column(kids...)), first
}

// Inside something that scrolls, a finger's press waits up to 80 ms before it shows, and one that goes on to scroll
// never lights the button it started on, on any frame.
func TestAFingerThatScrollsLightsNothing(t *testing.T) {
	col, b := longColumn()
	w, _, run := touchStage(t, col, geom.Sz(300, 300))
	at := geom.Pt(30, 20)
	touchDown(w, at)
	for i := range 3 {
		run(1)
		if b.hover.Value() != 0 || b.squash.Value() != 0 {
			t.Fatalf("frame %d, %d ms after landing in a scroll: light %v, squash %v; want none yet", i, (i+1)*16,
				b.hover.Value(), b.squash.Value())
		}
	}
	for i := range 6 {
		w.Input(input.PointerMove{Pos: geom.Pt(at.X, at.Y+float32(i+1)*10), Touch: true, Time: time.Now()})
		run(1)
		if b.hover.Value() != 0 || b.squash.Value() != 0 || b.touch.shine.Value() != 0 {
			t.Fatalf("frame %d of the scroll: light %v, squash %v, glow %v; want none", i, b.hover.Value(),
				b.squash.Value(), b.touch.shine.Value())
		}
	}
	touchUp(w, geom.Pt(at.X, at.Y+60))
	for i := range 30 {
		run(1)
		if b.hover.Value() != 0 || b.touch.shine.Value() != 0 {
			t.Fatalf("frame %d after the scroll: light %v, glow %v; want none", i, b.hover.Value(), b.touch.shine.Value())
		}
	}
}

// Inside something that scrolls, a finger held still lights the button once the wait is over, and a tap quicker
// than the wait still glows on it for a moment, and clicks it.
func TestAFingerHeldStillInAScrollLightsAfterTheWait(t *testing.T) {
	col, b := longColumn()
	clicks := 0
	b.OnClick = func(*gunim.UI) gunim.Intent { clicks++; return nil }
	w, _, run := touchStage(t, col, geom.Sz(300, 300))
	at := geom.Pt(30, 20)
	touchDown(w, at)
	run(7)
	if b.hover.Target() != 1 || b.squash.Target() != 1 {
		t.Fatalf("held still past the wait: light heading to %v, squash to %v; want both lit", b.hover.Target(), b.squash.Target())
	}
	touchUp(w, at)
	run(60)

	touchDown(w, at)
	run(1)
	touchUp(w, at)
	lit := false
	for range 50 {
		run(1)
		lit = lit || b.touch.shine.Value() > 0.3
	}
	if !lit {
		t.Fatal("a quick tap in a scroll never lit the button")
	}
	if b.hover.Value() > 0.01 || b.touch.shine.Value() > 0.01 {
		t.Fatalf("shortly after a quick tap the light is at %v and the glow at %v, want both gone",
			b.hover.Value(), b.touch.shine.Value())
	}
	if clicks != 2 {
		t.Fatalf("two taps clicked %d times, want 2", clicks)
	}
}

// Under the touch theme a pointer resting on a control opens no tooltip.
func TestTheTouchThemeShowsNoTooltip(t *testing.T) {
	b := NewButton("Water")
	b.Tooltip = "Waters the bed now"
	w, _, run := touchStage(t, Row(b), geom.Sz(300, 100))
	w.Input(input.PointerMove{Pos: geom.Pt(20, 20), Time: time.Now()})
	run(90)
	if b.tip.popup != nil || b.tip.stop != nil {
		t.Fatal("a tooltip opened, or waits to, under the touch theme")
	}
}

// A hold button acts once it has been held for Hold, and only once however long it is held.
func TestAHoldButtonActsOnceHeldLongEnough(t *testing.T) {
	b := NewHoldButton("Revoke laptop")
	b.OnClick = Sends(pressed{1})
	w, _, run := touchStage(t, Row(b), geom.Sz(400, 100))
	at := geom.Pt(30, 24)
	touchDown(w, at)
	last := float32(0)
	for i := range 59 {
		run(1)
		if p := b.Progress(); p < last-1e-4 {
			t.Fatalf("frame %d: the ring went back from %v to %v while held", i, last, p)
		}
		last = b.Progress()
	}
	if got := sent(w); len(got) != 0 {
		t.Fatalf("held just short of a second, it sent %v", got)
	}
	run(60)
	if got := sent(w); len(got) != 1 || got[0] != (pressed{1}) {
		t.Fatalf("held two seconds, it sent %v, want one intent", got)
	}
	touchUp(w, at)
	run(90)
	if b.Progress() > 0.01 {
		t.Fatalf("let go, the ring stays at %v", b.Progress())
	}
	if got := sent(w); len(got) != 0 {
		t.Fatalf("letting go sent %v", got)
	}
}

// Let go early, a hold button's ring drains back on every frame and nothing happens; pressed again before it has
// drained, it fills on from where it is, and acts sooner than a full hold.
func TestAHoldLetGoEarlyDrainsAndResumes(t *testing.T) {
	b := NewHoldButton("Remove guest")
	b.OnClick = Sends(pressed{2})
	w, _, run := touchStage(t, Row(b), geom.Sz(400, 100))
	at := geom.Pt(30, 24)
	touchDown(w, at)
	run(36)
	half := b.Progress()
	if half < 0.5 || half > 0.7 {
		t.Fatalf("after 0.6 s the ring is at %v, want about 0.6", half)
	}
	touchUp(w, at)
	last := b.Progress()
	for i := range 6 {
		run(1)
		if p := b.Progress(); p > last+1e-4 {
			t.Fatalf("frame %d after letting go: the ring grew from %v to %v", i, last, p)
		}
		last = b.Progress()
	}
	if last <= 0.05 {
		t.Fatalf("a tenth of a second after letting go the ring is at %v, want it still draining", last)
	}
	touchDown(w, at)
	frames := 0
	for len(sent(w)) == 0 && frames < 120 {
		run(1)
		frames++
	}
	if frames >= 60 {
		t.Fatalf("pressed again from %v, it took %d frames to act, want fewer than a full second's", last, frames)
	}
	touchUp(w, at)
	run(1)
}

// Space held down holds a hold button from the keyboard; let go early, nothing happens.
func TestAHoldButtonHoldsFromTheKeyboard(t *testing.T) {
	b := NewHoldButton("Revoke laptop")
	b.OnClick = Sends(pressed{3})
	w, g, run := touchStage(t, Row(b), geom.Sz(400, 100))
	g.ui.Focus(b)
	w.Input(input.KeyPress{Key: input.KeySpace})
	run(30)
	w.Input(input.KeyRelease{Key: input.KeySpace})
	run(30)
	if got := sent(w); len(got) != 0 {
		t.Fatalf("Space held half a second sent %v", got)
	}
	w.Input(input.KeyPress{Key: input.KeySpace})
	for range 20 {
		w.Input(input.KeyPress{Key: input.KeySpace, Repeat: true})
		run(4)
	}
	w.Input(input.KeyRelease{Key: input.KeySpace})
	run(1)
	if got := sent(w); len(got) != 1 {
		t.Fatalf("Space held over a second sent %v, want one intent", got)
	}
}

// A hold button tells a screen reader it is a button and its name, and asks no hold of it: a press from one acts.
func TestAHoldButtonSpeaksToAScreenReader(t *testing.T) {
	b := NewHoldButton("Revoke laptop")
	b.OnClick = Sends(pressed{4})
	w, g, run := touchStage(t, Row(b), geom.Sz(400, 100))
	info := b.Access()
	if info.Role != access.RoleButton || info.Name != "Revoke laptop" || info.Description != "" {
		t.Fatalf("access %+v, want a plain button named Revoke laptop, asking for no hold", info)
	}
	if !b.AccessAct(access.Request{Part: -1, Action: access.ActionPress}, g.ui) {
		t.Fatal("a screen reader's press was refused")
	}
	run(1)
	if got := sent(w); len(got) != 1 {
		t.Fatalf("a screen reader's press sent %v, want one intent", got)
	}
	b.Disabled = true
	run(2)
	if b.Access().State&access.StateDisabled == 0 || b.AccessAct(access.Request{Part: -1, Action: access.ActionPress}, g.ui) {
		t.Fatal("a disabled hold button reads as enabled, or acts")
	}
}

// pinStage mounts a PIN pad of six digits and returns it with its window, UI and frames.
func pinStage(t *testing.T) (*PinPad, *gunim.Window, *grab, func(int)) {
	t.Helper()
	p := NewPinPad(6, "Unlock")
	w, g, run := touchStage(t, p, geom.Sz(360, 420))
	return p, w, g, run
}

// tapKey taps the PIN pad's key i with a finger.
func tapKey(w *gunim.Window, run func(int), p *PinPad, i int) {
	c := p.keys[i].Center()
	touchDown(w, c)
	run(1)
	touchUp(w, c)
	run(1)
}

// pinKey is the index of the key that types digit d.
func pinKey(d int) int {
	if d == 0 {
		return pinZero
	}
	return d - 1
}

// Tapped digits fill dots, delete takes the last one off, and the commit key hands the PIN over once and clears it.
func TestAPinPadHandsThePinOverOnceAndClearsIt(t *testing.T) {
	p, w, _, run := pinStage(t)
	var got []string
	var handed []byte
	p.OnCommit = func(pin []byte, _ *gunim.UI) gunim.Intent {
		got, handed = append(got, string(pin)), pin
		return nil
	}
	first := &p.digits[:1][0]
	for _, d := range []int{9, 7, 3, 1, 5} {
		tapKey(w, run, p, pinKey(d))
	}
	tapKey(w, run, p, pinDelete)
	if p.Len() != 4 {
		t.Fatalf("five digits and a delete leave %d digits, want 4", p.Len())
	}
	run(30)
	for i, d := range p.dots {
		if want := float32(0); i < 4 {
			if v := d.Value(); v < 0.95 {
				t.Fatalf("dot %d is %v full with a digit typed, want full", i, v)
			}
		} else if v := d.Value(); v > want+0.01 {
			t.Fatalf("dot %d is %v full with no digit, want empty", i, v)
		}
	}
	tapKey(w, run, p, pinEnter)
	if len(got) != 1 || got[0] != "9731" {
		t.Fatalf("the pad handed over %q, want 9731 once", got)
	}
	if p.Len() != 0 {
		t.Fatalf("after handing it over the pad holds %d digits, want none", p.Len())
	}
	for i, b := range append(handed[:cap(handed)], p.digits[:cap(p.digits)]...) {
		if b != 0 {
			t.Fatalf("byte %d of the PIN is still %q once OnCommit has returned, want wiped", i, b)
		}
	}
	if &p.digits[:1][0] != first {
		t.Fatal("the digits moved to another array as they were typed, leaving a copy behind")
	}
	tapKey(w, run, p, pinEnter)
	if len(got) != 1 {
		t.Fatalf("an empty pad handed over %q", got[1:])
	}
}

// The pad takes no more digits than its Length.
func TestAPinPadStopsAtItsLength(t *testing.T) {
	p, w, _, run := pinStage(t)
	for range 9 {
		tapKey(w, run, p, pinKey(4))
	}
	if p.Len() != 6 {
		t.Fatalf("nine taps typed %d digits on a pad of 6", p.Len())
	}
}

// A keyboard's digits, Backspace and Enter work the pad, from the number row and the number pad.
func TestAPinPadTakesAKeyboard(t *testing.T) {
	p, w, g, run := pinStage(t)
	var got string
	p.OnCommit = func(pin []byte, _ *gunim.UI) gunim.Intent { got = string(pin); return nil }
	g.ui.Focus(p)
	for _, k := range []input.Key{input.Key2, input.KeyKP0, input.Key8, input.KeyBackspace, input.Key6} {
		key(w, run, k, 0)
	}
	key(w, run, input.KeyKPEnter, 0)
	if got != "206" {
		t.Fatalf("the keys handed over %q, want 206", got)
	}
	for i, l := range p.lit {
		if l.Value() != 0 || l.Target() != 0 {
			t.Fatalf("key %d on the screen lit for a key typed on a keyboard", i)
		}
	}
}

// The digits typed never reach a screen reader: it hears how many there are, and a part for each key.
func TestAPinPadKeepsItsDigitsFromAScreenReader(t *testing.T) {
	p, w, _, run := pinStage(t)
	for _, d := range []int{9, 7, 3, 1} {
		tapKey(w, run, p, pinKey(d))
	}
	info := p.Access()
	heard := make([]string, 0, 3+2*len(info.Parts))
	heard = append(heard, info.Name, info.Value, info.Description)
	for _, part := range info.Parts {
		heard = append(heard, part.Value, part.Description)
	}
	all := strings.Join(heard, "|")
	if strings.Contains(all, "9731") || strings.Contains(all, "973") {
		t.Fatalf("a screen reader hears the PIN in %q", all)
	}
	if info.Value != "4/6" || len(info.Parts) != pinKeys {
		t.Fatalf("access value %q with %d parts, want 4/6 and a part for each of the %d keys", info.Value, len(info.Parts), pinKeys)
	}
	if info.Parts[pinEnter].Name != "Unlock" || info.Parts[pinDelete].Role != access.RoleButton {
		t.Fatalf("parts %+v, want buttons named by their keys", info.Parts)
	}
}

// After a wrong PIN the dots shake and settle, the pad takes no digits while the ring drains, and takes them again
// once it has.
func TestAPinPadWaitsAfterAWrongPin(t *testing.T) {
	p, w, _, run := pinStage(t)
	p.OnCommit = func(_ []byte, u *gunim.UI) gunim.Intent { p.SetWaiting(500*time.Millisecond, u); return nil }
	for _, d := range []int{1, 2, 3, 4} {
		tapKey(w, run, p, pinKey(d))
	}
	tapKey(w, run, p, pinEnter)
	if !p.Waiting() {
		t.Fatal("the pad does not wait after a wrong PIN")
	}
	if d := p.Access().Description; !strings.Contains(d, "Wrong PIN") || !strings.Contains(d, "1 s") {
		t.Fatalf("waiting, the pad says %q to a screen reader, want why and for how long", d)
	}
	moved, last := false, p.wait.Value()
	for i := range 20 {
		run(1)
		if s := p.shake.Value(); s < -1.5 || s > 1.5 {
			t.Fatalf("frame %d: the dots swing %v dots' widths, want at most 1.5", i, s)
		} else if s > 0.2 || s < -0.2 {
			moved = true
		}
		if v := p.wait.Value(); v > last+1e-4 {
			t.Fatalf("frame %d: the wait's ring grew from %v to %v", i, last, v)
		}
		last = p.wait.Value()
	}
	if !moved {
		t.Fatal("the dots never shook")
	}
	tapKey(w, run, p, pinKey(5))
	if p.Len() != 0 {
		t.Fatal("the pad took a digit while it waited")
	}
	run(40)
	if p.Waiting() {
		t.Fatal("the pad still waits once the wait is over")
	}
	if s := p.shake.Value(); s > 0.01 || s < -0.01 {
		t.Fatalf("the dots rest %v off their place", s)
	}
	tapKey(w, run, p, pinKey(5))
	if p.Len() != 1 {
		t.Fatal("the pad takes no digit once the wait is over")
	}
}

// A wait set before the pad is mounted holds it from the first frame.
func TestAPinPadWaitSetBeforeMountingHolds(t *testing.T) {
	p := NewPinPad(4, "OK")
	p.SetWaiting(time.Second, nil)
	w, _, run := touchStage(t, p, geom.Sz(360, 420))
	if !p.Waiting() || p.dim.Value() != 1 {
		t.Fatalf("waiting %v, keys dimmed %v; want a wait shown from the first frame", p.Waiting(), p.dim.Value())
	}
	tapKey(w, run, p, pinKey(1))
	if p.Len() != 0 {
		t.Fatal("the pad took a digit while it waited")
	}
	run(70)
	if p.Waiting() {
		t.Fatal("the wait set before mounting never ended")
	}
}

// stackStage mounts a stack of one labelled screen in a window 400 wide, and returns it with its window, UI and
// frames.
func stackStage(t *testing.T) (*Stack, *gunim.Window, *grab, func(int)) {
	t.Helper()
	s := NewStack(NewLabel("Home"))
	w, g, run := touchStage(t, s, geom.Sz(400, 300))
	return s, w, g, run
}

// A screen pushed slides in from the right on every frame, and popped slides back out and leaves the tree.
func TestAStackSlidesScreensInAndOut(t *testing.T) {
	s, _, g, run := stackStage(t)
	next := NewLabel("Plants")
	s.Push(next, g.ui)
	run(1)
	top := s.front()
	last := top.off.Value()
	for i := range 60 {
		run(1)
		v := top.off.Value()
		if v > last+1e-4 || v < -0.02 {
			t.Fatalf("frame %d: the screen coming in went from %v to %v", i, last, v)
		}
		last = v
	}
	if last > 0.001 || s.Top() != next {
		t.Fatalf("the screen pushed rests %v of the width off, top %v", last, s.Top())
	}
	if !s.Pop(g.ui) || s.Len() != 1 {
		t.Fatal("Pop left the stack with more than its first screen")
	}
	last = top.off.Value()
	for i := range 90 {
		run(1)
		v := top.off.Value()
		if v < last-1e-4 {
			t.Fatalf("frame %d: the screen going came back from %v to %v", i, last, v)
		}
		last = v
	}
	if _, ok := g.ui.Bounds(next); ok {
		t.Fatal("the screen popped is still in the tree once it has slid away")
	}
	if s.Pop(g.ui) {
		t.Fatal("Pop took the last screen")
	}
}

// swipe drags from x0 to x1 across the stack in steps over d, with a finger or the mouse.
func swipe(w *gunim.Window, run func(int), x0, x1 float32, steps int, d time.Duration, touch bool) {
	t0 := time.Now()
	y := float32(150)
	if touch {
		w.Input(input.PointerMove{Pos: geom.Pt(x0, y), Touch: true, Time: t0})
	}
	w.Input(input.PointerDown{Pos: geom.Pt(x0, y), Button: input.ButtonPrimary, Clicks: 1, Touch: touch, Time: t0})
	run(1)
	for i := 1; i <= steps; i++ {
		x := x0 + (x1-x0)*float32(i)/float32(steps)
		w.Input(input.PointerMove{Pos: geom.Pt(x, y), Touch: touch, Time: t0.Add(d * time.Duration(i) / time.Duration(steps))})
		run(1)
	}
	w.Input(input.PointerUp{Pos: geom.Pt(x1, y), Button: input.ButtonPrimary, Touch: touch, Time: t0.Add(d + time.Millisecond)})
	if touch {
		w.Input(input.PointerLeave{Time: t0.Add(d + time.Millisecond)})
	}
}

// A slow swipe from the edge less than a third of the way springs back; one past a third goes back and runs OnPop.
func TestASwipeFromTheEdgeGoesBackPastAThird(t *testing.T) {
	for _, touch := range []bool{true, false} {
		s, w, g, run := stackStage(t)
		s.OnPop = Sends(pressed{5})
		s.Push(NewLabel("Plants"), g.ui)
		run(60)
		top := s.front()
		swipe(w, run, 6, 106, 10, time.Second, touch)
		run(60)
		if s.Len() != 2 || top.off.Value() > 0.01 {
			t.Fatalf("touch %v: a slow swipe a quarter of the way left %d screens, the top %v off", touch, s.Len(), top.off.Value())
		}
		if got := sent(w); len(got) != 0 {
			t.Fatalf("touch %v: a swipe that sprang back sent %v", touch, got)
		}
		swipe(w, run, 6, 226, 10, time.Second, touch)
		run(1)
		if s.Len() != 1 {
			t.Fatalf("touch %v: a slow swipe past half way left %d screens", touch, s.Len())
		}
		if got := sent(w); len(got) != 1 || got[0] != (pressed{5}) {
			t.Fatalf("touch %v: going back by a swipe sent %v, want OnPop's intent", touch, got)
		}
		run(90)
	}
}

// A quick flick from the edge goes back though it went a short way, and the screen goes on at the finger's speed.
func TestAFlickFromTheEdgeGoesBack(t *testing.T) {
	s, w, g, run := stackStage(t)
	s.Push(NewLabel("Plants"), g.ui)
	run(60)
	top := s.front()
	swipe(w, run, 6, 86, 4, 60*time.Millisecond, true)
	before := top.off.Value()
	run(1)
	if s.Len() != 1 {
		t.Fatalf("a quick flick a fifth of the way left %d screens", s.Len())
	}
	if top.off.Value()-before < 0.02 {
		t.Fatalf("the flicked screen moved %v of the width in a frame, want it going on at speed", top.off.Value()-before)
	}
}

// A drag that starts away from the edge moves no screen.
func TestADragAwayFromTheEdgeLeavesTheStack(t *testing.T) {
	s, w, g, run := stackStage(t)
	s.Push(NewLabel("Plants"), g.ui)
	run(60)
	swipe(w, run, 120, 380, 10, 300*time.Millisecond, false)
	run(30)
	if s.Len() != 2 || s.front().off.Value() != 0 {
		t.Fatalf("a drag from the middle left %d screens, the top %v off", s.Len(), s.front().off.Value())
	}
}

// Alt+Left goes back, and a screen reader's close does too.
func TestAStackGoesBackFromKeysAndAScreenReader(t *testing.T) {
	s, w, g, run := stackStage(t)
	b := NewButton("Bed one")
	s.Push(b, g.ui)
	s.Push(NewLabel("Deeper"), g.ui)
	run(60)
	g.ui.Focus(b)
	key(w, run, input.KeyLeft, input.ModAlt)
	if s.Len() != 2 {
		t.Fatalf("Alt+Left left %d screens, want 2", s.Len())
	}
	if info := s.Access(); len(info.Actions) != 1 || info.Actions[0] != access.ActionClose {
		t.Fatalf("access %+v, want an action that goes back", info)
	}
	if !s.AccessAct(access.Request{Part: -1, Action: access.ActionClose}, g.ui) || s.Len() != 1 {
		t.Fatal("a screen reader's close did not go back")
	}
	run(60)
	if len(s.Access().Actions) != 0 {
		t.Fatal("with one screen left the stack still offers to go back")
	}
}

// keyboardStage mounts a text field over a keyboard, and returns them with the window, UI and frames.
func keyboardStage(t *testing.T) (*Keyboard, *TextField, *gunim.Window, *grab, func(int)) {
	t.Helper()
	f := NewTextField()
	k := NewKeyboard(Column(f))
	w, g, run := touchStage(t, k, geom.Sz(600, 400))
	return k, f, w, g, run
}

// kbIndex returns the index of the key that types s, or of the first key of kind.
func kbIndex(t *testing.T, k *Keyboard, s string, kind keyKind) int {
	t.Helper()
	for i, key := range k.keys {
		if key.kind == kind && (kind != keyChar || key.text == s) {
			return i
		}
	}
	t.Fatalf("no key %q of kind %d", s, kind)
	return -1
}

// tapKb taps the keyboard's key i with a finger.
func tapKb(w *gunim.Window, run func(int), k *Keyboard, i int) {
	c := k.rects[i].Center()
	touchDown(w, c)
	run(1)
	touchUp(w, c)
	run(1)
}

// The keyboard slides up on every frame as a field takes the focus, and down as it leaves.
func TestAKeyboardSlidesUpForAField(t *testing.T) {
	k, f, _, g, run := keyboardStage(t)
	if k.Shown() || k.top != 400 {
		t.Fatalf("with nothing focused the keyboard shows, its top at %v", k.top)
	}
	g.ui.Focus(f)
	last := k.top
	for i := range 60 {
		run(1)
		if k.top > last+0.01 {
			t.Fatalf("frame %d: the keyboard went down from %v to %v as it opened", i, last, k.top)
		}
		last = k.top
	}
	if !k.Shown() || last > 400-k.height+0.5 {
		t.Fatalf("open, the keyboard's top is at %v, want %v", last, 400-k.height)
	}
	for i, r := range k.rects {
		if r.Min.X < 0 || r.Max.X > 600 || r.Max.Y > 400 {
			t.Fatalf("key %d lies at %v, outside the keyboard's 600 by 400", i, r)
		}
		if r.Size().H < KeyboardKey.Get(theme.NewLive(Dark().With(Touch()...))) {
			t.Fatalf("key %d is %v tall", i, r.Size().H)
		}
	}
	g.ui.Focus(nil)
	run(60)
	if k.Shown() || k.top < 399.5 {
		t.Fatalf("with the focus gone the keyboard's top is at %v", k.top)
	}
}

// Keys type into the focused field through its text edits: Shift makes a capital for one letter, Delete takes the
// last character off, and the second page types digits.
func TestAKeyboardTypesIntoTheField(t *testing.T) {
	k, f, w, g, run := keyboardStage(t)
	var committed string
	f.OnCommit = func(s string, _ *gunim.UI) gunim.Intent { committed = s; return nil }
	g.ui.Focus(f)
	run(60)
	tapKb(w, run, k, kbIndex(t, k, "", keyShift))
	for _, s := range []string{"h", "i", "x"} {
		tapKb(w, run, k, kbIndex(t, k, s, keyChar))
	}
	tapKb(w, run, k, kbIndex(t, k, "", keyDelete))
	tapKb(w, run, k, kbIndex(t, k, " ", keySpace))
	tapKb(w, run, k, kbIndex(t, k, "", keyPage))
	tapKb(w, run, k, kbIndex(t, k, "4", keyChar))
	tapKb(w, run, k, kbIndex(t, k, "2", keyChar))
	if got := f.Text(); got != "Hi 42" {
		t.Fatalf("the field holds %q, want %q", got, "Hi 42")
	}
	if g.ui.Focused() != gunim.Node(f) {
		t.Fatal("typing on the keyboard took the focus from the field")
	}
	tapKb(w, run, k, kbIndex(t, k, "", keyEnter))
	if committed != "Hi 42" {
		t.Fatalf("Enter committed %q, want the field's text", committed)
	}
}

// The hide key takes the keyboard down, and the focus with it.
func TestAKeyboardHides(t *testing.T) {
	k, f, w, g, run := keyboardStage(t)
	g.ui.Focus(f)
	run(60)
	tapKb(w, run, k, kbIndex(t, k, "", keyHide))
	run(60)
	if k.Shown() || g.ui.Focused() != nil {
		t.Fatalf("after the hide key the keyboard shows %v, the focus is on %v", k.Shown(), g.ui.Focused())
	}
}

// A screen reader hears a part for each key while the keyboard is up, named for what it does, and presses them.
func TestAKeyboardSpeaksToAScreenReader(t *testing.T) {
	k, f, _, g, run := keyboardStage(t)
	if parts := k.Access().Parts; len(parts) != 0 {
		t.Fatalf("down, the keyboard offers %d keys", len(parts))
	}
	g.ui.Focus(f)
	run(60)
	info := k.Access()
	if len(info.Parts) != len(k.keys) {
		t.Fatalf("up, the keyboard offers %d keys, want %d", len(info.Parts), len(k.keys))
	}
	names := map[string]bool{}
	for _, p := range info.Parts {
		names[p.Name] = true
	}
	for _, want := range []string{"q", "Shift", "Delete", "Enter", "Space", "Hide keyboard"} {
		if !names[want] {
			t.Errorf("no key is named %q", want)
		}
	}
	if !k.AccessAct(access.Request{Part: kbIndex(t, k, "q", keyChar), Action: access.ActionPress}, g.ui) {
		t.Fatal("a screen reader's press on q was refused")
	}
	run(1)
	if f.Text() != "q" {
		t.Fatalf("a screen reader's press on q typed %q", f.Text())
	}
}

// The kit's widgets draw inside the box they were laid out at, with a focus ring's room round it, while they move:
// a hold half full, a PIN pad shaking, a keyboard part way up, and a screen sliding into a stack.
func TestTheTouchKitDrawsInsideItsBox(t *testing.T) {
	hold := NewHoldButton("Revoke laptop")
	hold.Icon = nil
	pad := NewPinPad(6, "Unlock")
	f := NewTextField()
	kb := NewKeyboard(Column(f))
	st := NewStack(NewLabel("Home"))
	w, g, run := touchStage(t, Column(Row(hold), pad, NewSized(kb, 600, 320), NewSized(st, 600, 100)), geom.Sz(600, 1000))
	st.Push(NewLabel("Plants"), g.ui)
	th := theme.NewLive(Dark().With(Touch()...))
	touchDown(w, geom.Pt(20, 20))
	pad.OnCommit = func(_ []byte, u *gunim.UI) gunim.Intent { pad.SetWaiting(time.Second, u); return nil }
	g.ui.Focus(f)
	for i := range 40 {
		run(1)
		if i == 5 {
			pad.press(0, g.ui)
			pad.press(pinEnter, g.ui)
		}
		for name, n := range map[string]gunim.Node{"hold button": hold, "PIN pad": pad, "keyboard": kb, "stack": st} {
			b, ok := g.ui.Bounds(n)
			if !ok {
				t.Fatalf("the %s is not laid out", name)
			}
			box := b.Size()
			if out := spills(paintedIn(n, box, th), box, 3); len(out) > 0 {
				t.Fatalf("frame %d: the %s draws outside its box %v: %v", i, name, box, out)
			}
		}
	}
	touchUp(w, geom.Pt(20, 20))
	run(1)
}

// A hold that acted leaves nothing to carry on: a press soon after starts from empty, and needs a full Hold again.
func TestAHoldButtonActsOnceForEachFullHold(t *testing.T) {
	b := NewHoldButton("Revoke laptop")
	b.OnClick = Sends(pressed{6})
	w, _, run := touchStage(t, Row(b), geom.Sz(400, 100))
	at := geom.Pt(30, 24)
	touchDown(w, at)
	run(64)
	touchUp(w, at)
	run(3)
	if got := sent(w); len(got) != 1 {
		t.Fatalf("one full hold sent %v, want one intent", got)
	}
	touchDown(w, at)
	run(1)
	if p := b.Progress(); p > 0.05 {
		t.Fatalf("pressed again 50 ms after acting, the button starts %v full, want empty", p)
	}
	run(30)
	if got := sent(w); len(got) != 0 {
		t.Fatalf("half a second into the next hold it sent %v, want nothing yet", got)
	}
	run(34)
	if got := sent(w); len(got) != 1 {
		t.Fatalf("a second full hold sent %v, want one intent", got)
	}
	touchUp(w, at)
	run(1)
}

// The button fills from its start to its end as it is held, and shows a tick once it has acted, until let go.
func TestAHoldButtonFillsAndShowsItIsDone(t *testing.T) {
	b := NewHoldButton("Remove guest")
	w, _, run := touchStage(t, Row(b), geom.Sz(400, 100))
	at := geom.Pt(30, 24)
	touchDown(w, at)
	run(30)
	th := theme.NewLive(Dark().With(Touch()...))
	box := b.size
	var clips []float32
	for _, op := range paintedIn(b, box, th) {
		if l, ok := op.(*paint.LayerOp); ok && l.Opts.Clip && l.Opts.Bounds.Min.X == 0 {
			clips = append(clips, l.Opts.Bounds.Max.X)
		}
	}
	if len(clips) == 0 || clips[0] < box.W*0.4 || clips[0] > box.W*0.6 {
		t.Fatalf("half way through the hold the fill reaches %v of %v, want about half", clips, box.W)
	}
	run(40)
	if b.done.Value() < 0.9 {
		t.Fatalf("held past a second, the tick shows %v, want it in", b.done.Value())
	}
	touchUp(w, at)
	run(90)
	if b.done.Value() > 0.01 || b.Progress() > 0.01 {
		t.Fatalf("let go, the tick shows %v and the fill %v, want both gone", b.done.Value(), b.Progress())
	}
}

// In a list, a finger that lands on a hold button and moves scrolls the list, and the hold lets go unacted.
func TestAFingerMovingOnAHoldButtonScrolls(t *testing.T) {
	kids := make([]gunim.Node, 0, 20)
	b := NewHoldButton("Remove guest")
	b.OnClick = Sends(pressed{7})
	kids = append(kids, Row(b))
	for range 19 {
		kids = append(kids, NewButton("Row"))
	}
	sc := NewScroll(Column(kids...))
	w, _, run := touchStage(t, sc, geom.Sz(300, 300))
	at := geom.Pt(30, 24)
	touchDown(w, at)
	run(8)
	if b.DragsTouch() {
		t.Fatal("130 ms after landing the button keeps the finger from scrolling")
	}
	for i := range 8 {
		w.Input(input.PointerMove{Pos: geom.Pt(at.X, at.Y-float32(i+1)*12), Touch: true, Time: time.Now()})
		run(1)
	}
	touchUp(w, geom.Pt(at.X, at.Y-96))
	run(90)
	if got := sent(w); len(got) != 0 {
		t.Fatalf("a finger that scrolled sent %v", got)
	}
	if b.Holding() || b.Progress() > 0.01 {
		t.Fatalf("after the scroll the button holds %v at %v, want let go and empty", b.Holding(), b.Progress())
	}
}

// Inside something that scrolls, a PIN key lights only once the press has waited, as every control does.
func TestAPinKeyInAScrollLightsAfterTheWait(t *testing.T) {
	p := NewPinPad(4, "OK")
	sc := NewScroll(Column(p, NewSized(NewLabel(""), 10, 900)))
	w, _, run := touchStage(t, sc, geom.Sz(360, 420))
	c := p.keys[4].Center()
	touchDown(w, c)
	for i := range 3 {
		run(1)
		if v := p.lit[4].Value(); v != 0 {
			t.Fatalf("frame %d after landing in a scroll the key is lit %v, want dark yet", i, v)
		}
	}
	run(5)
	if p.lit[4].Target() != 1 {
		t.Fatal("held still past the wait, the key does not light")
	}
	touchUp(w, c)
	run(1)
}

// A theme in another language names the keys and the actions a screen reader hears.
func TestTheTouchKitSpeaksTheThemesLanguage(t *testing.T) {
	sv := Dark().With(append(Touch(),
		theme.Set(PinDelete, "Radera"),
		theme.Set(KeyboardShiftName, "Skift"),
		theme.Set(KeyboardName, "Tangentbord"),
		theme.Set(StackBack, "Tillbaka"))...)
	p := NewPinPad(4, "Lås upp")
	f := NewTextField()
	k := NewKeyboard(Column(f))
	s := NewStack(Column(p, NewSized(k, 600, 300)))
	g := &grab{child: NewThemed(s, sv)}
	w, run := stage(t, &frame{child: g, size: geom.Sz(600, 800)})
	_ = w
	s.Push(NewLabel("Två"), g.ui)
	g.ui.Focus(f)
	run(2)
	if got := p.Access().Parts[pinDelete].Name; got != "Radera" {
		t.Errorf("the delete key is named %q, want the theme's Radera", got)
	}
	if got := s.Access().Description; got != "Tillbaka" {
		t.Errorf("the stack's way back is named %q, want the theme's Tillbaka", got)
	}
	info := k.Access()
	if info.Name != "Tangentbord" {
		t.Errorf("the keyboard is named %q, want Tangentbord", info.Name)
	}
	found := false
	for _, part := range info.Parts {
		found = found || part.Name == "Skift"
	}
	if !found {
		t.Error("no key is named Skift")
	}
}

// Delete takes off one character as a reader sees it, and a finger that slides off it stops it repeating.
func TestKeyboardDeleteTakesOneCharacterAndStopsOffTheKey(t *testing.T) {
	k, f, w, g, run := keyboardStage(t)
	g.ui.Focus(f)
	f.SetText("café 👩‍👩‍👧", g.ui)
	run(60)
	del := kbIndex(t, k, "", keyDelete)
	tapKb(w, run, k, del)
	if got := f.Text(); got != "café " {
		t.Fatalf("Delete after a family emoji left %q, want the emoji gone whole", got)
	}
	tapKb(w, run, k, del)
	tapKb(w, run, k, del)
	if got := f.Text(); got != "caf" {
		t.Fatalf("Delete after an accented e left %q, want the e and its accent gone together", got)
	}
	f.SetText("abcdefghijklmnopqrstuvwxyz", g.ui)
	run(1)
	c := k.rects[del].Center()
	touchDown(w, c)
	run(30)
	w.Input(input.PointerMove{Pos: k.rects[0].Center(), Touch: true, Time: time.Now()})
	run(1)
	left := f.Text()
	run(60)
	if f.Text() != left || len(left) >= 26 {
		t.Fatalf("held, Delete left %q, and slid off, %q; want it to have deleted, then stopped", left, f.Text())
	}
	touchUp(w, k.rects[0].Center())
	run(1)
}

// The letters page has a comma and a full stop, as the signs page does.
func TestTheKeyboardHasACommaOnTheLetters(t *testing.T) {
	k, _, _, _, _ := keyboardStage(t)
	kbIndex(t, k, ",", keyChar)
	kbIndex(t, k, ".", keyChar)
}

// A swipe past a third that is called off, its press let go at Away, springs back; Escape goes back too.
func TestAStackSwipeCalledOffSpringsBack(t *testing.T) {
	s, w, g, run := stackStage(t)
	s.Push(NewButton("Bed"), g.ui)
	run(60)
	t0 := time.Now()
	w.Input(input.PointerDown{Pos: geom.Pt(6, 150), Button: input.ButtonPrimary, Clicks: 1, Time: t0})
	for i := 1; i <= 10; i++ {
		w.Input(input.PointerMove{Pos: geom.Pt(6+float32(i)*25, 150), Time: t0.Add(time.Duration(i) * 100 * time.Millisecond)})
		run(1)
	}
	w.Input(input.PointerUp{Pos: input.Away, Button: input.ButtonPrimary, Time: t0.Add(1100 * time.Millisecond)})
	run(60)
	if s.Len() != 2 || s.front().off.Value() > 0.01 {
		t.Fatalf("a swipe called off past half way left %d screens, the top %v off", s.Len(), s.front().off.Value())
	}
	g.ui.Focus(s.Top())
	key(w, run, input.KeyEscape, 0)
	if s.Len() != 1 {
		t.Fatalf("Escape left %d screens, want 1", s.Len())
	}
}

// fill is a screen of one colour, for telling which screen shows where. One that is low fills only its lower half,
// and draws nothing above, as a screen with no fill of its own leaves gaps.
type fill struct {
	c   color.NRGBA
	low bool
}

func (f *fill) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size { return c.Max }

func (f *fill) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	r := geom.Rect{Max: box.Point()}
	if f.low {
		r.Min.Y = box.H / 2
	}
	p.RRect(r, 0, paint.Solid(f.c))
}

// shownAt returns the colour of the last opaque fill ops draw at pt, in the window's space, inside the clips round
// it, and false where none does.
func shownAt(ops []paint.Op, pt geom.Point) (color.NRGBA, bool) {
	inside := func(r geom.Rect) bool {
		return pt.X >= r.Min.X && pt.X < r.Max.X && pt.Y >= r.Min.Y && pt.Y < r.Max.Y
	}
	moved := func(t paint.Transform, r geom.Rect) geom.Rect {
		return geom.Rect{Min: t.Apply(r.Min), Max: t.Apply(r.Max)}.Normalized()
	}
	clips := []bool{true}
	var got color.NRGBA
	found := false
	for _, op := range ops {
		switch op := op.(type) {
		case *paint.LayerOp:
			in := clips[len(clips)-1]
			if op.Opts.Clip {
				in = in && inside(moved(op.Transform, op.Opts.Bounds))
			}
			clips = append(clips, in)
		case *paint.LayerEndOp:
			clips = clips[:len(clips)-1]
		case *paint.RRectOp:
			if clips[len(clips)-1] && op.Fill.Solid.A == 0xff && inside(moved(op.Transform, op.Rect)) {
				got, found = op.Fill.Solid, true
			}
		}
	}
	return got, found
}

var (
	red   = color.NRGBA{R: 0xff, A: 0xff}
	green = color.NRGBA{G: 0xff, A: 0xff}
	blue  = color.NRGBA{B: 0xff, A: 0xff}
)

// colourStack mounts a stack whose first screen is red, in a window 400 by 300, with screens pushed and settled
// over it.
func colourStack(t *testing.T, over ...*fill) (*Stack, *gunim.Window, *grab, func(int)) {
	t.Helper()
	s := NewStack(&fill{c: red})
	w, g, run := touchStage(t, s, geom.Sz(400, 300))
	for _, f := range over {
		s.Push(f, g.ui)
	}
	run(90)
	return s, w, g, run
}

// seen returns the colours the window shows across the stack at y, every 10 pixels from 5, and "none" where nothing
// opaque is drawn.
func seen(w *gunim.Window, y float32) []string {
	name := map[color.NRGBA]string{red: "red", green: "green", blue: "blue"}
	var out []string
	for x := float32(5); x < 400; x += 10 {
		c, ok := shownAt(w.Offscreen().Ops(), geom.Pt(x, y))
		switch {
		case !ok:
			out = append(out, "none")
		case name[c] != "":
			out = append(out, name[c])
		default:
			out = append(out, "other")
		}
	}
	return out
}

// checkSeen fails unless what shows is want left of edge, a share of the width, and over right of it.
func checkSeen(t *testing.T, frame int, got []string, edge float32, want, over string) {
	t.Helper()
	for i, c := range got {
		x := float32(5 + 10*i)
		switch {
		case x < edge*400-1 && c != want:
			t.Fatalf("frame %d: at %v, left of the edge at %v, the stack shows %s, want %s: %v", frame, x, edge*400, c, want, got)
		case x > edge*400+1 && c != over:
			t.Fatalf("frame %d: at %v, right of the edge at %v, the stack shows %s, want %s: %v", frame, x, edge*400, c, over, got)
		}
	}
}

// As a screen is popped, it shows right of its edge and the screen under it left of it, on every frame.
func TestAStackPopShowsEachScreenInItsOwnPlace(t *testing.T) {
	s, w, g, run := colourStack(t, &fill{c: green})
	going := s.front()
	s.Pop(g.ui)
	for i := range 60 {
		run(1)
		if !slices.Contains(s.gone, going) {
			break
		}
		checkSeen(t, i, seen(w, 150), going.off.Value(), "red", "green")
	}
}

// Two screens popped at once slide off together, and the one under the top one never shows through it, even where
// the top one draws nothing.
func TestAStackDoublePopShowsOnlyTheTopScreenGoing(t *testing.T) {
	s, w, g, run := colourStack(t, &fill{c: green}, &fill{c: blue, low: true})
	top := s.front()
	s.Pop(g.ui)
	s.Pop(g.ui)
	for i := range 60 {
		run(1)
		if !slices.Contains(s.gone, top) {
			break
		}
		checkSeen(t, i, seen(w, 225), top.off.Value(), "red", "blue")
		checkSeen(t, i, seen(w, 75), top.off.Value(), "red", "none")
	}
	if s.Len() != 1 || s.Top() == nil {
		t.Fatalf("after two pops the stack holds %d screens", s.Len())
	}
}

// A Pop and a Push in one go replace the top screen: the screen popped stays in its place under the one coming, on
// every frame, and goes once it is covered; the screen under both never shows.
func TestAStackPopThenPushReplaces(t *testing.T) {
	s, w, g, run := colourStack(t, &fill{c: green})
	popped := s.front()
	s.Pop(g.ui)
	s.Push(&fill{c: blue}, g.ui)
	coming := s.front()
	for i := range 90 {
		run(1)
		checkSeen(t, i, seen(w, 150), coming.off.Value(), "green", "blue")
		if coming.off.Value() < 0.001 && !coming.off.Active() {
			break
		}
	}
	run(2)
	if _, ok := g.ui.Bounds(popped.child); ok || slices.Contains(s.gone, popped) {
		t.Fatal("the screen replaced is still in the tree once the new one covers it")
	}
	if got := seen(w, 150); slices.ContainsFunc(got, func(c string) bool { return c != "blue" }) {
		t.Fatalf("settled, the stack shows %v, want the new screen alone", got)
	}
}

// The touch kit's text reads at 4.5:1 and its edges stand out at 3:1, in the dark theme and the light.
func TestTheTouchKitMeetsItsContrast(t *testing.T) {
	for name, base := range map[string]theme.Theme{"dark": Dark(), "light": Light()} {
		th := theme.NewLive(base.With(Touch()...))
		for _, c := range []struct {
			what   string
			fg, bg theme.Token[color.NRGBA]
			least  float64
		}{
			{"a caption on a card", CaptionInk, CardFill, 4.5},
			{"a caption on the window", CaptionInk, Background, 4.5},
			{"white on a danger button pressed", ButtonStrongInk, ButtonDangerHover, 4.5},
			{"white on a danger button", ButtonStrongInk, ButtonDangerFill, 4.5},
			{"an empty PIN dot", PinDotEdge, Background, 3},
			{"a field's border", FieldBorder, FieldFill, 3},
			{"a key's edge on the keyboard", KeyboardKeyEdge, KeyboardFill, 3},
		} {
			if got := ratio(c.fg.Get(th), c.bg.Get(th)); got < c.least {
				t.Errorf("%s: %s reads at %.2f:1, want %.1f:1 or more", name, c.what, got, c.least)
			}
		}
	}
}

// With the keyboard up, the focused field is scrolled clear of it, its border whole, though it started out of sight.
func TestAKeyboardKeepsTheFieldInSight(t *testing.T) {
	f := NewTextField()
	body := Column(NewSized(NewLabel("Above"), 10, 260), f)
	sc := NewScroll(body)
	k := NewKeyboard(Column(NewSized(NewLabel("Top"), 10, 48), sc).Grow(sc, 1))
	_, g, run := touchStage(t, k, geom.Sz(600, 420))
	g.ui.Focus(f)
	run(90)
	fb, ok := g.ui.Bounds(f)
	if !ok {
		t.Fatal("the field is not laid out")
	}
	if fb.Max.Y > k.top || fb.Min.Y < 48 {
		t.Fatalf("the field lies from %v to %v, want it whole between 48 and the keyboard's top %v",
			fb.Min.Y, fb.Max.Y, k.top)
	}
}

// Where nothing round it scrolls, a hold button keeps the finger at once: one that rolls 12 pixels at 100 ms holds
// on, and the button acts.
func TestAHoldButtonKeepsAFingerThatRollsWhereNothingScrolls(t *testing.T) {
	b := NewHoldButton("Reset all beds")
	b.OnClick = Sends(pressed{8})
	w, _, run := touchStage(t, Row(b), geom.Sz(400, 100))
	at := geom.Pt(30, 24)
	touchDown(w, at)
	run(6)
	w.Input(input.PointerMove{Pos: at.Add(geom.Pt(0, 12)), Touch: true, Time: time.Now()})
	run(1)
	if !b.Holding() {
		t.Fatal("a finger that rolled 12 pixels at 100 ms let the hold go, with nothing to scroll")
	}
	run(60)
	if got := sent(w); len(got) != 1 {
		t.Fatalf("held a second with a roll in it, the button sent %v, want one intent", got)
	}
	touchUp(w, at.Add(geom.Pt(0, 12)))
	run(1)
}

// The PIN handed over is wiped though OnCommit panics, and a slice kept from one commit reads zeros, never the next
// PIN.
func TestAPinPadWipesThePinThoughOnCommitPanics(t *testing.T) {
	p, w, g, run := pinStage(t)
	var kept []byte
	p.OnCommit = func(pin []byte, _ *gunim.UI) gunim.Intent {
		kept = pin
		panic("the check failed")
	}
	for _, d := range []int{9, 7, 3, 1} {
		tapKey(w, run, p, pinKey(d))
	}
	func() {
		defer func() { _ = recover() }()
		p.press(pinEnter, g.ui)
	}()
	if string(kept) != "\x00\x00\x00\x00" {
		t.Fatalf("after OnCommit panicked the PIN handed over reads %q, want wiped", kept)
	}
	p.OnCommit = func([]byte, *gunim.UI) gunim.Intent { return nil }
	for _, d := range []int{2, 4, 6, 8} {
		tapKey(w, run, p, pinKey(d))
	}
	tapKey(w, run, p, pinEnter)
	if string(kept) != "\x00\x00\x00\x00" {
		t.Fatalf("a slice kept from the last commit reads %q after the next, want it untouched and wiped", kept)
	}
}

// A Length set shorter than the digits typed drops those past it, and the pad hands over what is left.
func TestAPinPadShortenedWithDigitsTypedHandsOverWhatFits(t *testing.T) {
	p, w, _, run := pinStage(t)
	var got string
	p.OnCommit = func(pin []byte, _ *gunim.UI) gunim.Intent { got = string(pin); return nil }
	for _, d := range []int{1, 2, 3, 4, 5, 6} {
		tapKey(w, run, p, pinKey(d))
	}
	p.Length = 4
	run(1)
	tapKey(w, run, p, pinEnter)
	if got != "1234" {
		t.Fatalf("shortened to 4 with 6 typed, the pad handed over %q, want 1234", got)
	}
}
