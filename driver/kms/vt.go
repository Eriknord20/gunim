//go:build linux && (arm64 || amd64)

package kms

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The virtual terminal's ioctls, from linux/vt.h and linux/kd.h.
const (
	vtOpenQry     = 0x5600
	vtSetMode     = 0x5602
	vtGetState    = 0x5603
	vtRelDisp     = 0x5605
	vtActivate    = 0x5606
	vtDisallocate = 0x5608

	vtAuto    = 0
	vtProcess = 1
	vtAckAcq  = 2

	kdSetMode  = 0x4B3A
	kdGetMode  = 0x4B3B
	kdText     = 0x00
	kdGraphics = 0x01
	kdGKBMode  = 0x4B44
	kdSKBMode  = 0x4B45
	kUnicode   = 0x03
	kOff       = 0x04

	ttyMajor = 4
)

type vtMode struct {
	mode, waitv           int8
	relsig, acqsig, frsig int16
}

type vtStat struct {
	active, signal, state uint16
}

// console is the calls the VT code makes on the kernel's consoles. The
// real one makes them on /dev/ttyN, and a test stands in for it.
type console interface {
	// open opens /dev/ttyN, or /dev/tty0 for n 0, the VT showing.
	open(n int) (int, error)
	ioctl(fd int, req uintptr, arg unsafe.Pointer) error
	ioctlInt(fd int, req, arg uintptr) error
	close(fd int) error
}

type kernelConsole struct{}

func (kernelConsole) open(n int) (int, error) {
	return unix.Open("/dev/tty"+strconv.Itoa(n), unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOCTTY, 0)
}
func (kernelConsole) ioctl(fd int, req uintptr, arg unsafe.Pointer) error { return ioctl(fd, req, arg) }
func (kernelConsole) ioctlInt(fd int, req, arg uintptr) error             { return ioctlInt(fd, req, arg) }
func (kernelConsole) close(fd int) error                                  { return unix.Close(fd) }

// consoles is the console the VT code uses; a test swaps it.
var consoles console = kernelConsole{}

// vt is the virtual terminal the driver draws on. The text console draws
// on the same screen, so the driver puts its VT in graphics mode, where
// the console leaves the screen alone, and turns the VT's keyboard off,
// so keys don't reach a login behind it. It takes switching away and back
// in hand: the kernel asks before it switches, and the driver lets go of
// the screen first.
type vt struct {
	c   console
	fd  int
	num int
	// prev is the VT that showed before the driver switched to its own, or
	// 0 where it switched none. opened says it opened a free one, which it
	// gives back.
	prev   int
	opened bool
	// kbMode is the VT's keyboard mode to put back, once kbSaved.
	kbMode  int32
	kbSaved bool
	// signals carries the kernel's asks: SIGUSR1 to switch away, SIGUSR2
	// on coming back.
	signals chan os.Signal
}

// openVT takes the VT named by which: "" for the one the program runs on,
// if it runs on one, "free" for a free one, or a number or /dev/ttyN.
// It returns nil with no error where there is no VT to take, as for a
// program started over SSH with which empty.
//
// before runs once the VT to take and the VT showing are known, before the
// switch, so the state file holds them should the run die on the way.
func openVT(which string, before func(*vt) error) (*vt, error) {
	num := 0
	switch which {
	case "":
		if num = ownVT(); num == 0 {
			return nil, nil
		}
	case "free":
	default:
		n, err := strconv.Atoi(strings.TrimPrefix(which, "/dev/tty"))
		if err != nil || n < 1 || n > 63 {
			return nil, fmt.Errorf("VT %q: want free, a number from 1 to 63, or /dev/ttyN", which)
		}
		num = n
	}
	return takeVT(consoles, num, before)
}

