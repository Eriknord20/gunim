//go:build windows

package midi

import (
	"fmt"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// portAddr is a port's place in WinMM's list, and its name there, by
// which the package tells it is gone.
type portAddr struct {
	id   uint32
	name string
}

var (
	winmm                     = windows.NewLazySystemDLL("winmm.dll")
	procMidiOutGetNumDevs     = winmm.NewProc("midiOutGetNumDevs")
	procMidiOutGetDevCapsW    = winmm.NewProc("midiOutGetDevCapsW")
	procMidiOutOpen           = winmm.NewProc("midiOutOpen")
	procMidiOutClose          = winmm.NewProc("midiOutClose")
	procMidiOutShortMsg       = winmm.NewProc("midiOutShortMsg")
	procMidiOutLongMsg        = winmm.NewProc("midiOutLongMsg")
	procMidiOutPrepareHeader  = winmm.NewProc("midiOutPrepareHeader")
	procMidiOutUnprepareHdr   = winmm.NewProc("midiOutUnprepareHeader")
	procMidiInGetNumDevs      = winmm.NewProc("midiInGetNumDevs")
	procMidiInGetDevCapsW     = winmm.NewProc("midiInGetDevCapsW")
	procMidiInOpen            = winmm.NewProc("midiInOpen")
	procMidiInClose           = winmm.NewProc("midiInClose")
	procMidiInStart           = winmm.NewProc("midiInStart")
	procMidiInReset           = winmm.NewProc("midiInReset")
	procMidiInAddBuffer       = winmm.NewProc("midiInAddBuffer")
	procMidiInPrepareHeader   = winmm.NewProc("midiInPrepareHeader")
	procMidiInUnprepareHeader = winmm.NewProc("midiInUnprepareHeader")

	kernel32                   = windows.NewLazySystemDLL("kernel32.dll")
	procCreateWaitableTimerExW = kernel32.NewProc("CreateWaitableTimerExW")
	procSetWaitableTimer       = kernel32.NewProc("SetWaitableTimer")
)

const (
	callbackFunction = 0x30000
	mimData          = 0x3C3
	mimLongData      = 0x3C4
	mhdrDone         = 1

	timerHighResolution = 0x2
	timerAllAccess      = 0x1F0003

	mmUnspecified = 1
	mmAllocated   = 4
	mmInvalHandle = 5
	mmNoDriver    = 6
	midiNoDevice  = 68

	// inBuffers sysex buffers of inBufferSize bytes each wait for sysex
	// coming in.
	inBuffers    = 4
	inBufferSize = 4096
)

// mmError is an error a WinMM function returned.
type mmError struct {
	op   string
	code uintptr
}

func (e *mmError) Error() string {
	switch e.code {
	case mmUnspecified:
		return e.op + ": the system failed to open it"
	case mmAllocated:
		return e.op + ": another program has the port open"
	case midiNoDevice, mmNoDriver:
		return e.op + ": the device is gone"
	}
	return fmt.Sprintf("%s: error %d", e.op, e.code)
}

func (e *mmError) gone() bool {
	return e.code == midiNoDevice || e.code == mmNoDriver || e.code == mmInvalHandle
}

func mm(op string, r uintptr) error {
	if r != 0 {
		return &mmError{op, r}
	}
	return nil
}

// midiOutCaps is MIDIOUTCAPSW, and midiInCaps MIDIINCAPSW.
type midiOutCaps struct {
	mid, pid      uint16
	driverVersion uint32
	name          [32]uint16
	technology    uint16
	voices        uint16
	notes         uint16
	channelMask   uint16
	support       uint32
}

type midiInCaps struct {
	mid, pid      uint16
	driverVersion uint32
	name          [32]uint16
	support       uint32
}

// midiHdr is MIDIHDR.
type midiHdr struct {
	data          *byte
	bufferLength  uint32
	bytesRecorded uint32
	user          uintptr
	flags         uint32
	next          uintptr
	reserved      uintptr
	offset        uint32
	reserved2     [8]uintptr
}

func sysList(out bool) ([]sysPort, error) {
	if err := winmm.Load(); err != nil {
		return nil, err
	}
	var ps []sysPort
	if out {
		n, _, _ := procMidiOutGetNumDevs.Call()
		for id := range uint32(n) {
			var c midiOutCaps
			if r, _, _ := procMidiOutGetDevCapsW.Call(uintptr(id), uintptr(unsafe.Pointer(&c)), unsafe.Sizeof(c)); r != 0 {
				continue
			}
			name := windows.UTF16ToString(c.name[:])
			ps = append(ps, sysPort{name, portAddr{id, name}})
		}
		return ps, nil
	}
	n, _, _ := procMidiInGetNumDevs.Call()
	for id := range uint32(n) {
		var c midiInCaps
		if r, _, _ := procMidiInGetDevCapsW.Call(uintptr(id), uintptr(unsafe.Pointer(&c)), unsafe.Sizeof(c)); r != 0 {
			continue
		}
		name := windows.UTF16ToString(c.name[:])
		ps = append(ps, sysPort{name, portAddr{id, name}})
	}
	return ps, nil
}

var watchOnce sync.Once

// sysWatch looks at the ports once a second, as WinMM tells of none
// coming or going.
func sysWatch() {
	watchOnce.Do(func() {
		go func() {
			list := func() (in, out []string) {
				for _, o := range []bool{false, true} {
					ps, _ := sysList(o)
					for _, p := range ps {
						if o {
							out = append(out, p.name)
						} else {
							in = append(in, p.name)
						}
					}
				}
				return in, out
			}
			in, out := list()
			for range time.Tick(time.Second) {
				in2, out2 := list()
				if slices.Equal(in, in2) && slices.Equal(out, out2) {
					continue
				}
				in, out = in2, out2
				goneWhere(func(a portAddr, isOut bool) bool {
					if isOut {
						return !slices.Contains(out, a.name)
					}
					return !slices.Contains(in, a.name)
				})
				announce()
			}
		}()
	})
}

// outSys is an Out's WinMM device, and the thread that sends what was
// sent ahead at its time.
type outSys struct {
	h      uintptr // HMIDIOUT
	sched  schedule
	wake   windows.Handle
	timer  windows.Handle
	period bool // timeBeginPeriod(1) holds, as the timer is coarse
	quit   atomic.Bool
	done   chan struct{}
	shut   sync.Once

	// write sends a message at once: writeDevice, or a test's own.
	write func(Message) error

	mu    sync.Mutex // held over each message out
	hdr   midiHdr
	sysex []byte
	args  [2]uintptr // midiOutShortMsg's, made once
}

func (s *outSys) open(o *Out) error {
	if err := mm("midiOutOpen", call(procMidiOutOpen, uintptr(unsafe.Pointer(&s.h)), uintptr(o.addr.id), 0, 0, 0)); err != nil {
		return err
	}
	if err := s.start(o, s.writeDevice); err != nil {
		call(procMidiOutClose, s.h)
		return err
	}
	return nil
}

// start starts the thread that sends what was sent ahead, through
// write.
func (s *outSys) start(o *Out, write func(Message) error) error {
	s.write = write
	var err error
	if s.wake, err = windows.CreateEvent(nil, 0, 0, nil); err != nil {
		return err
	}
	t, _, e := procCreateWaitableTimerExW.Call(0, 0, timerHighResolution, timerAllAccess)
	if t == 0 {
		// Before Windows 10 1803: a timer of the system's period, set
		// to a millisecond.
		t, _, e = procCreateWaitableTimerExW.Call(0, 0, 0, timerAllAccess)
		s.period = t != 0 && windows.TimeBeginPeriod(1) == nil
	}
	if t == 0 {
		_ = windows.CloseHandle(s.wake)
		return fmt.Errorf("CreateWaitableTimerExW: %w", e)
	}
	s.timer = windows.Handle(t)
	s.done = make(chan struct{})
	go s.run(o)
	return nil
}

func call(p *windows.LazyProc, args ...uintptr) uintptr {
	r, _, _ := p.Call(args...)
	return r
}

// run sends each message sent ahead at its time.
func (s *outSys) run(o *Out) {
	defer close(s.done)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	handles := []windows.Handle{s.wake, s.timer}
	for !s.quit.Load() {
		for {
			m, ok := s.sched.due(sinceEpoch(time.Now()))
			if !ok {
				break
			}
			if err := s.write(m); err != nil {
				if e, ok := err.(*mmError); ok && e.gone() { //nolint:errorlint // write returns it bare
					go o.gone()
					return
				}
			}
		}
		if at, ok := s.sched.next(); ok {
			d := at - sinceEpoch(time.Now())
			if d <= 0 {
				continue
			}
			due := -max(d/100, 1) // relative, in 100 ns
			_, _, _ = procSetWaitableTimer.Call(uintptr(s.timer), uintptr(unsafe.Pointer(&due)), 0, 0, 0, 0)
		}
		_, _ = windows.WaitForMultipleObjects(handles, false, windows.INFINITE)
	}
}

func (s *outSys) send(o *Out, at time.Time, m Message) error {
	now, first, err := s.sched.add(sinceEpoch(at), sinceEpoch(time.Now()), m)
	switch {
	case err != nil:
		return err
	case first:
		_ = windows.SetEvent(s.wake)
	case now:
		err = s.write(m)
		if e, ok := err.(*mmError); ok && e.gone() { //nolint:errorlint // write returns it bare
			o.gone()
			return ErrGone
		}
		return err
	}
	return nil
}

// writeDevice sends m to the device at once: a short message whole,
// and a sysex before it returns.
func (s *outSys) writeDevice(m Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.Status != 0xF0 {
		// The arguments are held in s, as SyscallN's own would be made
		// anew each call.
		s.args[0] = s.h
		s.args[1] = uintptr(m.Status) | uintptr(m.Data1&0x7F)<<8 | uintptr(m.Data2&0x7F)<<16
		r, _, _ := syscall.SyscallN(procMidiOutShortMsg.Addr(), s.args[:]...)
		if r != 0 {
			return &mmError{"midiOutShortMsg", r}
		}
		return nil
	}
	s.sysex = m.AppendTo(s.sysex[:0])
	s.hdr = midiHdr{data: &s.sysex[0], bufferLength: uint32(len(s.sysex))}
	hdr, size := uintptr(unsafe.Pointer(&s.hdr)), unsafe.Sizeof(s.hdr)
	if err := mm("midiOutPrepareHeader", call(procMidiOutPrepareHeader, s.h, hdr, size)); err != nil {
		return err
	}
	err := mm("midiOutLongMsg", call(procMidiOutLongMsg, s.h, hdr, size))
	if err == nil {
		for atomic.LoadUint32(&s.hdr.flags)&mhdrDone == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	call(procMidiOutUnprepareHdr, s.h, hdr, size)
	return err
}

func (s *outSys) cancel(*Out) error {
	s.sched.clear()
	return nil
}

func (s *outSys) close(*Out) error {
	var err error
	s.shut.Do(func() {
		s.quit.Store(true)
		_ = windows.SetEvent(s.wake)
		<-s.done
		s.sched.clear()
		if s.h != 0 {
			err = mm("midiOutClose", call(procMidiOutClose, s.h))
		}
		_ = windows.CloseHandle(s.timer)
		_ = windows.CloseHandle(s.wake)
		if s.period {
			_ = windows.TimeEndPeriod(1)
		}
	})
	return err
}

func (s *outSys) gone(o *Out) { _ = s.close(o) }

// inSys is an In's WinMM device, and the goroutine that hands on what
// it hears.
type inSys struct {
	h      uintptr // HMIDIIN
	key    uintptr // its key in ins
	start  time.Time
	events chan inEvent // short messages; a burst past its room is lost
	// longs carries the sysex buffers WinMM hands back, with room for
	// every one, so each comes back to be given to WinMM again.
	longs chan inEvent
	stop  chan struct{}
	hdrs  [inBuffers]midiHdr
	bufs  [inBuffers][inBufferSize]byte
	p     Parser
	quit  atomic.Bool
	done  chan struct{}
	shut  sync.Once
}

// An inEvent is what WinMM's callback was told.
type inEvent struct {
	msg, p1, p2 uintptr
}

// ins are the open Ins, by the key WinMM's callback is given.
var (
	ins     sync.Map
	insNext atomic.Uintptr
)

// inCallback is the MidiInProc of every In. It hands what it is told
// to the In's goroutine, as WinMM asks a callback to call none of its
// functions.
var inCallback = sync.OnceValue(func() uintptr {
	return syscall.NewCallback(func(_, msg, key, p1, p2 uintptr) uintptr {
		if msg != mimData && msg != mimLongData {
			return 0
		}
		if v, ok := ins.Load(key); ok {
			if i, ok := v.(*In); ok {
				ch := i.sys.events
				if msg == mimLongData {
					ch = i.sys.longs
				}
				select {
				case ch <- inEvent{msg, p1, p2}:
				default:
				}
			}
		}
		return 0
	})
})

func (s *inSys) open(i *In) error {
	s.key = insNext.Add(1)
	s.events = make(chan inEvent, 1024)
	s.longs = make(chan inEvent, inBuffers)
	s.stop = make(chan struct{})
	ins.Store(s.key, i)
	if err := mm("midiInOpen", call(procMidiInOpen, uintptr(unsafe.Pointer(&s.h)), uintptr(i.addr.id),
		inCallback(), s.key, callbackFunction)); err != nil {
		ins.Delete(s.key)
		return err
	}
	size := unsafe.Sizeof(midiHdr{})
	for k := range s.hdrs {
		s.hdrs[k] = midiHdr{data: &s.bufs[k][0], bufferLength: inBufferSize}
		hdr := uintptr(unsafe.Pointer(&s.hdrs[k]))
		if err := mm("midiInPrepareHeader", call(procMidiInPrepareHeader, s.h, hdr, size)); err != nil {
			s.release()
			return err
		}
		if err := mm("midiInAddBuffer", call(procMidiInAddBuffer, s.h, hdr, size)); err != nil {
			s.release()
			return err
		}
	}
	s.done = make(chan struct{})
	// WinMM counts the stamps from midiInStart, a moment after this.
	s.start = time.Now()
	go s.run(i)
	if err := mm("midiInStart", call(procMidiInStart, s.h)); err != nil {
		s.quit.Store(true)
		close(s.stop)
		<-s.done
		s.release()
		return err
	}
	return nil
}

// run hands each message WinMM hears to f, and gives each sysex buffer
// back to WinMM once read.
func (s *inSys) run(i *In) {
	defer close(s.done)
	for {
		var e inEvent
		select {
		case e = <-s.events:
		case e = <-s.longs:
		case <-s.stop:
			return
		}
		if s.quit.Load() {
			return
		}
		t := s.start.Add(time.Duration(e.p2) * time.Millisecond)
		if now := time.Now(); t.After(now) {
			t = now
		}
		if e.msg == mimData {
			status := byte(e.p1)
			if n := dataLen(status); n >= 0 && status != 0xF0 && status != 0xF7 {
				m := Message{Status: status}
				if n > 0 {
					m.Data1 = byte(e.p1>>8) & 0x7F
				}
				if n > 1 {
					m.Data2 = byte(e.p1>>16) & 0x7F
				}
				i.deliver(Event{Time: t, Message: m})
			}
			continue
		}
		for k := range s.hdrs {
			hdr := &s.hdrs[k]
			if uintptr(unsafe.Pointer(hdr)) != e.p1 {
				continue
			}
			s.p.Parse(s.bufs[k][:hdr.bytesRecorded], func(m Message) {
				i.deliver(Event{Time: t, Message: m})
			})
			call(procMidiInAddBuffer, s.h, e.p1, unsafe.Sizeof(*hdr))
		}
	}
}

// release unprepares the buffers and closes the device.
func (s *inSys) release() {
	call(procMidiInReset, s.h)
	for k := range s.hdrs {
		call(procMidiInUnprepareHeader, s.h, uintptr(unsafe.Pointer(&s.hdrs[k])), unsafe.Sizeof(s.hdrs[k]))
	}
	call(procMidiInClose, s.h)
	ins.Delete(s.key)
}

func (s *inSys) close(*In) error {
	s.shut.Do(func() {
		s.quit.Store(true)
		// Reset hands every buffer back, and run gives none to WinMM
		// again as it stops.
		call(procMidiInReset, s.h)
		close(s.stop)
		<-s.done
		s.release()
	})
	return nil
}

func (s *inSys) gone(i *In) { _ = s.close(i) }
