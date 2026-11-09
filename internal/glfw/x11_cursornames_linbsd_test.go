// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

//go:build freebsd || linux || netbsd

package glfw

import "testing"

func TestEveryStandardCursorHasAThemeNameAndTheResizesAFontCursor(t *testing.T) {
	shapes := []StandardCursor{ArrowCursor, IBeamCursor, CrosshairCursor, HandCursor, HResizeCursor, VResizeCursor,
		ResizeNWSECursor, ResizeNESWCursor, ResizeAllCursor, NotAllowedCursor}
	for _, s := range shapes {
		if len(cursorNames(s)) == 0 {
			t.Errorf("shape %#x has no theme name", int(s))
		}
	}
	// The diagonals had no font cursor, so a display without a named theme showed the arrow at a window's corners.
	for _, s := range []StandardCursor{HResizeCursor, VResizeCursor, ResizeNWSECursor, ResizeNESWCursor} {
		if _, ok := fontCursor(s); !ok {
			t.Errorf("resize shape %#x has no font cursor", int(s))
		}
	}
	if n, _ := fontCursor(ResizeNWSECursor); n != _XC_bottom_right_corner {
		t.Errorf("nwse font cursor = %d, want bottom_right_corner", n)
	}
}
