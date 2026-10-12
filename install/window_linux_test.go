//go:build linux && !android

package install

import (
	"context"
	"errors"
	"image/color"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/paint"
)

// offscreen runs the installer's window on s offscreen, its work's
// reports handled between frames, as serve would.
type offscreen struct {
	t *testing.T
	w *gunim.Window
	r *runner
}

func openOffscreen(t *testing.T, s *Session) *offscreen {
	t.Helper()
	w := gunimtest.New(t, geom.Sz(640, 600), &gunim.Box{})
	r, err := attach(w, s, nil, accentFor(&s.App))
	if err != nil {
		t.Fatal(err)
	}
	o := &offscreen{t: t, w: w, r: r}
	o.frames(10)
	return o
}

// frames draws n frames, taking the work's reports between them.
func (o *offscreen) frames(n int) {
	for range n {
		for more := true; more; {
			select {
			case fn := <-o.r.events:
				fn()
			default:
				more = false
			}
		}
		o.w.Frame(time.Second / 60)
	}
}

// until draws frames until the scene is on page, or fails.
func (o *offscreen) until(page string) {
	o.t.Helper()
	for range 600 {
		if o.r.sc.Page == page {
			o.frames(60)
			return
		}
		o.frames(1)
		time.Sleep(time.Millisecond)
	}
	o.t.Fatalf("the installer is on %q, not %q (problem %q)", o.r.sc.Page, page, o.r.sc.Problem)
}

// The window takes an install from the welcome to done, with the ring
// and the pages crossing over, and an uninstall from asking to
// removed; what it shows is the scene the work made.
func TestWindowInstallsAndRemoves(t *testing.T) {
	leastWork = 0
	t.Cleanup(func() { leastWork = 1600 * time.Millisecond })
	_, program := testHome(t)
	a := testApp()
	s, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	o := openOffscreen(t, s)
	if o.r.sc.Page != pageWelcome || len(o.r.sc.Offers) != 5 {
		t.Fatalf("opened on %q with %d offers", o.r.sc.Page, len(o.r.sc.Offers))
	}
	ctx := t.Context()
	o.r.handle(ctx, started{Picks: map[string]bool{PickDesktop: true}})
	if o.r.sc.Page != pageWorking {
		t.Fatalf("pressing Install went to %q", o.r.sc.Page)
	}
	o.until(pageDone)
	if _, err := os.Stat(s.Exe); err != nil {
		t.Fatal("done, and the program is not installed:", err)
	}
	o.r.handle(ctx, opened{})
	if s.next != nextOpen {
		t.Error("Open did not leave the installed program to start")
	}

	gone, err := sessionFor(a, program, true)
	if err != nil {
		t.Fatal(err)
	}
	o = openOffscreen(t, gone)
	if o.r.sc.Page != pageRemove {
		t.Fatalf("uninstall opened on %q", o.r.sc.Page)
	}
	o.r.handle(ctx, removed{})
	o.until(pageRemoved)
	if _, err := os.Stat(s.Exe); err == nil {
		t.Fatal("removed, and the program is still there")
	}
}

// A failure shows, and Try Again goes back to the choices.
func TestWindowFailsAndGoesBack(t *testing.T) {
	leastWork = 0
	t.Cleanup(func() { leastWork = 1600 * time.Millisecond })
	_, program := testHome(t)
	a := testApp()
	a.Installed = func(context.Context, Installation) error { return os.ErrPermission }
	s, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	o := openOffscreen(t, s)
	o.r.handle(t.Context(), started{})
	o.until(pageFailed)
	if o.r.sc.Problem == "" {
		t.Error("the failure says nothing")
	}
	o.r.handle(t.Context(), retried{})
	o.until(pageWelcome)
}

// What the two halves say to each other would survive a socket.
func TestWire(t *testing.T) {
	if err := gunim.CheckWire(
		scene{Name: "x", Offers: []Offer{{Key: "k", Label: "l", On: true}}, Mode: Upgrade, Progress: 0.5},
		started{Picks: map[string]bool{"a": true}}, opened{}, ranHere{}, closed{}, retried{}, quitThem{}, removed{Data: true},
	); err != nil {
		t.Fatal(err)
	}
}

