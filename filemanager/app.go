package filemanager

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/input"
)

// pollEvery is how often the folder showing is checked for changes.
const pollEvery = 2 * time.Second

// Options set up the program half of one window.
type Options struct {
	// FS is the file system the window shows, and the computer's own,
	// as LocalFS returns it, when nil.
	FS FS
	// SystemFrame gives the window the system's title bar and frame, as
	// gunim.WindowOptions.SystemFrame does, for an application whose
	// user wants the window manager's own.
	SystemFrame bool
	// Dir is the folder the window opens on, and the file system's home
	// folder when empty.
	Dir string
	// PrefsPath is the file the settings are kept in, and
	// gunim-files/prefs.json in the user's configuration folder when
	// empty.
	PrefsPath string
	// Places finds the places the sidebar offers, on a goroutine of its
	// own, as the window opens and each time it gets the keyboard back.
	// When nil, they are those LocalPlaces finds on the computer's own
	// file system, and the home folder and the top on another.
	Places func() ([]Place, error)
	// Favourites keeps the favourites. When nil, they are kept in the
	// settings file on the computer's own file system, and for as long
	// as the window is open on another. An AnyFSFavourites keeps them
	// on any file system, and the window lists them all.
	Favourites FavouriteStore
	// Log, when set, hears each failure and warning the window shows,
	// and each notice, as one line, for a program to keep in a log of its
	// own: what the window says goes once it is dismissed.
	Log func(line string)
	// Visit goes to the folder at path on the file system of ID fs, for
	// a place or a favourite on another file system than the window's,
	// on a goroutine of its own. w is the window that asks, which Visit
	// can turn to the file system with Show, or leave as it is and open
	// another. newWindow says the user asked for the place in a window
	// of its own, as with Ctrl held: the program opens one on fs, and
	// leaves w as it is. Back and Forward to a folder on another file
	// system ask for a Visit too, and the window steps through its
	// history once Show shows that folder. A window whose Visit is nil
	// says it cannot go there.
	Visit func(w *Window, fs, path string, newWindow bool)
	// PlaceMenu adds the program's own items to the context menu of a
	// place, such as Disconnect for a machine. It is called on the
	// window's serve loop as the menu opens, so it must be quick.
	PlaceMenu func(p Place) []PlaceItem
	// PlaceCommand does what the program's item id of place p asks, on a
	// goroutine of its own. w is the window the menu opened in.
	PlaceCommand func(w *Window, p Place, id string)
	// ItemActions are items of the program's own on the context menu of
	// the items selected, such as View in a reader of its own, and
	// ItemAction does what one asks, on a goroutine of its own: id is the
	// action's, w the window the menu opened in, and paths the items, on
	// the file system of ID fs.
	ItemActions []ItemAction
	ItemAction  func(w *Window, fs string, paths []string, id string)
	// Transfer copies or moves items between file systems, as a drop or a
	// paste asks, as one of the window's operations: its progress and a
	// way to stop it show with the window's own, and it asks about names
	// that clash through the window. It returns once the items are
	// across, or why not; ctx ends when the user stops it. w is the window
	// they go to. When nil, items cannot go between file systems.
	//
	// Each Transfer runs on a goroutine of its own, and the window does
	// not close until it returns, so it should return soon once ctx ends.
	Transfer func(ctx context.Context, w *Window, t Transfer, p *TransferProgress) error
	// FSName names the file system of ID fs, as the window's title says
	// first: the machine it is on, say. When nil, or where it returns "",
	// the title names the folder and the program only. The window asks
	// on its own goroutine, as it opens, as it turns to another file
	// system and when its hub refreshes, so FSName must be quick.
	FSName func(fs string) string
	// Name is what the window's title calls the program, and Files when
	// empty.
	Name string
	// Poll is how often the folder showing is checked for changes: every
	// two seconds when zero, and never when below zero.
	Poll time.Duration
	// Select is a name to select once the first folder is read, and
	// Script the steps of a script to run after that, split by commas,
	// such as copy,into:Documents,paste. Script is for demonstrations
	// and tests; see runScript for the steps.
	Select, Script string

	// trash stands in for the file system's trash, for tests.
	trash Trasher
	// defaults stands in for DefaultFavourites, for tests.
	defaults func() []Favourite
}

