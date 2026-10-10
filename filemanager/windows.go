package filemanager

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
)

// Hub is the Files windows of one process, each served by a program half
// of its own, and what they share: the clipboard of paths, the
// favourites, and word of folders an operation changed. A window opens
// another in its hub, as Ctrl and a click on a folder do.
type Hub struct {
	// ctx ends every window of the hub.
	ctx  context.Context
	mu   sync.Mutex
	apps []*app
	// clips holds the clipboard of the windows on each file system, by
	// its ID, as paths of one mean nothing on another, and last the
	// clipboard most recently cut, copied or emptied anywhere, which a
	// window that can transfer pastes from another file system. seq
	// counts the clipboards. favs holds the favourites of each file
	// system other than the computer's own, for the windows whose
	// options keep them nowhere.
	clips map[string]clipboard
	last  clipboard
	seq   int
	favs  map[string]*memFavourites
	// open opens a window with o and serves it, and is nil where no
	// window can open, as in a test.
	open func(o Options) error
	// wg counts the windows Open opened that are still open, and errs
	// holds what they ended with.
	wg   sync.WaitGroup
	errs []error
	ga   *gunim.App
	// copies are the files of other file systems fetched to open with
	// the computer's programs, kept until the hub ends, and watching
	// says the hub looks at them for changes. ids holds the ID of the
	// file system each window shows, for the watch to find one to tell.
	copies   *openCopies
	watching bool
	ids      map[*app]string
}

// NewHub makes a hub whose windows open on ga, and close when ctx ends.
func NewHub(ctx context.Context, ga *gunim.App) *Hub {
	h := &Hub{ctx: ctx, ga: ga}
	// The copies fetched to open go with the hub, those no program
	// holds; a hub that starts takes away what is left a day later.
	context.AfterFunc(ctx, h.removeCopies)
	h.open = func(o Options) error {
		_, err := h.Open(o)
		return err
	}
	return h
}

// Window is a file manager of a hub, in a window of its own or in a pane
// of a window the program draws, which its program can watch, close, and
// turn to another file system.
type Window struct {
	c gunim.Client
	// pane joins a file manager in a pane to its program, and is nil for
	// one in a window of its own.
	pane *paneLink
	// ready closes once the window's program half runs, and done once
	// it has stopped, with err what it stopped with.
	ready, done chan struct{}
	a           *app
	err         error
}

func newWindow(c gunim.Client) *Window {
	return &Window{c: c, ready: make(chan struct{}), done: make(chan struct{})}
}

// Client is the window's client, for what the program does with the
// window itself, such as a screenshot. A pane has none of its own: its
// Client is the zero one.
func (w *Window) Client() gunim.Client { return w.c }

// Done closes once the window has closed.
func (w *Window) Done() <-chan struct{} { return w.done }

// Err is what the window stopped with, once Done has closed.
func (w *Window) Err() error {
	<-w.done
	return w.err
}

// Close closes the window, once the user agrees to stop what is running
// in it.
func (w *Window) Close() {
	w.do(func(a *app) { a.close() })
}

// Stop closes the window, or the pane, at once, stopping what is running
// in it, as a program does that takes a pane away without asking.
func (w *Window) Stop() {
	w.do(func(a *app) {
		for _, r := range a.ops.running {
			r.cancel()
		}
		a.leave()
	})
}

// Show turns the window to the folder dir on fsys, or its home folder
// when dir is empty, as a Visit may want: the clipboard, the places and
// the favourites become those of fsys, and the folder it showed goes
// into the history, so Back returns to it. Operations still running
// carry on where they started.
func (w *Window) Show(fsys FS, dir string) {
	w.do(func(a *app) { a.showFS(fsys, dir) })
}

// Notify shows a notice in the window, as the window shows its own: a
// title, a body, and a kind, success, warning or info, for its icon.
func (w *Window) Notify(title, body, kind string) {
	w.do(func(a *app) { a.patch(Notice{Title: title, Body: body, Kind: kind}) })
}

// SetNote puts text on the status bar while the window shows the folder
// path on the file system with ID fs, as a program says what it knows of
// the folder: where it stands in version control, say. It is not shown
// in another folder, so a note that comes late for a folder the user has
// left is never shown in the next. An empty text takes the note away.
func (w *Window) SetNote(fs, path, text string) {
	w.do(func(a *app) {
		a.note = note{fs: fs, path: path, text: text}
		a.publishStatus()
	})
}

