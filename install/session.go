package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// Mode is what a session does, from how this copy of the program stands
// against the one installed.
type Mode int

const (
	// Fresh installs a program that is not installed yet.
	Fresh Mode = iota
	// Upgrade installs this copy over an older one.
	Upgrade
	// Reinstall installs this copy over the same version.
	Reinstall
	// Downgrade installs this copy over a newer one.
	Downgrade
	// Remove uninstalls the program.
	Remove
)

// Session is one run of the installer: where the program goes, what is
// there now, what to offer, and the means to install and uninstall. The
// installer's window drives one; an application with a window of its
// own, set as [App.Window], drives it in its place.
type Session struct {
	// App is the program, its defaults filled in.
	App App
	// Mode is what the session is for.
	Mode Mode
	// Dir is the folder the program goes in, and Exe the program there.
	Dir, Exe string
	// Have is the copy installed already, or nil.
	Have *Installation
	// Offers are the offers to make, ticked as the user last chose, or
	// as the application starts them.
	Offers []Offer
	// Need is how many bytes the install takes, and Free how many are
	// free where it goes, or -1 when that is not known.
	Need, Free int64

	// self is this program, and args what it was started with; next is
	// what to do once the installer's window closes.
	self string
	args []string
	next next
}

// Offer is one offer the installer makes.
type Offer struct {
	// Key names it in [Installation.Picks].
	Key string
	// Label is what it says, and Detail a line under it.
	Label, Detail string
	// On says it is ticked to begin with.
	On bool
}

// Progress is how far an install or uninstall has got: what it does
// now, and how much of the whole is done, from 0 to 1.
type Progress struct {
	Step string
	Done float32
}

// manifestName is the file in the program's folder that says what was
// installed.
const manifestName = "install.json"

// manifest is what an install keeps of itself in the program's folder,
// for the next install, the updates and the uninstall.
type manifest struct {
	ID      string          `json:"id"`
	Version string          `json:"version"`
	Exe     string          `json:"exe"`
	Files   []string        `json:"files,omitempty"`
	Picks   map[string]bool `json:"picks,omitempty"`
	// Updates is how the program takes newer releases; empty in an
	// install from before there was a choice of it.
	Updates UpdateMode `json:"updates,omitempty"`
	// Skip is a release an update gave way from, as one that kept
	// ending as it started, which the updates pass over.
	Skip string    `json:"skip,omitempty"`
	When time.Time `json:"installed"`
}

// dir is where a is installed: as App.Dir says, or the system's usual
// place.
func (a *App) dir() (string, error) {
	if a.Dir != nil {
		d, err := a.Dir()
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(d) {
			return "", fmt.Errorf("install: App.Dir gave %q, which is not a full path", d)
		}
		return filepath.Clean(d), nil
	}
	return defaultDir(a)
}

// errSetUp says the program is installed, and App.Installed failed or
// was stopped: installing it again runs it again.
var errSetUp = errors.New("installed, but setting it up did not finish")

// errOther says a folder holds the install of another program.
var errOther = errors.New("holds another program's install")

// readManifest reads what the install of the program named id in dir
// kept of itself, or nil for none. A manifest of another program's, as
// in a folder two programs share, is errOther: its files are not this
// program's to replace or take away.
func readManifest(dir, id string) (*manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, manifestName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("read %s: %w", manifestName, err)
	}
	if m.ID != id {
		return nil, fmt.Errorf("install: %s %w, %s", dir, errOther, m.ID)
	}
	return &m, nil
}

// write keeps m in dir, written whole or not at all. Nothing runs it,
// so it is moved over the one before with nothing moved aside.
func (m *manifest) write(dir string) error {
	raw, err := json.MarshalIndent(m, "", "\t")
	if err != nil {
		return err
	}
	return writeVia(filepath.Join(dir, manifestName), 0o644, os.Rename, func(f *os.File) error {
		_, err := f.Write(raw)
		return err
	})
}

