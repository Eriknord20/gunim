package vst3

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/marrasen/gunim/audio/plug"
)

// The machine code plugins stand for here: what a real one compiles to, with the unwind information a compiler gives it.

func imm64(v uint64) []byte { return binary.LittleEndian.AppendUint64(nil, v) }

// withTable is code at one allocation, with a function table of one function, its unwind information unwind.
func withTable(t *testing.T, code, unwind []byte) uintptr {
	t.Helper()
	size := len(code) + (-len(code) & 15)
	all := make([]byte, size+16+len(unwind))
	copy(all, code)
	binary.LittleEndian.PutUint32(all[size:], 0)
	binary.LittleEndian.PutUint32(all[size+4:], uint32(len(code)))
	binary.LittleEndian.PutUint32(all[size+8:], uint32(size+16))
	copy(all[size+16:], unwind)
	at := native(t, all)
	add := windows.NewLazySystemDLL("kernel32.dll").NewProc("RtlAddFunctionTable")
	if r, _, err := add.Call(at+uintptr(size), 1, at); r == 0 {
		t.Fatal(err)
	}
	return at
}

// trashCode moves the stack, sets every register a function must keep to something else, as the flags of the floating
// point and the direction, and reads address 0x5c: all that a fault part way through a function may leave.
func trashCode(t *testing.T) uintptr {
	t.Helper()
	code := []byte{0x48, 0x83, 0xEC, 0x48} // sub rsp,0x48
	const bad = 0xBADBADBADBADBAD
	for _, r := range [][]byte{{0x48, 0xBB}, {0x48, 0xBD}, {0x48, 0xBE}, {0x48, 0xBF}, {0x49, 0xBC}, {0x49, 0xBD}, {0x49, 0xBE}, {0x49, 0xBF}} {
		code = cat(code, r, imm64(bad)) // mov reg,bad
	}
	for x := byte(6); x <= 15; x++ {
		if x < 8 {
			code = append(code, 0x66, 0x48, 0x0F, 0x6E, 0xC3|x<<3) // movq xmmx,rbx
		} else {
			code = append(code, 0x66, 0x4C, 0x0F, 0x6E, 0xC3|(x&7)<<3) // movq xmmx,rbx
		}
	}
	code = cat(code,
		[]byte{0xC7, 0x04, 0x24, 0xC0, 0x9F, 0, 0},    // mov dword [rsp],0x9FC0: flush to zero, denormals as zero
		[]byte{0x0F, 0xAE, 0x14, 0x24},                // ldmxcsr [rsp]
		[]byte{0xC7, 0x04, 0x24, 0x7F, 0x00, 0, 0},    // mov dword [rsp],0x7F: x87 at single precision
		[]byte{0xD9, 0x2C, 0x24},                      // fldcw [rsp]
		[]byte{0xFD},                                  // std
		[]byte{0x48, 0x8B, 0x04, 0x25, 0x5C, 0, 0, 0}, // mov rax,[0x5c]
		[]byte{0xCC},                                  // int3: it never gets here
	)
	return withTable(t, code, []byte{1, 4, 1, 0, 4, 8<<4 | 2, 0, 0}) // sub rsp,0x48: UWOP_ALLOC_SMALL
}

