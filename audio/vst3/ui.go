package vst3

import (
	"runtime"
	"sync"
	"time"
)

// A plugin's controller and editor live on one thread, as they expect:
// the package's own, locked, which also runs what plugins ask to be run
// there.

// uiMu is held to read while work is given the thread, and to write as
// the thread starts or stops; uiExited closes as the thread running
// ends, where uiRunning says one runs.
var (
	uiMu      sync.RWMutex
	uiRunning bool
	uiExited  chan struct{}
)

// uiWork is what the thread is asked to do.
var uiWork = make(chan func(), 64)

// tickers are run on the thread every few milliseconds: a plugin's
// output to its controller, and its editor's timers.
var (
	tickersMu sync.Mutex
	tickers   = map[any]func(){}
)

// onUI runs f on the plugins' thread, starting it where none runs, and
// waits for it.
func onUI(f func()) {
	uiMu.RLock()
	for !uiRunning {
		uiMu.RUnlock()
		startUI()
		uiMu.RLock()
	}
	defer uiMu.RUnlock()
	done := make(chan struct{})
	uiWork <- func() {
		defer close(done)
		f()
	}
	wake()
	<-done
}

// tick has f run on the plugins' thread every few milliseconds, under
// key, until untick.
func tick(key any, f func()) {
	tickersMu.Lock()
	defer tickersMu.Unlock()
	tickers[key] = f
}

func untick(key any) {
	tickersMu.Lock()
	defer tickersMu.Unlock()
	delete(tickers, key)
}

// tickEvery is how often the thread runs its tickers.
const tickEvery = 16 * time.Millisecond

func runTickers() {
	tickersMu.Lock()
	fs := make([]func(), 0, len(tickers))
	for _, f := range tickers {
		fs = append(fs, f)
	}
	tickersMu.Unlock()
	for _, f := range fs {
		f()
	}
}

// startUI starts the plugins' thread, where none runs.
func startUI() {
	uiMu.Lock()
	defer uiMu.Unlock()
	if uiRunning {
		return
	}
	ready, exited := make(chan struct{}), make(chan struct{})
	go uiLoop(ready, exited)
	<-ready
	uiRunning, uiExited = true, exited
}

// stopUI ends the plugins' thread, once the work given it is done, and
// waits for it to; the next work starts another.
func stopUI() {
	uiMu.Lock()
	defer uiMu.Unlock()
	if !uiRunning {
		return
	}
	uiWork <- quitUI
	wake()
	<-uiExited
	uiRunning = false
}

// uiLoop runs the thread, locked, which ends with it.
func uiLoop(ready, exited chan struct{}) {
	defer close(exited)
	runtime.LockOSThread()
	uiRun(ready)
}