// installedOnce installs testApp, and returns a session to install it
// over itself, as a second download does.
func installedOnce(t *testing.T, a App) *Session {
	t.Helper()
	_, program := testHome(t)
	first, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Install(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	s, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// fastWatch makes the installer look for closed copies often, and wait
// little for one asked to close.
func fastWatch(t *testing.T) {
	was, wasClose, wasWork := watchEvery, closeWait, leastWork
	watchEvery, closeWait, leastWork = 5*time.Millisecond, 200*time.Millisecond, 0
	t.Cleanup(func() { watchEvery, closeWait, leastWork = was, wasClose, wasWork })
}

// A copy running holds the install up until it closes, and the install
// then goes on by itself.
func TestTheInstallWaitsForTheProgramToClose(t *testing.T) {
	fastWatch(t)
	s := installedOnce(t, testApp())
	o := openOffscreen(t, s)
	var open atomic.Bool
	open.Store(true)
	o.r.running = func() []int {
		if open.Load() {
			return []int{42}
		}
		return nil
	}
	o.r.handle(t.Context(), started{})
	o.until(pageRunning)
	open.Store(false)
	o.until(pageDone)
}

// Asked to, the program closes, with the ring turning meanwhile, and
// the install goes on; one that will not close is said to have stayed.
func TestCloseTheProgramFromTheInstaller(t *testing.T) {
	fastWatch(t)
	var open atomic.Bool
	open.Store(true)
	a := testApp()
	a.Quit = func(context.Context) error { open.Store(false); return nil }
	s := installedOnce(t, a)
	o := openOffscreen(t, s)
	o.r.running = func() []int {
		if open.Load() {
			return []int{42}
		}
		return nil
	}
	o.r.handle(t.Context(), started{})
	o.until(pageRunning)
	o.r.handle(t.Context(), quitThem{})
	if o.r.sc.Page != pageClosing {
		t.Fatalf("asked to close it, the installer is on %q", o.r.sc.Page)
	}
	o.until(pageDone)

	// One that stays open.
	stubborn := testApp()
	stubborn.Quit = func(context.Context) error { return nil }
	s = installedOnce(t, stubborn)
	o = openOffscreen(t, s)
	o.r.running = func() []int { return []int{42} }
	o.r.handle(t.Context(), started{})
	o.until(pageRunning)
	o.r.handle(t.Context(), quitThem{})
	o.until(pageRunning)
	if o.r.sc.Problem == "" {
		t.Fatal("a program that didn't close is not said to have stayed")
	}
	// Cancel still closes the window, and nothing was installed over it.
	o.r.handle(t.Context(), closed{})
	if !o.r.left || o.r.working {
		t.Errorf("Cancel left the window %v, working %v", o.r.left, o.r.working)
	}
}

// Close while the program is set up stops the hook: every frame until
// it has stopped says so, and the window then says the program is
// installed, with its setup undone, in place of closing on it.
func TestCloseStopsTheSetUp(t *testing.T) {
	fastWatch(t)
	_, program := testHome(t)
	a := testApp()
	stopped := make(chan struct{})
	a.Installed = func(ctx context.Context, _ Installation) error {
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}
	s, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	o := openOffscreen(t, s)
	o.r.handle(t.Context(), started{})
	for o.r.sc.Step != "Setting it up" {
		o.frames(1)
		time.Sleep(time.Millisecond)
	}
	o.r.handle(t.Context(), closed{})
	for i := 0; o.r.working; i++ {
		if i > 2000 {
			t.Fatal("Close did not stop the setup")
		}
		if o.r.sc.Page != pageWorking || o.r.sc.Step != "Stopping" {
			t.Fatalf("frame %d after Close: page %q, step %q; want the work stopping", i, o.r.sc.Page, o.r.sc.Step)
		}
		o.frames(1)
		time.Sleep(time.Millisecond)
	}
	<-stopped
	o.until(pageFailed)
	if o.r.left || !strings.Contains(o.r.sc.Problem, "installed") {
		t.Errorf("stopped in its setup, the window left %v, saying %q", o.r.left, o.r.sc.Problem)
	}
}

// Close while an uninstall's hook runs, before anything is taken away,
// stops it, and the window closes once it has stopped.
func TestCloseStopsTheUninstall(t *testing.T) {
	fastWatch(t)
	_, program := testHome(t)
	a := testApp()
	waiting := make(chan struct{})
	a.Uninstalling = func(ctx context.Context, _ Installation) error {
		close(waiting)
		<-ctx.Done()
		return ctx.Err()
	}
	first, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = first.Install(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	s, err := sessionFor(a, program, true)
	if err != nil {
		t.Fatal(err)
	}
	o := openOffscreen(t, s)
	o.r.handle(t.Context(), removed{})
	<-waiting
	o.r.handle(t.Context(), closed{})
	for i := 0; !o.r.left; i++ {
		if i > 2000 {
			t.Fatal("Close did not stop the uninstall")
		}
		if o.r.sc.Page != pageWorking || o.r.sc.Step != "Stopping" {
			t.Fatalf("frame %d after Close: page %q, step %q; want the work stopping", i, o.r.sc.Page, o.r.sc.Step)
		}
		o.frames(1)
		time.Sleep(time.Millisecond)
	}
	if _, err = os.Stat(s.Exe); err != nil {
		t.Error("an uninstall stopped before it began took the program:", err)
	}
}

// A hook that will not stop holds the window only until Close is
// pressed again.
func TestASecondCloseLeavesAtOnce(t *testing.T) {
	fastWatch(t)
	_, program := testHome(t)
	a := testApp()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	a.Installed = func(context.Context, Installation) error { <-release; return nil }
	s, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	o := openOffscreen(t, s)
	o.r.handle(t.Context(), started{})
	for o.r.sc.Step != "Setting it up" {
		o.frames(1)
		time.Sleep(time.Millisecond)
	}
	o.r.handle(t.Context(), closed{})
	o.frames(5)
	if o.r.left {
		t.Fatal("one Close left the window before the work stopped")
	}
	o.r.handle(t.Context(), closed{})
	if !o.r.left {
		t.Fatal("a second Close did not leave the window")
	}
}

// Close pressed as the work ends, as when the program closed and the
// install went on by itself just before Cancel: what was done shows.
func TestCloseAfterTheWorkEndedShowsWhatWasDone(t *testing.T) {
	fastWatch(t)
	_, program := testHome(t)
	s, err := sessionFor(testApp(), program, false)
	if err != nil {
		t.Fatal(err)
	}
	o := openOffscreen(t, s)
	o.r.handle(t.Context(), started{})
	<-o.r.finished
	o.r.handle(t.Context(), closed{})
	o.until(pageDone)
	if o.r.left {
		t.Error("the window closed on an install that was done, saying nothing")
	}
}

// A late answer to an earlier ask to close says nothing of the ask now.
func TestAnEarlierAskToCloseStaysQuiet(t *testing.T) {
	fastWatch(t)
	var open atomic.Bool
	open.Store(true)
	first := make(chan struct{})
	second := make(chan struct{})
	t.Cleanup(func() { close(second) })
	var asks atomic.Int32
	a := testApp()
	a.Quit = func(ctx context.Context) error {
		if asks.Add(1) == 1 {
			<-first
			return errors.New("no answer")
		}
		<-second
		return nil
	}
	var fails atomic.Bool
	a.Installed = func(context.Context, Installation) error {
		if fails.Swap(false) {
			return os.ErrPermission
		}
		return nil
	}
	s := installedOnce(t, a)
	fails.Store(true)
	o := openOffscreen(t, s)
	o.r.running = func() []int {
		if open.Load() {
			return []int{42}
		}
		return nil
	}
	ctx := t.Context()
	o.r.handle(ctx, started{})
	o.until(pageRunning)
	o.r.handle(ctx, quitThem{})
	open.Store(false)
	o.until(pageFailed)
	o.r.handle(ctx, retried{})
	open.Store(true)
	o.r.handle(ctx, started{})
	o.until(pageRunning)
	o.r.handle(ctx, quitThem{})
	close(first)
	for i := range 100 {
		o.frames(1)
		time.Sleep(time.Millisecond)
		if o.r.sc.Page != pageClosing || o.r.sc.Problem != "" {
			t.Fatalf("frame %d after the first ask's late answer: page %q, problem %q", i, o.r.sc.Page, o.r.sc.Problem)
		}
	}
}

// Interrupted, as by Ctrl+C, the window waits for the work to stop
// before serve returns, and says it was interrupted.
func TestAnInterruptWaitsForTheWork(t *testing.T) {
	fastWatch(t)
	_, program := testHome(t)
	a := testApp()
	a.Installed = func(ctx context.Context, _ Installation) error {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		return ctx.Err()
	}
	s, err := sessionFor(a, program, false)
	if err != nil {
		t.Fatal(err)
	}
	o := openOffscreen(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	o.r.handle(ctx, started{})
	for o.r.sc.Step != "Setting it up" {
		o.frames(1)
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := o.r.serve(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("interrupted, serve returned %v", err)
	}
	select {
	case <-o.r.finished:
	default:
		t.Error("serve returned with the work still running")
	}
}

// attach registers the installer's views with w, shows s's first page
// there, and returns the application half, ready to serve.
func attach(w *gunim.Window, s *Session, img *paint.Image, accent color.NRGBA) (*runner, error) {
	sc := firstScene(s)
	c, err := mountStage(w, &s.App, img, accent, sc)
	if err != nil {
		return nil, err
	}
	return &runner{s: s, c: c, sc: sc, events: make(chan func(), 16)}, nil
}
