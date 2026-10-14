package vst3

import (
	"testing"
	"time"
)

func TestShutdownEndsThePluginsThreadAndWorkStartsAnother(t *testing.T) {
	var first, second chan struct{}
	onUI(func() {})
	uiMu.RLock()
	first = uiExited
	uiMu.RUnlock()
	if err := Shutdown(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("the thread runs on after Shutdown")
	}
	ran := false
	onUI(func() { ran = true })
	if !ran {
		t.Fatal("work after Shutdown did not run")
	}
	uiMu.RLock()
	second = uiExited
	uiMu.RUnlock()
	if second == first {
		t.Fatal("work after Shutdown ran on no new thread")
	}
	if err := Shutdown(); err != nil {
		t.Fatal(err)
	}
	// With no thread, Shutdown starts none.
	if err := Shutdown(); err != nil {
		t.Fatal(err)
	}
	uiMu.RLock()
	running := uiRunning
	uiMu.RUnlock()
	if running {
		t.Fatal("Shutdown started a thread")
	}
}
