//go:build linux && !android

package midi

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

// portAddr is a port's address in the ALSA sequencer: its client's
// number and its own.
type portAddr struct {
	client, port uint8
}

// seqEvent is ALSA's snd_seq_event_t, laid out as the kernel takes it.
type seqEvent struct {
	typ, flags, tag, queue uint8
	sec, nsec              uint32
	srcClient, srcPort     uint8
	dstClient, dstPort     uint8
	data                   [12]byte
}

// Event types, flags and port capabilities, from ALSA's asequencer.h.
const (
	evNoteOn       = 6
	evNoteOff      = 7
	evKeyPress     = 8
	evController   = 10
	evPgmChange    = 11
	evChanPress    = 12
	evPitchBend    = 13
	evControl14    = 14
	evNonRegParam  = 15
	evRegParam     = 16
	evSongPos      = 20
	evSongSel      = 21
	evQFrame       = 22
	evStart        = 30
	evContinue     = 31
	evStop         = 32
	evClock        = 36
	evTuneRequest  = 40
	evReset        = 41
	evSensing      = 42
	evClientStart  = 60
	evClientExit   = 61
	evClientChange = 62
	evPortStart    = 63
	evPortExit     = 64
	evPortChange   = 65
	evSysex        = 130

	timeStampReal  = 1 << 0
	lengthVariable = 1 << 2
	lengthMask     = 3 << 2

	capRead      = 1 << 0
	capWrite     = 1 << 1
	capSubsRead  = 1 << 5
	capSubsWrite = 1 << 6
	capNoExport  = 1 << 7

	typeMIDIGeneric = 1 << 1
	typeApplication = 1 << 20

	queueStart = 30

	removeOutput = 1 << 1
	removeDest   = 1 << 2
	removeTag    = 1 << 9

	openDuplex = 3
	nonBlock   = 1

	// sysexChunk is the most sysex bytes one event carries; a longer
	// sysex leaves as several, as ALSA itself splits one coming in.
	sysexChunk = 256
)

var (
	sndStrerror                      func(errnum int32) string
	sndSeqOpen                       func(seq *uintptr, name string, streams, mode int32) int32
	sndSeqSetClientName              func(seq uintptr, name string) int32
	sndSeqClientID                   func(seq uintptr) int32
	sndSeqSetClientPoolOutput        func(seq uintptr, size uint) int32
	sndSeqSetClientPoolInput         func(seq uintptr, size uint) int32
	sndSeqCreateSimplePort           func(seq uintptr, name string, caps, typ uint32) int32
	sndSeqAllocNamedQueue            func(seq uintptr, name string) int32
	sndSeqControlQueue               func(seq uintptr, q, typ, value int32, ev uintptr) int32
	sndSeqDrainOutput                func(seq uintptr) int32
	sndSeqConnectFrom                func(seq uintptr, myport, client, port int32) int32
	sndSeqPollDescriptors            func(seq uintptr, pfds unsafe.Pointer, space uint32, events int16) int32
	sndSeqClientInfoMalloc           func(p *uintptr) int32
	sndSeqClientInfoSetClient        func(info uintptr, client int32)
	sndSeqClientInfoGetClient        func(info uintptr) int32
	sndSeqClientInfoGetName          func(info uintptr) string
	sndSeqQueryNextClient            func(seq, info uintptr) int32
	sndSeqPortInfoMalloc             func(p *uintptr) int32
	sndSeqPortInfoSetClient          func(info uintptr, client int32)
	sndSeqPortInfoSetPort            func(info uintptr, port int32)
	sndSeqPortInfoGetPort            func(info uintptr) int32
	sndSeqPortInfoGetName            func(info uintptr) string
	sndSeqPortInfoGetCapability      func(info uintptr) uint32
	sndSeqQueryNextPort              func(seq, info uintptr) int32
	sndSeqPortSubscribeMalloc        func(p *uintptr) int32
	sndSeqPortSubscribeSetSender     func(sub uintptr, addr *portAddr)
	sndSeqPortSubscribeSetDest       func(sub uintptr, addr *portAddr)
	sndSeqPortSubscribeSetQueue      func(sub uintptr, q int32)
	sndSeqPortSubscribeSetTimeUpdate func(sub uintptr, on int32)
	sndSeqPortSubscribeSetTimeReal   func(sub uintptr, on int32)
	sndSeqSubscribePort              func(seq, sub uintptr) int32
	sndSeqUnsubscribePort            func(seq, sub uintptr) int32
	sndSeqQueueStatusMalloc          func(p *uintptr) int32
	sndSeqGetQueueStatus             func(seq uintptr, q int32, st uintptr) int32
	sndSeqQueueStatusGetRealTime     func(st uintptr) unsafe.Pointer
	sndSeqRemoveEventsMalloc         func(p *uintptr) int32
	sndSeqRemoveEventsSetCondition   func(rm uintptr, flags uint32)
	sndSeqRemoveEventsSetQueue       func(rm uintptr, q int32)

	// Called through SyscallN, which makes nothing new as it calls.
	fnOutputDirect uintptr // snd_seq_event_output_direct
	fnEventInput   uintptr // snd_seq_event_input
	fnRemoveEvents uintptr // snd_seq_remove_events
	fnRemoveDest   uintptr // snd_seq_remove_events_set_dest
	fnRemoveTag    uintptr // snd_seq_remove_events_set_tag
)

