package vst3

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The machine code a plugin stands for here, as the guard tells a plugin by its
// being outside this program's image.
var (
	// A read of address 0x5c, 8 bytes long, then mov eax,1; ret.
	faultCode = []byte{0x48, 0x8B, 0x04, 0x25, 0x5C, 0x00, 0x00, 0x00, 0xB8, 0x01, 0x00, 0x00, 0x00, 0xC3}
	// A vectored exception handler that steps over an access violation's 8 bytes
	// and answers EXCEPTION_CONTINUE_EXECUTION, as a plugin that recovers does.
	recoverCode = []byte{
		0x48, 0x8B, 0x41, 0x08, // mov rax,[rcx+8]
		0x48, 0x8B, 0x11, // mov rdx,[rcx]
		0x81, 0x3A, 0x05, 0x00, 0x00, 0xC0, // cmp dword [rdx],0xC0000005
		0x75, 0x0F, // jne out
		0x48, 0x81, 0x80, 0xF8, 0x00, 0x00, 0x00, 0x08, 0x00, 0x00, 0x00, // add qword [rax+0xF8],8
		0x83, 0xC8, 0xFF, // or eax,-1
		0xC3,       // ret
		0x31, 0xC0, // out: xor eax,eax
		0xC3, // ret
	}
)

// native copies code where it can run, outside this program's image.
func native(t *testing.T, code []byte) uintptr {
	t.Helper()
	at, err := windows.VirtualAlloc(0, uintptr(len(code)), windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_EXECUTE_READWRITE)
	if err != nil {
		t.Fatal(err)
	}
	copy(unsafe.Slice(*(**byte)(unsafe.Pointer(&at)), len(code)), code)
	return at
}

// runGuardCase is the body of one case, in a process of its own, as a case that
// goes wrong ends it.
func runGuardCase(t *testing.T, name string) {
	fault := func() uintptr {
		r, _, _ := syscall.SyscallN(native(t, faultCode))
		return r
	}
	recovering := func() {
		add := windows.NewLazySystemDLL("kernel32.dll").NewProc("AddVectoredExceptionHandler")
		if r, _, err := add.Call(1, native(t, recoverCode)); r == 0 {
			t.Fatal(err)
		}
	}
	guard := func() {
		if err := installGuard(); err != nil {
			t.Fatal(err)
		}
	}
	switch name {
	case "recovers":
		guard()
		recovering()
		if r := fault(); r != 1 {
			t.Fatalf("the code went on and returned %d, want 1", r)
		}
		n, _, off := Recovered()
		if n != 1 {
			t.Fatalf("%d faults recovered, want 1", n)
		}
		fmt.Printf("recovered, at %#x\n", off)
	case "noguard":
		recovering()
		fault()
	case "nothandled":
		guard()
		fault()
	case "gonil":
		guard()
		recovering()
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("no panic from a nil dereference")
				}
			}()
			*noInt = 1
		}()
		if n, _, _ := Recovered(); n != 0 {
			t.Fatalf("%d faults of plugins, for Go's own", n)
		}
		fmt.Println("recovered, as a panic")
	default:
		t.Fatalf("no case %q", name)
	}
}

// noInt is a nil pointer, out of the sight of what checks for one, so writing through it is a real fault.
var noInt *int

const guardCaseEnv = "GUNIM_VST3_GUARD_CASE"

func TestGuardCase(t *testing.T) {
	name := os.Getenv(guardCaseEnv)
	if name == "" {
		t.Skip("run by TestTheGuard")
	}
	runGuardCase(t, name)
}

func TestTheGuard(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		dies   bool
		output string
	}{
		// A plugin's recovery goes on, and is counted.
		{"recovers", false, "recovered, at"},
		// What the guard is for: Go's last handler takes the same recovery for a crash.
		{"noguard", true, "Exception 0xc0000005"},
		// A plugin's fault no one catches is still a crash, reported, not a loop.
		{"nothandled", true, "Exception 0xc0000005"},
		// Go's own faults stay Go's: the plugin's handler passes, and it is a panic.
		{"gonil", false, "recovered, as a panic"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestGuardCase$", "-test.v", "-test.timeout=30s")
			cmd.Env = append(os.Environ(), guardCaseEnv+"="+c.name)
			out, err := cmd.CombinedOutput()
			if died := err != nil; died != c.dies {
				t.Fatalf("died: %v, want %v: %v\n%s", died, c.dies, err, out)
			}
			if !strings.Contains(string(out), c.output) {
				t.Fatalf("its output lacks %q:\n%s", c.output, out)
			}
		})
	}
}
