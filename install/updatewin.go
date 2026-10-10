package install

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/markdown"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/widget"
)

// An update the user takes in the installer's window. The program
// running opens it with [ShowUpdate] when a newer release is out: what
// the releases since its own changed, and Update Now. The release
// downloads with its progress shown, and the program restarts into it.
// Its new copy starts first, with Env saying which process it replaces,
// and opens the same window: it waits for the program to end, finishes
// the update, says the program is up to date, and runs it. A program
// updated by itself, with no window, says so on its next start, and
// [ShowWhatsNew] shows what the update brought.

// The pages of an update.
const (
	pageUpdate     = "update"     // what's new, and update now?
	pageNotes      = "notes"      // what the last update brought
	pageRestarting = "restarting" // the program closes and starts again
	pageUpdated    = "updated"    // the new release runs
	pageAbout      = "about"      // the program, what its releases changed, and a check for updates
)

// restartPrefix starts Env's value for the copy an update starts in the
// place of the program running: "restart:<pid>", the process it waits
// for to end.
const restartPrefix = "restart:"

// updateHeight is how tall the window of an update is: taller than the
// installer's, for the notes.
const updateHeight = 660

// Update is a newer release a program offers the user in the
// installer's window, as [App.Available] and [App.Updated] tell of one.
type Update struct {
	Release Release
	// Ready says the release is in place already, as App.Updated tells:
	// the window offers the restart into it alone.
	Ready bool
	// Exe is the program the release replaces: "" for the installed
	// one, or the path of a copy that is not installed and updates
	// itself where it is, as [StageTo] does.
	Exe string
	// Quit asks the program to end, for the restart into the release,
	// once its new copy has started; the new copy waits for it to end,
	// and says so. It runs on a goroutine of its own, and an error says
	// the program will not end. Nil, the window offers no restart, and
	// the release starts the next time the program does.
	Quit func() error
}

// ShowUpdate opens the installer's window on app for u, in a's look,
// and serves it in the background until it closes or ctx ends: what the
// releases after this one changed, up to u's, and Update Now, or
// Restart Now for a release in place already. The release downloads
// with its progress shown, and then the program restarts into it.
func ShowUpdate(ctx context.Context, app *gunim.App, a App, u Update) error {
	if err := a.check(); err != nil {
		return err
	}
	if !supported {
		// No installer here to wait for the program to end: the
		// release starts the next time the program does.
		u.Quit = nil
	}
	sc := updateScene(&a, pageUpdate, a.Version, u.Release.Version)
	sc.Ready, sc.Restart = u.Ready, u.Quit != nil
	c, err := openStage(app, &a, "Update "+a.Name, updateHeight, sc)
	if err != nil {
		return err
	}
	r := newUpdater(a, c, sc)
	r.u = u
	r.start(ctx, a.Version, u.Release.Version)
	return nil
}

// ShowWhatsNew opens a window on app, in a's look, with what the
// releases after version from changed, up to this one, as after an
// update, and serves it in the background until it closes or ctx ends.
func ShowWhatsNew(ctx context.Context, app *gunim.App, a App, from string) error {
	if err := a.check(); err != nil {
		return err
	}
	sc := updateScene(&a, pageNotes, from, a.Version)
	c, err := openStage(app, &a, "What's New in "+a.Name, updateHeight, sc)
	if err != nil {
		return err
	}
	newUpdater(a, c, sc).start(ctx, from, a.Version)
	return nil
}

// About is how the window [ShowAbout] opens takes a newer release it
// finds: as [Update] does, by the program's Quit, and in place of Exe.
type About struct {
	Quit func() error
	Exe  string
}

// ShowAbout opens a window on app about the program a describes, in the
// installer's look, and serves it in the background until it closes or
// ctx ends: its name, version and maker, what each of its releases
// changed, and Check for Updates. The check runs with the window open,
// and says how it went there; a newer release turns the window to it, to
// update now as [ShowUpdate] does.
func ShowAbout(ctx context.Context, app *gunim.App, a App, o About) error {
	if err := a.check(); err != nil {
		return err
	}
	sc := updateScene(&a, pageAbout, "", a.Version)
	sc.Description = a.Description
	c, err := openStage(app, &a, "About "+a.Name, updateHeight, sc)
	if err != nil {
		return err
	}
	r := newUpdater(a, c, sc)
	r.about = &o
	r.start(ctx, "", a.Version)
	return nil
}

