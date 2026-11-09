// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

//go:build freebsd || linux || netbsd

// gunim change: this file is gunim's.

package glfw

// cursorNames are the names a cursor theme may give shape, the CSS name first and then the older X names that some
// themes have instead.
func cursorNames(shape StandardCursor) []string {
	switch shape {
	case ArrowCursor:
		return []string{"default", "left_ptr"}
	case IBeamCursor:
		return []string{"text", "xterm"}
	case CrosshairCursor:
		return []string{"crosshair", "cross"}
	case HandCursor:
		return []string{"pointer", "hand2", "hand1"}
	case HResizeCursor:
		return []string{"ew-resize", "sb_h_double_arrow", "h_double_arrow"}
	case VResizeCursor:
		return []string{"ns-resize", "sb_v_double_arrow", "v_double_arrow"}
	case ResizeNWSECursor:
		return []string{"nwse-resize", "size_fdiag", "bd_double_arrow", "bottom_right_corner"}
	case ResizeNESWCursor:
		return []string{"nesw-resize", "size_bdiag", "fd_double_arrow", "bottom_left_corner"}
	case ResizeAllCursor:
		return []string{"all-scroll", "fleur", "move"}
	case NotAllowedCursor:
		return []string{"not-allowed", "crossed_circle"}
	}
	return nil
}

// fontCursor is the X core font's cursor for shape, where no theme has one. The core font has no diagonal double
// arrow, so the diagonals take its corner cursors, as window managers do on a window's corners.
func fontCursor(shape StandardCursor) (uint32, bool) {
	switch shape {
	case ArrowCursor:
		return _XC_left_ptr, true
	case IBeamCursor:
		return _XC_xterm, true
	case CrosshairCursor:
		return _XC_crosshair, true
	case HandCursor:
		return _XC_hand2, true
	case HResizeCursor:
		return _XC_sb_h_double_arrow, true
	case VResizeCursor:
		return _XC_sb_v_double_arrow, true
	case ResizeNWSECursor:
		return _XC_bottom_right_corner, true
	case ResizeNESWCursor:
		return _XC_bottom_left_corner, true
	case ResizeAllCursor:
		return _XC_fleur, true
	}
	return 0, false
}
