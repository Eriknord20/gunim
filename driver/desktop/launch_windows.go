package desktop

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/marrasen/gunim/driver"
)

var (
	launchShell32                  = windows.NewLazySystemDLL("shell32.dll")
	procILCreateFromPath           = launchShell32.NewProc("ILCreateFromPathW")
	procILFree                     = launchShell32.NewProc("ILFree")
	procSHOpenFolderAndSelectItems = launchShell32.NewProc("SHOpenFolderAndSelectItems")
)

// seErrNoAssoc is what ShellExecute returns for a file no program is kept
// for.
const seErrNoAssoc = syscall.Errno(31)

var _ driver.Launcher = (*Window)(nil)

// Open implements [driver.Launcher] with ShellExecute, and asks which
// program to use for a file the system keeps none for.
func (w *Window) Open(path string) error {
	hwnd, err := w.launchOwner()
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("desktop: opening %s: %w", path, err)
	}
	dir, err := windows.UTF16PtrFromString(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("desktop: opening %s: %w", path, err)
	}
	return withCOM(func() error {
		err := windows.ShellExecute(hwnd, nil, file, nil, dir, windows.SW_SHOWNORMAL)
		if errors.Is(err, seErrNoAssoc) {
			openAs, _ := windows.UTF16PtrFromString("openas")
			err = windows.ShellExecute(hwnd, openAs, file, nil, dir, windows.SW_SHOWNORMAL)
		}
		if err != nil {
			return fmt.Errorf("desktop: opening %s: %w", path, err)
		}
		return nil
	})
}

var _ driver.LinkOpener = (*Window)(nil)

// OpenLink implements [driver.LinkOpener] with ShellExecute, which hands
// the page to the browser the system keeps for the web.
func (w *Window) OpenLink(url string) error {
	hwnd, err := w.launchOwner()
	if err != nil {
		return err
	}
	u, err := windows.UTF16PtrFromString(url)
	if err != nil {
		return fmt.Errorf("desktop: opening %s: %w", url, err)
	}
	return withCOM(func() error {
		if err := windows.ShellExecute(hwnd, nil, u, nil, nil, windows.SW_SHOWNORMAL); err != nil {
			return fmt.Errorf("desktop: opening %s: %w", url, err)
		}
		return nil
	})
}

// Reveal implements [driver.Launcher]: File Explorer opens the folder
// that holds path, with path selected. explorer.exe is started for it,
// as the shell would hand the folder to whatever program opens folders,
// which may be the program asking; the shell's own way is kept for when
// explorer.exe can't be started.
func (w *Window) Reveal(path string) error {
	if err := revealInExplorer(filepath.Clean(path)); err == nil {
		return nil
	}
	// The shell finds an item by a path of backslashes alone.
	p, err := windows.UTF16PtrFromString(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("desktop: showing %s: %w", path, err)
	}
	return withCOM(func() error {
		pidl, _, callErr := procILCreateFromPath.Call(uintptr(unsafe.Pointer(p)))
		if pidl == 0 {
			return fmt.Errorf("desktop: showing %s: %w", path, callErr)
		}
		defer func() { _, _, _ = procILFree.Call(pidl) }()
		hr, _, _ := procSHOpenFolderAndSelectItems.Call(pidl, 0, 0, 0)
		if hrFailed(uint32(hr)) {
			return fmt.Errorf("desktop: showing %s: HRESULT %#x", path, uint32(hr))
		}
		return nil
	})
}

// revealInExplorer starts File Explorer on the folder that holds path,
// path selected. Explorer reads its command line itself, so the path is
// quoted as it wants, after the comma.
func revealInExplorer(path string) error {
	dir, err := windows.GetWindowsDirectory()
	if err != nil {
		return err
	}
	exe := filepath.Join(dir, "explorer.exe")
	cmd := exec.CommandContext(context.Background(), exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `"` + exe + `" /select,"` + path + `"`}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// launchOwner returns the window's handle, for the dialogs the shell may
// show while it opens a file.
func (w *Window) launchOwner() (windows.Handle, error) {
	var hwnd windows.Handle
	if err := w.d.call(func() error {
		h, err := w.gw.GetWin32Window()
		hwnd = windows.Handle(h)
		return err
	}); err != nil {
		return 0, fmt.Errorf("desktop: finding the window to open a file from: %w", err)
	}
	return hwnd, nil
}

// withCOM runs fn on a thread with COM started, as the shell's calls want.
func withCOM(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := procChooseCoInit.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE)
	switch {
	case uint32(hr) == rpcEChangedMode:
	case hrFailed(uint32(hr)):
		return fmt.Errorf("desktop: starting COM: HRESULT %#x", uint32(hr))
	default:
		defer func() { _, _, _ = procChooseCoUninit.Call() }()
	}
	return fn()
}
