package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"
)

// Env is the environment variable that steers [Run] past its usual
// choice: "skip" runs the program as it is, with no installer, and
// "show" opens the installer's window even in a build from a working
// tree, to try it.
const Env = "GUNIM_INSTALL"

// Run is the installer's way into a program. Call it first in main,
// before the program reads its flags:
//
//	func main() {
//		install.Run(install.App{Name: "Marras Mastering Studio", Version: version, Icon: icon()})
//		…
//	}
//
// Run returns when the program should go on and run as itself: when it
// is the installed copy, when it was built from a working tree, and on
// a system installing is not done on yet. Otherwise it does what was
// asked, and ends the program:
//
//   - Started as "program -install", it installs with no window, as a
//     script would, with the choices of the copy installed before, or
//     the defaults.
//   - Started as "program -uninstall", as the system's list of installed
//     programs starts it, it asks in a window and takes the program
//     away. With -quiet it asks nothing.
//   - Started any other way, it opens the installer's window, which
//     installs this copy, updates the one installed, or opens it.
//
// The installed copy, as it starts, takes away what an update moved
// aside once the new release has run a while, or puts it back in place
// for a release that keeps ending as it starts, puts the rest of a new
// release in place on its first start,
// and, if the user chose to keep it up to date, looks for newer
// releases while it runs.
//
// A mistake in a, such as no Name, ends the program with the mistake
// said, so it shows the first time the program runs.
func Run(a App) {
	if err := a.check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	env := os.Getenv(Env)
	_ = os.Unsetenv(Env)
	if env == "skip" {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	self = resolve(self)
	verb, quiet := "", false
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-install", "--install":
			verb = "install"
		case "-uninstall", "--uninstall":
			verb = "uninstall"
		}
		if verb != "" {
			for _, arg := range os.Args[2:] {
				if arg == "-quiet" || arg == "--quiet" {
					quiet = true
				}
			}
		}
	}
	if !supported {
		if verb != "" {
			fmt.Fprintln(os.Stderr, ErrUnsupported)
			os.Exit(1)
		}
		return
	}
	if pid, ok := restartPID(env); ok && verb == "" {
		// Started by an update in place of the program running.
		os.Exit(restarted(a, self, pid))
	}
	switch verb {
	case "install":
		os.Exit(installQuietly(a, self))
	case "uninstall":
		s, err := sessionFor(a, self, true)
		if err != nil {
			fail(err)
		}
		if quiet {
			if err := s.Uninstall(context.Background(), false, nil); err != nil {
				fail(err)
			}
			os.Exit(0)
		}
		os.Exit(show(a, s))
	}
	dir, err := a.dir()
	if err != nil {
		return
	}
	if samePath(self, filepath.Join(dir, a.exe())) {
		startInstalled(a, self, dir)
		return
	}
	if a.former(self) && IsRelease(a.Version) {
		// An older version's install, updated in place by that version's
		// own updates: the program moves to where it goes now, and runs,
		// or hands over to a newer version installed there already.
		if newer := moveIn(a, self); newer != "" {
			if err = launch(newer, os.Args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "install: starting %s: %v\n", newer, err)
				return
			}
			os.Exit(0)
		}
		return
	}
	if !IsRelease(a.Version) && env != "show" {
		return
	}
	s, err := sessionFor(a, self, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "install:", err)
		return
	}
	s.args = os.Args[1:]
	os.Exit(show(a, s))
}

// fail says err and ends the program.
func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// installQuietly installs the program at self with no window, and
// returns the exit status.
func installQuietly(a App, self string) int {
	s, err := sessionFor(a, self, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	in, err := s.Install(context.Background(), nil, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "couldn't install %s: %v\n", a.Name, err)
		return 1
	}
	fmt.Printf("%s %s is installed in %s.\n", a.Name, a.Version, in.Dir)
	return 0
}

