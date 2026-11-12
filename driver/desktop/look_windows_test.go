//go:build windows

package desktop

import (
	"image/color"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/internal/glfw"
)

var procSendMessageW = user32.NewProc("SendMessageW")

func TestTheSystemLookReads(t *testing.T) {
	l, err := readLook()
	if err != nil {
		t.Fatal(err)
	}
	if l.TextScale < 1 || l.TextScale > 2.25 {
		t.Errorf("text scale = %v, want 1 to 2.25", l.TextScale)
	}
	if l.Contrast != nil && l.Contrast.Window == l.Contrast.WindowText {
		t.Errorf("a contrast theme has text the colour of its window, %v", l.Contrast.Window)
	}
}

func TestAValueOfAnotherTypeFallsBackAndAFailureIsAnError(t *testing.T) {
	for _, c := range []struct {
		v    uint64
		err  error
		want uint64
		ok   bool
	}{
		{150, nil, 150, true},
		{0, windows.ERROR_FILE_NOT_FOUND, 100, true},
		{0, registry.ErrUnexpectedType, 100, true},
		{0, windows.ERROR_ACCESS_DENIED, 0, false},
	} {
		got, err := dwordOr(c.v, c.err, 100)
		if got != c.want || (err == nil) != c.ok {
			t.Errorf("a value of %d read with %v means %d, %v; want %d, and an error %v", c.v, c.err, got, err,
				c.want, !c.ok)
		}
	}
}

func TestTheTextScaleKeepsToWhatWindowsOffers(t *testing.T) {
	for percent, want := range map[uint64]float32{100: 1, 150: 1.5, 225: 2.25, 0: 1, 50: 1, 400: 2.25} {
		if got := textScale(percent); got != want {
			t.Errorf("TextScaleFactor %d is a scale of %v, want %v", percent, got, want)
		}
	}
}

func TestStopWaitsForACallInProgressAndNoneComesAfter(t *testing.T) {
	if pump == nil {
		t.Skip("no display")
	}
	var mu sync.Mutex
	scale := float32(1)
	lookNow = func() (driver.SystemLook, error) {
		mu.Lock()
		defer mu.Unlock()
		return driver.SystemLook{TextScale: scale}, nil
	}
	in, release := make(chan struct{}), make(chan struct{})
	var calls sync.WaitGroup
	calls.Add(1)
	called := 0
	stop, err := pump.WatchSystemLook(func(driver.SystemLook, error) {
		called++
		if called == 1 {
			calls.Done()
			close(in)
			<-release
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	scale = 1.5
	mu.Unlock()
	announce(t)
	<-in
	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("stop returned while a call to fn was still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-stopped
	calls.Wait()
	mu.Lock()
	scale = 2
	mu.Unlock()
	announce(t)
	time.Sleep(100 * time.Millisecond)
	// Restored only now that the watch has ended, so nothing reads it meanwhile.
	lookNow = readLook
	if called != 1 {
		t.Fatalf("fn ran %d times, want once: none after stop", called)
	}
}

// announce sends the watch's window a setting change, as Windows does, and waits for it to be heard.
func announce(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	pump.post(func() {
		if h := glfw.SettingsWindow(); h != 0 {
			_, _, _ = procSendMessageW.Call(h, 0x001a, 0, 0) // WM_SETTINGCHANGE
		}
		close(done)
	})
	<-done
}

func TestAChangedSettingReachesTheWatch(t *testing.T) {
	if pump == nil {
		t.Skip("no display")
	}
	var mu sync.Mutex
	now := driver.SystemLook{TextScale: 1}
	lookNow = func() (driver.SystemLook, error) {
		mu.Lock()
		defer mu.Unlock()
		return now, nil
	}
	defer func() { lookNow = readLook }()

	heard := make(chan driver.SystemLook, 4)
	stop, err := pump.WatchSystemLook(func(l driver.SystemLook, err error) {
		if err != nil {
			t.Error(err)
		}
		heard <- l
	})
	if err != nil {
		t.Fatal(err)
	}
	announce := func() {
		done := make(chan struct{})
		pump.post(func() {
			_, _, _ = procSendMessageW.Call(glfw.SettingsWindow(), 0x001a, 0, 0) // WM_SETTINGCHANGE
			close(done)
		})
		<-done
	}

	mu.Lock()
	now = driver.SystemLook{Dark: true, TextScale: 1.25,
		Contrast: &driver.ContrastColors{Window: color.NRGBA{A: 0xff}}}
	mu.Unlock()
	announce()
	select {
	case l := <-heard:
		if !l.Dark || l.TextScale != 1.25 || l.Contrast == nil {
			t.Errorf("the watch heard %+v, want the dark, larger, contrast look set", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a changed setting never reached the watch")
	}

	// The same look again says nothing.
	announce()
	select {
	case l := <-heard:
		t.Errorf("the watch heard %+v, though nothing changed", l)
	case <-time.After(200 * time.Millisecond):
	}

	stop()
	stop()
	gone := make(chan uintptr, 1)
	pump.post(func() { gone <- glfw.SettingsWindow() })
	if h := <-gone; h != 0 {
		t.Errorf("the settings window is still up after stop")
	}
}