// updatedFrom is the version the update finished as this program
// started replaced.
var updatedFrom string

// UpdatedFrom is the version an update replaced, on the first start of
// the release it put in place by itself, and "" otherwise: for a program
// to say it was updated, and offer [ShowWhatsNew]. An update taken
// through [ShowUpdate] showed what was new before it began, and says
// nothing here.
func UpdatedFrom() string { return updatedFrom }

// updateScene is the scene of an update's window on page, from version
// from to version to.
func updateScene(a *App, page, from, to string) scene {
	return scene{
		Name: a.Name, Version: strings.TrimPrefix(to, "v"), Have: strings.TrimPrefix(from, "v"),
		Publisher: a.Publisher, Mode: Upgrade, Page: page, System: runtime.GOOS,
		Quit: a.Quit != nil, Updating: true, NotesLoading: page == pageUpdate || page == pageNotes,
	}
}

// updater is the application half of an update's window.
type updater struct {
	a      App
	u      Update
	c      gunim.Client
	sc     scene
	logged logged
	// ctx ends the window, and events are the work's reports, run on
	// serve's goroutine.
	ctx    context.Context
	events chan func()
	// working says the download runs, stopWork stops it, and leaving
	// that Close stopped it, and the window closes once it has.
	working, leaving bool
	stopWork         context.CancelFunc
	// launch starts the new copy, and pid is this process; a test sets
	// them.
	launch func(exe string, args []string, env ...string) error
	pid    int
	// left says the window was left; a test reads it.
	left bool
	// about is how a newer release the page about the program finds is
	// taken, on that page.
	about *About
	// check asks for the newest release; a test sets it.
	check func(ctx context.Context, a App) (Release, bool, error)
}

func newUpdater(a App, c gunim.Client, sc scene) *updater {
	return &updater{a: a, c: c, sc: sc, events: make(chan func(), 16), launch: launch, pid: os.Getpid(), check: Check}
}

// start serves the window, and reads the notes of the releases after
// from up to to.
func (r *updater) start(ctx context.Context, from, to string) {
	r.ctx = ctx
	go func() { _ = r.serve() }()
	r.readNotes(from, to)
}

func (r *updater) show() {
	r.logged.log("update", r.sc)
	_ = r.c.Update("installer", r.sc)
}

func (r *updater) leave() {
	r.left = true
	r.c.Leave()
}

// send runs fn on serve's goroutine, unless the window has closed.
func (r *updater) send(fn func()) {
	select {
	case r.events <- fn:
	case <-r.ctx.Done():
	}
}

func (r *updater) serve() error {
	for {
		select {
		case <-r.ctx.Done():
			r.c.Leave()
			return r.ctx.Err()
		case fn := <-r.events:
			fn()
		case ev, ok := <-r.c.Intents():
			if !ok {
				if r.stopWork != nil {
					r.stopWork()
				}
				return nil
			}
			r.handle(ev.Intent)
		}
	}
}

func (r *updater) handle(in gunim.Intent) {
	switch in := in.(type) {
	case closed:
		if r.working {
			if r.leaving {
				r.leave()
				return
			}
			r.leaving = true
			r.stopWork()
			r.sc.Step = "Stopping"
			r.show()
			return
		}
		r.leave()
	case updateNow:
		if r.working || r.sc.Page != pageUpdate {
			return
		}
		r.updateNow()
	case retried:
		r.sc.Page, r.sc.Problem = pageUpdate, ""
		r.show()
	case checkNow:
		if r.sc.Page != pageAbout || r.sc.Checking {
			return
		}
		r.checkNow()
	case markdown.Link:
		openLink(r.c, in.URL)
	}
}

