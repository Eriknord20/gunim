package plugtest

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
)

// Picture draws a view at size in an offscreen window, after a second of frames, and returns the picture. It skips
// t where nothing can draw pixels.
func Picture(t testing.TB, view gunim.Node, size geom.Size) *image.RGBA {
	t.Helper()
	w := mount(t, size, view)
	for range 60 {
		w.Frame(time.Second / 60)
	}
	return gunimtest.Pixels(t, w)
}

// Sheet draws a new view from view at each of sizes, in rows at most 3200 pixels wide, each under its size, and
// writes the sheet to path as a PNG, for a person to choose a view's sizes by. It skips t where nothing can draw
// pixels.
func Sheet(t testing.TB, view func() gunim.Node, sizes []geom.Size, path string) {
	t.Helper()
	const gap, caption, most = 16, 18, 3200
	pics := make([]*image.RGBA, len(sizes))
	type place struct{ x, y int }
	at := make([]place, len(sizes))
	x, y, rowH, wide := gap, gap, 0, 0
	for i, s := range sizes {
		pics[i] = Picture(t, view(), s)
		b := pics[i].Bounds()
		if x > gap && x+b.Dx() > most {
			x, y, rowH = gap, y+rowH+caption+gap, 0
		}
		at[i] = place{x, y}
		x += b.Dx() + gap
		rowH = max(rowH, b.Dy())
		wide = max(wide, x)
	}
	sheet := image.NewRGBA(image.Rect(0, 0, wide, y+rowH+caption+gap))
	draw.Draw(sheet, sheet.Bounds(), image.NewUniform(color.RGBA{R: 0x50, G: 0x50, B: 0x58, A: 0xff}), image.Point{},
		draw.Src)
	d := font.Drawer{Dst: sheet, Src: image.White, Face: basicfont.Face7x13}
	for i, p := range pics {
		draw.Draw(sheet, p.Bounds().Add(image.Pt(at[i].x, at[i].y+caption)), p, p.Bounds().Min, draw.Src)
		d.Dot = fixed.P(at[i].x, at[i].y+13)
		d.DrawString(fmt.Sprintf("%g × %g", sizes[i].W, sizes[i].H))
	}
	gunimtest.WritePNG(t, path, sheet)
}

// Shots writes what a person needs to choose and check the sizes of d's window view into dir: a sheet of the view
// at sizes, and pictures at d's least size and at the size it opens at. view makes a new view for each picture.
func Shots(t testing.TB, view func() gunim.Node, d plug.Descriptor, sizes []geom.Size, dir string) {
	t.Helper()
	Sheet(t, view, sizes, filepath.Join(dir, "sheet.png"))
	for name, s := range map[string]geom.Size{"least": d.ViewMinSize, "open": d.ViewSize} {
		if s != (geom.Size{}) {
			gunimtest.WritePNG(t, filepath.Join(dir, fmt.Sprintf("%s-%gx%g.png", name, s.W, s.H)), Picture(t, view(), s))
		}
	}
}
