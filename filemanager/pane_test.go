package filemanager

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// slotHost is a program's window with a place for a pane, given the ID
// of its state, and a field of the program's own beside it.
type slotHost struct {
	slot  *slotBox
	other *widget.TextField
}

func (h *slotHost) Children() []gunim.Node { return []gunim.Node{h.other, h.slot} }

func (h *slotHost) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kids.At(0).Layout(gunim.Tight(geom.Sz(c.Max.W, 30)))
	kids.At(0).Place(geom.Point{})
	kids.At(1).Layout(gunim.Tight(geom.Sz(c.Max.W, c.Max.H-30)))
	kids.At(1).Place(geom.Pt(0, 30))
	return c.Max
}

func (h *slotHost) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

// slotBox lays out what is mounted in it over the whole of it.
type slotBox struct{ dialogLayer }

// focusOther is a patch that gives the program's field the keyboard, and
// focusPane one that gives it to the pane's listing.
type (
	focusOther struct{}
	focusPane  struct{}
	// askFocus asks the window what has the keyboard.
	askFocus struct{}
)

// paneScreen is a window of a program that shows a pane.
type paneScreen struct {
	t    *testing.T
	w    *gunim.Window
	host *slotHost
}

// newPaneScreen opens a window whose place for a pane is called slot, and
// hands the intents of its views to the pane pw.
func newPaneScreen(t *testing.T, pw *Window) *paneScreen {
	t.Helper()
	s := &paneScreen{t: t, w: gunim.NewOffscreen(geom.Sz(1100, 700), nil)}
	RegisterViews(s.w)
	gunim.RegisterView(s.w, "host", func(string) *slotHost {
		s.host = &slotHost{slot: &slotBox{}, other: widget.NewTextField()}
		return s.host
	}, func(n *slotHost, id string, u *gunim.UI) {
		if err := u.SetID(n.slot, gunim.ID(id)); err != nil {
			t.Error(err)
		}
	})
	gunim.RegisterPatch(s.w, "host", func(n *slotHost, _ focusOther, u *gunim.UI) { u.Focus(n.other) })
	gunim.RegisterPatch(s.w, "browser", func(b *browser, _ focusPane, u *gunim.UI) { b.focusListing(u) })
	c := s.w.Client()
	if err := c.Mount(gunim.Root, "host", "host", "slot"); err != nil {
		t.Fatal(err)
	}
	s.frames(2)
	go func() {
		for ev := range c.Intents() {
			pw.Deliver(ev)
		}
	}()
	t.Cleanup(c.Close)
	return s
}

func (s *paneScreen) frames(n int) {
	for range n {
		s.w.Frame(time.Second / 60)
	}
}

// browser returns the browser of pane1 in the window, or nil while it
// shows none.
func (s *paneScreen) browser() *browser {
	var got *browser
	gunim.RegisterPatch(s.w, "browser", func(b *browser, _ grab, _ *gunim.UI) { got = b })
	_ = s.w.Client().Patch("pane1/"+string(browserID), grab{})
	s.frames(1)
	return got
}

// until draws frames until cond holds, and fails the test after five
// seconds.
func (s *paneScreen) until(what string, cond func() bool) {
	s.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			s.t.Fatalf("waited five seconds for this: %s", what)
		}
		s.frames(1)
		time.Sleep(2 * time.Millisecond)
	}
}

// focused returns the node with the keyboard.
func (s *paneScreen) focused() gunim.Node {
	var n gunim.Node
	gunim.RegisterPatch(s.w, "host", func(_ *slotHost, _ askFocus, u *gunim.UI) { n = u.Focused() })
	_ = s.w.Client().Patch("host", askFocus{})
	s.frames(1)
	return n
}

// paneWorld is a pane on a folder, and what its host heard of it.
type paneWorld struct {
	dir string
	pw  *Window
	mu  sync.Mutex
	// titles are the folders the host was told the pane shows.
	titles []string
}

func (p *paneWorld) heard() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.titles)
}

