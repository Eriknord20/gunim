package audioui

import (
	"image/color"
	"math"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// LEDs is a meter of lights, as a console's: a row or a column of them
// a channel, lit from the foot up to the level. Pitch is how far apart
// the lights are along the meter, at least, and Gap the dark between
// two; Sep is the room between the channels.
type LEDs struct {
	Pitch, Gap, Sep float32
	// Upright stands the channels side by side, lit from the bottom;
	// else they lie left over right, lit from the left.
	Upright bool
}

// ChipLEDs are the lights of a meter in a chip: two short rows.
var ChipLEDs = LEDs{Pitch: 3.5, Gap: 1.25, Sep: 2}

// LEDAt is the level a light stands for, in decibels, where it is from
// 0 at the meter's foot to 1 at its top: the inverse of [MeterAt], so
// the lights follow a meter's scale.
func LEDAt(u float32) float32 {
	return float32(60*math.Pow(float64(min(max(u, 0), 1)), 1/1.8) - 60)
}

// LEDColour is the colour of a light at db: Safe up to -18 dB, turning
// Near by -9, and Over from -3 to full scale.
func LEDColour(th *theme.Live, db float32) color.NRGBA {
	safe, near, over := Safe.Get(th), Near.Get(th), Over.Get(th)
	switch {
	case db < -18:
		return safe
	case db < -9:
		return Mix(safe, near, (db+18)/9)
	case db < -3:
		return near
	case db < -1:
		return Mix(near, over, (db+3)/2)
	}
	return over
}

// Paint draws lv in r: each channel's lights lit full up to its RMS
// and fainter on up to its peak, its held peak a light lit alone, and
// the rest dark.
func (s LEDs) Paint(p *paint.Painter, th *theme.Live, r geom.Rect, lv *Levels) {
	size := r.Size()
	long, across := size.W, size.H
	if s.Upright {
		long, across = size.H, size.W
	}
	n := int((long + s.Gap) / s.Pitch)
	if n < 1 || across <= s.Sep {
		return
	}
	// The lights fill the meter's length, each a little longer than
	// Pitch where it does not come out even.
	pitch := (long + s.Gap) / float32(n)
	thick := (across - s.Sep) / 2
	dark := Faded(widget.Ink.Get(th), 0.07)
	for ch := range 2 {
		lit, peak, hold := MeterAt(lv.RMS[ch]), MeterAt(lv.Peak[ch]), MeterAt(lv.Hold[ch])
		held := -1
		if lv.Hold[ch] > -59 {
			held = min(int(hold*float32(n)), n-1)
		}
		for i := range n {
			from := float32(i) * pitch
			mid := (float32(i) + 0.5) / float32(n)
			c := dark
			switch {
			case lv.RMS[ch] > -59 && mid <= lit, i == held:
				c = LEDColour(th, LEDAt(mid))
			case lv.Peak[ch] > -59 && mid <= peak:
				c = Faded(LEDColour(th, LEDAt(mid)), 0.4)
			}
			var at geom.Rect
			if s.Upright {
				at = geom.Rc(r.Min.X+float32(ch)*(thick+s.Sep), r.Max.Y-from-(pitch-s.Gap), thick, pitch-s.Gap)
			} else {
				at = geom.Rc(r.Min.X+from, r.Min.Y+float32(ch)*(thick+s.Sep), pitch-s.Gap, thick)
			}
			p.RRect(at, min(1, thick/2), paint.Solid(c))
		}
	}
}
