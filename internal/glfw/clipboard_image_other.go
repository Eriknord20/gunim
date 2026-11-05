// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

// gunim change: this file is gunim's.

//go:build !windows && !js

package glfw

// platformGetClipboardImage finds no picture: reading one from the clipboard is not written yet for X11 or Cocoa.
func platformGetClipboardImage() ([]byte, error) { return nil, nil }