func newPaneWorld(t *testing.T, spec ...string) *paneWorld {
	t.Helper()
	return newPaneWorldWith(t, nil, spec...)
}

// newPaneWorldWith is newPaneWorld with the host set changes.
func newPaneWorldWith(t *testing.T, set func(h *PaneHost), spec ...string) *paneWorld {
	t.Helper()
	root := t.TempDir()
	p := &paneWorld{dir: filepath.Join(root, "dir")}
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tree(t, p.dir, spec...)
	ctx, cancel := context.WithCancel(context.Background())
	h := NewHub(ctx, nil)
	host := PaneHost{ID: "pane1", Title: func(_, folder string) {
		p.mu.Lock()
		p.titles = append(p.titles, folder)
		p.mu.Unlock()
	}}
	if set != nil {
		set(&host)
	}
	pw, err := h.NewPane(Options{Dir: p.dir, PrefsPath: filepath.Join(root, "prefs.json"), Poll: -1,
		defaults: func() []Favourite { return nil }, trash: xdgTrash{dir: filepath.Join(root, "Trash")}}, host)
	if err != nil {
		t.Fatal(err)
	}
	p.pw = pw
	t.Cleanup(func() {
		cancel()
		<-pw.Done()
	})
	return p
}

// A pane shows in one window and then in another, as it was: the folder,
// and the way back.
func TestAPaneMovesBetweenWindowsAsItWas(t *testing.T) {
	p := newPaneWorld(t, "sub/", "sub/inner.txt", "a.txt")
	one := newPaneScreen(t, p.pw)
	two := newPaneScreen(t, p.pw)
	p.pw.Attach(one.w.Client(), "slot")
	var b *browser
	one.until("the pane shows in the first window", func() bool {
		b = one.browser()
		return b != nil && b.path.path == p.dir && b.listing.cur != nil
	})
	if !p.pw.Owns("pane1/browser") || p.pw.Owns("host") || p.pw.Owns("pane10/browser") {
		t.Fatal("the pane tells its intents from the window's wrongly")
	}
	sub := filepath.Join(p.dir, "sub")
	p.pw.Deliver(gunim.Envelope{From: "pane1/browser", Intent: Navigate{Path: sub}})
	one.until("the pane goes into sub", func() bool { return b.path.path == sub })
	one.until("the host hears the folder's name", func() bool { return slices.Contains(p.heard(), "sub") })
	if slices.Contains(b.title.cmds[2], CmdThemeDark) {
		t.Fatal("a pane offers to pick the theme")
	}

	p.pw.Attach(two.w.Client(), "slot")
	var b2 *browser
	two.until("the pane shows in the second window", func() bool {
		b2 = two.browser()
		return b2 != nil && b2.path.path == sub && !b2.path.back.Disabled && b2.listing.cur != nil
	})
	one.until("the pane leaves the first window", func() bool { return one.browser() == nil })
	p.pw.Deliver(gunim.Envelope{From: "pane1/browser", Intent: Command{Name: CmdBack}})
	two.until("Back goes back where the pane was in the first window", func() bool { return b2.path.path == p.dir })
}