// ownVT returns the VT the program's standard input is, or 0.
func ownVT() int {
	var st unix.Stat_t
	if unix.Fstat(0, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFCHR || unix.Major(st.Rdev) != ttyMajor {
		return 0
	}
	if n := int(unix.Minor(st.Rdev)); n < 64 {
		return n
	}
	return 0
}

// takeVT takes VT num, or a free one for 0, on c.
//
// A VT found in graphics mode, or with its keyboard off, was left so by a
// run that ended without putting it back: takeVT puts it back to text mode
// and a working keyboard first, and takes that as how it found it.
func takeVT(c console, num int, before func(*vt) error) (*vt, error) {
	t := &vt{c: c, fd: -1}
	tty0, err := c.open(0)
	if err != nil {
		return nil, fmt.Errorf("VT: %w", err)
	}
	defer func() { _ = c.close(tty0) }()
	var st vtStat
	if err := c.ioctl(tty0, vtGetState, unsafe.Pointer(&st)); err != nil {
		return nil, fmt.Errorf("VT: state: %w", err)
	}
	if num == 0 {
		var n int32
		if err := c.ioctl(tty0, vtOpenQry, unsafe.Pointer(&n)); err != nil || n < 1 {
			return nil, fmt.Errorf("VT: no free VT: %w", err)
		}
		num, t.opened = int(n), true
	}
	t.num = num
	t.fd, err = c.open(num)
	if err != nil {
		return nil, fmt.Errorf("VT %d: %w", num, err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = t.close() // the error that brought it here says more
		}
	}()
	if err := saneVT(c, t.fd, num); err != nil {
		return nil, err
	}
	if int(st.active) != num {
		t.prev = int(st.active)
	}
	if before != nil {
		if err := before(t); err != nil {
			return nil, err
		}
	}
	if t.prev > 0 {
		if err := c.ioctlInt(t.fd, vtActivate, uintptr(num)); err != nil {
			return nil, fmt.Errorf("VT %d: switch to it: %w", num, err)
		}
		if err := waitActive(c, num); err != nil {
			return nil, err
		}
	}
	if err := c.ioctl(t.fd, kdGKBMode, unsafe.Pointer(&t.kbMode)); err != nil {
		return nil, fmt.Errorf("VT %d: keyboard mode: %w", num, err)
	}
	t.kbSaved = true
	if err := c.ioctlInt(t.fd, kdSKBMode, kOff); err != nil {
		return nil, fmt.Errorf("VT %d: keyboard off: %w", num, err)
	}
	if err := c.ioctlInt(t.fd, kdSetMode, kdGraphics); err != nil {
		return nil, fmt.Errorf("VT %d: graphics mode: %w", num, err)
	}
	t.signals = make(chan os.Signal, 4)
	signal.Notify(t.signals, syscall.SIGUSR1, syscall.SIGUSR2)
	m := vtMode{mode: vtProcess, relsig: int16(syscall.SIGUSR1), acqsig: int16(syscall.SIGUSR2)}
	if err := c.ioctl(t.fd, vtSetMode, unsafe.Pointer(&m)); err != nil {
		return nil, fmt.Errorf("VT %d: take switching: %w", num, err)
	}
	ok = true
	return t, nil
}

// saneVT puts VT num, open as fd, back to text mode and a working
// keyboard where a run before left it in graphics mode or with its
// keyboard off, so neither is taken for how the VT was.
func saneVT(c console, fd, num int) error {
	var mode int32
	if err := c.ioctl(fd, kdGetMode, unsafe.Pointer(&mode)); err == nil && mode != kdText {
		log.Printf("gunim: kms: VT %d was left in graphics mode; putting it back in text mode first", num)
		if err := c.ioctlInt(fd, kdSetMode, kdText); err != nil {
			return fmt.Errorf("VT %d: text mode: %w", num, err)
		}
	}
	var kb int32
	if err := c.ioctl(fd, kdGKBMode, unsafe.Pointer(&kb)); err == nil && kb == kOff {
		log.Printf("gunim: kms: VT %d was left with its keyboard off; turning it on first", num)
		if err := c.ioctlInt(fd, kdSKBMode, kUnicode); err != nil {
			return fmt.Errorf("VT %d: keyboard on: %w", num, err)
		}
	}
	return nil
}

// activate switches to VT n, as Ctrl+Alt+Fn would.
func (t *vt) activate(n int) error {
	return t.c.ioctlInt(t.fd, vtActivate, uintptr(n))
}

// released tells the kernel the driver has let go of the screen, and the
// switch away can go ahead.
func (t *vt) released() error { return t.c.ioctlInt(t.fd, vtRelDisp, 1) }

// acquired tells the kernel the driver has the screen again.
func (t *vt) acquired() error { return t.c.ioctlInt(t.fd, vtRelDisp, vtAckAcq) }

// close puts the VT back as the driver found it, switches back to the VT
// that showed before, and gives back a VT it opened.
func (t *vt) close() error {
	if t == nil || t.fd < 0 {
		return nil
	}
	c := t.c
	var errs []error
	if t.signals != nil {
		signal.Stop(t.signals)
	}
	m := vtMode{mode: vtAuto}
	errs = append(errs, c.ioctl(t.fd, vtSetMode, unsafe.Pointer(&m)), c.ioctlInt(t.fd, kdSetMode, kdText))
	if t.kbSaved {
		errs = append(errs, c.ioctlInt(t.fd, kdSKBMode, uintptr(t.kbMode)))
	}
	if t.prev > 0 {
		errs = append(errs, c.ioctlInt(t.fd, vtActivate, uintptr(t.prev)), waitActive(c, t.prev))
	}
	errs = append(errs, c.close(t.fd))
	t.fd = -1
	if t.opened && t.prev > 0 {
		errs = append(errs, disallocate(c, t.num))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("VT %d: put back: %w", t.num, err)
	}
	return nil
}

// vtWait is how long a switch of VT may take before the driver gives up on
// it.
const vtWait = 2 * time.Second

// waitActive waits until VT n shows, for vtWait at most. It asks the VT's
// state in a loop, where VT_WAITACTIVE would block in the kernel with no
// limit, and retry for ever as signals broke it off.
func waitActive(c console, n int) error {
	tty0, err := c.open(0)
	if err != nil {
		return fmt.Errorf("VT: %w", err)
	}
	defer func() { _ = c.close(tty0) }()
	deadline := time.Now().Add(vtWait)
	for {
		var st vtStat
		if err := c.ioctl(tty0, vtGetState, unsafe.Pointer(&st)); err != nil {
			return fmt.Errorf("VT: state: %w", err)
		}
		if int(st.active) == n {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("VT %d did not show within %v", n, vtWait)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// disallocate gives VT n back to the kernel. It is busy where another
// program opened it meanwhile, which is fine: it is theirs then.
func disallocate(c console, n int) error {
	tty0, err := c.open(0)
	if err != nil {
		return err
	}
	defer func() { _ = c.close(tty0) }()
	if err := c.ioctlInt(tty0, vtDisallocate, uintptr(n)); err != nil && !errors.Is(err, unix.EBUSY) {
		return err
	}
	return nil
}

// repairVT puts back a VT a run before took and did not give back, as its
// state file says: text mode and a working keyboard on it, the VT that
// showed before it showing again, and the VT given back if it opened it.
func repairVT(c console, s state) error {
	if s.VT <= 0 {
		return nil
	}
	fd, err := c.open(s.VT)
	if err != nil {
		return fmt.Errorf("VT %d: %w", s.VT, err)
	}
	m := vtMode{mode: vtAuto}
	errs := []error{
		c.ioctl(fd, vtSetMode, unsafe.Pointer(&m)),
		c.ioctlInt(fd, kdSetMode, kdText),
		c.ioctlInt(fd, kdSKBMode, kUnicode),
	}
	var st vtStat
	if tty0, err := c.open(0); err == nil {
		errs = append(errs, c.ioctl(tty0, vtGetState, unsafe.Pointer(&st)), c.close(tty0))
	}
	back := int(st.active) == s.VT && s.Prev > 0
	if back {
		errs = append(errs, c.ioctlInt(fd, vtActivate, uintptr(s.Prev)), waitActive(c, s.Prev))
	}
	errs = append(errs, c.close(fd))
	if s.Opened && back {
		errs = append(errs, disallocate(c, s.VT))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("VT %d: put back what a run before left: %w", s.VT, err)
	}
	return nil
}

// vtKey returns the VT that Ctrl+Alt with key switches to, or 0.
func vtKey(key uint16) int {
	switch {
	case key >= keyF1 && key <= keyF10:
		return int(key-keyF1) + 1
	case key == keyF11:
		return 11
	case key == keyF12:
		return 12
	}
	return 0
}
