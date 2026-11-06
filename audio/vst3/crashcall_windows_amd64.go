package vst3

import (
	"encoding/binary"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Every call into a plugin goes through a small wrapper, machine code made here, with a function table of its own and
// an exception routine, as Go's assembler can't give a function either. A fault in the plugin walks its stack frames
// from the inside out, so the plugin's own handlers come first and the wrapper's routine after them: a plugin that
// handles its own fault never reaches it. Where one does, the routine sends the thread to the wrapper's landing, on the
// wrapper's frame, which puts back all the registers the plugin may have left wrong and returns to Go as a plain return
// would, with the sign of a crash. Going by the frame needs no slot per thread, and calls within calls look after
// themselves.
//
// The routine goes there by RtlRestoreContext, not by answering that the exception may go on. A C++ throw is raised as
// one that can't go on, and Windows would raise that answer as a new exception, over and over until the stack ran out.
// Going straight there also leaves Go's last handler out of it, which would take the answer for a crash.
//
// The plugin's frames are not unwound: its destructors don't run, as they are code of a module no longer to be trusted.
//
// Faults a frame walk doesn't reach stay crashes, reported as ever: a plugin's frames without unwind information, as
// code it generates may be, or a stack it corrupted; its editor's window procedure, which Windows calls; and threads it
// started. So do __fastfail and TerminateProcess, as in a plugin's std::terminate, as no handler sees them.

// The wrapper's frame, from its stack pointer in the body. Windows hands the routine this as the establisher frame.
const (
	frameArgs   = 0x20  // the arguments past the fourth, for the plugin
	frameMXCSR  = 0x60  // the floating point control words, as they were
	frameX87    = 0x64  //
	frameFault  = 0x68  // where the fault was
	frameXMM    = 0x70  // XMM6 to XMM15
	frameCode   = 0x110 // the exception's code
	frameSize   = 0x118 // less the pushes
	framePushes = 8 * 8
	// frameIn is where the wrapper's own fifth argument is, the plugin's fourth.
	frameIn = frameSize + framePushes + 8 + 0x20
	// stackArgs is how many arguments the plugin takes past its fourth: more than any method takes.
	stackArgs = 8
)

// crashMark is what the wrapper returns from a crash, in RAX. It also gives the fault's address in XMM0 and the
// exception's code as the last error, which the system's call hands on.
const crashMark = 0x6B5C5A7D1E3F9A41

// memFree is the state of memory no one holds, as VirtualQuery gives it.
const memFree = 0x10000

var (
	// wrapper is the wrapper's entry, 0 before it is made, or where it could not be.
	wrapper atomic.Uintptr
)

func cat(parts ...[]byte) []byte { return slices.Concat(parts...) }

func le32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

func le64(v uint64) []byte { return binary.LittleEndian.AppendUint64(nil, v) }

// xmmOp is an instruction op on XMM register x, at [rsp+at]: 0x29 stores, 0x28 loads.
func xmmOp(op byte, x int, at uint32) []byte {
	mod := byte(0x84 | (x&7)<<3)
	if x >= 8 {
		return cat([]byte{0x44, 0x0F, op, mod, 0x24}, le32(at))
	}
	return cat([]byte{0x0F, op, mod, 0x24}, le32(at))
}

// prologue is the wrapper's prologue, with its unwind codes, last first.
func prologue() (code, unwind []byte) {
	pushes := []struct {
		reg  byte
		code []byte
	}{
		{5, []byte{0x55}}, {3, []byte{0x53}}, {6, []byte{0x56}}, {7, []byte{0x57}},
		{12, []byte{0x41, 0x54}}, {13, []byte{0x41, 0x55}}, {14, []byte{0x41, 0x56}}, {15, []byte{0x41, 0x57}},
	}
	for _, p := range pushes {
		code = append(code, p.code...)
		unwind = append([]byte{byte(len(code)), p.reg << 4}, unwind...)
	}
	code = cat(code, []byte{0x48, 0x81, 0xEC}, le32(frameSize)) // sub rsp,frameSize
	unwind = append([]byte{byte(len(code)), 1, frameSize / 8, 0}, unwind...)
	for x := 6; x <= 15; x++ {
		at := frameXMM + uint32(x-6)*16
		code = cat(code, xmmOp(0x29, x, at)) // movaps [rsp+at],xmmx
		unwind = append([]byte{byte(len(code)), 8 | byte(x)<<4, byte(at / 16), 0}, unwind...)
	}
	return code, unwind
}

// epilogue is the wrapper's way out.
func epilogue() []byte {
	var code []byte
	for x := 6; x <= 15; x++ {
		code = cat(code, xmmOp(0x28, x, frameXMM+uint32(x-6)*16)) // movaps xmmx,[rsp+at]
	}
	return cat(code, []byte{0x48, 0x81, 0xC4}, le32(frameSize), // add rsp,frameSize
		[]byte{0x41, 0x5F, 0x41, 0x5E, 0x41, 0x5D, 0x41, 0x5C, 0x5F, 0x5E, 0x5B, 0x5D, 0xC3}) // pop r15 to rbp; ret
}

// wrapperCode lays out the wrapper: wrapper(fn, a1, a2, ...) calls fn(a1, a2, ...), its floating registers moved along
// with its integer ones, as the system's call fills both. The function ends at end; the landing and the routine follow.
// restore is where RtlRestoreContext is.
func wrapperCode(restore uintptr) (code, unwindInfo []byte, end int) {
	pro, unwind := prologue()
	code = cat(pro,
		[]byte{0x0F, 0xAE, 0x5C, 0x24, frameMXCSR},    // stmxcsr [rsp+frameMXCSR]
		[]byte{0xD9, 0x7C, 0x24, frameX87},            // fnstcw [rsp+frameX87]
		[]byte{0x48, 0x89, 0xC8},                      // mov rax,rcx
		[]byte{0x48, 0x89, 0xD1},                      // mov rcx,rdx
		[]byte{0x4C, 0x89, 0xC2},                      // mov rdx,r8
		[]byte{0x4D, 0x89, 0xC8},                      // mov r8,r9
		[]byte{0x4C, 0x8B, 0x8C, 0x24}, le32(frameIn), // mov r9,[rsp+frameIn]
		[]byte{0x0F, 0x28, 0xC1},             // movaps xmm0,xmm1
		[]byte{0x0F, 0x28, 0xCA},             // movaps xmm1,xmm2
		[]byte{0x0F, 0x28, 0xD3},             // movaps xmm2,xmm3
		[]byte{0x66, 0x49, 0x0F, 0x6E, 0xD9}, // movq xmm3,r9
	)
	for i := range uint32(stackArgs) {
		code = cat(code,
			[]byte{0x4C, 0x8B, 0x94, 0x24}, le32(frameIn+8+8*i), // mov r10,[rsp+in]
			[]byte{0x4C, 0x89, 0x54, 0x24, byte(frameArgs + 8*i)}) // mov [rsp+out],r10
	}
	code = cat(code, []byte{0xFF, 0xD0}) // call rax
	exit := len(code)
	code = cat(code, epilogue())
	end = len(code)

	// The landing, where the routine moves the thread.
	landing := len(code)
	land := cat([]byte{
		// cld
		0xFC,
		// fninit: the x87's stack empties, its control word comes next
		0xDB, 0xE3,
		// fldcw [rsp+frameX87]
		0xD9, 0x6C, 0x24, frameX87,
		// ldmxcsr [rsp+frameMXCSR]
		0x0F, 0xAE, 0x54, 0x24, frameMXCSR,
		// movq xmm0,[rsp+frameFault]
		0xF3, 0x0F, 0x7E, 0x44, 0x24, frameFault,
		// mov eax,[rsp+frameCode]
		0x8B, 0x84, 0x24,
	}, le32(frameCode), []byte{
		// mov gs:[0x68],eax: the TEB's last error
		0x65, 0x89, 0x04, 0x25, 0x68, 0, 0, 0,
		// mov rax,crashMark
		0x48, 0xB8,
	}, le64(crashMark))
	code = cat(code, land, []byte{0xE9}, le32(uint32(exit-(landing+len(land)+5)))) // jmp exit

	// The routine: routine(record, frame, context, dispatcher), which come in RCX, RDX, R8 and R9.
	routine := len(code)
	head := []byte{
		0x8B, 0x41, 0x04, // mov eax,[rcx+4]: its flags
		0xA8, 0x06, // test al,6: unwinding
		0x75, 0, // jnz pass
		0x8B, 0x01, // mov eax,[rcx]: its code
		0x3D, 0x43, 0x43, 0x47, 0x20, // cmp eax,0x20474343: a C++ throw of GCC's, of no severity at all
		0x74, 0x08, // je take
		0xC1, 0xE8, 0x1E, // shr eax,30
		0x83, 0xF8, 0x03, // cmp eax,3: an error, not a warning or a note, as a debugger's
		0x75, 0, // jne pass
		// take:
		0x48, 0x8B, 0x41, 0x10, // mov rax,[rcx+0x10]: where it was
		0x48, 0x89, 0x42, frameFault, // mov [rdx+frameFault],rax
		0x8B, 0x01, // mov eax,[rcx]
	}
	mid := cat(head,
		[]byte{0x89, 0x82}, le32(frameCode), // mov [rdx+frameCode],eax
		[]byte{0x49, 0x89, 0x90, 0x98, 0, 0, 0}, // mov [r8+0x98],rdx: Context.Rsp
	)
	at := routine + len(mid) + 7
	body := cat(mid,
		[]byte{0x48, 0x8D, 0x05}, le32(uint32(landing-at)), // lea rax,[rip+landing]
		[]byte{0x49, 0x89, 0x80, 0xF8, 0, 0, 0},   // mov [r8+0xF8],rax: Context.Rip
		[]byte{0x4C, 0x89, 0xC1},                  // mov rcx,r8
		[]byte{0x31, 0xD2},                        // xor edx,edx
		[]byte{0x48, 0x83, 0xEC, 0x28},            // sub rsp,0x28
		[]byte{0x48, 0xB8}, le64(uint64(restore)), // mov rax,restore
		[]byte{0xFF, 0xD0}, // call rax: it never comes back
		[]byte{0xCC},       // int3
	)
	body[6] = byte(len(body) - 7)
	body[23] = byte(len(body) - 24)
	code = cat(code, body, []byte{0xB8, 1, 0, 0, 0, 0xC3}) // pass: mov eax,1; ret: ExceptionContinueSearch

	unwindInfo = cat([]byte{1 | 1<<3, byte(len(pro)), byte(len(unwind) / 2), 0}, unwind, le32(uint32(routine)))
	return code, unwindInfo, end
}

// makeWrapper makes the wrapper, where it can run, and registers its function table. Where it can't, a plugin's crash
// stays what it was, the end of the program.
func makeWrapper() error {
	restore, err := windows.GetProcAddress(windows.Handle(windows.NewLazySystemDLL("ntdll.dll").Handle()), "RtlRestoreContext")
	if err != nil {
		return err
	}
	code, unwind, end := wrapperCode(restore)
	code = append(code, make([]byte, -len(code)&15)...)
	tableAt := len(code)
	unwindAt := tableAt + 16
	size := unwindAt + len(unwind)
	base, err := windows.VirtualAlloc(0, uintptr(size), windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_READWRITE)
	if err != nil {
		return err
	}
	mem := unsafe.Slice(*(**byte)(unsafe.Pointer(&base)), size)
	copy(mem, code)
	binary.LittleEndian.PutUint32(mem[tableAt:], 0)
	binary.LittleEndian.PutUint32(mem[tableAt+4:], uint32(end))
	binary.LittleEndian.PutUint32(mem[tableAt+8:], uint32(unwindAt))
	copy(mem[unwindAt:], unwind)
	var was uint32
	if err := windows.VirtualProtect(base, uintptr(size), windows.PAGE_EXECUTE_READ, &was); err != nil {
		return err
	}
	add := windows.NewLazySystemDLL("kernel32.dll").NewProc("RtlAddFunctionTable")
	if r, _, err := add.Call(base+uintptr(tableAt), 1, base); r == 0 {
		return fmt.Errorf("RtlAddFunctionTable: %w", err)
	}
	wrapper.Store(base)
	return nil
}

// span is memory a module holds.
type span struct{ lo, hi uintptr }

// ownedSpan is memory the module the host loaded from the file owner holds, or a library of its.
type ownedSpan struct {
	span
	owner string
}

var (
	// deadSpans are the modules that crashed. A call reads them with no lock.
	deadSpans atomic.Pointer[[]span]
	// testGate, set by a test once deadSpans is set, says what is dead in their place.
	testGate func(fn uintptr) bool
	// testFault, set by a test, says what to call in place of fn.
	testFault atomic.Pointer[func(fn uintptr) uintptr]

	crashesMu sync.Mutex
	crashList []Crash
	owned     []ownedSpan

	crashOnce sync.Once
	crashed   = make(chan struct{})

	// uiThread is the plugins' thread, and uiOwner the file of the module it is calling, where a call to something
	// not known as that module's is still its.
	uiThread atomic.Uint32
	uiOwner  atomic.Pointer[string]
)

// crashSignal closes as the first plugin crashes.
func crashSignal() <-chan struct{} { return crashed }

// noteUIThread notes that this thread is the plugins'.
func noteUIThread() { uiThread.Store(windows.GetCurrentThreadId()) }

// setUIOwner says the plugins' thread now calls the module of the file owner, or none for "".
func setUIOwner(owner string) {
	if owner == "" {
		uiOwner.Store(nil)
		return
	}
	uiOwner.Store(&owner)
}

// ownFn notes that what fn is in belongs to the module of the file owner: a library it loaded, or code it made, as what
// a host calls may be in either. A crash in any of it is then a crash of that module, for all of it, under its name. It
// runs as an object is made, not as it plays. The program's own image and the system's are nobody's.
func ownFn(fn uintptr, owner string) {
	if owner == "" || fn == 0 {
		return
	}
	lo, hi, path := moduleOf(fn)
	if lo >= imageLo && lo < imageHi || isSystem(path) {
		return
	}
	s := ownedSpan{span{lo, hi}, owner}
	crashesMu.Lock()
	defer crashesMu.Unlock()
	if path == "" {
		// Code it made is its alone: what held this memory before has let it go.
		owned = slices.DeleteFunc(owned, func(o ownedSpan) bool { return o.span == s.span && o.owner != owner })
	}
	if !slices.Contains(owned, s) {
		owned = append(owned, s)
	}
}

// forgetOwned forgets what the module of the file owner held, as it unloads.
func forgetOwned(owner string) {
	crashesMu.Lock()
	defer crashesMu.Unlock()
	owned = slices.DeleteFunc(owned, func(o ownedSpan) bool { return o.owner == owner })
}

// isSystem reports whether the file at path is one of the system's.
func isSystem(path string) bool {
	dir, err := windows.GetSystemDirectory()
	if err != nil || path == "" {
		return false
	}
	return len(path) > len(dir) && strings.EqualFold(path[:len(dir)], dir)
}

// moduleDead reports whether fn is in a module that crashed.
func moduleDead(fn uintptr) bool {
	l := deadSpans.Load()
	if l == nil {
		return false
	}
	if testGate != nil {
		return testGate(fn)
	}
	for _, s := range *l {
		if fn >= s.lo && fn < s.hi {
			return true
		}
	}
	return false
}

// nativeCall calls fn with args, as the system's call does, unless its module crashed before. It takes no more than
// maxCallArgs. A call that crashes, or isn't made, gives crashedResult. It makes nothing, unless it crashes.
func nativeCall(fn uintptr, args ...uintptr) (r1, r2 uintptr) {
	if moduleDead(fn) {
		return crashedResult, 0
	}
	if f := testFault.Load(); f != nil {
		fn = (*f)(fn)
	}
	w := wrapper.Load()
	if w == 0 {
		r1, r2, _ = syscall.SyscallN(fn, args...)
		return r1, r2
	}
	var all [maxCallArgs + 1]uintptr
	all[0] = fn
	n := 1 + copy(all[1:], args)
	r1, r2, e := syscall.SyscallN(w, all[:n]...)
	if r1 == crashMark {
		noteCrash(fn, r2, uint32(e))
		return crashedResult, 0
	}
	return r1, r2
}

// ownersOf are the files of the modules that own fn, more than one where they share a library, known with no call to the
// system. crashesMu is held.
func ownersOf(fn uintptr) []string {
	var out []string
	for _, o := range owned {
		if fn >= o.lo && fn < o.hi && !slices.Contains(out, o.owner) {
			out = append(out, o.owner)
		}
	}
	return out
}

// noteCrash notes that the module that owns fn crashed: code at the address at. Where modules share the library fn is in,
// all of them did: a fault there may leave what they share wrong and its locks held, and which one's object it was isn't
// known here. All of each goes dead first, so another thread calls it no more. It runs on the thread that crashed, perhaps
// the audio's, once for a module.
func noteCrash(fn, at uintptr, code uint32) {
	crashesMu.Lock()
	owners := ownersOf(fn)
	crashesMu.Unlock()
	// Not known: only now ask the system, with no lock held of ours.
	var lo, hi uintptr
	path := ""
	if len(owners) == 0 {
		lo, hi, path = moduleOf(fn)
		if u := uiOwner.Load(); u != nil && windows.GetCurrentThreadId() == uiThread.Load() {
			owners = []string{*u}
		}
	}
	crashesMu.Lock()
	defer crashesMu.Unlock()
	defer crashOnce.Do(func() { close(crashed) })
	if moduleDead(fn) {
		// Two threads crashed in it at once.
		return
	}
	var list []span
	if l := deadSpans.Load(); l != nil {
		list = slices.Clone(*l)
	}
	for _, o := range owned {
		if slices.Contains(owners, o.owner) {
			list = append(list, o.span)
		}
	}
	switch {
	case hi == 0:
	case lo >= imageLo && lo < imageHi || isSystem(path):
		// Only what was called: Go's image holds what a test stands in for a plugin, and the system's is nobody's.
		list = append(list, span{fn, fn + 1})
	default:
		list = append(list, span{lo, hi})
	}
	deadSpans.Store(&list)
	if len(owners) == 0 {
		if isSystem(path) {
			path = ""
		}
		owners = []string{path}
	}
	first := len(crashList)
	for _, o := range owners {
		crashList = append(crashList, Crash{Module: o, Code: code, Offset: at})
	}
	// Finding the fault's file takes the loader's lock, which a thread held in the plugin may hold.
	go findFault(first, len(crashList), at)
}

// findFault notes which file the fault at the address at was in, of the crashes from first up to end.
func findFault(first, end int, at uintptr) {
	lo, _, f := moduleOf(at)
	if f == "" {
		return
	}
	crashesMu.Lock()
	defer crashesMu.Unlock()
	for i := first; i < end; i++ {
		crashList[i].Fault, crashList[i].Offset = f, at-lo
	}
}

// moduleOf is the memory the module holding at has, and its file. Memory no module holds, as code a plugin made, is
// taken as the allocation it is part of, with no file.
func moduleOf(at uintptr) (lo, hi uintptr, path string) {
	var h windows.Handle
	const flags = windows.GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | windows.GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT
	get := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleExW")
	if r, _, _ := get.Call(flags, at, uintptr(unsafe.Pointer(&h))); r != 0 {
		var info windows.ModuleInfo
		if windows.GetModuleInformation(windows.CurrentProcess(), h, &info, uint32(unsafe.Sizeof(info))) == nil {
			buf := make([]uint16, windows.MAX_LONG_PATH)
			if l, err := windows.GetModuleFileName(h, &buf[0], uint32(len(buf))); err == nil {
				path = windows.UTF16ToString(buf[:l])
			}
			return info.BaseOfDll, info.BaseOfDll + uintptr(info.SizeOfImage), path
		}
	}
	var m windows.MemoryBasicInformation
	if windows.VirtualQuery(at, &m, unsafe.Sizeof(m)) == nil && m.State != memFree && m.AllocationBase != 0 {
		return m.AllocationBase, m.BaseAddress + m.RegionSize, ""
	}
	// Free memory is nobody's, so it stands for itself alone.
	return at, at + 1, ""
}

// Crashes returns the plugins' crashes the program has lived through, the oldest first. Where modules share a library, a
// crash in it is one for each. A crash's Fault comes a moment after it. It is safe to call from any goroutine.
func Crashes() []Crash {
	crashesMu.Lock()
	defer crashesMu.Unlock()
	return slices.Clone(crashList)
}

// EndNow ends the program at once, with code, as one should that lived through a plugin's crash, once it has saved what
// it means to: a normal end runs code of every module loaded as it goes, on locks the crash may have left held.
func EndNow(code uint32) {
	_ = windows.TerminateProcess(windows.CurrentProcess(), code)
}