// readNotes reads what the releases after from changed, up to to, in
// the background, and shows them.
func (r *updater) readNotes(from, to string) {
	go func() {
		notes, err := WhatsNew(r.ctx, r.a, from, to)
		r.send(func() {
			r.sc.NotesLoading = false
			if err != nil {
				r.sc.NotesErr = err.Error()
			} else {
				r.sc.Notes = joinNotes(notes)
			}
			r.show()
		})
	}()
}

// checkNow asks for the newest release, with the window open, and says
// how it went there; a newer one turns the window to it.
func (r *updater) checkNow() {
	r.sc.Checking, r.sc.Trouble, r.sc.Status = true, false, "Checking for updates…"
	r.show()
	check := r.check
	go func() {
		rel, newer, err := check(r.ctx, r.a)
		r.send(func() {
			r.sc.Checking = false
			switch {
			case err != nil:
				r.sc.Trouble, r.sc.Status = true, "Couldn't check: "+err.Error()
			case newer && r.about != nil:
				r.found(rel)
				return
			case !IsRelease(r.a.Version):
				r.sc.Status = "The newest release is " + strings.TrimPrefix(rel.Version, "v") + "; this build is none."
			default:
				r.sc.Status = r.a.Name + " is up to date."
			}
			r.show()
		})
	}()
}

// found turns the window to the newer release rel, to update now.
func (r *updater) found(rel Release) {
	r.u = Update{Release: rel, Quit: r.about.Quit, Exe: r.about.Exe}
	if !supported {
		r.u.Quit = nil
	}
	r.sc.Page, r.sc.Status = pageUpdate, ""
	r.sc.Have, r.sc.Version = strings.TrimPrefix(r.a.Version, "v"), strings.TrimPrefix(rel.Version, "v")
	r.sc.Ready, r.sc.Restart = false, r.u.Quit != nil
	r.sc.NotesLoading, r.sc.Notes, r.sc.NotesErr = true, "", ""
	r.show()
	r.readNotes(r.a.Version, rel.Version)
}

// joinNotes is the notes of releases, newest first, as one document,
// each under its version.
func joinNotes(notes []ReleaseNotes) string {
	var b strings.Builder
	for i, n := range notes {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("## " + strings.TrimPrefix(n.Version, "v") + "\n\n")
		if body := strings.TrimSpace(n.Notes); body != "" {
			b.WriteString(body)
		} else {
			b.WriteString("_No notes._")
		}
	}
	return b.String()
}

// updateNow downloads the release and puts it in place, showing how far
// it has got, and then restarts into it.
func (r *updater) updateNow() {
	if r.u.Ready {
		r.restart()
		return
	}
	ctx, stop := context.WithCancel(r.ctx)
	r.working, r.leaving, r.stopWork = true, false, stop
	r.sc.Page, r.sc.Step, r.sc.Progress, r.sc.Problem = pageWorking, "Starting", 0, ""
	r.show()
	go func() {
		defer stop()
		report := func(p Progress) {
			r.send(func() {
				if r.working && !r.leaving {
					r.sc.Step, r.sc.Progress = p.Step, p.Done
					r.show()
				}
			})
		}
		var err error
		if r.u.Exe == "" {
			err = stageReporting(ctx, r.a, r.u.Release, report)
		} else {
			err = stageToReporting(ctx, r.a, r.u.Release, r.u.Exe, report)
		}
		r.send(func() {
			r.working = false
			switch {
			case r.leaving:
				r.leave()
			case err != nil:
				r.sc.Page, r.sc.Problem = pageFailed, err.Error()
				r.show()
			default:
				r.u.Ready, r.sc.Ready = true, true
				r.sc.Progress = 1
				r.restart()
			}
		})
	}()
}

// restart starts the release put in place, which waits for this
// program to end, and asks the program to end. Without a way to end
// it, the window says the release starts next time.
func (r *updater) restart() {
	if r.u.Quit == nil {
		r.sc.Page = pageUpdate
		r.show()
		return
	}
	exe := r.u.Exe
	if exe == "" {
		dir, err := r.a.dir()
		if err != nil {
			r.failed(err)
			return
		}
		exe = filepath.Join(dir, r.a.exe())
	}
	r.sc.Page = pageRestarting
	r.show()
	if err := r.launch(exe, os.Args[1:], Env+"="+restartPrefix+strconv.Itoa(r.pid)); err != nil {
		r.failed(fmt.Errorf("starting %s: %w", r.sc.Version, err))
		return
	}
	quit := r.u.Quit
	go func() {
		err := quit()
		r.send(func() {
			if err != nil {
				// The new copy says the program is still running, and
				// waits; this window has nothing more to say.
				r.failed(fmt.Errorf("%s didn't close: %w", r.a.Name, err))
				return
			}
			r.leave()
		})
	}()
}