// seq is the program's one client of the sequencer: one port, through
// which every Out sends and every In hears, and one queue, which holds
// messages sent ahead and stamps the ones that come in.
var seq struct {
	once sync.Once
	err  error

	queueOnce sync.Once
	queueErr  error
	running   atomic.Bool // the queue runs

	h      uintptr // snd_seq_t*
	client uint8
	port   uint8
	queue  int32
	fd     int32

	// base is the time the queue's time counts from, and skew how far
	// the queue has fallen behind the clock since, in nanoseconds.
	base time.Time
	skew atomic.Int64

	mu     sync.Mutex // held over calls on h, but for reading events
	ev     seqEvent   // the event Send fills
	sysex  []byte     // a sysex Send sends, with its 0xF0 and 0xF7
	rm     uintptr    // snd_seq_remove_events_t*
	status uintptr    // snd_seq_queue_status_t*
	dest   portAddr   // the address Cancel lets go of messages for
	// args are the arguments of a call through SyscallN, held here as
	// one made of its own arguments would be made anew each call.
	args [2]uintptr

	// readArgs and evp are the reader's: snd_seq_event_input's
	// arguments, and the event it reads.
	readArgs [2]uintptr
	evp      unsafe.Pointer

	route   sync.Mutex
	sources map[portAddr]*source
	scratch []*In // the Ins an event goes to, for the reader alone
}

// A source is a port the client hears, and the Ins listening to it.
type source struct {
	ins []*In
	sub uintptr // snd_seq_port_subscribe_t*
	p   Parser  // gathers a sysex the sequencer splits over events
	buf []byte
}