// keepCode is a caller of the wrapper in machine code, as Go would hide what it holds in registers: it sets the registers
// a function must keep to known values, calls wrapper with fn, and returns the bits by which any came back changed, ORed.
func keepCode(t *testing.T, w, fn uintptr) uintptr {
	t.Helper()
	const keep = 0x1122334455667788
	pro, _ := prologue()
	code := pro
	code = cat(code, []byte{0x0F, 0xAE, 0x5C, 0x24, 0x28}) // stmxcsr [rsp+0x28]
	code = cat(code, []byte{0xD9, 0x7C, 0x24, 0x2C})       // fnstcw [rsp+0x2C]
	regs := [][]byte{{0x48, 0xBB}, {0x48, 0xBD}, {0x48, 0xBE}, {0x48, 0xBF}, {0x49, 0xBC}, {0x49, 0xBD}, {0x49, 0xBE}, {0x49, 0xBF}}
	for _, r := range regs {
		code = cat(code, r, imm64(keep)) // mov reg,keep
	}
	for x := byte(6); x <= 15; x++ {
		if x < 8 {
			code = append(code, 0x66, 0x48, 0x0F, 0x6E, 0xC3|x<<3) // movq xmmx,rbx
		} else {
			code = append(code, 0x66, 0x4C, 0x0F, 0x6E, 0xC3|(x&7)<<3) // movq xmmx,rbx
		}
	}
	code = cat(code,
		[]byte{0x48, 0xB9}, imm64(uint64(fn)), // mov rcx,fn
		[]byte{0x48, 0xB8}, imm64(uint64(w)), // mov rax,w
		[]byte{0xFF, 0xD0},              // call rax
		[]byte{0x31, 0xC0},              // xor eax,eax
		[]byte{0x49, 0xBA}, imm64(keep), // mov r10,keep
		[]byte{0x9C, 0x41, 0x5B, 0x49, 0xC1, 0xEB, 0x0A, 0x41, 0x83, 0xE3, 0x01}, // pushfq; pop r11; shr r11,10; and r11d,1: the direction
		[]byte{0x4C, 0x09, 0xD8}, // or rax,r11
	)
	for _, c := range [][]byte{{0x4C, 0x39, 0xD3}, {0x4C, 0x39, 0xD5}, {0x4C, 0x39, 0xD6}, {0x4C, 0x39, 0xD7}, {0x4D, 0x39, 0xD4}, {0x4D, 0x39, 0xD5}, {0x4D, 0x39, 0xD6}, {0x4D, 0x39, 0xD7}} {
		code = cat(code, c, []byte{0x74, 0x04, 0x48, 0x83, 0xC8, 0x02}) // cmp reg,r10; je +4... or rax,2
	}
	for x := byte(6); x <= 15; x++ {
		if x < 8 {
			code = append(code, 0x66, 0x49, 0x0F, 0x7E, 0xC3|x<<3) // movq r11,xmmx
		} else {
			code = append(code, 0x66, 0x4D, 0x0F, 0x7E, 0xC3|(x&7)<<3) // movq r11,xmmx
		}
		code = cat(code, []byte{0x4D, 0x39, 0xD3, 0x74, 0x04, 0x48, 0x83, 0xC8, 0x04}) // cmp r11,r10; je +4; or rax,4
	}
	code = cat(code,
		[]byte{0x0F, 0xAE, 0x5C, 0x24, 0x30},       // stmxcsr [rsp+0x30]
		[]byte{0xD9, 0x7C, 0x24, 0x34},             // fnstcw [rsp+0x34]
		[]byte{0x44, 0x8B, 0x54, 0x24, 0x28},       // mov r10d,[rsp+0x28]
		[]byte{0x44, 0x39, 0x54, 0x24, 0x30},       // cmp [rsp+0x30],r10d
		[]byte{0x74, 0x04, 0x48, 0x83, 0xC8, 0x08}, // je +4; or rax,8
		[]byte{0x44, 0x0F, 0xB7, 0x54, 0x24, 0x2C}, // movzx r10d,word [rsp+0x2C]
		[]byte{0x66, 0x44, 0x39, 0x54, 0x24, 0x34}, // cmp [rsp+0x34],r10w
		[]byte{0x74, 0x04, 0x48, 0x83, 0xC8, 0x10}, // je +4; or rax,16
		[]byte{0xFC},                         // cld
		[]byte{0x0F, 0xAE, 0x54, 0x24, 0x28}, // ldmxcsr [rsp+0x28]
		[]byte{0xD9, 0x6C, 0x24, 0x2C},       // fldcw [rsp+0x2C]
		epilogue())
	return native(t, code)
}

// ownHandlerCode reads address 0x5c, with a frame handler that steps over the read, as a __try in a plugin does.
func ownHandlerCode(t *testing.T) uintptr {
	t.Helper()
	code := []byte{
		0x48, 0x83, 0xEC, 0x28, // sub rsp,0x28
		0x48, 0x8B, 0x04, 0x25, 0x5C, 0, 0, 0, // mov rax,[0x5c]
		0xB8, 7, 0, 0, 0, // mov eax,7
		0x48, 0x83, 0xC4, 0x28, // add rsp,0x28
		0xC3, // ret
	}
	const handlerAt = 22
	code = cat(code, []byte{
		0x49, 0x81, 0x80, 0xF8, 0, 0, 0, 8, 0, 0, 0, // add qword [r8+0xF8],8
		0x31, 0xC0, 0xC3, // xor eax,eax; ret: ExceptionContinueExecution
	})
	return withTable(t, code, cat([]byte{1 | 1<<3, 4, 1, 0, 4, 4<<4 | 2, 0, 0}, le32(handlerAt)))
}