// Running is how many operations the window is running: its own, such
// as a copy, and the transfers it has asked the program for. A program
// asks before it closes the window, as closing stops them. It is zero
// once the window has closed, and when the window takes longer than a
// second to say.
func (w *Window) Running() int {
	got := make(chan int, 1)
	go w.do(func(a *app) { got <- len(a.ops.running) })
	select {
	case n := <-got:
		return n
	case <-w.done:
	case <-time.After(time.Second):
	}
	return 0
}

// do runs fn on the window's serve loop, unless it has stopped.
func (w *Window) do(fn func(a *app)) {
	select {
	case <-w.ready:
	case <-w.done:
		return
	}
	if w.a == nil {
		// It never started.
		return
	}
	w.a.post(func() { fn(w.a) })
}

// Open opens a window with o, and serves it in the background until the
// hub's context ends or the window closes. Wait waits for it to close.
func (h *Hub) Open(o Options) (*Window, error) {
	if h.ga == nil {
		return nil, errors.New("files: the hub has no app to open windows on")
	}
	wo := WindowOptions()
	wo.SystemFrame = o.SystemFrame
	if o.Name != "" {
		wo.Title = o.Name
	}
	gw, err := h.ga.NewWindow(wo)
	if err != nil {
		return nil, fmt.Errorf("files: %w", err)
	}
	RegisterViews(gw)
	w := newWindow(gw.Client())
	h.wg.Go(func() {
		if err := serveWindow(h.ctx, w, o, h); err != nil {
			h.mu.Lock()
			h.errs = append(h.errs, err)
			h.mu.Unlock()
		}
	})
	return w, nil
}

// Serve serves a window the caller opened, on c, until the hub's context
// ends or the window closes. The window must have the views
// RegisterViews registers, and should open with WindowOptions or options
// like them.
func (h *Hub) Serve(c gunim.Client, o Options) error {
	return serveWindow(h.ctx, newWindow(c), o, h)
}

// Wait waits for every window Open opened to close, and returns the
// errors they ended with.
func (h *Hub) Wait() error {
	h.wg.Wait()
	h.removeCopies()
	h.mu.Lock()
	defer h.mu.Unlock()
	return errors.Join(h.errs...)
}

// Serve opens a window on ga with o, and serves it and every window
// opened from it until ctx ends or they have all closed.
func Serve(ctx context.Context, ga *gunim.App, o Options) error {
	h := NewHub(ctx, ga)
	if _, err := h.Open(o); err != nil {
		return err
	}
	return h.Wait()
}

// memFavourites returns the hub's store of the favourites of the file
// system of ID id.
func (h *Hub) memFavourites(id string) *memFavourites {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.favs == nil {
		h.favs = map[string]*memFavourites{}
	}
	m, ok := h.favs[id]
	if !ok {
		m = &memFavourites{}
		h.favs[id] = m
	}
	return m
}

// Refresh has every window of h find its places, read its favourites
// and ask its file system's name again, as when a caller's places,
// favourites or names have changed: a server that connected, say.
func (h *Hub) Refresh() {
	h.mu.Lock()
	list := slices.Clone(h.apps)
	h.mu.Unlock()
	for _, a := range list {
		go a.post(func() {
			a.renameFS()
			a.loadFavourites()
			a.loadPlaces()
		})
	}
}

// joinHub adds a to h, or to a hub of its own when h is nil.
func joinHub(a *app, h *Hub) *Hub {
	if h == nil {
		h = &Hub{}
	}
	h.mu.Lock()
	h.apps = append(h.apps, a)
	if h.ids == nil {
		h.ids = map[*app]string{}
	}
	h.ids[a] = a.fs.ID()
	a.takeClip(h)
	h.mu.Unlock()
	// A change waiting for a window is offered in this one.
	h.watchCopies()
	return h
}

// shows notes that a shows the file system of ID id now.
func (h *Hub) shows(a *app, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.ids[a]; ok {
		h.ids[a] = id
	}
}

// clipboard is the paths cut or copied, and whether they were cut: on
// the file system of ID fs, which writes them as ps. seq tells one
// clipboard from another.
type clipboard struct {
	paths []string
	cut   bool
	fs    string
	ps    PathStyle
	seq   int
}

// leave takes a off h. The changes it had to offer or upload go to
// another window.
func (h *Hub) leave(a *app) {
	h.mu.Lock()
	h.apps = slices.DeleteFunc(h.apps, func(o *app) bool { return o == a })
	delete(h.ids, a)
	c := h.copies
	h.mu.Unlock()
	if c != nil {
		c.releaseApp(a)
	}
}

