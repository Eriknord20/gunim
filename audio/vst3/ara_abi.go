package vst3

import (
	"encoding/binary"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"
)

// ARA's interface is plain C: structures of fields, and tables of
// functions that take the object they act on first. The host's side is
// written here as the plugin's is read: field by field, as the platform
// lays the structures out.

// The interfaces through which a VST3 module hands out its ARA factory.
var (
	iidARAMainFactory  = iid(0xDB2A1669, 0xFAFD42A5, 0xA82F864F, 0x7B6872EA)
	iidARAPlugInEntry  = iid(0x12814E54, 0xA1CE4076, 0x82B96813, 0x16950BD6)
	iidARAPlugInEntry2 = iid(0xCD9A5913, 0xC9EB46D7, 0x96CA53AD, 0xD1DB89F5)
)

const (
	// araMainFactoryClass is the category of a module's class that
	// hands out its ARA factory.
	araMainFactoryClass = "ARA Main Factory Class"
	// The methods: IMainFactory's and IPlugInEntryPoint's getFactory,
	// and IPlugInEntryPoint2's bindToDocumentControllerWithRoles.
	mARAGetFactory    = 3
	mARABindWithRoles = 3
	// The API generations the host speaks: 2.0 to 2.3.
	araGeneration2Final   = 4
	araGeneration2_3Final = 6
)

// ptrSize is the size of a pointer, and of a size_t.
const ptrSize = int(unsafe.Sizeof(uintptr(0)))

// araPack is the most a field of ARA's structures is aligned to: 1 on
// x86, where ARA packs its structures to the byte, and 8 on ARM.
var araPack = func() int {
	switch runtime.GOARCH {
	case "amd64", "386":
		return 1
	}
	return 8
}()

// Field kinds of a structure: a positive size is a number or a pointer,
// aligned to its size; bytes(n) is an array of n bytes, aligned to 1.
const (
	fI32 = 4
	fI64 = 8
	fF32 = 4
	fF64 = 8
)

// fPtr is a pointer's or a size_t's field.
var fPtr = ptrSize

// bytesField is an array of n bytes.
func bytesField(n int) int { return -n }

// layout returns where each field of a structure lies, and the
// structure's size, as the platform lays out ARA's.
func layout(fields ...int) (offsets []int, size int) {
	offsets = make([]int, len(fields))
	at, most := 0, 1
	for i, f := range fields {
		n, align := f, f
		if f < 0 {
			n, align = -f, 1
		}
		align = min(align, araPack)
		most = max(most, align)
		at = (at + align - 1) / align * align
		offsets[i] = at
		at += n
	}
	return offsets, (at + most - 1) / most * most
}

// cstruct is one of ARA's structures, in memory of Go's, which Go moves
// nowhere: written field by field, its size in its first.
type cstruct struct {
	b     []byte
	off   []int
	sizes []int
	// pins keeps what the structure points at alive with it.
	pins []any
}

// newStruct makes a structure of fields, its first field, a size_t,
// holding its size.
func newStruct(fields ...int) *cstruct {
	off, size := layout(fields...)
	c := &cstruct{b: make([]byte, max(size, 1)), off: off, sizes: fields}
	c.put(0, uint64(size))
	return c
}

// newPlain makes a structure of fields with no size of its own in it.
func newPlain(fields ...int) *cstruct {
	off, size := layout(fields...)
	return &cstruct{b: make([]byte, max(size, 1)), off: off, sizes: fields}
}

// addr is where the structure lies.
func (c *cstruct) addr() uintptr { return uintptr(unsafe.Pointer(&c.b[0])) }

// put writes field i, a number or a pointer.
func (c *cstruct) put(i int, v uint64) {
	b := c.b[c.off[i]:]
	switch c.sizes[i] {
	case 4:
		binary.LittleEndian.PutUint32(b, uint32(v))
	case 8:
		binary.LittleEndian.PutUint64(b, v)
	}
}

// putPtr writes field i, a pointer to p, which it keeps alive.
func (c *cstruct) putPtr(i int, p uintptr, pin any) {
	c.put(i, uint64(p))
	if pin != nil {
		c.pins = append(c.pins, pin)
	}
}

// putF64 writes field i, a double.
func (c *cstruct) putF64(i int, v float64) { c.put(i, math.Float64bits(v)) }

// putF32 writes field i, a float.
func (c *cstruct) putF32(i int, v float32) { c.put(i, uint64(math.Float32bits(v))) }

