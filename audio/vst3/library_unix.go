//go:build !windows && !darwin && !js

package vst3

import (
	"runtime"

	"github.com/ebitengine/purego"
)

// library is a shared library loaded.
type library struct {
	handle uintptr
}

// openLibrary loads the library at path and runs its entry, as a VST3
// module on Linux has: ModuleEntry, given its handle.
func openLibrary(path string) (library, error) {
	h, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return library{}, err
	}
	l := library{handle: h}
	if runtime.GOOS == "linux" {
		if entry, err := purego.Dlsym(h, "ModuleEntry"); err == nil {
			if r, _, _ := purego.SyscallN(entry, h); r&0xff == 0 {
				_ = purego.Dlclose(h)
				return library{}, errEntry
			}
		}
	}
	return l, nil
}

func (l library) symbol(name string) (uintptr, error) { return purego.Dlsym(l.handle, name) }

// close runs the module's exit, and unloads it.
func (l library) close() error {
	if l.handle == 0 {
		return nil
	}
	if exit, err := purego.Dlsym(l.handle, "ModuleExit"); err == nil {
		_, _, _ = purego.SyscallN(exit)
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
