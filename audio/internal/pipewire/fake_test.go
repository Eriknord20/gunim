//go:build linux && !android && (amd64 || arm64)

package pipewire

import (
	"runtime"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The C structures the process functions read, laid out as PipeWire
// lays them out.
type (
	spaChunk struct {
		offset, size  uint32
		stride, flags int32
	}
	spaData struct {
		typ, flags         uint32
		fd                 int64
		mapOffset, maxSize uint32
		data, chunk        uintptr
	}
	spaBuffer struct {
		nMetas, nDatas uint32
		metas, datas   uintptr
	}
	pwBuffer struct {
		buffer, userData      uintptr
		size, requested, time uint64
	}
)

// fakeBuffer is a pw_buffer and all it points to, in Go memory.
type fakeBuffer struct {
	pw      pwBuffer
	buf     spaBuffer
	data    spaData
	chunk   spaChunk
	samples []float32
}

// newFakeBuffer returns a buffer that holds frames frames, asking for
// requested. It is never inlined, so the buffer is on the heap, where
// it stays put: on a goroutine's stack it would move as the stack
// grows, and the addresses in it would point to where it was.
//
//go:noinline
func newFakeBuffer(frames, requested int) *fakeBuffer {
	b := &fakeBuffer{samples: make([]float32, frames*Channels+1)}
	b.data.maxSize = uint32(frames * Channels * 4)
	b.data.data = uintptr(unsafe.Pointer(&b.samples[0]))
	b.data.chunk = uintptr(unsafe.Pointer(&b.chunk))
	b.buf.nDatas = 1
	b.buf.datas = uintptr(unsafe.Pointer(&b.data))
	b.pw.buffer = uintptr(unsafe.Pointer(&b.buf))
	b.pw.requested = uint64(requested)
	return b
}

// fakePW is a stream of the fake server. Its handle is fs's address,
// which the fake functions in assembly read.
type fakePW struct {
	fs        fakeStream
	events    *[10]uintptr
	data      unsafe.Pointer
	props     string
	dir       uint32
	flags     uint32
	params    []byte
	state     State
	errText   []byte
	destroyed bool
}

// fakeServer stands in for libpipewire and its server. It runs the
// real process functions, through purego, as PipeWire's real-time
// thread would.
type fakeServer struct {
	mu      sync.Mutex
	streams []*fakePW
	props   []string
	// connectTo is the state a stream reaches as it connects, and
	// connectErr what it says where that is StateError.
	connectTo  State
	connectErr string
	loops      int
	stopped    int
}

func newFakeServer() *fakeServer { return &fakeServer{connectTo: StateStreaming} }

func (f *fakeServer) handle(s uintptr) *fakePW {
	for _, p := range f.streams {
		if uintptr(unsafe.Pointer(&p.fs)) == s {
			return p
		}
	}
	panic("no such stream")
}

// use puts f in place of libpipewire for the test.
func (f *fakeServer) use(t *testing.T) {
	t.Helper()
	a := &api{
		version: "1.0.5-fake",
		loopNew: func(string, uintptr) uintptr {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.loops++
			return 1
		},
		loopGetLoop: func(uintptr) uintptr { return 2 },
		loopStart:   func(uintptr) int32 { return 0 },
		loopStop: func(uintptr) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.stopped++
		},
		loopDestroy: func(uintptr) {},
		loopLock:    func(uintptr) {},
		loopUnlock:  func(uintptr) {},
		propsNewString: func(s string) uintptr {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.props = append(f.props, s)
			return uintptr(len(f.props))
		},
		streamNew: func(_ uintptr, _ string, props uintptr, events *[10]uintptr, data unsafe.Pointer) uintptr {
			f.mu.Lock()
			defer f.mu.Unlock()
			p := &fakePW{events: events, data: data, props: f.props[props-1]}
			f.streams = append(f.streams, p)
			return uintptr(unsafe.Pointer(&p.fs))
		},
		streamConnect: func(s uintptr, dir, _, flags uint32, params *unsafe.Pointer, n uint32) int32 {
			f.mu.Lock()
			p := f.handle(s)
			p.dir, p.flags = dir, flags
			if n == 1 {
				pod := *params
				size := *(*uint32)(pod) + 8
				p.params = append([]byte(nil), unsafe.Slice((*byte)(pod), size)...)
			}
			to, msg := f.connectTo, f.connectErr
			f.mu.Unlock()
			f.setState(p, to, msg)
			return 0
		},
		streamState: func(s uintptr, msg *unsafe.Pointer) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			p := f.handle(s)
			if len(p.errText) > 0 {
				*msg = unsafe.Pointer(&p.errText[0])
			}
			return int32(p.state)
		},
		streamDestroy: func(s uintptr) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.handle(s).destroyed = true
		},
		dequeue: procs.fakeDequeue,
		queue:   procs.fakeQueue,
		getTime: procs.fakeGetTime,
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

