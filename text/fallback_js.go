//go:build js && wasm

package text

import (
	"bytes"
	_ "embed"

	"github.com/go-text/typesetting/font"
)

// Browser Go programs cannot scan the host font directory. Keep common UI
// symbols available to all faces, after application fallbacks. SIL OFL 1.1.
//
//go:embed fonts/NotoSansSymbols2-Regular.ttf
var symbolData []byte

//go:embed fonts/NotoSansSymbols-Regular.ttf
var symbolData2 []byte
var browserFaces []*font.Face
var browserFontsTried bool

// Called with the text package mutex held.
func platformFace(r rune) *font.Face {
	if !systemOn {
		return nil
	}
	if !browserFontsTried {
		browserFontsTried = true
		for _, data := range [][]byte{symbolData, symbolData2} {
			if f, err := font.ParseTTF(bytes.NewReader(data)); err == nil {
				browserFaces = append(browserFaces, f)
				faceOf(f)
			}
		}
	}
	for _, f := range browserFaces {
		if _, ok := f.NominalGlyph(r); ok {
			return f
		}
	}
	return nil
}
