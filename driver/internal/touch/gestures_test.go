package touch

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// win is a window that writes down what it hears, one line an event.
type win struct {
	name    string
	origin  geom.Point
	fingers bool
	got     []string
}

func (w *win) Push(ev any) {
	var s string
	switch e := ev.(type) {
	case input.PointerMove:
		s = fmt.Sprintf("move %v", pt(e.Pos))
	case input.PointerDown:
		s = fmt.Sprintf("down %v clicks %d", pt(e.Pos), e.Clicks)
	case input.PointerUp:
		s = fmt.Sprintf("up %v", pt(e.Pos))
	case input.PointerLeave:
		s = "leave"
	case input.Finger:
		s = fmt.Sprintf("finger %d %s %v", e.ID, map[input.FingerPhase]string{input.FingerDown: "down", input.FingerMove: "move", input.FingerUp: "up"}[e.Phase], pt(e.Pos))
	case input.Pinch:
		s = fmt.Sprintf("pinch %s %v scale %.2f delta %v", map[input.PinchPhase]string{input.PinchStart: "start", input.PinchMove: "move", input.PinchEnd: "end"}[e.Phase], pt(e.Pos), e.Scale, pt(e.Delta))
	default:
		s = fmt.Sprintf("%T", ev)
	}
	w.got = append(w.got, w.name+": "+s)
}

func (w *win) Origin() geom.Point { return w.origin }
func (w *win) TakesFingers() bool { return w.fingers }

// take returns what w heard since the last take.
func (w *win) take() string {
	s := strings.Join(w.got, "\n")
	w.got = nil
	return s
}

func pt(p geom.Point) string {
	if p == input.Away {
		return "away"
	}
	return fmt.Sprintf("%g,%g", p.X, p.Y)
}

// screen is a page with a popup over it, at 100,100 to 200,200.
func screen() (g *Gestures, page, popup *win) {
	page = &win{name: "page"}
	popup = &win{name: "popup", origin: geom.Pt(100, 100)}
	g = &Gestures{Hit: func(p geom.Point) Window {
		if p.X >= 100 && p.Y >= 100 && p.X < 200 && p.Y < 200 {
			return popup
		}
		return page
	}}
	return g, page, popup
}

func want(t *testing.T, w *win, lines ...string) {
	t.Helper()
	if got := w.take(); got != strings.Join(lines, "\n") {
		t.Errorf("got\n%s\nwant\n%s", got, strings.Join(lines, "\n"))
	}
}

var t0 = time.Unix(1000, 0)

func at(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }

// The behaviour below is driver/android's, from before its gesture code
// moved here: Android's view calls Touch, Finger and Pinch as these tests
// do, and must see the same events.

func TestATapPressesAndLetsGoAsTouch(t *testing.T) {
	g, page, _ := screen()
	g.Touch(Down, 0, geom.Pt(10, 20), at(0))
	g.Touch(Move, 0, geom.Pt(12, 21), at(17))
	g.Touch(Up, 0, geom.Pt(12, 21), at(34))
	want(t, page, "page: move 10,20", "page: down 10,20 clicks 1", "page: move 12,21", "page: up 12,21", "page: leave")
	if g.Touched() != nil {
		t.Error("the touch still holds a window after the lift")
	}
}

func TestTwoQuickTapsCountTwoClicks(t *testing.T) {
	g2, page2, _ := screen()
	g2.Touch(Down, 0, geom.Pt(10, 20), at(0))
	g2.Touch(Up, 0, geom.Pt(10, 20), at(50))
	g2.Touch(Down, 0, geom.Pt(20, 30), at(200))
	want(t, page2, "page: move 10,20", "page: down 10,20 clicks 1", "page: up 10,20", "page: leave",
		"page: move 20,30", "page: down 20,30 clicks 2")
	// Far apart, or too late, starts again at one.
	g2.Touch(Up, 0, geom.Pt(20, 30), at(250))
	g2.Touch(Down, 0, geom.Pt(90, 30), at(300))
	g2.Touch(Up, 0, geom.Pt(90, 30), at(320))
	g2.Touch(Down, 0, geom.Pt(90, 30), at(700))
	want(t, page2, "page: up 20,30", "page: leave", "page: move 90,30", "page: down 90,30 clicks 1",
		"page: up 90,30", "page: leave", "page: move 90,30", "page: down 90,30 clicks 1")
}

func TestATouchStaysWithTheWindowItStartedIn(t *testing.T) {
	g, page, popup := screen()
	var started []Window
	g.Started = func(w Window) { started = append(started, w) }
	g.Touch(Down, 0, geom.Pt(150, 150), at(0))
	g.Touch(Move, 0, geom.Pt(300, 150), at(17))
	g.Touch(Up, 0, geom.Pt(300, 150), at(34))
	want(t, popup, "popup: move 50,50", "popup: down 50,50 clicks 1", "popup: move 200,50", "popup: up 200,50", "popup: leave")
	want(t, page)
	if len(started) != 1 || started[0] != popup {
		t.Errorf("Started heard %v, want the popup once", started)
	}
}