// A dialog in a pane covers the pane only: the keyboard stays where it is
// in the rest of the window, and F10 opens the pane's menus only from
// inside it.
func TestADialogInAPaneLeavesTheWindowWorking(t *testing.T) {
	p := newPaneWorld(t, "a.txt")
	s := newPaneScreen(t, p.pw)
	p.pw.Attach(s.w.Client(), "slot")
	var b *browser
	s.until("the pane shows", func() bool {
		b = s.browser()
		return b != nil && b.listing.cur != nil
	})
	if err := s.w.Client().Patch("host", focusOther{}); err != nil {
		t.Fatal(err)
	}
	s.frames(2)
	s.w.Input(input.KeyPress{Key: input.KeyF10, Time: time.Now()})
	s.frames(2)
	if b.title.bar.IsOpen() {
		t.Fatal("F10 outside the pane opened its menus")
	}
	p.pw.do(func(a *app) { a.confirm(Confirm{Title: "Sure?", OK: "Yes"}, func() {}) })
	s.until("the pane asks", func() bool {
		var up bool
		ran := make(chan struct{})
		p.pw.do(func(a *app) { up = len(a.ops.dialogs) > 0; close(ran) })
		<-ran
		return up
	})
	s.frames(20)
	if s.focused() != gunim.Node(s.host.other) {
		t.Fatalf("the pane's dialog took the keyboard from the window: %T has it", s.focused())
	}
	s.w.Input(input.TextInput{Text: "x", Time: time.Now()})
	s.frames(2)
	if s.host.other.Text() != "x" {
		t.Fatalf("the window's field took no typing beside the pane's dialog: %q", s.host.other.Text())
	}
	// The pane's own listing is under its dialog: turning to the pane
	// gives the dialog the keyboard.
	if err := s.w.Client().Patch("pane1/browser", focusPane{}); err != nil {
		t.Fatal(err)
	}
	s.frames(2)
	if f := s.focused(); f == gunim.Node(s.host.other) || f == b.listing.cur.focusNode() {
		t.Fatalf("turning to the pane gave %T the keyboard, want its dialog", f)
	}
}

// Ctrl+W in a pane closes it.
func TestAPaneCloses(t *testing.T) {
	p := newPaneWorld(t, "a.txt")
	s := newPaneScreen(t, p.pw)
	p.pw.Attach(s.w.Client(), "slot")
	s.until("the pane shows", func() bool {
		b := s.browser()
		return b != nil && b.listing.cur != nil
	})
	if err := s.w.Client().Patch("pane1/browser", focusPane{}); err != nil {
		t.Fatal(err)
	}
	s.frames(2)
	s.w.Input(input.KeyPress{Key: input.KeyW, Mods: input.ModControl, Time: time.Now()})
	s.frames(2)
	select {
	case <-p.pw.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Ctrl+W left the pane open")
	}
	s.until("the pane leaves the window", func() bool { return s.browser() == nil })
}

// The commands the host's menus offer leave the pane's, and the host
// runs them in the pane.
func TestTheHostsCommandsLeaveThePanesMenus(t *testing.T) {
	hosted := []string{CmdCopy, CmdCut, CmdPaste, CmdSelectAll, CmdNewWindow, CmdCloseApp}
	p := newPaneWorldWith(t, func(h *PaneHost) { h.Commands = hosted }, "a.txt", "b.txt")
	s := newPaneScreen(t, p.pw)
	p.pw.Attach(s.w.Client(), "slot")
	var b *browser
	s.until("the pane shows its rows", func() bool {
		b = s.browser()
		return b != nil && b.listing.cur != nil && b.listing.cur.grid.Rows() == 2
	})
	for _, cmds := range b.title.cmds {
		for _, c := range hosted {
			if slices.Contains(cmds, c) {
				t.Fatalf("the pane's menus still offer %q", c)
			}
		}
	}
	if !slices.Contains(b.title.cmds[1], CmdRename) {
		t.Fatal("the pane's menus lost what the host doesn't offer")
	}
	var ran bool
	gunim.RegisterPatch(s.w, "host", func(_ *slotHost, _ askFocus, u *gunim.UI) { ran = Run(u, "pane1", CmdSelectAll) })
	_ = s.w.Client().Patch("host", askFocus{})
	s.frames(2)
	if !ran || len(b.listing.cur.grid.SelectedRows()) == 0 {
		t.Fatal("Run didn't select all in the pane")
	}
}

