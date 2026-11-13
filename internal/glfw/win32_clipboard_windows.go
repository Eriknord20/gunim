// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

// gunim change: this file is gunim's. The Ebitengine port left the Win32
// clipboard unimplemented, and panicked on any use of it. This follows
// C GLFW's win32_window.c.

package glfw

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	_CF_UNICODETEXT = 13
	_GMEM_MOVEABLE  = 0x0002
)

var (
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procGetClipboardData = user32.NewProc("GetClipboardData")
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")

	procGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	procGlobalFree   = kernel32.NewProc("GlobalFree")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
)

// openClipboard opens the clipboard for the helper window. Another
// process may hold it for a moment, so it tries a few times.
func openClipboard() error {
	var err error
	for range 3 {
		r, _, e := procOpenClipboard.Call(uintptr(_glfw.platformWindow.helperWindowHandle))
		if r != 0 {
			return nil
		}
		err = e
		time.Sleep(time.Millisecond)
	}
	return fmt.Errorf("glfw: failed to open clipboard: %w", err)
}

func closeClipboard() {
	_, _, _ = procCloseClipboard.Call()
}

func platformSetClipboardString(str string) error { return setClipboardText(str, false) }

// SetClipboardStringSensitive puts str on the clipboard marked as sensitive, so Windows keeps it out of the clipboard
// history and the cloud clipboard, and clipboard monitors are asked to skip it. An empty str empties the clipboard.
// It must be called on the main thread.
func SetClipboardStringSensitive(str string) error {
	if !_glfw.initialized {
		return NotInitialized
	}
	return setClipboardText(str, true)
}

// ClearClipboardStringIf empties the clipboard if it holds the text str, and reports whether it did. It holds the
// clipboard open from the read to the emptying, so no other program can put something there in between. It must be
// called on the main thread.
func ClearClipboardStringIf(str string) (bool, error) {
	if !_glfw.initialized {
		return false, NotInitialized
	}
	if err := openClipboard(); err != nil {
		return false, err
	}
	defer closeClipboard()
	now, err := clipboardText()
	if errors.Is(err, FormatUnavailable) {
		return false, nil
	}
	if err != nil || now != str {
		return false, err
	}
	if r, _, e := procEmptyClipboard.Call(); r == 0 {
		return false, fmt.Errorf("glfw: failed to empty the clipboard: %w", e)
	}
	return true, nil
}

// Clipboard formats that mark what is on the clipboard as sensitive. Windows reads the first by its presence, and
// the other two as a DWORD, where 0 says no.
const (
	excludeFromMonitors = "ExcludeClipboardContentFromMonitorProcessing"
	includeInHistory    = "CanIncludeInClipboardHistory"
	uploadToCloud       = "CanUploadToCloudClipboard"
)

// sensitiveFormats are the formats a sensitive text is put with, and the DWORD each holds.
var sensitiveFormats = []struct {
	name  string
	value uint32
}{{excludeFromMonitors, 0}, {includeInHistory, 0}, {uploadToCloud, 0}}

// globalBytes copies b into new movable global memory, for the clipboard to own.
func globalBytes(b []byte) (uintptr, error) {
	object, _, e := procGlobalAlloc.Call(_GMEM_MOVEABLE, uintptr(max(len(b), 1)))
	if object == 0 {
		return 0, fmt.Errorf("glfw: failed to allocate global handle for clipboard: %w", e)
	}
	buffer, _, e := procGlobalLock.Call(object)
	if buffer == 0 {
		_, _, _ = procGlobalFree.Call(object)
		return 0, fmt.Errorf("glfw: failed to lock global handle: %w", e)
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(buffer)), len(b)), b)
	_, _, _ = procGlobalUnlock.Call(object)
	return object, nil
}

// setClipboardData hands object to the clipboard in format, which owns it once this succeeds. It must be called with
// the clipboard open.
func setClipboardData(format, object uintptr) error {
	if r, _, e := procSetClipboardData.Call(format, object); r == 0 {
		_, _, _ = procGlobalFree.Call(object)
		return fmt.Errorf("glfw: failed to set clipboard data: %w", e)
	}
	return nil
}