func TestACancelLetsThePressGoAway(t *testing.T) {
	g, page, _ := screen()
	g.Touch(Down, 0, geom.Pt(10, 20), at(0))
	page.take()
	g.Touch(Cancel, -1, geom.Point{}, at(10))
	want(t, page, "page: up away", "page: leave")
}

func TestAWindowThatTakesFingersHearsTheFirstFingerBeforeThePress(t *testing.T) {
	g, page, _ := screen()
	page.fingers = true
	if !g.Touch(Down, 3, geom.Pt(10, 20), at(0)) {
		t.Fatal("Touch did not say the finger is the window's own")
	}
	g.Finger(Move, 3, geom.Pt(11, 22), at(17))
	g.Finger(Down, 4, geom.Pt(50, 60), at(20))
	g.Finger(Up, 4, geom.Pt(50, 61), at(30))
	g.Touch(Up, 3, geom.Pt(11, 22), at(40))
	g.Finger(Up, 3, geom.Pt(11, 22), at(40))
	want(t, page, "page: finger 3 down 10,20", "page: move 10,20", "page: down 10,20 clicks 1",
		"page: finger 3 move 11,22", "page: finger 4 down 50,60", "page: finger 4 up 50,61",
		"page: up 11,22", "page: leave", "page: finger 3 up 11,22")
}

func TestAFingerWhoseLiftWasLostLiftsAsTheNextTouchStarts(t *testing.T) {
	g, page, _ := screen()
	page.fingers = true
	g.Touch(Down, 1, geom.Pt(10, 20), at(0))
	g.Finger(Down, 2, geom.Pt(30, 20), at(5))
	page.take()
	// Lost: every lift. The next touch starts.
	g.Touch(Down, 1, geom.Pt(40, 40), at(500))
	got := page.take()
	if !strings.Contains(got, "page: finger 2 up away") || !strings.Contains(got, "page: finger 1 up away") {
		t.Errorf("the fingers left down did not lift:\n%s", got)
	}
	// A finger id landing again lifts the one held under it first.
	g.Finger(Down, 7, geom.Pt(60, 60), at(510))
	g.Finger(Down, 7, geom.Pt(70, 60), at(520))
	want(t, page, "page: finger 7 down 60,60", "page: finger 7 up away", "page: finger 7 down 70,60")
	g.Finger(Cancel, -1, geom.Point{}, at(530))
	if got := page.take(); !strings.Contains(got, "page: finger 7 up away") || !strings.Contains(got, "page: finger 1 up away") {
		t.Errorf("a cancel did not lift every finger:\n%s", got)
	}
}

func TestASecondFingerPinches(t *testing.T) {
	g, page, _ := screen()
	g.Touch(Down, 0, geom.Pt(10, 10), at(0))
	page.take()
	g.Pinch(PinchStart, geom.Pt(10, 10), geom.Pt(30, 10), at(10))
	g.Pinch(PinchMove, geom.Pt(0, 10), geom.Pt(40, 10), at(27))
	// The pointer's own moves are left out while pinched.
	g.Touch(Move, 0, geom.Pt(0, 10), at(28))
	g.Pinch(PinchEnd, geom.Pt(0, 10), geom.Pt(40, 10), at(40))
	g.Touch(Up, 0, geom.Pt(0, 10), at(50))
	want(t, page, "page: up away", "page: pinch start 20,10 scale 1.00 delta 0,0",
		"page: pinch move 20,10 scale 2.00 delta 0,0", "page: pinch end 20,10 scale 1.00 delta 0,0", "page: leave")
}

func TestAPinchEndsAsItsTouchLifts(t *testing.T) {
	g, page, _ := screen()
	g.Touch(Down, 0, geom.Pt(10, 10), at(0))
	g.Pinch(PinchStart, geom.Pt(10, 10), geom.Pt(30, 10), at(10))
	page.take()
	g.Touch(Up, 0, geom.Pt(10, 10), at(50))
	want(t, page, "page: pinch end 20,10 scale 1.00 delta 0,0", "page: leave")
}

func TestAClosedWindowIsForgotten(t *testing.T) {
	g, page, _ := screen()
	page.fingers = true
	g.Touch(Down, 0, geom.Pt(10, 10), at(0))
	g.Forget(page)
	page.take()
	g.Touch(Move, 0, geom.Pt(11, 10), at(10))
	g.Finger(Move, 0, geom.Pt(11, 10), at(10))
	want(t, page)
}
