package eq

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug/plugtest"
	"github.com/marrasen/gunim/geom"
)

// showing returns the plugin's window view playing music, its meters and traces moving.
func showing(t *testing.T) gunim.Node {
	t.Helper()
	h := plugtest.NewHost(&Descriptor)
	h.Trace = func(id uint32, f int64) (float32, bool) {
		return float32(-4 + 3*math.Sin(float64(f)/9000+float64(id))), true
	}
	pl := Descriptor.New(h)
	setBands(t, pl, []audio.Band{{ID: 1, Kind: audio.LowCut, Freq: 40, Q: 0.7, On: true},
		{ID: 2, Kind: audio.Bell, Freq: 250, Gain: -4.5, Q: 1.4, On: true},
		{ID: 3, Kind: audio.Bell, Freq: 2400, Gain: 6, Q: 2, On: true},
		{ID: 4, Kind: audio.HighShelf, Freq: 9000, Gain: 3, Q: 0.7, On: true}})
	v := plugtest.WindowView(pl, h)
	if v == nil {
		t.Fatal("the plugin has no view for a window")
	}
	return v
}

// The view fits at its least size, at the size it opens at, and on a large screen.
func TestTheViewFitsItsSizes(t *testing.T) {
	for _, s := range []geom.Size{Descriptor.ViewMinSize, Descriptor.ViewSize, geom.Sz(1536, 864)} {
		v := showing(t)
		plugtest.Fits(t, v, s, plugtest.Fit{})
	}
}

// Under its least size the view lays out at it and draws nothing outside its box.
func TestUnderItsLeastSizeTheViewClips(t *testing.T) {
	plugtest.Inside(t, showing(t), geom.Sz(Descriptor.ViewMinSize.W/2, Descriptor.ViewMinSize.H/2))
}

// TestSheet draws the view at a range of sizes into GUNIM_SHEETS, for a person to look at.
func TestSheet(t *testing.T) {
	dir := os.Getenv("GUNIM_SHEETS")
	if dir == "" {
		t.Skip("GUNIM_SHEETS names no folder")
	}
	var sizes []geom.Size
	for _, s := range [][2]float32{{400, 280}, {480, 320}, {560, 360}, {640, 400}, {720, 440}, {800, 480},
		{880, 520}, {960, 560}, {1040, 600}, {1200, 680}, {640, 560}, {1100, 420}} {
		sizes = append(sizes, geom.Sz(s[0], s[1]))
	}
	plugtest.Shots(t, func() gunim.Node { return showing(t) }, Descriptor, sizes, dir)
}

// With every band listed, the list's controls fit at the view's sizes. The graph shows through the list's glass, so
// its labels may lie under the list's.
func TestTheBandListFitsTheViewsSizes(t *testing.T) {
	for _, s := range []geom.Size{Descriptor.ViewMinSize, Descriptor.ViewSize} {
		v, ok := showing(t).(*full)
		if !ok {
			t.Fatal("the window's view is of another type")
		}
		v.views.SetSelected(1, nil)
		v.listed.Jump(1)
		v.list.show(nil)
		for _, m := range plugtest.Misfits(t, v, s, plugtest.Fit{}) {
			if m.Kind != plugtest.Overlaps || !strings.HasPrefix(m.Text, "text ") {
				t.Errorf("at %v×%v: %s", s.W, s.H, m)
			}
		}
	}
}
