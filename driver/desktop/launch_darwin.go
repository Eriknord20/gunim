package desktop

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/marrasen/gunim/driver"
)

var (
	_ driver.Launcher   = (*Window)(nil)
	_ driver.LinkOpener = (*Window)(nil)
)

// Open implements [driver.Launcher] with open(1), which opens the file with the
// program the system keeps for it.
func (w *Window) Open(path string) error { return openFile(path) }

// Reveal implements [driver.Launcher] with open(1), which shows the file
// selected in Finder.
func (w *Window) Reveal(path string) error { return openFile(path, "-R") }

// OpenLink implements [driver.LinkOpener] with open(1), which opens the page in
// the system's browser.
func (w *Window) OpenLink(link string) error {
	// Only a web page: open(1) would read a leading dash as an option, and run
	// what other kinds of address name.
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("desktop: %q is no web page to open", link)
	}
	return runOpen(link)
}

// openWait is how long open(1) gets to answer.
const openWait = 5 * time.Second

// openFile runs open(1) on path, which is absolute so it can't read as an option.
func openFile(path string, flags ...string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("desktop: opening %s: %w", path, err)
	}
	return runOpen(append(flags, abs)...)
}

// runOpen runs open(1), which says whether the system took the request. Past
// openWait it counts as taken, as apps call this from loops other work waits on.
func runOpen(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), openWait)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/open", args...).CombinedOutput()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("desktop: opening %s: %w: %s", args[len(args)-1], err, out)
	}
	return nil
}
