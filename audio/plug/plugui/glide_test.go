package plugui

import (
	"math"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/theme"
)

// A value set from outside, as a patch picked or an undo, glides the knob's needle there; at rest it asks for no
// frames.
func TestAKnobGlidesToAValueSetElsewhere(t *testing.T) {
	h := &host{values: map[uint32]float64{3: 10}}
	k := NewKnob(h, release)
	w := stageNode(t, k)
	if got := k.turned.Value(); got != 0 {
		t.Fatalf("laid out at its least, the needle stands %v round", got)
	}
	gunim.RegisterPatch(w, "n", func(k *Knob, v float64, u *gunim.UI) { k.Follow(v, u) })
	if err := w.Client().Patch("n", 1000.0); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	if got := k.turned.Value(); got <= 0.05 || got >= 0.95 {
		t.Fatalf("a frame after a patch change the needle stands %v round, want part way to 1", got)
	}
	frames(w, 60)
	if got := k.turned.Value(); got != 1 {
		t.Fatalf("settled, the needle stands %v round, want 1", got)
	}
	if k.Step(time.Second / 60) {
		t.Error("settled, the knob still asks for frames")
	}
}

// A drag moves the needle with the pointer on the same frame, with no glide behind it.
func TestADragMovesTheNeedleWithNoLag(t *testing.T) {
	h := &host{values: map[uint32]float64{3: 10}}
	k := NewKnob(h, release)
	w := stageNode(t, k)
	w.Input(input.PointerMove{Pos: geom.Pt(50, 90)})
	w.Input(input.PointerDown{Pos: geom.Pt(50, 90), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	w.Frame(time.Second / 60)
	for _, y := range []float32{70, 40, 10} {
		w.Input(input.PointerMove{Pos: geom.Pt(50, y)})
		w.Frame(time.Second / 60)
		if want := float32(k.turnOf(k.Value())); math.Abs(float64(k.turned.Value()-want)) > 1e-6 {
			t.Fatalf("dragged to %v, the needle stands %v round, want %v at once", y, k.turned.Value(), want)
		}
	}
	w.Input(input.PointerUp{Pos: geom.Pt(50, 10), Button: input.ButtonPrimary, Time: time.Now()})
	w.Frame(time.Second / 60)
}

// Set with no UI, as a view builds a knob, the needle stands at the value at once.
func TestAKnobFollowedWithNoUIJumps(t *testing.T) {
	h := &host{values: map[uint32]float64{3: 10}}
	tp := NewTrimPot(h, release)
	tp.Layout(gunim.Tight(geom.Sz(TrimPotSize, TrimPotSize)), gunim.Frame{}, gunim.Children{})
	tp.Follow(1000, nil)
	if got := tp.turned.Value(); got != 1 {
		t.Errorf("followed with no UI, the slot stands %v round, want 1 at once", got)
	}
}

// A selector's lamp and a power lamp set from outside glide too.
func TestLampsSetElsewhereGlide(t *testing.T) {
	s := NewSelector("Mode", []string{"A", "B", "C"}, nil)
	w := stageNode(t, s)
	gunim.RegisterPatch(w, "n", func(s *Selector, i int, u *gunim.UI) { s.SetSelected(i, u) })
	if err := w.Client().Patch("n", 2); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	if got := s.lit[2].Value(); got <= 0 || got >= 1 {
		t.Errorf("a frame after it is set, C's lamp is %v lit, want part way", got)
	}
	p := NewPower("gate")
	w = stageNode(t, p)
	gunim.RegisterPatch(w, "n", func(p *Power, on bool, u *gunim.UI) { p.SetChecked(on, u) })
	if err := w.Client().Patch("n", true); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	if got := p.on.Value(); got <= 0 || got >= 1 {
		t.Errorf("a frame after it is set on, the power lamp is %v lit, want part way", got)
	}
}

// glides runs frames at 120 Hz until the knob's needle rests at want, and fails where it overshoots, stands still
// before it arrives, or takes longer than within to rest. It returns how long it took.
func glides(t *testing.T, w *gunim.Window, k *Knob, want float32, within time.Duration) time.Duration {
	t.Helper()
	const frame = time.Second / 120
	from := k.turned.Value()
	last := from
	for took := frame; took <= within; took += frame {
		w.Frame(frame)
		v := k.turned.Value()
		if (want-from)*(v-last) < 0 || (want-from)*(v-want) > 1e-6 {
			t.Fatalf("at %v the needle went from %v to %v, heading for %v from %v: it overshot or turned back",
				took, last, v, want, from)
		}
		if took == frame && (v == from || v == want) {
			t.Fatalf("a frame in, the needle stands at %v, from %v to %v: want it part way", v, from, want)
		}
		if v == want && !k.turned.Active() {
			return took
		}
		last = v
	}
	t.Fatalf("after %v the needle stands at %v, want it at rest at %v", within, k.turned.Value(), want)
	return 0
}

// A new parameter for the knob, set from outside, glides the needle there and rests within about 150 ms.
func TestSetParamGlidesTheNeedle(t *testing.T) {
	h := &host{values: map[uint32]float64{3: 10}}
	k := NewKnob(h, release)
	w := stageNode(t, k)
	gunim.RegisterPatch(w, "n", func(k *Knob, v float64, u *gunim.UI) {
		k.SetParam(plug.Param{ID: 3, Name: "Slope", Min: 0, Max: 1}, v, u)
	})
	if err := w.Client().Patch("n", 0.8); err != nil {
		t.Fatal(err)
	}
	glides(t, w, k, 0.8, 160*time.Millisecond)
}

// An arrow key and a double click glide the needle to where they set the knob.
func TestKeysAndADoubleClickGlideTheNeedle(t *testing.T) {
	h := &host{values: map[uint32]float64{3: 10}}
	k := NewKnob(h, release)
	w := stageNode(t, k)
	// The rest between the key and the click counts by the frames
	w.TimeByFrames(true)
	w.Input(input.KeyPress{Key: input.KeyTab})
	frames(w, 30)
	w.Input(input.KeyPress{Key: input.KeyUp, Mods: input.ModShift})
	glides(t, w, k, float32(k.turnOf(k.Value())), 160*time.Millisecond)
	frames(w, 30)
	at := geom.Pt(KnobW/2, 20)
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary, Time: time.Now()})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 2, Time: time.Now()})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary, Time: time.Now()})
	if k.Value() != release.Default {
		t.Fatalf("a double click set the knob to %v, want its default %v", k.Value(), release.Default)
	}
	glides(t, w, k, float32(k.turnOf(k.Value())), 160*time.Millisecond)
}

