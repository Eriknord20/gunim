package dynamics

import (
	"math"
	"os"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugtest"
	"github.com/marrasen/gunim/geom"
)

// showing returns a panel playing music, its meters moving and its longest values shown.
func showing(t *testing.T) *panel {
	t.Helper()
	p, h := newDynamics(t)
	m := &plugtest.Music{}
	h.in, h.out = m, m
	h.traced = func(id uint32, f int64) (float32, bool) {
		w := float32(math.Sin(float64(f) / 9000))
		switch id {
		case CompGRID:
			return -5 + 3*w, true
		case LimitGRID:
			return -1.5 - w, true
		case GateGRID:
			return 0, true
		}
		return 0, false
	}
	for id, v := range map[uint32]float64{GateThresholdID: -60, CompThresholdID: -24, CompMakeupID: -12,
		LimitCeilingID: -0.3, InputID: -12, OutputID: -12, GateOnID: 1} {
		h.values[id] = v
	}
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	return pn
}

// fit is what the fit check cannot see for itself: the meters' least size, and the parts laid over others.
func fit(pn *panel) plugtest.Fit {
	least := geom.Sz(meterH/0.62, meterH)
	return plugtest.Fit{Least: map[gunim.Node]geom.Size{pn.gr: least, pn.out: least},
		Over: []gunim.Node{pn.zero, pn.tuner.Card}}
}

// The panel fits at its least size, at the size it opens at, and on a large screen, its meters at their full size.
func TestThePanelFitsItsSizes(t *testing.T) {
	for _, s := range []geom.Size{Descriptor.ViewMinSize, Descriptor.ViewSize, geom.Sz(1536, 864)} {
		pn := showing(t)
		plugtest.Fits(t, pn, s, fit(pn))
	}
}

// Under its least size the panel lays out at it, scrolled to its start, and draws nothing outside its box.
func TestUnderItsLeastSizeThePanelLaysOutAtItAndClips(t *testing.T) {
	under := geom.Sz(480, 320)
	plugtest.UnderLeast(t, func() gunim.Node { return showing(t) }, under, Descriptor.ViewMinSize)
}

// TestSheet draws the panel at a range of sizes into GUNIM_SHEETS, for a person to look at.
func TestSheet(t *testing.T) {
	dir := os.Getenv("GUNIM_SHEETS")
	if dir == "" {
		t.Skip("GUNIM_SHEETS names no folder")
	}
	dims := [][2]float32{{560, 380}, {640, 420}, {720, 460}, {720, 480}, {760, 500}, {800, 550},
		{880, 560}, {960, 600}, {1040, 640}, {1280, 720}, {1100, 440}, {760, 640}}
	sizes := make([]geom.Size, 0, len(dims))
	for _, s := range dims {
		sizes = append(sizes, geom.Sz(s[0], s[1]))
	}
	plugtest.Shots(t, func() gunim.Node { return showing(t) }, Descriptor, sizes, dir)
}