// throwCode does as a throw does with an exception no frame of the plugin catches: MSVC's raises one it says can't go on,
// GCC's one of its own code, which it doesn't.
func throwCode(t *testing.T, code uint32, flags byte) uintptr {
	t.Helper()
	raise, err := windows.GetProcAddress(windows.Handle(windows.NewLazySystemDLL("kernel32.dll").Handle()), "RaiseException")
	if err != nil {
		t.Fatal(err)
	}
	return withTable(t, cat(
		[]byte{0x48, 0x83, 0xEC, 0x28},             // sub rsp,0x28
		[]byte{0xB9}, le32(code), // mov ecx,code
		[]byte{0xBA, flags, 0, 0, 0}, // mov edx,flags
		[]byte{0x4D, 0x31, 0xC0, 0x4D, 0x31, 0xC9}, // xor r8,r8; xor r9,r9
		[]byte{0x48, 0xB8}, imm64(uint64(raise)),   // mov rax,raise
		[]byte{0xFF, 0xD0},                   // call rax
		[]byte{0x48, 0x83, 0xC4, 0x28, 0xC3}, // add rsp,0x28; ret
	), []byte{1, 4, 1, 0, 4, 4<<4 | 2, 0, 0})
}

// runCrashCase is the body of one case, in a process of its own, as a case that goes wrong ends it.
func runCrashCase(t *testing.T, name string) {
	if err := installGuard(); err != nil {
		t.Fatal(err)
	}
	w := wrapper.Load()
	switch name {
	case "crash":
		// Not called again, and not counted among what the plugin recovered from.
		at := native(t, faultCode)
		if r := callN(at, nil); r != crashedResult {
			t.Fatalf("the call gave %#x", r)
		}
		if !moduleDead(at) {
			t.Fatal("its module lives on")
		}
		if n, _, _ := Recovered(); n != 0 {
			t.Fatalf("%d counted as the plugin's own recoveries", n)
		}
		cs := Crashes()
		if len(cs) != 1 || cs[0].Code != 0xC0000005 || cs[0].Module != "" {
			t.Fatalf("crashes: %+v", cs)
		}
		if r := callN(at, nil); r != crashedResult || len(Crashes()) != 1 {
			t.Fatalf("called again: %#x, %+v", r, Crashes())
		}
		fmt.Println("lived")
	case "system":
		// A fault within a system library a plugin called is the plugin's: it dies by the function called.
		strlen, err := windows.GetProcAddress(windows.Handle(windows.NewLazySystemDLL("ntdll.dll").Handle()), "strlen")
		if err != nil {
			t.Fatal(err)
		}
		stub := withTable(t, cat(
			[]byte{0x48, 0x83, 0xEC, 0x28},            // sub rsp,0x28
			[]byte{0xB9, 0x5C, 0, 0, 0},               // mov ecx,0x5c
			[]byte{0x48, 0xB8}, imm64(uint64(strlen)), // mov rax,strlen
			[]byte{0xFF, 0xD0},                   // call rax
			[]byte{0x48, 0x83, 0xC4, 0x28, 0xC3}, // add rsp,0x28; ret
		), []byte{1, 4, 1, 0, 4, 4<<4 | 2, 0, 0})
		if r := callN(stub, nil); r != crashedResult {
			t.Fatalf("the call gave %#x", r)
		}
		cs := Crashes()
		if len(cs) != 1 || !strings.Contains(strings.ToLower(cs[0].Fault), "ntdll") {
			t.Fatalf("crashes: %+v", cs)
		}
		if moduleDead(strlen) {
			t.Fatal("the system's library went dead with it")
		}
		fmt.Println("lived")
	case "registers":
		// Through the wrapper from a caller in machine code: everything a function keeps comes back as it was.
		// The check itself: called bare, code that changes them all is seen to.
		changes := native(t, cat(
			[]byte{0x48, 0x31, 0xDB}, []byte{0x66, 0x0F, 0xEF, 0xF6}, // xor rbx,rbx; pxor xmm6,xmm6
			[]byte{0xC7, 0x44, 0x24, 0x08, 0xC0, 0x9F, 0, 0}, []byte{0x0F, 0xAE, 0x54, 0x24, 0x08}, // mov dword [rsp+8],0x9FC0; ldmxcsr [rsp+8]
			[]byte{0xC7, 0x44, 0x24, 0x08, 0x7F, 0, 0, 0}, []byte{0xD9, 0x6C, 0x24, 0x08}, // mov dword [rsp+8],0x7F; fldcw [rsp+8]
			[]byte{0xFD, 0xC3}, // std; ret
		))
		if r, _, _ := syscall.SyscallN(keepCode(t, changes, 0)); r != 1|2|4|8|16 {
			t.Fatalf("the check saw %#x of the changes", r)
		}
		leaf := native(t, []byte{0x31, 0xC0, 0xC3})
		if r, _, _ := syscall.SyscallN(keepCode(t, w, leaf)); r != 0 {
			t.Fatalf("a plain call changed %#x", r)
		}
		if r, _, _ := syscall.SyscallN(keepCode(t, w, trashCode(t))); r != 0 {
			t.Fatalf("a crash changed %#x (1 the direction, 2 an integer register, 4 an XMM one, 8 MXCSR, 16 the x87's word)", r)
		}
		fmt.Println("lived")
	case "own":
		// A plugin's own handler in its frames comes first, and the wrapper's never runs.
		if r := callN(ownHandlerCode(t), nil); r != 7 {
			t.Fatalf("the call gave %#x", r)
		}
		if len(Crashes()) != 0 {
			t.Fatalf("taken for a crash: %+v", Crashes())
		}
		if n, _, _ := Recovered(); n != 1 {
			t.Fatalf("%d recovered, want 1", n)
		}
		fmt.Println("lived")
	case "vectored":
		// So does one that catches it before any frame, as Reason Rack Plugin does.
		add := windows.NewLazySystemDLL("kernel32.dll").NewProc("AddVectoredExceptionHandler")
		if r, _, err := add.Call(1, native(t, recoverCode)); r == 0 {
			t.Fatal(err)
		}
		if r := callN(native(t, faultCode), nil); r != 1 {
			t.Fatalf("the call gave %#x", r)
		}
		if len(Crashes()) != 0 {
			t.Fatalf("taken for a crash: %+v", Crashes())
		}
		fmt.Println("lived")
	case "throw":
		if r := callN(throwCode(t, 0xE06D7363, 1), nil); r != crashedResult {
			t.Fatalf("the call gave %#x", r)
		}
		if cs := Crashes(); len(cs) != 1 || cs[0].Code != 0xE06D7363 {
			t.Fatalf("crashes: %+v", cs)
		}
		fmt.Println("lived")
	case "throwgcc":
		if r := callN(throwCode(t, 0x20474343, 0), nil); r != crashedResult {
			t.Fatalf("the call gave %#x", r)
		}
		if cs := Crashes(); len(cs) != 1 || cs[0].Code != 0x20474343 {
			t.Fatalf("crashes: %+v", cs)
		}
		fmt.Println("lived")
	case "thread":
		// On a thread Go doesn't own, as an audio driver's.
		at := native(t, faultCode)
		stub := native(t, cat(
			[]byte{0x48, 0x83, 0xEC, 0x28},
			[]byte{0x48, 0xB9}, imm64(uint64(at)), // mov rcx,at
			[]byte{0x48, 0xB8}, imm64(uint64(w)), // mov rax,w
			[]byte{0xFF, 0xD0},                   // call rax
			[]byte{0x48, 0x83, 0xC4, 0x28, 0xC3}, // add rsp,0x28; ret
		))
		create := windows.NewLazySystemDLL("kernel32.dll").NewProc("CreateThread")
		h, _, err := create.Call(0, 0, stub, 0, 0, 0)
		if h == 0 {
			t.Fatal(err)
		}
		if _, err := windows.WaitForSingleObject(windows.Handle(h), 10000); err != nil {
			t.Fatal(err)
		}
		var code uint32
		get := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetExitCodeThread")
		if r, _, err := get.Call(h, uintptr(unsafe.Pointer(&code))); r == 0 {
			t.Fatal(err)
		}
		if code != uint32(crashMark&0xFFFFFFFF) {
			t.Fatalf("the thread gave %#x", code)
		}
		fmt.Println("lived")
	case "busy":
		// Many crashes, on many threads, with the garbage collector busy. The wrapper is called bare, as a plugin dies once.
		at := native(t, faultCode)
		stop := make(chan struct{})
		var made atomic.Int64
		go func() {
			var keep [][]byte
			for {
				select {
				case <-stop:
					return
				default:
				}
				keep = append(keep, make([]byte, 4096))
				made.Add(1)
				if len(keep) > 2000 {
					keep = nil
					runtime.GC()
				}
			}
		}()
		const each, threads = 25000, 4
		var wg sync.WaitGroup
		var wrong atomic.Int64
		for range threads {
			wg.Go(func() {
				for range each {
					if r, r2, e := syscall.SyscallN(w, at); r != crashMark || r2 != at || e != 0xC0000005 {
						wrong.Add(1)
					}
				}
			})
		}
		wg.Wait()
		close(stop)
		if wrong.Load() != 0 {
			t.Fatalf("%d wrong of %d", wrong.Load(), each*threads)
		}
		fmt.Printf("lived, %d crashes, %d blocks made\n", each*threads, made.Load())
	default:
		t.Fatalf("no case %q", name)
	}
}