// Transfer is items going from one file system to another, which the
// window cannot do itself: copied, or moved when Move is set, or put
// into a new zip file when Zip is set. A drop or a paste of items from
// several folders makes one Transfer for each, and each runs as an
// operation of its own; a paste as a zip makes one Transfer of all.
type Transfer struct {
	// FromFS is the ID of the file system the items are on, and Paths
	// the items, all in one folder.
	FromFS string
	Paths  []string
	// ToFS is the ID of the file system they go to, and Into the folder
	// there they go into.
	ToFS string
	Into string
	Move bool
	// Zip, when set, is the name of a new zip file in Into that the
	// items go into, with all that folders among them hold: ZipFiles
	// makes it. Nothing may be at its path already.
	Zip string
}

// app is the application half. Everything on it runs on the serve loop;
// work done elsewhere comes back through done.
type app struct {
	ctx context.Context
	// c is where the window half shows, and parent the ID it is mounted
	// under there. ids makes the IDs of its views.
	c      screen
	parent gunim.ID
	ids    viewIDs
	// pane joins a file manager in a pane to its program, and is nil for
	// one in a window of its own. quit closes once a pane closes.
	pane *paneLink
	quit chan struct{}
	// iconsSent are the icons Windows shows that the window was sent, or
	// asked for.
	iconsSent map[string]bool
	// mods are the modifier keys held as the intent being handled was sent.
	mods input.Mods
	done chan func()
	// fs is the file system the window shows, ps how it writes paths,
	// and trash its trash, or nil where it has none.
	fs    FS
	ps    PathStyle
	trash Trasher
	// stopped closes once the serve loop has stopped reading done.
	stopped chan struct{}

	prefs     prefs
	prefsPath string
	// prefsErr is set when the settings could not be read, so they are
	// not written over.
	prefsErr error
	shell    Shell

	nav     navState
	ops     opsState
	preview previewState
	thumbs  thumbState
	viewer  viewerState
	reading readingState
	search  searchState
	places  []Place
	favs    []Favourite
	banner  int
	// bannerText is what the banner says until it is dismissed, for a
	// window a pane comes to, and held the notices said while a pane was
	// in none.
	bannerText string
	held       []Notice
	// closeAsked says the user is being asked whether to stop what runs
	// and close.
	closeAsked bool
	// title is the file system and the folder the pane's host was last told of,
	// and folder the file system and the path.
	title, folder string
	// note is what the program said of a folder for the status bar,
	// shown while that folder is.
	note note
	// script is what is left of the steps to run, once the first folder
	// is read.
	script    []string
	scripting bool
	handlers  []handler
	// opts are the options the window opened with, which a window it
	// opens takes too.
	opts Options
	// hub is the windows of the process, win the handle of this one,
	// and dnd what drops need.
	hub *Hub
	win *Window
	// placesGen counts the lookups of the places, so an older one that
	// answers late is dropped.
	placesGen int
	dnd       dndState
	// uploads holds the copies changed on this computer that the window
	// offers to upload, by the key of the notice that offers.
	uploads map[string]copyKey
}

// handler is one area's share of the intents: it reports whether it
// took in.
type handler func(in gunim.Intent) bool

// serveWindow runs the application half of window w, in hub h, until ctx
// ends or the window closes.
func serveWindow(ctx context.Context, w *Window, o Options, h *Hub) (err error) {
	defer func() {
		w.err = err
		close(w.done)
	}()
	c := w.c
	a, err := launch(ctx, c, o, h)
	if err != nil {
		close(w.ready)
		return err
	}
	a.win, w.a = w, a
	close(w.ready)
	return a.serve(ctx, o, c.Intents())
}

// serve runs the serve loop on the intents of in until ctx ends, in
// closes or the user closes a pane.
func (a *app) serve(ctx context.Context, o Options, in <-chan gunim.Envelope) error {
	handlers := a.handlers
	var tick <-chan time.Time
	if o.Poll >= 0 {
		every := o.Poll
		if every == 0 {
			every = pollEvery
		}
		t := time.NewTicker(every)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-ctx.Done():
			a.stopAll()
			return nil
		case <-a.quit:
			a.detach()
			a.stopAll()
			return nil
		case fn := <-a.done:
			fn()
		case <-tick:
			a.poll()
		case ev, ok := <-in:
			if !ok {
				a.stopAll()
				return a.c.c.Err()
			}
			a.take(handlers, ev)
		}
	}
}

// launch makes the application half and fills the window. A nil h puts
// the window in a hub of its own.
func launch(ctx context.Context, c gunim.Client, o Options, h *Hub) (*app, error) {
	a, err := newApp(ctx, screen{c: c, on: true}, o)
	if err != nil {
		return nil, err
	}
	a.parent = gunim.Root
	if err := c.Mount(a.parent, a.ids.browser(), "browser", a.shell); err != nil {
		return nil, err
	}
	a.join(h, newWindow(c))
	return a, nil
}

