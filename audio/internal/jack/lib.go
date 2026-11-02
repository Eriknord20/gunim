//go:build linux && !android && (amd64 || arm64)

package jack

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
	"unsafe"

	"github.com/ebitengine/purego"
)

// JACK's own numbers.
const (
	optionNoStartServer = 0x01

	portIsInput    = 0x1
	portIsOutput   = 0x2
	portIsPhysical = 0x4

	captureLatency  = 0
	playbackLatency = 1

	audioType = "32 bit float mono audio"
)

// latencyRange is a jack_latency_range_t.
type latencyRange struct{ min, max uint32 }

// api is what the client asks of libjack, as Go functions: the
// library's, or a test's stand-in. A client and a port are their
// handles. getBuffer is jack_port_get_buffer's address, which the
// process function calls.
type api struct {
	path          string
	clientOpen    func(name string, options uint32, status *uint32) uintptr
	clientClose   func(c uintptr) int32
	clientName    func(c uintptr) string
	sampleRate    func(c uintptr) uint32
	bufferSize    func(c uintptr) uint32
	portRegister  func(c uintptr, name, typ string, flags, size uint64) uintptr
	portName      func(p uintptr) string
	ports         func(c uintptr, flags uint64) []string
	connections   func(p uintptr) []string
	portByName    func(c uintptr, name string) uintptr
	connect       func(c uintptr, src, dst string) int32
	latency       func(p uintptr, mode int32, r *latencyRange)
	setProcess    func(c, fn, arg uintptr) int32
	setBufferSize func(c, fn, arg uintptr) int32
	setRate       func(c, fn, arg uintptr) int32
	setXrun       func(c, fn, arg uintptr) int32
	setGraph      func(c, fn, arg uintptr) int32
	onShutdown    func(c, fn, arg uintptr)
	activate      func(c uintptr) int32
	deactivate    func(c uintptr) int32
	sinceCycle    func(c uintptr) uint32
	getBuffer     uintptr
}

// libraries returns the libjacks to try, in order: the one the system
// finds by name, as jackd's or as pw-jack's LD_LIBRARY_PATH sets it,
// then PipeWire's own, where it is installed beside PipeWire but not in
// the system's path, as Debian and Ubuntu install it.
func libraries() []string {
	libs := []string{"libjack.so.0"}
	for _, pattern := range []string{
		"/usr/lib/*-linux-gnu*/pipewire-0.3/jack/libjack.so.0",
		"/usr/lib64/pipewire-0.3/jack/libjack.so.0",
		"/usr/lib/pipewire-0.3/jack/libjack.so.0",
	} {
		found, _ := filepath.Glob(pattern)
		libs = append(libs, found...)
	}
	return libs
}

var (
	libMu sync.Mutex
	// libs are the libraries loaded, by path, nil where one failed to
	// load; chosen is the one a server last answered through.
	libs   = map[string]*api{}
	chosen *api
)

// loadLib loads the library at path and reads its functions, once.
func loadLib(path string) (*api, error) {
	if a, ok := libs[path]; ok {
		if a == nil {
			return nil, fmt.Errorf("jack: %s failed to load", path)
		}
		return a, nil
	}
	libs[path] = nil
	h, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("jack: loading %s: %w", path, err)
	}
	a, err := readLib(h, path)
	if err != nil {
		return nil, err
	}
	libs[path] = a
	return a, nil
}

