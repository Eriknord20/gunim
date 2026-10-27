// SPDX-License-Identifier: Apache-2.0

package glfw

import "golang.org/x/sys/windows"

var (
	procGetFocus = user32.NewProc("GetFocus")
	procIsChild  = user32.NewProc("IsChild")
)

// takeFocusFromChild gives hWnd the keyboard back where a child window
// of its holds it, as a press on hWnd's own content does in gunim.
//
// A child window may take the keyboard, as a plugin's editor embedded in
// the window does when clicked, from a thread of its own. A press on the
// rest of the window then leaves it there, as Windows moves the keyboard
// only as a window turns active, and the window is active already: the
// keys typed into the field pressed would go to the child.
func takeFocusFromChild(hWnd windows.HWND) {
	f, _, _ := procGetFocus.Call()
	if f == 0 || windows.HWND(f) == hWnd {
		return
	}
	if r, _, _ := procIsChild.Call(uintptr(hWnd), f); r == 0 {
		return
	}
	// Taking the keyboard back is best effort. An error here must not
	// reach WaitEvents, which would stop the program over a press.
	_, _ = _SetFocus(hWnd)
}
