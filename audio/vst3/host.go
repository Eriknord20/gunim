package vst3

import (
	"errors"
	"sync"
	"sync/atomic"
	"unicode/utf16"
	"unsafe"
)

// The host's objects are Go values the plugin holds as pointers: each
// starts with its table of functions, as an object of C++ does, and is
// kept in live while the plugin may hold it.

// A kind is a sort of host object: the interfaces it answers, and its
// table, made once.
type kind struct {
	iids []uid
	// make builds the table's own methods, after FUnknown's three.
	make func() []uintptr
	once sync.Once
	vt   []uintptr
	// other answers interfaces the object hands to another object of the
	// host's, as a plug frame does its run loop.
	other func(this uintptr, id uid) uintptr
}

func (k *kind) table() *uintptr {
	k.once.Do(func() {
		unknown.once.Do(func() {
			unknown.vt = []uintptr{
				newCallback(queryInterface),
				newCallback(addRef),
				newCallback(releaseObject),
			}
		})
		k.vt = append(append([]uintptr(nil), unknown.vt...), k.make()...)
	})
	return &k.vt[0]
}

// unknown holds FUnknown's three methods, which every kind shares.
var unknown kind

// object is the start of every host object.
type object struct {
	vt   *uintptr
	refs atomic.Int32
	kind *kind
	// transient objects, as the messages a plugin asks for, go once
	// released; the others live as long as what owns them.
	transient bool
}

// live holds the host's objects while a plugin may.
var live sync.Map

// hostObject is any of the host's objects.
type hostObject interface{ base() *object }

func (o *object) base() *object { return o }

// keep makes v, an object of kind k, ready to hand to a plugin, and
// returns it as the plugin sees it.
func keep(v hostObject, k *kind, transient bool) uintptr {
	o := v.base()
	o.vt, o.kind, o.transient = k.table(), k, transient
	o.refs.Store(1)
	p := uintptr(unsafe.Pointer(o))
	live.Store(p, v)
	return p
}

// self returns the host's object a plugin calls, at this: looked up, as
// Go keeps its pointers, rather than made from the number.
func self[T any](this uintptr) *T {
	v, _ := live.Load(this)
	t, _ := v.(*T)
	return t
}

// selfObject returns the start of the object at this.
func selfObject(this uintptr) *object {
	v, _ := live.Load(this)
	o, _ := v.(hostObject)
	return o.base()
}

// drop lets go of the object at p.
func drop(p uintptr) { live.Delete(p) }

func queryInterface(this, id, out uintptr) uintptr {
	o := selfObject(this)
	want := *ptr[uid](id)
	if want == iidFUnknown {
		o.refs.Add(1)
		*ptr[uintptr](out) = this
		return resultOf(resultOK)
	}
	for _, i := range o.kind.iids {
		if i == want {
			o.refs.Add(1)
			*ptr[uintptr](out) = this
			return resultOf(resultOK)
		}
	}
	if o.kind.other != nil {
		if p := o.kind.other(this, want); p != 0 {
			*ptr[uintptr](out) = p
			return resultOf(resultOK)
		}
	}
	*ptr[uintptr](out) = 0
	return resultOf(resultNoInterface)
}

func addRef(this uintptr) uintptr {
	return uintptr(selfObject(this).refs.Add(1))
}

func releaseObject(this uintptr) uintptr {
	o := selfObject(this)
	n := o.refs.Add(-1)
	if n <= 0 && o.transient {
		drop(this)
	}
	return uintptr(max(n, 0))
}

// resultOf is a result code as a method of the host's returns it.
func resultOf(r int32) uintptr { return uintptr(uint32(r)) }

// i32 is an int32 argument, of the bits the call passed.
func i32(a uintptr) int32 { return int32(uint32(a)) }

// putString128 writes s to the String128 at p.
func putString128(p uintptr, s string) {
	dst := ptr[[128]uint16](p)
	n := copy(dst[:127], utf16.Encode([]rune(s)))
	dst[n] = 0
}

// cstringAt reads the C string at p.
func cstringAt(p uintptr) string {
	if p == 0 {
		return ""
	}
	var b []byte
	for i := uintptr(0); ; i++ {
		c := *ptr[byte](p + i)
		if c == 0 {
			return string(b)
		}
		b = append(b, c)
	}
}

// The host application, which the plugin is initialized with: it names
// the host and makes the messages a plugin's halves talk with.

type hostApp struct {
	object
}