// A host that shows the pane's menus gets them as they are, ticks and
// all, and the pane shows no button for them.
func TestAHostShowsThePanesMenus(t *testing.T) {
	p := newPaneWorldWith(t, func(h *PaneHost) {
		h.HostMenus = true
		h.Commands = []string{CmdCopy}
	}, "a.txt")
	s := newPaneScreen(t, p.pw)
	p.pw.Attach(s.w.Client(), "slot")
	var b *browser
	s.until("the pane shows", func() bool {
		b = s.browser()
		return b != nil && b.listing.cur != nil
	})
	var menus []Menu
	var button geom.Rect
	gunim.RegisterPatch(s.w, "host", func(_ *slotHost, _ askFocus, u *gunim.UI) {
		menus = Menus(u, "pane1")
		button, _ = u.Bounds(b.title.bar.Menubar)
	})
	_ = s.w.Client().Patch("host", askFocus{})
	s.frames(2)
	titles := make([]string, 0, len(menus))
	var hidden, copyItem bool
	for _, m := range menus {
		titles = append(titles, m.Title)
		for _, it := range m.Items {
			hidden = hidden || it.Cmd == CmdHidden
			copyItem = copyItem || it.Cmd == CmdCopy
		}
	}
	if !slices.Equal(titles, []string{"File", "Edit", "View", "Go"}) || !hidden || copyItem {
		t.Fatalf("the host was given the menus %v, Show hidden %v, Copy %v", titles, hidden, copyItem)
	}
	if button.Size().W > 0 {
		t.Fatalf("the pane shows its menus' button, at %v, while the host shows the menus", button)
	}
}

// A pane that moves to another window shows the system's icons there
// too, though the new window starts with none of them.
func TestAPaneKeepsItsIconsInAnotherWindow(t *testing.T) {
	fakeIcons(t)
	p := newPaneWorld(t, "sub/", "a.txt")
	one := newPaneScreen(t, p.pw)
	two := newPaneScreen(t, p.pw)
	p.pw.Attach(one.w.Client(), "slot")
	one.until("the icons show in the first window", func() bool {
		b := one.browser()
		return b != nil && b.icons["dir"].Small != nil && b.icons["ext:.txt"].Small != nil
	})
	p.pw.Attach(two.w.Client(), "slot")
	two.until("the icons show in the second window", func() bool {
		b := two.browser()
		return b != nil && b.icons["dir"].Small != nil && b.icons["ext:.txt"].Small != nil
	})
}

// A pane tells its host the whole path of each folder it shows, and its
// status bar shows the host's note on a folder while it shows that
// folder only.
func TestAPaneShowsTheHostsNoteOnItsFolder(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	p := newPaneWorldWith(t, func(h *PaneHost) {
		h.Folder = func(_, path string) {
			mu.Lock()
			paths = append(paths, path)
			mu.Unlock()
		}
	}, "sub/", "a.txt")
	told := func(path string) bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.Contains(paths, path)
	}
	s := newPaneScreen(t, p.pw)
	p.pw.Attach(s.w.Client(), "slot")
	var b *browser
	s.until("the pane shows", func() bool {
		b = s.browser()
		return b != nil && b.path.path == p.dir && b.listing.cur != nil
	})
	s.until("the host hears the folder's path", func() bool { return told(p.dir) })
	fs := LocalFS().ID()
	p.pw.SetNote(fs, p.dir, "Git: main")
	s.until("the note shows", func() bool { return b.status.note.Text == "Git: main" })

	sub := filepath.Join(p.dir, "sub")
	p.pw.Deliver(gunim.Envelope{From: "pane1/browser", Intent: Navigate{Path: sub}})
	s.until("the pane goes into sub", func() bool { return b.path.path == sub && told(sub) })
	s.until("the note goes with the folder", func() bool { return b.status.note.Text == "" })
	// Late, for the folder the user has left.
	p.pw.SetNote(fs, p.dir, "Git: main · 1 changed")
	p.pw.Running() // waits for the serve loop, which has taken the note by then
	s.frames(3)
	if got := b.status.note.Text; got != "" {
		t.Fatalf("a note on the folder left shows in sub: %q", got)
	}
	p.pw.SetNote(fs, sub, "Git: other")
	s.until("the note on sub shows", func() bool { return b.status.note.Text == "Git: other" })
	p.pw.SetNote(fs, sub, "")
	s.until("an empty note takes it away", func() bool { return b.status.note.Text == "" })
}
