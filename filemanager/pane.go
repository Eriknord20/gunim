package filemanager

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/marrasen/gunim"
)

// PaneHost is what a file manager in a pane asks of the program it lives
// in, as [Hub.NewPane] makes one. The program draws the windows the pane
// is shown in, puts the pane where it likes in them, and moves it
// between them with [Window.Attach] and [Window.Detach].
type PaneHost struct {
	// ID starts the IDs the pane's views are mounted as, followed by a
	// slash, under the ID Attach is given, so the intents sent from them
	// are the pane's; [Window.Owns] tells them. It must differ from the
	// ID of every other pane a window shows, and the program's own views
	// must not start with it and a slash.
	ID string
	// Title hears the ID of the file system the pane shows and the name
	// of the folder, each time either changes, for the program to show
	// where it names its panes. It is called on the pane's serve loop,
	// so it must be quick, and must not wait for the program.
	Title func(fs, folder string)
	// Folder hears the ID of the file system the pane shows and the
	// whole path of the folder, each time either changes, for the
	// program to say something of it with [Window.SetNote]. It is called
	// on the pane's serve loop, so it must be quick, and must not wait
	// for the program.
	Folder func(fs, path string)
	// Commands are the file manager's commands the program's own menus
	// offer for the pane, as CmdCopy and CmdPaste in an Edit menu of its
	// own, which run them with [Run]. The pane's menus leave them out.
	Commands []string
	// HostMenus says the program shows the pane's menus in its own, as
	// [Menus] gives them, so the pane shows no button of its own for
	// them, and F10 and Alt are the program's.
	HostMenus bool
	// Open opens another file manager with o, as New window and a click
	// with Ctrl held on a place ask: in a pane of its own, say. When nil,
	// they open in windows of their own, on the hub's app.
	Open func(o Options) error
}

// NewPane starts a file manager with o that shows in a pane of a window
// the program draws, not in a window of its own. It serves it in the
// background until the hub's context ends or the user closes it, which
// Done tells; it shows nothing until Attach puts it in a window.
//
// In a pane, the file manager leaves the window to the program. It sets
// no title, theme or zoom of the window: its colours are the tokens of
// the theme round it, so the program sets the files.* tokens in its own
// themes for it to fit in. Its keys work only while the keyboard is in
// it, and its dialogs cover only the pane, so the rest of the window
// works meanwhile; the picture viewer still fills the window. The
// program hands it the intents of its views with Deliver.
func (h *Hub) NewPane(o Options, host PaneHost) (*Window, error) {
	if host.ID == "" {
		return nil, errors.New("files: a pane needs an ID")
	}
	w := newWindow(gunim.Client{})
	w.pane = &paneLink{host: host, in: make(chan gunim.Envelope, 256)}
	h.wg.Go(func() {
		if err := servePane(h.ctx, w, o, h); err != nil {
			h.mu.Lock()
			h.errs = append(h.errs, err)
			h.mu.Unlock()
		}
	})
	return w, nil
}

// paneLink is what joins a pane to its program: the host, and the intents
// handed to it.
type paneLink struct {
	host PaneHost
	in   chan gunim.Envelope
}

// Attach shows the pane in the window of c, mounted under parent, which
// is the node the program gives its place in the window with
// [gunim.UI.SetID]. A pane shown elsewhere leaves there first. The pane
// sends the window all it shows: the folder, the selection, the places,
// the operations running, and the dialog asking, if one is.
func (w *Window) Attach(c gunim.Client, parent gunim.ID) {
	if w.pane == nil {
		return
	}
	w.do(func(a *app) { a.attach(c, parent) })
}

// Detach takes the pane out of the window it shows in, as before Attach
// puts it in another. It carries on meanwhile: what runs goes on, and the
// folder, the history and the selection stay as they are.
func (w *Window) Detach() {
	if w.pane == nil {
		return
	}
	w.do(func(a *app) { a.detach() })
}

// Owns reports whether an intent sent from the view of ID from is the
// pane's: one of its own. It is false for a file manager in a window of its own.
func (w *Window) Owns(from gunim.ID) bool {
	return w.pane != nil && strings.HasPrefix(string(from), w.pane.host.ID+"/")
}