func loadALSA() error {
	var lib uintptr
	var err error
	for _, name := range []string{"libasound.so.2", "libasound.so"} {
		if lib, err = purego.Dlopen(name, purego.RTLD_LAZY|purego.RTLD_GLOBAL); err == nil {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("loading libasound: %w", err)
	}
	reg := func(fn any, name string) {
		if err == nil {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("libasound: %v", r)
				}
			}()
			purego.RegisterLibFunc(fn, lib, name)
		}
	}
	reg(&sndStrerror, "snd_strerror")
	reg(&sndSeqOpen, "snd_seq_open")
	reg(&sndSeqSetClientName, "snd_seq_set_client_name")
	reg(&sndSeqClientID, "snd_seq_client_id")
	reg(&sndSeqSetClientPoolOutput, "snd_seq_set_client_pool_output")
	reg(&sndSeqSetClientPoolInput, "snd_seq_set_client_pool_input")
	reg(&sndSeqCreateSimplePort, "snd_seq_create_simple_port")
	reg(&sndSeqAllocNamedQueue, "snd_seq_alloc_named_queue")
	reg(&sndSeqControlQueue, "snd_seq_control_queue")
	reg(&sndSeqDrainOutput, "snd_seq_drain_output")
	reg(&sndSeqConnectFrom, "snd_seq_connect_from")
	reg(&sndSeqPollDescriptors, "snd_seq_poll_descriptors")
	reg(&sndSeqClientInfoMalloc, "snd_seq_client_info_malloc")
	reg(&sndSeqClientInfoSetClient, "snd_seq_client_info_set_client")
	reg(&sndSeqClientInfoGetClient, "snd_seq_client_info_get_client")
	reg(&sndSeqClientInfoGetName, "snd_seq_client_info_get_name")
	reg(&sndSeqQueryNextClient, "snd_seq_query_next_client")
	reg(&sndSeqPortInfoMalloc, "snd_seq_port_info_malloc")
	reg(&sndSeqPortInfoSetClient, "snd_seq_port_info_set_client")
	reg(&sndSeqPortInfoSetPort, "snd_seq_port_info_set_port")
	reg(&sndSeqPortInfoGetPort, "snd_seq_port_info_get_port")
	reg(&sndSeqPortInfoGetName, "snd_seq_port_info_get_name")
	reg(&sndSeqPortInfoGetCapability, "snd_seq_port_info_get_capability")
	reg(&sndSeqQueryNextPort, "snd_seq_query_next_port")
	reg(&sndSeqPortSubscribeMalloc, "snd_seq_port_subscribe_malloc")
	reg(&sndSeqPortSubscribeSetSender, "snd_seq_port_subscribe_set_sender")
	reg(&sndSeqPortSubscribeSetDest, "snd_seq_port_subscribe_set_dest")
	reg(&sndSeqPortSubscribeSetQueue, "snd_seq_port_subscribe_set_queue")
	reg(&sndSeqPortSubscribeSetTimeUpdate, "snd_seq_port_subscribe_set_time_update")
	reg(&sndSeqPortSubscribeSetTimeReal, "snd_seq_port_subscribe_set_time_real")
	reg(&sndSeqSubscribePort, "snd_seq_subscribe_port")
	reg(&sndSeqUnsubscribePort, "snd_seq_unsubscribe_port")
	reg(&sndSeqQueueStatusMalloc, "snd_seq_queue_status_malloc")
	reg(&sndSeqGetQueueStatus, "snd_seq_get_queue_status")
	reg(&sndSeqQueueStatusGetRealTime, "snd_seq_queue_status_get_real_time")
	reg(&sndSeqRemoveEventsMalloc, "snd_seq_remove_events_malloc")
	reg(&sndSeqRemoveEventsSetCondition, "snd_seq_remove_events_set_condition")
	reg(&sndSeqRemoveEventsSetQueue, "snd_seq_remove_events_set_queue")
	if err != nil {
		return err
	}
	for _, f := range []struct {
		p    *uintptr
		name string
	}{
		{&fnOutputDirect, "snd_seq_event_output_direct"},
		{&fnEventInput, "snd_seq_event_input"},
		{&fnRemoveEvents, "snd_seq_remove_events"},
		{&fnRemoveDest, "snd_seq_remove_events_set_dest"},
		{&fnRemoveTag, "snd_seq_remove_events_set_tag"},
	} {
		if *f.p, err = purego.Dlsym(lib, f.name); err != nil {
			return fmt.Errorf("libasound: %w", err)
		}
	}
	return nil
}

// alsaError is an error code a libasound function returned.
type alsaError struct {
	op   string
	code int32
}

func (e *alsaError) Error() string {
	return fmt.Sprintf("%s: %s", e.op, sndStrerror(e.code))
}

func (e *alsaError) Unwrap() error { return syscall.Errno(-e.code) }

func check(op string, r int32) error {
	if r < 0 {
		return &alsaError{op, r}
	}
	return nil
}

// openSeq opens the client, once, and starts its queue and the goroutine
// that reads what comes in.
func openSeq() error {
	seq.once.Do(func() { seq.err = startOnce() })
	return seq.err
}