var hostKind = &kind{iids: []uid{iidHostApplication}, make: func() []uintptr {
	return []uintptr{
		newCallback(func(this, name uintptr) uintptr {
			putString128(name, "gunim")
			return resultOf(resultOK)
		}),
		newCallback(func(this, cid, iidp, out uintptr) uintptr {
			c, i := *ptr[uid](cid), *ptr[uid](iidp)
			var p uintptr
			switch {
			case c == iidMessage && i == iidMessage:
				p = newMessage()
			case c == iidAttributeList && i == iidAttributeList:
				p = newAttributes()
			default:
				*ptr[uintptr](out) = 0
				return resultOf(resultFalse)
			}
			*ptr[uintptr](out) = p
			return resultOf(resultOK)
		}),
	}
}}

// theHost is the one host application.
var theHost = sync.OnceValue(func() uintptr {
	return keep(new(hostApp), hostKind, false)
})

// A message, as one half of a plugin sends the other.

type message struct {
	object
	id    []byte
	attrs uintptr
}

var messageKind = &kind{iids: []uid{iidMessage}, make: func() []uintptr {
	return []uintptr{
		newCallback(func(this uintptr) uintptr {
			m := self[message](this)
			if len(m.id) == 0 {
				return 0
			}
			return uintptr(unsafe.Pointer(&m.id[0]))
		}),
		newCallback(func(this, id uintptr) uintptr {
			m := self[message](this)
			m.id = append([]byte(cstringAt(id)), 0)
			return 0
		}),
		newCallback(func(this uintptr) uintptr {
			return self[message](this).attrs
		}),
	}
}}

func newMessage() uintptr {
	m := &message{attrs: newAttributes()}
	return keep(m, messageKind, true)
}

// An attribute list, a message's contents.

type attribute struct {
	i int64
	f float64
	// s is a string, as UTF-16 ending in 0, and b a blob.
	s []uint16
	b []byte
}

type attributes struct {
	object
	mu sync.Mutex
	m  map[string]*attribute
}

func (a *attributes) set(id uintptr, v attribute) uintptr {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.m[cstringAt(id)] = &v
	return resultOf(resultOK)
}

func (a *attributes) get(id uintptr) *attribute {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.m[cstringAt(id)]
}

var attributesKind = &kind{iids: []uid{iidAttributeList}, make: func() []uintptr {
	at := self[attributes]
	missing := resultOf(resultFalse)
	return []uintptr{
		newCallback(func(this, id, v uintptr) uintptr { return at(this).set(id, attribute{i: int64(v)}) }),
		newCallback(func(this, id, out uintptr) uintptr {
			a := at(this).get(id)
			if a == nil {
				return missing
			}
			*ptr[int64](out) = a.i
			return resultOf(resultOK)
		}),
		newFloatCallback(func(this, id uintptr, v float64) uintptr { return at(this).set(id, attribute{f: v}) }),
		newCallback(func(this, id, out uintptr) uintptr {
			a := at(this).get(id)
			if a == nil {
				return missing
			}
			*ptr[float64](out) = a.f
			return resultOf(resultOK)
		}),
		newCallback(func(this, id, s uintptr) uintptr {
			v := attribute{}
			for i := uintptr(0); ; i += 2 {
				c := *ptr[uint16](s + i)
				v.s = append(v.s, c)
				if c == 0 {
					break
				}
			}
			return at(this).set(id, v)
		}),
		newCallback(func(this, id, s, size uintptr) uintptr {
			a := at(this).get(id)
			if a == nil || a.s == nil {
				return missing
			}
			n := int(uint32(size)) / 2
			if n == 0 {
				return missing
			}
			dst := unsafe.Slice(ptr[uint16](s), n)
			k := copy(dst[:n-1], a.s)
			dst[k] = 0
			return resultOf(resultOK)
		}),
		newCallback(func(this, id, data, size uintptr) uintptr {
			b := make([]byte, uint32(size))
			if len(b) > 0 {
				copy(b, unsafe.Slice(ptr[byte](data), len(b)))
			}
			return at(this).set(id, attribute{b: b})
		}),
		newCallback(func(this, id, data, size uintptr) uintptr {
			a := at(this).get(id)
			if a == nil || a.b == nil {
				return missing
			}
			*ptr[uintptr](data) = 0
			if len(a.b) > 0 {
				*ptr[uintptr](data) = uintptr(unsafe.Pointer(&a.b[0]))
			}
			*ptr[uint32](size) = uint32(len(a.b))
			return resultOf(resultOK)
		}),
	}
}}

