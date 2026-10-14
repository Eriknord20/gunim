package audioui

import (
	"image/color"
	"math"
	"testing"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

func TestALightStandsForTheLevelOfItsPlaceOnTheScale(t *testing.T) {
	for _, db := range []float32{-60, -40, -18, -6, -1, 0} {
		if got := LEDAt(MeterAt(db)); math.Abs(float64(got-db)) > 0.01 {
			t.Errorf("the light at %.0f dB's place stands for %.2f dB", db, got)
		}
	}
}

func TestLightsTurnFromSafeToNearToOver(t *testing.T) {
	th := theme.NewLive(widget.Dark())
	for db, want := range map[float32]theme.Token[color.NRGBA]{-40: Safe, -6: Near, 0: Over} {
		if got := LEDColour(th, db); got != want.Get(th) {
			t.Errorf("a light at %.0f dB is %v, want %v", db, got, want.Get(th))
		}
	}
	if c := LEDColour(th, -13); c == Safe.Get(th) || c == Near.Get(th) {
		t.Error("a light between -18 and -9 dB takes neither colour's blend")
	}
}

func TestAMeterOfLightsLightsUpToItsLevel(t *testing.T) {
	th := theme.NewLive(widget.Dark())
	count := func(lv *Levels) (lit int) {
		var p paint.Painter
		ChipLEDs.Paint(&p, th, geom.Rc(0, 0, 35, 8), lv)
		for _, op := range p.Ops() {
			if r, ok := op.(*paint.RRectOp); ok && r.Fill.Solid.A == 0xff {
				lit++
			}
		}
		return lit
	}
	if n := count(NewLevels()); n != 0 {
		t.Fatalf("silence lights %d lights", n)
	}
	l := NewLevels()
	l.Take(sine(800, 1000, 0.5), rate, time.Second/60)
	quiet := count(l)
	l.Take(sine(800, 1000, 1), rate, time.Second/60)
	loud := count(l)
	if quiet == 0 || loud <= quiet {
		t.Fatalf("a sine at 0.5 lights %d lights, and at 1 %d", quiet, loud)
	}
}
