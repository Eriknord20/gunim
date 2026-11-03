//go:build linux && !android && (amd64 || arm64)

package pipewire

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// PipeWire's own numbers.
const (
	directionInput  = 0 // PW_DIRECTION_INPUT: a stream that records
	directionOutput = 1 // PW_DIRECTION_OUTPUT: a stream that plays
	anyID           = 0xffffffff

	flagAutoconnect = 1 << 0
	flagMapBuffers  = 1 << 2
	flagRTProcess   = 1 << 4
)

// minVersion is the oldest PipeWire the client plays through: 0.3.50
// added pw_stream_get_time_n, and 0.3.49 pw_buffer's requested.
var minVersion = [3]int{0, 3, 50}

// libName is the library's name, as every distribution installs it.
const libName = "libpipewire-0.3.so.0"

// api is what the client asks of libpipewire, as Go functions: the
// library's, or a test's stand-in. A loop, a stream and properties are
// their handles. dequeue, queue and getTime are the addresses of
// pw_stream_dequeue_buffer, pw_stream_queue_buffer and
// pw_stream_get_time_n, which the process functions call.
type api struct {
	version        string
	loopNew        func(name string, props uintptr) uintptr
	loopGetLoop    func(l uintptr) uintptr
	loopStart      func(l uintptr) int32
	loopStop       func(l uintptr)
	loopDestroy    func(l uintptr)
	loopLock       func(l uintptr)
	loopUnlock     func(l uintptr)
	propsNewString func(s string) uintptr
	streamNew      func(loop uintptr, name string, props uintptr, events *[10]uintptr, data unsafe.Pointer) uintptr
	streamConnect  func(s uintptr, direction, target, flags uint32, params *unsafe.Pointer, n uint32) int32
	streamState    func(s uintptr, err *unsafe.Pointer) int32
	streamDestroy  func(s uintptr)
	dequeue        uintptr
	queue          uintptr
	getTime        uintptr
}

var (
	libMu sync.Mutex
	// lib is the library, once loaded; libErr why it failed to.
	lib    *api
	libErr error
)

// testAPI, where set, stands in for libpipewire.
var testAPI *api

// load loads the library and starts it, once.
func load() (*api, error) {
	libMu.Lock()
	defer libMu.Unlock()
	if testAPI != nil {
		return testAPI, nil
	}
	if lib == nil && libErr == nil {
		lib, libErr = loadLib()
	}
	return lib, libErr
}

func loadLib() (a *api, err error) {
	h, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("pipewire: loading %s: %w", libName, err)
	}
	defer func() {
		// RegisterLibFunc panics on a symbol the library lacks.
		if r := recover(); r != nil {
			a, err = nil, fmt.Errorf("pipewire: %s: %v", libName, r)
		}
	}()
	var version func() string
	purego.RegisterLibFunc(&version, h, "pw_get_library_version")
	a = &api{version: version()}
	if !atLeast(a.version, minVersion) {
		return nil, fmt.Errorf("pipewire: libpipewire is %s; gunim needs %d.%d.%d or newer",
			a.version, minVersion[0], minVersion[1], minVersion[2])
	}
	var initLib func(argc, argv uintptr)
	purego.RegisterLibFunc(&initLib, h, "pw_init")
	purego.RegisterLibFunc(&a.loopNew, h, "pw_thread_loop_new")
	purego.RegisterLibFunc(&a.loopGetLoop, h, "pw_thread_loop_get_loop")
	purego.RegisterLibFunc(&a.loopStart, h, "pw_thread_loop_start")
	purego.RegisterLibFunc(&a.loopStop, h, "pw_thread_loop_stop")
	purego.RegisterLibFunc(&a.loopDestroy, h, "pw_thread_loop_destroy")
	purego.RegisterLibFunc(&a.loopLock, h, "pw_thread_loop_lock")
	purego.RegisterLibFunc(&a.loopUnlock, h, "pw_thread_loop_unlock")
	purego.RegisterLibFunc(&a.propsNewString, h, "pw_properties_new_string")
	purego.RegisterLibFunc(&a.streamNew, h, "pw_stream_new_simple")
	purego.RegisterLibFunc(&a.streamConnect, h, "pw_stream_connect")
	purego.RegisterLibFunc(&a.streamState, h, "pw_stream_get_state")
	purego.RegisterLibFunc(&a.streamDestroy, h, "pw_stream_destroy")
	for _, f := range []struct {
		name string
		addr *uintptr
	}{
		{"pw_stream_dequeue_buffer", &a.dequeue},
		{"pw_stream_queue_buffer", &a.queue},
		{"pw_stream_get_time_n", &a.getTime},
	} {
		if *f.addr, err = purego.Dlsym(h, f.name); err != nil {
			return nil, fmt.Errorf("pipewire: %s: %w", libName, err)
		}
	}
	initLib(0, 0)
	return a, nil
}

// atLeast reports whether version, as "1.0.5", is least or newer.
func atLeast(version string, least [3]int) bool {
	parts := strings.SplitN(version, ".", 3)
	for i := range 3 {
		n := 0
		if i < len(parts) {
			digits := strings.TrimRightFunc(parts[i], func(r rune) bool { return r < '0' || r > '9' })
			n, _ = strconv.Atoi(digits)
		}
		if n != least[i] {
			return n > least[i]
		}
	}
	return true
}

// errNoServer is why the library loaded but no server answers.
var errNoServer = errors.New("pipewire: no PipeWire server is running")

// socketPath returns where the server listens, as libpipewire finds it:
// PIPEWIRE_REMOTE in PIPEWIRE_RUNTIME_DIR, else XDG_RUNTIME_DIR, else
// USERPROFILE; "pipewire-0" where PIPEWIRE_REMOTE is unset.
func socketPath() string {
	name := os.Getenv("PIPEWIRE_REMOTE")
	if name == "" {
		name = "pipewire-0"
	}
	if filepath.IsAbs(name) {
		return name
	}
	for _, env := range []string{"PIPEWIRE_RUNTIME_DIR", "XDG_RUNTIME_DIR", "USERPROFILE"} {
		if dir := os.Getenv(env); dir != "" {
			return filepath.Join(dir, name)
		}
	}
	return ""
}

// serverListens reports whether the server's socket is there.
func serverListens() bool {
	p := socketPath()
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && fi.Mode()&os.ModeSocket != 0
}

// cString copies the C string at p.
func cString(p unsafe.Pointer) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}

// programName returns the program's own name.
func programName() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Base(exe)
	}
	return "gunim"
}

// quote returns s as a quoted string, as pw_properties_new_string
// reads a value: within double quotes, a backslash before each quote
// and backslash, and no control characters.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range strings.ToValidUTF8(s, "?") {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < ' ' || r == 0x7f:
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
