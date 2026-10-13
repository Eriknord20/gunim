package audioui

import (
	"strconv"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// drawnSpan returns how far across and down each text and rounded rectangle among ops reaches, its transform taken
// as a move: a run of text from its first glyph to half its size past its last.
func drawnSpan(ops []paint.Op) []geom.Rect {
	var out []geom.Rect
	for _, op := range ops {
		switch o := op.(type) {
		case *paint.TextOp:
			if len(o.Glyphs) == 0 {
				continue
			}
			x, y := o.Transform.C, o.Transform.F
			out = append(out, geom.Rect{Min: geom.Pt(x+o.Glyphs[0].At.X, y-o.Size), Max: geom.Pt(x+o.Glyphs[len(o.Glyphs)-1].At.X+o.Size/2, y)})
		case *paint.RRectOp:
			out = append(out, o.Rect.Add(geom.Pt(o.Transform.C, o.Transform.F)))
		}
	}
	return out
}

// keptAcross fails the test where any of ops reaches outside x0 to x1.
func keptAcross(t *testing.T, ops []paint.Op, x0, x1 float32, what string) {
	t.Helper()
	for _, r := range drawnSpan(ops) {
		if r.Min.X < x0-0.5 || r.Max.X > x1+0.5 || r.Size().W < 0 || r.Size().H < 0 {
			t.Fatalf("%s draws %v, outside %v to %v", what, r, x0, x1)
		}
	}
}

// A fader's cap stays on its track for a gain past its range, and a fader shorter than its cap draws nothing upside
// down.
func TestAFaderKeepsItsCapInItsBox(t *testing.T) {
	for _, c := range []struct {
		gain float32
		box  geom.Size
	}{{40, geom.Sz(30, 200)}, {-40, geom.Sz(30, 200)}, {0, geom.Sz(30, 16)}, {30, geom.Sz(10, 6)}} {
		f := NewFader(func() float32 { return c.gain }, nil)
		var p paint.Painter
		f.Paint(&p, gunim.Frame{}, c.box, gunim.Children{})
		for _, r := range drawnSpan(p.Ops()) {
			if r.Min.Y < -0.5 || r.Max.Y > c.box.H+0.5 || r.Size().W < 0 || r.Size().H < 0 {
				t.Fatalf("at %v dB in a box %v, the fader draws %v", c.gain, c.box, r)
			}
		}
	}
}

// A loudness panel keeps everything it draws inside its width, however narrow.
func TestALoudnessPanelKeepsToItsWidth(t *testing.T) {
	l := NewLoudness(rate)
	tone := sine(rate/10, 1000, 0.9)
	for range 50 {
		l.Write(tone)
	}
	l.Step(1e9, true)
	for _, w := range []float32{30, 60, 78, 120, 185, 300} {
		var p paint.Painter
		l.Paint(&p, nil, geom.Rc(10, 0, w, 200), -14)
		keptAcross(t, p.Ops(), 10, 10+w, "a loudness panel "+itoa(w)+" wide")
	}
}

// A meter's labels keep to its width, and a spectrum's pitches to its area.
func TestMeterAndPitchLabelsKeepToTheirWidth(t *testing.T) {
	l := NewLevels()
	l.Top = [2]float32{-0.1, -88.8}
	var p paint.Painter
	PaintMeter(&p, nil, geom.Rc(10, 40, MeterW, 200), l)
	keptAcross(t, p.Ops(), 10, 10+MeterW, "a meter")
	for _, w := range []float32{60, 150, 190, 400} {
		p.Reset()
		PaintPitches(&p, nil, geom.Rc(10, 0, w, 100), false)
		keptAcross(t, p.Ops(), 10, 10+w, "a spectrum's pitches "+itoa(w)+" wide")
	}
}

func itoa(w float32) string { return strconv.Itoa(int(w)) }

// A meter's two peak readouts stay apart, however long, and inside its
// width.
func TestAMetersReadoutsStayApart(t *testing.T) {
	for _, top := range [][2]float32{{-19.9, -23.6}, {-0.1, -88.8}, {-3.2, -4.1}, {-59.9, -60}} {
		l := NewLevels()
		l.Top = top
		var p paint.Painter
		PaintMeter(&p, nil, geom.Rc(10, 40, MeterW, 200), l)
		var texts []geom.Rect
		for _, op := range p.Ops() {
			if o, ok := op.(*paint.TextOp); ok && o.Size == 8 && len(o.Glyphs) > 0 {
				texts = append(texts, drawnSpan([]paint.Op{o})[0])
			}
		}
		if len(texts) != 2 {
			t.Fatalf("%v: the meter draws %d readouts", top, len(texts))
		}
		if texts[0].Max.X > texts[1].Min.X || texts[0].Min.X < 10-0.5 || texts[1].Max.X > 10+MeterW+0.5 {
			t.Fatalf("%v: the readouts stand at %v and %v, in a meter from 10 to %v", top, texts[0], texts[1], 10+MeterW)
		}
	}
}
