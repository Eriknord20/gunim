package install

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// An update is on trial until the release it put in place has run a
// while. Stage keeps the program it replaced beside it, as <exe>.old,
// and a trial file, <exe>.trial. Each start of the new release counts in
// it; one that runs trialRun passes, and both go. A release that keeps
// ending sooner, trialStarts times in a row, as one that crashes as it
// starts does, gives way to the program before it at its next start,
// and the updates pass it over from then on.

// trial is what Stage keeps of the release it put in place, until that
// release has passed.
type trial struct {
	Version string `json:"version"`
	Starts  int    `json:"starts"`
}

var (
	// trialStarts is how many starts in a row a release may end within
	// trialRun before it gives way, at the next.
	trialStarts = 3
	// trialRun is how long a release runs to pass.
	trialRun = 15 * time.Second
)

// trialPath is where the trial of the program at exe is kept.
func trialPath(exe string) string { return exe + ".trial" }

// readTrial reads the trial of the program at exe, and false for none.
func readTrial(exe string) (trial, bool) {
	raw, err := os.ReadFile(trialPath(exe))
	if err != nil {
		return trial{}, false
	}
	var t trial
	if json.Unmarshal(raw, &t) != nil {
		return trial{}, false
	}
	return t, true
}

// write keeps t beside the program at exe.
func (t trial) write(exe string) error {
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return writeVia(trialPath(exe), 0o644, os.Rename, func(f *os.File) error {
		_, err := f.Write(raw)
		return err
	})
}

// keepOld keeps the program at exe as exe.old, for a release Stage puts
// in place to give way to. Where Replace moves a running program aside,
// it keeps it there itself; elsewhere it is linked, or copied. It
// reports whether there is a program kept.
func keepOld(exe string) bool {
	fi, err := os.Stat(exe)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	if movesAside {
		return true
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if os.Link(exe, old) == nil {
		return true
	}
	raw, err := os.ReadFile(exe)
	return err == nil && writeFile(old, raw, 0o755) == nil
}

// onTrial counts this start of the installed program at self, a's
// release, against the trial an update left, and reports whether the
// program before it was put back in place, for this start to hand over
// to. Without a trial, or with one of another release, what an update
// moved aside goes.
func onTrial(a App, self string) (gaveWay bool) {
	t, ok := readTrial(self)
	if ok && t.Version != a.Version && IsRelease(t.Version) && Newer(t.Version, a.Version) {
		// The program a newer release is replacing, started as it is put
		// in place: all stays as it is.
		return false
	}
	if !ok || t.Version != a.Version {
		_ = os.Remove(trialPath(self))
		CleanOld(self)
		return false
	}
	// A start beside a copy already running, as when several files are
	// opened at once, or a restart, says nothing of whether the release
	// can start: it is not counted, and the release passes all the same
	// when one of them runs a while.
	if !othersRunning(self) {
		t.Starts++
		if t.Starts > trialStarts {
			return giveWay(a, self, t) == nil
		}
		if t.write(self) != nil {
			return false
		}
	}
	run := trialRun
	go func() {
		time.Sleep(run)
		passed(self, t.Version)
	}()
	return false
}

// othersRunning reports whether another process runs the program at
// self.
func othersRunning(self string) bool {
	pids, err := running(self)
	if err != nil {
		return false
	}
	for _, pid := range pids {
		if pid != os.Getpid() {
			return true
		}
	}
	return false
}

// keptOld reports whether a program is kept at exe.old.
func keptOld(exe string) bool {
	fi, err := os.Stat(exe + ".old")
	return err == nil && fi.Mode().IsRegular()
}

// passed ends the trial of version at self: the release ran long enough,
// and the program before it goes. The trial is taken away a few times
// over, as on Windows a file another process reads for a moment, as a
// copy starting does, cannot be.
func passed(self, version string) {
	if t, ok := readTrial(self); ok && t.Version == version {
		_ = persist(func() error {
			if err := os.Remove(trialPath(self)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			return nil
		})
		CleanOld(self)
	}
}

// giveWay puts back the program an update replaced with t's release,
// and has the updates pass that release over.
func giveWay(a App, self string, t trial) error {
	old := self + ".old"
	if _, err := os.Stat(old); err != nil {
		_ = os.Remove(trialPath(self))
		return err
	}
	// The release to pass over is kept first: put back without it, the
	// program before would stage it again at its next look.
	dir := filepath.Dir(self)
	m, err := readManifest(dir, a.id())
	if err != nil {
		return err
	}
	if m != nil {
		m.Skip = t.Version
		if err := persist(func() error { return m.write(dir) }); err != nil {
			return err
		}
	}
	back := self + ".back"
	if err := os.Rename(old, back); err != nil {
		return err
	}
	if err := Replace(back, self); err != nil {
		_ = os.Rename(back, old)
		return err
	}
	_ = os.Remove(trialPath(self))
	return nil
}

// persist runs fn until it succeeds, a few times, a little apart, as a
// file another process reads for a moment cannot be replaced on
// Windows.
func persist(fn func() error) error {
	var err error
	for range 10 {
		if err = fn(); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return err
}

// skipped is the release an update gave way from, which the updates
// pass over, or "".
func skipped(a App) string {
	dir, err := a.dir()
	if err != nil {
		return ""
	}
	m, err := readManifest(dir, a.id())
	if err != nil || m == nil {
		return ""
	}
	return m.Skip
}