// setState moves p to state, and tells the client, as the server's
// loop thread does.
func (f *fakeServer) setState(p *fakePW, state State, msg string) {
	f.mu.Lock()
	old := p.state
	p.state = state
	p.errText = nil
	if msg != "" {
		p.errText = append([]byte(msg), 0)
	}
	fn, data := p.events[2], p.data
	f.mu.Unlock()
	purego.SyscallN(fn, uintptr(data), uintptr(int64(old)), uintptr(int64(state)), 0)
}

// stream returns the stream that plays, or records, where dir says.
func (f *fakeServer) stream(t *testing.T, dir uint32) *fakePW {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.streams {
		if p.dir == dir && !p.destroyed {
			return p
		}
	}
	t.Fatalf("no stream of direction %d", dir)
	return nil
}

// run runs p's process function with the buffers given, and the time
// report t, and returns how many buffers it queued.
func (f *fakeServer) run(p *fakePW, t pwTime, bufs ...*fakeBuffer) int {
	list := make([]uintptr, len(bufs)+1)
	for i, b := range bufs {
		list[i] = uintptr(unsafe.Pointer(&b.pw))
	}
	p.fs.next = uintptr(unsafe.Pointer(&list[0]))
	p.fs.time = t
	before := p.fs.queues
	purego.SyscallN(p.events[8], uintptr(p.data))
	runtime.KeepAlive(list)
	runtime.KeepAlive(bufs)
	return int(p.fs.queues - before)
}

// play runs a cycle of the output with a buffer of room frames that
// asks for requested, and returns what it played: the chunk's frames,
// interleaved.
func (f *fakeServer) play(t *testing.T, requested int, tm pwTime) []float32 {
	t.Helper()
	return f.playInto(t, newFakeBuffer(8192, requested), tm)
}

func (f *fakeServer) playInto(t *testing.T, b *fakeBuffer, tm pwTime) []float32 {
	t.Helper()
	p := f.stream(t, directionOutput)
	if q := f.run(p, tm, b); q != 1 {
		t.Fatalf("the output queued %d buffers, want 1", q)
	}
	if p.fs.queued != uintptr(unsafe.Pointer(&b.pw)) {
		t.Fatal("the output queued another buffer")
	}
	if b.chunk.offset != 0 || b.chunk.stride != 8 || b.chunk.size%8 != 0 {
		t.Fatalf("chunk %+v", b.chunk)
	}
	if b.pw.size != uint64(b.chunk.size/8) {
		t.Fatalf("pw_buffer size %d for a chunk of %d bytes", b.pw.size, b.chunk.size)
	}
	if b.samples[len(b.samples)-1] != 0 {
		t.Fatal("the output wrote past its buffer")
	}
	return b.samples[:b.chunk.size/4]
}

// record runs a cycle of the input that hands it the frames of each
// block, one buffer each, with the time report tm.
func (f *fakeServer) record(t *testing.T, tm pwTime, blocks ...[]float32) {
	t.Helper()
	p := f.stream(t, directionInput)
	bufs := make([]*fakeBuffer, 0, len(blocks))
	for _, b := range blocks {
		fb := newFakeBuffer(len(b)/Channels+16, 0)
		// An offset into the buffer, as a device may hand.
		fb.chunk.offset = 16 * Channels * 4
		copy(fb.samples[16*Channels:], b)
		fb.chunk.size = uint32(len(b) * 4)
		fb.chunk.stride = 8
		bufs = append(bufs, fb)
	}
	if q := f.run(p, tm, bufs...); q != len(blocks) {
		t.Fatalf("the input queued %d buffers back, want %d", q, len(blocks))
	}
}

// has reports whether the stream was made with the property prop.
func (p *fakePW) has(prop string) bool { return strings.Contains(p.props, prop) }
