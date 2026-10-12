package gunim

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// fingersOf returns the fingers r heard, without their times.
func fingersOf(r *recorder) []input.Finger {
	var out []input.Finger
	for _, e := range r.events {
		if f, ok := e.(input.Finger); ok {
			f.Time = time.Time{}
			out = append(out, f)
		}
	}
	return out
}

func TestAFingerGoesToTheNodeItCameDownOnWhereverItGoes(t *testing.T) {
	w := newTestWindow()
	left, right := &recorder{}, &recorder{}
	s := &swapped{}
	w.ui.Insert(w.ui.Root(), s)
	w.ui.Insert(s, left)
	w.ui.Insert(s, right)
	run(w, 1)
	hand := func(id int, ph input.FingerPhase, at geom.Point) {
		w.ui.handlePlatform(input.Finger{ID: id, Pos: at, Phase: ph, Time: time.Now()})
	}
	hand(3, input.FingerDown, geom.Pt(150, 40))
	hand(5, input.FingerDown, geom.Pt(20, 30))
	hand(3, input.FingerMove, geom.Pt(60, 45)) // over the left node now
	hand(3, input.FingerUp, geom.Pt(60, 45))
	hand(3, input.FingerMove, geom.Pt(160, 45)) // lifted: goes nowhere
	hand(5, input.FingerUp, input.Away)
	want := []input.Finger{
		{ID: 3, Pos: geom.Pt(50, 40), Phase: input.FingerDown},
		{ID: 3, Pos: geom.Pt(-40, 45), Phase: input.FingerMove},
		{ID: 3, Pos: geom.Pt(-40, 45), Phase: input.FingerUp},
	}
	if got := fingersOf(right); !equalFingers(got, want) {
		t.Errorf("the right node heard %+v, want %+v", got, want)
	}
	wantL := []input.Finger{
		{ID: 5, Pos: geom.Pt(20, 30), Phase: input.FingerDown},
		{ID: 5, Pos: input.Away, Phase: input.FingerUp},
	}
	if got := fingersOf(left); !equalFingers(got, wantL) {
		t.Errorf("the left node heard %+v, want %+v", got, wantL)
	}
}

// A finger whose lift was lost lifts when its id comes down again, as
// Android gives a lifted finger's id to the next one: the old node hears
// it lift, and the new finger goes only where it lands.
func TestAFingerDownAgainUnderItsIDLiftsTheOldOne(t *testing.T) {
	for _, tc := range []struct {
		name  string
		again geom.Point // where the id comes down the second time
		right []input.Finger
	}{
		{name: "on empty space", again: geom.Pt(500, 400)},
		{name: "on another node", again: geom.Pt(150, 40), right: []input.Finger{
			{ID: 1, Pos: geom.Pt(50, 40), Phase: input.FingerDown},
			{ID: 1, Pos: geom.Pt(-70, 40), Phase: input.FingerMove},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newTestWindow()
			left, right := &recorder{}, &recorder{}
			s := &swapped{}
			w.ui.Insert(w.ui.Root(), s)
			w.ui.Insert(s, left)
			w.ui.Insert(s, right)
			run(w, 1)
			hand := func(ph input.FingerPhase, at geom.Point) {
				w.ui.handlePlatform(input.Finger{ID: 1, Pos: at, Phase: ph, Time: time.Now()})
				run(w, 1)
			}
			check := func(step string, r *recorder, want []input.Finger) {
				t.Helper()
				if got := fingersOf(r); !equalFingers(got, want) {
					t.Errorf("after %s the node heard %+v, want %+v", step, got, want)
				}
			}
			down := input.Finger{ID: 1, Pos: geom.Pt(20, 30), Phase: input.FingerDown}
			lift := input.Finger{ID: 1, Pos: input.Away, Phase: input.FingerUp}

			hand(input.FingerDown, geom.Pt(20, 30))
			check("the first down", left, []input.Finger{down})
			check("the first down", right, nil)

			hand(input.FingerDown, tc.again)
			check("the second down", left, []input.Finger{down, lift})
			check("the second down", right, tc.right[:min(1, len(tc.right))])

			hand(input.FingerMove, geom.Pt(30, 40)) // over the left node
			check("the move", left, []input.Finger{down, lift})
			check("the move", right, tc.right)
			if tc.right == nil && len(w.ui.fingers) != 0 {
				t.Errorf("a finger on empty space left %v behind", w.ui.fingers)
			}
		})
	}
}

func TestAFingerOnANodeThatLeavesGoesNowhereMore(t *testing.T) {
	w := newTestWindow()
	r := &recorder{}
	w.ui.Insert(w.ui.Root(), r)
	run(w, 1)
	w.ui.handlePlatform(input.Finger{ID: 1, Pos: geom.Pt(10, 10), Phase: input.FingerDown})
	w.ui.Remove(r)
	run(w, 1)
	w.ui.handlePlatform(input.Finger{ID: 1, Pos: geom.Pt(12, 10), Phase: input.FingerMove})
	if got := fingersOf(r); len(got) != 1 {
		t.Errorf("a node gone heard %+v, want only the finger coming down before it went", got)
	}
	if len(w.ui.fingers) != 0 {
		t.Errorf("the window still sends fingers %v somewhere", w.ui.fingers)
	}
}