func (r *updater) failed(err error) {
	r.sc.Page, r.sc.Problem = pageFailed, err.Error()
	r.show()
}

// openLink opens the web page at addr, a link in the notes, in the
// browser: https alone.
func openLink(c gunim.Client, addr string) {
	if u, err := url.Parse(addr); err != nil || u.Scheme != "https" {
		return
	}
	go func() { _ = c.Open(addr) }()
}

// restarted is the run of the copy an update started in place of the
// program running as process pid: it shows the restart in the
// installer's window until pid has ended, finishes the update, and
// starts the program as itself. It returns the exit status.
func restarted(a App, self string, pid int) int {
	unlock, ok := lockRestart(self)
	if !ok {
		// Another copy waits for the program already, as when the user
		// asked for the restart twice: it starts the program.
		return 0
	}
	defer unlock()
	alive, done := watchProcess(pid)
	defer done()
	dir, err := a.dir()
	installed := err == nil && samePath(self, filepath.Join(dir, a.exe()))
	r := &restarter{a: a, self: self, installed: installed, events: make(chan func(), 16),
		alive: alive, adopted: make(chan struct{})}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err = gunim.Main(ctx, func(app *gunim.App) error {
		sc := updateScene(&a, pageRestarting, "", a.Version)
		sc.NotesLoading = false
		c, oerr := openStage(app, &a, "Update "+a.Name, 600, sc)
		if oerr != nil {
			return oerr
		}
		r.c, r.sc = c, sc
		return r.serve(ctx)
	})
	if errors.Is(err, driver.ErrNoDriver) {
		// No display: the restart goes on without a word.
		r.quietly(ctx)
	} else if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "install:", err)
	}
	if r.finishing && installed {
		// The window went while the update was being finished: the
		// program starts once it is.
		<-r.adopted
	}
	if r.cancelled || ctx.Err() != nil {
		return 0
	}
	var env []string
	if !installed {
		// A copy that is not installed runs as it is, with no installer.
		env = []string{Env + "=skip"}
	}
	if err := launch(self, os.Args[1:], env...); err != nil {
		fmt.Fprintf(os.Stderr, "install: starting %s: %v\n", self, err)
		return 1
	}
	return 0
}

// restarter is the application half of the window of the copy an
// update started.
type restarter struct {
	a    App
	self string
	// installed says self is the installed program, whose update is
	// finished here.
	installed bool
	c         gunim.Client
	sc        scene
	ctx       context.Context
	events    chan func()
	// finishing says the update is being finished, and cancelled that
	// the user closed the window before the program ended, so the new
	// copy does not start.
	finishing, cancelled bool
	// alive reports whether the program still runs, and adopted closes
	// once the update is finished.
	alive   func() bool
	adopted chan struct{}
}

// updatedFor is how long the window says the program is up to date
// before it closes by itself.
var updatedFor = 1800 * time.Millisecond

func (r *restarter) show() { _ = r.c.Update("installer", r.sc) }

func (r *restarter) send(fn func()) {
	select {
	case r.events <- fn:
	case <-r.ctx.Done():
	}
}

func (r *restarter) running() bool { return r.alive() }

func (r *restarter) serve(ctx context.Context) error {
	r.ctx = ctx
	r.watch()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case fn := <-r.events:
			fn()
		case ev, ok := <-r.c.Intents():
			if !ok {
				return nil
			}
			r.handle(ev.Intent)
		}
	}
}

