package single

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// runningOf reads what the file of the one running for dir says.
func runningOf(t *testing.T, dir string) running {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, File))
	if err != nil {
		t.Fatal(err)
	}
	var r running
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// dialOne connects to the one running for dir, as any local program
// can, and closes the connection when the test ends.
func dialOne(t *testing.T, dir string) net.Conn {
	t.Helper()
	conn, err := dial(runningOf(t, dir).Addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// dial connects to addr, for a test that closes the connection itself.
func dial(addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: time.Second}
	return d.DialContext(context.Background(), "tcp", addr)
}

// flood writes first and then bytes with no newline to conn, until the
// one running drops it or until the time given.
func flood(conn net.Conn, first []byte, until time.Time) {
	chunk := bytes.Repeat([]byte("a"), 64<<10)
	_ = conn.SetWriteDeadline(until)
	if _, err := conn.Write(first); err != nil {
		return
	}
	for {
		if _, err := conn.Write(chunk); err != nil {
			return
		}
	}
}

// dropped waits up to within for the one running to close conn, and
// reports whether it did.
func dropped(conn net.Conn, within time.Duration) bool {
	_ = conn.SetReadDeadline(time.Now().Add(within))
	buf := make([]byte, 64)
	for {
		_, err := conn.Read(buf)
		if err == nil {
			continue
		}
		var ne net.Error
		return !errors.As(err, &ne) || !ne.Timeout()
	}
}

// heapGrowth runs f and returns how far the heap in use grew, at its
// peak, above where it was before.
func heapGrowth(f func()) uint64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base := ms.HeapInuse
	var peak atomic.Uint64
	done := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		var ms runtime.MemStats
		for {
			runtime.ReadMemStats(&ms)
			if ms.HeapInuse > peak.Load() {
				peak.Store(ms.HeapInuse)
			}
			select {
			case <-done:
				return
			case <-time.After(2 * time.Millisecond):
			}
		}
	}()
	f()
	close(done)
	<-sampled
	return peak.Load() - min(base, peak.Load())
}

// setTimes sets proveTime and readTime for one test.
func setTimes(t *testing.T, prove, read time.Duration) {
	wasProve, wasRead := proveTime, readTime
	proveTime, readTime = prove, read
	t.Cleanup(func() { proveTime, readTime = wasProve, wasRead })
}

// A line longer than maxLine, after the token, is dropped as soon as it
// passes the limit, not read to its end.
func TestAnOverLongLineIsDropped(t *testing.T) {
	dir := t.TempDir()
	startOne(t, dir, true)
	conn := dialOne(t, dir)
	go flood(conn, []byte(`{"token":"`+runningOf(t, dir).Token+`","args":["`), time.Now().Add(10*time.Second))
	if !dropped(conn, readTime/2) {
		t.Fatalf("a line longer than %d bytes was still read after %v", maxLine, readTime/2)
	}
}

// Linux's default 2 MiB of arguments is handed over even when every
// byte is one that JSON escapes to six.
func TestALongEscapedHandoverIsTaken(t *testing.T) {
	dir := t.TempDir()
	got, _ := startOne(t, dir, true)
	args := make([]string, 2048)
	for i := range args {
		args[i] = strings.Repeat("&", 1024)
	}
	if ok, err := Hand(dir, Handover{Args: args}); !ok || err != nil {
		t.Fatalf("the handover was taken %v, %v", ok, err)
	}
	if h := <-got; !slices.Equal(h.Args, args) {
		t.Fatal("the one running heard other arguments")
	}
}

// A handover longer than maxLine is not sent, and Hand says why.
func TestATooLongHandoverIsRefusedPlainly(t *testing.T) {
	dir := t.TempDir()
	got, _ := startOne(t, dir, true)
	ok, err := Hand(dir, Handover{Args: []string{strings.Repeat("x", maxLine)}})
	if ok || err == nil || !strings.Contains(err.Error(), "bytes encoded") {
		t.Fatalf("a handover over maxLine was taken %v, %v", ok, err)
	}
	select {
	case h := <-got:
		t.Fatalf("the one running heard %d arguments", len(h.Args))
	case <-time.After(100 * time.Millisecond):
	}
}

// A connection that does not start with the token is dropped as soon
// as its start is read, without waiting for the rest of its line.
func TestAWrongStartIsDroppedAtOnce(t *testing.T) {
	setTimes(t, 2*time.Second, 2*time.Second)
	dir := t.TempDir()
	startOne(t, dir, true)
	conn := dialOne(t, dir)
	if _, err := conn.Write([]byte(`{"args":["` + strings.Repeat("x", 100))); err != nil {
		t.Fatal(err)
	}
	if !dropped(conn, time.Second) {
		t.Fatal("a line without the token was still read after 1s")
	}
}