// readLib reads the functions of the library h, and silences its
// messages.
func readLib(h uintptr, path string) (a *api, err error) {
	defer func() {
		// RegisterLibFunc panics on a symbol the library lacks.
		if r := recover(); r != nil {
			a, err = nil, fmt.Errorf("jack: %s: %v", path, r)
		}
	}()
	a = &api{path: path}
	purego.RegisterLibFunc(&a.clientOpen, h, "jack_client_open")
	purego.RegisterLibFunc(&a.clientClose, h, "jack_client_close")
	purego.RegisterLibFunc(&a.clientName, h, "jack_get_client_name")
	purego.RegisterLibFunc(&a.sampleRate, h, "jack_get_sample_rate")
	purego.RegisterLibFunc(&a.bufferSize, h, "jack_get_buffer_size")
	purego.RegisterLibFunc(&a.portRegister, h, "jack_port_register")
	purego.RegisterLibFunc(&a.portName, h, "jack_port_name")
	purego.RegisterLibFunc(&a.portByName, h, "jack_port_by_name")
	purego.RegisterLibFunc(&a.connect, h, "jack_connect")
	purego.RegisterLibFunc(&a.latency, h, "jack_port_get_latency_range")
	purego.RegisterLibFunc(&a.setProcess, h, "jack_set_process_callback")
	purego.RegisterLibFunc(&a.setBufferSize, h, "jack_set_buffer_size_callback")
	purego.RegisterLibFunc(&a.setRate, h, "jack_set_sample_rate_callback")
	purego.RegisterLibFunc(&a.setXrun, h, "jack_set_xrun_callback")
	purego.RegisterLibFunc(&a.setGraph, h, "jack_set_graph_order_callback")
	purego.RegisterLibFunc(&a.onShutdown, h, "jack_on_shutdown")
	purego.RegisterLibFunc(&a.activate, h, "jack_activate")
	purego.RegisterLibFunc(&a.deactivate, h, "jack_deactivate")
	purego.RegisterLibFunc(&a.sinceCycle, h, "jack_frames_since_cycle_start")
	var getPorts func(c uintptr, name, typ string, flags uint64) unsafe.Pointer
	var getConnections func(p uintptr) unsafe.Pointer
	var free func(p unsafe.Pointer)
	var setError, setInfo func(fn uintptr)
	purego.RegisterLibFunc(&getPorts, h, "jack_get_ports")
	purego.RegisterLibFunc(&getConnections, h, "jack_port_get_connections")
	purego.RegisterLibFunc(&free, h, "jack_free")
	purego.RegisterLibFunc(&setError, h, "jack_set_error_function")
	purego.RegisterLibFunc(&setInfo, h, "jack_set_info_function")
	a.ports = func(c uintptr, flags uint64) []string {
		return takeNames(getPorts(c, "", audioType, flags), free)
	}
	a.connections = func(p uintptr) []string { return takeNames(getConnections(p), free) }
	a.getBuffer, err = purego.Dlsym(h, "jack_port_get_buffer")
	if err != nil {
		return nil, fmt.Errorf("jack: %s: %w", path, err)
	}
	setError(procs.silent)
	setInfo(procs.silent)
	return a, nil
}

// takeNames reads a list of names libjack made, ended by a null, and
// frees it.
func takeNames(list unsafe.Pointer, free func(unsafe.Pointer)) []string {
	if list == nil {
		return nil
	}
	defer free(list)
	var names []string
	for p := (*unsafe.Pointer)(list); *p != nil; p = (*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(p), unsafe.Sizeof(p))) {
		names = append(names, cString(*p))
	}
	return names
}

// cString copies the C string at p.
func cString(p unsafe.Pointer) string {
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}

// errNoServer is why no server answered.
var errNoServer = errors.New("jack: no JACK server is running")

// connect opens a client of the server, through the library a server
// last answered through, else the first that a server answers through.
// It returns the client and the library's functions.
func connect(name string) (*api, uintptr, error) {
	libMu.Lock()
	defer libMu.Unlock()
	if testAPI != nil {
		var status uint32
		if c := testAPI.clientOpen(name, optionNoStartServer, &status); c != 0 {
			return testAPI, c, nil
		}
		return nil, 0, errNoServer
	}
	order := libraries()
	if chosen != nil {
		order = append([]string{chosen.path}, order...)
	}
	var errs []error
	for _, path := range order {
		a, err := loadLib(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		var status uint32
		if c := a.clientOpen(name, optionNoStartServer, &status); c != 0 {
			chosen = a
			return a, c, nil
		}
	}
	if len(errs) == len(order) {
		return nil, 0, fmt.Errorf("jack: libjack is missing: %w", errors.Join(errs...))
	}
	return nil, 0, errNoServer
}

// testAPI, where set, stands in for libjack.
var testAPI *api

// nameSize is the longest name a client may have, in bytes, as JACK
// and PipeWire's libjack both allow.
const nameSize = 63

// clientName returns a name for the server from name, or the program's
// name: as long as the server allows, and with no colon, which ends a
// client's name in its ports' names.
func clientName(name string) string {
	if name == "" {
		name = programName()
	}
	name = strings.ReplaceAll(name, ":", "-")
	for len(name) > nameSize {
		_, n := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-n]
	}
	return name
}

// programName returns the program's own name.
func programName() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Base(exe)
	}
	return "gunim"
}
