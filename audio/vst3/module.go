package vst3

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"
)

// A Module is a plugin's library, loaded: its factory, and the classes it
// makes.
type Module struct {
	// Path is the bundle or file the module was loaded from.
	Path string
	// Vendor is who made it, as its factory says.
	Vendor  string
	lib     library
	factory uintptr
	// Classes are what the module makes.
	Classes []Class
}

// Class is a kind of object a module makes: for a host, the audio
// effects and the instruments.
type Class struct {
	ID uid
	// Name is the plugin's name, Category its kind, as "Audio Module
	// Class" for an effect or an instrument, and SubCategories what it
	// does, as "Fx|EQ" or "Instrument|Synth".
	Name, Category, SubCategories string
	Vendor, Version               string
	// ARA says the module makes an ARA factory of the class's name, as
	// Melodyne's does: the class binds to ARA documents.
	ARA bool
}

// IDString writes the class's ID as hexadecimal, as a project keeps it.
func (c Class) IDString() string { return fmt.Sprintf("%X", c.ID[:]) }

// Audio says whether the class is an audio component, which a host runs:
// an effect or an instrument.
func (c Class) Audio() bool { return c.Category == "Audio Module Class" }

// Instrument says whether the class is an instrument: an audio
// component whose first subcategory is "Instrument", as in
// "Instrument|Synth". An instrument plays the notes it takes.
func (c Class) Instrument() bool {
	first, _, _ := strings.Cut(c.SubCategories, "|")
	return c.Audio() && first == "Instrument"
}

// Effect says whether the class is an audio effect, a component a host
// runs sound through: an audio component other than an instrument.
func (c Class) Effect() bool { return c.Audio() && !c.Instrument() }

// binaryPath returns the library within a bundle, for this platform, or
// path itself where it is a file.
func binaryPath(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return path, nil
	}
	name := strings.TrimSuffix(filepath.Base(path), ".vst3")
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i386"}[runtime.GOARCH]
	var lib string
	switch runtime.GOOS {
	case "windows":
		if runtime.GOARCH == "arm64" {
			arch = "arm64"
		}
		lib = filepath.Join(path, "Contents", arch+"-win", name+".vst3")
	case "darwin":
		lib = filepath.Join(path, "Contents", "MacOS", name)
	default:
		lib = filepath.Join(path, "Contents", arch+"-linux", name+".so")
	}
	if _, err := os.Stat(lib); err != nil {
		return "", fmt.Errorf("vst3: %s holds no library for this platform: %w", path, err)
	}
	return lib, nil
}

// Open loads the plugin at path, a .vst3 bundle or file, and reads the
// classes its factory makes.
func Open(path string) (*Module, error) {
	bin, err := binaryPath(path)
	if err != nil {
		return nil, err
	}
	var m *Module
	onUI(func() { m, err = loadModule(path, bin) })
	return m, err
}

// loadModule loads the library bin of the plugin at path, on the
// plugins' thread, as a module expects to be loaded.
func loadModule(path, bin string) (*Module, error) {
	lib, err := openLibrary(bin)
	if err != nil {
		return nil, fmt.Errorf("vst3: %s: %w", path, err)
	}
	m := &Module{Path: path, lib: lib}
	getFactory, err := lib.symbol("GetPluginFactory")
	if err != nil {
		_ = lib.close()
		return nil, fmt.Errorf("vst3: %s: no GetPluginFactory: %w", path, err)
	}
	m.factory = callFunc(getFactory)
	if m.factory == 0 {
		_ = lib.close()
		return nil, fmt.Errorf("vst3: %s: GetPluginFactory gave nothing", path)
	}
	// The factory's own words, and the host's context, where it takes
	// one.
	var info struct {
		vendor [64]byte
		url    [256]byte
		email  [128]byte
		flags  int32
	}
	if result(call(m.factory, mGetFactoryInfo, uintptr(unsafe.Pointer(&info)))) == resultOK {
		m.Vendor = cstring(info.vendor[:])
	}
	if f3 := query(m.factory, iidPluginFactory3); f3 != 0 {
		call(f3, mSetHostContext, theHost())
		release(f3)
	}
	f2 := query(m.factory, iidPluginFactory2)
	defer release(f2)
	n := int(int32(call(m.factory, mCountClasses)))
	for i := range n {
		c := Class{}
		if f2 != 0 {
			var ci struct {
				cid         uid
				cardinality int32
				category    [32]byte
				name        [64]byte
				flags       uint32
				subCats     [128]byte
				vendor      [64]byte
				version     [64]byte
				sdkVersion  [64]byte
			}
			if result(call(f2, mGetClassInfo2, uintptr(i), uintptr(unsafe.Pointer(&ci)))) == resultOK {
				c = Class{ID: ci.cid, Name: cstring(ci.name[:]), Category: cstring(ci.category[:]),
					SubCategories: cstring(ci.subCats[:]), Vendor: cstring(ci.vendor[:]), Version: cstring(ci.version[:])}
			}
		}
		if c.Name == "" {
			var ci struct {
				cid         uid
				cardinality int32
				category    [32]byte
				name        [64]byte
			}
			if result(call(m.factory, mGetClassInfo, uintptr(i), uintptr(unsafe.Pointer(&ci)))) != resultOK {
				continue
			}
			c = Class{ID: ci.cid, Name: cstring(ci.name[:]), Category: cstring(ci.category[:])}
		}
		if c.Vendor == "" {
			c.Vendor = m.Vendor
		}
		m.Classes = append(m.Classes, c)
	}
	markARA(m.Classes)
	return m, nil
}

// markARA marks the audio classes of classes that an ARA factory of
// their name pairs with.
func markARA(classes []Class) {
	for _, f := range classes {
		if f.Category != araMainFactoryClass {
			continue
		}
		for i := range classes {
			if classes[i].Audio() && classes[i].Name == f.Name {
				classes[i].ARA = true
			}
		}
	}
}

// Effects returns the module's audio effects.
func (m *Module) Effects() []Class { return m.classes(Class.Effect) }

// Instruments returns the module's instruments.
func (m *Module) Instruments() []Class { return m.classes(Class.Instrument) }

// classes returns the module's classes that keep says to.
func (m *Module) classes(keep func(Class) bool) []Class {
	var out []Class
	for _, c := range m.Classes {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

// create makes an object of class id, as the interface iid.
func (m *Module) create(id, iid uid) uintptr {
	var obj uintptr
	if m.factory == 0 {
		// Closed.
		return 0
	}
	if result(call(m.factory, mCreateInstance, uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&iid)),
		uintptr(unsafe.Pointer(&obj)))) != resultOK {
		return 0
	}
	return obj
}

// Close lets go of the module's factory and unloads its library. The
// plugins made from it must be closed first. Closing it again does
// nothing.
func (m *Module) Close() error {
	var err error
	onUI(func() {
		if m.factory != 0 {
			release(m.factory)
			m.factory = 0
		}
		err = m.lib.close()
		m.lib = library{}
	})
	return err
}

// Find returns the class of the module with the ID id, written as
// [Class.IDString] writes it.
func (m *Module) Find(id string) (Class, bool) {
	for _, c := range m.Classes {
		if c.IDString() == id {
			return c, true
		}
	}
	return Class{}, false
}
