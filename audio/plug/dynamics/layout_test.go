package dynamics

import (
	"math"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
)

// boundsOf lays the panel out at size and returns where each of nodes
// is.
func boundsOf(t *testing.T, pn *panel, size geom.Size, nodes ...gunim.Node) []geom.Rect {
	t.Helper()
	w := gunimtest.New(t, size, nil)
	gunim.RegisterView(w, "dyn", func(struct{}) gunim.Node { return pn }, nil)
	if err := w.Client().Mount(gunim.Root, "dyn", "dyn", nil); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	out := make([]geom.Rect, len(nodes))
	gunim.RegisterPatch(w, "dyn", func(_ gunim.Node, _ struct{}, u *gunim.UI) {
		for i, n := range nodes {
			out[i], _ = u.Bounds(n)
		}
	})
	if err := w.Client().Patch("dyn", struct{}{}); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	return out
}

func TestTheSoundFillsThePanelUnderItsControls(t *testing.T) {
	p, h := newDynamics(t)
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	size := geom.Sz(1600, 860)
	nodes := make([]gunim.Node, 0, 3+len(pn.sections))
	nodes = append(nodes, pn.hist, pn.gr, pn.out)
	for _, s := range pn.sections {
		nodes = append(nodes, s)
	}
	b := boundsOf(t, pn, size, nodes...)
	if b[0] != (geom.Rect{Max: size.Point()}) {
		t.Fatalf("the history is %v, want the whole panel", b[0])
	}
	// The sections in one row along the foot, centred.
	first, last := b[3], b[len(b)-1]
	if first.Min.Y != last.Min.Y || last.Max.Y != size.H-panelInset ||
		math.Abs(float64((first.Min.X+last.Max.X)/2-size.W/2)) > 1 {
		t.Errorf("the sections run from %v to %v: not along the foot, centred", first, last)
	}
	// The meters side by side, centred above them.
	gr, out := b[1], b[2]
	if gr.Min.Y != out.Min.Y || gr.Max.X >= out.Min.X || out.Max.Y > first.Min.Y-plugui.SectionGap ||
		math.Abs(float64((gr.Min.X+out.Max.X)/2-size.W/2)) > 1 {
		t.Errorf("the meters are at %v and %v, over sections from %v: not side by side, centred above", gr, out, first)
	}
}

func TestTheZeroVUTrimSitsInTheOutputMeterAndTurns(t *testing.T) {
	p, h := newDynamics(t)
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	b := boundsOf(t, pn, geom.Sz(1600, 860), pn.out, pn.zero)
	out, pot := b[0], b[1]
	if !out.Contains(pot.Min) || !out.Contains(pot.Max.Sub(geom.Pt(1, 1))) || pot.Center().X < out.Center().X ||
		pot.Center().Y < out.Center().Y {
		t.Errorf("the trim pot is at %v, the output meter at %v: not in its lower right corner", pot, out)
	}
	for _, s := range pn.sections {
		for _, k := range s.Knobs {
			if k.Param().ID == MeterID {
				t.Error("0 VU is still a knob in a section")
			}
		}
	}
	// Dragged up, it sets 0 VU higher, as one gesture.
	pn.zero.Handle(input.PointerDown{Pos: geom.Pt(12, 12), Button: input.ButtonPrimary, Clicks: 1}, nil)
	pn.zero.Handle(input.PointerMove{Pos: geom.Pt(12, -40)}, nil)
	pn.zero.Handle(input.PointerUp{Pos: geom.Pt(12, -40), Button: input.ButtonPrimary}, nil)
	if got := h.values[MeterID]; got <= param(MeterID).Default || h.log[0] != "begin" || h.log[len(h.log)-1] != "end" {
		t.Errorf("dragged up, 0 VU is at %v dBFS, by %v", got, h.log)
	}
	pn.Follow(MeterID, -20, nil)
	if pn.zero.Value() != -20 || pn.ref != -20 {
		t.Errorf("set elsewhere to -20, the trim shows %v and the meter reads from %v", pn.zero.Value(), pn.ref)
	}
}

func TestTheSidechainsControlsWorkThroughTheHost(t *testing.T) {
	p, h := newDynamics(t)
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	if !pn.freq.Disabled {
		t.Error("listening to the whole sound, the frequency is not greyed")
	}
	pn.side.OnPick(SideBand, nil)
	if h.values[SideModeID] != SideBand || pn.freq.Disabled {
		t.Errorf("band picked, the sidechain is %v, the frequency greyed %v", h.values[SideModeID], pn.freq.Disabled)
	}
	pn.listen.OnClick(nil)
	if h.values[SideListenID] != 1 {
		t.Errorf("listen pressed, it is %v", h.values[SideListenID])
	}
	pn.Follow(SideListenID, 1, nil)
	pn.listen.OnClick(nil)
	if h.values[SideListenID] != 0 {
		t.Errorf("listen pressed again, it is %v", h.values[SideListenID])
	}
	pn.Follow(SideModeID, SideFull, nil)
	if pn.side.Selected() != SideFull || !pn.freq.Disabled {
		t.Errorf("set to the whole sound elsewhere, the selector shows %d, the frequency greyed %v", pn.side.Selected(), pn.freq.Disabled)
	}
}

func TestEachSectionsPowerLampStandsAtTheLeftOfItsHead(t *testing.T) {
	p, h := newDynamics(t)
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	comp := pn.sections[2]
	b := boundsOf(t, pn, geom.Sz(1600, 860), comp, comp.Switch, comp.Knobs[0])
	sec, lamp, knob := b[0], b[1], b[2]
	if lamp.Size() != geom.Sz(plugui.PowerSize, plugui.PowerSize) || lamp.Min.X > sec.Min.X+plugui.SectionPad ||
		lamp.Max.Y > knob.Min.Y || lamp.Min.Y < sec.Min.Y {
		t.Errorf("the compressor's lamp is at %v, in the section at %v over its knob at %v", lamp, sec, knob)
	}
}
