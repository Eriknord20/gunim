// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

// gunim change: this file is gunim's.

package glfw

import (
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego/objc"

	"github.com/marrasen/gunim/internal/cocoa"
)

var (
	sel_clearContents        = objc.RegisterName("clearContents")
	sel_setData_forType      = objc.RegisterName("setData:forType:")
	sel_dataWithBytes_length = objc.RegisterName("dataWithBytes:length:")

	// nsPasteboardTypePNG is NSPasteboardTypePNG.
	nsPasteboardTypePNG = cocoa.NSString_alloc().InitWithUTF8String("public.png")
)

// platformSetClipboardImage puts img on the general pasteboard as public.png, or empties the pasteboard when img
// holds no picture.
func platformSetClipboardImage(img *ClipboardImage) error {
	pool := cocoa.NSAutoreleasePool_new()
	defer pool.Release()

	pasteboard := objc.ID(class_NSPasteboard).Send(sel_generalPasteboard)
	pasteboard.Send(sel_clearContents)
	if len(img.png) == 0 {
		return nil
	}
	// NSData copies the bytes.
	data := objc.ID(objc.GetClass("NSData")).Send(sel_dataWithBytes_length,
		uintptr(unsafe.Pointer(&img.png[0])), uint(len(img.png)))
	if data == 0 {
		return fmt.Errorf("glfw: failed to make the picture's data for the pasteboard: %w", PlatformError)
	}
	if !objc.Send[bool](pasteboard, sel_setData_forType, data, nsPasteboardTypePNG.ID) {
		return fmt.Errorf("glfw: failed to put the picture on the pasteboard: %w", PlatformError)
	}
	return nil
}