// installation is what m says of the install in dir.
func (m *manifest) installation(a *App, dir string) *Installation {
	return &Installation{Dir: dir, Exe: filepath.Join(dir, m.Exe), Version: m.Version, Picks: maps(m.Picks), Updates: m.mode(a)}
}

// mode is how the install takes newer releases: as it keeps it, or, in
// one from before there was a choice, as its offer was ticked, or as a
// starts.
func (m *manifest) mode(a *App) UpdateMode {
	if m.Updates.valid() {
		return m.Updates
	}
	if on, ok := m.Picks[PickUpdates]; ok {
		if on {
			return UpdatesInstall
		}
		return a.unticked()
	}
	return a.startMode()
}

// maps copies a map of picks.
func maps(p map[string]bool) map[string]bool {
	out := make(map[string]bool, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

// NewSession works out what installing a would do from this program,
// the one running. Uninstall says the session is for taking the
// installed program away.
func NewSession(a App, uninstall bool) (*Session, error) {
	if err := a.check(); err != nil {
		return nil, err
	}
	if !supported {
		return nil, ErrUnsupported
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	self = resolve(self)
	return sessionFor(a, self, uninstall)
}

// NewSessionFor is [NewSession] for the program at exe in place of the
// one running, as for a test, or a program that installs another it
// holds.
func NewSessionFor(a App, exe string, uninstall bool) (*Session, error) {
	if err := a.check(); err != nil {
		return nil, err
	}
	if !supported {
		return nil, ErrUnsupported
	}
	return sessionFor(a, resolve(exe), uninstall)
}

// sessionFor is NewSession for the program at self.
func sessionFor(a App, self string, uninstall bool) (*Session, error) {
	dir, err := a.dir()
	if err != nil {
		return nil, err
	}
	if a.Icon == nil && a.IconFunc != nil {
		a.Icon = a.IconFunc()
	}
	s := &Session{App: a, Dir: dir, Exe: filepath.Join(dir, a.exe()), self: self, Free: -1}
	m, err := readManifest(dir, a.id())
	if err != nil {
		return nil, err
	}
	var kept map[string]bool
	if m != nil {
		s.Have = m.installation(&a, dir)
		kept = m.Picks
	} else if exe := a.found(s.Exe); exe != "" {
		// Installed by hand, by an older installer that kept nothing, or
		// where an older version put it: what it has on the system is
		// what the user chose.
		kept = detect(&a, exe)
		s.Have = &Installation{Dir: filepath.Dir(exe), Exe: exe, Picks: maps(kept)}
	}
	s.Offers = a.offers(kept)
	switch {
	case uninstall:
		s.Mode = Remove
	case s.Have == nil:
		s.Mode = Fresh
	case s.Have.Version == "":
		// Installed before the installer kept its version: this copy
		// takes its place.
		s.Mode = Upgrade
	default:
		switch compare(a.Version, s.Have.Version) {
		case 1:
			s.Mode = Upgrade
		case -1:
			s.Mode = Downgrade
		default:
			s.Mode = Reinstall
		}
	}
	if !uninstall {
		s.Need, err = a.size(self)
		if err != nil {
			return nil, err
		}
		if s.Have != nil {
			// The program's data is there already, and counted against
			// what is free.
			s.Need -= a.Space
		}
		if free, err := freeSpace(existing(dir)); err == nil {
			s.Free = free
		}
	}
	return s, nil
}

// found is the program installed already: at exe, where it goes, or
// else at one of the places older versions put it. It is "" for none.
func (a *App) found(exe string) string {
	if fi, err := os.Stat(exe); err == nil && fi.Mode().IsRegular() {
		return exe
	}
	for _, f := range a.Formerly {
		if fi, err := os.Lstat(f); err == nil && fi.Mode().IsRegular() {
			return f
		}
	}
	return ""
}

// ours reports whether p is the program of install in: where it is,
// or where an older version put it. What the system holds that leads to
// another path, as a link or a shortcut of the same name, is another
// program's, and stays.
func (a *App) ours(in Installation, p string) bool {
	return samePath(p, in.Exe) || a.former(p)
}

// former reports whether exe is one of the places older versions put
// the program.
func (a *App) former(exe string) bool {
	for _, f := range a.Formerly {
		if samePath(f, exe) {
			return true
		}
	}
	return false
}

// existing is dir, or the nearest folder above it that is there.
func existing(dir string) string {
	for {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			return dir
		}
		dir = up
	}
}

// size is how many bytes installing a from self takes.
func (a *App) size(self string) (int64, error) {
	fi, err := os.Stat(self)
	if err != nil {
		return 0, err
	}
	n := fi.Size() + a.Space
	if a.Files != nil {
		err := fs.WalkDir(a.Files, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			n += info.Size()
			return nil
		})
		if err != nil {
			return 0, fmt.Errorf("install: App.Files: %w", err)
		}
	}
	return n, nil
}

// Room says what stands in the way of the install for want of space,
// or nothing.
func (s *Session) Room() error {
	if s.Free >= 0 && s.Need > s.Free {
		return fmt.Errorf("%s needs %s, and %s has %s free", s.App.Name, Bytes(s.Need), existing(s.Dir), Bytes(s.Free))
	}
	return nil
}

// Running lists the processes running the installed program, other
// than this one: where it goes, and where an older version put it, when
// it was found there. On Windows a running program's files cannot be
// replaced or taken away.
func (s *Session) Running() []int {
	pids, err := running(s.Exe)
	if err != nil {
		return nil
	}
	if s.Have != nil && !samePath(s.Have.Exe, s.Exe) {
		if more, err := running(s.Have.Exe); err == nil {
			pids = append(pids, more...)
		}
	}
	return slices.DeleteFunc(pids, func(pid int) bool { return pid == os.Getpid() })
}

// Install installs the program with the offers picked, telling progress
// how it goes. It returns the installation.
func (s *Session) Install(ctx context.Context, picks map[string]bool, progress func(Progress)) (Installation, error) {
	return s.installTo(ctx, picks, progress, true)
}

// installTo is Install, checking there is room first with room.
func (s *Session) installTo(ctx context.Context, picks map[string]bool, progress func(Progress), room bool) (Installation, error) {
	if progress == nil {
		progress = func(Progress) {}
	}
	if s.Mode == Remove {
		return Installation{}, errors.New("install: this session uninstalls")
	}
	if room {
		if err := s.Room(); err != nil {
			return Installation{}, err
		}
	}
	in := Installation{Dir: s.Dir, Exe: s.Exe, Version: s.App.Version, Picks: map[string]bool{}}
	for _, o := range s.Offers {
		on := o.On
		if v, ok := picks[o.Key]; ok {
			on = v
		}
		in.Picks[o.Key] = on
	}
	progress(Progress{Step: "Making room", Done: 0})
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return in, err
	}
	old, err := readManifest(s.Dir, s.App.id())
	if err != nil {
		return in, err
	}
	// How it takes updates: as it did, or as the program starts, and as
	// the offer says when the user ticked or unticked it.
	in.Updates = s.App.startMode()
	if old != nil {
		in.Updates = old.mode(&s.App)
	} else if s.Have != nil && s.Have.Updates.valid() {
		in.Updates = s.Have.Updates
	}
	if on, ok := picks[PickUpdates]; ok {
		switch {
		case on:
			in.Updates = UpdatesInstall
		case in.Updates == UpdatesInstall:
			in.Updates = s.App.unticked()
		}
	}
	if s.App.Updates != nil {
		in.Picks[PickUpdates] = in.Updates == UpdatesInstall
	}
	// A fresh install that stops before it has written what it put in
	// place takes it all back, so no half install is left to be found,
	// and taken on, at the program's next start.
	fresh := old == nil && s.Have == nil
	var placed []string
	registered := false
	fail := func(err error) (Installation, error) {
		if fresh {
			if registered {
				_ = unregister(&s.App, in)
			}
			for _, f := range placed {
				_ = os.Remove(f)
			}
			pruneEmpty(s.Dir)
			_ = os.Remove(s.Dir)
		}
		return in, err
	}
	progress(Progress{Step: "Copying " + s.App.Name, Done: 0.1})
	if !samePath(s.self, s.Exe) {
		// A copy put in place by hand ends a trial: the program it
		// replaces is the user's choice now, as is this one.
		_ = os.Remove(trialPath(s.Exe))
		if err = copyFile(ctx, s.self, s.Exe, func(f float32) {
			progress(Progress{Step: "Copying " + s.App.Name, Done: 0.1 + 0.5*f})
		}); err != nil {
			return fail(fmt.Errorf("copy %s to %s: %w", s.App.Name, s.Exe, err))
		}
		placed = append(placed, s.Exe)
	}
	files, err := s.App.copyFiles(ctx, s.Dir, func(f float32) {
		progress(Progress{Step: "Copying its files", Done: 0.6 + 0.15*f})
	})
	if err != nil {
		return fail(err)
	}
	for _, f := range files {
		placed = append(placed, filepath.Join(s.Dir, filepath.FromSlash(f)))
	}
	if old != nil {
		// Files an older version had that this one does not.
		for _, f := range old.Files {
			if !slices.Contains(files, f) {
				_ = os.Remove(filepath.Join(s.Dir, filepath.FromSlash(f)))
			}
		}
		pruneEmpty(s.Dir)
	}
	progress(Progress{Step: "Adding it to the system", Done: 0.8})
	registered = true
	if err := register(&s.App, in); err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	m := manifest{ID: s.App.id(), Version: s.App.Version, Exe: s.App.exe(), Files: files, Picks: in.Picks, Updates: in.Updates, When: time.Now().UTC()}
	if old != nil {
		m.Skip = old.Skip
	}
	if err := m.write(s.Dir); err != nil {
		return fail(err)
	}
	if s.App.Installed != nil {
		progress(Progress{Step: "Setting it up", Done: 0.9})
		if err := s.App.Installed(ctx, in); err != nil {
			return in, fmt.Errorf("%w: %w", errSetUp, err)
		}
	}
	progress(Progress{Step: "Done", Done: 1})
	s.Have = &in
	return in, nil
}