// Lines without the token are dropped before they are decoded, so many
// of them hold almost no memory.
func TestATokenlessLineIsNotDecoded(t *testing.T) {
	dir := t.TempDir()
	startOne(t, dir, true)
	// A line of empty strings decodes into many times its own size.
	line := []byte(`{"args":[""` + strings.Repeat(`,""`, (1<<20)/3) + "]}\n")
	const conns = 16
	addr := runningOf(t, dir).Addr
	until := time.Now().Add(2 * time.Second)
	grew := heapGrowth(func() {
		var wg sync.WaitGroup
		for range conns {
			wg.Go(func() {
				for time.Now().Before(until) {
					conn, err := dial(addr)
					if err != nil {
						continue
					}
					_ = conn.SetDeadline(until)
					if _, err := conn.Write(line); err == nil {
						dropped(conn, time.Until(until))
					}
					_ = conn.Close()
				}
			})
		}
		wg.Wait()
	})
	const bound = 32 << 20
	if grew > bound {
		t.Errorf("%d connections sending lines without the token grew the heap by %d MiB, over %d MiB",
			conns, grew>>20, bound>>20)
	}
}

// A connection that sends nothing, or a byte at a time, is dropped
// after proveTime, and one that has shown the token after readTime.
func TestASlowSenderIsDropped(t *testing.T) {
	setTimes(t, 200*time.Millisecond, 2*time.Second)
	dir := t.TempDir()
	startOne(t, dir, true)
	trickle := func(conn net.Conn) {
		for {
			if _, err := conn.Write([]byte("a")); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	silent := dialOne(t, dir)
	slow := dialOne(t, dir)
	go trickle(slow)
	for name, conn := range map[string]net.Conn{"silent": silent, "slow": slow} {
		if !dropped(conn, time.Second) {
			t.Errorf("the %s connection was not dropped after %v", name, proveTime)
		}
	}
	shown := dialOne(t, dir)
	if _, err := shown.Write([]byte(`{"token":"` + runningOf(t, dir).Token + `"`)); err != nil {
		t.Fatal(err)
	}
	go trickle(shown)
	start := time.Now()
	if !dropped(shown, readTime+time.Second) || time.Since(start) < readTime/2 {
		t.Errorf("a slow sender with the token was dropped after %v, not %v", time.Since(start), readTime)
	}
}

// Connections that hold the one running without the token do not keep
// a real handover out.
func TestIdleConnectionsDoNotBlockAHandover(t *testing.T) {
	dir := t.TempDir()
	got, _ := startOne(t, dir, true)
	for range 2 * maxProving {
		dialOne(t, dir)
	}
	addr := runningOf(t, dir).Addr
	stopDialing := make(chan struct{})
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for {
				select {
				case <-stopDialing:
					return
				default:
				}
				conn, err := dial(addr)
				if err != nil {
					continue
				}
				dropped(conn, 5*time.Second)
				_ = conn.Close()
			}
		})
	}
	defer func() {
		close(stopDialing)
		wg.Wait()
	}()
	for i := range 3 {
		start := time.Now()
		ok, err := Hand(dir, Handover{Args: []string{"-e", "x"}})
		if took := time.Since(start); !ok || err != nil || took > proveTime/2 {
			t.Fatalf("handover %d was taken %v, %v, after %v", i, ok, err, took)
		}
		<-got
	}
}

// Many connections that flood the one running without the token hold
// almost no memory, and a handover after them is still taken.
func TestManyConnectionsKeepMemoryBounded(t *testing.T) {
	dir := t.TempDir()
	got, _ := startOne(t, dir, true)
	const conns = 64
	until := time.Now().Add(2 * time.Second)
	grew := heapGrowth(func() {
		var wg sync.WaitGroup
		for range conns {
			conn := dialOne(t, dir)
			wg.Go(func() { flood(conn, nil, until) })
		}
		wg.Wait()
	})
	const bound = 16 << 20
	if grew > bound {
		t.Errorf("%d connections grew the heap by %d MiB, over %d MiB", conns, grew>>20, bound>>20)
	}
	if ok, err := Hand(dir, Handover{Args: []string{"-e", "x"}}); !ok || err != nil {
		t.Fatalf("after the flood, the handover was taken %v, %v", ok, err)
	}
	<-got
}
