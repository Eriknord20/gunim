package vst3

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/ebitengine/purego"

	"github.com/marrasen/gunim/internal/mainthread"
)

// On macOS a plugin's controller and editor live on the main thread,
// where AppKit takes its calls alone, wherever a driver runs work there;
// elsewhere, as in a program with no window, on the package's own.

var pthreadMainNP = sync.OnceValue(func() func() int32 {
	var f func() int32
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return func() int32 { return 0 }
	}
	purego.RegisterLibFunc(&f, lib, "pthread_main_np")
	return f
})

// onMainThread reports whether it runs on the main thread.
func onMainThread() bool { return pthreadMainNP()() != 0 }

// onMain runs f on the main thread, where a driver runs work there, and
// waits for it: at once where it runs there already. It reports whether
// it did; where not, the package's own thread runs f.
func onMain(f func()) bool {
	if !mainthread.Available() {
		return false
	}
	startMainTicks()
	if onMainThread() {
		f()
		return true
	}
	done := make(chan struct{})
	if !mainthread.Post(func() {
		defer close(done)
		f()
	}) {
		return false
	}
	<-done
	return true
}

// onExit runs f, a module's exit, where it can run safely: on the main thread, at once, where it runs there
// already, with or without a driver; else as onUI does. A module whose editor or controller lived on the main
// thread tears down AppKit state as it exits, and traps if that runs on another thread. This happens as a program
// ends and its driver has already let the main thread go.
func onExit(f func()) {
	if onMainThread() {
		f()
		return
	}
	onUI(f)
}

// ticksElsewhere says the tickers run on the main thread, not the
// package's own.
func ticksElsewhere() bool { return mainthread.Available() }

var (
	mainTicksOnce sync.Once
	// ticking says the main thread has tickers to run still, so no more
	// are posted till it has.
	ticking atomic.Bool
)

// startMainTicks has the main thread run the tickers every tickEvery,
// while a driver runs work there.
func startMainTicks() {
	mainTicksOnce.Do(func() {
		go func() {
			t := time.NewTicker(tickEvery)
			defer t.Stop()
			for range t.C {
				if !mainthread.Available() || ticking.Swap(true) {
					continue
				}
				if !mainthread.Post(func() {
					defer ticking.Store(false)
					runTickers()
				}) {
					ticking.Store(false)
				}
			}
		}()
	})
}
