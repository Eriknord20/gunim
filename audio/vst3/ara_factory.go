package vst3

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

// ErrNoARA is the error of a class that has no ARA factory.
var ErrNoARA = errors.New("vst3: the plugin has no ARA")

// ContentType is a kind of content an ARA plugin and its host share.
type ContentType int32

// The kinds of content.
const (
	ContentNotes       ContentType = 10
	ContentTempo       ContentType = 20
	ContentBars        ContentType = 21
	ContentTuning      ContentType = 31
	ContentKeys        ContentType = 42
	ContentSheetChords ContentType = 45
)

// Transforms are the ways a playback region may play its modification
// other than as it is: stretched in time, and with fades the plugin
// makes from the content.
type Transforms int32

// The transforms.
const (
	Timestretch        Transforms = 1 << 0
	TimestretchByTempo Transforms = 1 << 1
	FadeAtTail         Transforms = 1 << 2
	FadeAtHead         Transforms = 1 << 3
)

// ARAFactory is a plugin's ARA factory: what the plugin does with ARA,
// and the documents it makes.
type ARAFactory struct {
	// Class is the plugin's audio class, of the factory's name, whose
	// instances bind to its documents.
	Class Class
	// Name, Vendor, URL and Version are the plugin's, as the factory
	// gives them.
	Name, Vendor, URL, Version string
	// ID names the factory, as "com.celemony.ara.melodyne".
	ID string
	// ArchiveID names the form of the archives it writes, and
	// Compatible the forms of others it reads too.
	ArchiveID  string
	Compatible []string
	// Analyzes are the kinds of content it finds in audio.
	Analyzes []ContentType
	// Transforms are the transforms its regions play.
	Transforms Transforms
	// Generation is the API generation in use: 4 for ARA 2.0, 6 for 2.3.
	Generation int

	ptr    uintptr
	module *Module
	// docs counts the documents open: ARA is initialized while any is.
	docs int
	// build makes an instance of a factory of no module's, as the fake's.
	build func(p *Plugin) error
}

// The factories initialized, by their address: ARA initializes a
// factory once, for the program.
var (
	araMu        sync.Mutex
	araFactories = map[uintptr]*ARAFactory{}
	// araAssert is the address ARA's assert function is read from: none.
	araAssert = new(uintptr)
)

// ARAFactory returns the ARA factory of class c, the module's class of
// category "ARA Main Factory Class" named as c, speaking ARA 2.0 or
// later, the latest both sides know. ARA is initialized while a
// document of the factory is open.
func (m *Module) ARAFactory(c Class) (*ARAFactory, error) {
	var f *ARAFactory
	var err error
	onUI(func() {
		for _, k := range m.Classes {
			if k.Category != araMainFactoryClass || k.Name != c.Name {
				continue
			}
			obj := m.create(k.ID, iidARAMainFactory)
			if obj == 0 {
				continue
			}
			p := call(obj, mARAGetFactory)
			// The factory lives as long as the module; the object that
			// gave it may go.
			f, err = araFactoryAt(p, c, m)
			release(obj)
			return
		}
		err = fmt.Errorf("%w: %s", ErrNoARA, c.Name)
	})
	return f, err
}

// The fields of an ARAFactory.
var araFactoryFields = []int{fPtr, fI32, fI32, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr,
	fPtr, fI32, fI32}

const (
	afLowest = iota + 1
	afHighest
	afID
	afInit
	afUninit
	afName
	afVendor
	afURL
	afVersion
	afCreate
	afArchiveID
	afCompatibleN
	afCompatible
	afAnalyzesN
	afAnalyzes
	afTransforms
	afFileChunks
)

// araFactoryAt reads the factory at p, for class c of m, once for the
// program. It runs on the plugins' thread.
func araFactoryAt(p uintptr, c Class, m *Module) (*ARAFactory, error) {
	if p == 0 {
		return nil, fmt.Errorf("%w: %s gave no factory", ErrNoARA, c.Name)
	}
	if ptrSize != 8 {
		return nil, fmt.Errorf("%w: %s: ARA runs on 64-bit platforms", ErrNoARA, c.Name)
	}
	araMu.Lock()
	defer araMu.Unlock()
	if f := araFactories[p]; f != nil {
		return f, nil
	}
	r := readAt(p, araFactoryFields...)
	if !r.has(afTransforms) {
		return nil, fmt.Errorf("%w: %s: its factory is too old", ErrNoARA, c.Name)
	}
	lo, hi := int(r.i32(afLowest)), int(r.i32(afHighest))
	gen := min(hi, araGeneration2_3Final)
	if gen < araGeneration2Final || gen < lo {
		return nil, fmt.Errorf("%w: %s speaks ARA generations %d to %d, of the 4 to 6 the host speaks", ErrNoARA, c.Name, lo, hi)
	}
	f := &ARAFactory{Class: c, Name: r.str(afName), Vendor: r.str(afVendor), URL: r.str(afURL), Version: r.str(afVersion),
		ID: r.str(afID), ArchiveID: r.str(afArchiveID), Compatible: r.strings(afCompatibleN, afCompatible),
		Transforms: Transforms(r.i32(afTransforms)), Generation: gen, ptr: p, module: m}
	for k := range r.count(afAnalyzesN) {
		f.Analyzes = append(f.Analyzes, ContentType(*ptr[int32](r.ptr(afAnalyzes) + uintptr(4*k))))
	}
	araFactories[p] = f
	return f, nil
}

// open initializes the factory as its first document opens. It runs on
// the plugins' thread.
func (f *ARAFactory) open() {
	araMu.Lock()
	defer araMu.Unlock()
	f.docs++
	if f.docs > 1 {
		return
	}
	cfg := newStruct(fPtr, fI32, fPtr)
	cfg.put(1, uint64(f.Generation))
	cfg.putPtr(2, uintptr(unsafe.Pointer(araAssert)), araAssert)
	*araAssert = araAssertFn()
	cfn(readAt(f.ptr, araFactoryFields...).ptr(afInit), cfg.addr())
	runtime.KeepAlive(cfg)
}

// close lets the factory go as its last document closes, as ARA asks
// before a program ends. It runs on the plugins' thread.
func (f *ARAFactory) close() {
	araMu.Lock()
	defer araMu.Unlock()
	f.docs--
	if f.docs == 0 {
		cfn(readAt(f.ptr, araFactoryFields...).ptr(afUninit))
	}
}

// Reads reports whether the factory reads archives of the form id: its
// own, or one it is compatible with.
func (f *ARAFactory) Reads(id string) bool {
	if id == f.ArchiveID {
		return true
	}
	for _, c := range f.Compatible {
		if c == id {
			return true
		}
	}
	return false
}
