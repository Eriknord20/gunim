package paint

import (
	"testing"

	"github.com/marrasen/gunim/geom"
)

// benchDamage times Damage over two frames of n ops: still, or with a
// few dots that come and go early in it, or with everything moved, as
// in a scroll.
func benchDamage(b *testing.B, n int, frame func(p *Painter, odd bool)) {
	var p Painter
	odd := false
	p.Reset()
	frame(&p, odd)
	for b.Loop() {
		odd = !odd
		p.Reset()
		frame(&p, odd)
		_ = p.DamageRects()
	}
}

func BenchmarkDamageOfAStillFrame(b *testing.B) {
	benchDamage(b, 20000, func(p *Painter, _ bool) {
		for i := range 20000 {
			p.RRect(geom.Rc(float32(i%100*10), float32(i/100*5), 8, 4), 0, dark)
		}
	})
}

func BenchmarkDamageOfDotsComingEarly(b *testing.B) {
	benchDamage(b, 20000, func(p *Painter, odd bool) {
		dots := 5
		if odd {
			dots = 40
		}
		for i := range dots {
			p.RRect(geom.Rc(float32(i*3), 0, 2, 2), 0, light)
		}
		for i := range 20000 {
			p.RRect(geom.Rc(float32(i%100*10), float32(10+i/100*5), 8, 4), 0, dark)
		}
	})
}

func BenchmarkDamageOfAScroll(b *testing.B) {
	benchDamage(b, 20000, func(p *Painter, odd bool) {
		dy := float32(0)
		if odd {
			dy = 3
		}
		for i := range 20000 {
			p.RRect(geom.Rc(float32(i%100*10), dy+float32(i/100*5), 8, 4), 0, dark)
		}
	})
}
