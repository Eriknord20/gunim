//go:build linux && (arm64 || amd64)

package kms

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The kernel's input event codes (include/uapi/linux/input-event-codes.h).
const (
	evSyn = 0x00
	evKey = 0x01
	evAbs = 0x03

	synReport  = 0
	synDropped = 3

	absMTSlot       = 0x2f
	absMTPositionX  = 0x35
	absMTPositionY  = 0x36
	absMTTrackingID = 0x39

	inputPropDirect = 0x01

	keyLeftCtrl  = 29
	keyLeftAlt   = 56
	keyRightCtrl = 97
	keyRightAlt  = 100
	keyF1        = 59
	keyF10       = 68
	keyF11       = 87
	keyF12       = 88
	keyA         = 30
)

// absInfo is input_absinfo: the range of an absolute axis.
type absInfo struct {
	value, minimum, maximum, fuzz, flat, resolution int32
}

func eviocgname(n uintptr) uintptr    { return ioc(iocRead, 'E', 0x06, n) }
func eviocgprop(n uintptr) uintptr    { return ioc(iocRead, 'E', 0x09, n) }
func eviocgbit(ev, n uintptr) uintptr { return ioc(iocRead, 'E', 0x20+ev, n) }
func eviocgabs(abs uintptr) uintptr   { return ior('E', 0x40+abs, unsafe.Sizeof(absInfo{})) }
func eviocgmtslots(n uintptr) uintptr { return ioc(iocRead, 'E', 0x0a, n) }

var (
	eviocgrab     = iow('E', 0x90, 4)
	eviocsclockid = iow('E', 0xa0, 4)
)

// event is one input_event.
type event struct {
	sec, usec int64
	typ, code uint16
	value     int32
}

// liveEventSize is input_event's size on this machine: a timeval of two
// longs, then 8 bytes.
const liveEventSize = 2*int(unsafe.Sizeof(uintptr(0))) + 8

// decodeEvent reads one input_event of size bytes, 16 or 24.
func decodeEvent(b []byte, size int) event {
	var e event
	o := 8
	if size == 24 {
		e.sec, e.usec = int64(binary.NativeEndian.Uint64(b)), int64(binary.NativeEndian.Uint64(b[8:]))
		o = 16
	} else {
		e.sec, e.usec = int64(int32(binary.NativeEndian.Uint32(b))), int64(int32(binary.NativeEndian.Uint32(b[4:])))
	}
	e.typ = binary.NativeEndian.Uint16(b[o:])
	e.code = binary.NativeEndian.Uint16(b[o+2:])
	e.value = int32(binary.NativeEndian.Uint32(b[o+4:]))
	return e
}

// encodeEvent writes e as a 24-byte input_event, as a recording keeps it.
func encodeEvent(b []byte, e event) {
	binary.NativeEndian.PutUint64(b, uint64(e.sec))
	binary.NativeEndian.PutUint64(b[8:], uint64(e.usec))
	binary.NativeEndian.PutUint16(b[16:], e.typ)
	binary.NativeEndian.PutUint16(b[18:], e.code)
	binary.NativeEndian.PutUint32(b[20:], uint32(e.value))
}

// inputDev is an open evdev device.
type inputDev struct {
	fd   int
	path string
	name string
	// touch says it is a touch screen: direct, with multi-touch slots.
	// keys says it has letter keys, as a keyboard has.
	touch, keys bool
	// x and y are the range of the touch positions, and slots how many
	// fingers it tracks.
	x, y  absInfo
	slots int
	// realtime says its events are stamped on the wall clock, where the
	// monotonic one could not be set: their stamps then mean nothing to
	// flips, and the driver stamps them as it reads them.
	realtime bool
}

func bitSet(b []byte, n int) bool { return n/8 < len(b) && b[n/8]&(1<<(n%8)) != 0 }

