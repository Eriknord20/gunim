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

// A tall meter lights its columns as the chips' rows are lit, in the
// lights' colours, not in grey bars.
func TestATallMeterIsLit(t *testing.T) {
	th := theme.NewLive(widget.Dark())
	l := NewLevels()
	l.Take(sine(800, 1000, 0.5), rate, time.Second/60)
	var p paint.Painter
	PaintMeter(&p, th, geom.Rc(0, 40, MeterW, 200), l)
	safe := 0
	for _, op := range p.Ops() {
		if r, ok := op.(*paint.RRectOp); ok && r.Fill.Solid == Safe.Get(th) {
			safe++
		}
	}
	if safe == 0 {
		t.Fatal("a meter of a sine at 0.5 lights no lights")
	}
}

// Each light a meter lights is in LEDColour's colour for the level it
// stands for, fainter where only the peak reaches it, however many
// lights the meter has.
func TestEachLightTakesTheColourOfItsLevel(t *testing.T) {
	th := theme.NewLive(widget.Dark())
	lv := &Levels{RMS: [2]float32{-6, -20}, Peak: [2]float32{-0.5, -4}, Hold: [2]float32{-0.5, -2}}
	for _, long := range []float32{200, 600} {
		s := LEDs{Pitch: 4, Gap: 1, Sep: 2, Upright: true}
		var p paint.Painter
		s.Paint(&p, th, geom.Rc(0, 0, 20, long), lv)
		n := int((long + s.Gap) / s.Pitch)
		ops := p.Ops()
		if len(ops) != 2*n {
			t.Fatalf("a meter of %d lights a channel drew %d ops", n, len(ops))
		}
		dark := paint.Faded(widget.Ink.Get(th), 0.07)
		for k, op := range ops {
			ch, i := k/n, k%n
			mid := (float32(i) + 0.5) / float32(n)
			want := dark
			held := min(int(MeterAt(lv.Hold[ch])*float32(n)), n-1)
			switch {
			case mid <= MeterAt(lv.RMS[ch]), i == held:
				want = LEDColour(th, LEDAt(mid))
			case mid <= MeterAt(lv.Peak[ch]):
				want = paint.Faded(LEDColour(th, LEDAt(mid)), 0.4)
			}
			if got := op.(*paint.RRectOp).Fill.Solid; got != want {
				t.Fatalf("in a meter of %d lights, light %d of channel %d is %v, want %v", n, i, ch, got, want)
			}
		}
	}
}

// BenchmarkAMeterOfLights times painting a mixer's meter, loud.
func BenchmarkAMeterOfLights(b *testing.B) {
	th := theme.NewLive(widget.Dark())
	lv := &Levels{RMS: [2]float32{-6, -8}, Peak: [2]float32{-1, -2}, Hold: [2]float32{-0.5, -1}}
	s := LEDs{Pitch: 4, Gap: 1, Sep: 2, Upright: true}
	var p paint.Painter
	for range b.N {
		p.Reset()
		s.Paint(&p, th, geom.Rc(0, 0, 46, 124), lv)
	}
}

func TestAReadoutNearZeroHasNoSign(t *testing.T) {
	for _, c := range []struct {
		format string
		v      float64
		want   string
	}{
		{"%.0f", -0.1, "0"},
		{"%.0f", -0.6, "-1"},
		{"%.1f", -0.04, "0.0"},
		{"%.1f", -0.06, "-0.1"},
		{"%.1f", 0.04, "0.0"},
		{"%.1f", -10.0, "-10.0"},
	} {
		if got := Readout(c.format, c.v); got != c.want {
			t.Errorf("Readout(%q, %v) = %q, want %q", c.format, c.v, got, c.want)
		}
	}
}
