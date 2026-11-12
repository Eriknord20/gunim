package vocal

import (
	"math"
	"os"
	"testing"

	"github.com/marrasen/gunim"
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
	dims := [][2]float32{{400, 280}, {480, 320}, {560, 360}, {640, 400}, {720, 440}, {800, 480},
		{880, 520}, {960, 560}, {1040, 600}, {1200, 680}, {640, 560}, {1100, 420}}
	sizes := make([]geom.Size, 0, len(dims))
	for _, s := range dims {
		sizes = append(sizes, geom.Sz(s[0], s[1]))
	}
	plugtest.Shots(t, func() gunim.Node { return showing(t) }, Descriptor, sizes, dir)
}