// setClipboardText replaces the clipboard with str, and with sensitive marks it so. A sensitive text whose marks
// cannot all be set is taken off again, and the error returned.
func setClipboardText(str string, sensitive bool) error {
	text, err := windows.UTF16FromString(str)
	if err != nil {
		return errors.New("glfw: the text holds a NUL character, which the clipboard cannot hold")
	}
	formats := make([]uintptr, len(sensitiveFormats))
	if sensitive {
		for i, f := range sensitiveFormats {
			name, err := windows.UTF16PtrFromString(f.name)
			if err != nil {
				return err
			}
			id, _, e := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(name)))
			if id == 0 {
				return fmt.Errorf("glfw: failed to register the clipboard format %s: %w", f.name, e)
			}
			formats[i] = id
		}
	}
	object, err := globalBytes(unsafe.Slice((*byte)(unsafe.Pointer(&text[0])), len(text)*2))
	if err != nil {
		return err
	}
	if err := openClipboard(); err != nil {
		_, _, _ = procGlobalFree.Call(object)
		return err
	}
	defer closeClipboard()
	if r, _, e := procEmptyClipboard.Call(); r == 0 {
		_, _, _ = procGlobalFree.Call(object)
		return fmt.Errorf("glfw: failed to empty the clipboard: %w", e)
	}
	if sensitive && str == "" {
		_, _, _ = procGlobalFree.Call(object)
		return nil
	}
	if !sensitive {
		return setClipboardData(_CF_UNICODETEXT, object)
	}
	// Other programs see the clipboard only once it closes, with the marks and the text together.
	for i, f := range sensitiveFormats {
		mark, err := globalBytes(binaryDWORD(f.value))
		if err == nil {
			err = setClipboardData(formats[i], mark)
		}
		if err != nil {
			_, _, _ = procGlobalFree.Call(object)
			_, _, _ = procEmptyClipboard.Call()
			return err
		}
	}
	if err := setClipboardData(_CF_UNICODETEXT, object); err != nil {
		_, _, _ = procEmptyClipboard.Call()
		return err
	}
	return nil
}

// binaryDWORD is v as a little-endian DWORD.
func binaryDWORD(v uint32) []byte { return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)} }

func platformGetClipboardString() (string, error) {
	if err := openClipboard(); err != nil {
		return "", err
	}
	defer closeClipboard()
	return clipboardText()
}

// clipboardText reads the clipboard's text. It must be called with the clipboard open.
func clipboardText() (string, error) {
	object, _, _ := procGetClipboardData.Call(_CF_UNICODETEXT)
	if object == 0 {
		return "", fmt.Errorf("glfw: failed to convert clipboard to string: %w", FormatUnavailable)
	}
	buffer, _, e := procGlobalLock.Call(object)
	if buffer == 0 {
		return "", fmt.Errorf("glfw: failed to lock global handle: %w", e)
	}
	defer procGlobalUnlock.Call(object)
	return windows.UTF16PtrToString((*uint16)(unsafe.Pointer(buffer))), nil
}

// Clipboard formats that hold a picture, besides PNG, which has no number of its own.
const (
	_CF_DIB   = 8
	_CF_DIBV5 = 17
)

var (
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
)

// platformGetClipboardImage returns the clipboard's picture as PNG: its PNG as it is, which browsers and the
// Snipping Tool put there, or else its bitmap, which Print Screen puts there, encoded.
func platformGetClipboardImage() ([]byte, error) {
	name, err := windows.UTF16PtrFromString("PNG")
	if err != nil {
		return nil, err
	}
	pngFormat, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(name)))
	if err := openClipboard(); err != nil {
		return nil, err
	}
	defer closeClipboard()
	if pngFormat != 0 {
		data, ok, err := clipboardBytes(pngFormat)
		if err != nil || ok {
			return data, err
		}
	}
	for _, format := range []uintptr{_CF_DIBV5, _CF_DIB} {
		data, ok, err := clipboardBytes(format)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		img, err := decodeDIB(data)
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		if err := enc.Encode(&b, img); err != nil {
			return nil, fmt.Errorf("glfw: encoding the clipboard's picture: %w", err)
		}
		return b.Bytes(), nil
	}
	return nil, nil
}