func newAttributes() uintptr {
	a := &attributes{m: map[string]*attribute{}}
	return keep(a, attributesKind, true)
}

// A stream, in memory, that a plugin writes its state to and reads it
// from.

type stream struct {
	object
	b   []byte
	pos int64
}

var streamKind = &kind{iids: []uid{iidBStream}, make: func() []uintptr {
	st := self[stream]
	return []uintptr{
		newCallback(func(this, buf, n, read uintptr) uintptr {
			s := st(this)
			k := 0
			if want := int(i32(n)); want > 0 && s.pos < int64(len(s.b)) {
				k = copy(unsafe.Slice(ptr[byte](buf), want), s.b[s.pos:])
			}
			s.pos += int64(k)
			if read != 0 {
				*ptr[int32](read) = int32(k)
			}
			return resultOf(resultOK)
		}),
		newCallback(func(this, buf, n, written uintptr) uintptr {
			s := st(this)
			k := int(i32(n))
			if k < 0 {
				return resultOf(resultInvalid)
			}
			if end := s.pos + int64(k); end > int64(len(s.b)) {
				s.b = append(s.b, make([]byte, end-int64(len(s.b)))...)
			}
			if k > 0 {
				copy(s.b[s.pos:], unsafe.Slice(ptr[byte](buf), k))
			}
			s.pos += int64(k)
			if written != 0 {
				*ptr[int32](written) = int32(k)
			}
			return resultOf(resultOK)
		}),
		newCallback(func(this, pos, mode, out uintptr) uintptr {
			s := st(this)
			p := int64(pos)
			switch i32(mode) {
			case 1:
				p += s.pos
			case 2:
				p += int64(len(s.b))
			}
			if p < 0 {
				return resultOf(resultInvalid)
			}
			s.pos = p
			if out != 0 {
				*ptr[int64](out) = p
			}
			return resultOf(resultOK)
		}),
		newCallback(func(this, out uintptr) uintptr {
			*ptr[int64](out) = st(this).pos
			return resultOf(resultOK)
		}),
	}
}}

// newStream returns a stream reading b, and its pointer; drop it once
// done.
func newStream(b []byte) (s *stream, p uintptr) {
	s = &stream{b: b}
	return s, keep(s, streamKind, false)
}

// The changes of parameters in a block of processing, in and out: a
// queue of points for each parameter changed.

// maxPoints is how many points a queue holds in a block.
const maxPoints = 64

type point struct {
	offset int32
	value  float64
}

type paramQueue struct {
	object
	id     uint32
	n      int
	points [maxPoints]point
}

type paramChanges struct {
	object
	queues []*paramQueue
	ptrs   []uintptr
	n      int
}

var queueKind = &kind{iids: []uid{iidParamValueQueue}, make: func() []uintptr {
	q := self[paramQueue]
	return []uintptr{
		newCallback(func(this uintptr) uintptr { return uintptr(q(this).id) }),
		newCallback(func(this uintptr) uintptr { return uintptr(uint32(q(this).n)) }),
		newCallback(func(this, index, offset, value uintptr) uintptr {
			qu, i := q(this), int(i32(index))
			if i < 0 || i >= qu.n {
				return resultOf(resultInvalid)
			}
			*ptr[int32](offset) = qu.points[i].offset
			*ptr[float64](value) = qu.points[i].value
			return resultOf(resultOK)
		}),
		newFloatCallback4(func(this, offset uintptr, value float64, index uintptr) uintptr {
			qu := q(this)
			i := qu.add(i32(offset), value)
			if i < 0 {
				return resultOf(resultFalse)
			}
			if index != 0 {
				*ptr[int32](index) = int32(i)
			}
			return resultOf(resultOK)
		}),
	}
}}

// add adds a point, in order of offset, replacing one at the same
// offset; it returns its index, or -1 with the queue full.
func (q *paramQueue) add(offset int32, value float64) int {
	for i := range q.n {
		if q.points[i].offset == offset {
			q.points[i].value = value
			return i
		}
	}
	if q.n == maxPoints {
		return -1
	}
	i := q.n
	for i > 0 && q.points[i-1].offset > offset {
		q.points[i] = q.points[i-1]
		i--
	}
	q.points[i] = point{offset, value}
	q.n++
	return i
}

