package audioui

import (
	"image"
	"image/color"
	"math"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// Spectrogram is the spectrum heard over the last seconds, scrolling
// left: a column for each moment, its pitches up it, coloured by how
// loud. The columns are kept in tiles of GramTile each. A tile never
// changes once made, so a column added makes only the newest tile
// anew, and the older ones slide along as they are.
type Spectrogram struct {
	// tiles are the tiles filled, the oldest first; fill is the tile
	// being filled, n columns of it, and newest its image as filled.
	tiles  []*paint.Image
	fill   *image.RGBA
	n      int
	newest *paint.Image
	keep   int
}

// NewSpectrogram returns an empty spectrogram.
func NewSpectrogram() *Spectrogram { return &Spectrogram{} }

// GramTile is how many columns a spectrogram's tile holds.
const GramTile = 32

// Push adds a column of levels, from 0 to 1, lowest pitch first, as
// [GramLevel] gives them.
func (g *Spectrogram) Push(levels []float32) {
	h := len(levels)
	if g.fill == nil || g.fill.Rect.Dy() != h {
		g.fill = image.NewRGBA(image.Rect(0, 0, GramTile, h))
		g.n = 0
	}
	for i, v := range levels {
		g.fill.SetRGBA(g.n, h-1-i, GramColor(v))
	}
	g.n++
	g.newest = paint.NewImage(g.fill)
	if g.n == GramTile {
		g.tiles = append(g.tiles, g.newest)
		g.fill, g.newest, g.n = nil, nil, 0
		if over := len(g.tiles) - g.keep; g.keep > 0 && over > 0 {
			g.tiles = g.tiles[over:]
		}
	}
}

// Paint draws the columns into r, the newest at its right edge, each
// col pixels wide, and keeps as many as r shows.
func (g *Spectrogram) Paint(p *paint.Painter, r geom.Rect, col float32) {
	g.keep = int(r.Size().W/(col*GramTile)) + 2
	end := p.Layer(paint.LayerOpts{Bounds: r, Opacity: 1, Clip: true, Radius: 10})
	defer end()
	x := r.Max.X
	if g.newest != nil {
		w := float32(g.n) * col
		h := float32(g.fill.Rect.Dy())
		p.Image(g.newest, geom.Rc(x-w, r.Min.Y, w, r.Size().H), paint.ImageOpts{Src: geom.Rc(0, 0, float32(g.n), h), Opacity: 1})
		x -= w
	}
	for i := len(g.tiles) - 1; i >= 0 && x > r.Min.X; i-- {
		w := GramTile * col
		p.Image(g.tiles[i], geom.Rc(x-w, r.Min.Y, w, r.Size().H), paint.ImageOpts{Opacity: 1})
		x -= w
	}
}

// GramStops are the colours of a spectrogram's level, from silence to
// full.
var GramStops = []color.NRGBA{paint.RGB(0x0c, 0x0e, 0x13), paint.RGB(0x1a, 0x2a, 0x66), paint.RGB(0x2c, 0x8f, 0xa8),
	paint.RGB(0x4f, 0xd6, 0xc0), paint.RGB(0xff, 0xc8, 0x57), paint.RGB(0xff, 0xf4, 0xe0)}

// GramColor is the colour of level v, from 0 to 1.
func GramColor(v float32) color.RGBA {
	v = min(max(v, 0), 1) * float32(len(GramStops)-1)
	i := min(int(v), len(GramStops)-2)
	c := anim.Mix(anim.ColorCodec, GramStops[i], GramStops[i+1], v-float32(i))
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: 0xff}
}

// GramLevel is a spectrum's level, in decibels and tilted, as a
// spectrogram's, from 0 to 1: the lowest fifth of the spectrum's scale
// faded out, so the noise floor stays dark.
func GramLevel(db float32) float32 {
	t := (db - SpectrumBottom) / (SpectrumTop - SpectrumBottom)
	return float32(math.Max(0, float64(t-0.2)/0.8))
}

// Columns is how many columns the spectrogram holds.
func (g *Spectrogram) Columns() int { return len(g.tiles)*GramTile + g.n }
