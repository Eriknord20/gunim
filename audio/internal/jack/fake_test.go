//go:build linux && !android && (amd64 || arm64)

package jack

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

// fakeServer stands in for libjack and its server. It runs the real
// process function, through purego, as the server's thread would: a
// port's handle is its buffer's address, and jackSame hands it back as
// jack_port_get_buffer would hand the buffer.
type fakeServer struct {
	mu           sync.Mutex
	rate, buffer int
	// physOut are the system's outputs (JACK's physical input ports),
	// physIn its inputs.
	physOut, physIn []string
	// lat is the latency range each port reports, by name.
	lat   map[string]latencyRange
	ports map[uintptr]*fakePort
	// links are the connections, source to destination.
	links      [][2]string
	cb         map[string]uintptr
	arg        uintptr
	active     bool
	opens      int
	closes     int
	refuse     bool
	since      uint32
	refuseLink map[string]bool
}

type fakePort struct {
	name string
	buf  []float32
	out  bool
}

func newFakeServer(rate, buffer int, physOut, physIn []string) *fakeServer {
	f := &fakeServer{rate: rate, buffer: buffer, physOut: physOut, physIn: physIn,
		lat: map[string]latencyRange{}, ports: map[uintptr]*fakePort{}, cb: map[string]uintptr{}}
	for _, n := range physOut {
		f.add(n, false)
	}
	for _, n := range physIn {
		f.add(n, true)
	}
	return f
}

// add makes a port, its buffer as large as any buffer.
func (f *fakeServer) add(name string, out bool) uintptr {
	p := &fakePort{name: name, buf: make([]float32, maxBuffer), out: out}
	h := uintptr(unsafe.Pointer(&p.buf[0]))
	f.ports[h] = p
	return h
}

func (f *fakeServer) byName(name string) (uintptr, *fakePort) {
	for h, p := range f.ports {
		if p.name == name {
			return h, p
		}
	}
	return 0, nil
}

// use puts f in place of libjack for the test.
func (f *fakeServer) use(t *testing.T) {
	t.Helper()
	set := func(name string) func(c, fn, arg uintptr) int32 {
		return func(_, fn, arg uintptr) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.cb[name], f.arg = fn, arg
			return 0
		}
	}
	a := &api{
		path: "fake",
		clientOpen: func(name string, _ uint32, _ *uint32) uintptr {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.refuse {
				return 0
			}
			f.opens++
			return 1
		},
		clientClose: func(uintptr) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.closes++
			f.active = false
			return 0
		},
		clientName: func(uintptr) string { return "app" },
		sampleRate: func(uintptr) uint32 { return uint32(f.rate) },
		bufferSize: func(uintptr) uint32 { return uint32(f.buffer) },
		portRegister: func(_ uintptr, name, _ string, flags, _ uint64) uintptr {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.add("app:"+name, flags&portIsOutput != 0)
		},
		portName: func(p uintptr) string {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.ports[p].name
		},
		ports: func(_ uintptr, flags uint64) []string {
			if flags&portIsInput != 0 {
				return f.physOut
			}
			return f.physIn
		},
		connections: func(p uintptr) []string {
			f.mu.Lock()
			defer f.mu.Unlock()
			name := f.ports[p].name
			var to []string
			for _, l := range f.links {
				if l[0] == name {
					to = append(to, l[1])
				}
				if l[1] == name {
					to = append(to, l[0])
				}
			}
			return to
		},
		portByName: func(_ uintptr, name string) uintptr {
			f.mu.Lock()
			defer f.mu.Unlock()
			h, _ := f.byName(name)
			return h
		},
		connect: func(_ uintptr, src, dst string) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.refuseLink[dst] || f.refuseLink[src] {
				return 1
			}
			f.links = append(f.links, [2]string{src, dst})
			return 0
		},
		latency: func(p uintptr, mode int32, r *latencyRange) {
			f.mu.Lock()
			defer f.mu.Unlock()
			name := f.ports[p].name
			if mode == captureLatency {
				name += "/capture"
			}
			*r = f.lat[name]
		},
		setProcess:    set("process"),
		setBufferSize: set("bufsize"),
		setRate:       set("rate"),
		setXrun:       set("xrun"),
		setGraph:      set("graph"),
		onShutdown:    func(c, fn, arg uintptr) { _ = set("shutdown")(c, fn, arg) },
		activate: func(uintptr) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.active = true
			return 0
		},
		deactivate: func(uintptr) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.active = false
			return 0
		},
		sinceCycle: func(uintptr) uint32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.since
		},
		getBuffer: procs.same,
	}
	libMu.Lock()
	testAPI = a
	libMu.Unlock()
	t.Cleanup(func() {
		libMu.Lock()
		testAPI = nil
		libMu.Unlock()
	})
}

// cycle runs one buffer of n frames, as the server does: it hands the
// system's inputs to the ports they connect to, runs the process
// function, and returns what each of the client's output ports played,
// in order.
func (f *fakeServer) cycle(t *testing.T, n int) [][]float32 {
	t.Helper()
	out, err := f.run(n)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// run is cycle, for any goroutine.
func (f *fakeServer) run(n int) ([][]float32, error) {
	f.mu.Lock()
	for _, p := range f.ports {
		if !p.out && strings.HasPrefix(p.name, "app:") {
			clear(p.buf)
		}
	}
	for _, l := range f.links {
		_, src := f.byName(l[0])
		_, dst := f.byName(l[1])
		if src.out && !dst.out {
			copy(dst.buf[:n], src.buf[:n])
		}
	}
	process, arg := f.cb["process"], f.arg
	active := f.active
	f.mu.Unlock()
	if !active || process == 0 {
		return nil, errors.New("the client is not active")
	}
	if r, _, _ := purego.SyscallN(process, uintptr(n), arg); r != 0 {
		return nil, fmt.Errorf("process returned %d", r)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]float32
	for k := 1; ; k++ {
		_, p := f.byName("app:out_" + strconv.Itoa(k))
		if p == nil {
			return out, nil
		}
		out = append(out, append([]float32(nil), p.buf[:n]...))
	}
}

// call runs the callback named with its arguments, as the server's
// threads do.
func (f *fakeServer) call(t *testing.T, name string, args ...uintptr) {
	t.Helper()
	f.mu.Lock()
	fn, arg := f.cb[name], f.arg
	f.mu.Unlock()
	if fn == 0 {
		t.Fatalf("no %s callback", name)
	}
	purego.SyscallN(fn, append(args, arg)...)
}
