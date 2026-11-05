//go:build !js

// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

// gunim change: this file is gunim's. It tells a key press that typed
// text from one that did not, where modifiers alone leave it unclear:
// AltGr+Q types @ on a German keyboard with Control and Alt held, and
// a terminal must send the @ alone, not Control+Alt+Q as well.

package glfw

// KeyTyped reports whether the key press the key callback is reporting
// also typed text, which follows through the text input callback. A
// dead key, which types with the key after it, counts. It holds only
// inside the key callback.
func (w *Window) KeyTyped() bool { return w.keyTyped }

// inputKeyTyped reports a key, saying whether it typed text.
func (w *Window) inputKeyTyped(key Key, scancode int, action Action, mods ModifierKey, typed bool) {
	w.keyTyped = typed
	w.inputKey(key, scancode, action, mods)
	w.keyTyped = false
}
