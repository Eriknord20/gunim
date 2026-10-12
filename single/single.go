// Package single keeps one copy of a program running for a user: a copy
// started while another runs hands its command line to that one, which
// opens a window for it, and ends. One process then holds the program's
// windows, its files and whatever it runs beside it, as a backend, and an
// update replaces the one copy running, not one of several.
//
// The one running listens on the loopback address, on a port it writes
// in a file only the user can read, with a random token that a
// handover has to carry. Nothing else on the machine can open windows
// through it without reading that file.
package single

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// File is where the one running says where it listens, in the
// program's own directory, which the caller passes as dir.
const File = "running.json"

// Handover is a command line handed to the one running: the arguments,
// and the folder it was started in. Take says whether the one running
// took it: one on its way out does not, and the copy handing it over
// then runs as the one.
type Handover struct {
	Args []string   `json:"args"`
	Dir  string     `json:"dir"`
	Take func(bool) `json:"-"`
}

// wire is what a handover sends: the handover, and the token.
type wire struct {
	Token string `json:"token"`
	Handover
}

// running is what the file says.
type running struct {
	Addr  string `json:"addr"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

// Running reports whether a copy of the program is running for dir.
func Running(dir string) bool {
	raw, err := os.ReadFile(filepath.Join(dir, File))
	if err != nil {
		return false
	}
	var r running
	return json.Unmarshal(raw, &r) == nil && alive(r.PID)
}

// handTime is how long a handover waits for the one running to answer.
const handTime = 3 * time.Second

// Hand hands h to the copy running for dir, and reports whether one
// took it. With none running, or one that does not answer, it reports
// false, and the caller runs as the one.
func Hand(dir string, h Handover) (bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, File))
	if err != nil {
		return false, nil
	}
	var r running
	if json.Unmarshal(raw, &r) != nil || r.Addr == "" || !alive(r.PID) {
		// Left by a copy that ended without taking it with it: whatever
		// listens on that port now is not the program.
		return false, nil
	}
	letToFront(r.PID)
	conn, err := net.DialTimeout("tcp", r.Addr, handTime)
	if err != nil {
		// Gone without taking the file with it.
		return false, nil
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(handTime))
	msg, err := json.Marshal(wire{Token: r.Token, Handover: h})
	if err != nil {
		return false, err
	}
	if _, err := conn.Write(append(msg, '\n')); err != nil {
		return false, nil
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || line != "ok\n" {
		return false, fmt.Errorf("the copy already running did not take the command line: %q", line)
	}
	return true, nil
}

// Listen makes this the copy running for dir, and sends what later
// ones hand it on the channel, each to be answered with Take, until
// stop is called or ctx ends. stop takes the file away, unless another
// has written it since, before it returns.
func Listen(ctx context.Context, dir string) (handovers <-chan Handover, stop func(), err error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, nil, err
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	r := running{Addr: l.Addr().String(), Token: hex.EncodeToString(token), PID: os.Getpid()}
	raw, err := json.Marshal(r)
	if err != nil {
		_ = l.Close()
		return nil, nil, err
	}
	path := filepath.Join(dir, File)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		_ = l.Close()
		return nil, nil, err
	}
	// Written whole, then moved into place, so a copy starting reads
	// all of it or none.
	part := path + ".part"
	if err := os.WriteFile(part, raw, 0o600); err != nil {
		_ = l.Close()
		return nil, nil, err
	}
	if err := os.Rename(part, path); err != nil {
		_ = l.Close()
		return nil, nil, err
	}
	out := make(chan Handover)
	ctx, cancel := context.WithCancel(ctx)
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			_ = l.Close()
			if now, err := os.ReadFile(path); err == nil && string(now) == string(raw) {
				_ = os.Remove(path)
			}
		})
	}
	go func() {
		<-ctx.Done()
		stop()
	}()
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				continue
			}
			go serve(ctx, conn, r.Token, out)
		}
	}()
	return out, stop, nil
}

// serve takes one handover, carrying the token, and answers it.
func serve(ctx context.Context, conn net.Conn, token string, out chan<- Handover) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(handTime))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var w wire
	if json.Unmarshal(line, &w) != nil || subtle.ConstantTimeCompare([]byte(w.Token), []byte(token)) != 1 {
		return
	}
	// Answered once the one running has taken it, or not: never before.
	taken := make(chan bool, 1)
	h := w.Handover
	h.Take = func(ok bool) { taken <- ok }
	select {
	case out <- h:
	case <-ctx.Done():
		return
	}
	select {
	case ok := <-taken:
		if ok {
			_, _ = conn.Write([]byte("ok\n"))
		}
	case <-ctx.Done():
	}
}
