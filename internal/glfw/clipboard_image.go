// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

// gunim change: this file is gunim's.

//go:build darwin || freebsd || linux || netbsd || windows

package glfw

import (
	"bytes"
	"fmt"
	"image/png"
	"runtime"
)

// A ClipboardImage is a picture made ready for [SetClipboardImage]: the PNG it came from and, where the platform
// keeps a picture as a bitmap too, that bitmap.
type ClipboardImage struct {
	png []byte
	// dib is the picture as a CF_DIBV5 bitmap, on Windows.
	dib []byte
}

// PrepareClipboardImage readies the PNG file's bytes b for [SetClipboardImage]. It does the slow part, decoding the
// picture and writing it again in the form the platform keeps, so it is for a goroutine other than the main one.
// It keeps a copy of b. A nil b readies an empty clipboard.
func PrepareClipboardImage(b []byte) (*ClipboardImage, error) {
	if b == nil {
		return &ClipboardImage{}, nil
	}
	img := &ClipboardImage{png: bytes.Clone(b)}
	if runtime.GOOS != "windows" {
		if _, err := png.DecodeConfig(bytes.NewReader(b)); err != nil {
			return nil, fmt.Errorf("glfw: the clipboard's picture is not a PNG: %w", err)
		}
		return img, nil
	}
	pic, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("glfw: the clipboard's picture is not a PNG: %w", err)
	}
	img.dib = encodeDIBV5(pic)
	return img, nil
}

// SetClipboardImage replaces what the clipboard holds with img, or empties it when img holds no picture. It must
// run on the main thread.
func SetClipboardImage(img *ClipboardImage) error {
	if !_glfw.initialized {
		return NotInitialized
	}
	return platformSetClipboardImage(img)
}