var changesKind = &kind{iids: []uid{iidParameterChanges}, make: func() []uintptr {
	pc := self[paramChanges]
	return []uintptr{
		newCallback(func(this uintptr) uintptr { return uintptr(uint32(pc(this).n)) }),
		newCallback(func(this, index uintptr) uintptr {
			c, i := pc(this), int(i32(index))
			if i < 0 || i >= c.n {
				return 0
			}
			return c.ptrs[i]
		}),
		newCallback(func(this, id, index uintptr) uintptr {
			c := pc(this)
			i := c.queue(*ptr[uint32](id))
			if i < 0 {
				return 0
			}
			if index != 0 {
				*ptr[int32](index) = int32(i)
			}
			return c.ptrs[i]
		}),
	}
}}

// newChanges returns a set of changes that holds up to n parameters in a
// block.
func newChanges(n int) (c *paramChanges, p uintptr) {
	c = &paramChanges{queues: make([]*paramQueue, n), ptrs: make([]uintptr, n)}
	for i := range c.queues {
		c.queues[i] = new(paramQueue)
		c.ptrs[i] = keep(c.queues[i], queueKind, false)
	}
	return c, keep(c, changesKind, false)
}

// queue returns the index of id's queue, adding it, or -1 with the
// changes full.
func (c *paramChanges) queue(id uint32) int {
	for i := range c.n {
		if c.queues[i].id == id {
			return i
		}
	}
	if c.n == len(c.queues) {
		return -1
	}
	q := c.queues[c.n]
	q.id, q.n = id, 0
	c.n++
	return c.n - 1
}

// clear empties the changes for the next block.
func (c *paramChanges) clear() { c.n = 0 }

// free lets go of the changes and their queues.
func (c *paramChanges) free() {
	for _, p := range c.ptrs {
		drop(p)
	}
	drop(uintptr(unsafe.Pointer(&c.object))) //nolint:gosec // its own address
}

// The handler of a plugin's edits: its editor tells the host each change
// of a parameter, which the host passes on to the processor, and each
// gesture's start and end. Its second half, IComponentHandler2, hears
// the plugin say its state changed otherwise, and its groups of edits.

type handler struct {
	object
	p  *Plugin
	h2 uintptr
}

type handler2 struct {
	object
	p *Plugin
}

// newHandler returns the handler of p's edits, ready to hand to its
// controller.
func newHandler(p *Plugin) *handler {
	h := &handler{p: p}
	keep(h, handlerKind, false)
	h.h2 = keep(&handler2{p: p}, handler2Kind, false)
	return h
}

// drop lets go of the handler and its second half.
func (h *handler) drop() {
	drop(h.h2)
	drop(uintptr(unsafe.Pointer(&h.object))) //nolint:gosec // its own address
}

// gesture counts a gesture in the editor starting, by 1, or ending, by
// -1, never below none.
func (p *Plugin) gesture(by int32) {
	for {
		n := p.gestures.Load()
		if p.gestures.CompareAndSwap(n, max(n+by, 0)) {
			return
		}
	}
}

var handlerKind = &kind{iids: []uid{iidComponentHandler}, make: func() []uintptr {
	h := self[handler]
	return []uintptr{
		newCallback(func(this, id uintptr) uintptr {
			h(this).p.gesture(1)
			return resultOf(resultOK)
		}),
		newFloatCallback(func(this, id uintptr, v float64) uintptr {
			h(this).p.edited(uint32(id), v)
			return resultOf(resultOK)
		}),
		newCallback(func(this, id uintptr) uintptr {
			h(this).p.gesture(-1)
			return resultOf(resultOK)
		}),
		newCallback(func(this, flags uintptr) uintptr {
			h(this).p.restart(i32(flags))
			return resultOf(resultOK)
		}),
	}
}, other: func(this uintptr, id uid) uintptr {
	if id != iidComponentHandler2 {
		return 0
	}
	h := self[handler](this)
	if h == nil {
		return 0
	}
	selfObject(h.h2).refs.Add(1)
	return h.h2
}}

var handler2Kind = &kind{iids: []uid{iidComponentHandler2}, make: func() []uintptr {
	h := self[handler2]
	return []uintptr{
		newCallback(func(this, state uintptr) uintptr {
			if state&0xff != 0 {
				h(this).p.editsMade.Add(1)
			}
			return resultOf(resultOK)
		}),
		newCallback(func(this, name uintptr) uintptr { return resultOf(resultFalse) }),
		newCallback(func(this uintptr) uintptr {
			h(this).p.gesture(1)
			return resultOf(resultOK)
		}),
		newCallback(func(this uintptr) uintptr {
			h(this).p.gesture(-1)
			return resultOf(resultOK)
		}),
	}
}}

var errEntry = errors.New("the module's entry failed")
