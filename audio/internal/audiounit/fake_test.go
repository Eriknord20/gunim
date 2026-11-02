//go:build ((darwin && !ios) || (linux && !android)) && (amd64 || arm64)

package audiounit

import (
	"encoding/binary"
	"errors"
	"math"
	"sync"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

// fakeCA stands in for Core Audio: its devices are properties in a map,
// and its units hold the callbacks the client sets. A test runs the real
// callbacks, through purego, as Core Audio's threads would. An input
// unit's handle is the address of its testSource, and auTestRender
// stands in for AudioUnitRender: it renders the source's samples.
type fakeCA struct {
	mu    sync.Mutex
	props map[propKey][]byte
	names map[propKey]string
	units map[uintptr]*fakeUnit
	// made lists the units in the order made.
	made []*fakeUnit
	// listeners counts the listeners added, by property.
	listeners map[propKey]int
	clock     int64
	denied    bool
	// refuse makes setting a unit's property fail, by its number.
	refuse map[uint32]bool
	// unlistenFails makes removing a listener from an object fail, by
	// the object, with the status given.
	unlistenFails map[uint32]int32
	// masked says the calls run inside masked now; unmasked counts the
	// units made, readied or started outside it.
	masked   bool
	unmasked int
}

type propKey struct{ obj, selector, scope, element uint32 }

type fakeUnit struct {
	src                      testSource
	props                    map[[3]uint32][]byte
	ready, running, disposed bool
	starts, stops            int
}

func newFakeCA() *fakeCA {
	return &fakeCA{props: map[propKey][]byte{}, names: map[propKey]string{}, units: map[uintptr]*fakeUnit{},
		listeners: map[propKey]int{}, refuse: map[uint32]bool{}, unlistenFails: map[uint32]int32{}}
}

// A fakeDevice says what a device of the fake's is.
type fakeDevice struct {
	id                     uint32
	name                   string
	rate                   float64
	outs, ins              int
	buffer, lo, hi         uint32
	lat, safety, streamLat uint32
	inLat, inStreamLat     uint32
}

func (f *fakeCA) put(obj, selector, scope, element uint32, v any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putLocked(obj, selector, scope, element, v)
}

func (f *fakeCA) putLocked(obj, selector, scope, element uint32, v any) {
	var b []byte
	switch v := v.(type) {
	case uint32:
		b = binary.LittleEndian.AppendUint32(nil, v)
	case float64:
		b = binary.LittleEndian.AppendUint64(nil, math.Float64bits(v))
	case []byte:
		b = v
	default:
		panic("fakeCA: a property of an unknown type")
	}
	f.props[propKey{obj, selector, scope, element}] = b
}

// bufferList returns the bytes of an AudioBufferList of one buffer of
// chans channels, as a stream configuration holds it, or of none.
func bufferList(chans int) []byte {
	if chans == 0 {
		return binary.LittleEndian.AppendUint32(make([]byte, 0, 8), 0)
	}
	b := make([]byte, bufferListHeader+16)
	binary.LittleEndian.PutUint32(b, 1)
	binary.LittleEndian.PutUint32(b[bufferListHeader:], uint32(chans))
	return b
}

// add adds a device; its streams are objects id*10+1 and id*10+2.
func (f *fakeCA) add(d fakeDevice) {
	if d.lo == 0 {
		d.lo, d.hi = 15, 4096
	}
	f.put(d.id, propNominalSampleRate, scopeGlobal, 0, d.rate)
	f.put(d.id, propDeviceIsAlive, scopeGlobal, 0, uint32(1))
	f.put(d.id, propStreamConfiguration, scopeOutput, 0, bufferList(d.outs))
	f.put(d.id, propStreamConfiguration, scopeInput, 0, bufferList(d.ins))
	r := make([]byte, 16)
	binary.LittleEndian.PutUint64(r, math.Float64bits(float64(d.lo)))
	binary.LittleEndian.PutUint64(r[8:], math.Float64bits(float64(d.hi)))
	f.put(d.id, propBufferFrameRange, scopeGlobal, 0, r)
	f.put(d.id, propBufferFrameSize, scopeGlobal, 0, d.buffer)
	f.put(d.id, propLatency, scopeOutput, 0, d.lat)
	f.put(d.id, propSafetyOffset, scopeOutput, 0, d.safety)
	f.put(d.id, propLatency, scopeInput, 0, d.inLat)
	f.put(d.id, propStreams, scopeOutput, 0, binary.LittleEndian.AppendUint32(nil, d.id*10+1))
	f.put(d.id, propStreams, scopeInput, 0, binary.LittleEndian.AppendUint32(nil, d.id*10+2))
	f.put(d.id*10+1, propLatency, scopeGlobal, 0, d.streamLat)
	f.put(d.id*10+2, propLatency, scopeGlobal, 0, d.inStreamLat)
	f.mu.Lock()
	f.names[propKey{d.id, propName, scopeGlobal, 0}] = d.name
	f.mu.Unlock()
}

// defaults makes out and in the default devices.
func (f *fakeCA) defaults(out, in uint32) {
	f.put(systemObject, propDefaultOutputDevice, scopeGlobal, 0, out)
	f.put(systemObject, propDefaultInputDevice, scopeGlobal, 0, in)
}

// use puts f in place of Core Audio for the test.
func (f *fakeCA) use(t *testing.T) {
	t.Helper()
	a := &api{
		get: func(obj uint32, addr address, size *uint32, out unsafe.Pointer) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			b, ok := f.props[propKey{obj, addr.selector, addr.scope, addr.element}]
			if !ok {
				return -1
			}
			n := min(int(*size), len(b))
			copy(unsafe.Slice((*byte)(out), n), b)
			*size = uint32(n)
			return noErr
		},
		size: func(obj uint32, addr address, size *uint32) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			b, ok := f.props[propKey{obj, addr.selector, addr.scope, addr.element}]
			if !ok {
				return -1
			}
			*size = uint32(len(b))
			return noErr
		},
		set: func(obj uint32, addr address, size uint32, in unsafe.Pointer) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.putLocked(obj, addr.selector, addr.scope, addr.element, append([]byte(nil), unsafe.Slice((*byte)(in), size)...))
			return noErr
		},
		str: func(obj uint32, addr address) (string, bool) {
			f.mu.Lock()
			defer f.mu.Unlock()
			s, ok := f.names[propKey{obj, addr.selector, addr.scope, addr.element}]
			return s, ok
		},
		listen: func(obj uint32, addr address, proc, client uintptr) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			if proc != procs.changed || client != uintptr(unsafe.Pointer(&changes)) {
				return -1
			}
			f.listeners[propKey{obj, addr.selector, addr.scope, addr.element}]++
			return noErr
		},
		unlisten: func(obj uint32, addr address, proc, client uintptr) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			k := propKey{obj, addr.selector, addr.scope, addr.element}
			if st := f.unlistenFails[obj]; st != noErr {
				return st
			}
			if f.listeners[k] == 0 {
				return -1
			}
			f.listeners[k]--
			return noErr
		},
		newUnit: func() (uintptr, int32) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if !f.masked {
				f.unmasked++
			}
			u := &fakeUnit{props: map[[3]uint32][]byte{}}
			h := uintptr(unsafe.Pointer(&u.src))
			f.units[h] = u
			f.made = append(f.made, u)
			return h, noErr
		},
		setUnit: func(h uintptr, prop, scope, elem uint32, in unsafe.Pointer, size uint32) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.refuse[prop] {
				return -50
			}
			f.units[h].props[[3]uint32{prop, scope, elem}] = append([]byte(nil), unsafe.Slice((*byte)(in), size)...)
			return noErr
		},
		getUnit: func(uintptr, uint32, uint32, uint32, unsafe.Pointer, *uint32) int32 { return -1 },
		initialize: func(h uintptr) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			if !f.masked {
				f.unmasked++
			}
			f.units[h].ready = true
			return noErr
		},
		start: func(h uintptr) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			u := f.units[h]
			u.running, u.starts = true, u.starts+1
			if !f.masked {
				f.unmasked++
			}
			return noErr
		},
		stop: func(h uintptr) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			u := f.units[h]
			u.running, u.stops = false, u.stops+1
			return noErr
		},
		uninitialize: func(h uintptr) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.units[h].ready = false
			return noErr
		},
		dispose: func(h uintptr) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.units[h].disposed = true
			return noErr
		},
		render: procs.testRender,
		numer:  1,
		denom:  1,
		now: func() int64 {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.clock
		},
		masked: func(fn func()) {
			f.mu.Lock()
			f.masked = true
			f.mu.Unlock()
			defer func() {
				f.mu.Lock()
				f.masked = false
				f.mu.Unlock()
			}()
			fn()
		},
		mayRecord: func() error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.denied {
				return ErrDenied
			}
			return nil
		},
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

