package widget_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/widget"
)

type kitState struct{ N int }

// mountKit mounts root in a window of size, and is how to change it:
// change runs on the UI goroutine with each number published, and the
// window lays out again.
func mountKit(t *testing.T, size geom.Size, root gunim.Node, change func(n int)) (w *gunim.Window, publish func(n int)) {
	t.Helper()
	w = gunimtest.New(t, size, nil)
	gunim.RegisterView(w, "kit", func(kitState) gunim.Node { return root },
		func(_ gunim.Node, s kitState, u *gunim.UI) {
			if s.N > 0 {
				change(s.N)
			}
			u.Invalidate()
		})
	c := w.Client()
	if err := c.Mount(gunim.Root, "kit", "kit", kitState{}, "kit"); err != nil {
		t.Fatal(err)
	}
	return w, func(n int) {
		if err := c.Publish("kit", kitState{N: n}); err != nil {
			t.Fatal(err)
		}
	}
}

func frameN(w *gunim.Window, n int) {
	for range n {
		w.Frame(time.Second / 60)
	}
}

// A badge is there at once the first time; hidden, it shrinks and
// fades out over a few frames, not at once.
func TestABadgeFadesOutWhenHidden(t *testing.T) {
	b := widget.NewBadge("2 problems")
	w, publish := mountKit(t, geom.Sz(300, 100), widget.Row(b, widget.NewSpacer()), func(int) { b.SetHidden(true, nil) })
	frameN(w, 2)
	if got := widget.BadgeShown(b); got != 1 {
		t.Fatalf("shown at first, it is %v of the way in", got)
	}
	publish(1)
	last := float32(1)
	for i := range 60 {
		frameN(w, 1)
		got := widget.BadgeShown(b)
		if got > last+1e-4 {
			t.Fatalf("frame %d of hiding: it grew, from %v to %v", i, last, got)
		}
		if i == 2 && (got <= 0 || got >= 1) {
			t.Fatalf("a few frames after hiding, it is %v of the way in; want it part way out", got)
		}
		last = got
	}
	frameN(w, 120)
	if got := widget.BadgeShown(b); got > 0.001 {
		t.Fatalf("hidden and settled, it is %v of the way in", got)
	}
}

type toggled struct{ On bool }

// A click on a status switch asks for the other state and leaves its
// own alone; set on, the knob slides there over frames.
func TestAStatusSwitchAsksForTheOtherState(t *testing.T) {
	s := widget.NewStatusSwitch("not connected")
	s.OnChange = func(on bool, _ *gunim.UI) gunim.Intent { return toggled{On: on} }
	w, publish := mountKit(t, geom.Sz(300, 100), widget.Column(widget.Row(s, widget.NewSpacer()), widget.NewSpacer()),
		func(int) { s.SetChecked(true, nil) })
	frameN(w, 2)
	w.Input(input.PointerMove{Pos: geom.Pt(10, 10)})
	w.Input(input.PointerDown{Pos: geom.Pt(10, 10), Button: input.ButtonPrimary})
	w.Input(input.PointerUp{Pos: geom.Pt(10, 10), Button: input.ButtonPrimary})
	frameN(w, 1)
	select {
	case env := <-w.Client().Intents():
		if env.Intent != (toggled{On: true}) {
			t.Fatalf("the click sent %#v", env.Intent)
		}
	case <-time.After(time.Second):
		t.Fatal("the click sent nothing")
	}
	if s.Checked() {
		t.Fatal("the click turned the switch on itself; the app does")
	}
	publish(1)
	last := float32(0)
	for i := range 30 {
		frameN(w, 1)
		k := widget.KnobAt(s)
		if k < last {
			t.Fatalf("frame %d: the knob went back, from %v to %v", i, last, k)
		}
		if i == 2 && (k <= 0 || k >= 1) {
			t.Fatalf("a few frames after turning on, the knob is at %v; want it on its way", k)
		}
		last = k
	}
}

// A log view follows its newest line and says it was seen; scrolled
// up, it stays on the lines it shows as more come, and says nothing
// was seen.
func TestALogViewStaysWhereItWasScrolledBack(t *testing.T) {
	var lines []widget.LogLine
	add := func(n int) {
		for range n {
			lines = append(lines, widget.LogLine{Lead: "09:00:00", Text: fmt.Sprint("line ", len(lines))})
		}
	}
	add(100)
	v := widget.NewLogView()
	var seen int64
	v.OnSeen = func(end int64, _ *gunim.UI) gunim.Intent { seen = end; return nil }
	w, publish := mountKit(t, geom.Sz(600, 500), v, func(int) {
		add(5)
		v.SetLines(lines, int64(len(lines)), nil)
	})
	v.SetLines(lines, int64(len(lines)), nil)
	frameN(w, 30)
	if seen != 100 || !widget.LogAtEnd(v) {
		t.Fatalf("opened, it is at the end %v and says %d were seen", widget.LogAtEnd(v), seen)
	}
	w.Input(input.Scroll{Pos: geom.Pt(100, 100), Delta: geom.Pt(0, 300)})
	frameN(w, 60)
	if widget.LogAtEnd(v) {
		t.Fatal("scrolled up, it is still at the end")
	}
	at := widget.LogOffset(v)
	publish(1)
	for i := range 30 {
		frameN(w, 1)
		if got := widget.LogOffset(v); got != at {
			t.Fatalf("frame %d after five lines came: scrolled to %v from %v; want it to stay", i, got, at)
		}
	}
	if seen != 100 {
		t.Fatalf("scrolled up, it says %d were seen", seen)
	}
}

// A compact live graph takes its room before its first samples, and
// asks Sample for them while it runs.
func TestACompactLiveGraphSamplesItself(t *testing.T) {
	g := widget.NewLiveGraph(100*time.Millisecond, 30)
	g.Compact, g.Width = true, 40
	asked := 0
	g.Sample = func() float64 { asked++; return float64(asked) }
	g.SetRunning(true)
	w, _ := mountKit(t, geom.Sz(300, 100), widget.Column(widget.Row(g, widget.NewSpacer()), widget.NewSpacer()), func(int) {})
	frameN(w, 1)
	if asked != 0 {
		t.Fatalf("before any time passed, Sample was asked %d times", asked)
	}
	frameN(w, 30)
	if asked < 3 {
		t.Fatalf("half a second on, Sample was asked %d times; want about 5", asked)
	}
}

// A plain log view's lines take the whole of it, with no box round
// them.
func TestAPlainLogViewIsAllLines(t *testing.T) {
	v := widget.NewLogView()
	v.Plain = true
	v.SetLines([]widget.LogLine{{Text: "one"}}, 1, nil)
	w, _ := mountKit(t, geom.Sz(400, 300), v, func(int) {})
	frameN(w, 2)
	if at := widget.LogListAt(v); at != (geom.Point{}) {
		t.Fatalf("a plain log's lines start at %v, not at its corner", at)
	}
}
