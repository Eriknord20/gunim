package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

// A main thread that stops answering freezes every window of the
// program, and the system says it is not responding. The watch asks the
// main thread to run a task now and then; one not run within hangAfter
// has the program write where each of its goroutines is to a file in the
// temporary folder, and to stderr, once a hang: the main thread's stack
// says what holds it.

// The watch's pace: how often it asks, and how long a task may wait.
const (
	hangEvery = 2 * time.Second
	hangAfter = 10 * time.Second
)

// watchHang watches d's main thread until stop is called, asking it to run a task every so often and telling of a
// hang once a task has waited after. Each watch keeps its own pace, so a test's quick watch leaves a driver's alone.
func watchHang(d *Driver, every, after time.Duration) (stop func()) {
	var ran atomic.Int64
	ran.Store(time.Now().UnixNano())
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		told := false
		for {
			select {
			case <-done:
				return
			case <-t.C:
			}
			if !d.post(func() { ran.Store(time.Now().UnixNano()) }) {
				return
			}
			late := time.Since(time.Unix(0, ran.Load()))
			switch {
			case late < after:
				told = false
			case !told:
				told = true
				writeHang(late)
			}
		}
	}()
	return func() { close(done) }
}

// writeHang writes where every goroutine is, the main thread having not
// answered for late.
func writeHang(late time.Duration) {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	exe, _ := os.Executable()
	name := strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe))
	head := fmt.Sprintf("gunim: the main thread of %s has not answered for %.0f s, at %s; where each goroutine is:\n\n",
		name, late.Seconds(), time.Now().Format(time.RFC3339))
	path := filepath.Join(os.TempDir(), fmt.Sprintf("gunim-hang-%s-%d-%s.txt", name, os.Getpid(), time.Now().Format("20060102-150405")))
	// Written aside and renamed into place, the report shows only whole.
	part := path + ".part"
	err := os.WriteFile(part, append([]byte(head), buf...), 0o600)
	if err == nil {
		err = os.Rename(part, path)
	}
	if err == nil {
		fmt.Fprintf(os.Stderr, "gunim: the main thread has not answered for %.0f s; where each goroutine is: %s\n", late.Seconds(), path)
		return
	}
	fmt.Fprintf(os.Stderr, "%s%s\n", head, buf)
}
