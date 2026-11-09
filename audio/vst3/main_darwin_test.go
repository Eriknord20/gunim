package vst3

import (
	"os"
	"runtime"
	"testing"
)

// The main goroutine keeps the main thread, so tests can run work there, as a program does once its driver stops.
func init() { runtime.LockOSThread() }

// onTestMain is work the tests run on the main thread.
var onTestMain = make(chan func())

func TestMain(m *testing.M) {
	done := make(chan int)
	go func() { done <- m.Run() }()
	for {
		select {
		case f := <-onTestMain:
			f()
		case code := <-done:
			os.Exit(code)
		}
	}
}

// runOnTestMain runs f on the main thread and waits for it.
func runOnTestMain(f func()) {
	done := make(chan struct{})
	onTestMain <- func() {
		defer close(done)
		f()
	}
	<-done
}

// A module's exit called from the main thread, with no driver, runs there and not on the plugins' own thread.
func TestExitFromTheMainThreadWithNoDriverRunsThere(t *testing.T) {
	var onMainBefore, ranOnMain bool
	runOnTestMain(func() {
		onMainBefore = onMainThread()
		onExit(func() { ranOnMain = onMainThread() })
	})
	if !onMainBefore {
		t.Fatal("the test's work did not run on the main thread")
	}
	if !ranOnMain {
		t.Error("the exit ran off the main thread")
	}
}

// Elsewhere, with no driver, the exit runs on the plugins' own thread.
func TestExitOffTheMainThreadWithNoDriverRunsOnThePluginsThread(t *testing.T) {
	var ranOnMain bool
	ran := false
	onExit(func() { ran, ranOnMain = true, onMainThread() })
	if !ran {
		t.Fatal("the exit did not run")
	}
	if ranOnMain {
		t.Error("the exit ran on the main thread")
	}
}