// A value that moves on every frame, as an automation ramp, has the needle follow it closely, and rest as it stops.
func TestTheNeedleFollowsARampClosely(t *testing.T) {
	mix := plug.Param{ID: 1, Name: "Mix", Max: 1}
	h := &host{values: map[uint32]float64{1: 0}}
	k := NewKnob(h, mix)
	w := stageNode(t, k)
	// The knob tells a ramp from a jump by the time between values, so that time counts by the frames: on a busy
	// machine, as under the race detector, the wall clock leaves gaps long enough to glide.
	w.TimeByFrames(true)
	gunim.RegisterPatch(w, "n", func(k *Knob, v float64, u *gunim.UI) { k.Follow(v, u) })
	const frame = time.Second / 120
	for i := 1; i <= 120; i++ {
		v := float64(i) / 120
		if err := w.Client().Patch("n", v); err != nil {
			t.Fatal(err)
		}
		w.Frame(frame)
		// The first frame of the ramp may glide; after it, the needle stands with the value.
		if lag := v - float64(k.turned.Value()); i > 1 && math.Abs(lag) > 1e-6 {
			t.Fatalf("on frame %d of the ramp the needle trails the value %v by %v", i, v, lag)
		}
	}
	w.Frame(frame)
	if k.turned.Value() != 1 || k.Step(frame) {
		t.Errorf("a frame after the ramp, the needle stands at %v, moving %v", k.turned.Value(), k.turned.Active())
	}
}