// setClock sets the host time, in nanoseconds.
func (f *fakeCA) setClock(ns int64) {
	f.mu.Lock()
	f.clock = ns
	f.mu.Unlock()
}

// callback returns the callback set on u as prop, at scope and elem.
func (f *fakeCA) callback(u *fakeUnit, prop, scope, elem uint32) (callbackStruct, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := u.props[[3]uint32{prop, scope, elem}]
	if !ok {
		return callbackStruct{}, errors.New("no callback set")
	}
	if !u.running {
		return callbackStruct{}, errors.New("the unit is not running")
	}
	return *(*callbackStruct)(unsafe.Pointer(&b[0])), nil
}

// timestamp returns an AudioTimeStamp at host time host, valid where
// host is not 0.
func timestamp(host uint64) *[8]uint64 {
	var ts [8]uint64
	if host != 0 {
		ts[1], ts[7] = host, 2
	}
	return &ts
}

// render runs the output unit's render callback for n frames, into
// bufs buffers of n frames each, at host time host, as Core Audio's
// thread does, and returns what each buffer holds.
func (f *fakeCA) render(t *testing.T, n, bufs int, host uint64) [][]float32 {
	t.Helper()
	return f.renderSized(t, n, bufs, n, host)
}

// renderSized is render into buffers of room frames each.
func (f *fakeCA) renderSized(t *testing.T, n, bufs, room int, host uint64) [][]float32 {
	t.Helper()
	cb, err := f.callback(f.made[0], unitRenderCallback, unitInput, busOut)
	if err != nil {
		t.Fatal(err)
	}
	out := make([][]float32, bufs)
	list := make([]uint64, 1+2*bufs)
	list[0] = uint64(bufs)
	for k := range out {
		out[k] = make([]float32, room)
		for i := range out[k] {
			out[k][i] = 99 // what a buffer held before
		}
		list[1+2*k] = 1 | uint64(4*room)<<32
		list[2+2*k] = uint64(uintptr(unsafe.Pointer(&out[k][0])))
	}
	var flags uint32
	ts := timestamp(host)
	r, _, _ := purego.SyscallN(cb.proc, cb.refCon, uintptr(unsafe.Pointer(&flags)), uintptr(unsafe.Pointer(ts)),
		busOut, uintptr(n), uintptr(unsafe.Pointer(&list[0])))
	if int32(r) != noErr {
		t.Fatalf("render returned %d", int32(r))
	}
	return out
}