func startOnce() error {
	if err := loadALSA(); err != nil {
		return err
	}
	if err := check("snd_seq_open", sndSeqOpen(&seq.h, "default", openDuplex, nonBlock)); err != nil {
		return err
	}
	name := filepath.Base(os.Args[0])
	if name == "" || name == "." {
		name = "gunim"
	}
	_ = sndSeqSetClientName(seq.h, name)
	// The kernel's most: room to send ahead, and to hear a burst.
	_ = sndSeqSetClientPoolOutput(seq.h, 2000)
	_ = sndSeqSetClientPoolInput(seq.h, 2000)
	seq.client = uint8(sndSeqClientID(seq.h))
	p := sndSeqCreateSimplePort(seq.h, name,
		capRead|capWrite|capNoExport, typeMIDIGeneric|typeApplication)
	if err := check("snd_seq_create_simple_port", p); err != nil {
		return err
	}
	seq.port = uint8(p)
	q := sndSeqAllocNamedQueue(seq.h, name)
	if err := check("snd_seq_alloc_named_queue", q); err != nil {
		return err
	}
	seq.queue = q
	if err := check("snd_seq_remove_events_malloc", sndSeqRemoveEventsMalloc(&seq.rm)); err != nil {
		return err
	}
	sndSeqRemoveEventsSetCondition(seq.rm, removeOutput|removeDest|removeTag)
	sndSeqRemoveEventsSetQueue(seq.rm, q)
	if err := check("snd_seq_queue_status_malloc", sndSeqQueueStatusMalloc(&seq.status)); err != nil {
		return err
	}
	// The System client's Announce port tells of ports as they come
	// and go.
	if err := check("snd_seq_connect_from", sndSeqConnectFrom(seq.h, int32(seq.port), 0, 1)); err != nil {
		return err
	}
	var pfd unix.PollFd
	if n := sndSeqPollDescriptors(seq.h, unsafe.Pointer(&pfd), 1, unix.POLLIN); n != 1 {
		return errors.New("midi: the sequencer has no descriptor to poll")
	}
	seq.fd = pfd.Fd
	seq.sources = make(map[portAddr]*source)
	go read()
	return nil
}

// startQueue starts the queue, once, as the first port opens: it ticks
// a thousand times a second from then on.
func startQueue() error {
	if err := openSeq(); err != nil {
		return err
	}
	seq.queueOnce.Do(func() {
		seq.mu.Lock()
		err := check("snd_seq_control_queue", sndSeqControlQueue(seq.h, seq.queue, queueStart, 0, 0))
		if err == nil {
			err = check("snd_seq_drain_output", sndSeqDrainOutput(seq.h))
		}
		seq.base = time.Now()
		seq.mu.Unlock()
		if seq.queueErr = err; err == nil {
			resync()
			seq.running.Store(true)
		}
	})
	return seq.queueErr
}

// resync measures how far the queue's time is from the clock's.
func resync() {
	seq.mu.Lock()
	defer seq.mu.Unlock()
	before := time.Now()
	if sndSeqGetQueueStatus(seq.h, seq.queue, seq.status) < 0 {
		return
	}
	after := time.Now()
	rt := (*[2]uint32)(sndSeqQueueStatusGetRealTime(seq.status))
	queued := int64(rt[0])*1e9 + int64(rt[1])
	clock := int64(before.Sub(seq.base) + after.Sub(before)/2)
	seq.skew.Store(clock - queued)
}

func sysWatch() { _ = openSeq() }

func sysList(out bool) ([]sysPort, error) {
	if err := openSeq(); err != nil {
		return nil, err
	}
	seq.mu.Lock()
	defer seq.mu.Unlock()
	var ci, pi uintptr
	if err := check("snd_seq_client_info_malloc", sndSeqClientInfoMalloc(&ci)); err != nil {
		return nil, err
	}
	defer purego.SyscallN(libcFree(), ci)
	if err := check("snd_seq_port_info_malloc", sndSeqPortInfoMalloc(&pi)); err != nil {
		return nil, err
	}
	defer purego.SyscallN(libcFree(), pi)
	want := uint32(capRead | capSubsRead)
	if out {
		want = capWrite | capSubsWrite
	}
	var ps []sysPort
	sndSeqClientInfoSetClient(ci, -1)
	for sndSeqQueryNextClient(seq.h, ci) >= 0 {
		c := sndSeqClientInfoGetClient(ci)
		if c == 0 || c == int32(seq.client) {
			continue
		}
		cname := sndSeqClientInfoGetName(ci)
		sndSeqPortInfoSetClient(pi, c)
		sndSeqPortInfoSetPort(pi, -1)
		for sndSeqQueryNextPort(seq.h, pi) >= 0 {
			caps := sndSeqPortInfoGetCapability(pi)
			if caps&want != want || caps&capNoExport != 0 {
				continue
			}
			ps = append(ps, sysPort{
				name: portName(cname, sndSeqPortInfoGetName(pi)),
				addr: portAddr{uint8(c), uint8(sndSeqPortInfoGetPort(pi))},
			})
		}
	}
	return ps, nil
}

// portName names a port as a person knows it: by the port's name where
// that starts with its client's, as a USB keyboard's does, and by both
// otherwise.
func portName(client, port string) string {
	if len(port) >= len(client) && port[:len(client)] == client {
		return port
	}
	return client + ": " + port
}

