//go:build linux && (arm64 || amd64)

package kms

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// fakeConsole is the kernel's VTs, as far as the VT code asks: which shows,
// each one's mode, keyboard and switching, which exist, and the calls made.
type fakeConsole struct {
	active    int
	mode      map[int]int32 // KD_TEXT or KD_GRAPHICS
	kb        map[int]int32
	process   map[int]bool // VT_PROCESS
	allocated map[int]bool
	// fds is the VT each open file is, 0 for /dev/tty0.
	fds   map[int]int
	next  int
	calls []string
	// noTTY0 refuses /dev/tty0, as for a user outside the group tty.
	noTTY0 bool
}

func newFakeConsole(active int) *fakeConsole {
	return &fakeConsole{
		active: active, mode: map[int]int32{}, kb: map[int]int32{}, process: map[int]bool{},
		allocated: map[int]bool{1: true, active: true}, fds: map[int]int{}, next: 100,
	}
}

func (c *fakeConsole) open(n int) (int, error) {
	if n == 0 && c.noTTY0 {
		return -1, unix.EACCES
	}
	c.next++
	c.fds[c.next] = n
	if n > 0 {
		c.allocated[n] = true
		if _, ok := c.kb[n]; !ok {
			c.kb[n] = kUnicode
		}
	}
	return c.next, nil
}

func (c *fakeConsole) close(fd int) error {
	if _, ok := c.fds[fd]; !ok {
		return unix.EBADF
	}
	delete(c.fds, fd)
	return nil
}

func (c *fakeConsole) ioctl(fd int, req uintptr, arg unsafe.Pointer) error {
	n, ok := c.fds[fd]
	if !ok {
		return unix.EBADF
	}
	switch req {
	case vtGetState:
		(*vtStat)(arg).active = uint16(c.active)
	case vtOpenQry:
		for i := 1; ; i++ {
			if !c.allocated[i] {
				*(*int32)(arg) = int32(i)
				return nil
			}
		}
	case kdGetMode:
		*(*int32)(arg) = c.mode[n]
	case kdGKBMode:
		*(*int32)(arg) = c.kb[n]
	case vtSetMode:
		c.process[n] = (*vtMode)(arg).mode == vtProcess
		c.calls = append(c.calls, fmt.Sprintf("%d: switching %v", n, c.process[n]))
	default:
		return unix.ENOTTY
	}
	return nil
}

func (c *fakeConsole) ioctlInt(fd int, req, arg uintptr) error {
	n, ok := c.fds[fd]
	if !ok {
		return unix.EBADF
	}
	switch req {
	case kdSetMode:
		c.mode[n] = int32(arg)
		c.calls = append(c.calls, fmt.Sprintf("%d: mode %d", n, arg))
	case kdSKBMode:
		c.kb[n] = int32(arg)
		c.calls = append(c.calls, fmt.Sprintf("%d: keyboard %d", n, arg))
	case vtActivate:
		c.active = int(arg)
		c.calls = append(c.calls, fmt.Sprintf("show %d", arg))
	case vtDisallocate:
		if c.active == int(arg) {
			return unix.EBUSY
		}
		delete(c.allocated, int(arg))
		c.calls = append(c.calls, fmt.Sprintf("give back %d", arg))
	case vtRelDisp:
	default:
		return unix.ENOTTY
	}
	return nil
}