// record has the input unit hand on in, n frames of chans planar
// channels, at host time host, as Core Audio's thread does. The unit
// renders status: 0, or a failure.
func (f *fakeCA) record(t *testing.T, in [][]float32, n int, host uint64, status int32) {
	t.Helper()
	u := f.made[len(f.made)-1]
	cb, err := f.callback(u, unitInputCallback, unitGlobal, busOut)
	if err != nil {
		t.Fatal(err)
	}
	u.src.chans, u.src.status = uint64(len(in)), uint64(uint32(status))
	for k := range in {
		u.src.src[k] = uintptr(unsafe.Pointer(&in[k][0]))
	}
	var flags uint32
	ts := timestamp(host)
	r, _, _ := purego.SyscallN(cb.proc, cb.refCon, uintptr(unsafe.Pointer(&flags)), uintptr(unsafe.Pointer(ts)),
		busIn, uintptr(n), 0)
	if int32(r) != noErr {
		t.Fatalf("the input callback returned %d", int32(r))
	}
}

// tell has Core Audio's notification thread call the listener, as it
// does for a change of a property listened to.
func (f *fakeCA) tell(obj, selector uint32) {
	addr := address{selector, scopeGlobal, 0}
	purego.SyscallN(procs.changed, uintptr(obj), 1, uintptr(unsafe.Pointer(&addr)), uintptr(unsafe.Pointer(&changes)))
}
