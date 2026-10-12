//go:build linux && !android

package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
)

// What's new is the notes of the releases after one version up to
// another, newest first; a release with no program for this system
// still has its notes.
func TestWhatsNew(t *testing.T) {
	srv := fakeGitHub(t,
		release{tag: "v1.1.0", notes: "eleven", files: map[string][]byte{asset("1.1.0"): []byte("a")}},
		release{tag: "v1.3.0", notes: "thirteen", files: map[string][]byte{asset("1.3.0"): []byte("c")}},
		release{tag: "v1.2.0", notes: "twelve", files: map[string][]byte{"elsewhere": []byte("b")}},
		release{tag: "v1.4.0-beta.1", prerelease: true, notes: "beta", files: map[string][]byte{asset("1.4.0-beta.1"): []byte("d")}},
	)
	a := App{Name: "studio", Version: "v1.3.0", Updates: GitHub{Repo: "marrasen/studio", API: srv.URL}}
	notes, err := WhatsNew(context.Background(), a, "v1.1.0", "v1.3.0")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(notes))
	for _, n := range notes {
		got = append(got, n.Version+":"+n.Notes)
	}
	if !slices.Equal(got, []string{"v1.3.0:thirteen", "v1.2.0:twelve"}) {
		t.Fatalf("what's new is %q", got)
	}
	if doc := joinNotes(notes); !strings.HasPrefix(doc, "## 1.3.0\n\nthirteen\n\n## 1.2.0") {
		t.Fatalf("the notes read %q", doc)
	}
}

