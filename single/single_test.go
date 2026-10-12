package single

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

// listen makes this the one running for dir, taking each handover with
// take, and returns what it took.
func listen(t *testing.T, dir string, take bool) (<-chan Handover, func()) {
	t.Helper()
	in, stop, err := Listen(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan Handover, 8)
	go func() {
		for h := range in {
			h.Take(take)
			got <- h
		}
	}()
	t.Cleanup(stop)
	return got, stop
}

// A copy started while one runs hands its command line over, and the
// one running hears it.
func TestACommandLineIsHandedOver(t *testing.T) {
	dir := t.TempDir()
	if ok, err := Hand(dir, Handover{Args: []string{"-ssh", "x"}}); ok || err != nil {
		t.Fatalf("with none running, the handover was taken %v, %v", ok, err)
	}
	got, _ := listen(t, dir, true)
	ok, err := Hand(dir, Handover{Args: []string{"-ssh", "x"}, Dir: "/srv"})
	if !ok || err != nil {
		t.Fatalf("the handover was taken %v, %v", ok, err)
	}
	select {
	case h := <-got:
		if !slices.Equal(h.Args, []string{"-ssh", "x"}) || h.Dir != "/srv" {
			t.Fatalf("the one running heard %+v", h)
		}
	case <-time.After(time.Second):
		t.Fatal("the one running heard nothing")
	}
	// Windows reports every writable file as rw-rw-rw-: there the file
	// is private by being in the user's own profile.
	if fi, err := os.Stat(filepath.Join(dir, File)); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the file is %v, %v", fi.Mode(), err)
	}
}

// A handover the one running does not take, as on its way out, is not
// said to be taken: the copy handing it over runs as the one.
func TestAHandoverNotTakenIsSaidSo(t *testing.T) {
	dir := t.TempDir()
	listen(t, dir, false)
	if ok, _ := Hand(dir, Handover{Args: []string{"-e", "x"}}); ok {
		t.Fatal("a handover not taken was said to be")
	}
}

// A handover without the token is not heard.
func TestAHandoverWithoutTheTokenIsRefused(t *testing.T) {
	dir := t.TempDir()
	got, _ := listen(t, dir, true)
	raw, _ := os.ReadFile(filepath.Join(dir, File))
	var r running
	_ = json.Unmarshal(raw, &r)
	conn, err := net.Dial("tcp", r.Addr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write([]byte(`{"token":"wrong","args":["-e","rm"]}` + "\n"))
	buf := make([]byte, 8)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	n, _ := conn.Read(buf)
	_ = conn.Close()
	if n != 0 {
		t.Fatalf("a wrong token was answered %q", buf[:n])
	}
	select {
	case h := <-got:
		t.Fatalf("a wrong token was heard: %+v", h)
	case <-time.After(100 * time.Millisecond):
	}
}

// The file is gone by the time stop returns, and a copy starting then
// runs as the one.
func TestTheFileGoesWithTheOneRunning(t *testing.T) {
	dir := t.TempDir()
	_, stop := listen(t, dir, true)
	stop()
	if _, err := os.Stat(filepath.Join(dir, File)); !os.IsNotExist(err) {
		t.Fatalf("stopped, the file is still there: %v", err)
	}
	if ok, _ := Hand(dir, Handover{}); ok {
		t.Fatal("a handover was taken with none running")
	}
}

// A file left by a copy that ended is not believed, whatever listens
// on its port now.
func TestAFileLeftBehindIsNotBelieved(t *testing.T) {
	dir := t.TempDir()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("ok\n"))
			_ = c.Close()
		}
	}()
	raw, _ := json.Marshal(running{Addr: l.Addr().String(), Token: "x", PID: 1 << 30})
	if err := os.WriteFile(filepath.Join(dir, File), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, _ := Hand(dir, Handover{Args: []string{"-e", "secret"}}); ok {
		t.Fatal("a handover went to whatever holds the port of a copy that ended")
	}
}
