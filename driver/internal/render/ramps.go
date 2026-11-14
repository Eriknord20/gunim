//go:build linux || windows || darwin || (js && wasm)

package render

import (
	"encoding/binary"
	"math"

	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/paint"
)

// A gradient with stops, or on a mask, takes its colours from a row of
// the ramp texture: rampWidth texels from its start to its end, straight
// colours as the gradient blends them. A row is drawn once and kept
// while gradients of the same colours draw, so a frame that shades a
// hundred shapes alike uploads one row. When every row is taken, the
// batch draws and the rows start again from the top.
const (
	rampWidth = 256
	rampRows  = 256
)

// ramps is a renderer's ramp texture, on unit 6, and which gradient's
// colours each row holds.
type ramps struct {
	tex  uint32
	rows map[string]int
	next int
	// key and pix are scratch, kept between gradients.
	key []byte
	pix []byte
}

// gradMode returns the mode a shape draws gr in: two colours from the
// quad's own, or a row of the ramp for a gradient with stops.
func (r *Renderer) gradMode(gr *paint.Gradient) float32 {
	if len(gr.Stops) > 0 {
		return r.rampMode(gr)
	}
	if gr.Radial {
		return gradRadial
	}
	return gradLinear
}

// rampMode returns the mode that draws gr from a row of the ramp,
// drawing the row first where no row holds its colours.
func (r *Renderer) rampMode(gr *paint.Gradient) float32 {
	row := r.rampRow(gr)
	mode := gradRamp + 2*row
	if gr.Radial {
		mode++
	}
	return float32(mode)
}

// rampRow returns the row of the ramp that holds gr's colours.
func (r *Renderer) rampRow(gr *paint.Gradient) int {
	rs := &r.ramps
	rs.key = rampKey(rs.key[:0], gr)
	if row, ok := rs.rows[string(rs.key)]; ok {
		return row
	}
	g := r.GL
	if rs.tex == 0 {
		rs.tex = g.CreateTexture()
		g.ActiveTexture(glTexture6)
		g.BindTexture(gl.TEXTURE_2D, rs.tex)
		g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, glLinear)
		g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, glLinear)
		g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
		g.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
		g.TexImage2D(gl.TEXTURE_2D, 0, glRGBA8, rampWidth, rampRows, gl.RGBA, gl.UNSIGNED_BYTE, nil)
		g.ActiveTexture(gl.TEXTURE0)
		rs.rows = map[string]int{}
	}
	if rs.next == rampRows {
		// Every row is taken: what is queued draws with the rows as they
		// are, and they fill again from the top.
		r.flush()
		clear(rs.rows)
		rs.next = 0
	}
	row := rs.next
	rs.next++
	rs.rows[string(rs.key)] = row
	rs.pix = rs.pix[:0]
	for i := range rampWidth {
		c := gr.At(float32(i) / (rampWidth - 1))
		rs.pix = append(rs.pix, c.R, c.G, c.B, c.A)
	}
	g.ActiveTexture(glTexture6)
	g.BindTexture(gl.TEXTURE_2D, rs.tex)
	g.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	g.TexSubImage2D(gl.TEXTURE_2D, 0, 0, int32(row), rampWidth, 1, gl.RGBA, gl.UNSIGNED_BYTE, rs.pix)
	g.ActiveTexture(gl.TEXTURE0)
	if framesDebug {
		r.Stats.Ramps++
	}
	return row
}

// rampKey appends to b what makes gr's colours: its ends and its stops.
func rampKey(b []byte, gr *paint.Gradient) []byte {
	b = append(b, gr.Start.R, gr.Start.G, gr.Start.B, gr.Start.A, gr.End.R, gr.End.G, gr.End.B, gr.End.A)
	for _, s := range gr.Stops {
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(s.At))
		b = append(b, s.Color.R, s.Color.G, s.Color.B, s.Color.A)
	}
	return b
}