// startInstalled readies the installed copy, the program at self in
// dir, as it starts.
func startInstalled(a App, self, dir string) {
	if onTrial(a, self) {
		// This release kept ending as it started, and the program before
		// it is back in place: it runs in this one's stead.
		if err := launch(self, os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "install: starting %s again: %v\n", self, err)
			return
		}
		os.Exit(0)
	}
	m, err := readManifest(dir, a.id())
	if err != nil {
		return
	}
	if m == nil {
		if !IsRelease(a.Version) {
			return
		}
		// Installed by an installer that kept no record, as an older
		// version's own was: the program takes it on, with what it has
		// on the system, as it would have been installed.
		if err := adopt(a, self); err != nil {
			fmt.Fprintf(os.Stderr, "install: taking on the install in %s: %v\n", dir, err)
			return
		}
		if m, err = readManifest(dir, a.id()); err != nil || m == nil {
			return
		}
	}
	CleanOld(filepath.Join(dir, manifestName))
	for _, f := range m.Files {
		CleanOld(filepath.Join(dir, filepath.FromSlash(f)))
	}
	if m.Version != a.Version && IsRelease(a.Version) {
		// The first start after an update put this program in place: its
		// files and its entries follow it. The room is not checked: the
		// program is in place already, and stopping now would leave it
		// with the last release's files.
		if err := adopt(a, self); err != nil {
			fmt.Fprintf(os.Stderr, "install: finishing the update to %s: %v\n", a.Version, err)
		}
		if IsRelease(m.Version) && Newer(a.Version, m.Version) {
			// Not a release that gave way, which runs an older one.
			updatedFrom = m.Version
		}
	}
	if a.Updates != nil && IsRelease(a.Version) {
		// It reads the mode at each look, so a change of it in the
		// program's settings holds from the next.
		go keepUpToDate(context.Background(), a)
	}
}

// adopt installs the program at self quietly, with the picks the
// install there has, without checking the room.
func adopt(a App, self string) error {
	return adoptReporting(context.Background(), a, self, nil)
}

// adoptReporting is [adopt], telling progress how it goes.
func adoptReporting(ctx context.Context, a App, self string, progress func(Progress)) error {
	s, err := sessionFor(a, self, false)
	if err != nil {
		return err
	}
	_, err = s.installTo(ctx, nil, progress, false)
	return err
}

// moveIn installs the program at self, where an older version put it,
// to where it goes now, with what that install has on the system. A
// newer version installed already stays: moveIn returns it, for the old
// copy to hand over to, as when a shortcut an older installer made
// still starts the old place. It returns "" when this copy runs on.
func moveIn(a App, self string) (newer string) {
	s, err := sessionFor(a, self, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "install: moving %s from %s: %v\n", a.Name, self, err)
		return ""
	}
	if fi, err := os.Stat(s.Exe); err == nil && fi.Mode().IsRegular() && s.Mode == Downgrade {
		removeOld(self)
		return s.Exe
	}
	if _, err := s.installTo(context.Background(), nil, nil, false); err != nil {
		fmt.Fprintf(os.Stderr, "install: moving %s from %s: %v\n", a.Name, self, err)
		return ""
	}
	removeOld(self)
	return ""
}

// removeOld takes away the program at self, where an older version put
// it, now that it is installed where it goes: at once, or once it has
// ended, where the system keeps a running program's file. Where the old
// place is the link that now stands there, it stays.
func removeOld(self string) {
	if fi, err := os.Lstat(self); err == nil && fi.Mode().IsRegular() {
		removeWhenEnded(self)
	}
}

// next is what the program does once the installer's window has
// closed.
type next int

const (
	// nextEnd is the installer having nothing more to do. nextOpen
	// starts the installed program, and nextHere runs this copy as it
	// is.
	nextEnd next = iota
	nextOpen
	nextHere
)

// Open has the installed program start once the installer's window
// closes, with the arguments this copy was started with.
func (s *Session) Open() { s.next = nextOpen }

// RunHere has this copy run as it is, uninstalled, once the installer's
// window closes.
func (s *Session) RunHere() { s.next = nextHere }

// show runs the installer's window on s, then does what it was left to
// do, and returns the exit status.
func show(a App, s *Session) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := gunim.Main(ctx, func(app *gunim.App) error {
		if a.Window != nil {
			return a.Window(ctx, app, s)
		}
		return window(ctx, app, s)
	})
	if ctx.Err() != nil {
		// Interrupted, as by Ctrl+C in a terminal: the window waited for
		// the work to stop, and the program ends as an interrupted one.
		fmt.Fprintln(os.Stderr, "install: interrupted")
		return 130
	}
	if errors.Is(err, driver.ErrNoDriver) {
		if s.Mode == Remove {
			fmt.Fprintln(os.Stderr, "install: no display to ask on; -uninstall -quiet removes "+a.Name+" without asking")
			return 1
		}
		// No display: the installer cannot ask, and the program runs as
		// it is.
		s.next = nextHere
		err = nil
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	switch s.next {
	case nextOpen:
		if err := launch(s.Exe, s.args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	case nextHere:
		return again(s.self, s.args)
	default:
		// The end of the list is not a step.
	}
	return 0
}

// again runs the program at self once more, past the installer, and
// returns its exit status. A new process rather than going on in this
// one: the window system closed with the installer's window, and some
// do not open twice.
func again(self string, args []string) int {
	// No context: this is the installed copy taking over, and it runs
	// for as long as the user keeps it open.
	cmd := exec.Command(self, args...) //nolint:noctx
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), Env+"=skip")
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
