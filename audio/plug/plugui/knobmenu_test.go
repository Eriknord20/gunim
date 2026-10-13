package plugui

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
)

// menuHost is a host with a menu for each parameter, and control
// moving them.
type menuHost struct {
	host
	menus []geom.Point
	ids   []uint32
	mod   map[uint32]plug.Modulation
}

func (h *menuHost) ParamMenu(id uint32, at geom.Point, _ *gunim.UI) bool {
	h.ids, h.menus = append(h.ids, id), append(h.menus, at)
	return true
}

func (h *menuHost) Modulation(id uint32) (plug.Modulation, bool) {
	m, ok := h.mod[id]
	return m, ok
}

// mountKnob shows k alone in a window, and returns the window.
func mountKnob(t *testing.T, k *Knob) *gunim.Window {
	t.Helper()
	w := gunimtest.New(t, geom.Sz(KnobW, KnobH), nil)
	gunim.RegisterView(w, "knob", func(struct{}) gunim.Node { return k }, nil)
	if err := w.Client().Mount(gunim.Root, "knob", "knob", nil); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	return w
}

// A right click on a knob opens its host's menu for the parameter,
// where the click is, and turns nothing.
func TestARightClickOpensTheHostsMenuForTheParameter(t *testing.T) {
	h := &menuHost{host: host{values: map[uint32]float64{3: 100}}}
	k := NewKnob(h, release)
	w := mountKnob(t, k)
	at := geom.Pt(KnobW/2, 20)
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonSecondary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonSecondary})
	w.Frame(time.Second / 60)
	if len(h.ids) != 1 || h.ids[0] != release.ID || h.menus[0] != at {
		t.Fatalf("the right click asked for menus %v at %v; want one for %d at %v", h.ids, h.menus, release.ID, at)
	}
	if len(h.log) != 0 {
		t.Errorf("the right click turned the knob: %v", h.log)
	}
	// A knob bound to something else has no menu to open.
	f := NewKnobFunc(release, 100, func(*gunim.UI) {}, func(float64, *gunim.UI) {}, func(*gunim.UI) {})
	if f.Handle(input.PointerDown{Pos: at, Button: input.ButtonSecondary, Clicks: 1}, nil) {
		t.Error("a knob of no host's took a right click")
	}
}

// A knob whose parameter control moves draws every frame, ring and
// all, and one left alone rests.
func TestAModulatedKnobDrawsEveryFrame(t *testing.T) {
	h := &menuHost{host: host{values: map[uint32]float64{3: 100}}, mod: map[uint32]plug.Modulation{}}
	k := NewKnob(h, release)
	w := mountKnob(t, k)
	for range 30 {
		w.Frame(time.Second / 60)
	}
	if k.Step(time.Second / 60) {
		t.Fatal("a knob left alone keeps drawing")
	}
	for _, m := range []plug.Modulation{{Depth: 0.3, Now: 200}, {Depth: -2, Now: 5}, {Sets: true, Now: 1000}} {
		h.mod[3] = m
		if !k.Step(time.Second / 60) {
			t.Fatalf("a knob moved by %+v rests", m)
		}
		w.Frame(time.Second / 60)
	}
}
