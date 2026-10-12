package audioui

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
)

// mixerFader is a channel fader, -60 to 6 dB, its top given more room,
// mounted alone in a window 24 wide and 224 tall; log notes its
// gestures.
func mixerFader(t *testing.T) (f *Fader, w *gunim.Window, gain *float32, log *[]string) {
	t.Helper()
	gain, log = new(float32), new([]string)
	f = NewFader(func() float32 { return *gain }, func(v float32, _ *gunim.UI) gunim.Intent {
		*gain = v
		*log = append(*log, "set")
		return nil
	})
	f.Low, f.High, f.Curve = -60, 6, 2
	f.OnBegin = func(*gunim.UI) { *log = append(*log, "begin") }
	f.OnEnd = func(*gunim.UI) { *log = append(*log, "end") }
	w = gunimtest.New(t, geom.Sz(24, 224), nil)
	gunim.RegisterView(w, "fader", func(struct{}) gunim.Node { return f }, nil)
	if err := w.Client().Mount(gunim.Root, "fader", "fader", nil); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	return f, w, gain, log
}

// A fader with ends of its own runs from Low to High, bent by its
// Curve, so 0 dB stands most of the way up, and its keys reach its ends.
func TestAFaderRunsBetweenItsEnds(t *testing.T) {
	f, w, gain, _ := mixerFader(t)
	if at := f.travel(0); math.Abs(float64(at)-(60.0/66)*(60.0/66)) > 1e-4 {
		t.Fatalf("0 dB stands %v up the fader, want %v", at, (60.0/66)*(60.0/66))
	}
	for _, v := range []float32{-60, -30, -6, 0, 3, 6} {
		if back := f.gainAt(f.travel(v)); math.Abs(float64(back-v)) > 1e-3 {
			t.Fatalf("%v dB stands where %v dB is read back", v, back)
		}
	}
	w.Input(input.KeyPress{Key: input.KeyTab})
	w.Input(input.KeyPress{Key: input.KeyEnd})
	w.Frame(time.Second / 60)
	if *gain != 6 {
		t.Fatalf("End took the gain to %v, want the top, 6", *gain)
	}
	w.Input(input.KeyPress{Key: input.KeyHome})
	w.Frame(time.Second / 60)
	if *gain != -60 {
		t.Fatalf("Home took the gain to %v, want the bottom, -60", *gain)
	}
	if a := f.Access(); a.Range.Min != -60 || a.Range.Max != 6 {
		t.Fatalf("the fader reads as running %v to %v, want -60 to 6", a.Range.Min, a.Range.Max)
	}
}

// A drag is one gesture: OnBegin as it starts, OnEnd as it is let go,
// and the cap follows the pointer every frame between, up as it goes up.
func TestAFaderDragIsOneGesture(t *testing.T) {
	f, w, gain, log := mixerFader(t)
	capY := 12 + (224-24)*(1-f.travel(0))
	w.Input(input.PointerMove{Pos: geom.Pt(12, capY)})
	w.Input(input.PointerDown{Pos: geom.Pt(12, capY), Button: input.ButtonPrimary, Clicks: 1})
	last := *gain
	for y := capY; y > capY-48; y -= 4 {
		w.Input(input.PointerMove{Pos: geom.Pt(12, y)})
		w.Frame(time.Second / 60)
		if *gain < last {
			t.Fatalf("the pointer moved up to %v and the gain fell, %v to %v", y, last, *gain)
		}
		last = *gain
	}
	if !f.Dragging() || *gain <= 0 {
		t.Fatalf("dragged up, the fader is at %v dB, dragging %v", *gain, f.Dragging())
	}
	w.Input(input.PointerUp{Pos: geom.Pt(12, capY-48), Button: input.ButtonPrimary})
	w.Frame(time.Second / 60)
	if (*log)[0] != "begin" || (*log)[len(*log)-1] != "end" || slices.Index(*log, "end") != len(*log)-1 || f.Dragging() {
		t.Fatalf("the drag went %v, want one begin, the sets, and one end", *log)
	}
}

// A second button pressed mid-drag ends the drag where it got to: the
// pointer moving on and the first button let go change nothing more.
func TestASecondButtonEndsAFaderDrag(t *testing.T) {
	f, w, gain, log := mixerFader(t)
	capY := 12 + (224-24)*(1-f.travel(0))
	w.Input(input.PointerMove{Pos: geom.Pt(12, capY)})
	w.Input(input.PointerDown{Pos: geom.Pt(12, capY), Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerMove{Pos: geom.Pt(12, capY-20)})
	w.Frame(time.Second / 60)
	w.Input(input.PointerDown{Pos: geom.Pt(12, capY-20), Button: input.ButtonSecondary, Clicks: 1})
	w.Frame(time.Second / 60)
	if f.Dragging() || (*log)[len(*log)-1] != "end" {
		t.Fatalf("after a second button the fader drags on: %v", *log)
	}
	held, n := *gain, len(*log)
	for y := capY - 20; y > capY-80; y -= 8 {
		w.Input(input.PointerMove{Pos: geom.Pt(12, y)})
		w.Frame(time.Second / 60)
		if *gain != held {
			t.Fatalf("after a second button, the pointer at %v moved the gain %v to %v", y, held, *gain)
		}
	}
	w.Input(input.PointerUp{Pos: geom.Pt(12, capY-80), Button: input.ButtonSecondary})
	w.Input(input.PointerUp{Pos: geom.Pt(12, capY-80), Button: input.ButtonPrimary})
	w.Frame(time.Second / 60)
	if len(*log) != n {
		t.Fatalf("letting go after the drag ended went on with %v", (*log)[n:])
	}
}