// Uninstall takes the program away: its shortcuts, its entries, its
// files, and, with data, the folders of the user's data it named. The
// program's own file goes once this program has ended, where the system
// keeps a running program's file.
func (s *Session) Uninstall(ctx context.Context, data bool, progress func(Progress)) error {
	if progress == nil {
		progress = func(Progress) {}
	}
	in := Installation{Dir: s.Dir, Exe: s.Exe, Picks: map[string]bool{}}
	if s.Have != nil {
		in = *s.Have
	}
	if s.App.Uninstalling != nil {
		progress(Progress{Step: "Getting it ready", Done: 0.05})
		if err := s.App.Uninstalling(ctx, in); err != nil {
			return err
		}
	}
	progress(Progress{Step: "Taking it off the system", Done: 0.2})
	if err := unregister(&s.App, in); err != nil {
		return err
	}
	progress(Progress{Step: "Removing its files", Done: 0.5})
	m, err := readManifest(s.Dir, s.App.id())
	if err != nil {
		return err
	}
	var files []string
	if m != nil {
		for _, f := range m.Files {
			files = append(files, filepath.Join(s.Dir, filepath.FromSlash(f)))
		}
	}
	files = append(files, s.Exe, filepath.Join(s.Dir, manifestName))
	if s.Have != nil && !samePath(s.Have.Exe, s.Exe) {
		// Where an older version put it.
		files = append(files, s.Have.Exe)
	}
	// And what replacing them left beside them.
	for _, f := range slices.Clone(files) {
		files = append(files, f+".old", f+".new", f+".back", trialPath(f))
		files = append(files, leftovers(f, ".new")...)
		files = append(files, leftovers(f, ".old")...)
	}
	if err := removeFiles(files, s.Dir); err != nil {
		return err
	}
	if data {
		progress(Progress{Step: "Removing your data", Done: 0.8})
		for _, d := range s.App.Data {
			if err := removeData(d); err != nil {
				return err
			}
		}
	}
	progress(Progress{Step: "Done", Done: 1})
	s.Have = nil
	return nil
}