// join puts a in hub h, or in a hub of its own when h is nil, as the
// file manager of w, and starts it.
func (a *app) join(h *Hub, w *Window) {
	a.hub = joinHub(a, h)
	a.win = w
	w.a = a
	a.publishClip()
	a.handlers = []handler{a.handleDnd, a.handleNav, a.handleOps, a.handlePreview, a.handlePlaces, a.handleShell,
		a.handleIcons, a.handleViewer, a.handleReading, a.handleSearch, a.handleTheme}
	a.startup(a.opts)
}

// newApp makes the application half, with the settings read.
func newApp(ctx context.Context, c screen, o Options) (*app, error) {
	if o.FS == nil {
		o.FS = LocalFS()
	}
	tr := o.trash
	if tr == nil {
		tr, _ = o.FS.(Trasher)
	}
	var err error
	a := &app{ctx: ctx, c: c, done: make(chan func(), 256), stopped: make(chan struct{}), fs: o.FS, ps: o.FS.Paths(),
		trash: tr, prefsPath: o.PrefsPath, opts: o}
	a.nav.init()
	a.ops.init()
	a.thumbs.init()
	if a.prefsPath == "" {
		if a.prefsPath, err = defaultPrefsPath(); err != nil {
			return nil, err
		}
	}
	a.prefs, a.prefsErr = loadPrefs(a.prefsPath)
	a.iconsSent = map[string]bool{}
	a.shell = Shell{Light: a.prefs.Light, ShowHidden: a.prefs.ShowHidden, ShowPreview: !a.prefs.HidePreview,
		SystemIcons: a.systemIconsOn(),
		Sidebar:     a.prefs.Sidebar, FS: a.fs.ID(), Paths: a.ps, NoTrash: tr == nil,
		Transfers: o.Transfer != nil, PlaceMenu: o.PlaceMenu != nil, Actions: o.ItemActions, Name: o.Name, UploadEdited: a.prefs.UploadEdited}
	a.shell.Where, a.shell.Fetches = a.where(), a.fetches()
	a.shell.OpenWith = a.openWithOn()
	a.nav.sort, a.nav.desc = a.prefs.Sort, a.prefs.Desc
	return a, nil
}

// startup fills the window once the browser is mounted.
func (a *app) startup(o Options) {
	if a.prefsErr != nil {
		a.fail(a.prefsErr.Error() + " Settings will not be saved until the file is fixed or removed.")
	}
	if a.prefs.Zoom > 0 && a.pane == nil {
		a.send(a.c.SetZoom(a.prefs.Zoom))
	}
	a.loadFavourites()
	a.loadPlaces()
	if o.Script != "" {
		a.script = strings.Split(o.Script, ",")
	}
	a.startNav(o.Dir)
	if o.Select != "" {
		a.nav.pick = []string{o.Select}
	}
}

// take handles the intent ev carries, with the modifier keys held as it was sent.
func (a *app) take(handlers []handler, ev gunim.Envelope) {
	a.mods = ev.Mods
	defer func() { a.mods = 0 }()
	a.handle(handlers, ev.Intent)
}

func (a *app) handle(handlers []handler, in gunim.Intent) {
	for _, h := range handlers {
		if h(in) {
			return
		}
	}
	switch v := in.(type) {
	case gunim.CommandFailed:
		log.Printf("command %s failed on %q%q: %s", v.Command, v.ID, v.Key, v.Reason)
	case gunim.Zoomed:
		if a.pane == nil {
			a.savePrefs(func(p *prefs) { p.Zoom = v.Zoom })
		}
	}
}

// handleShell takes the intents about the window as a whole.
func (a *app) handleShell(in gunim.Intent) bool {
	switch v := in.(type) {
	case Command:
		switch v.Name {
		case CmdTheme:
			a.shell.Light = !a.shell.Light
			light := a.shell.Light
			a.savePrefs(func(p *prefs) { p.Light = light })
		case CmdPreview:
			a.shell.ShowPreview = !a.shell.ShowPreview
			hide := !a.shell.ShowPreview
			a.savePrefs(func(p *prefs) { p.HidePreview = hide })
		case CmdCloseApp:
			a.close()
			return true
		case CmdUploadAsk, CmdUploadAlways, CmdUploadNever:
			a.setUploadEdited(strings.TrimPrefix(v.Name, "upload."))
			return true
		default:
			return false
		}
		a.publishShell()
		return true
	case CloseAsked:
		a.close()
		return true
	case BannerDismissed:
		if v.Seq == a.banner {
			a.bannerText = ""
		}
		return true
	case SidebarMoved:
		a.shell.Sidebar = v.Width
		a.savePrefs(func(p *prefs) { p.Sidebar = v.Width })
		return true
	}
	return false
}

