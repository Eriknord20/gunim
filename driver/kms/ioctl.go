//go:build linux && (arm64 || amd64)

package kms

import (
	"errors"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The ioctl number's fields, as Linux's asm-generic/ioctl.h packs them.
const (
	iocNone  = 0
	iocWrite = 1
	iocRead  = 2
)

func ioc(dir, typ, nr, size uintptr) uintptr {
	return dir<<30 | size<<16 | typ<<8 | nr
}

func ion(typ, nr uintptr) uintptr       { return ioc(iocNone, typ, nr, 0) }
func iow(typ, nr, size uintptr) uintptr { return ioc(iocWrite, typ, nr, size) }
func ior(typ, nr, size uintptr) uintptr { return ioc(iocRead, typ, nr, size) }

// drmIOWR is DRM_IOWR: an ioctl of the DRM's own, 'd', that reads and
// writes its argument.
func drmIOWR(nr, size uintptr) uintptr { return ioc(iocRead|iocWrite, 'd', nr, size) }

// ioctl calls the ioctl req on fd with arg, again where a signal broke it
// off, and for a DRM call, again where the driver says to try again, as
// libdrm does: at once, and after a while with a pause, so a driver that
// keeps saying so does not spin a core.
func ioctl(fd int, req uintptr, arg unsafe.Pointer) error {
	for tries := 0; ; tries++ {
		_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
		if e == unix.EINTR {
			continue
		}
		if e == unix.EAGAIN && req>>8&0xff == 'd' {
			if tries >= 100 {
				time.Sleep(time.Millisecond)
			}
			continue
		}
		if e != 0 {
			return e
		}
		return nil
	}
}

// ioctlInt calls the ioctl req on fd with a plain number for its argument.
func ioctlInt(fd int, req, arg uintptr) error {
	for {
		_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, arg)
		if e == unix.EINTR {
			continue
		}
		if e != 0 {
			return e
		}
		return nil
	}
}

// pins holds the arrays an ioctl's structure points at, so they stay on
// the heap and in place until the call returns. Pinning makes them escape
// to the heap, where a stack that grows cannot move them.
type pins struct{ runtime.Pinner }

func (p *pins) u32s(s []uint32) uint64 {
	if len(s) == 0 {
		return 0
	}
	p.Pin(&s[0])
	return uint64(uintptr(unsafe.Pointer(&s[0])))
}

func (p *pins) u64s(s []uint64) uint64 {
	if len(s) == 0 {
		return 0
	}
	p.Pin(&s[0])
	return uint64(uintptr(unsafe.Pointer(&s[0])))
}

func (p *pins) bytes(s []byte) uint64 {
	if len(s) == 0 {
		return 0
	}
	p.Pin(&s[0])
	return uint64(uintptr(unsafe.Pointer(&s[0])))
}

func (p *pins) modes(s []modeInfo) uint64 {
	if len(s) == 0 {
		return 0
	}
	p.Pin(&s[0])
	return uint64(uintptr(unsafe.Pointer(&s[0])))
}

// errNotMaster is returned where another program shows the screen.
var errNotMaster = errors.New("another program holds the screen (DRM master)")

// cstr returns the string in b up to its first NUL.
func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// The errors a refused commit carries where another program is master.
var (
	errEACCES error = unix.EACCES
	errEPERM  error = unix.EPERM
)
