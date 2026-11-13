//go:build linux && (arm64 || amd64)

package kms

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// state is what a run took that outlives the process: the VT it switched
// to, the VT that showed before, and the backlight's level as it found it.
// The driver writes it to a file before it takes them, holds the file
// locked for the run's life, and removes it once it has put them back. A
// run that finds the file unlocked was preceded by one that ended without
// putting them back, as on SIGKILL or a crash in Mesa's C code, and puts
// them back first, so the state it then finds is the machine's own. A file
// still locked belongs to a run that is alive, and is left alone.
type state struct {
	VT        int    `json:"vt,omitempty"`
	Prev      int    `json:"prev,omitempty"`
	Opened    bool   `json:"opened,omitempty"`
	Backlight string `json:"backlight,omitempty"`
	Level     int    `json:"level,omitempty"`
}

// statePath returns where the state file goes: under /run for root, else
// under the service's runtime directory (systemd's RuntimeDirectory=) or
// the user's, or "" where there is none. A test points it elsewhere.
var statePath = func() string {
	if os.Geteuid() == 0 {
		return "/run/gunim-kms.json"
	}
	for _, env := range []string{"RUNTIME_DIRECTORY", "XDG_RUNTIME_DIR"} {
		// systemd lists several directories with colons between.
		if d, _, _ := strings.Cut(os.Getenv(env), ":"); d != "" {
			return filepath.Join(d, "gunim-kms.json")
		}
	}
	return ""
}

// errStateHeld is returned where a live run holds the state file.
var errStateHeld = errors.New("another run holds the screen's state file")

// noStateOnce logs, once, that a run keeps no state file.
var noStateOnce sync.Once

// stateFile is the state file, open and locked for the run's life.
type stateFile struct {
	f    *os.File
	path string
}

// lockState opens the state file and locks it, and returns what a run
// before left in it, if it left anything. It returns errStateHeld where a
// live run holds the lock, and a nil file with no error where there is no
// place for one.
func lockState() (sf *stateFile, left state, found bool, err error) {
	p := statePath()
	if p == "" {
		noStateOnce.Do(func() {
			log.Print("gunim: kms: no runtime directory for the state file (RUNTIME_DIRECTORY or XDG_RUNTIME_DIR); " +
				"a crash would leave the VT and the backlight for the next run to find as they are")
		})
		return nil, state{}, false, nil
	}
	f, err := openLocked(p)
	if err != nil {
		return nil, state{}, false, err
	}
	sf = &stateFile{f: f, path: p}
	b, err := os.ReadFile(p)
	if err == nil && len(b) > 0 {
		if err := json.Unmarshal(b, &left); err != nil {
			log.Printf("gunim: kms: %s: %v", p, err)
		} else {
			found = left != (state{})
		}
	}
	return sf, left, found, nil
}

// openLocked opens the file at p and locks it. A run that ends removes the
// file while it holds the lock, so a file opened before that removal and
// locked after it is no longer the one at p: it tries again, with the file
// at p now.
func openLocked(p string) (*os.File, error) {
	for range 10 {
		f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return nil, fmt.Errorf("state file: %w", err)
		}
		if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
			_ = f.Close()
			if errors.Is(err, unix.EWOULDBLOCK) {
				return nil, errStateHeld
			}
			return nil, fmt.Errorf("state file: lock: %w", err)
		}
		held, err1 := f.Stat()
		named, err2 := os.Stat(p)
		if err1 == nil && err2 == nil && os.SameFile(held, named) {
			return f, nil
		}
		_ = f.Close()
	}
	return nil, errors.New("state file: removed again and again as it was locked")
}

// write keeps s for a run after this one, should this one end without
// putting it back.
func (sf *stateFile) write(s state) error {
	if sf == nil {
		return nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := sf.f.Truncate(0); err != nil {
		return fmt.Errorf("state file: %w", err)
	}
	if _, err := sf.f.WriteAt(b, 0); err != nil {
		return fmt.Errorf("state file: %w", err)
	}
	return nil
}

// done removes the file, once what it holds is put back, and lets go of
// the lock.
func (sf *stateFile) done() {
	if sf == nil {
		return
	}
	if err := os.Remove(sf.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("gunim: kms: %v", err)
	}
	_ = sf.f.Close()
}

// keep lets go of the lock and leaves the file, for the next run to put
// back what this one could not.
func (sf *stateFile) keep() {
	if sf == nil {
		return
	}
	_ = sf.f.Close()
}

// repair puts back what a run before left, as its state file says.
func repair(s state) error {
	errs := []error{repairVT(consoles, s)}
	if s.Backlight != "" && s.Level > 0 {
		b := &backlight{dir: s.Backlight}
		errs = append(errs, b.setLevel(s.Level))
	}
	return errors.Join(errs...)
}