const crashCaseEnv = "GUNIM_VST3_CRASH_CASE"

func TestCrashCase(t *testing.T) {
	name := os.Getenv(crashCaseEnv)
	if name == "" {
		t.Skip("run by TestCrashesInACall")
	}
	runCrashCase(t, name)
}

// A plugin's crash during a call into it comes back as a call that failed, in each way a plugin can get there.
func TestCrashesInACall(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"crash", "system", "registers", "own", "vectored", "throw", "throwgcc", "thread", "busy"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCrashCase$", "-test.v", "-test.timeout=120s")
			cmd.Env = append(os.Environ(), crashCaseEnv+"="+name)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if !strings.Contains(string(out), "lived") {
				t.Fatalf("its output lacks the end:\n%s", out)
			}
		})
	}
}

// The wrapper hands on every argument, each in its integer and its floating register, and both results.
func TestTheWrapperPassesWhatACallDoes(t *testing.T) {
	t.Parallel()
	if err := installGuard(); err != nil {
		t.Fatal(err)
	}
	var got [8]uintptr
	fn := syscall.NewCallback(func(a, b, c, d, e, f, g, h uintptr) uintptr {
		got = [8]uintptr{a, b, c, d, e, f, g, h}
		return 77
	})
	all := []uintptr{1, 2, 3, 4, 5, 6, 7, 8}
	if r := callN(fn, all); r != 77 {
		t.Fatalf("it returned %d", r)
	}
	if [8]uintptr(all) != got {
		t.Fatalf("it took %v", got)
	}
	// movq rax,xmmN; ret
	for i, code := range [][]byte{{0x66, 0x48, 0x0F, 0x7E, 0xC0}, {0x66, 0x48, 0x0F, 0x7E, 0xC8}, {0x66, 0x48, 0x0F, 0x7E, 0xD0}, {0x66, 0x48, 0x0F, 0x7E, 0xD8}} {
		at := native(t, append(code, 0xC3))
		if r := callN(at, all[:4]); r != all[i] {
			t.Fatalf("XMM%d held %d", i, r)
		}
	}
	// addsd xmm0,xmm0; ret: a double comes back.
	double := native(t, []byte{0xF2, 0x0F, 0x58, 0xC0, 0xC3})
	if _, r2 := nativeCall(double, uintptr(math.Float64bits(1.5))); math.Float64frombits(uint64(r2)) != 3 {
		t.Fatalf("it gave %v", math.Float64frombits(uint64(r2)))
	}
}