// others runs fn on the serve loop of each app of h but a.
func (h *Hub) others(a *app, fn func(o *app)) {
	h.mu.Lock()
	list := slices.Clone(h.apps)
	h.mu.Unlock()
	for _, o := range list {
		if o != a {
			go o.post(func() { fn(o) })
		}
	}
}

// neighbours runs fn on the serve loop of each app of h but a that shows
// the file system a does.
func (h *Hub) neighbours(a *app, fn func(o *app)) {
	id := a.fs.ID()
	h.others(a, func(o *app) {
		if o.fs.ID() == id {
			fn(o)
		}
	})
}

// clipChanged shares a's clipboard with the other windows, and tells a's
// window what Paste would paste.
func (a *app) clipChanged() {
	h := a.hub
	h.mu.Lock()
	if h.clips == nil {
		h.clips = map[string]clipboard{}
	}
	h.seq++
	c := clipboard{paths: slices.Clone(a.ops.clip), cut: a.ops.cut, fs: a.fs.ID(), ps: a.ps, seq: h.seq}
	h.clips[c.fs], h.last = c, c
	h.mu.Unlock()
	a.syncClip()
	h.others(a, func(o *app) { o.syncClip() })
}

// clearClip empties the clipboard c, once its items are pasted, wherever
// it is still kept. It reports false when c was kept nowhere any more:
// pasted, or replaced, meanwhile.
func (h *Hub) clearClip(c clipboard) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clips[c.fs].seq != c.seq && h.last.seq != c.seq {
		// Pasted or replaced already, by another window.
		return false
	}
	h.seq++
	empty := clipboard{fs: c.fs, ps: c.ps, seq: h.seq}
	if h.clips[c.fs].seq == c.seq {
		h.clips[c.fs] = empty
	}
	if h.last.seq == c.seq {
		h.last = empty
	}
	return true
}

// syncClip takes what the hub's clipboards say Paste would paste, and
// tells a's window.
func (a *app) syncClip() {
	a.hub.mu.Lock()
	a.takeClip(a.hub)
	a.hub.mu.Unlock()
	a.publishClip()
}

// takeClip takes what Paste would paste from h, whose lock is held: the
// clipboard of a's file system, or the most recent one where it is of
// another and a's program can carry its items across.
func (a *app) takeClip(h *Hub) {
	own := h.clips[a.fs.ID()]
	a.ops.clip, a.ops.cut, a.ops.away = slices.Clone(own.paths), own.cut, clipboard{}
	if a.opts.Transfer != nil && h.last.seq > 0 && h.last.fs != a.fs.ID() {
		a.ops.away = h.last
		a.ops.away.paths = slices.Clone(h.last.paths)
		a.ops.clip, a.ops.cut = nil, false
	}
}

func (a *app) publishClip() {
	if c := a.ops.away; c.seq > 0 {
		a.patch(ClipState{Count: len(c.paths), Cut: c.cut})
		return
	}
	a.patch(ClipState{Count: len(a.ops.clip), Cut: a.ops.cut})
}

func cloneNames(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// touched tells the other windows that j changed folders, so a window
// showing one reads it again at once.
func (a *app) touched(j job) {
	dirs := []string{j.dest}
	for _, s := range j.srcs {
		dirs = append(dirs, a.ps.Dir(s))
	}
	if j.undo != nil {
		for _, s := range j.undo.steps {
			dirs = append(dirs, a.ps.Dir(s.from), a.ps.Dir(s.to))
		}
	}
	a.hub.neighbours(a, func(o *app) {
		if slices.ContainsFunc(dirs, func(d string) bool { return d != "" && o.ps.Same(d, o.nav.path) }) {
			o.relist()
		}
	})
}

// openWindow opens another window on dir, or another pane, where the
// pane's host opens them.
func (a *app) openWindow(dir string) {
	open := a.hub.open
	if a.pane != nil && a.pane.host.Open != nil {
		open = a.pane.host.Open
	}
	if open == nil {
		a.fail("Another window cannot open here.")
		return
	}
	o := a.opts
	o.Dir, o.Select, o.Script = dir, "", ""
	go func() {
		if err := open(o); err != nil {
			a.post(func() { a.fail("Opening a new window: " + err.Error()) })
		}
	}()
}

// WindowOptions are the options a Files window opens with.
func WindowOptions() gunim.WindowOptions {
	return gunim.WindowOptions{
		Title:      "Files",
		Size:       geom.Sz(1180, 740),
		Root:       &root{},
		ZoomKeys:   true,
		Icons:      icons(),
		AskToClose: CloseAsked{},
		// Items drag from a window behind another, as from Explorer's.
		DragFromBehind: true,
	}
}
