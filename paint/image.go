package paint

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"strconv"

	"github.com/marrasen/gunim/geom"
)

// An Image is a picture to draw: pixels held in memory, which never
// change once made.
//
// It travels by reference. A state that carries an *Image hands the
// window the same pixels the application decoded, with no copy, and
// the driver uploads them to the GPU once and keeps the texture for as
// long as frames draw the image. A new picture is a new Image.
type Image struct {
	w, h int
	// pix is premultiplied RGBA, four bytes a pixel, row by row from
	// the top.
	pix []byte
}

// NewImage copies m into a new Image.
func NewImage(m image.Image) *Image {
	b := m.Bounds()
	rgba, ok := m.(*image.RGBA)
	if !ok || rgba.Stride != 4*b.Dx() || b.Min != (image.Point{}) {
		// image.RGBA is premultiplied, and draw converts anything else.
		rgba = image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(rgba, rgba.Bounds(), m, b.Min, draw.Src)
		return &Image{w: b.Dx(), h: b.Dy(), pix: rgba.Pix}
	}
	return &Image{w: b.Dx(), h: b.Dy(), pix: bytes.Clone(rgba.Pix)}
}

// Size returns the image's size in pixels.
func (m *Image) Size() (w, h int) { return m.w, m.h }

// Pix returns the pixels, premultiplied RGBA row by row from the top,
// for a driver to upload. They must not be changed.
func (m *Image) Pix() []byte { return m.pix }

// EncodePNG writes the image to w as a PNG.
func (m *Image) EncodePNG(w io.Writer) error {
	rgba := &image.RGBA{Pix: m.pix, Stride: 4 * m.w, Rect: image.Rect(0, 0, m.w, m.h)}
	if err := png.Encode(w, rgba); err != nil {
		return fmt.Errorf("paint: encode image: %w", err)
	}
	return nil
}

// MarshalJSON encodes the image as a PNG in a JSON string, so a state
// that carries one can cross a socket transport.
func (m *Image) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	if err := m.EncodePNG(&buf); err != nil {
		return nil, err
	}
	return strconv.AppendQuote(nil, base64.StdEncoding.EncodeToString(buf.Bytes())), nil
}

// UnmarshalJSON decodes an image MarshalJSON encoded.
func (m *Image) UnmarshalJSON(data []byte) error {
	s, err := strconv.Unquote(string(data))
	if err != nil {
		return fmt.Errorf("paint: decode image: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return fmt.Errorf("paint: decode image: %w", err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("paint: decode image: %w", err)
	}
	*m = *NewImage(img)
	return nil
}

// ImageOp draws an image, or the part of it Src picks, into Rect.
type ImageOp struct {
	Image *Image
	Rect  geom.Rect
	// Src is the part of the image to draw, in image pixels. The zero
	// Rect draws all of it.
	Src     geom.Rect
	Radius  float32
	Opacity float32
	// Transform is the transform in force when the op was recorded.
	Transform Transform
	// Blend is how the image meets what is beneath it, set from the
	// blend in force as the op is recorded; see [Painter.Blend]. An
	// image added with [BlendAdd] adds its colours, as a picture of a
	// flare on black does.
	Blend Blend
}

func (*ImageOp) isOp() {}

// ImageOpts are the options for [Painter.Image].
type ImageOpts struct {
	// Src is the part of the image to draw, in image pixels. The zero
	// Rect draws all of it.
	Src geom.Rect
	// Radius rounds the corners of the drawn rectangle.
	Radius float32
	// Opacity multiplies the image's own alpha. The zero value draws
	// nothing, so an image drawn as it is takes 1.
	Opacity float32
}

// Image records img drawn into r.
func (p *Painter) Image(img *Image, r geom.Rect, o ImageOpts) {
	if img == nil || o.Opacity <= 0 {
		return
	}
	p.record(&ImageOp{Image: img, Rect: r, Src: o.Src, Radius: o.Radius, Opacity: o.Opacity, Transform: p.at(),
		Blend: p.blendNow()}, r)
}
