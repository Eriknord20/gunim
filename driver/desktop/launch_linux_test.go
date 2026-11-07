//go:build linux

package desktop

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// fakePortal answers OpenURI on a bus of its own: with a Response of code, or none
// where code is negative, after delay, and failing outright where refuse is set.
type fakePortal struct {
	conn   *dbus.Conn
	code   int
	delay  time.Duration
	reply  time.Duration
	refuse bool
}

func (f *fakePortal) OpenURI(_, _ string, _ map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	const handle = dbus.ObjectPath("/org/freedesktop/portal/desktop/request/test/1")
	if f.refuse {
		return "", dbus.MakeFailedError(errors.New("no such portal"))
	}
	time.Sleep(f.reply)
	if f.code >= 0 {
		respond := func() {
			_ = f.conn.Emit(handle, "org.freedesktop.portal.Request.Response", uint32(f.code), map[string]dbus.Variant{})
		}
		if f.delay > 0 {
			time.AfterFunc(f.delay, respond)
		} else {
			// As soon as it can: this may reach the caller before the method's reply.
			respond()
		}
	}
	return handle, nil
}

// portalBus starts a private session bus, with fake on it as the portal, and
// returns a way to dial it.
func portalBus(t *testing.T, fake *fakePortal) func() (*dbus.Conn, error) {
	t.Helper()
	exe, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("no dbus-daemon here")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, exe, "--session", "--nofork", "--print-address")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})
	addr, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	dial := func() (*dbus.Conn, error) { return dbus.Connect(strings.TrimSpace(addr)) }
	srv, err := dial()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	fake.conn = srv
	if err := srv.Export(fake, "/org/freedesktop/portal/desktop", "org.freedesktop.portal.OpenURI"); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.RequestName("org.freedesktop.portal.Desktop", dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	return dial
}

// ask runs a request of fake, with a wait, hold and what it does with a late
// failure as given, and returns its outcome.
func ask(dial func() (*dbus.Conn, error), wait, hold time.Duration, release func(), late func(error)) error {
	p := portalAsk{dial: dial, wait: wait, hold: hold, release: release, late: late}
	return p.run("OpenURI", "", "https://example.com/", map[string]dbus.Variant{})
}

// What the portal says at once comes back to the caller.
func TestThePortalsAnswerComesBack(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fake    fakePortal
		failure bool
	}{
		{"done", fakePortal{code: 0}, false},
		{"cancelled by the person", fakePortal{code: 1}, false},
		{"failed", fakePortal{code: 2}, true},
		{"refused the call", fakePortal{refuse: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := tc.fake
			released := make(chan struct{})
			// A wait no machine reaches: this is about the answer, not the time.
			err := ask(portalBus(t, &fake), time.Minute, time.Minute, func() { close(released) }, func(error) { t.Error("heard late") })
			if (err != nil) != tc.failure {
				t.Errorf("got %v", err)
			}
			<-released
		})
	}
}

// No bus to reach is a failure, and still lets go of what was sent.
func TestNoBusIsAFailure(t *testing.T) {
	released := false
	err := ask(func() (*dbus.Conn, error) { return nil, errors.New("no bus") }, time.Minute, time.Minute, func() { released = true }, func(error) {})
	if err == nil || !released {
		t.Errorf("got %v, released %v", err, released)
	}
}

// A portal that answers late, or takes its time over the call, doesn't hold up
// the caller, and a late failure is still heard.
func TestASlowPortalHoldsNoOneUp(t *testing.T) {
	for name, fake := range map[string]fakePortal{
		"slow answer": {code: 2, delay: 300 * time.Millisecond},
		"slow call":   {code: 2, reply: 300 * time.Millisecond},
	} {
		t.Run(name, func(t *testing.T) {
			f := fake
			late := make(chan error, 1)
			if err := ask(portalBus(t, &f), time.Millisecond, time.Minute, nil, func(err error) { late <- err }); err != nil {
				t.Errorf("the caller heard %v", err)
			}
			select {
			case err := <-late:
				if err == nil {
					t.Error("the late failure came with no word")
				}
			case <-time.After(30 * time.Second):
				t.Error("the late failure went unheard")
			}
		})
	}
}

// While the person chooses a program, the connection stays: the portal drops a
// request and its dialog once the connection goes.
func TestTheConnectionStaysWhileThePortalAsks(t *testing.T) {
	var conn *dbus.Conn
	bus := portalBus(t, &fakePortal{code: -1})
	dialed := make(chan struct{})
	dial := func() (*dbus.Conn, error) {
		c, err := bus()
		conn = c
		close(dialed)
		return c, err
	}
	hold := 500 * time.Millisecond
	if err := ask(dial, time.Millisecond, hold, nil, func(error) { t.Error("no answer counted as failing") }); err != nil {
		t.Fatal(err)
	}
	<-dialed
	if !conn.Connected() {
		t.Fatal("the connection went as the caller stopped waiting")
	}
	for end := time.Now().Add(30 * time.Second); conn.Connected(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the connection outstayed its hold")
		}
	}
}

// fakeXDGOpen puts an xdg-open first on the path, which exits with code and
// writes its argument to the file it returns the name of.
func fakeXDGOpen(t *testing.T, code string) string {
	t.Helper()
	dir := t.TempDir()
	got := filepath.Join(dir, "got")
	script := "#!/bin/sh\nprintf %s \"$1\" > \"" + got + "\"\nexit " + code + "\n"
	if err := os.WriteFile(filepath.Join(dir, "xdg-open"), []byte(script), 0o755); err != nil { //nolint:gosec // it must run
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return got
}

// With no portal, as on a plain window manager, xdg-open opens it.
func TestWithNoPortalXDGOpenOpensIt(t *testing.T) {
	got := fakeXDGOpen(t, "0")
	if err := orXDGOpen("song.hej", "/music", time.Minute, errors.New("no portal")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(got); string(b) != "/music" {
		t.Errorf("xdg-open was given %q", b)
	}
	if err := orXDGOpen("song.hej", "/elsewhere", time.Minute, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(got); string(b) != "/music" {
		t.Errorf("xdg-open ran though the portal worked: %q", b)
	}
}

// With neither, the error says what the portal said.
func TestWithNeitherThePortalsWordIsKept(t *testing.T) {
	why := errors.New("no portal")
	fakeXDGOpen(t, "3")
	if err := orXDGOpen("song.hej", "/music", time.Minute, why); !errors.Is(err, why) {
		t.Errorf("xdg-open failing gave %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	if err := orXDGOpen("song.hej", "/music", time.Minute, why); !errors.Is(err, why) {
		t.Errorf("no xdg-open gave %v", err)
	}
}

// A file shows through its folder, and a folder as itself.
func TestFolderOf(t *testing.T) {
	dir := t.TempDir()
	if got := folderOf(dir); got != dir {
		t.Errorf("a folder gave %q", got)
	}
	if got := folderOf(filepath.Join(dir, "song.hej")); got != dir {
		t.Errorf("a file gave %q", got)
	}
}