// putString writes field i, a pointer to s as a C string, or NULL for
// none.
func (c *cstruct) putString(i int, s string) {
	if s == "" {
		c.put(i, 0)
		return
	}
	b := cstr(s)
	c.putPtr(i, uintptr(unsafe.Pointer(&b[0])), b)
}

// putBytes writes field i, an array of bytes.
func (c *cstruct) putBytes(i int, v []byte) { copy(c.b[c.off[i]:c.off[i]-c.sizes[i]], v) }

// cstr returns s as a C string.
func cstr(s string) []byte { return append([]byte(s), 0) }

// readStruct reads fields of a structure of the plugin's at p.
type readStruct struct {
	p     uintptr
	off   []int
	sizes []int
	size  int
}

func readAt(p uintptr, fields ...int) readStruct {
	off, size := layout(fields...)
	return readStruct{p: p, off: off, sizes: fields, size: size}
}

// has says the structure, whose first field is its size, holds field i.
func (r readStruct) has(i int) bool {
	return r.p != 0 && r.get(0) >= uint64(r.off[i]+abs(r.sizes[i]))
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// get reads field i, a number or a pointer.
func (r readStruct) get(i int) uint64 {
	at := r.p + uintptr(r.off[i])
	switch r.sizes[i] {
	case 4:
		return uint64(*ptr[uint32](at))
	case 8:
		return *ptr[uint64](at)
	}
	return 0
}

func (r readStruct) ptr(i int) uintptr  { return uintptr(r.get(i)) }
func (r readStruct) i32(i int) int32    { return int32(uint32(r.get(i))) }
func (r readStruct) f64(i int) float64  { return math.Float64frombits(r.get(i)) }
func (r readStruct) f32(i int) float32  { return math.Float32frombits(uint32(r.get(i))) }
func (r readStruct) str(i int) string   { return cstringAt(r.ptr(i)) }
func (r readStruct) at(i int) uintptr   { return r.p + uintptr(r.off[i]) }
func (r readStruct) bytes(i int) []byte { return unsafe.Slice(ptr[byte](r.at(i)), -r.sizes[i]) }
func (r readStruct) boolean(i int) bool { return r.get(i)&0xffffffff != 0 }
func (r readStruct) count(i int) int    { return int(r.get(i)) }
func (r readStruct) strings(n, at int) []string {
	out := make([]string, r.count(n))
	for k := range out {
		out[k] = cstringAt(*ptr[uintptr](r.ptr(at) + uintptr(k*ptrSize)))
	}
	return out
}

// A table of functions: its size first, then a pointer to each.

// fnTable is a table of functions of the host's, as a plugin calls them.
type fnTable []uintptr

// newTable returns a table of fns, its size first.
func newTable(fns ...uintptr) fnTable {
	t := make(fnTable, 1+len(fns))
	t[0] = uintptr(len(t) * ptrSize)
	copy(t[1:], fns)
	return t
}

func (t fnTable) addr() uintptr { return uintptr(unsafe.Pointer(&t[0])) }

// cfn calls the C function at fn with args.
//
//go:uintptrescapes
func cfn(fn uintptr, args ...uintptr) uintptr {
	var all [maxCallArgs]uintptr
	n := copy(all[:], args)
	return callN(fn, all[:n])
}

// iface is one of the plugin's tables of functions, with the object
// they act on.
type iface struct {
	ref, table uintptr
}

// has says the table holds function i, from 0, after its size.
func (f iface) has(i int) bool {
	return f.table != 0 && *ptr[uintptr](f.table) >= uintptr((i+2)*ptrSize)
}

// call calls function i of the table, from 0, on its object, with args.
func (f iface) call(i int, args ...uintptr) uintptr {
	fn := *ptr[uintptr](f.table + uintptr((i+1)*ptrSize))
	var all [maxCallArgs]uintptr
	all[0] = f.ref
	n := 1 + copy(all[1:], args)
	return callN(fn, all[:n])
}

// Handles: the host's objects as the plugin holds them, numbers of the
// host's, looked up as the plugin calls back.

var (
	handles    sync.Map
	lastHandle atomic.Uintptr
)

// newHandle keeps v under a new handle, and returns it.
func newHandle(v any) uintptr {
	h := lastHandle.Add(1)
	handles.Store(h, v)
	return h
}

// handle returns what h names, as a T, or nil.
func handle[T any](h uintptr) *T {
	v, _ := handles.Load(h)
	t, _ := v.(*T)
	return t
}

// dropHandle lets go of h.
func dropHandle(h uintptr) { handles.Delete(h) }

// araBool is an ARABool, as the C side reads it.
func araBool(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}