func (r *restarter) handle(in gunim.Intent) {
	switch in.(type) {
	case closed:
		switch r.sc.Page {
		case pageUpdated:
			r.c.Leave()
		case pageWorking:
			// Half an update is worse than waiting a moment: it closes
			// once the update is finished.
		default:
			r.cancelled = true
			r.c.Leave()
		}
	case quitThem:
		if r.a.Quit == nil || r.sc.Page != pageRunning {
			return
		}
		r.sc.Page, r.sc.Problem = pageRestarting, ""
		r.show()
		go func() {
			err := r.a.Quit(r.ctx)
			if err != nil {
				r.send(func() {
					if r.sc.Page == pageRestarting {
						r.sc.Page, r.sc.Problem = pageRunning, "It didn't close: "+err.Error()
						r.show()
					}
				})
			}
		}()
	}
}

// watch looks for the program to have ended, says so when it is slow
// to, and finishes the update once it has.
func (r *restarter) watch() {
	wait, every := closeWait, watchEvery
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		slow := time.After(wait)
		for {
			select {
			case <-r.ctx.Done():
				return
			case <-slow:
				if r.running() {
					r.send(func() {
						if r.sc.Page == pageRestarting {
							r.sc.Page, r.sc.Running = pageRunning, 1
							r.show()
						}
					})
				}
			case <-t.C:
				if !r.running() {
					r.send(r.finish)
					return
				}
			}
		}
	}()
}

// finish finishes the update, now the program has ended: the rest of
// the release's files and its entries, as on its first start.
func (r *restarter) finish() {
	if r.finishing || r.cancelled {
		return
	}
	r.finishing = true
	if !r.installed {
		r.updated()
		return
	}
	r.sc.Page, r.sc.Step, r.sc.Progress = pageWorking, "Finishing the update", 0
	r.show()
	go func() {
		defer close(r.adopted)
		// Not stopped with the window: half an update is worse than
		// waiting for the rest.
		err := adoptReporting(context.WithoutCancel(r.ctx), r.a, r.self, func(p Progress) {
			r.send(func() {
				r.sc.Progress = p.Done
				r.show()
			})
		})
		if err != nil {
			// The program is in place, and runs; its next start tries
			// again.
			fmt.Fprintf(os.Stderr, "install: finishing the update to %s: %v\n", r.a.Version, err)
		}
		r.send(r.updated)
	}()
}

// updated says the program is up to date, and closes the window after
// a moment, for the program to start.
func (r *restarter) updated() {
	r.sc.Page, r.sc.Progress = pageUpdated, 1
	r.show()
	wait := updatedFor
	go func() {
		select {
		case <-time.After(wait):
			r.send(func() { r.c.Leave() })
		case <-r.ctx.Done():
		}
	}()
}

// quietly waits for the program to end and finishes the update, with no
// window to show it in.
func (r *restarter) quietly(ctx context.Context) {
	for r.running() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(watchEvery):
		}
	}
	if r.installed {
		if err := adoptReporting(ctx, r.a, r.self, nil); err != nil {
			fmt.Fprintf(os.Stderr, "install: finishing the update to %s: %v\n", r.a.Version, err)
		}
	}
}

// lockRestart keeps other copies an update starts for the program at
// self from waiting for it too, and reports false when one does
// already; unlock lets the next one.
func lockRestart(self string) (unlock func(), ok bool) {
	path := self + ".restarting"
	for range 2 {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString(strconv.Itoa(os.Getpid()))
			_ = f.Close()
			return func() { _ = os.Remove(path) }, true
		}
		raw, rerr := os.ReadFile(path)
		if rerr == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid != os.Getpid() {
				held, done := watchProcess(pid)
				busy := held()
				done()
				if busy {
					return nil, false
				}
			}
		}
		// Left by a copy that ended without letting it go.
		_ = os.Remove(path)
	}
	// It cannot be kept: the restart goes on, unguarded.
	return func() {}, true
}

// restartPID is the process an update's new copy replaces, from Env's
// value, and false when the value says no such thing.
func restartPID(env string) (int, bool) {
	v, ok := strings.CutPrefix(env, restartPrefix)
	if !ok {
		return 0, false
	}
	pid, err := strconv.Atoi(v)
	return pid, err == nil && pid > 0
}

// notesBox is the card that holds what an update changed: a line while
// the notes are read, and then the notes, scrolling.
type notesBox struct {
	card *widget.Card
	// of is what the card shows, so it changes only when that does.
	of string
}