// FocusIn returns the node that takes the keyboard in the pane whose
// host's ID is id, in the window of u, or nil while the pane shows in no
// window there: its listing. A program that moves the keyboard from pane
// to pane focuses it. It must be called on the UI goroutine.
func FocusIn(u *gunim.UI, id string) gunim.Node {
	b, ok := u.Mounted(gunim.ID(id + "/" + string(browserID))).(*browser)
	if !ok || b.listing.cur == nil {
		return nil
	}
	return b.listing.cur.focusNode()
}

// Run runs the command cmd in the pane whose host's ID is id, in the
// window of u, as a pick of it in the pane's menus does, and reports
// whether the pane shows there: for the program's own menu items, as
// its Edit › Copy. It must be called on the UI goroutine.
func Run(u *gunim.UI, id, cmd string) bool {
	b, ok := u.Mounted(gunim.ID(id + "/" + string(browserID))).(*browser)
	if !ok {
		return false
	}
	b.title.run(cmd, u)
	return true
}

// Menu is one of a pane's menus, as [Menus] gives them.
type Menu struct {
	Title string
	Items []MenuItem
}

// MenuItem is a line of a menu: what it says, the keys that run it, the
// command [Run] runs for it, whether it is ticked, and whether a line
// goes above it.
type MenuItem struct {
	Label, Hint, Cmd string
	Checked, Line    bool
}

// Menus returns the menus of the pane whose host's ID is id, in the
// window of u, as they are now, with their ticks and less the commands
// the host's menus offer, or nil while the pane shows in no window
// there. A program that shows them in its own menus asks again as its
// menus open, as the ticks follow what the pane shows. It must be called
// on the UI goroutine.
func Menus(u *gunim.UI, id string) []Menu {
	b, ok := u.Mounted(gunim.ID(id + "/" + string(browserID))).(*browser)
	if !ok {
		return nil
	}
	t := b.title
	out := make([]Menu, 0, len(t.bar.Menus))
	for m, bm := range t.bar.Menus {
		menu := Menu{Title: bm.Title}
		for i, line := range bm.Items {
			menu.Items = append(menu.Items, MenuItem{Label: line.Label, Hint: line.Hint, Cmd: t.cmds[m][i],
				Checked: line.Checked, Line: line.Break && i > 0})
		}
		out = append(out, menu)
	}
	return out
}

// Focus gives the keyboard to the listing, as the program does when the
// user turns to the file manager: to its pane, say.
func (w *Window) Focus() {
	w.do(func(a *app) { a.patch(FocusListing{}) })
}

// Deliver hands the pane an intent of the window it shows in, and reports
// whether it was the pane's, as Owns tells. A command that failed on one
// of the pane's views is the pane's too, wherever it came from.
func (w *Window) Deliver(ev gunim.Envelope) bool {
	if w.pane == nil {
		return false
	}
	owns := w.Owns(ev.From)
	if f, ok := ev.Intent.(gunim.CommandFailed); ok {
		owns = owns || w.Owns(f.ID) || f.Key != "" && w.Owns(gunim.ID(f.Key))
	}
	if !owns {
		return false
	}
	select {
	case w.pane.in <- ev:
	case <-w.done:
	}
	return true
}

// servePane runs the application half of pane w, in hub h, until ctx
// ends or the user closes it.
func servePane(ctx context.Context, w *Window, o Options, h *Hub) (err error) {
	defer func() {
		w.err = err
		close(w.done)
	}()
	a, err := newApp(ctx, screen{}, o)
	if err != nil {
		close(w.ready)
		return err
	}
	a.pane = w.pane
	a.quit = make(chan struct{})
	a.ids = viewIDs{prefix: w.pane.host.ID + "/"}
	a.shell.Pane = true
	a.shell.Hosted = slices.Clone(w.pane.host.Commands)
	a.shell.HostMenus = w.pane.host.HostMenus
	a.join(h, w)
	close(w.ready)
	return a.serve(ctx, o, w.pane.in)
}

// attach shows the pane in the window of c, under parent.
func (a *app) attach(c gunim.Client, parent gunim.ID) {
	if a.c.on {
		a.detach()
	}
	a.c = screen{c: c, on: true}
	a.parent = parent
	// The new window has none of the icons, and builds the browser afresh.
	clear(a.iconsSent)
	a.send(a.c.Mount(parent, a.ids.browser(), "browser", a.shell))
	a.republish()
	// So those of the folder showing are sent again: the next listing
	// may be a long way off, and its rows draw none until then.
	a.keyIcons(a.nav.path, a.nav.all)
}

