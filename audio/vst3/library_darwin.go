package vst3

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// library is a shared library loaded, and the CFBundle of the .vst3 folder that holds it.
type library struct {
	handle uintptr
	bundle uintptr
}

// coreFoundation is the CoreFoundation calls a module's entry needs.
var coreFoundation struct {
	once      sync.Once
	err       error
	urlCreate func(alloc uintptr, path *byte, n int, dir bool) uintptr
	create    func(alloc, url uintptr) uintptr
	release   func(ref uintptr)
}

// loadCoreFoundation loads CoreFoundation once.
func loadCoreFoundation() error {
	cf := &coreFoundation
	cf.once.Do(func() {
		h, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			cf.err = err
			return
		}
		purego.RegisterLibFunc(&cf.urlCreate, h, "CFURLCreateFromFileSystemRepresentation")
		purego.RegisterLibFunc(&cf.create, h, "CFBundleCreate")
		purego.RegisterLibFunc(&cf.release, h, "CFRelease")
	})
	return cf.err
}

// newBundle makes a CFBundle for the folder at path.
func newBundle(path string) (uintptr, error) {
	if err := loadCoreFoundation(); err != nil {
		return 0, err
	}
	cf := &coreFoundation
	b := []byte(path)
	url := cf.urlCreate(0, unsafe.SliceData(b), len(b), true)
	if url == 0 {
		return 0, errors.New("no CFURL for " + path)
	}
	defer cf.release(url)
	bundle := cf.create(0, url)
	if bundle == 0 {
		return 0, errors.New("no CFBundle for " + path)
	}
	return bundle, nil
}

// openLibrary loads the library at path and runs its entry, as a VST3 module on macOS has: bundleEntry, given the
// CFBundle of its .vst3 folder.
func openLibrary(path string) (library, error) {
	h, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return library{}, err
	}
	l := library{handle: h}
	dir := bundleOf(path)
	if entry, err := purego.Dlsym(h, "bundleEntry"); err == nil && dir != "" {
		if l.bundle, err = newBundle(dir); err != nil {
			_ = purego.Dlclose(h)
			return library{}, err
		}
		if r, _, _ := purego.SyscallN(entry, l.bundle); r&0xff == 0 {
			coreFoundation.release(l.bundle)
			_ = purego.Dlclose(h)
			return library{}, errEntry
		}
	}
	return l, nil
}

func (l library) symbol(name string) (uintptr, error) { return purego.Dlsym(l.handle, name) }

// close runs the module's exit, bundleExit, and unloads it.
func (l library) close() error {
	if l.handle == 0 {
		return nil
	}
	if l.bundle != 0 {
		if exit, err := purego.Dlsym(l.handle, "bundleExit"); err == nil {
			_, _, _ = purego.SyscallN(exit)
		}
		coreFoundation.release(l.bundle)
	}
	return purego.Dlclose(l.handle)
}

// callFunc calls the function fn with no arguments.
func callFunc(fn uintptr) uintptr {
	r, _, _ := purego.SyscallN(fn)
	return r
}

// base is nothing here: only Windows on x64 lives through a plugin's crash.
func (l library) base() uintptr { return 0 }
