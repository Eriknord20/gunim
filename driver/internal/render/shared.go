//go:build linux || windows || darwin

package render

import (
	"image"
	"math"
	"sync"

	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
)

// Shared is what every window's renderer shares: the shader programs,
// and the glyph atlas as the CPU sees it.
//
// A program is compiled once, by the first render thread to need it,
// and used by all of them, which saves each new window, and every popup,
// the time it takes to compile. That is safe because the programs hold
// no state a renderer changes: everything a draw needs travels with
// its vertices, and the samplers are set once, as the programs are
// linked. Programs belong to the share group every window's context is
// in.
//
// The glyph atlas is shared as packing and pixels. Each glyph is
// rasterized once for every window, and each window's renderer copies
// the glyphs it draws into a texture of its own. Changing one texture
// from several threads at once would need fences between them; copying
// into textures of their own needs none.
type Shared struct {
	mu    sync.Mutex
	built bool
	err   error
	draw  program
	clip  program
	blur  program
	// dual says the draw program gives a second colour to blend by, for
	// glyphs on subpixels, and header is the start of its source.
	dual   bool
	header string
	// convert is the draw program that converts to a display's colours,
	// built the first time a window needs it; see color.go.
	convert      program
	convertBuilt bool
	convertErr   error
	// atlas holds greyscale glyphs, lcd glyphs rendered for
	// subpixels, and color glyphs in colours of their own.
	atlas, lcd, color sharedAtlas
}

// programs returns the shared programs, building them on first use
// with the calling thread's context.
func (s *Shared) programs(g gl.Context, isES bool) (draw, clip, blur program, dual bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.built {
		s.built = true
		s.draw, s.clip, s.blur, s.dual, s.header, s.err = buildPrograms(g, isES)
		// The other threads may use them as soon as the lock goes, so
		// they must be complete in the share group by then.
		g.Finish()
	}
	return s.draw, s.clip, s.blur, s.dual, s.err
}

// convertProgram returns the program that converts to a display's
// colours, building it on first use with the calling thread's context.
// The other programs must have been built.
func (s *Shared) convertProgram(g gl.Context) (program, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.convertBuilt {
		s.convertBuilt = true
		s.convert, s.convertErr = buildConvert(g, s.header)
		g.Finish()
	}
	return s.convert, s.convertErr
}

// sharedAtlas packs glyph masks into an atlasSize square, in shelves, above the scratch strip at its foot.
// When it fills, it starts again from empty and moves to a new epoch,
// which tells each renderer to clear its texture.
type sharedAtlas struct {
	x, y, rowH int
	epoch      int
	slots      map[glyphKey]sharedSlot
}

// scratchRows is the height of the strip along the atlas's foot that each renderer fills anew every frame, with
// masks drawn for that frame alone.
const scratchRows = 128

// sharedSlot is a glyph's place in the atlas and its mask.
type sharedSlot struct {
	glyphSlot
	pix []byte
}

type glyphKey struct {
	face, id uint32
	// size is the device size in 1/64 pixel.
	size  int32
	shift uint8
	// raster is how the glyph is rendered: hinted, and for subpixels.
	raster text.Raster
	// shape is the shape a mask draws, w by h device pixels, in place of a glyph.
	shape paint.Shape
	w, h  int32
	// color marks a glyph in colours of its own, such as an emoji.
	color bool
}

// glyphSlot is where a glyph sits in the atlas. A zero w marks a glyph
// with nothing to draw, such as a space.
type glyphSlot struct {
	x, y, w, h int
	off        image.Point
}

// glyph returns a glyph's place in the atlas, rasterizing it with raster on first
// use, and the atlas's epoch. It reports false for a glyph with nothing
// to draw.
func (s *Shared) glyph(key glyphKey, raster func() text.Mask) (sharedSlot, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := &s.atlas
	switch {
	case key.color:
		a = &s.color
	case key.raster.LCD:
		a = &s.lcd
	}
	if a.slots == nil {
		a.slots, a.epoch = map[glyphKey]sharedSlot{}, 1
	}
	if slot, ok := a.slots[key]; ok {
		return slot, a.epoch, slot.w > 0
	}
	m := raster()
	const room = atlasSize - scratchRows
	if m.W == 0 || m.W+1 > atlasSize || m.H+1 > room {
		a.slots[key] = sharedSlot{}
		return sharedSlot{}, a.epoch, false
	}
	if a.x+m.W+1 > atlasSize {
		a.x, a.y, a.rowH = 0, a.y+a.rowH, 0
	}
	if a.y+m.H+1 > room {
		clear(a.slots)
		a.x, a.y, a.rowH = 0, 0, 0
		a.epoch++
	}
	slot := sharedSlot{glyphSlot: glyphSlot{x: a.x, y: a.y, w: m.W, h: m.H, off: m.Offset}, pix: m.Pix}
	a.x += m.W + 1
	a.rowH = max(a.rowH, m.H+1)
	a.slots[key] = slot
	return slot, a.epoch, true
}

// glyphKeyFor returns the atlas key for a glyph at a device size and
// subpixel shift, rendered as raster says.
func glyphKeyFor(face, id uint32, sizePx float32, shift uint8, raster text.Raster) glyphKey {
	return glyphKey{face: face, id: id, size: int32(math.Round(float64(sizePx) * 64)), shift: shift, raster: raster}
}