// detach takes the pane out of its window. The dialog asking stays
// asked, and shows again once the pane is attached; the picture viewer
// closes.
func (a *app) detach() {
	if !a.c.on {
		return
	}
	a.closeViewer()
	a.send(a.c.Unmount(a.ids.browser()))
	a.c = screen{}
}

// republish sends a window all the browser shows, as it is now.
func (a *app) republish() {
	a.publishClip()
	a.publishPlaces()
	if d := &a.dnd; len(d.vols) > 0 {
		a.patch(Volumes{Of: cloneNames(d.vols), Errs: cloneNames(d.volErrs)})
	}
	a.publishListing()
	a.publishBands()
	a.publishSelection()
	a.publishView()
	a.publishStatus()
	a.publishOps()
	// Shown again, not told again: the log heard them as they came.
	key := string(a.ids.browser())
	if a.bannerText != "" {
		a.send(a.c.Patch(key, Banner{Seq: a.banner, Text: a.bannerText}))
	}
	for _, n := range a.held {
		a.send(a.c.Patch(key, n))
	}
	a.held = nil
	a.preview.subject = ""
	a.showPreview()
	if len(a.ops.dialogs) > 0 {
		a.mountDialog(a.ops.dialogs[0])
	}
}

// screen is where the application half shows: the client of a window, or
// nothing, while a pane is in none. Its commands do nothing then.
type screen struct {
	c  gunim.Client
	on bool
}

// Mount mounts a view, as [gunim.Client.Mount] does.
func (s screen) Mount(parent, id gunim.ID, view string, state any, watch ...string) error {
	if !s.on {
		return nil
	}
	return s.c.Mount(parent, id, view, state, watch...)
}

// Update updates a view, as [gunim.Client.Update] does.
func (s screen) Update(id gunim.ID, state any) error {
	if !s.on {
		return nil
	}
	return s.c.Update(id, state)
}

// Patch patches the views watching key, as [gunim.Client.Patch] does.
func (s screen) Patch(key string, p any) error {
	if !s.on {
		return nil
	}
	return s.c.Patch(key, p)
}

// Unmount takes a view away, as [gunim.Client.Unmount] does.
func (s screen) Unmount(id gunim.ID) error {
	if !s.on {
		return nil
	}
	return s.c.Unmount(id)
}

// Focus moves the keyboard, as [gunim.Client.Focus] does.
func (s screen) Focus(id gunim.ID) error {
	if !s.on {
		return nil
	}
	return s.c.Focus(id)
}

// SetZoom zooms the window, as [gunim.Client.SetZoom] does.
func (s screen) SetZoom(z float32) error {
	if !s.on {
		return nil
	}
	return s.c.SetZoom(z)
}

// errHidden says a pane in no window cannot open or reveal a file.
var errHidden = errors.New("the file manager shows in no window")

// Open opens a file with its program, as [gunim.Client.Open] does.
func (s screen) Open(path string) error {
	if !s.on {
		return errHidden
	}
	return s.c.Open(path)
}

// OpenLink opens a web address in the browser, as
// [gunim.Client.OpenLink] does.
func (s screen) OpenLink(url string) error {
	if !s.on {
		return nil
	}
	return s.c.OpenLink(url)
}

// Reveal shows a file in the system's file manager, as [gunim.Client.Reveal] does.
func (s screen) Reveal(path string) error {
	if !s.on {
		return errHidden
	}
	return s.c.Reveal(path)
}

// Leave closes the window, as [gunim.Client.Leave] does.
func (s screen) Leave() {
	if s.on {
		s.c.Leave()
	}
}

// viewIDs makes the IDs of the views of one file manager: those of a
// window of its own, or those of a pane, which start with its host's ID.
type viewIDs struct{ prefix string }

// browser is the ID of the view that holds the whole file manager.
func (v viewIDs) browser() gunim.ID { return gunim.ID(v.prefix) + browserID }

// dialog is the ID of the nth dialog.
func (v viewIDs) dialog(n int) gunim.ID {
	return gunim.ID(v.prefix) + dialogID + gunim.ID("-"+strconv.Itoa(n))
}

// reading is the ID of the nth text viewer.
func (v viewIDs) reading(n int) gunim.ID { return gunim.ID(v.prefix + "reading-" + strconv.Itoa(n)) }

// viewer is the ID of the nth picture viewer.
func (v viewIDs) viewer(n int) gunim.ID { return gunim.ID(v.prefix + "viewer-" + strconv.Itoa(n)) }