// clipboardBytes returns a copy of the open clipboard's data in format, and false when it holds none.
func clipboardBytes(format uintptr) ([]byte, bool, error) {
	if r, _, _ := procIsClipboardFormatAvailable.Call(format); r == 0 {
		return nil, false, nil
	}
	object, _, e := procGetClipboardData.Call(format)
	if object == 0 {
		return nil, false, fmt.Errorf("glfw: failed to get clipboard data: %w", e)
	}
	size, _, _ := procGlobalSize.Call(object)
	buffer, _, e := procGlobalLock.Call(object)
	if buffer == 0 {
		return nil, false, fmt.Errorf("glfw: failed to lock global handle: %w", e)
	}
	defer procGlobalUnlock.Call(object)
	out := make([]byte, size)
	copy(out, unsafe.Slice((*byte)(unsafe.Pointer(buffer)), size))
	return out, true, nil
}

// platformSetClipboardImage puts img on the clipboard twice over: as CF_DIBV5, which every program that pastes a
// picture reads, and Windows turns into CF_DIB and CF_BITMAP for older ones, and as the registered "PNG" format,
// which browsers and Office prefer and which keeps alpha as it is. An img with no picture empties the clipboard.
func platformSetClipboardImage(img *ClipboardImage) error {
	var pngFormat uintptr
	if img.png != nil {
		name, err := windows.UTF16PtrFromString("PNG")
		if err != nil {
			return err
		}
		// Without a "PNG" format, which is never expected, the bitmap alone carries the picture.
		pngFormat, _, _ = procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(name)))
	}
	type item struct {
		format uintptr
		data   []byte
		object uintptr
	}
	var items []item
	for _, it := range []item{{format: _CF_DIBV5, data: img.dib}, {format: pngFormat, data: img.png}} {
		if it.format != 0 && it.data != nil {
			items = append(items, it)
		}
	}
	free := func(items []item) {
		for _, it := range items {
			if it.object != 0 {
				_, _, _ = procGlobalFree.Call(it.object)
			}
		}
	}
	for i := range items {
		o, err := globalCopy(items[i].data)
		if err != nil {
			free(items)
			return err
		}
		items[i].object = o
	}

	if err := openClipboard(); err != nil {
		free(items)
		return err
	}
	defer closeClipboard()
	if r, _, e := procEmptyClipboard.Call(); r == 0 {
		free(items)
		return fmt.Errorf("glfw: failed to empty clipboard: %w", e)
	}
	for i, it := range items {
		// The clipboard owns the memory once this succeeds.
		if r, _, e := procSetClipboardData.Call(it.format, it.object); r == 0 {
			free(items[i:])
			return fmt.Errorf("glfw: failed to set clipboard data: %w", e)
		}
	}
	return nil
}

// globalCopy returns a movable global memory object holding a copy of data, for the clipboard to own.
func globalCopy(data []byte) (uintptr, error) {
	object, _, e := procGlobalAlloc.Call(_GMEM_MOVEABLE, uintptr(len(data)))
	if object == 0 {
		return 0, fmt.Errorf("glfw: failed to allocate global handle for clipboard: %w", e)
	}
	buffer, _, e := procGlobalLock.Call(object)
	if buffer == 0 {
		_, _, _ = procGlobalFree.Call(object)
		return 0, fmt.Errorf("glfw: failed to lock global handle: %w", e)
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(buffer)), len(data)), data)
	_, _, _ = procGlobalUnlock.Call(object)
	return object, nil
}