// close closes the window, once the user agrees to stop what is running.
func (a *app) close() {
	n := len(a.ops.running)
	if n == 0 {
		a.leave()
		return
	}
	if a.closeAsked {
		// Asked already, and waiting for the answer.
		return
	}
	a.closeAsked = true
	a.ask(Confirm{Title: "Stop " + plural(n, "operation") + " and close?",
		Body: "What is running stops where it has got to. Anything half copied is taken away.", OK: "Stop and close"},
		func(v Confirmed) {
			a.closeAsked = false
			if !v.OK {
				return
			}
			for _, r := range a.ops.running {
				r.cancel()
			}
			a.leave()
		})
}

// leave closes the window, or a pane, at once.
func (a *app) leave() {
	if a.pane != nil {
		select {
		case <-a.quit:
		default:
			close(a.quit)
		}
		return
	}
	a.c.Leave()
}

// where names the file system the window shows, for its title, or is
// empty.
func (a *app) where() string {
	if a.opts.FSName == nil {
		return ""
	}
	return a.opts.FSName(a.fs.ID())
}

// renameFS takes a new name of the file system, as FSName gives it now.
func (a *app) renameFS() {
	if w := a.where(); w != a.shell.Where {
		a.shell.Where = w
		a.publishShell()
	}
}

func (a *app) publishShell() { a.send(a.c.Update(a.ids.browser(), a.shell)) }

// post runs fn on the serve loop, unless the app has stopped.
func (a *app) post(fn func()) {
	select {
	case a.done <- fn:
	case <-a.ctx.Done():
	case <-a.stopped:
	}
}

// patch sends a patch to the browser.
func (a *app) patch(v any) {
	a.logShown(v)
	if n, ok := v.(Notice); ok && !a.c.on && a.pane != nil && len(a.held) < 8 {
		// Said once the pane shows.
		a.held = append(a.held, n)
	}
	a.send(a.c.Patch(string(a.ids.browser()), v))
}

// logShown tells Options.Log of a failure or a warning v shows, and of
// every notice.
func (a *app) logShown(v any) {
	switch v := v.(type) {
	case Banner:
		if v.Text != "" {
			a.logLine(v.Text)
		}
	case Notice:
		a.logLine(v.Title, v.Body)
	case ErrorBox:
		a.logLine(v.Title, v.Body)
	case Preview:
		if v.Err != "" {
			a.logLine("Previewing "+v.Shown, v.Err)
		}
	case Counted:
		if v.Err != "" {
			a.logLine("Counting what a folder holds", v.Err)
		}
	case PropsCounted:
		if v.Err != "" {
			a.logLine("Counting the items for Properties", v.Err)
		}
	}
}

// logLine tells Options.Log of a failure: what failed, then why, when
// it says something.
func (a *app) logLine(what string, why ...string) {
	if a.opts.Log == nil {
		return
	}
	parts := []string{"files: " + what}
	for _, w := range why {
		if w != "" {
			parts = append(parts, w)
		}
	}
	a.opts.Log(strings.Join(parts, ": "))
}

// send logs a command the window did not take. A window that has closed
// takes nothing, and needs no word about it.
func (a *app) send(err error) {
	if err != nil && !errors.Is(err, gunim.ErrWindowClosed) {
		log.Printf("sending to the window: %v", err)
	}
}

// fail shows msg in the banner under the path bar.
func (a *app) fail(msg string) {
	a.banner++
	a.bannerText = msg
	a.patch(Banner{Seq: a.banner, Text: msg})
}

// savePrefs makes change to the settings, and writes it to the settings
// file, unless the file could not be read. Several windows can keep their
// settings in the one file, so it reads the file again and makes only
// change to it, leaving what another window wrote there.
func (a *app) savePrefs(change func(p *prefs)) {
	change(&a.prefs)
	if a.prefsErr != nil {
		return
	}
	if err := updatePrefs(a.prefsPath, change); err != nil {
		a.fail(err.Error())
	}
}

// stopAll cancels what is running, and waits for the operations to stop,
// so none is left writing once the app has gone.
func (a *app) stopAll() {
	for _, r := range a.ops.running {
		r.cancel()
	}
	a.nav.stop()
	a.preview.stop()
	a.viewer.stop()
	a.search.stop()
	a.hub.leave(a)
	close(a.stopped)
	a.ops.wg.Wait()
}
