//go:build !js

package vst3

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// A plugin's controller and editor live on one thread, as they expect:
// the package's own, locked, which also runs what plugins ask to be run
// there; on macOS the main thread, where a driver runs work there.

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

// uiStuckWait is how long a wait on the plugins' thread may go on once a plugin has crashed. Until then a wait has no
// bound, as loading a plugin can take a minute. After a crash the thread may be held for good, by a call of the plugin's
// waiting on a lock the crash left held, and a song is still to be saved.
var uiStuckWait = 5 * time.Second

// uiGaveUp says a wait on the plugins' thread ran out after a crash: nothing is given it from then on.
var uiGaveUp atomic.Bool

// waitUI waits for done, without bound until a plugin crashes and then for uiStuckWait, and reports whether it closed.
func waitUI(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	case <-crashSignal():
	}
	t := time.NewTimer(uiStuckWait)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		uiGaveUp.Store(true)
		return false
	}
}

// onUI runs f on the plugins' thread, starting it where none runs, and
// waits for it. A caller that reads what f gives goes by ranOnUI.
func onUI(f func()) { ranOnUI(f) }

// ranOnUI is onUI, and reports whether f ran. It didn't where the thread is stuck after a plugin's crash.
func ranOnUI(f func()) bool {
	if uiGaveUp.Load() {
		return false
	}
	if onMain(f) {
		return true
	}
	uiMu.RLock()
	for !uiRunning {
		uiMu.RUnlock()
		startUI()
		uiMu.RLock()
	}
	defer uiMu.RUnlock()
	done := make(chan struct{})
	var abandoned atomic.Bool
	work := func() {
		defer close(done)
		if !abandoned.Load() {
			f()
		}
	}
	select {
	case uiWork <- work:
	default:
		// Full, so it is behind: as long a wait as for the work itself.
		sent := make(chan struct{})
		go func() {
			uiWork <- work
			close(sent)
		}()
		if !waitUI(sent) {
			abandoned.Store(true)
			return false
		}
	}
	wake()
	if !waitUI(done) {
		abandoned.Store(true)
		return false
	}
	return true
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
	if !uiRunning || uiGaveUp.Load() {
		return
	}
	select {
	case uiWork <- quitUI:
	default:
		return
	}
	wake()
	if waitUI(uiExited) {
		uiRunning = false
	}
}

// uiLoop runs the thread, locked, which ends with it.
func uiLoop(ready, exited chan struct{}) {
	defer close(exited)
	runtime.LockOSThread()
	uiRun(ready)
}