// removeData takes the folder d of the user's data away, unless it is a
// folder no program's data can be, such as the home folder.
func removeData(d string) error {
	if d == "" {
		return nil
	}
	d = filepath.Clean(d)
	if !filepath.IsAbs(d) {
		return fmt.Errorf("install: will not remove %s, which is not a full path", d)
	}
	home, _ := os.UserHomeDir()
	cfg, _ := os.UserConfigDir()
	cache, _ := os.UserCacheDir()
	for _, keep := range []string{home, cfg, cache, filepath.Dir(home)} {
		if keep != "" && samePath(d, keep) {
			return fmt.Errorf("install: will not remove %s, which holds more than this program's data", d)
		}
	}
	if len(strings.Split(filepath.ToSlash(d), "/")) < 3 {
		return fmt.Errorf("install: will not remove %s, which is too near the top", d)
	}
	return os.RemoveAll(d)
}

// copyFiles writes a's Files into dir, each whole or not at all, and
// returns their names, as slash paths.
func (a *App) copyFiles(ctx context.Context, dir string, progress func(float32)) ([]string, error) {
	if a.Files == nil {
		return nil, nil
	}
	var names []string
	err := fs.WalkDir(a.Files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if p == manifestName || path.Base(p) == a.exe() && path.Dir(p) == "." {
			return fmt.Errorf("install: App.Files holds %s, which the installer writes itself", p)
		}
		names = append(names, p)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("install: App.Files: %w", err)
	}
	sort.Strings(names)
	for i, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, err := fs.ReadFile(a.Files, name)
		if err != nil {
			return nil, err
		}
		to := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return nil, err
		}
		mode := os.FileMode(0o644)
		if info, err := fs.Stat(a.Files, name); err == nil && info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		if err := writeFile(to, raw, mode); err != nil {
			return nil, fmt.Errorf("write %s: %w", to, err)
		}
		progress(float32(i+1) / float32(len(names)))
	}
	return names, nil
}