func newNotesBox(sc scene) *notesBox {
	b := &notesBox{}
	b.set(sc)
	return b
}

// state is what of sc the box shows.
func notesState(sc scene) string {
	switch {
	case sc.NotesLoading:
		return "loading"
	case sc.NotesErr != "":
		return "err:" + sc.NotesErr
	}
	return "notes:" + sc.Notes
}

func (b *notesBox) set(sc scene) {
	b.of = notesState(sc)
	var in gunim.Node
	switch {
	case sc.NotesLoading:
		in = center(soft("Reading what's new…"))
	case sc.NotesErr != "":
		l := soft("Couldn't read what's new: " + sc.NotesErr)
		l.Color = failInk
		in = l
	case strings.TrimSpace(sc.Notes) == "":
		in = center(soft("The release says nothing of what changed."))
	default:
		in = widget.NewScroll(markdown.New(sc.Notes))
	}
	b.card = widget.NewCard(in)
}

// show shows sc's notes, when they changed.
func (b *notesBox) show(sc scene, u *gunim.UI) {
	if notesState(sc) != b.of {
		old := b.card
		b.set(sc)
		u.Remove(old)
		u.Insert(b, b.card)
	}
}

// Children implements [gunim.Composite].
func (b *notesBox) Children() []gunim.Node { return []gunim.Node{b.card} }

// Layout implements [gunim.Node].
func (b *notesBox) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	k.Layout(gunim.Tight(c.Max))
	k.Place(geom.Point{})
	return c.Max
}

// Paint implements [gunim.Node].
func (b *notesBox) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// updatePage fills p, a page of an update, as sc says.
func updatePage(p *page, sc scene) {
	switch sc.Page {
	case pageUpdate:
		title, sub := "Update "+sc.Name, "From "+sc.Have+" to "+sc.Version
		if sc.Ready {
			title = sc.Name + " " + sc.Version + " is ready"
			if !sc.Restart {
				sub = "It starts the next time " + sc.Name + " does."
			}
		}
		heading(p, title, sub)
		p.notes = newNotesBox(sc)
		p.fill = p.notes
		p.add(20, p.notes)
		later := closeButton("Later")
		if sc.Ready && !sc.Restart {
			p.foot = footer(nil, closeButton("Close"))
			break
		}
		act := widget.NewButton("Update Now")
		if sc.Ready {
			act.Label = "Restart Now"
		}
		act.Kind = widget.ButtonPrimary
		act.OnClick = widget.Sends(updateNow{})
		p.foot = footer(later, act)
	case pageAbout:
		heading(p, sc.Name, versionLine(sc))
		if sc.Description != "" {
			say(p, sc.Description)
		}
		p.notes = newNotesBox(sc)
		p.fill = p.notes
		p.add(20, p.notes)
		status := soft(sc.Status)
		status.Size = smallSize
		status.Align = text.AlignStart
		if sc.Trouble {
			status.Color = failInk
		}
		p.status, p.trouble = status, sc.Trouble
		check := widget.NewButton("Check for Updates")
		check.OnClick = widget.Sends(checkNow{})
		check.Disabled = sc.Checking
		p.check = check
		done := closeButton("Close")
		done.Kind = widget.ButtonPrimary
		p.foot = footer(status, check, done)
	case pageNotes:
		sub := "Version " + sc.Version
		if sc.Have != "" {
			sub = "Updated from " + sc.Have + " to " + sc.Version
		}
		heading(p, "What's new in "+sc.Name, sub)
		p.notes = newNotesBox(sc)
		p.fill = p.notes
		p.add(20, p.notes)
		p.foot = footer(nil, closeButton("Close"))
	case pageRestarting:
		heading(p, "Restarting "+sc.Name+"…", "")
		say(p, "Version "+sc.Version+" opens once "+sc.Name+" has closed.")
		p.foot = footer(cancel())
	case pageUpdated:
		heading(p, sc.Name+" is up to date", "Version "+sc.Version)
		say(p, "It opens in a moment.")
		p.foot = footer(nil, closeButton("Open Now"))
	}
}