// openInput opens an evdev device and reads what it is.
func openInput(path string) (*inputDev, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	d := &inputDev{fd: fd, path: path}
	name := make([]byte, 256)
	if ioctl(fd, eviocgname(uintptr(len(name))), unsafe.Pointer(&name[0])) == nil {
		d.name = cstr(name)
	}
	// A query that fails leaves its bits clear, so the device counts as
	// neither a touch screen nor a keyboard.
	props := make([]byte, 8)
	_ = ioctl(fd, eviocgprop(uintptr(len(props))), unsafe.Pointer(&props[0]))
	abs := make([]byte, 8)
	_ = ioctl(fd, eviocgbit(evAbs, uintptr(len(abs))), unsafe.Pointer(&abs[0]))
	keys := make([]byte, 96)
	_ = ioctl(fd, eviocgbit(evKey, uintptr(len(keys))), unsafe.Pointer(&keys[0]))
	d.touch = bitSet(props, inputPropDirect) && bitSet(abs, absMTSlot) && bitSet(abs, absMTPositionX)
	d.keys = bitSet(keys, keyA) && bitSet(keys, keyF1)
	if d.touch {
		var slot absInfo
		errX := ioctl(fd, eviocgabs(absMTPositionX), unsafe.Pointer(&d.x))
		errY := ioctl(fd, eviocgabs(absMTPositionY), unsafe.Pointer(&d.y))
		errS := ioctl(fd, eviocgabs(absMTSlot), unsafe.Pointer(&slot))
		// A touch screen whose ranges can't be read can't be mapped.
		d.touch = errX == nil && errY == nil && errS == nil
		d.slots = int(slot.maximum) + 1
	}
	return d, nil
}

func (d *inputDev) close() { _ = unix.Close(d.fd) }

// take takes a touch screen for the driver: the device to itself, so
// nothing else on the system hears its touches, and its events stamped on
// the monotonic clock, as flips are. Events queued before then go, as
// they carry the other clock. Another program holding the device is an
// error: the kernel would hand it every touch, and the driver none.
func (d *inputDev) take() error {
	if err := ioctlInt(d.fd, eviocgrab, 1); err != nil {
		return fmt.Errorf("%s: another program holds the touch screen: %w", d.path, err)
	}
	clock := int32(unix.CLOCK_MONOTONIC)
	if err := ioctl(d.fd, eviocsclockid, unsafe.Pointer(&clock)); err != nil {
		log.Printf("gunim: kms: %s: touch events are not stamped on the monotonic clock, so they are stamped as read: %v", d.path, err)
		d.realtime = true
	}
	drain(d.fd)
	return nil
}

// drain reads and drops what waits on fd, which does not block.
func drain(fd int) {
	buf := make([]byte, 64*liveEventSize)
	for {
		n, _ := unix.Read(fd, buf)
		if n <= 0 {
			return
		}
	}
}

// ErrNoTouchScreen is why Open fails with Options.RequireTouch set and no
// touch screen found.
var ErrNoTouchScreen = errors.New("kms: no touch screen found")

// findInputs opens the touch screen, where withTouch, and the keyboards,
// where withKeys. want names the touch screen by path or name, or is empty
// for the first one found; one named, or one required where none is named,
// that isn't found fails. It takes the touch screen it finds; see take.
func findInputs(want string, withTouch, require, withKeys bool) (touch *inputDev, keyboards []*inputDev, err error) {
	return findInputsIn("/dev/input/event*", want, withTouch, require, withKeys)
}

