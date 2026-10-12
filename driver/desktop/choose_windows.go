package desktop

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/marrasen/gunim/driver"
)

var (
	chooseOle32        = windows.NewLazySystemDLL("ole32.dll")
	procChooseCoInit   = chooseOle32.NewProc("CoInitializeEx")
	procChooseCoUninit = chooseOle32.NewProc("CoUninitialize")
	procChooseCoCreate = chooseOle32.NewProc("CoCreateInstance")
	procChooseCoFree   = chooseOle32.NewProc("CoTaskMemFree")

	chooseShell32          = windows.NewLazySystemDLL("shell32.dll")
	procChooseItemFromPath = chooseShell32.NewProc("SHCreateItemFromParsingName")

	clsidFileOpenDialog = windows.GUID{Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE,
		Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidFileOpenDialog = windows.GUID{Data1: 0xD57C7288, Data2: 0xD4AD, Data3: 0x4768,
		Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
	clsidFileSaveDialog = windows.GUID{Data1: 0xC0B4E2F3, Data2: 0xBA21, Data3: 0x4773,
		Data4: [8]byte{0x8D, 0xBA, 0x33, 0x5E, 0xC9, 0x46, 0xEB, 0x8B}}
	iidFileSaveDialog = windows.GUID{Data1: 0x84BCCD23, Data2: 0x5FDE, Data3: 0x4CDB,
		Data4: [8]byte{0xAE, 0xA4, 0xAF, 0x64, 0xB8, 0x3D, 0x78, 0xAB}}
	iidShellItem = windows.GUID{Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE,
		Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
)

// COM and file dialog constants.
const (
	coinitApartmentThreaded = 0x2
	coinitDisableOLE1DDE    = 0x4
	clsctxInprocServer      = 0x1
	fosOverwritePrompt      = 0x2
	fosPickFolders          = 0x20
	fosForceFileSystem      = 0x40
	fosAllowMultiSelect     = 0x200
	fosPathMustExist        = 0x800
	fosFileMustExist        = 0x1000
	sigdnFileSysPath        = 0x80058000
	hresultCancelled        = 0x800704C7
	rpcEChangedMode         = 0x80010106
)

// The methods called, by their place in each interface's table.
const (
	vtRelease        = 2
	vtShow           = 3
	vtSetFileTypes   = 4
	vtSetOptions     = 9
	vtGetOptions     = 10
	vtSetFolder      = 12
	vtSetFileName    = 15
	vtSetTitle       = 17
	vtGetResult      = 20
	vtGetResults     = 27
	vtArrayGetCount  = 7
	vtArrayGetItemAt = 8
	vtItemGetName    = 5
)

// filterSpec is COMDLG_FILTERSPEC.
type filterSpec struct {
	name, spec *uint16
}

// comCall calls method index of the COM object obj.
func comCall(obj unsafe.Pointer, index int, args ...uintptr) uint32 {
	table := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(table, uintptr(index)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return uint32(r)
}

func hrFailed(hr uint32) bool { return int32(hr) < 0 }

var (
	_ driver.FileChooser = (*Window)(nil)
	_ driver.FileSaver   = (*Window)(nil)
)

// dialog describes a file dialog to show: which one, its title, filters
// and options, and the folder and file name it starts with.
type dialog struct {
	clsid, iid *windows.GUID
	title      string
	filters    []driver.FileFilter
	options    uint32
	folder     string
	name       string
}

// showDialog shows d owned by w, and runs chosen with the dialog once the
// user has chosen; a cancelled dialog runs nothing.
func (w *Window) showDialog(d dialog, chosen func(dlg unsafe.Pointer) error) error {
	var hwnd uintptr
	if err := w.d.call(func() error {
		h, err := w.gw.GetWin32Window()
		hwnd = uintptr(h)
		return err
	}); err != nil {
		return fmt.Errorf("desktop: finding the window for a file dialog: %w", err)
	}

	// COM belongs to a thread, and the dialog runs its own message loop on it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := procChooseCoInit.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE)
	switch {
	case uint32(hr) == rpcEChangedMode:
	case hrFailed(uint32(hr)):
		return fmt.Errorf("desktop: starting COM for a file dialog: HRESULT %#x", uint32(hr))
	default:
		defer func() { _, _, _ = procChooseCoUninit.Call() }()
	}

	var dlg unsafe.Pointer
	hr, _, _ = procChooseCoCreate.Call(uintptr(unsafe.Pointer(d.clsid)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(d.iid)), uintptr(unsafe.Pointer(&dlg)))
	if hrFailed(uint32(hr)) {
		return fmt.Errorf("desktop: making a file dialog: HRESULT %#x", uint32(hr))
	}
	defer comCall(dlg, vtRelease)

	var opts uint32
	if hr := comCall(dlg, vtGetOptions, uintptr(unsafe.Pointer(&opts))); hrFailed(hr) {
		return fmt.Errorf("desktop: reading the file dialog's options: HRESULT %#x", hr)
	}
	if hr := comCall(dlg, vtSetOptions, uintptr(opts|d.options)); hrFailed(hr) {
		return fmt.Errorf("desktop: setting the file dialog's options: HRESULT %#x", hr)
	}
	if err := setText(dlg, vtSetTitle, d.title, "title"); err != nil {
		return err
	}
	setFolder(dlg, d.folder)
	if err := setText(dlg, vtSetFileName, d.name, "file name"); err != nil {
		return err
	}
	if len(d.filters) > 0 {
		specs := make([]filterSpec, len(d.filters))
		for i, f := range d.filters {
			name, err := windows.UTF16PtrFromString(f.Name)
			if err != nil {
				return fmt.Errorf("desktop: the file dialog's filter %q: %w", f.Name, err)
			}
			spec, err := windows.UTF16PtrFromString(strings.Join(f.Patterns, ";"))
			if err != nil {
				return fmt.Errorf("desktop: the file dialog's filter %q: %w", f.Name, err)
			}
			specs[i] = filterSpec{name, spec}
		}
		if hr := comCall(dlg, vtSetFileTypes, uintptr(len(specs)), uintptr(unsafe.Pointer(&specs[0]))); hrFailed(hr) {
			return fmt.Errorf("desktop: setting the file dialog's filters: HRESULT %#x", hr)
		}
	}

	switch hr := comCall(dlg, vtShow, hwnd); {
	case hr == hresultCancelled:
		return nil
	case hrFailed(hr):
		return fmt.Errorf("desktop: showing the file dialog: HRESULT %#x", hr)
	}
	return chosen(dlg)
}

// setFolder opens the dialog in folder; an empty folder, or one that is
// not there, leaves the dialog where the system opens it.
func setFolder(dlg unsafe.Pointer, folder string) {
	if folder == "" {
		return
	}
	p, err := windows.UTF16PtrFromString(folder)
	if err != nil {
		return
	}
	var item unsafe.Pointer
	hr, _, _ := procChooseItemFromPath.Call(uintptr(unsafe.Pointer(p)), 0,
		uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&item)))
	if hrFailed(uint32(hr)) {
		return
	}
	defer comCall(item, vtRelease)
	comCall(dlg, vtSetFolder, uintptr(item))
}

// setText calls the dialog's method index with s, for its title or file
// name, and leaves it alone for an empty s.
func setText(dlg unsafe.Pointer, index int, s, what string) error {
	if s == "" {
		return nil
	}
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return fmt.Errorf("desktop: the file dialog's %s: %w", what, err)
	}
	if hr := comCall(dlg, index, uintptr(unsafe.Pointer(p))); hrFailed(hr) {
		return fmt.Errorf("desktop: setting the file dialog's %s: HRESULT %#x", what, hr)
	}
	return nil
}

// ChooseFiles implements [driver.FileChooser] with the Windows file
// dialog, IFileOpenDialog.
func (w *Window) ChooseFiles(o driver.ChooseOptions) ([]string, error) {
	d := dialog{clsid: &clsidFileOpenDialog, iid: &iidFileOpenDialog, title: o.Title, folder: o.Folder,
		options: fosForceFileSystem | fosPathMustExist | fosFileMustExist}
	if o.Multiple {
		d.options |= fosAllowMultiSelect
	}
	if o.Folders {
		d.options |= fosPickFolders
	} else {
		d.filters = o.Filters
	}
	var paths []string
	err := w.showDialog(d, func(dlg unsafe.Pointer) error {
		var err error
		paths, err = dialogResults(dlg)
		return err
	})
	return paths, err
}

// SaveFile implements [driver.FileSaver] with the Windows file dialog,
// IFileSaveDialog.
func (w *Window) SaveFile(o driver.SaveOptions) (string, error) {
	d := dialog{clsid: &clsidFileSaveDialog, iid: &iidFileSaveDialog, title: o.Title, name: o.Name,
		folder: o.Folder, filters: o.Filters, options: fosForceFileSystem | fosPathMustExist | fosOverwritePrompt}
	var path string
	err := w.showDialog(d, func(dlg unsafe.Pointer) error {
		var item unsafe.Pointer
		if hr := comCall(dlg, vtGetResult, uintptr(unsafe.Pointer(&item))); hrFailed(hr) {
			return fmt.Errorf("desktop: reading where the file dialog saves: HRESULT %#x", hr)
		}
		defer comCall(item, vtRelease)
		var err error
		path, err = itemPath(item)
		return err
	})
	return path, err
}

// itemPath returns the path of an IShellItem.
func itemPath(item unsafe.Pointer) (string, error) {
	var name *uint16
	if hr := comCall(item, vtItemGetName, sigdnFileSysPath, uintptr(unsafe.Pointer(&name))); hrFailed(hr) {
		return "", fmt.Errorf("desktop: reading a path from the file dialog: HRESULT %#x", hr)
	}
	defer func() { _, _, _ = procChooseCoFree.Call(uintptr(unsafe.Pointer(name))) }()
	return windows.UTF16PtrToString(name), nil
}

// dialogResults returns the paths chosen in an IFileOpenDialog.
func dialogResults(dlg unsafe.Pointer) ([]string, error) {
	var items unsafe.Pointer
	if hr := comCall(dlg, vtGetResults, uintptr(unsafe.Pointer(&items))); hrFailed(hr) {
		return nil, fmt.Errorf("desktop: reading what the file dialog chose: HRESULT %#x", hr)
	}
	defer comCall(items, vtRelease)
	var n uint32
	if hr := comCall(items, vtArrayGetCount, uintptr(unsafe.Pointer(&n))); hrFailed(hr) {
		return nil, fmt.Errorf("desktop: counting what the file dialog chose: HRESULT %#x", hr)
	}
	out := make([]string, 0, n)
	for i := range int(n) {
		var item unsafe.Pointer
		if hr := comCall(items, vtArrayGetItemAt, uintptr(i), uintptr(unsafe.Pointer(&item))); hrFailed(hr) {
			return nil, fmt.Errorf("desktop: reading choice %d of the file dialog: HRESULT %#x", i, hr)
		}
		p, err := itemPath(item)
		comCall(item, vtRelease)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
