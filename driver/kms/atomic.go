//go:build linux && (arm64 || amd64)

package kms

import (
	"runtime"
	"unsafe"
)

// An atomicer takes atomic commits: a card, or a test's stand-in.
type atomicer interface {
	atomic(req *modeAtomic) error
}

// atomicReq builds one atomic commit: properties to set on objects,
// grouped by object as the kernel wants them. It keeps its arrays from one
// commit to the next, so a frame's commit allocates nothing.
type atomicReq struct {
	objs   []uint32
	counts []uint32
	props  []uint32
	values []uint64
	pin    runtime.Pinner
	req    modeAtomic
}

// reset empties the request for a new commit.
func (a *atomicReq) reset() {
	a.objs, a.counts = a.objs[:0], a.counts[:0]
	a.props, a.values = a.props[:0], a.values[:0]
}

// add sets the property prop of obj to v. Properties of one object go
// together: the kernel takes each object once, with its properties after
// each other.
func (a *atomicReq) add(obj, prop uint32, v uint64) {
	if n := len(a.objs); n == 0 || a.objs[n-1] != obj {
		a.objs = append(a.objs, obj)
		a.counts = append(a.counts, 0)
	}
	a.counts[len(a.counts)-1]++
	a.props = append(a.props, prop)
	a.values = append(a.values, v)
}

// commit sends the request to d with flags, and userData for the flip
// event it asks for.
func (a *atomicReq) commit(d atomicer, flags uint32, userData uint64) error {
	if len(a.objs) == 0 {
		return nil
	}
	a.pin.Pin(&a.objs[0])
	a.pin.Pin(&a.counts[0])
	a.pin.Pin(&a.props[0])
	a.pin.Pin(&a.values[0])
	a.req = modeAtomic{
		flags:      flags,
		countObjs:  uint32(len(a.objs)),
		objs:       uint64(uintptr(unsafe.Pointer(&a.objs[0]))),
		countProps: uint64(uintptr(unsafe.Pointer(&a.counts[0]))),
		props:      uint64(uintptr(unsafe.Pointer(&a.props[0]))),
		values:     uint64(uintptr(unsafe.Pointer(&a.values[0]))),
		userData:   userData,
	}
	err := d.atomic(&a.req)
	a.pin.Unpin()
	return err
}
