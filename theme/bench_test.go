package theme

import (
	"image/color"
	"testing"
	"time"
)

var benchFill = Color("bench.fill", color.NRGBA{R: 0x20, G: 0x80, B: 0xc0, A: 0xff})

var sinkColor color.NRGBA

// BenchmarkTokenGet reads a colour token at rest, as a widget does each
// time it paints.
func BenchmarkTokenGet(b *testing.B) {
	l := NewLive(Make("dark", Set(benchFill, color.NRGBA{R: 0x30, G: 0x40, B: 0x50, A: 0xff})))
	benchFill.Get(l)
	b.ReportAllocs()
	for b.Loop() {
		sinkColor = benchFill.Get(l)
	}
}

// BenchmarkTokenGetAnimating reads a colour token ten times a frame
// while a theme switch runs, as a frame with several widgets does.
func BenchmarkTokenGetAnimating(b *testing.B) {
	dark := Make("dark", Set(benchFill, color.NRGBA{R: 0x30, G: 0x40, B: 0x50, A: 0xff}))
	light := Make("light", Set(benchFill, color.NRGBA{R: 0xf0, G: 0xe0, B: 0xd0, A: 0xff}))
	l := NewLive(dark)
	benchFill.Get(l)
	b.ReportAllocs()
	n := 0
	for b.Loop() {
		if n%600 == 0 {
			if l.Active().Name == "dark" {
				l.Use(light)
			} else {
				l.Use(dark)
			}
		}
		if n%10 == 0 {
			l.Step(time.Second / 60)
		}
		sinkColor = benchFill.Get(l)
		n++
	}
}
