//go:build linux

package desktop

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"

	"github.com/marrasen/gunim/driver"
)

var _ driver.Launcher = (*Window)(nil)

// Open implements [driver.Launcher] with the desktop portal's OpenURI,
// which opens the file with the program the desktop keeps for it.
func (w *Window) Open(path string) error {
	return w.openURI("OpenFile", path, func(abs string) string { return abs })
}

// Reveal implements [driver.Launcher] with the desktop portal's OpenURI,
// which opens the folder holding path in the desktop's file manager.
func (w *Window) Reveal(path string) error { return w.openURI("OpenDirectory", path, folderOf) }

var _ driver.LinkOpener = (*Window)(nil)

// OpenLink implements [driver.LinkOpener] with the desktop portal's
// OpenURI, which opens the page in the desktop's browser.
func (w *Window) OpenLink(url string) error {
	parent, err := w.portalParent()
	if err != nil {
		return err
	}
	return portalOrXDGOpen(url, url, nil, "OpenURI", parent, url, map[string]dbus.Variant{})
}

// openURI hands path to the OpenURI portal's method, owned by w, or what target
// makes of it to xdg-open if the portal can't.
func (w *Window) openURI(method, path string, target func(abs string) string) error {
	parent, err := w.portalParent()
	if err != nil {
		return err
	}
	// Absolute, so xdg-open can't read a name as an option or an address.
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("desktop: opening %s: %w", path, err)
	}
	fd, err := unix.Open(abs, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("desktop: opening %s: %w", path, err)
	}
	return portalOrXDGOpen(path, target(abs), func() { _ = unix.Close(fd) }, method, parent, dbus.UnixFD(fd), map[string]dbus.Variant{})
}

// folderOf is path where it is a folder, else the folder holding it.
func folderOf(path string) string {
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		return path
	}
	return filepath.Dir(path)
}

const (
	// portalAnswerWait is how long a caller waits to hear from the portal. Apps
	// call these from loops other work waits on, so it's short, and past it the
	// request counts as taken: the portal may be asking which program to use.
	portalAnswerWait = 500 * time.Millisecond
	// portalHold is how long the connection stays open for a request. The portal
	// drops a request whose connection goes, with any dialog it has up, so this
	// leaves the person time to choose.
	portalHold = 5 * time.Minute
	// xdgOpenWait is how long xdg-open gets to fail. It may stay as long as the
	// program it opened, so past this it counts as having worked.
	xdgOpenWait = 500 * time.Millisecond
)

// portalOrXDGOpen asks the portal, and has xdg-open open target if the portal
// fails or is missing, as on a plain window manager, whether that's before the
// caller stops waiting or after. It names what in what it reports. release, if
// set, runs once the portal has what it was sent.
func portalOrXDGOpen(what, target string, release func(), method string, args ...any) error {
	ask := portalAsk{
		dial:    func() (*dbus.Conn, error) { return dbus.ConnectSessionBus() },
		wait:    portalAnswerWait,
		hold:    portalHold,
		release: release,
		late: func(why error) {
			if err := orXDGOpen(what, target, xdgOpenWait, why); err != nil {
				log.Printf("gunim: %v", err)
			}
		},
	}
	return orXDGOpen(what, target, xdgOpenWait, ask.run(method, args...))
}

// portalDeclined is the lowest Response code for a request that failed; 0 is
// done and 1 is cancelled by the person.
const portalDeclined = 2

// portalAsk is one request of the OpenURI portal.
type portalAsk struct {
	dial func() (*dbus.Conn, error)
	// wait is how long run waits to hear, and hold how long the connection stays.
	wait, hold time.Duration
	// release, if set, runs once the call has been answered or has failed.
	release func()
	// late hears a failure that comes after run stopped waiting.
	late func(error)
}

// run asks on a goroutine of its own, which owns the connection, as closing it
// would cancel the request. It returns the outcome if that comes within wait,
// and nil if not.
func (p portalAsk) run(method string, args ...any) error {
	heard, gone := make(chan error), make(chan struct{})
	go func() {
		err := p.ask(method, args...)
		select {
		case heard <- err:
		case <-gone:
			if err != nil {
				p.late(err)
			}
		}
	}()
	timeout := time.NewTimer(p.wait)
	defer timeout.Stop()
	select {
	case err := <-heard:
		return err
	case <-timeout.C:
		close(gone)
		return nil
	}
}

// ask calls the method and waits for the portal's Response, the one place it says
// that a request it took has failed. No answer within hold counts as done.
func (p portalAsk) ask(method string, args ...any) error {
	released := func() {
		if p.release != nil {
			p.release()
		}
	}
	conn, err := p.dial()
	if err != nil {
		released()
		return fmt.Errorf("reaching the session bus: %w", err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), p.hold)
	defer cancel()
	// Listening first: an answer may come before the call returns.
	if err := conn.AddMatchSignalContext(ctx, dbus.WithMatchInterface("org.freedesktop.portal.Request"), dbus.WithMatchMember("Response")); err != nil {
		released()
		return err
	}
	answers := make(chan *dbus.Signal, 8)
	conn.Signal(answers)
	defer conn.RemoveSignal(answers)

	var handle dbus.ObjectPath
	desk := conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop")
	err = desk.CallWithContext(ctx, "org.freedesktop.portal.OpenURI."+method, 0, args...).Store(&handle)
	released()
	if errors.Is(err, context.DeadlineExceeded) {
		// It may have heard, so this isn't a failure to open it again over.
		return nil
	}
	if err != nil {
		return err
	}
	for {
		select {
		case s, ok := <-answers:
			if !ok {
				return nil
			}
			if s.Path != handle || s.Name != "org.freedesktop.portal.Request.Response" || len(s.Body) == 0 {
				continue
			}
			if code, _ := s.Body[0].(uint32); code >= portalDeclined {
				return fmt.Errorf("the desktop portal could not open it (%d)", code)
			}
			return nil
		case <-ctx.Done():
			return nil
		}
	}
}

// orXDGOpen falls back on xdg-open where the portal failed with why, giving it
// wait to fail. It names what in what it reports.
func orXDGOpen(what, target string, wait time.Duration, why error) error {
	if why == nil {
		return nil
	}
	cmd := exec.CommandContext(context.Background(), "xdg-open", target)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("desktop: opening %s: %w", what, why)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("desktop: opening %s: %w, nor could xdg-open: %w", what, why, err)
		}
	case <-time.After(wait):
	}
	return nil
}