// A knob taken out of the tree, set elsewhere while out, and put back shows its value on the first frame back.
func TestAKnobBackInTheTreeShowsItsValueAtOnce(t *testing.T) {
	h := &host{values: map[uint32]float64{3: 10}}
	k := NewKnob(h, release)
	r := &row{kids: []gunim.Node{k}}
	w := gunimtest.New(t, geom.Sz(400, 120), nil)
	gunim.RegisterView(w, "row", func(struct{}) gunim.Node { return r }, nil)
	if err := w.Client().Mount(gunim.Root, "row", "row", nil); err != nil {
		t.Fatal(err)
	}
	frames(w, 2)
	gunim.RegisterPatch(w, "row", func(r *row, step string, u *gunim.UI) {
		switch step {
		case "out":
			u.Remove(k)
		case "follow":
			k.Follow(1000, u)
		case "in":
			u.Insert(r, k)
		}
	})
	for _, step := range []string{"out", "follow", "in"} {
		if err := w.Client().Patch("row", step); err != nil {
			t.Fatal(err)
		}
		frames(w, 1)
	}
	if got := k.turned.Value(); got != 1 {
		t.Errorf("on the first frame back the needle stands %v round, want 1", got)
	}
}

// wrapped is a control that embeds a knob and lays itself out its own way.
type wrapped struct{ *Knob }

func (w *wrapped) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Constrain(geom.Sz(KnobW, KnobH))
}

// A control that embeds a knob and has a Layout of its own still shows the needle where the knob is turned.
func TestAWrappedKnobsNeedleFollows(t *testing.T) {
	mix := plug.Param{ID: 1, Name: "Mix", Max: 1}
	h := &host{values: map[uint32]float64{1: 0}}
	wk := &wrapped{NewKnob(h, mix)}
	w := stageNode(t, wk)
	at := geom.Pt(KnobW/2, 60)
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	w.Input(input.PointerMove{Pos: geom.Pt(at.X, at.Y-90)})
	w.Input(input.PointerUp{Pos: geom.Pt(at.X, at.Y-90), Button: input.ButtonPrimary, Time: time.Now()})
	frames(w, 2)
	if wk.Value() != 0.5 {
		t.Fatalf("dragged half its throw, the knob stands at %v", wk.Value())
	}
	if got := wk.turned.Value(); got != 0.5 {
		t.Errorf("dragged to 0.5, the wrapped knob's needle stands %v round", got)
	}
}

// Two values set close together and then a long rest, the window asleep meanwhile, leave the next value set to glide:
// the needle counts the rest by the clock, not by the frames drawn.
func TestTheNeedleGlidesAfterARestTheWindowSleptThrough(t *testing.T) {
	mix := plug.Param{ID: 1, Name: "Mix", Max: 1}
	h := &host{values: map[uint32]float64{1: 0}}
	k := NewKnob(h, mix)
	w := stageNode(t, k)
	gunim.RegisterPatch(w, "n", func(k *Knob, v float64, u *gunim.UI) { k.Follow(v, u) })
	for _, v := range []float64{0.5, 0.55} {
		if err := w.Client().Patch("n", v); err != nil {
			t.Fatal(err)
		}
		w.Frame(time.Second / 60)
	}
	// The window sleeps: the clock moves on, and no frame is drawn
	time.Sleep(2 * knobFollows)
	if err := w.Client().Patch("n", 0.0); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	if got := k.turned.Value(); got <= 0.01 || got >= 0.54 {
		t.Errorf("a frame after a value set past a rest, the needle stands %v round, want part way from 0.55 to 0", got)
	}
}

// A trim pot's needle glides with the theme's spring, as a knob's does.
func TestATrimPotGlidesWithTheThemesSpring(t *testing.T) {
	sp := anim.Spring{Response: 0.4, Damping: 1}
	th := theme.NewLive(theme.Make("slow", theme.Set(KnobGlide, sp)))
	tp := NewTrimPot(&host{values: map[uint32]float64{3: 10}}, release)
	tp.Layout(gunim.Tight(geom.Sz(TrimPotSize, TrimPotSize)), gunim.Frame{Theme: th}, gunim.Children{})
	if tp.glide != sp {
		t.Errorf("laid out in a theme that sets the knob's glide, the trim pot glides with %v, want %v", tp.glide, sp)
	}
}