// A download says how far it has got, and then that it checks it.
func TestStageReportsProgress(t *testing.T) {
	dir := t.TempDir()
	srv := fakeGitHub(t, release{tag: "v2.0.0", files: map[string][]byte{asset("2.0.0"): []byte(strings.Repeat("two", 1000))}})
	a := App{Name: "studio", Version: "v1.0.0", Dir: func() (string, error) { return dir, nil },
		Updates: GitHub{Repo: "marrasen/studio", API: srv.URL}, UpdateKey: testKey}
	r, _, err := Check(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	var steps []string
	last := float32(-1)
	err = stageReporting(context.Background(), a, r, func(p Progress) {
		if p.Done < last {
			t.Errorf("the progress went back from %v to %v", last, p.Done)
		}
		last = p.Done
		steps = append(steps, p.Step)
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(steps, "|")
	if !strings.Contains(joined, "Downloading 3.0 kB of 3.0 kB") || !strings.Contains(joined, "Checking it") ||
		!strings.HasSuffix(joined, "Putting it in place") {
		t.Fatalf("the steps were %q", steps)
	}
}

// offscreenUpdate runs an update's window offscreen.
func offscreenUpdate(t *testing.T, a App, u Update, page string) (*gunim.Window, *updater) {
	t.Helper()
	w := gunimtest.New(t, geom.Sz(640, updateHeight), &gunim.Box{})
	sc := updateScene(&a, page, a.Version, u.Release.Version)
	if page == pageNotes {
		sc = updateScene(&a, page, "v1.0.0", a.Version)
	}
	sc.Ready, sc.Restart = u.Ready, u.Quit != nil
	c, err := mountStage(w, &a, nil, accentFor(&a), sc)
	if err != nil {
		t.Fatal(err)
	}
	r := newUpdater(a, c, sc)
	r.u = u
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r.ctx = ctx
	return w, r
}

// frames draws n frames of w, taking r's intents and reports between.
func updateFrames(w *gunim.Window, r *updater, n int) {
	for range n {
		for more := true; more; {
			select {
			case fn := <-r.events:
				fn()
			case ev := <-r.c.Intents():
				r.handle(ev.Intent)
			default:
				more = false
			}
		}
		w.Frame(time.Second / 60)
	}
}

// The window shows what's new, downloads the release with its progress
// on Update Now, puts it in place, starts it in this one's place, and
// asks the program to end.
func TestUpdateWindowUpdatesAndRestarts(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "studio")
	if err := os.WriteFile(exe, []byte("one"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := fakeGitHub(t,
		release{tag: "v2.0.0", notes: "**Faster.** It is.", files: map[string][]byte{asset("2.0.0"): []byte("two")}},
		release{tag: "v1.0.0", notes: "First.", files: map[string][]byte{asset("1.0.0"): []byte("one")}},
	)
	a := App{Name: "studio", Version: "v1.0.0", Dir: func() (string, error) { return dir, nil },
		Updates: GitHub{Repo: "marrasen/studio", API: srv.URL}, UpdateKey: testKey}
	rel, _, err := Check(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	var quits atomic.Int32
	w, r := offscreenUpdate(t, a, Update{Release: rel, Quit: func() error { quits.Add(1); return nil }}, pageUpdate)
	var started []string
	r.launch = func(exe string, _ []string, env ...string) error {
		started = append(started, exe+" "+strings.Join(env, " "))
		return nil
	}
	r.pid = 4242
	r.readNotes("v1.0.0", "v2.0.0")
	until(t, func() bool { updateFrames(w, r, 1); return !r.sc.NotesLoading })
	if r.sc.Notes != "## 2.0.0\n\n**Faster.** It is." || r.sc.NotesErr != "" {
		t.Fatalf("the notes are %q (%q)", r.sc.Notes, r.sc.NotesErr)
	}
	updateFrames(w, r, 30)

	r.handle(updateNow{})
	until(t, func() bool { updateFrames(w, r, 1); return r.left })
	if raw, _ := os.ReadFile(exe); string(raw) != "two" {
		t.Fatalf("the program holds %q", raw)
	}
	if !slices.Equal(started, []string{exe + " " + Env + "=restart:4242"}) || quits.Load() != 1 {
		t.Fatalf("started %q, and asked the program to end %d times", started, quits.Load())
	}
}

// With another copy of the program running, Update Now first says it would
// go on as the release before, and puts nothing in place; pressed again,
// the update goes ahead.
func TestUpdateWindowSaysAnotherCopyRuns(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "studio")
	if err := os.WriteFile(exe, []byte("one"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := fakeGitHub(t,
		release{tag: "v2.0.0", notes: "Faster.", files: map[string][]byte{asset("2.0.0"): []byte("two")}},
		release{tag: "v1.0.0", notes: "First.", files: map[string][]byte{asset("1.0.0"): []byte("one")}},
	)
	a := App{Name: "studio", Version: "v1.0.0", Dir: func() (string, error) { return dir, nil },
		Updates: GitHub{Repo: "marrasen/studio", API: srv.URL}, UpdateKey: testKey}
	rel, _, err := Check(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	w, r := offscreenUpdate(t, a, Update{Release: rel, Quit: func() error { return nil }}, pageUpdate)
	var asked []string
	r.others = func(exe string) int { asked = append(asked, exe); return 1 }
	var started int
	r.launch = func(string, []string, ...string) error { started++; return nil }
	updateFrames(w, r, 5)

	r.handle(updateNow{})
	updateFrames(w, r, 10)
	if r.sc.Page != pageUpdate || r.sc.Others != 1 || started != 0 || r.working {
		t.Fatalf("with another copy running, Update Now went to %q, others %d, started %d", r.sc.Page, r.sc.Others, started)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "one" {
		t.Fatalf("the program was replaced before the user agreed: %q", raw)
	}
	if !slices.Equal(asked, []string{exe}) {
		t.Fatalf("asked after the copies of %v, want %s", asked, exe)
	}
	if !strings.Contains(othersLine(r.sc), "Another copy of studio is running") || !strings.Contains(othersLine(r.sc), "1.0.0") {
		t.Fatalf("the window says %q", othersLine(r.sc))
	}

	r.handle(updateNow{})
	until(t, func() bool { updateFrames(w, r, 1); return r.left })
	if raw, _ := os.ReadFile(exe); string(raw) != "two" || started != 1 {
		t.Fatalf("pressed again, the program holds %q and %d copies started", raw, started)
	}
}

// A release in place already restarts at once; with no way to end the
// program, the window says it starts next time.
func TestUpdateWindowReadyAndNoRestart(t *testing.T) {
	a := App{Name: "studio", Version: "v1.0.0", Dir: func() (string, error) { return t.TempDir(), nil },
		Updates: GitHub{Repo: "marrasen/studio", API: "http://127.0.0.1:1"}, UpdateKey: testKey}
	w, r := offscreenUpdate(t, a, Update{Release: Release{Version: "v2.0.0"}, Ready: true}, pageUpdate)
	r.launch = func(string, []string, ...string) error {
		t.Error("started a copy with no way to end this one")
		return nil
	}
	r.readNotes("v1.0.0", "v2.0.0")
	until(t, func() bool { updateFrames(w, r, 1); return !r.sc.NotesLoading })
	if r.sc.NotesErr == "" {
		t.Fatal("notes that could not be read said nothing")
	}
	r.handle(updateNow{})
	updateFrames(w, r, 30)
	if r.sc.Page != pageUpdate || r.left {
		t.Fatalf("on %q, left %v", r.sc.Page, r.left)
	}
}

// What's new shows the notes, and Close closes it.
func TestWhatsNewWindow(t *testing.T) {
	srv := fakeGitHub(t, release{tag: "v1.1.0", notes: "- [Page](https://example.com)", files: map[string][]byte{asset("1.1.0"): []byte("a")}})
	a := App{Name: "studio", Version: "v1.1.0", Updates: GitHub{Repo: "marrasen/studio", API: srv.URL}}
	w, r := offscreenUpdate(t, a, Update{}, pageNotes)
	r.readNotes("v1.0.0", "v1.1.0")
	until(t, func() bool { updateFrames(w, r, 1); return !r.sc.NotesLoading })
	updateFrames(w, r, 30)
	if r.sc.Notes == "" || r.sc.Have != "1.0.0" {
		t.Fatalf("the scene is %+v", r.sc)
	}
	r.handle(closed{})
	if !r.left {
		t.Fatal("Close left the window open")
	}
}

// The new copy waits for the program to end, says it is up to date,
// and closes for the program to start; closed while it waits, it starts
// nothing.
func TestRestarterWaitsThenSaysUpToDate(t *testing.T) {
	updatedFor = 10 * time.Millisecond
	watchEvery = 5 * time.Millisecond
	t.Cleanup(func() { updatedFor, watchEvery = 1800*time.Millisecond, 400*time.Millisecond })
	a := App{Name: "studio", Version: "v2.0.0"}
	for _, cancelIt := range []bool{false, true} {
		w := gunimtest.New(t, geom.Sz(640, 600), &gunim.Box{})
		sc := updateScene(&a, pageRestarting, "", a.Version)
		c, err := mountStage(w, &a, nil, accentFor(&a), sc)
		if err != nil {
			t.Fatal(err)
		}
		var gone atomic.Bool
		r := &restarter{a: a, c: c, sc: sc, events: make(chan func(), 16), alive: func() bool { return !gone.Load() },
			adopted: make(chan struct{})}
		ctx, cancel := context.WithCancel(context.Background())
		r.ctx = ctx
		r.watch()
		frames := func(n int) {
			for range n {
				for more := true; more; {
					select {
					case fn := <-r.events:
						fn()
					case ev := <-c.Intents():
						r.handle(ev.Intent)
					default:
						more = false
					}
				}
				w.Frame(time.Second / 60)
			}
		}
		frames(20)
		if r.sc.Page != pageRestarting {
			t.Fatalf("waiting, the window is on %q", r.sc.Page)
		}
		if cancelIt {
			r.handle(closed{})
			gone.Store(true)
			frames(20)
			if !r.cancelled || r.finishing {
				t.Fatalf("closed while waiting: cancelled %v, finishing %v", r.cancelled, r.finishing)
			}
			cancel()
			continue
		}
		gone.Store(true)
		until(t, func() bool { frames(1); return r.sc.Page == pageUpdated })
		if r.cancelled {
			t.Fatal("the restart was cancelled")
		}
		cancel()
	}
}

func TestRestartPID(t *testing.T) {
	for env, want := range map[string]int{"restart:12": 12, "restart:": 0, "restart:x": 0, "show": 0, "restart:-3": 0} {
		if pid, ok := restartPID(env); pid != want && ok || ok != (want > 0) {
			t.Errorf("restartPID(%q) = %d, %v", env, pid, ok)
		}
	}
}

// until runs step until it reports true, or fails after five seconds.
func until(t *testing.T, step func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
		if step() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("waited five seconds")
}

// A second copy started for the same restart leaves it to the first.
func TestOneCopyWaitsForTheRestart(t *testing.T) {
	self := filepath.Join(t.TempDir(), "studio")
	unlock, ok := lockRestart(self)
	if !ok {
		t.Fatal("the first copy was turned away")
	}
	unlock()
	// Held by another process that runs, the test's parent.
	if err := os.WriteFile(self+".restarting", []byte(strconv.Itoa(os.Getppid())), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, second := lockRestart(self); second {
		t.Fatal("a second copy waits too")
	}
	// One left by a copy that has ended is taken.
	if err := os.WriteFile(self+".restarting", []byte("999999999"), 0o644); err != nil {
		t.Fatal(err)
	}
	unlock, ok = lockRestart(self)
	if !ok {
		t.Fatal("a lock left behind keeps the restart from going on")
	}
	unlock()
}

// A process is known by when it started.
func TestWatchProcess(t *testing.T) {
	alive, done := watchProcess(os.Getpid())
	defer done()
	if !alive() {
		t.Fatal("this process is not alive")
	}
	if gone, _ := watchProcess(999999999); gone() {
		t.Fatal("a process that never was is alive")
	}
}

// The window about the program checks for updates with the window open,
// and says how it went there: up to date, a failure, or a newer release,
// which turns the window to it.
func TestAboutChecksForUpdatesInPlace(t *testing.T) {
	a := App{Name: "studio", Version: "v1.0.0", Updates: GitHub{Repo: "marrasen/studio", API: "http://127.0.0.1:1"}}
	w := gunimtest.New(t, geom.Sz(640, updateHeight), &gunim.Box{})
	sc := updateScene(&a, pageAbout, "", a.Version)
	c, err := mountStage(w, &a, nil, accentFor(&a), sc)
	if err != nil {
		t.Fatal(err)
	}
	r := newUpdater(a, c, sc)
	r.about = &About{Quit: func() error { return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r.ctx = ctx
	answers := []struct {
		rel   Release
		newer bool
		err   error
	}{{err: errors.New("offline")}, {rel: Release{Version: "v1.0.0"}}, {rel: Release{Version: "v1.1.0"}, newer: true}}
	r.check = func(context.Context, App) (Release, bool, error) {
		next := answers[0]
		answers = answers[1:]
		return next.rel, next.newer, next.err
	}
	for _, want := range []string{"Couldn't check: offline", "studio is up to date."} {
		r.handle(checkNow{})
		if !r.sc.Checking {
			t.Fatal("the check says nothing while it runs")
		}
		until(t, func() bool { updateFrames(w, r, 1); return !r.sc.Checking })
		if r.sc.Status != want || r.sc.Page != pageAbout || r.left {
			t.Fatalf("the check says %q on %q, want %q", r.sc.Status, r.sc.Page, want)
		}
	}
	r.handle(checkNow{})
	until(t, func() bool { updateFrames(w, r, 1); return r.sc.Page == pageUpdate })
	if r.sc.Have != "1.0.0" || r.sc.Version != "1.1.0" || !r.sc.Restart || r.u.Release.Version != "v1.1.0" {
		t.Fatalf("a newer release shows as %+v", r.sc)
	}
	updateFrames(w, r, 30)
}

// countingNotes counts the times its notes are read.
type countingNotes struct{ reads *int }

func (c countingNotes) Latest(context.Context) (Release, error) { return Release{}, nil }

func (c countingNotes) ReleaseNotes(context.Context) ([]ReleaseNotes, error) {
	*c.reads++
	return []ReleaseNotes{{Version: "v1.1.0", Notes: "new"}}, nil
}

// Notes read once are kept a while, so a window opened again soon does
// not ask again.
func TestNotesAreKeptAWhile(t *testing.T) {
	was := notesFor
	t.Cleanup(func() { notesFor = was })
	reads := 0
	a := App{Name: "studio", Version: "v1.1.0", Updates: countingNotes{reads: &reads}}
	for range 3 {
		if notes, err := WhatsNew(context.Background(), a, "v1.0.0", "v1.1.0"); err != nil || len(notes) != 1 {
			t.Fatalf("what's new is %v, %v", notes, err)
		}
	}
	if reads != 1 {
		t.Fatalf("the notes were read %d times", reads)
	}
	notesFor = 0
	if _, err := WhatsNew(context.Background(), a, "v1.0.0", "v1.1.0"); err != nil || reads != 2 {
		t.Fatalf("kept past their time, the notes were read %d times, %v", reads, err)
	}
}
