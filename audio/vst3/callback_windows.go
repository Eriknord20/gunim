package vst3

import (
	"math"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// newCallback returns fn as a function a plugin can call.
func newCallback(fn any) uintptr { return syscall.NewCallback(fn) }

// A callback of Windows' takes its arguments in the integer registers
// only, while x64 passes a float or a double in the XMM register of its
// place. floatThunk returns a function that copies XMM2 into R8, the
// third integer register, and jumps to fn: the callback reads the
// double's bits there.
func floatThunk(fn uintptr) uintptr { return floatThunkArgs(fn, 2) }

// floatThunkArgs returns a function that copies the XMM register of
// each of args, places from 0 to 3, into the integer register of the
// same place, and jumps to fn.
func floatThunkArgs(fn uintptr, args ...int) uintptr {
	// movq r64, xmm: 66 REX.W 0F 7E, then the registers. The integer
	// registers of the four places are RCX, RDX, R8 and R9.
	moves := map[int][]byte{
		0: {0x66, 0x48, 0x0F, 0x7E, 0xC1}, // movq rcx, xmm0
		1: {0x66, 0x48, 0x0F, 0x7E, 0xCA}, // movq rdx, xmm1
		2: {0x66, 0x49, 0x0F, 0x7E, 0xD0}, // movq r8, xmm2
		3: {0x66, 0x49, 0x0F, 0x7E, 0xD9}, // movq r9, xmm3
	}
	var code []byte
	for _, a := range args {
		code = append(code, moves[a]...)
	}
	code = append(code, 0x48, 0xB8, 0, 0, 0, 0, 0, 0, 0, 0, // mov rax, fn
		0xFF, 0xE0) // jmp rax
	*(*uint64)(unsafe.Pointer(&code[len(code)-10])) = uint64(fn)
	mem, err := windows.VirtualAlloc(0, uintptr(len(code)), windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_READWRITE)
	if err != nil {
		panic("vst3: " + err.Error())
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(mem)), len(code)), code) //nolint:govet // memory of the system's
	var old uint32
	if err := windows.VirtualProtect(mem, uintptr(len(code)), windows.PAGE_EXECUTE_READ, &old); err != nil {
		panic("vst3: " + err.Error())
	}
	return mem
}

// newFloatCallback returns fn, which takes a double third, as a function
// a plugin can call.
func newFloatCallback(fn func(a, b uintptr, v float64) uintptr) uintptr {
	return floatThunk(syscall.NewCallback(func(a, b, v uintptr) uintptr {
		return fn(a, b, math.Float64frombits(uint64(v)))
	}))
}

// newFloatCallback4 is newFloatCallback for a function of four
// arguments.
func newFloatCallback4(fn func(a, b uintptr, v float64, d uintptr) uintptr) uintptr {
	return floatThunk(syscall.NewCallback(func(a, b, v, d uintptr) uintptr {
		return fn(a, b, math.Float64frombits(uint64(v)), d)
	}))
}