// A free VT is taken in graphics mode with its keyboard off, and given back
// as found: text mode, its keyboard, the VT before showing, and the VT
// given back to the kernel.
func TestAFreeVTIsTakenAndGivenBack(t *testing.T) {
	c := newFakeConsole(1)
	v, err := takeVT(c, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.num != 2 || v.prev != 1 || !v.opened || c.active != 2 {
		t.Fatalf("took VT %d from %d (opened %v), showing %d", v.num, v.prev, v.opened, c.active)
	}
	if c.mode[2] != kdGraphics || c.kb[2] != kOff || !c.process[2] {
		t.Fatalf("VT 2: mode %d, keyboard %d, switching %v", c.mode[2], c.kb[2], c.process[2])
	}
	c.calls = nil
	if err := v.close(); err != nil {
		t.Fatal(err)
	}
	want := []string{"2: switching false", "2: mode 0", "2: keyboard 3", "show 1", "give back 2"}
	if !slices.Equal(c.calls, want) {
		t.Fatalf("put back with %v, want %v", c.calls, want)
	}
	if len(c.fds) != 0 {
		t.Fatalf("files left open: %v", c.fds)
	}
}

// A VT a crashed run left in graphics mode with its keyboard off is put
// right first, so a clean exit gives it back working, not as the crash
// left it.
func TestAVTLeftBrokenIsPutRightFirst(t *testing.T) {
	c := newFakeConsole(1)
	c.mode[1], c.kb[1] = kdGraphics, kOff
	v, err := takeVT(c, 1, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.kbMode != kUnicode {
		t.Fatalf("took keyboard mode %d as how it was, want K_UNICODE", v.kbMode)
	}
	if err := v.close(); err != nil {
		t.Fatal(err)
	}
	if c.mode[1] != kdText || c.kb[1] != kUnicode || c.process[1] {
		t.Fatalf("VT 1 left in mode %d, keyboard %d, switching %v", c.mode[1], c.kb[1], c.process[1])
	}
}

// A run that crashed on a free VT left it showing; the next run, from its
// state file, puts it right, shows the VT before it, and gives it back, so
// the run after takes a fresh VT and returns to the right one.
func TestACrashedRunsVTIsRepairedFromItsState(t *testing.T) {
	c := newFakeConsole(1)
	crashed, err := takeVT(c, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := state{VT: crashed.num, Prev: crashed.prev, Opened: crashed.opened}
	// The process dies: the kernel closes its files, and nothing else.
	for fd := range c.fds {
		_ = c.close(fd)
	}
	if err := repairVT(c, s); err != nil {
		t.Fatal(err)
	}
	if c.active != 1 || c.allocated[2] || c.mode[2] != kdText || c.process[2] {
		t.Fatalf("after the repair: showing %d, VT 2 allocated %v, mode %d", c.active, c.allocated[2], c.mode[2])
	}
	next, err := takeVT(c, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.prev != 1 {
		t.Fatalf("the next run returns to VT %d, want 1", next.prev)
	}
	if err := next.close(); err != nil || c.active != 1 {
		t.Fatalf("the next run left VT %d showing: %v", c.active, err)
	}
}

// An ordinary user, as a service's, opens no /dev/tty0: the driver asks
// its own VT which shows, and takes it, and only a free VT is out of reach.
func TestAnOrdinaryUserTakesItsOwnVT(t *testing.T) {
	c := newFakeConsole(1)
	c.noTTY0 = true
	v, err := takeVT(c, 1, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.prev != 0 || c.mode[1] != kdGraphics || c.kb[1] != kOff {
		t.Fatalf("took VT 1 from %d, mode %d, keyboard %d", v.prev, c.mode[1], c.kb[1])
	}
	if err := v.close(); err != nil {
		t.Fatal(err)
	}
	// Another VT, from the one showing: it switches, and back on exit.
	v, err = takeVT(c, 3, nil, nil)
	if err != nil || c.active != 3 {
		t.Fatalf("VT 3: showing %d, %v", c.active, err)
	}
	if err := v.close(); err != nil || c.active != 1 {
		t.Fatalf("after VT 3: showing %d, %v", c.active, err)
	}
	if _, err := takeVT(c, 0, nil, nil); err == nil || !strings.Contains(err.Error(), "/dev/tty0") {
		t.Fatalf("a free VT without /dev/tty0: %v", err)
	}
}

// A run started again on the VT of a run that crashed takes it as it is:
// it keeps that run's VT before, and switches to it only where it shows. A
// VT that someone switched away from is taken away, to wait for them.
func TestARestartTakesTheCrashedRunsVTAsItIs(t *testing.T) {
	c := newFakeConsole(1)
	crashed, err := takeVT(c, 3, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	left := state{VT: crashed.num, Prev: crashed.prev}
	for fd := range c.fds {
		_ = c.close(fd)
	}
	// Someone switches to the login on VT 2 as the run crashes.
	c.active = 2
	c.calls = nil
	v, err := takeVT(c, 3, &left, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !v.reused || !v.away || v.prev != 1 || c.active != 2 {
		t.Fatalf("reused %v, away %v, from %d, showing %d; want reused, away, from 1, and VT 2 still showing", v.reused, v.away, v.prev, c.active)
	}
	for _, call := range c.calls {
		if strings.HasPrefix(call, "show") {
			t.Fatalf("the restart switched VT: %v", c.calls)
		}
	}
	if c.mode[3] != kdGraphics || !c.process[3] {
		t.Fatalf("VT 3: mode %d, switching %v", c.mode[3], c.process[3])
	}
	// The VT showing as the run restarts is taken, not away.
	_ = v.close()
	c.active = 3
	left = state{VT: 3, Prev: 1}
	if v, err = takeVT(c, 3, &left, nil); err != nil || v.away || v.prev != 1 {
		t.Fatalf("away %v, from %d, %v", v.away, v.prev, err)
	}
	if err := v.close(); err != nil || c.active != 1 {
		t.Fatalf("the restart's exit left VT %d showing, %v; want the first run's VT before, 1", c.active, err)
	}
}

// A free VT a crashed run opened is taken again, and the VT before it kept;
// any other VT asked for repairs the crashed run's.
func TestWhichVTReusesACrashedRunsFreeVT(t *testing.T) {
	c := newFakeConsole(1)
	c.mode[4] = kdGraphics
	was := consoles
	consoles = c
	t.Cleanup(func() { consoles = was })
	left := state{VT: 4, Prev: 1, Opened: true}
	if n, take, err := whichVT("free", &left); err != nil || !take || n != 4 {
		t.Fatalf("free after a crash on a free VT: %d %v %v", n, take, err)
	}
	// Someone logged in on it since: it is in text mode, and theirs.
	c.mode[4] = kdText
	if n, take, err := whichVT("free", &left); err != nil || !take || n != 0 {
		t.Fatalf("free after the crashed run's VT was taken again: %d %v %v; want a new free VT", n, take, err)
	}
	if n, take, err := whichVT("free", nil); err != nil || !take || n != 0 {
		t.Fatalf("free: %d %v %v", n, take, err)
	}
	if n, _, err := whichVT("/dev/tty6", &left); err != nil || n != 6 {
		t.Fatalf("/dev/tty6: %d %v", n, err)
	}
	if _, _, err := whichVT("64", nil); err == nil {
		t.Fatal("VT 64 taken")
	}
}

func TestStateFilesRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "gunim-kms.json")
	was := statePath
	statePath = func() string { return p }
	t.Cleanup(func() { statePath = was })
	sf, _, found, err := lockState()
	if err != nil || found {
		t.Fatalf("a fresh state file: found %v, %v", found, err)
	}
	want := state{VT: 2, Prev: 1, Opened: true, Backlight: "/sys/class/backlight/x", Level: 15}
	if err := sf.write(want); err != nil {
		t.Fatal(err)
	}
	// The run dies: the kernel lets go of its lock, and the file stays.
	sf.keep()
	sf, left, found, err := lockState()
	if err != nil || !found || left != want {
		t.Fatalf("the next run found %+v (%v), %v", left, found, err)
	}
	sf.done()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("the state file stayed once put back")
	}
}

// A second start while a run holds the screen leaves that run's state
// alone: the file is locked, and nothing is repaired or written.
func TestALiveRunsStateIsLeftAlone(t *testing.T) {
	p := filepath.Join(t.TempDir(), "gunim-kms.json")
	was := statePath
	statePath = func() string { return p }
	t.Cleanup(func() { statePath = was })
	live, _, _, err := lockState()
	if err != nil {
		t.Fatal(err)
	}
	defer live.done()
	want := state{VT: 2, Prev: 1}
	if err := live.write(want); err != nil {
		t.Fatal(err)
	}
	second, _, found, err := lockState()
	if !errors.Is(err, errStateHeld) || second != nil || found {
		t.Fatalf("a second start got %v, %v, found %v", second, err, found)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"vt":2`) {
		t.Fatalf("the live run's state became %q", b)
	}
}

// With no runtime directory there is no state file, and a run goes on.
func TestNoPlaceForAStateFile(t *testing.T) {
	was := statePath
	statePath = func() string { return "" }
	t.Cleanup(func() { statePath = was })
	sf, _, found, err := lockState()
	if sf != nil || found || err != nil {
		t.Fatalf("got %v, found %v, %v", sf, found, err)
	}
	if err := sf.write(state{VT: 2}); err != nil {
		t.Fatal(err)
	}
	sf.done()
	sf.keep()
}

// systemd's RuntimeDirectory= gives a service that is not root a place.
func TestAServiceKeepsItsStateInItsRuntimeDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root keeps its state under /run")
	}
	t.Setenv("RUNTIME_DIRECTORY", "/run/hjortron-touch:/run/other")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if got := statePath(); got != "/run/hjortron-touch/gunim-kms.json" {
		t.Fatalf("state at %s", got)
	}
}

// The state is written before the VT switches, so a run that dies on the
// way leaves it for the next.
func TestTheStateIsWrittenBeforeTheSwitch(t *testing.T) {
	c := newFakeConsole(1)
	var seen []string
	v, err := takeVT(c, 0, nil, func(v *vt) error {
		seen = append(seen, fmt.Sprintf("VT %d from %d, showing %d", v.num, v.prev, c.active))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = v.close() }()
	if want := []string{"VT 2 from 1, showing 1"}; !slices.Equal(seen, want) {
		t.Fatalf("before the switch: %v, want %v", seen, want)
	}
}

// A backlight found dark is no level to go back to: a run before may have
// blanked the screen and crashed. It goes back to half way instead.
func TestADarkBacklightIsNotKeptAsFound(t *testing.T) {
	dir := t.TempDir()
	was := sysBacklight
	sysBacklight = dir
	t.Cleanup(func() { sysBacklight = was })
	bl := filepath.Join(dir, "panel")
	if err := os.Mkdir(bl, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{"max_brightness": "31", "brightness": "0"} {
		if err := os.WriteFile(filepath.Join(bl, name), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := openBacklight("")
	if err != nil {
		t.Fatal(err)
	}
	if b.was != 16 {
		t.Fatalf("goes back to %d, want 16", b.was)
	}
	if err := b.restore(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(bl, "brightness")); string(got) != "16" {
		t.Fatalf("put back %q", got)
	}
}

// A free VT a crashed run left, and someone has since logged in on, is in
// text mode again: the next run's repair leaves it alone, and switches no
// one away from it.
func TestARepairLeavesAFreeVTInUseAlone(t *testing.T) {
	c := newFakeConsole(4)
	c.mode[4] = kdText
	c.allocated[4] = true
	if err := repairVT(c, state{VT: 4, Prev: 1, Opened: true}); err != nil {
		t.Fatal(err)
	}
	if c.active != 4 || !c.allocated[4] || len(c.calls) != 0 {
		t.Fatalf("showing %d, VT 4 allocated %v, calls %v; want VT 4 left alone", c.active, c.allocated[4], c.calls)
	}
}

// On exit the driver switches back to the VT before only from its own: one
// someone switched to meanwhile stays on the screen, and a free VT stays
// allocated, as no switch made it safe to give back.
func TestExitSwitchesBackOnlyFromItsOwnVT(t *testing.T) {
	c := newFakeConsole(1)
	v, err := takeVT(c, 0, nil, nil)
	if err != nil || c.active != 2 {
		t.Fatalf("took VT %d, showing %d, %v", v.num, c.active, err)
	}
	c.active = 5 // Ctrl+Alt+F5, or chvt 5
	c.calls = nil
	if err := v.close(); err != nil {
		t.Fatal(err)
	}
	for _, call := range c.calls {
		if strings.HasPrefix(call, "show") || strings.HasPrefix(call, "give back") {
			t.Fatalf("closed with %v, want no switch and no give back", c.calls)
		}
	}
	if c.active != 5 {
		t.Fatalf("showing %d, want 5", c.active)
	}
}

// A backlight the user may not write is no backlight: the shade dims alone,
// and blanking leaves the backlight lit as it was.
func TestABacklightThatCannotBeWrittenIsNone(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes any file")
	}
	dir := t.TempDir()
	bl := filepath.Join(dir, "panel")
	if err := os.Mkdir(bl, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{"max_brightness": "31", "brightness": "15"} {
		if err := os.WriteFile(filepath.Join(bl, name), []byte(v), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	was := sysBacklight
	sysBacklight = dir
	t.Cleanup(func() { sysBacklight = was })
	b, err := openBacklight("")
	if err != nil || b == nil {
		t.Fatalf("%v, %v", b, err)
	}
	if usableBacklight(b) != nil {
		t.Fatal("a read-only backlight is used")
	}
	if err := os.Chmod(filepath.Join(bl, "brightness"), 0o644); err != nil {
		t.Fatal(err)
	}
	if usableBacklight(b) != b {
		t.Fatal("a writable backlight is not used")
	}
}
