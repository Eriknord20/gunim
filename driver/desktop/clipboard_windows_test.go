//go:build windows

package desktop

import (
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

var (
	testUser32           = windows.NewLazySystemDLL("user32.dll")
	testOpenClipboard    = testUser32.NewProc("OpenClipboard")
	testCloseClipboard   = testUser32.NewProc("CloseClipboard")
	testRegisterFormat   = testUser32.NewProc("RegisterClipboardFormatW")
	testFormatAvailable  = testUser32.NewProc("IsClipboardFormatAvailable")
	testGetClipboardData = testUser32.NewProc("GetClipboardData")
	testGlobalLock       = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalLock")
	testGlobalUnlock     = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalUnlock")
)

// marks reads the three sensitive marks off the clipboard: whether each is there, and the DWORD the last two hold.
func marks(t *testing.T) (present [3]bool, values [3]uint32) {
	t.Helper()
	if r, _, err := testOpenClipboard.Call(0); r == 0 {
		t.Fatalf("OpenClipboard: %v", err)
	}
	defer func() { _, _, _ = testCloseClipboard.Call() }()
	for i, name := range []string{"ExcludeClipboardContentFromMonitorProcessing", "CanIncludeInClipboardHistory",
		"CanUploadToCloudClipboard"} {
		p, _ := windows.UTF16PtrFromString(name)
		id, _, _ := testRegisterFormat.Call(uintptr(unsafe.Pointer(p)))
		if r, _, _ := testFormatAvailable.Call(id); r == 0 {
			continue
		}
		present[i] = true
		h, _, _ := testGetClipboardData.Call(id)
		if h == 0 {
			continue
		}
		b, _, _ := testGlobalLock.Call(h)
		if b != 0 {
			values[i] = *(*uint32)(unsafe.Pointer(b)) //nolint:govet // memory GlobalLock lent
			_, _, _ = testGlobalUnlock.Call(h)
		}
	}
	return present, values
}

// Sensitive text goes on the real clipboard with the marks that keep it out of Win+V history and the cloud, plain
// text goes on without them, and ClearClipboardIf takes off only the text it is given. It replaces what the clipboard
// holds, so it runs in CI, or where GUNIM_TEST_CLIPBOARD is set.
func TestSensitiveTextGoesOnTheClipboardMarked(t *testing.T) {
	if os.Getenv("CI") == "" && os.Getenv("GUNIM_TEST_CLIPBOARD") == "" {
		t.Skip("replaces the clipboard; set GUNIM_TEST_CLIPBOARD=1 to run it here")
	}
	if display == nil {
		t.Skip("no display")
	}
	dw, err := display.NewWindow(driver.Options{Title: "gunim clipboard", Size: geom.Sz(100, 100), Hidden: true})
	if err != nil {
		t.Fatal(err)
	}
	w := dw.(*Window)
	defer func() { _ = w.Close() }()

	if err := w.SetClipboardSensitive("hunter2"); err != nil {
		t.Fatal(err)
	}
	if s, err := w.Clipboard(); err != nil || s != "hunter2" {
		t.Fatalf("the clipboard holds %q, %v; want the password", s, err)
	}
	if present, values := marks(t); present != [3]bool{true, true, true} || values[1] != 0 || values[2] != 0 {
		t.Fatalf("the marks are there %v, holding %v; want all three, history and cloud at 0", present, values)
	}

	if err := w.SetClipboard("a path"); err != nil {
		t.Fatal(err)
	}
	if present, _ := marks(t); present != [3]bool{} {
		t.Fatalf("plain text carries the marks %v", present)
	}
	if cleared, err := w.ClearClipboardIf("hunter2"); cleared || err != nil {
		t.Fatalf("clearing the password once other text was copied cleared %v, %v", cleared, err)
	}
	if s, _ := w.Clipboard(); s != "a path" {
		t.Fatalf("the clipboard holds %q, want the text copied since", s)
	}

	if err := w.SetClipboardSensitive("hunter2"); err != nil {
		t.Fatal(err)
	}
	if cleared, err := w.ClearClipboardIf("hunter2"); !cleared || err != nil {
		t.Fatalf("clearing the password still there cleared %v, %v", cleared, err)
	}
	if s, err := w.Clipboard(); s != "" {
		t.Fatalf("after clearing, the clipboard holds %q, %v", s, err)
	}
	if cleared, err := w.ClearClipboardIf("hunter2"); cleared || err != nil {
		t.Fatalf("clearing an empty clipboard cleared %v, %v", cleared, err)
	}
}
