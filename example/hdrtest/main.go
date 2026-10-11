// Command hdrtest tries gunim's HDR spike on Windows: the window shows
// in scRGB, and a test pattern shows patches at one to eight times the
// white of ordinary windows, and a ramp from black to eight times. With
// HDR on in Windows' display settings, the patches past 1x look brighter
// than the white swatch above them, up to the screen's peak, past which
// they look the same. With HDR off they all look like the swatch.
//
//	go run ./example/hdrtest
package main

import (
	"context"
	"fmt"
	"image/color"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver/desktop"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
)

func main() {
	// The spike reads these as each window opens.
	os.Setenv("GUNIM_HDR", "1")
	os.Setenv("GUNIM_HDR_TEST", "1")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := gunim.Main(ctx, func(a *gunim.App) error {
		w, err := a.NewWindow(gunim.WindowOptions{Title: "gunim HDR test", Size: geom.Sz(1100, 700), Root: &pattern{face: text.Default()}})
		if err != nil {
			return err
		}
		for range w.Client().Intents() {
		}
		return w.Client().Err()
	})
	if err != nil {
		log.Fatal(err)
	}
}

// pattern labels the test pattern the HDR pass draws over the window,
// in the same places: the band from 8% to 92% across and 40% to 70%
// down, patches over its upper half and the ramp over its lower half.
type pattern struct {
	face *text.Face
}

// Step implements [gunim.Animator]: the window draws on, so the status
// line follows what the spike finds.
func (*pattern) Step(time.Duration) bool { return true }

// Layout implements [gunim.Node].
func (*pattern) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size { return c.Max }

var (
	ground = color.NRGBA{R: 0x30, G: 0x30, B: 0x30, A: 0xff}
	ink    = color.NRGBA{R: 0xd0, G: 0xd0, B: 0xd0, A: 0xff}
	white  = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
)

// Paint implements [gunim.Node].
func (pt *pattern) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(ground))
	say := func(s string, size float32, at geom.Point, centre bool) {
		para := pt.face.Layout(s, text.Style{Size: size}, box.W*0.84)
		if centre {
			at.X -= para.Size.W / 2
		}
		para.Paint(p, at, ink)
	}
	x0, x1 := box.W*0.08, box.W*0.92
	say("gunim HDR test", 22, geom.Pt(x0, 24), false)
	status := desktop.HDRStatus()
	if status == "" {
		status = "The HDR spike says nothing yet: it runs on Windows alone."
	}
	say(status, 14, geom.Pt(x0, 62), false)

	// White as every ordinary window shows it, to hold the patches to.
	sw := geom.Rc(x0, box.H*0.22, box.W*0.20, box.H*0.10)
	p.RRect(sw, 0, paint.Solid(white))
	say("Interface white: the white of ordinary windows. The 1x patch below should match it.", 14,
		geom.Pt(sw.Max.X+16, sw.Min.Y+sw.Size().H/2-9), false)

	// The patches' factors over them, and the ramp's under it.
	for i, k := range []string{"1x", "1.5x", "2x", "3x", "4x", "6x", "8x"} {
		cx := x0 + (x1-x0)*(float32(i)+0.5)/7
		say(k, 15, geom.Pt(cx, box.H*0.40-24), true)
	}
	for _, m := range []float32{0, 1, 2, 4, 8} {
		label := fmt.Sprintf("%gx", m)
		if m == 0 {
			label = "black"
		}
		say(label, 14, geom.Pt(x0+(x1-x0)*m/8, box.H*0.70+8), true)
	}
	say("If HDR works, patches past 1x are brighter than the swatch. Where they stop getting brighter is the screen's peak.",
		14, geom.Pt(x0, box.H*0.80), false)
	say("If every patch looks like the swatch, the window is not showing in HDR: is HDR on in Windows' display settings?",
		14, geom.Pt(x0, box.H*0.80+24), false)
}