var libcFree = sync.OnceValue(func() uintptr {
	for _, name := range []string{"libc.so.6", "libc.so"} {
		if lib, err := purego.Dlopen(name, purego.RTLD_LAZY|purego.RTLD_GLOBAL); err == nil {
			if f, err := purego.Dlsym(lib, "free"); err == nil {
				return f
			}
		}
	}
	panic("midi: libc has no free")
})

// outSys is an Out's part in the sequencer: the tag its events carry,
// so Cancel lets go of its own alone, where two Outs send to one port.
type outSys struct {
	tag uint8
}

// tags gives each Out a tag; a tag comes round again after 128 Outs.
var tags atomic.Uint32

func (s *outSys) open(*Out) error {
	s.tag = uint8(tags.Add(1) % 128)
	return startQueue()
}

// queueTime is at as the queue's time, in nanoseconds; zero for a time
// past.
func queueTime(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return max(int64(at.Sub(seq.base))-seq.skew.Load(), 0)
}

func (s *outSys) send(o *Out, at time.Time, m Message) error {
	t := queueTime(at)
	seq.mu.Lock()
	defer seq.mu.Unlock()
	ev := &seq.ev
	*ev = seqEvent{
		flags: timeStampReal, tag: s.tag, queue: uint8(seq.queue),
		sec: uint32(t / 1e9), nsec: uint32(t % 1e9),
		srcClient: seq.client, srcPort: seq.port,
		dstClient: o.addr.client, dstPort: o.addr.port,
	}
	if m.Status == 0xF0 {
		return sendSysex(ev, m.Sysex)
	}
	encode(ev, m)
	return output(ev)
}

// systemTypes are the event types of the system messages, by the low
// four bits of their status bytes.
var systemTypes = [16]uint8{
	0x1: evQFrame, 0x2: evSongPos, 0x3: evSongSel, 0x6: evTuneRequest,
	0x8: evClock, 0xA: evStart, 0xB: evContinue, 0xC: evStop,
	0xE: evSensing, 0xF: evReset,
}

// encode fills ev's type and data from m, a channel or system message.
func encode(ev *seqEvent, m Message) {
	d := &ev.data
	note := func(t uint8) {
		ev.typ = t
		d[0], d[1], d[2] = m.Status&0x0F, m.Data1, m.Data2
	}
	ctrl := func(t uint8, param uint32, value int32) {
		ev.typ = t
		d[0] = m.Status & 0x0F
		binary.NativeEndian.PutUint32(d[4:], param)
		binary.NativeEndian.PutUint32(d[8:], uint32(value))
	}
	switch m.Status & 0xF0 {
	case 0x80:
		note(evNoteOff)
	case 0x90:
		note(evNoteOn)
	case 0xA0:
		note(evKeyPress)
	case 0xB0:
		ctrl(evController, uint32(m.Data1), int32(m.Data2))
	case 0xC0:
		ctrl(evPgmChange, 0, int32(m.Data1))
	case 0xD0:
		ctrl(evChanPress, 0, int32(m.Data1))
	case 0xE0:
		ctrl(evPitchBend, 0, int32(m.Bend()))
	default:
		ev.typ = systemTypes[m.Status&0x0F]
		switch m.Status {
		case 0xF1, 0xF3:
			ctrl(ev.typ, 0, int32(m.Data1))
			d[0] = 0
		case 0xF2:
			ctrl(ev.typ, 0, int32(m.Beats()))
			d[0] = 0
		}
	}
}

