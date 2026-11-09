//go:build !js

package text

import "github.com/go-text/typesetting/font"

func platformFace(rune) *font.Face { return nil }