// writeFile writes data to the file at to, through one beside it that
// is moved into place, so it holds the old bytes or the new, never
// part.
func writeFile(to string, data []byte, mode os.FileMode) error {
	return writeVia(to, mode, Replace, func(f *os.File) error {
		_, err := f.Write(data)
		return err
	})
}

// writeVia writes the file at to with write, through a file of its own
// beside it, which is put on the disk and moved into place with place,
// so to holds the old bytes or the new, never part. Each write has its own
// file, so two at once, as an update and an installer, never mix.
func writeVia(to string, mode os.FileMode, place func(part, to string) error, write func(*os.File) error) error {
	f, err := os.CreateTemp(filepath.Dir(to), "."+filepath.Base(to)+".*.new")
	if err != nil {
		return err
	}
	part := f.Name()
	err = f.Chmod(mode)
	if err == nil {
		err = write(f)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = place(part, to)
	}
	if err != nil {
		_ = os.Remove(part)
	}
	return err
}

// leftovers are the files of their own names, ending in suffix, that
// writes into file, or moves aside of it, left beside it: ".new" for a
// write the program ended in the middle of, ".old" for a program moved
// out of the way while it ran.
func leftovers(file, suffix string) []string {
	var out []string
	dir, base := filepath.Split(file)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, "."+base+".") && strings.HasSuffix(n, suffix) {
			out = append(out, filepath.Join(dir, n))
		}
	}
	return out
}