func TestTakeFingersReachesTheWindow(t *testing.T) {
	w := NewOffscreen(geom.Sz(400, 800), nil)
	if err := w.Client().TakeFingers(true); err != nil {
		t.Fatal(err)
	}
	if !w.Offscreen().FingersTaken() {
		t.Fatal("the window does not take fingers after TakeFingers(true)")
	}
	if err := w.Client().TakeFingers(false); err != nil || w.Offscreen().FingersTaken() {
		t.Fatalf("after TakeFingers(false) the window takes fingers %v (err %v)", w.Offscreen().FingersTaken(), err)
	}
	var _ driver.FingerTaker = w.Offscreen()
}

func equalFingers(a, b []input.Finger) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// board is a node that takes fingers, with one child at its top left,
// as a game's board with a button on it.
type board struct{ recorder }

func (b *board) Layout(c Constraints, _ Frame, kids Children) geom.Size {
	k := kids.At(0)
	k.Layout(Tight(geom.Sz(50, 50)))
	k.Place(geom.Pt(0, 0))
	return c.Max
}

func (b *board) Paint(p *paint.Painter, _ Frame, _ geom.Size, kids Children) { kids.At(0).Paint(p) }

// pressOnly is a node that takes the pointer and lets fingers by, as a
// stock button does.
type pressOnly struct{ recorder }

func (p *pressOnly) Handle(e input.Event, u *UI) bool {
	p.recorder.Handle(e, u)
	_, finger := e.(input.Finger)
	return !finger
}

// pointerOf returns the pointer presses, moves and releases r heard.
func pointerOf(r *recorder) []string {
	var out []string
	for _, e := range r.events {
		switch e.(type) {
		case input.PointerDown:
			out = append(out, "down")
		case input.PointerMove:
			out = append(out, "move")
		case input.PointerUp:
			out = append(out, "up")
		}
	}
	return out
}

// In a window that takes fingers the first finger comes as a Finger
// too, ahead of its press, as a driver sends it. A node holding the
// finger hears none of its pointer events; a node inside it still takes
// the press, and so does a node elsewhere that took no finger.
func TestTheFirstFingerIsAFingerAndThePointerStopsAtItsNode(t *testing.T) {
	for _, tc := range []struct {
		name        string
		at          geom.Point
		board, btn  []string // pointer events each hears
		right       []string
		boardFinger bool // the board holds the finger
	}{
		{name: "on the board", at: geom.Pt(70, 70), boardFinger: true},
		{name: "on the button on the board", at: geom.Pt(20, 20), boardFinger: true,
			btn: []string{"move", "down", "up"}},
		{name: "on a node that takes no finger", at: geom.Pt(150, 40),
			right: []string{"move", "down", "up"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newTestWindow()
			b, btn, right := &board{}, &pressOnly{}, &pressOnly{}
			s := &swapped{}
			w.ui.Insert(w.ui.Root(), s)
			w.ui.Insert(s, b)
			w.ui.Insert(s, right)
			w.ui.Insert(b, btn)
			run(w, 1)
			step := func(name string, evs ...input.Event) {
				t.Helper()
				for _, e := range evs {
					w.ui.handlePlatform(e)
				}
				run(w, 1)
				if got, ok := w.ui.fingers[7]; ok != (tc.boardFinger && name != "the lift") || (ok && got.node != b) {
					t.Errorf("after %s the finger is held by %v (%v)", name, got, ok)
				}
				for _, c := range []struct {
					who  string
					r    *recorder
					want []string
				}{{"board", &b.recorder, tc.board}, {"button", &btn.recorder, tc.btn}, {"right node", &right.recorder, tc.right}} {
					if got := pointerOf(c.r); len(got) > len(c.want) || !equalStrings(got, c.want[:len(got)]) {
						t.Errorf("after %s the %s heard %v of the pointer, want at most %v", name, c.who, got, c.want)
					}
				}
			}
			now := time.Now()
			to := tc.at.Add(geom.Pt(2, 1)) // inside the slop: a tap
			step("the finger coming down",
				input.Finger{ID: 7, Pos: tc.at, Phase: input.FingerDown, Time: now},
				input.PointerMove{Pos: tc.at, Touch: true, Time: now},
				input.PointerDown{Pos: tc.at, Button: input.ButtonPrimary, Clicks: 1, Touch: true, Time: now})
			step("the move",
				input.PointerMove{Pos: to, Touch: true, Time: now},
				input.Finger{ID: 7, Pos: to, Phase: input.FingerMove, Time: now})
			step("the release",
				input.PointerUp{Pos: to, Button: input.ButtonPrimary, Touch: true, Time: now},
				input.PointerLeave{Time: now})
			step("the lift", input.Finger{ID: 7, Pos: to, Phase: input.FingerUp, Time: now})
			for _, c := range []struct {
				who  string
				r    *recorder
				want []string
			}{{"board", &b.recorder, tc.board}, {"button", &btn.recorder, tc.btn}, {"right node", &right.recorder, tc.right}} {
				if got := pointerOf(c.r); !equalStrings(got, c.want) {
					t.Errorf("the %s heard %v of the pointer, want %v", c.who, got, c.want)
				}
			}
			if tc.boardFinger {
				want := []input.Finger{
					{ID: 7, Pos: tc.at, Phase: input.FingerDown},
					{ID: 7, Pos: to, Phase: input.FingerMove},
					{ID: 7, Pos: to, Phase: input.FingerUp},
				}
				if got := fingersOf(&b.recorder); !equalFingers(got, want) {
					t.Errorf("the board heard %+v, want %+v", got, want)
				}
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