// findInputsIn is findInputs among the devices pattern matches.
func findInputsIn(pattern, want string, withTouch, require, withKeys bool) (touch *inputDev, keyboards []*inputDev, err error) {
	if !withTouch && !withKeys {
		return nil, nil, nil
	}
	paths, _ := filepath.Glob(pattern)
	// event2 before event10.
	slices.SortFunc(paths, func(a, b string) int {
		if len(a) != len(b) {
			return len(a) - len(b)
		}
		return strings.Compare(a, b)
	})
	var errs []error
	for _, p := range paths {
		d, err := openInput(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		switch {
		case withTouch && d.touch && touch == nil && (want == "" || want == p || strings.EqualFold(want, d.name)):
			touch = d
		case withKeys && d.keys:
			keyboards = append(keyboards, d)
		default:
			d.close()
		}
	}
	closeAll := func() {
		if touch != nil {
			touch.close()
		}
		for _, k := range keyboards {
			k.close()
		}
	}
	if withTouch && touch == nil && want != "" {
		closeAll()
		return nil, nil, fmt.Errorf("no touch screen %q: %w", want, errors.Join(errs...))
	}
	if withTouch && touch == nil && require {
		closeAll()
		return nil, nil, errors.Join(append([]error{ErrNoTouchScreen}, errs...)...)
	}
	if touch != nil {
		if err := touch.take(); err != nil {
			closeAll()
			return nil, nil, err
		}
	}
	return touch, keyboards, nil
}

// contact is a finger's state in one report.
type contact struct {
	slot  int
	id    int32
	x, y  int32
	phase phase
}

type phase uint8

const (
	phaseDown phase = iota
	phaseMove
	phaseUp
)

// mtSlot is one slot of the multi-touch protocol B.
type mtSlot struct {
	id   int32 // the finger's tracking id, while live
	x, y int32
	live bool
	// trans is what happened to the slot since the last report, in order:
	// lifts and presses, with no position yet. moved says it moved.
	trans []contact
	moved bool
}

// mtDecoder turns protocol B events into finger reports. It is fed one
// event at a time, and returns at each SYN_REPORT what changed.
type mtDecoder struct {
	slots   []mtSlot
	slot    int
	dropped bool
	// resync reads a device's slots again after SYN_DROPPED: for an
	// ABS_MT code, the value of each slot, and for ABS_MT_SLOT the slot
	// its events go to now, as one value. It is nil for a recording, which
	// then lifts every finger.
	resync func(code uint16) []int32
	out    []contact
}

func newMTDecoder(slots int) *mtDecoder {
	return &mtDecoder{slots: make([]mtSlot, max(slots, 1))}
}

// feed takes one event, and returns the contacts that changed at a
// SYN_REPORT, or nil. The slice is the decoder's until the next call.
func (m *mtDecoder) feed(e event) []contact {
	if m.dropped {
		// Everything up to the next report is lost; then the state is read
		// again.
		if e.typ == evSyn && e.code == synReport {
			m.dropped = false
			return m.resynced()
		}
		return nil
	}
	switch e.typ {
	case evSyn:
		switch e.code {
		case synReport:
			return m.report()
		case synDropped:
			m.dropped = true
		}
	case evAbs:
		if e.code == absMTSlot {
			m.slot = int(e.value)
			return nil
		}
		if m.slot < 0 || m.slot >= len(m.slots) {
			return nil
		}
		s := &m.slots[m.slot]
		switch e.code {
		case absMTTrackingID:
			m.track(s, e.value)
		case absMTPositionX:
			s.x, s.moved = e.value, true
		case absMTPositionY:
			s.y, s.moved = e.value, true
		}
	}
	return nil
}

// track sets a slot's tracking id: -1 lifts its finger, and a new id
// presses a new one, lifting the one before if its lift was lost.
func (m *mtDecoder) track(s *mtSlot, id int32) {
	if s.live && (id < 0 || id != s.id) {
		s.trans = append(s.trans, contact{id: s.id, phase: phaseUp})
		s.live = false
	}
	if id >= 0 && !s.live {
		s.id, s.live = id, true
		s.trans = append(s.trans, contact{id: id, phase: phaseDown})
	}
}

// report collects what changed since the last report, at the slots' last
// positions.
func (m *mtDecoder) report() []contact {
	m.out = m.out[:0]
	for i := range m.slots {
		s := &m.slots[i]
		for _, c := range s.trans {
			c.slot, c.x, c.y = i, s.x, s.y
			m.out = append(m.out, c)
		}
		if len(s.trans) == 0 && s.moved && s.live {
			m.out = append(m.out, contact{slot: i, id: s.id, x: s.x, y: s.y, phase: phaseMove})
		}
		s.trans, s.moved = s.trans[:0], false
	}
	if len(m.out) == 0 {
		return nil
	}
	return m.out
}

// resynced reads every slot again after events were lost, and reports a
// lift for each finger gone, a press for each new one, and a move for each
// still down.
func (m *mtDecoder) resynced() []contact {
	var ids, xs, ys []int32
	if m.resync != nil {
		ids, xs, ys = m.resync(absMTTrackingID), m.resync(absMTPositionX), m.resync(absMTPositionY)
		// The slot events go to now: the kernel sends no ABS_MT_SLOT
		// again for events lost.
		if cur := m.resync(absMTSlot); len(cur) == 1 {
			m.slot = int(cur[0])
		}
	}
	for i := range m.slots {
		s := &m.slots[i]
		s.trans, s.moved = s.trans[:0], false
		id := int32(-1)
		if i < len(ids) {
			id = ids[i]
		}
		if id >= 0 && i < len(xs) && i < len(ys) {
			s.x, s.y = xs[i], ys[i]
		}
		m.track(s, id)
		s.moved = id >= 0
	}
	return m.report()
}

// liftAll lifts every finger down, as for a device gone, and returns the
// lifts.
func (m *mtDecoder) liftAll() []contact {
	for i := range m.slots {
		m.track(&m.slots[i], -1)
	}
	return m.report()
}

// mtSlots reads one ABS_MT axis of every slot from the device, or for
// ABS_MT_SLOT the slot its events go to now.
func (d *inputDev) mtSlots(code uint16) []int32 {
	if code == absMTSlot {
		var a absInfo
		if err := ioctl(d.fd, eviocgabs(absMTSlot), unsafe.Pointer(&a)); err != nil {
			return nil
		}
		return []int32{a.value}
	}
	buf := make([]int32, 1+d.slots)
	buf[0] = int32(code)
	if err := ioctl(d.fd, eviocgmtslots(uintptr(4*len(buf))), unsafe.Pointer(&buf[0])); err != nil {
		return nil
	}
	return buf[1:]
}

// A recording is a touch screen's events, kept to play back through the
// driver's own input path: a header line with the screen's ranges, then
// 24-byte input_events as a 64-bit kernel writes them.
const recordingMagic = "gunim-evdev 1"

// recorder writes the events read from a touch screen to a file.
type recorder struct {
	w   *bufio.Writer
	f   *os.File
	buf [24]byte
}

func newRecorder(path string, d *inputDev) (*recorder, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	r := &recorder{f: f, w: bufio.NewWriter(f)}
	// bufio keeps the first write error, which close returns.
	_, _ = fmt.Fprintf(r.w, "%s %d %d %d %d %d\n", recordingMagic, d.x.minimum, d.x.maximum, d.y.minimum, d.y.maximum, d.slots)
	return r, nil
}

func (r *recorder) write(e event) {
	encodeEvent(r.buf[:], e)
	_, _ = r.w.Write(r.buf[:]) // kept by bufio for close
}

func (r *recorder) close() error {
	err := r.w.Flush()
	return errors.Join(err, r.f.Close())
}

// playback is a recording read back.
type playback struct {
	x, y   absInfo
	slots  int
	events []event
}

// readRecording reads a recording.
func readRecording(r io.Reader) (*playback, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("touch recording: %w", err)
	}
	p := &playback{}
	var magic, version string
	if _, err := fmt.Sscanf(line, "%s %s %d %d %d %d %d", &magic, &version, &p.x.minimum, &p.x.maximum, &p.y.minimum, &p.y.maximum, &p.slots); err != nil ||
		magic+" "+version != recordingMagic {
		return nil, fmt.Errorf("touch recording: not a %s file", recordingMagic)
	}
	var buf [24]byte
	for {
		if _, err := io.ReadFull(br, buf[:]); err != nil {
			if err == io.EOF {
				return p, nil
			}
			return nil, fmt.Errorf("touch recording: %w", err)
		}
		p.events = append(p.events, decodeEvent(buf[:], 24))
	}
}

// eventTime is when an event happened, from its timestamp.
func (e event) time() time.Duration {
	return time.Duration(e.sec)*time.Second + time.Duration(e.usec)*time.Microsecond
}
