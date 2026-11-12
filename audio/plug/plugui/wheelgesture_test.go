package plugui

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// idHost is a host that logs each gesture's steps with the parameter's ID, as "begin1".
type idHost struct{ host }

func (h *idHost) Begin(id uint32, _ *gunim.UI) { h.log = append(h.log, "begin"+strconv.Itoa(int(id))) }

func (h *idHost) Set(id uint32, v float64, _ *gunim.UI) {
	h.values[id] = v
	h.log = append(h.log, "set"+strconv.Itoa(int(id)))
}

func (h *idHost) End(id uint32, _ *gunim.UI) { h.log = append(h.log, "end"+strconv.Itoa(int(id))) }

// paired fails unless every begin in log is ended before the next begins, and the last is ended.
func paired(t *testing.T, log []string) {
	t.Helper()
	open := ""
	for _, s := range log {
		switch {
		case len(s) > 5 && s[:5] == "begin":
			if open != "" {
				t.Errorf("%s came while %s was still open: %v", s, open, log)
			}
			open = s[5:]
		case len(s) > 3 && s[:3] == "end":
			if open != s[3:] {
				t.Errorf("%s came with %q open: %v", s, open, log)
			}
			open = ""
		}
	}
	if open != "" {
		t.Errorf("the turn of %s was never ended: %v", open, log)
	}
}

var (
	wheelMix   = plug.Param{ID: 1, Name: "Mix", Max: 1}
	wheelWidth = plug.Param{ID: 2, Name: "Width", Max: 1}
)

// The wheel's turn of one knob ends before a turn of the next begins.
func TestAWheelTurnEndsBeforeAnotherKnobsBegins(t *testing.T) {
	h := &idHost{host{values: map[uint32]float64{1: 0.5, 2: 0.5}}}
	a, b := NewKnob(h, wheelMix), NewKnob(h, wheelWidth)
	w := stageRow(t, a, b)
	wheelAt(w, geom.Pt(KnobW/2, 20), 1, 0)
	wheelAt(w, geom.Pt(KnobW+KnobW/2, 20), 1, 0)
	if want := []string{"begin1", "set1", "end1", "begin2", "set2"}; !slices.Equal(h.log, want) {
		t.Errorf("a notch on each knob in turn sent %v, want %v", h.log, want)
	}
	frames(w, 40)
	paired(t, h.log)
}

// A key or a press anywhere, as Ctrl+Z to undo, ends the wheel's turn before it goes on.
func TestAKeyOrAPressEndsAWheelTurn(t *testing.T) {
	for _, c := range []struct {
		what string
		e    input.Event
	}{
		{"Ctrl+Z", input.KeyPress{Key: input.KeyZ, Mods: input.ModControl}},
		{"a press elsewhere", input.PointerDown{Pos: geom.Pt(300, 100), Button: input.ButtonPrimary, Clicks: 1}},
	} {
		t.Run(c.what, func(t *testing.T) {
			h := &idHost{host{values: map[uint32]float64{1: 0.5}}}
			w := stageRow(t, NewKnob(h, wheelMix))
			wheelAt(w, geom.Pt(KnobW/2, 20), 1, 0)
			w.Input(c.e)
			if want := []string{"begin1", "set1", "end1"}; !slices.Equal(h.log, want) {
				t.Errorf("%s in the wheel's turn left %v, want %v", c.what, h.log, want)
			}
			frames(w, 40)
			paired(t, h.log)
		})
	}
}

// The pointer leaving the knob ends the wheel's turn.
func TestThePointerLeavingEndsAWheelTurn(t *testing.T) {
	h := &idHost{host{values: map[uint32]float64{1: 0.5}}}
	w := stageRow(t, NewKnob(h, wheelMix))
	wheelAt(w, geom.Pt(KnobW/2, 20), 1, 0)
	w.Input(input.PointerMove{Pos: geom.Pt(300, 100)})
	if want := []string{"begin1", "set1", "end1"}; !slices.Equal(h.log, want) {
		t.Errorf("the pointer leaving in the wheel's turn left %v, want %v", h.log, want)
	}
}

// SetParam ends the wheel's turn of the parameter the knob turned, as it ends a drag.
func TestSetParamEndsAWheelTurn(t *testing.T) {
	h := &idHost{host{values: map[uint32]float64{1: 0.5, 2: 0.5}}}
	k := NewKnob(h, wheelMix)
	w := stageRow(t, k)
	wheelAt(w, geom.Pt(KnobW/2, 20), 1, 0)
	k.SetParam(wheelWidth, 0.5, nil)
	if want := []string{"begin1", "set1", "end1"}; !slices.Equal(h.log, want) {
		t.Errorf("SetParam in the wheel's turn left %v, want %v", h.log, want)
	}
	frames(w, 40)
	paired(t, h.log)
}

// A knob leaving the tree, or its window closing, ends the wheel's turn.
func TestAKnobGoneEndsItsWheelTurn(t *testing.T) {
	for _, c := range []struct {
		what string
		gone func(w *gunim.Window)
	}{
		{"the knob's view unmounted", func(w *gunim.Window) {
			if err := w.Client().Unmount("row"); err != nil {
				t.Fatal(err)
			}
			w.Frame(time.Second / 60)
		}},
		{"the window closed", func(w *gunim.Window) { w.Close() }},
	} {
		t.Run(c.what, func(t *testing.T) {
			h := &idHost{host{values: map[uint32]float64{1: 0.5}}}
			w := stageRow(t, NewKnob(h, wheelMix))
			wheelAt(w, geom.Pt(KnobW/2, 20), 1, 0)
			c.gone(w)
			if want := []string{"begin1", "set1", "end1"}; !slices.Equal(h.log, want) {
				t.Errorf("with %s in the wheel's turn, the host heard %v, want %v", c.what, h.log, want)
			}
		})
	}
}