// copyFile copies the program at from to to, through a file beside to
// that is moved into place.
func copyFile(ctx context.Context, from, to string, progress func(float32)) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	fi, err := src.Stat()
	if err != nil {
		return err
	}
	return writeVia(to, 0o755, Replace, func(dst *os.File) error {
		buf := make([]byte, 1<<20)
		var done int64
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, rerr := src.Read(buf)
			if n > 0 {
				if _, err := dst.Write(buf[:n]); err != nil {
					return err
				}
				done += int64(n)
				if fi.Size() > 0 {
					progress(float32(done) / float32(fi.Size()))
				}
			}
			if rerr == io.EOF {
				return nil
			}
			if rerr != nil {
				return rerr
			}
		}
	})
}

// Replace puts the file at part in place of the one at to. Where a
// running program's file cannot be written over, as on Windows, the old
// one is moved aside to to.old first, which [CleanOld] takes away
// later, and moved back if the new one cannot be put in place, so to
// is never left missing.
func Replace(part, to string) error { return replaceKeeping(part, to, false) }

// replaceKeeping is Replace of the file at to, which with keep leaves
// to.old as it is, for an update to give way to: where the program is
// moved aside, it goes to a name of its own, which CleanOld takes away.
func replaceKeeping(part, to string, keep bool) error {
	old := ""
	if movesAside {
		if _, err := os.Stat(to); err == nil {
			if old, err = moveAside(to, keep); err != nil {
				return err
			}
		}
	}
	if err := os.Rename(part, to); err != nil {
		if old != "" {
			_ = os.Rename(old, to)
		}
		return fmt.Errorf("put %s in place: %w", filepath.Base(to), err)
	}
	return nil
}

// moveAside moves the file at file to file.old, for Replace, or, with
// keep, to a name of its own beside it, and returns where. One at
// file.old that cannot go, as the program the last update moved aside
// while it still runs, as from the tray, is moved out of the way first,
// under a name of its own, which CleanOld takes away once it can.
func moveAside(file string, keep bool) (string, error) {
	aside := func() string {
		return filepath.Join(filepath.Dir(file), fmt.Sprintf(".%s.%d.old", filepath.Base(file), time.Now().UnixNano()))
	}
	old := file + ".old"
	if keep {
		old = aside()
	} else if err := os.Remove(old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		if err := os.Rename(old, aside()); err != nil {
			return "", fmt.Errorf("move %s aside: %w", filepath.Base(old), err)
		}
	}
	if err := os.Rename(file, old); err != nil {
		return "", fmt.Errorf("move %s aside: %w", filepath.Base(file), err)
	}
	return old, nil
}

// CleanOld takes away the copy a replace moved aside from exe, once
// the program that ran from it has gone, and those moveAside moved out
// of its way.
func CleanOld(exe string) {
	_ = os.Remove(exe + ".old")
	for _, f := range leftovers(exe, ".old") {
		_ = os.Remove(f)
	}
}

// pruneEmpty takes away the empty folders under dir, deepest first.
func pruneEmpty(dir string) {
	var dirs []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && p != dir {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i])
	}
}

// samePath reports whether two paths name one file, letter case aside
// where the system ignores it, and through links.
func samePath(a, b string) bool {
	a, b = resolve(a), resolve(b)
	if caseless {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// resolve is path cleaned, with its links resolved where it exists.
func resolve(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}

// Bytes is n bytes as a person reads it: "18 MB", "1.2 GB".
func Bytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d bytes", n)
	}
	f, suffix := float64(n), ""
	for _, s := range []string{"kB", "MB", "GB", "TB"} {
		f /= unit
		suffix = s
		if f < unit {
			break
		}
	}
	if f < 10 {
		return fmt.Sprintf("%.1f %s", f, suffix)
	}
	return fmt.Sprintf("%.0f %s", f, suffix)
}