func sendSysex(ev *seqEvent, data []byte) error {
	b := append(append(append(seq.sysex[:0], 0xF0), data...), 0xF7)
	seq.sysex = b
	ev.typ = evSysex
	ev.flags |= lengthVariable
	for len(b) > 0 {
		n := min(len(b), sysexChunk)
		binary.NativeEndian.PutUint32(ev.data[0:], uint32(n))
		if unsafe.Sizeof(uintptr(0)) == 8 {
			binary.NativeEndian.PutUint64(ev.data[4:], uint64(uintptr(unsafe.Pointer(&b[0]))))
		} else {
			binary.NativeEndian.PutUint32(ev.data[4:], uint32(uintptr(unsafe.Pointer(&b[0]))))
		}
		err := output(ev)
		runtime.KeepAlive(b)
		if err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}

// output sends ev, held by seq.mu.
func output(ev *seqEvent) error {
	seq.args[0], seq.args[1] = seq.h, uintptr(unsafe.Pointer(ev))
	r, _, _ := purego.SyscallN(fnOutputDirect, seq.args[:2]...)
	switch code := int32(r); {
	case code >= 0:
		return nil
	case code == -int32(unix.EAGAIN):
		return ErrFull
	default:
		return &alsaError{"snd_seq_event_output_direct", code}
	}
}

func (s *outSys) cancel(o *Out) error {
	seq.mu.Lock()
	defer seq.mu.Unlock()
	seq.dest = o.addr
	seq.args[0], seq.args[1] = seq.rm, uintptr(unsafe.Pointer(&seq.dest))
	purego.SyscallN(fnRemoveDest, seq.args[:2]...)
	seq.args[0], seq.args[1] = seq.rm, uintptr(s.tag)
	purego.SyscallN(fnRemoveTag, seq.args[:2]...)
	seq.args[0], seq.args[1] = seq.h, seq.rm
	r, _, _ := purego.SyscallN(fnRemoveEvents, seq.args[:2]...)
	return check("snd_seq_remove_events", int32(r))
}

func (s *outSys) close(o *Out) error { return s.cancel(o) }

func (s *outSys) gone(o *Out) { _ = s.cancel(o) }

// inSys is an In's part in the sequencer: nothing beyond its source's.
type inSys struct{}

func (inSys) open(i *In) error {
	if err := startQueue(); err != nil {
		return err
	}
	seq.route.Lock()
	defer seq.route.Unlock()
	if s := seq.sources[i.addr]; s != nil {
		s.ins = append(s.ins, i)
		return nil
	}
	s := &source{ins: []*In{i}}
	if i.addr != (portAddr{seq.client, seq.port}) {
		if err := subscribe(s, i.addr); err != nil {
			return err
		}
	}
	seq.sources[i.addr] = s
	return nil
}

// subscribe connects a to the client's port, each event stamped with
// the queue's time as it arrives.
func subscribe(s *source, a portAddr) error {
	seq.mu.Lock()
	defer seq.mu.Unlock()
	if err := check("snd_seq_port_subscribe_malloc", sndSeqPortSubscribeMalloc(&s.sub)); err != nil {
		return err
	}
	dest := portAddr{seq.client, seq.port}
	sndSeqPortSubscribeSetSender(s.sub, &a)
	sndSeqPortSubscribeSetDest(s.sub, &dest)
	sndSeqPortSubscribeSetQueue(s.sub, seq.queue)
	sndSeqPortSubscribeSetTimeUpdate(s.sub, 1)
	sndSeqPortSubscribeSetTimeReal(s.sub, 1)
	if err := check("snd_seq_subscribe_port", sndSeqSubscribePort(seq.h, s.sub)); err != nil {
		purego.SyscallN(libcFree(), s.sub)
		s.sub = 0
		return err
	}
	return nil
}

func (inSys) close(i *In) error {
	seq.route.Lock()
	defer seq.route.Unlock()
	s := seq.sources[i.addr]
	if s == nil {
		return nil
	}
	for k, in := range s.ins {
		if in == i {
			s.ins = append(s.ins[:k], s.ins[k+1:]...)
			break
		}
	}
	if len(s.ins) > 0 {
		return nil
	}
	delete(seq.sources, i.addr)
	if s.sub == 0 {
		return nil
	}
	seq.mu.Lock()
	defer seq.mu.Unlock()
	// A source gone is unsubscribed already, and says so.
	_ = sndSeqUnsubscribePort(seq.h, s.sub)
	purego.SyscallN(libcFree(), s.sub)
	return nil
}

func (inSys) gone(i *In) { _ = inSys{}.close(i) }

// read reads what comes in, for as long as the program runs.
func read() {
	readArgs := &seq.readArgs
	readArgs[0], readArgs[1] = seq.h, uintptr(unsafe.Pointer(&seq.evp))
	fds := []unix.PollFd{{Fd: seq.fd, Events: unix.POLLIN}}
	last := time.Now()
	for {
		if _, err := unix.Poll(fds, 1000); err != nil && !errors.Is(err, unix.EINTR) {
			return
		}
		for {
			r, _, _ := purego.SyscallN(fnEventInput, readArgs[:]...)
			code := int32(r)
			if code == -int32(unix.ENOSPC) {
				// Events were lost as the pool overflowed; read on.
				continue
			}
			if code < 0 {
				break
			}
			received((*seqEvent)(seq.evp))
		}
		if time.Since(last) >= time.Second && seq.running.Load() {
			resync()
			last = time.Now()
		}
	}
}

// received hands an event to the Ins listening to its source, or takes
// note of a port come or gone.
func received(ev *seqEvent) {
	switch ev.typ {
	case evClientStart, evClientExit, evClientChange, evPortStart, evPortExit, evPortChange:
		a := portAddr{ev.data[0], ev.data[1]}
		if a.client == seq.client {
			return
		}
		switch ev.typ {
		case evPortExit:
			goneWhere(func(b portAddr, _ bool) bool { return b == a })
		case evClientExit:
			goneWhere(func(b portAddr, _ bool) bool { return b.client == a.client })
		}
		announce()
		return
	}
	t := time.Now()
	if ev.flags&timeStampReal != 0 && int32(ev.queue) == seq.queue {
		q := int64(ev.sec)*1e9 + int64(ev.nsec)
		t = seq.base.Add(time.Duration(q + seq.skew.Load()))
	}
	src := portAddr{ev.srcClient, ev.srcPort}
	seq.route.Lock()
	s := seq.sources[src]
	if s == nil {
		seq.route.Unlock()
		return
	}
	seq.scratch = append(seq.scratch[:0], s.ins...)
	ins := seq.scratch
	if ev.typ == evSysex && ev.flags&lengthMask == lengthVariable {
		n := binary.NativeEndian.Uint32(ev.data[0:])
		// The pointer sits unaligned in the event: copied out byte by
		// byte.
		var ptr unsafe.Pointer
		copy(unsafe.Slice((*byte)(unsafe.Pointer(&ptr)), unsafe.Sizeof(ptr)), ev.data[4:])
		data := unsafe.Slice((*byte)(ptr), n)
		s.buf = append(s.buf[:0], data...)
		seq.route.Unlock()
		s.p.Parse(s.buf, func(m Message) {
			for _, i := range ins {
				i.deliver(Event{Time: t, Message: m})
			}
		})
		return
	}
	seq.route.Unlock()
	decode(ev, func(m Message) {
		for _, i := range ins {
			i.deliver(Event{Time: t, Message: m})
		}
	})
}

// decode calls f with the messages ev carries: one, or a controller's
// two or four for a 14-bit controller or a parameter number.
func decode(ev *seqEvent, f func(Message)) {
	d := &ev.data
	ch := d[0] & 0x0F
	param := binary.NativeEndian.Uint32(d[4:])
	value := int32(binary.NativeEndian.Uint32(d[8:]))
	short := func(status byte, a, b int) {
		f(Message{Status: status, Data1: data(a), Data2: data(b)})
	}
	switch ev.typ {
	case evNoteOn:
		short(0x90|ch, int(d[1]), int(d[2]))
	case evNoteOff:
		short(0x80|ch, int(d[1]), int(d[2]))
	case evKeyPress:
		short(0xA0|ch, int(d[1]), int(d[2]))
	case evController:
		short(0xB0|ch, int(param), int(value))
	case evPgmChange:
		short(0xC0|ch, int(value), 0)
	case evChanPress:
		short(0xD0|ch, int(value), 0)
	case evPitchBend:
		f(Bend(int(ch), int(value)))
	case evControl14:
		if param < 32 {
			short(0xB0|ch, int(param), int(value>>7))
			short(0xB0|ch, int(param)+32, int(value&0x7F))
		} else {
			short(0xB0|ch, int(param), int(value))
		}
	case evNonRegParam, evRegParam:
		msb, lsb := 99, 98
		if ev.typ == evRegParam {
			msb, lsb = 101, 100
		}
		short(0xB0|ch, msb, int(param>>7))
		short(0xB0|ch, lsb, int(param&0x7F))
		short(0xB0|ch, 6, int(value>>7))
		short(0xB0|ch, 38, int(value&0x7F))
	case evSongPos:
		f(SongPosition(int(value)))
	case evSongSel:
		short(0xF3, int(value), 0)
	case evQFrame:
		short(0xF1, int(value), 0)
	case evTuneRequest:
		f(Message{Status: 0xF6})
	case evClock:
		f(Clock())
	case evStart:
		f(Start())
	case evContinue:
		f(Continue())
	case evStop:
		f(Stop())
	case evSensing:
		f(Message{Status: 0xFE})
	case evReset:
		f(Message{Status: 0xFF})
	}
}
