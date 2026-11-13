//go:build windows

package desktop

import (
	"os"
	"testing"
	"time"
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
	testGlobalSize       = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalSize")
	testGlobalUnlock     = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalUnlock")
)

// openClipboardWithin opens the clipboard, trying for a while, as the history service and rdpclip open it too right
// after a change.
func openClipboardWithin(t *testing.T) {
	t.Helper()
	for range 100 {
		if r, _, _ := testOpenClipboard.Call(0); r != 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the clipboard stayed held by another program for a second")
}

// marks reads the four sensitive marks off the clipboard: whether each is there, its size, and the DWORD it holds.
func marks(t *testing.T) (present [4]bool, sizes [4]uintptr, values [4]uint32) {
	t.Helper()
	openClipboardWithin(t)
	defer func() { _, _, _ = testCloseClipboard.Call() }()
	for i, name := range []string{"ExcludeClipboardContentFromMonitorProcessing", "CanIncludeInClipboardHistory",
		"CanUploadToCloudClipboard", "Clipboard Viewer Ignore"} {
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
		sizes[i], _, _ = testGlobalSize.Call(h)
		b, _, _ := testGlobalLock.Call(h)
		if b != 0 {
			values[i] = *(*uint32)(unsafe.Pointer(b)) //nolint:govet // memory GlobalLock lent
			_, _, _ = testGlobalUnlock.Call(h)
		}
	}
	return present, sizes, values
}

// Sensitive text goes on the real clipboard with the four marks that keep it out of Win+V history and the cloud, plain
// text goes on without them, and ClearClipboardIf takes off only the text it is given. It replaces what the clipboard
// holds, so it runs in CI, or where GUNIM_TEST_CLIPBOARD is set.
func TestSensitiveTextGoesOnTheClipboardMarked(t *testing.T) {
	if os.Getenv("CI") == "" && os.Getenv("GUNIM_TEST_CLIPBOARD") == "" {
		t.Skip("replaces the clipboard; set GUNIM_TEST_CLIPBOARD=1 to run it here")
	}
	// A runner with no interactive desktop has no display; internal/glfw's tests cover the clipboard there.
	if display == nil {
		t.Log("no display: the driver's clipboard is not tested here")
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
	if present, sizes, values := marks(t); present != [4]bool{true, true, true, true} || sizes != [4]uintptr{4, 4, 4, 4} ||
		values != [4]uint32{} {
		t.Fatalf("the marks are there %v, %v bytes, holding %v; want all four, each a DWORD of 0", present, sizes, values)
	}

	if err := w.SetClipboard("a path"); err != nil {
		t.Fatal(err)
	}
	if present, _, _ := marks(t); present != [4]bool{} {
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