// A call makes nothing, as the audio's thread makes one for each plugin in each block.
func TestACallMakesNothing(t *testing.T) {
	if err := installGuard(); err != nil {
		t.Fatal(err)
	}
	leaf := native(t, []byte{0xC3})
	args := []uintptr{1, 2}
	if n := testing.AllocsPerRun(100, func() { callN(leaf, args) }); n != 0 {
		t.Fatalf("%v made", n)
	}
}

// A plugin that crashes as it processes plays silence from then on, and is never called again, not even to let it go, and
// so is every other of its module. Its latency stays, and what it was last known to hold is what a song keeps.
func TestAPluginThatCrashesGoesSilent(t *testing.T) {
	t.Parallel()
	fault := native(t, faultCode)
	made := func() (*fakePlugin, *plugged, *Plugin) {
		f := fakeSynth()
		f.latency, f.state = 64, []byte("kept")
		cl := &plugClass{open: func(cfg Config) (*Plugin, error) { return f.open(t, cfg) }, class: f.class()}
		a := cl.newPlugin()
		a.proc.Prepare(plug.Setup{Rate: 48000, MaxBlock: 64, Channels: 2})
		p := a.Plugin()
		if p == nil {
			t.Fatalf("it didn't start: %v", a.Err())
		}
		p.processFn = fault
		return f, a, p
	}
	f, a, p := made()
	f2, a2, p2 := made()
	given := a.State()
	if len(given) == 0 {
		t.Fatal("it gave no state")
	}

	out := [][]float32{make([]float32, 64), make([]float32, 64)}
	for i := range out[0] {
		out[0][i], out[1][i] = 1, 1
	}
	blk := &plug.Block{Frames: 64, Out: out}
	a.proc.Process(blk)
	if !p.Gone() {
		t.Fatal("it plays on")
	}
	if !p2.Gone() {
		t.Fatal("the other of its module plays on")
	}
	if err := a.Err(); err != nil {
		t.Fatalf("it says %v", err)
	}
	if a.proc.Latency() != 64 {
		t.Fatalf("its latency is %d", a.proc.Latency())
	}
	for i := range out[0] {
		if out[0][i] != 0 || out[1][i] != 0 {
			t.Fatal("it left sound in its output")
		}
	}
	out[0][0] = 1
	a.proc.Process(blk)
	if out[0][0] != 0 {
		t.Fatal("the block after it isn't silent")
	}
	f.mu.Lock()
	sets := f.setStates
	f.mu.Unlock()
	if !bytes.Equal(a.State(), given) || a.Changed() {
		t.Fatal("what it last gave isn't what it gives")
	}
	if err := a.SetState([]byte("gVS3\x00\x00\x00\x00\x00\x00\x00\x00")); err != nil {
		t.Fatalf("setting its state says %v", err)
	}
	if err := a.Err(); err != nil {
		t.Fatalf("it says %v", err)
	}
	if string(a.State()) != "gVS3\x00\x00\x00\x00\x00\x00\x00\x00" {
		t.Fatal("what the song set isn't what the song keeps")
	}
	if _, err := p.State(); !errors.Is(err, ErrClosed) {
		t.Fatalf("reading its state says %v", err)
	}
	p.Reset()
	a.proc.Reset()
	a.Close()
	a2.Close()
	for _, x := range []*fakePlugin{f, f2} {
		x.mu.Lock()
		if x.terminates != 0 {
			t.Error("it was let go, with its locks perhaps held")
		}
		x.mu.Unlock()
	}
	f.mu.Lock()
	if f.setStates != sets {
		t.Error("it was given a state")
	}
	f.mu.Unlock()
}

// BenchmarkCall is what a call into a plugin costs, nothing faulting, by what goes around it.
func BenchmarkCall(b *testing.B) {
	if err := installGuard(); err != nil {
		b.Fatal(err)
	}
	at, err := windows.VirtualAlloc(0, 16, windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_EXECUTE_READWRITE)
	if err != nil {
		b.Fatal(err)
	}
	*(*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&at))) = 0xC3
	args := []uintptr{1, 2}
	b.Run("system", func(b *testing.B) {
		for b.Loop() {
			_, _, _ = syscall.SyscallN(at, args...)
		}
	})
	b.Run("wrapped", func(b *testing.B) {
		for b.Loop() {
			callN(at, args)
		}
	})
}
