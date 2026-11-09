//go:build darwin && !ios

package midi

import (
	"fmt"
	"math/bits"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// portAddr is a port's endpoint, and its unique ID, by which the
// package tells it is gone.
type portAddr struct {
	ref uint32 // MIDIEndpointRef
	uid int32
}

const (
	noErr = 0

	// CoreMIDI's errors for an endpoint that went away.
	midiUnknownEndpoint = -10834
	midiObjectNotFound  = -10842

	cfStringEncodingUTF8 = 0x08000100
	cfRunLoopRunFinished = 1

	// packetData is where a MIDIPacket's bytes start: after its
	// timestamp and length. A MIDIPacketList packs to 4 bytes, so the
	// first packet starts after the count, at 4.
	packetData = 8 + 2
	listHeader = 4
	// maxPacket is the most bytes of a sysex sent in one MIDISend.
	maxPacket = 32 << 10
)

var (
	midiClientCreate       func(name uintptr, notify uintptr, refCon uintptr, client *uint32) int32
	midiInputPortCreate    func(client uint32, name uintptr, read uintptr, refCon uintptr, port *uint32) int32
	midiOutputPortCreate   func(client uint32, name uintptr, port *uint32) int32
	midiPortConnectSource  func(port, src uint32, connRefCon uintptr) int32
	midiPortDisconnectSrc  func(port, src uint32) int32
	midiPortDispose        func(port uint32) int32
	midiGetNumberOfSources func() uint64
	midiGetSource          func(i uint64) uint32
	midiGetNumberOfDests   func() uint64
	midiGetDestination     func(i uint64) uint32
	midiObjectGetString    func(obj uint32, prop uintptr, out *uintptr) int32
	midiObjectGetInteger   func(obj uint32, prop uintptr, out *int32) int32
	midiFlushOutput        func(dest uint32) int32
	cfStringCreate         func(alloc uintptr, s string, encoding uint32) uintptr
	cfStringGetCString     func(s uintptr, buf *byte, size int, encoding uint32) bool
	cfRelease              func(ref uintptr)
	cfRunLoopRunInMode     func(mode uintptr, seconds float64, returnAfterSource bool) int32
	machAbsoluteTime       func() uint64
	machTimebaseInfo       func(info *timebase) int32

	// midiSend is MIDISend's address: Send calls it through SyscallN,
	// with arguments held in the Out, so a message makes nothing new.
	midiSend uintptr

	// The CFStrings of the properties read, and the run loop's mode.
	// CoreMIDI sets its keys as the first client is made: until then
	// keyDisplayName and keyUniqueID hold where they are kept.
	propDisplayName, propUniqueID, runLoopDefaultMode uintptr
	keyDisplayName, keyUniqueID                       uintptr

	// readProc and notifyProc are the callbacks CoreMIDI calls, made
	// once: purego keeps a callback for good.
	readProc, notifyProc uintptr
)

type timebase struct{ numer, denom uint32 }

var (
	loadOnce sync.Once
	loadErr  error
)

func load() error {
	loadOnce.Do(func() {
		cm, err := purego.Dlopen("/System/Library/Frameworks/CoreMIDI.framework/CoreMIDI", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		sys, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		purego.RegisterLibFunc(&midiClientCreate, cm, "MIDIClientCreate")
		purego.RegisterLibFunc(&midiInputPortCreate, cm, "MIDIInputPortCreate")
		purego.RegisterLibFunc(&midiOutputPortCreate, cm, "MIDIOutputPortCreate")
		purego.RegisterLibFunc(&midiPortConnectSource, cm, "MIDIPortConnectSource")
		purego.RegisterLibFunc(&midiPortDisconnectSrc, cm, "MIDIPortDisconnectSource")
		purego.RegisterLibFunc(&midiPortDispose, cm, "MIDIPortDispose")
		purego.RegisterLibFunc(&midiGetNumberOfSources, cm, "MIDIGetNumberOfSources")
		purego.RegisterLibFunc(&midiGetSource, cm, "MIDIGetSource")
		purego.RegisterLibFunc(&midiGetNumberOfDests, cm, "MIDIGetNumberOfDestinations")
		purego.RegisterLibFunc(&midiGetDestination, cm, "MIDIGetDestination")
		purego.RegisterLibFunc(&midiObjectGetString, cm, "MIDIObjectGetStringProperty")
		purego.RegisterLibFunc(&midiObjectGetInteger, cm, "MIDIObjectGetIntegerProperty")
		purego.RegisterLibFunc(&midiFlushOutput, cm, "MIDIFlushOutput")
		purego.RegisterLibFunc(&cfStringCreate, cf, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&cfStringGetCString, cf, "CFStringGetCString")
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&cfRunLoopRunInMode, cf, "CFRunLoopRunInMode")
		purego.RegisterLibFunc(&machAbsoluteTime, sys, "mach_absolute_time")
		purego.RegisterLibFunc(&machTimebaseInfo, sys, "mach_timebase_info")
		if midiSend, err = purego.Dlsym(cm, "MIDISend"); err != nil {
			loadErr = err
			return
		}
		// The keys are CFString globals: Dlsym gives where each is
		// kept.
		for _, s := range []struct {
			lib  uintptr
			name string
			to   *uintptr
		}{
			{cm, "kMIDIPropertyDisplayName", &keyDisplayName},
			{cm, "kMIDIPropertyUniqueID", &keyUniqueID},
			{cf, "kCFRunLoopDefaultMode", &runLoopDefaultMode},
		} {
			if *s.to, err = purego.Dlsym(s.lib, s.name); err != nil {
				loadErr = err
				return
			}
		}
		runLoopDefaultMode = *(*uintptr)(unsafe.Pointer(runLoopDefaultMode))
		var tb timebase
		if machTimebaseInfo(&tb) != noErr || tb.numer == 0 || tb.denom == 0 {
			loadErr = fmt.Errorf("midi: mach_timebase_info failed")
			return
		}
		clock.tb = tb
		// Go's monotonic clock counts mach_absolute_time too, so one
		// reading of both ties them for good.
		h0 := machAbsoluteTime()
		now := time.Now()
		h1 := machAbsoluteTime()
		clock.host, clock.at = h0+(h1-h0)/2, now
		readProc = purego.NewCallback(read)
		notifyProc = purego.NewCallback(notify)
	})
	return loadErr
}

// osError is an OSStatus a CoreMIDI function returned.
type osError struct {
	op   string
	code int32
}

func (e *osError) Error() string {
	switch e.code {
	case midiUnknownEndpoint, midiObjectNotFound:
		return e.op + ": the device is gone"
	}
	return fmt.Sprintf("%s: error %d", e.op, e.code)
}

func osErr(op string, st int32) error {
	if st != noErr {
		return &osError{op, st}
	}
	return nil
}

func goneStatus(st int32) bool { return st == midiUnknownEndpoint || st == midiObjectNotFound }

// clock ties CoreMIDI's host time, mach_absolute_time's ticks, to Go's
// monotonic clock.
var clock struct {
	tb   timebase
	host uint64
	at   time.Time
}

// nanos is host ticks h in nanoseconds.
func nanos(h uint64) int64 {
	hi, lo := bits.Mul64(h, uint64(clock.tb.numer))
	q, _ := bits.Div64(hi, lo, uint64(clock.tb.denom))
	return int64(q)
}

// timeOf is the time of host ticks h, never after now.
func timeOf(h uint64) time.Time {
	now := time.Now()
	if h == 0 {
		return now
	}
	t := clock.at.Add(time.Duration(nanos(h) - nanos(clock.host)))
	if t.After(now) {
		return now
	}
	return t
}

// hostAt is t in host ticks, 0, which CoreMIDI sends at once, for a
// time already past.
func hostAt(t time.Time) uint64 {
	if t.IsZero() {
		return 0
	}
	d := t.Sub(clock.at)
	if t.Before(time.Now()) {
		return 0
	}
	ns := uint64(nanos(clock.host) + int64(d))
	hi, lo := bits.Mul64(ns, uint64(clock.tb.denom))
	h, _ := bits.Div64(hi, lo, uint64(clock.tb.numer))
	return h
}

// client is the package's MIDI client. Its thread runs a run loop of
// its own, as CoreMIDI tells of ports coming and going on the run loop
// that made the client, and fills the lists it is asked for from what
// it was told.
var client struct {
	once    sync.Once
	ref     uint32
	err     error
	changed chan struct{}
}

func startClient() error {
	client.once.Do(func() {
		if client.err = load(); client.err != nil {
			return
		}
		client.changed = make(chan struct{}, 1)
		ready := make(chan error)
		go func() {
			// The thread stays in CoreMIDI's run loop for good.
			runtime.LockOSThread()
			name := cfStringCreate(0, "gunim", cfStringEncodingUTF8)
			st := midiClientCreate(name, notifyProc, 0, &client.ref)
			cfRelease(name)
			ready <- osErr("MIDIClientCreate", st)
			if st != noErr {
				runtime.UnlockOSThread()
				return
			}
			for {
				if cfRunLoopRunInMode(runLoopDefaultMode, 60, false) == cfRunLoopRunFinished {
					// Nothing to wait on yet: nothing to run.
					time.Sleep(time.Second)
				}
			}
		}()
		client.err = <-ready
		if client.err != nil {
			return
		}
		propDisplayName = *(*uintptr)(unsafe.Pointer(keyDisplayName))
		propUniqueID = *(*uintptr)(unsafe.Pointer(keyUniqueID))
		if propDisplayName == 0 || propUniqueID == 0 {
			client.err = fmt.Errorf("midi: CoreMIDI set no property keys")
			return
		}
		go follow()
	})
	return client.err
}

// notify is the client's MIDINotifyProc. It hands the news on to
// follow, as a callback is to return quickly.
func notify(msg, refCon uintptr) {
	select {
	case client.changed <- struct{}{}:
	default:
	}
}

// follow lists the ports as CoreMIDI tells they changed, marks gone
// the open ports no longer listed, and announces a change.
func follow() {
	list := func() (in, out []sysPort) {
		in, _ = sysList(false)
		out, _ = sysList(true)
		return in, out
	}
	in, out := list()
	for range client.changed {
		in2, out2 := list()
		if slices.Equal(in, in2) && slices.Equal(out, out2) {
			continue
		}
		in, out = in2, out2
		goneWhere(func(a portAddr, isOut bool) bool {
			ps := in
			if isOut {
				ps = out
			}
			return !slices.ContainsFunc(ps, func(p sysPort) bool { return p.addr.uid == a.uid })
		})
		announce()
	}
}

// endpointName is an endpoint's display name, as "Keystation 49 MK3"
// or "IAC Driver Bus 1".
func endpointName(e uint32) string {
	var s uintptr
	if midiObjectGetString(e, propDisplayName, &s) != noErr || s == 0 {
		return ""
	}
	defer cfRelease(s)
	buf := make([]byte, 1024)
	if !cfStringGetCString(s, &buf[0], len(buf), cfStringEncodingUTF8) {
		return ""
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}

func sysList(out bool) ([]sysPort, error) {
	if err := startClient(); err != nil {
		return nil, err
	}
	n, get := midiGetNumberOfSources, midiGetSource
	if out {
		n, get = midiGetNumberOfDests, midiGetDestination
	}
	var ps []sysPort
	for i := range n() {
		e := get(i)
		if e == 0 {
			continue
		}
		var uid int32
		_ = midiObjectGetInteger(e, propUniqueID, &uid)
		ps = append(ps, sysPort{endpointName(e), portAddr{e, uid}})
	}
	return ps, nil
}

func sysWatch() { _ = startClient() }

// outSys is an Out's CoreMIDI port. CoreMIDI holds a message sent
// ahead itself, and sends it at its timestamp.
type outSys struct {
	port uint32
	dest uint32

	mu sync.Mutex // held over each message out
	// short holds the packet list of a short message, of uint32s for
	// the list's alignment; long a sysex's, and sysex its bytes.
	short [8]uint32
	long  []uint32
	sysex []byte
	args  [3]uintptr // MIDISend's, made once
}

func (s *outSys) open(o *Out) error {
	if err := startClient(); err != nil {
		return err
	}
	name := cfStringCreate(0, "gunim out", cfStringEncodingUTF8)
	defer cfRelease(name)
	if err := osErr("MIDIOutputPortCreate", midiOutputPortCreate(client.ref, name, &s.port)); err != nil {
		return err
	}
	s.dest = o.addr.ref
	return nil
}

func (s *outSys) send(o *Out, at time.Time, m Message) error {
	ts := hostAt(at)
	s.mu.Lock()
	defer s.mu.Unlock()
	var st int32
	if m.Status != 0xF0 {
		b := unsafe.Slice((*byte)(unsafe.Pointer(&s.short[0])), len(s.short)*4)
		st = s.write(b, ts, m.AppendTo(b[listHeader+packetData:listHeader+packetData]))
	} else {
		s.sysex = m.AppendTo(s.sysex[:0])
		data := s.sysex
		for len(data) > 0 && st == noErr {
			n := min(len(data), maxPacket)
			if need := (listHeader + packetData + n + 3) / 4; len(s.long) < need {
				s.long = make([]uint32, need)
			}
			b := unsafe.Slice((*byte)(unsafe.Pointer(&s.long[0])), len(s.long)*4)
			copy(b[listHeader+packetData:], data[:n])
			st = s.write(b, ts, b[listHeader+packetData:listHeader+packetData+n])
			data = data[n:]
		}
	}
	if goneStatus(st) {
		go o.gone()
		return ErrGone
	}
	return osErr("MIDISend", st)
}

// write sends the packet list in b, of one packet, at host time ts,
// its bytes data, already in place after the packet's header.
func (s *outSys) write(b []byte, ts uint64, data []byte) int32 {
	*(*uint32)(unsafe.Pointer(&b[0])) = 1
	*(*uint64)(unsafe.Pointer(&b[listHeader])) = ts
	*(*uint16)(unsafe.Pointer(&b[listHeader+8])) = uint16(len(data))
	s.args = [3]uintptr{uintptr(s.port), uintptr(s.dest), uintptr(unsafe.Pointer(&b[0]))}
	r, _, _ := purego.SyscallN(midiSend, s.args[:]...)
	return int32(r)
}

func (s *outSys) cancel(*Out) error {
	return osErr("MIDIFlushOutput", midiFlushOutput(s.dest))
}

func (s *outSys) close(*Out) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.port == 0 {
		return nil
	}
	err := osErr("MIDIPortDispose", midiPortDispose(s.port))
	s.port = 0
	return err
}

func (s *outSys) gone(o *Out) { _ = s.close(o) }

// inSys is an In's CoreMIDI port. CoreMIDI calls read with what comes
// in, on a thread of its own, which hands each message to f.
type inSys struct {
	port uint32
	key  uintptr // its key in ins
	p    Parser  // read by CoreMIDI's thread alone
	shut sync.Once
}

// ins are the open Ins, by the key their port's connection is given.
var (
	ins     sync.Map
	insNext atomic.Uintptr
)

func (s *inSys) open(i *In) error {
	if err := startClient(); err != nil {
		return err
	}
	s.key = insNext.Add(1)
	ins.Store(s.key, i)
	name := cfStringCreate(0, "gunim in", cfStringEncodingUTF8)
	defer cfRelease(name)
	if err := osErr("MIDIInputPortCreate", midiInputPortCreate(client.ref, name, readProc, 0, &s.port)); err != nil {
		ins.Delete(s.key)
		return err
	}
	if err := osErr("MIDIPortConnectSource", midiPortConnectSource(s.port, i.addr.ref, s.key)); err != nil {
		_ = midiPortDispose(s.port)
		ins.Delete(s.key)
		return err
	}
	return nil
}

// read is every input port's MIDIReadProc: it parses each packet of
// list, and hands each message to the In whose key conn is.
func read(list, _, conn uintptr) {
	v, ok := ins.Load(conn)
	if !ok {
		return
	}
	i := v.(*In)
	l := unsafe.Pointer(list)
	n := *(*uint32)(l)
	pkt := unsafe.Add(l, listHeader)
	for range n {
		ts := *(*uint64)(pkt)
		size := uintptr(*(*uint16)(unsafe.Add(pkt, 8)))
		t := timeOf(ts)
		i.sys.p.Parse(unsafe.Slice((*byte)(unsafe.Add(pkt, packetData)), size), func(m Message) {
			i.deliver(Event{Time: t, Message: m})
		})
		next := uintptr(pkt) + packetData + size
		if runtime.GOARCH == "arm64" {
			// MIDIPacketNext: a packet starts on 4 bytes on ARM.
			next = (next + 3) &^ 3
		}
		pkt = unsafe.Add(pkt, next-uintptr(pkt))
	}
}

func (s *inSys) close(i *In) error {
	var err error
	s.shut.Do(func() {
		ins.Delete(s.key)
		_ = midiPortDisconnectSrc(s.port, i.addr.ref)
		err = osErr("MIDIPortDispose", midiPortDispose(s.port))
	})
	return err
}

func (s *inSys) gone(i *In) { _ = s.close(i) }
