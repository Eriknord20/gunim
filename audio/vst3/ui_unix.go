//go:build !windows

package vst3

import "time"

// uiQuit says the thread ends once the work it runs is done.
var uiQuit bool

// quitUI ends the thread, run on it.
func quitUI() { uiQuit = true }

// uiRun runs the plugins' thread: the work it is given, and its tickers,
// until quitUI.
func uiRun(ready chan struct{}) {
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	uiQuit = false
	close(ready)
	for {
		select {
		case f := <-uiWork:
			f()
			if uiQuit {
				return
			}
		case <-t.C:
			runTickers()
		}
	}
}

// wake tells the thread work waits; it waits on the work itself.
func wake() {}
